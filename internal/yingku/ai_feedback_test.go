package yingku

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAIFeedbackEveryBatchHasInstructionsAndGlobalOrder(t *testing.T) {
	a := testApp(t)
	var calls atomic.Int32
	note := "这些是《纠正后的剧名》，首播年份 2020；按文件名排序，从第 7 集开始连续编号，不要拆成多个作品。"
	a.aiClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Messages) != 2 || payload.Messages[0].Role != "system" || !strings.Contains(payload.Messages[0].Content, "instructions") {
			t.Fatal("missing instruction context")
		}
		var input struct {
			Folder, Instructions string
			Files                []aiManifestFile
		}
		if err := json.Unmarshal([]byte(payload.Messages[1].Content), &input); err != nil {
			t.Fatal(err)
		}
		if input.Instructions != note {
			t.Fatal("instructions omitted from a batch")
		}
		batch := calls.Add(1)
		result := aiAssignment{Groups: []aiGroup{{Type: "tv", Title: "纠正后的剧名", Year: "2020", Confidence: .95}}}
		for index, f := range input.Files {
			want := int(batch-1)*aiBatchSize + index + 1
			if f.Order != want {
				t.Fatalf("global order reset across batches: got=%d want=%d", f.Order, want)
			}
			if strings.HasPrefix(f.Path, "/") || strings.Contains(f.Path, "private") {
				t.Fatal("absolute source path leaked")
			}
			result.Groups[0].Files = append(result.Groups[0].Files, aiFileAssignment{ID: f.ID, Season: 1, Episode: f.Order + 6})
		}
		return fakeJSON(aiTestResponse(result)), nil
	})}
	plan, err := a.recognizeWithAI(context.Background(), AIConfig{Endpoint: "https://provider.test/v1", Model: "mock-model"}, "未知剧名", aiTestFiles(aiBatchSize+3), nil, "  "+note+"  ")
	if err != nil || calls.Load() != 2 || len(plan.Groups) != 1 || len(plan.Groups[0].Files) != 83 {
		t.Fatal("feedback recognition failed", err, plan)
	}
	if plan.Groups[0].Year != "2020" || plan.Groups[0].Files[0].Episode != 7 || plan.Groups[0].Files[82].Episode != 89 {
		t.Fatal("user correction or global numbering lost", plan.Groups[0])
	}
}

func TestAIFeedbackRejectsOversizeWithoutChangingScan(t *testing.T) {
	a, id := scanFixture(t)
	before, _ := a.scanSnapshot(id)
	a.s.Config.AI.Enabled = true
	var calls atomic.Int32
	a.aiClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { calls.Add(1); return fakeJSON(`{}`), nil })}
	if err := a.reidentifyScan(id, "ai", strings.Repeat("剧", 2001)); err == nil {
		t.Fatal("oversized instructions accepted")
	}
	after, _ := a.scanSnapshot(id)
	if after.run != before.run || after.Status != before.Status || after.AIInstructions != "" || calls.Load() != 0 {
		t.Fatal("rejected instructions mutated the scan")
	}
	if note, err := normalizeAIInstructions(strings.Repeat("剧", 2000)); err != nil || len([]rune(note)) != 2000 {
		t.Fatal("valid Chinese limit rejected", err)
	}
}

