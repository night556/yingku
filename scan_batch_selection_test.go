package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAIChineseSeriesAllSeasonsSelectedAcrossThreeBatches(t *testing.T) {
	a := testApp(t)
	files := []ScannedFile{}
	assignments := map[int]aiFileAssignment{}
	for season, count := range []int{40, 43, 40, 42} {
		for episode := 1; episode <= count; episode++ {
			id := len(files) + 1
			relative := fmt.Sprintf("[铁CTY晓岚%d][%d集][古装轻喜剧]/%02d.mkv", season+1, count, episode)
			files = append(files, ScannedFile{ID: id, Key: "/" + relative, Relative: relative, Entry: Entry{Name: fmt.Sprintf("%02d.mkv", episode)}})
			assignments[id] = aiFileAssignment{ID: id, Season: season + 1, Episode: episode}
		}
	}
	calls := 0
	a.aiClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		var input struct {
			Files []aiManifestFile `json:"files"`
		}
		if err := json.Unmarshal([]byte(payload.Messages[1].Content), &input); err != nil {
			t.Fatal(err)
		}
		batch := []aiFileAssignment{}
		for _, f := range input.Files {
			batch = append(batch, assignments[f.ID])
		}
		calls++
		return fakeJSON(aiTestResponse(aiAssignment{Groups: []aiGroup{{Type: "tv", Title: "铁齿铜牙纪晓岚", Confidence: .95, Files: batch}}})), nil
	})}
	plan, err := a.recognizeWithAI(context.Background(), AIConfig{Endpoint: "https://provider.test/v1", Model: "model"}, "铁齿铜牙纪晓岚", files, nil)
	if err != nil || calls != 3 || len(plan.Groups) != 1 || len(plan.Groups[0].Files) != 165 || !plan.Groups[0].Selected {
		t.Fatalf("incomplete Chinese series plan: calls=%d err=%v", calls, err)
	}
	for _, f := range plan.Groups[0].Files {
		want := assignments[f.ID]
		if !f.Selected || f.Season != want.Season || f.Episode != want.Episode {
			t.Fatalf("file %d lost selection or episode identity: %+v", f.ID, f)
		}
	}
	if os.Getenv("YINGKU_AI_BATCH_PREVIEW") == "1" {
		root := t.TempDir()
		for _, f := range files {
			filename := filepath.Join(root, filepath.FromSlash(f.Relative))
			if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filename, []byte("synthetic"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		a.s.Sources = []Source{{ID: "batch-test", Name: "跨批次测试来源（模拟）", Type: "local", Root: root}}
		a.s.Config.AI = AIConfig{Enabled: true, Endpoint: "https://provider.test/v1", Model: "mock-model"}
		a.s.Config.TMDBSecret, _ = a.encrypt("synthetic-tmdb-key")
		a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return fakeJSON(`{"results":[{"id":123,"name":"铁齿铜牙纪晓岚","first_air_date":"2001-01-08","overview":"模拟资料，用于验证扫描界面。"}]}`), nil
		})}
		server := httptest.NewServer(a.handler())
		defer server.Close()
		a.listenAddress = strings.TrimPrefix(server.URL, "http://")
		defer a.closeNetwork()
		fmt.Printf("BATCH_QA=%s\n", server.URL)
		<-time.After(20 * time.Minute)
	}
}

func TestMergedAILowConfidenceGroupStillRequiresReview(t *testing.T) {
	plan := mergeScanGroups([]ScanGroup{
		{Type: "tv", Title: "Show", Confidence: .95, Files: []PlannedFile{{ID: 1, Season: 1, Episode: 1}}},
		{Type: "tv", Title: "Show", Confidence: .5, Files: []PlannedFile{{ID: 2, Season: 2, Episode: 1}}},
	})
	if len(plan) != 1 || plan[0].Selected || !plan[0].Files[0].Selected || !plan[0].Files[1].Selected {
		t.Fatal("file selection and uncertain work selection were conflated")
	}
}
