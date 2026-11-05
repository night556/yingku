package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func aiTestResponse(value any) string {
	content, _ := json.Marshal(value)
	body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(content)}, "finish_reason": "stop"}}, "usage": map[string]int{"total_tokens": 100}})
	return string(body)
}
func aiTestFiles(count int) []ScannedFile {
	files := []ScannedFile{}
	for i := 1; i <= count; i++ {
		name := fmt.Sprintf("Show.S01E%02d.mkv", i)
		files = append(files, ScannedFile{ID: i, Key: "/Show/Season 01/" + name, Relative: "Season 01/" + name, Entry: Entry{Name: name}})
	}
	return files
}

func TestAIEndpointNormalizationAndTransportRules(t *testing.T) {
	for _, tc := range []struct{ input, want string }{{"https://example.test", "https://example.test/v1/chat/completions"}, {"https://example.test/custom/v1/", "https://example.test/custom/v1/chat/completions"}, {"https://example.test/v1/chat/completions", "https://example.test/v1/chat/completions"}, {"http://127.0.0.1:11434/v1", "http://127.0.0.1:11434/v1/chat/completions"}, {"http://192.168.1.2:1234/v1", "http://192.168.1.2:1234/v1/chat/completions"}} {
		got, err := aiEndpoint(tc.input)
		if err != nil || got != tc.want {
			t.Fatalf("endpoint %q: %q %v", tc.input, got, err)
		}
	}
	for _, bad := range []string{"file:///private", "https://user:password@example.test/v1", "https://example.test/v1?key=secret", "http://example.test/v1"} {
		if _, err := aiEndpoint(bad); err == nil {
			t.Fatalf("unsafe endpoint accepted: %s", bad)
		}
	}
}

func TestAIConfigEncryptionAndCredentialHostChange(t *testing.T) {
	a := testApp(t)
	server := httptest.NewServer(a.handler())
	defer server.Close()
	request(t, server, a, "POST", "/api/config/ai", AISettingsInput{Enabled: true, Endpoint: "https://first.test/v1", Model: "user-model", Key: "not-a-real-test-key"}, 200)
	if a.s.Config.AI.Secret == "not-a-real-test-key" || a.s.Config.AI.Secret == "" {
		t.Fatal("key not encrypted")
	}
	state, _ := json.Marshal(a.publicState())
	saved, _ := os.ReadFile(filepath.Join(a.dir, "state.json"))
	if bytes.Contains(state, []byte("not-a-real-test-key")) || bytes.Contains(saved, []byte("not-a-real-test-key")) || bytes.Contains(state, []byte(a.s.Config.AI.Secret)) {
		t.Fatal("AI secret leaked to public state or disk")
	}
	request(t, server, a, "POST", "/api/config/ai", AISettingsInput{Enabled: true, Endpoint: "https://first.test/v1", Model: "second-model"}, 200)
	if a.s.Config.AI.Secret == "" {
		t.Fatal("blank key did not retain saved key")
	}
	request(t, server, a, "POST", "/api/config/ai", AISettingsInput{Enabled: true, Endpoint: "https://second.test/v1", Model: "second-model"}, 200)
	if a.s.Config.AI.Secret != "" {
		t.Fatal("old provider credential forwarded to new host")
	}
	secret, _ := a.encrypt("old-host-key")
	next, err := a.nextAIConfig(AIConfig{Endpoint: "https://first.test/v1", Secret: secret}, AISettingsInput{Enabled: true, Endpoint: "https://second.test/v1", Model: "model", Key: "  "})
	if err != nil || next.Secret != "" {
		t.Fatal("whitespace key forwarded saved credential to a new host")
	}
}

