package main

import (
	"context"
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

func TestScanManualEpisodeHints(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		season, episode, end int
	}{
		{"/07.4k.mp4", -1, 7, 0}, {"/08.4k.mp4", -1, 8, 0}, {"/Season 02/09.4k.mp4", 2, 9, 0},
		{"/Season 02/Show.S03E10-E11.mkv", 3, 10, 11}, {"/Season 00/EP01.mp4", 0, 1, 0},
		{"/第二季/第十二集.mp4", 2, 12, 0}, {"/1080p.mp4", -1, 0, 0}, {"/花絮.mp4", 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hint := scanEpisodeHint(ScannedFile{Key: tc.name, Entry: Entry{Name: filepath.Base(tc.name)}})
			season := -1
			if hint.Season != nil {
				season = *hint.Season
			}
			if season != tc.season || hint.Episode != tc.episode || hint.EpisodeEnd != tc.end {
				t.Fatalf("unexpected hints: season=%d hint=%+v", season, hint)
			}
		})
	}
}

func TestManualScanBulkImportReusesOneConfirmedWork(t *testing.T) {
	a := testApp(t)
	root := t.TempDir()
	files := []ScannedFile{}
	for i := 7; i <= 41; i++ {
		name := fmt.Sprintf("%02d.4k.mp4", i)
		if err := os.WriteFile(filepath.Join(root, name), []byte("synthetic video"), 0600); err != nil {
			t.Fatal(err)
		}
		files = append(files, ScannedFile{ID: i, Key: "/" + name, Relative: name, Entry: Entry{Name: name}})
	}
	a.s.Sources = []Source{{ID: "bulk-local", Type: "local", Root: root, Name: "批量归类测试来源"}}
	a.scans = map[string]*ScanSession{"bulk": {ID: "bulk", SourceID: "bulk-local", SourceType: "local", Status: "ready", Files: files, Plan: ScanPlan{Groups: []ScanGroup{}, Unassigned: []UnassignedFile{}}, run: "bulk-run"}}
	a.s.Config.TMDBSecret, _ = a.encrypt("fixture-key")
	var searches atomic.Int32
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/search/tv") {
			searches.Add(1)
			return fakeJSON(`{"results":[{"id":321,"name":"批量归类测试剧","first_air_date":"2020-01-01","overview":"测试数据"}]}`), nil
		}
		return fakeJSON(`{"id":321,"name":"批量归类测试剧","first_air_date":"2020-01-01","seasons":[]}`), nil
	})}
	candidates, err := a.searchScanMatches(context.Background(), "bulk", scanMatchQuery{GroupID: "manual-7", Type: "tv", Query: "批量归类测试剧"})
	if err != nil || len(candidates) != 1 {
		t.Fatal("missing match", err)
	}
	c := candidates[0]
	group := ScanGroup{ID: "manual-7", Type: "tv", Title: c.Title, Year: c.Year, MatchToken: c.Token, Selected: true, Files: []PlannedFile{}}
	for _, file := range files {
		group.Files = append(group.Files, PlannedFile{ID: file.ID, Key: file.Key, Season: 1, Episode: file.ID, Selected: true})
	}
	s, _ := a.scanSnapshot("bulk")
	if s.Files[0].EpisodeHint == nil || s.Files[0].EpisodeHint.Episode != 7 || a.scans["bulk"].Files[0].EpisodeHint != nil {
		t.Fatal("snapshot hints missing or mutated live scan")
	}
	previews, err := a.checkScanPlan(context.Background(), s, ScanImportRequest{Mode: "reference", Groups: []ScanGroup{group}})
	if err != nil || planHasIssues(previews) || len(previews) != 1 || len(previews[0].Preview.Rows) != 35 {
		t.Fatalf("bulk preview failed: %+v %v", previews, err)
	}
	if err := a.startScanImport("bulk", ScanImportRequest{Mode: "reference", Groups: []ScanGroup{group}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool { snap, _ := a.scanSnapshot("bulk"); return snap.Status == "complete" })
	a.mu.Lock()
	itemCount, fileCount, taskCount := len(a.s.Items), len(a.s.Files), len(a.s.Tasks)
	a.mu.Unlock()
	if itemCount != 1 || fileCount != 35 || taskCount != 0 || searches.Load() != 1 {
		t.Fatalf("work should be matched once: items=%d files=%d tasks=%d searches=%d", itemCount, fileCount, taskCount, searches.Load())
	}
}

func TestManualScanInteractivePreview(t *testing.T) {
	if os.Getenv("YINGKU_SCAN_MANUAL_PREVIEW") != "1" {
		t.Skip("interactive scan fixture is opt-in")
	}
	a := testApp(t)
	root := t.TempDir()
	for i := 7; i <= 41; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("%02d.4k.mp4", i)), []byte("synthetic video"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a.s.Sources = []Source{{ID: "manual-fixture", Type: "local", Root: root, Name: "待确认剧集（模拟数据）"}}
	a.s.Config.AI = AIConfig{Enabled: true, Endpoint: "https://provider.test/v1", Model: "mock-model"}
	a.aiClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		result := aiAssignment{}
		for id := 1; id <= 35; id++ {
			result.Unassigned = append(result.Unassigned, struct {
				ID     int    `json:"id"`
				Reason string `json:"reason"`
			}{ID: id, Reason: "仅有序号，缺少作品名；可手动归入同一部剧。"})
		}
		return fakeJSON(aiTestResponse(result)), nil
	})}
	a.s.Config.TMDBSecret, _ = a.encrypt("fixture-key")
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/search/tv") {
			return fakeJSON(`{"results":[{"id":321,"name":"批量归类测试剧","first_air_date":"2020-01-01","overview":"这是独立测试用的模拟作品。确认一次后，其余集数追加到同一部剧。"}]}`), nil
		}
		return fakeJSON(`{"id":321,"name":"批量归类测试剧","first_air_date":"2020-01-01","seasons":[]}`), nil
	})}
	server := httptest.NewServer(a.handler())
	defer server.Close()
	fmt.Printf("MANUAL_SCAN_QA=%s\n", server.URL)
	<-time.After(20 * time.Minute)
}