func TestAIFeedbackFailureRetainsPlanAndConfirmedMatches(t *testing.T) {
	a, id := scanFixture(t)
	before, _ := a.scanSnapshot(id)
	g := before.Plan.Groups[0]
	candidate := ScanMatchCandidate{Token: "kept-token", Type: g.Type, Title: g.Title, Year: g.Year, Provider: "tmdb", ExternalID: "42"}
	a.scans[id].matches = map[string]scanMatchRecord{candidate.Token: {GroupID: g.ID, Type: g.Type, Candidate: candidate}}
	a.s.Config.AI = AIConfig{Enabled: true, Endpoint: "https://provider.test/v1", Model: "mock-model"}
	a.aiClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return fakeJSON(`{"choices":[]}`), nil })}
	server := httptest.NewServer(a.handler())
	defer server.Close()
	note := "只用于当前扫描的人工纠正说明"
	request(t, server, a, "POST", "/api/scans/"+id+"/recognize", map[string]any{"method": "ai", "instructions": "  " + note + "  "}, 202)
	waitFor(t, 3*time.Second, func() bool { s, _ := a.scanSnapshot(id); return s.Status == "ready" && s.Error != "" })
	after, _ := a.scanSnapshot(id)
	oldPlan, _ := json.Marshal(before.Plan)
	newPlan, _ := json.Marshal(after.Plan)
	if !bytes.Equal(oldPlan, newPlan) || after.AIInstructions != note {
		t.Fatal("failed AI request lost original plan or correction")
	}
	g.MatchToken = candidate.Token
	if _, err := resolvedScanImport(after, g, ScanImportRequest{Mode: "reference"}); err != nil {
		t.Fatal("failed AI request invalidated the old confirmed match", err)
	}
	public, _ := json.Marshal(a.publicState())
	if bytes.Contains(public, []byte(note)) {
		t.Fatal("per-scan instructions leaked into global state")
	}
	// A successful replacement invalidates old confirmations and produces a new plan.
	a.aiClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return fakeJSON(aiTestResponse(aiAssignment{Groups: []aiGroup{{Type: "tv", Title: "纠正后的作品", Confidence: .9, Files: []aiFileAssignment{{ID: before.Files[0].ID, Season: 1, Episode: 1}}}}})), nil
	})}
	if err := a.reidentifyScan(id, "ai", note); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool { s, _ := a.scanSnapshot(id); return s.Status == "ready" && s.Error == "" })
	final, _ := a.scanSnapshot(id)
	if len(final.matches) != 0 || len(final.Plan.Groups) != 1 || final.Plan.Groups[0].Title != "纠正后的作品" {
		t.Fatal("new result reused stale matches", final.Plan)
	}
}

func TestAIFeedbackInteractivePreview(t *testing.T) {
	if os.Getenv("YINGKU_AI_FEEDBACK_PREVIEW") != "1" {
		t.Skip("interactive AI feedback fixture is opt-in")
	}
	a := testApp(t)
	root := t.TempDir()
	for _, name := range []string{"01.4k.mp4", "02.4k.mp4", "03.4k.mp4", "彩蛋/彩蛋01.mp4"} {
		dest := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dest, []byte("synthetic video"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a.s.Sources = []Source{{ID: "feedback-fixture", Type: "local", Root: root, Name: "AI 纠正识别（模拟数据）"}}
	a.s.Config.AI = AIConfig{Enabled: true, Endpoint: "https://provider.test/v1", Model: "mock-model"}
	a.aiClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&payload)
		var input struct {
			Instructions string
			Files        []aiManifestFile
		}
		json.Unmarshal([]byte(payload.Messages[1].Content), &input)
		if strings.Contains(input.Instructions, "模拟失败") {
			return fakeJSON(`{"choices":[]}`), nil
		}
		group := aiGroup{Type: "tv", Title: "错误示例剧", Confidence: .95}
		if input.Instructions != "" {
			group.Title = "示例纠正剧"
			group.Year = "2001"
		}
		for _, f := range input.Files {
			season, episode := 1, f.Order
			if input.Instructions != "" && strings.Contains(f.Path, "彩蛋") {
				season, episode = 0, 1
			}
			group.Files = append(group.Files, aiFileAssignment{ID: f.ID, Season: season, Episode: episode})
		}
		return fakeJSON(aiTestResponse(aiAssignment{Groups: []aiGroup{group}})), nil
	})}
	a.s.Config.TMDBSecret, _ = a.encrypt("fixture-key")
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/search/tv") {
			query := r.URL.Query().Get("query")
			data, _ := json.Marshal(map[string]any{"results": []map[string]any{{"id": 321, "name": query, "first_air_date": "2001-01-01", "overview": "这是模拟作品。你可以用补充说明纠正作品名和特别篇归属。"}}})
			return fakeJSON(string(data)), nil
		}
		return fakeJSON(`{"id":321,"name":"示例纠正剧","first_air_date":"2001-01-01","seasons":[]}`), nil
	})}
	server := httptest.NewServer(a.handler())
	defer server.Close()
	fmt.Printf("AI_FEEDBACK_QA=%s\n", server.URL)
	<-time.After(20 * time.Minute)
}