func TestAICompatibleJSONFallbackAndRelativePathsOnly(t *testing.T) {
	a := testApp(t)
	secret, _ := a.encrypt("not-a-real-key")
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer not-a-real-key" {
			t.Error("wrong compatible request")
		}
		raw, _ := io.ReadAll(r.Body)
		if bytes.Contains(raw, []byte(`E:\private`)) || bytes.Contains(raw, []byte("not-a-real-key")) || bytes.Contains(raw, []byte("destination")) {
			t.Error("private filesystem or credential sent to model")
		}
		var payload struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
			ResponseFormat json.RawMessage `json:"response_format"`
		}
		if json.Unmarshal(raw, &payload) != nil || payload.Model != "user-model" || len(payload.Messages) != 2 {
			t.Error("bad request payload")
		}
		if calls.Add(1) == 1 {
			if len(payload.ResponseFormat) == 0 {
				t.Error("JSON mode not requested")
			}
			w.WriteHeader(400)
			io.WriteString(w, `{"error":{"message":"response_format unsupported"}}`)
			return
		}
		if len(payload.ResponseFormat) > 0 {
			t.Error("compatibility fallback retained unsupported format")
		}
		io.WriteString(w, aiTestResponse(aiAssignment{Groups: []aiGroup{{Type: "tv", Title: "Show", Confidence: 0.9, Files: []aiFileAssignment{{ID: 1, Season: 1, Episode: 1}}}}}))
	}))
	defer provider.Close()
	plan, err := a.recognizeWithAI(context.Background(), AIConfig{Endpoint: provider.URL + "/v1", Model: "user-model", Secret: secret}, "Show", aiTestFiles(1), nil)
	if err != nil || calls.Load() != 2 || len(plan.Groups) != 1 || !plan.Groups[0].Selected || plan.Groups[0].Files[0].Episode != 1 {
		t.Fatalf("compatible AI failed: %+v %v", plan, err)
	}
}

func TestAIValidatesOutputIDsRangesAndOmissions(t *testing.T) {
	cases := []struct {
		name       string
		assignment aiAssignment
		wantError  bool
		unassigned int
	}{
		{"unknown ID", aiAssignment{Groups: []aiGroup{{Type: "tv", Title: "Show", Confidence: .9, Files: []aiFileAssignment{{ID: 999, Season: 1, Episode: 1}}}}}, true, 0},
		{"duplicate ID", aiAssignment{Groups: []aiGroup{{Type: "tv", Title: "Show", Confidence: .9, Files: []aiFileAssignment{{ID: 1, Season: 1, Episode: 1}, {ID: 1, Season: 1, Episode: 2}}}}}, true, 0},
		{"invalid range", aiAssignment{Groups: []aiGroup{{Type: "tv", Title: "Show", Confidence: .9, Files: []aiFileAssignment{{ID: 1, Season: 1, Episode: 5, EpisodeEnd: 2}}}}}, true, 0},
		{"movie contains multiple files", aiAssignment{Groups: []aiGroup{{Type: "movie", Title: "Movie", Confidence: .9, Files: []aiFileAssignment{{ID: 1}, {ID: 2}}}}}, true, 0},
		{"omitted file retained", aiAssignment{Groups: []aiGroup{{Type: "tv", Title: "Show", Confidence: .9, Files: []aiFileAssignment{{ID: 1, Season: 1, Episode: 1}}}}}, false, 1},
		{"uncertain result not auto-selected", aiAssignment{Groups: []aiGroup{{Type: "tv", Title: "Show", Confidence: .2, Files: []aiFileAssignment{{ID: 1, Season: 1, Episode: 1}, {ID: 2, Season: 1, Episode: 2}}}}}, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			a.aiClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return fakeJSON(aiTestResponse(tc.assignment)), nil })}
			plan, err := a.recognizeWithAI(context.Background(), AIConfig{Endpoint: "https://provider.test/v1", Model: "model"}, "Show", aiTestFiles(2), nil)
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v plan=%+v", err, plan)
			}
			if !tc.wantError && len(plan.Unassigned) != tc.unassigned {
				t.Fatal("missing files silently dropped")
			}
			if tc.name == "uncertain result not auto-selected" && plan.Groups[0].Selected {
				t.Fatal("uncertain group selected by default")
			}
		})
	}
}

func TestAIBatchingMergesSeasonsAndTracksUsage(t *testing.T) {
	a := testApp(t)
	var calls atomic.Int32
	a.aiClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&payload)
		var input struct {
			Files []aiManifestFile `json:"files"`
		}
		json.Unmarshal([]byte(payload.Messages[1].Content), &input)
		if len(input.Files) > aiBatchSize {
			t.Error("batch limit exceeded")
		}
		files := []aiFileAssignment{}
		for _, f := range input.Files {
			files = append(files, aiFileAssignment{ID: f.ID, Season: 1, Episode: f.ID})
		}
		calls.Add(1)
		return fakeJSON(aiTestResponse(aiAssignment{Groups: []aiGroup{{Type: "tv", Title: "Show", Confidence: .9, Files: files}}})), nil
	})}
	processed, tokens := 0, 0
	plan, err := a.recognizeWithAI(context.Background(), AIConfig{Endpoint: "https://provider.test/v1", Model: "model"}, "Show", aiTestFiles(aiBatchSize+3), func(done int, usage aiUsage) { processed = done; tokens += usage.Total })
	if err != nil || calls.Load() != 2 || len(plan.Groups) != 1 || len(plan.Groups[0].Files) != aiBatchSize+3 || processed != aiBatchSize+3 || tokens != 200 {
		t.Fatalf("batch plan incomplete: %+v %v", plan, err)
	}
	for _, file := range plan.Groups[0].Files {
		if !file.Selected {
			t.Fatalf("AI batch merge left file %d unchecked", file.ID)
		}
	}
}

