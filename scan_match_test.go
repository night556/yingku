package main

import (
	"context"
	"encoding/json"
	"errors"
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

func scanMovie(t *testing.T, a *App, id string) (ScanSession, ScanGroup) {
	t.Helper()
	s, _ := a.scanSnapshot(id)
	for _, g := range s.Plan.Groups {
		if g.Type == "movie" {
			return s, g
		}
	}
	t.Fatal("missing movie group")
	return s, ScanGroup{}
}

func mockScanMetadata(t *testing.T, a *App) {
	t.Helper()
	a.s.Config.TMDBSecret, _ = a.encrypt("fixture-key")
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/search/movie") {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"results":[{"id":42,"title":"流浪地球","release_date":"2020-01-01"},{"id":41,"title":"流浪地球","release_date":"2019-02-05","poster_path":"/fixture.jpg","overview":"测试资料简介"}]}`)), Header: http.Header{}}, nil
		}
		return nil, errors.New("fixture detail service unavailable")
	})}
}

func TestScanMatchRequiresExplicitChoiceAndRejectsStaleIdentities(t *testing.T) {
	a, id := scanFixture(t)
	s, g := scanMovie(t, a, id)
	body := ScanImportRequest{Mode: "reference", Groups: []ScanGroup{g}}
	previews, err := a.checkScanPlan(context.Background(), s, body)
	if err != nil || !planHasIssues(previews) || len(a.s.Items) != 0 {
		t.Fatal("filename-only guess accepted as confirmed metadata", err)
	}
	mockScanMetadata(t, a)
	server := httptest.NewServer(a.handler())
	defer server.Close()
	response := request(t, server, a, "POST", "/api/scans/"+id+"/matches", scanMatchQuery{GroupID: g.ID, Type: "movie", Query: "流浪地球", Year: "2019"}, 200)
	var choices []ScanMatchCandidate
	if err := json.Unmarshal(response["results"], &choices); err != nil || len(choices) != 2 || choices[0].ExternalID != "41" || choices[0].URL != "https://www.themoviedb.org/movie/41" {
		t.Fatalf("invalid ranked source candidates: %+v %v", choices, err)
	}
	c := choices[0]
	g.Title, g.Year, g.MatchToken = c.Title, c.Year, c.Token
	s, _ = a.scanSnapshot(id)
	for _, change := range []func(*ScanGroup){
		func(v *ScanGroup) { v.MatchToken = "forged" },
		func(v *ScanGroup) { v.ID = "different-group" },
		func(v *ScanGroup) { v.Type = "tv" },
		func(v *ScanGroup) { v.Title = "Different movie" },
		func(v *ScanGroup) { v.Year = "2020" },
		func(v *ScanGroup) { v.Manual = true },
	} {
		v := g
		change(&v)
		if _, err := resolvedScanImport(s, v, body); err == nil {
			t.Fatalf("stale or fabricated confirmation accepted: %+v", v)
		}
	}
	resolved, err := resolvedScanImport(s, g, body)
	if err != nil || resolved.MetadataProvider != "tmdb" || resolved.TMDBID != 41 || resolved.MetadataID != "41" {
		t.Fatal("selected identity lost", resolved, err)
	}
	copyPreview, err := a.preview(context.Background(), func() ImportRequest { r := resolved; r.Mode = "copy"; return r }())
	if err != nil || !strings.Contains(copyPreview.Rows[0].Destination, "流浪地球 (2019) [tmdbid-41]") {
		t.Fatal("canonical title not used for target path", copyPreview, err)
	}
	if err := a.startScanImport(id, ScanImportRequest{Mode: "reference", Groups: []ScanGroup{g}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool { s, _ := a.scanSnapshot(id); return s.Status == "complete" })
	waitFor(t, 3*time.Second, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return len(a.s.Items) == 1 && a.s.Items[0].MetadataStatus == "failed"
	})
	a.mu.Lock()
	it := a.s.Items[0]
	a.mu.Unlock()
	if it.Title != c.Title || it.Year != c.Year || it.MetadataID != "41" || it.TMDBID != 41 || it.Poster != c.Poster || len(a.s.Files) != 1 || len(a.s.Tasks) != 0 {
		t.Fatal("confirmed identity was lost when detail fetch failed", it)
	}
	// Repeating a confirmed import still skips the existing source file.
	s, _ = a.scanSnapshot(id)
	previews, err = a.checkScanPlan(context.Background(), s, ScanImportRequest{Mode: "reference", Groups: []ScanGroup{g}})
	if err != nil || planHasIssues(previews) || !previews[0].Preview.Rows[0].Duplicate || previews[0].Preview.Count != 0 {
		t.Fatal("confirmed import broke duplicate handling", previews, err)
	}
}