func TestAIRedirectDoesNotForwardAuthorization(t *testing.T) {
	a := testApp(t)
	var received atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1) }))
	defer other.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer provider.Close()
	secret, _ := a.encrypt("test-secret")
	_, _, err := a.callRecognitionAI(context.Background(), AIConfig{Endpoint: provider.URL, Model: "model", Secret: secret}, "Show", []aiManifestFile{{ID: 1, Path: "S01E01.mkv"}})
	if err == nil || received.Load() != 0 {
		t.Fatal("credential-bearing request followed redirect")
	}
}

func scanFixture(t *testing.T) (*App, string) {
	t.Helper()
	a := testApp(t)
	root := t.TempDir()
	names := []string{"犯罪心理/Season 01/犯罪心理.S01E01.mkv", "犯罪心理/第二季/犯罪心理.S02E01-E02.mkv", "神探夏洛克/Season 01/神探夏洛克.S01E01.mp4", "电影/流浪地球.2019.mp4", "待整理/Season 01/EPxx.mkv"}
	for _, name := range names {
		file := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("local video"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a.s.Sources = []Source{{ID: "local", Name: "Collection", Type: "local", Root: root}}
	id, err := a.startScan("local", "/", "local")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool { s, _ := a.scanSnapshot(id); return s.Status == "ready" || s.Status == "failed" })
	s, _ := a.scanSnapshot(id)
	if s.Status != "ready" {
		t.Fatal(s.Error)
	}
	return a, id
}

func TestRecursiveScanGroupsWorksAndImportsWithoutCopying(t *testing.T) {
	a, id := scanFixture(t)
	s, _ := a.scanSnapshot(id)
	if s.VideoCount != 5 || len(s.Plan.Groups) != 3 || len(s.Plan.Unassigned) != 1 {
		t.Fatalf("wrong grouping: %+v", s.Plan)
	}
	var drama ScanGroup
	for _, g := range s.Plan.Groups {
		if g.Title == "犯罪心理" {
			drama = g
		}
	}
	if len(drama.Files) != 2 || drama.Files[1].Season != 2 || drama.Files[1].EpisodeEnd != 2 {
		t.Fatal("different seasons or merged episodes not recognized")
	}
	for i := range s.Plan.Groups {
		s.Plan.Groups[i].Manual = true
	}
	body := ScanImportRequest{Mode: "reference", Groups: s.Plan.Groups}
	previews, err := a.checkScanPlan(context.Background(), s, body)
	if err != nil || planHasIssues(previews) {
		t.Fatalf("plan preflight failed: %+v %v", previews, err)
	}
	if err = a.startScanImport(id, body); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool { s, _ := a.scanSnapshot(id); return !scanBusy(s.Status) })
	s, _ = a.scanSnapshot(id)
	if s.Status != "complete" || len(a.s.Items) != 3 || len(a.s.Files) != 4 || len(a.s.Tasks) != 0 {
		t.Fatalf("batch import failed: %+v", s.Results)
	}
	for _, f := range a.s.Files {
		if !f.Reference || f.Status != "completed" {
			t.Fatal("local batch copied or downloaded video")
		}
		if data, _ := os.ReadFile(f.Destination); string(data) != "local video" {
			t.Fatal("original video changed")
		}
	}
}

func TestScanPlanRejectsForeignFilesAndChangedSource(t *testing.T) {
	a, id := scanFixture(t)
	s, _ := a.scanSnapshot(id)
	body := ScanImportRequest{Mode: "reference", Groups: []ScanGroup{s.Plan.Groups[0]}}
	body.Groups[0].Files = append([]PlannedFile{}, body.Groups[0].Files...)
	body.Groups[0].Files[0].Key = "/not-in-scan.mp4"
	if _, err := validateScanEdits(s, body); err == nil {
		t.Fatal("file outside manifest accepted")
	}
	body.Groups = s.Plan.Groups
	a.s.Sources[0].Root = t.TempDir()
	if _, err := a.checkScanPlan(context.Background(), s, body); err == nil {
		t.Fatal("changed source scope not detected")
	}
}

func TestBatchChecksEveryGroupBeforeAnyImport(t *testing.T) {
	a, id := scanFixture(t)
	s, _ := a.scanSnapshot(id)
	groups := s.Plan.Groups
	for i := range groups {
		groups[i].Manual = true
	}
	groups[0].Title = ""
	if err := a.startScanImport(id, ScanImportRequest{Mode: "reference", Groups: groups}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool { s, _ := a.scanSnapshot(id); return !scanBusy(s.Status) })
	s, _ = a.scanSnapshot(id)
	if s.Status != "ready" || s.Error == "" || len(a.s.Items) != 0 || len(a.s.Files) != 0 {
		t.Fatal("invalid group caused partial import before preflight")
	}
}

func TestEpisodeRangesOverlapAndManualOverride(t *testing.T) {
	for _, name := range []string{"Show.S02E01-E02.mkv", "Show.S02E01E02.mkv", "Show.第二季.第1-2集.mkv"} {
		s, e, end := guessEpisodeRange(name)
		if s != 2 || e != 1 || end != 2 {
			t.Fatalf("range not parsed: %s => %d/%d-%d", name, s, e, end)
		}
	}
	a, id := scanFixture(t)
	s, _ := a.scanSnapshot(id)
	var group ScanGroup
	for _, g := range s.Plan.Groups {
		if g.Title == "犯罪心理" {
			group = g
		}
	}
	r := scanImportBody("local", group, ScanImportRequest{Mode: "reference"})
	if _, err := a.importMedia(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	root := a.s.Sources[0].Root
	if err := os.WriteFile(filepath.Join(root, "extra.S02E02.mkv"), []byte("other version"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := a.preview(context.Background(), ImportRequest{SourceID: "local", Type: "tv", Title: "犯罪心理", Files: []Selection{{Key: "/extra.S02E02.mkv"}}})
	if err != nil || p.Rows[0].Issue == "" {
		t.Fatal("merged episode overlap not detected")
	}
	zero := 0
	p, err = a.preview(context.Background(), ImportRequest{SourceID: "local", Type: "tv", Title: "Other", Files: []Selection{{Key: group.Files[1].Key, EpisodeEnd: &zero}}})
	if err != nil || p.Rows[0].EpisodeEnd != 0 {
		t.Fatal("explicitly clearing episode range ignored")
	}
}

func TestCancelAIKeepsLocalRecognitionResults(t *testing.T) {
	a, id := scanFixture(t)
	started := make(chan struct{}, 1)
	a.s.Config.AI = AIConfig{Enabled: true, Endpoint: "https://provider.test/v1", Model: "model"}
	a.aiClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		started <- struct{}{}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	if err := a.reidentifyScan(id, "ai"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("AI request did not start")
	}
	a.mu.Lock()
	a.scans[id].cancel()
	a.scans[id].Status = "cancelled"
	a.mu.Unlock()
	s, _ := a.scanSnapshot(id)
	if s.Plan.Method != "local" || len(s.Plan.Groups) != 3 {
		t.Fatal("cancellation discarded original recognition")
	}
}

func TestAIProviderErrorDoesNotLeakKey(t *testing.T) {
	a := testApp(t)
	a.aiClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		resp := fakeJSON(`{"error":{"message":"reflected-secret-key"}}`)
		resp.StatusCode = 401
		return resp, nil
	})}
	_, _, err := a.callRecognitionAI(context.Background(), AIConfig{Endpoint: "https://provider.test/v1", Model: "model"}, "Show", []aiManifestFile{{ID: 1, Path: "a.mkv"}})
	if err == nil || strings.Contains(err.Error(), "reflected-secret-key") {
		t.Fatal("raw provider error leaked")
	}
}

func TestFrontendModuleImportsAreServed(t *testing.T) {
	a := testApp(t)
	r := httptest.NewRequest("GET", "http://localhost/app.js", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "from './scan.js'") {
		t.Fatal("scanner module not referenced by app")
	}
	for _, path := range []string{"/cloud-auth.js", "/scan-matching.js", "/scan.js", "/casting.js", "/network.js", "/login.js", "/cinema.js"} {
		w = httptest.NewRecorder()
		r = httptest.NewRequest("GET", "http://localhost"+path, nil)
		r.RemoteAddr = "127.0.0.1:1234"
		a.handler().ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "javascript") {
			t.Fatalf("browser cannot load %s", path)
		}
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "http://localhost/cinema.css", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	a.handler().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "text/css") {
		t.Fatal("browser cannot load media view styles")
	}
}