func TestScanManualChoiceWorksOfflineWithoutAutomaticRematching(t *testing.T) {
	a, id := scanFixture(t)
	_, g := scanMovie(t, a, id)
	var calls atomic.Int32
	a.s.Config.AutoMetadata = true
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("offline fixture")
	})}
	if _, err := a.searchScanMatches(context.Background(), id, scanMatchQuery{GroupID: g.ID, Type: g.Type, Query: g.Title}); err == nil {
		t.Fatal("offline search falsely succeeded")
	}
	calls.Store(0)
	g.Manual = true
	if err := a.startScanImport(id, ScanImportRequest{Mode: "reference", Groups: []ScanGroup{g}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool { s, _ := a.scanSnapshot(id); return s.Status == "complete" })
	if calls.Load() != 0 || len(a.s.Items) != 1 || a.s.Items[0].MetadataStatus != "manual" {
		t.Fatal("manual offline choice launched an unconfirmed online match")
	}
}

func TestScanMatchUsesPublicSourcesWithoutAPIKey(t *testing.T) {
	a, id := scanFixture(t)
	_, g := scanMovie(t, a, id)
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		data := `{"query":{"pages":[{"pageid":91,"title":"Example (2019 film)","extract":"Example is a 2019 film.","index":1}]}}`
		if r.URL.Host == "api.tvmaze.com" {
			data = `[{"show":{"id":92,"name":"Example Show","premiered":"2022-01-01"}}]`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
	})}
	choices, err := a.searchScanMatches(context.Background(), id, scanMatchQuery{GroupID: g.ID, Type: "movie", Query: "Example", Year: "2019"})
	if err != nil || len(choices) != 1 || choices[0].Provider != "wikipedia" || choices[0].ExternalID != "en:91" || choices[0].Year != "2019" {
		t.Fatal("keyless movie candidates unavailable", choices, err)
	}
	choices, err = a.searchScanMatches(context.Background(), id, scanMatchQuery{GroupID: "tv-fixture", Type: "tv", Query: "Example Show", Year: "2022"})
	if err != nil || len(choices) != 2 || choices[0].Provider != "tvmaze" || choices[0].URL != "https://www.tvmaze.com/shows/92" {
		t.Fatal("keyless television candidates unavailable", choices, err)
	}
}

func TestScanMatchInteractivePreview(t *testing.T) {
	if os.Getenv("YINGKU_SCAN_MATCH_PREVIEW") != "1" {
		t.Skip("interactive scan matching fixture is opt-in")
	}
	a := testApp(t)
	root := t.TempDir()
	for _, name := range []string{"After.The.Night.2024.1080p.mp4", "Ocean.Stories.2016.720p.mp4", "Unknown.Film.2020.mp4", "Offline.Test.2023.mp4"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture video"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a.s.Sources = []Source{{ID: "fixture", Type: "local", Root: root, Name: "界面测试影片（临时资料库）"}}
	a.s.Config.TMDBSecret, _ = a.encrypt("fixture-key")
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/search/") {
			query := r.URL.Query().Get("query")
			if strings.Contains(query, "Offline") {
				return nil, errors.New("测试：资料源暂时无法连接")
			}
			if strings.Contains(query, "Unknown") {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"results":[]}`)), Header: http.Header{}}, nil
			}
			title, year := "长夜之后 · 测试作品", "2024"
			if strings.Contains(query, "Ocean") {
				title, year = "海洋故事 · 测试作品", "2016"
			}
			data, _ := json.Marshal(map[string]any{"results": []map[string]any{{"id": 101, "title": title, "release_date": year + "-06-01", "poster_path": "/fixture.jpg", "overview": "这是界面验证用的模拟资料，用于检查候选确认、规范名称、来源编号和导入流程。"}, {"id": 102, "title": title + "（同名版本）", "release_date": "1998-01-01", "overview": "同名作品测试候选，请核对年份。"}}})
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data))), Header: http.Header{}}, nil
		}
		if r.URL.Host == "image.tmdb.org" {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"image/png"}}, Body: io.NopCloser(strings.NewReader(string(tinyPNG(t))))}, nil
		}
		return nil, errors.New("测试：完整资料暂时不可用")
	})}
	server := httptest.NewServer(a.handler())
	defer server.Close()
	fmt.Println("SCAN_MATCH_PREVIEW=" + server.URL)
	<-time.After(20 * time.Minute)
}
