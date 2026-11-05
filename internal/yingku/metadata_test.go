package yingku

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLocalReferenceDefaultDoesNotCopyOrQueue(t *testing.T) {
	a := testApp(t)
	root := t.TempDir()
	file := filepath.Join(root, "Movie.mp4")
	payload := []byte("existing local media")
	if err := os.WriteFile(file, payload, 0644); err != nil {
		t.Fatal(err)
	}
	a.s.Sources = append(a.s.Sources, Source{ID: "local", Type: "local", Root: root})
	r := ImportRequest{SourceID: "local", Type: "movie", Title: "My Movie", OutputRoot: "ignored relative path", Files: []Selection{{Key: "/Movie.mp4"}}}
	p, err := a.preview(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if p.Mode != "reference" || p.Rows[0].Destination != file || p.Rows[0].Issue != "" {
		t.Fatalf("wrong local preview: %+v", p)
	}
	result, err := a.importMedia(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if result["mode"] != "reference" || len(a.s.Tasks) != 0 || len(a.s.Files) != 1 || a.s.Files[0].Status != "completed" || !a.s.Files[0].Reference {
		t.Fatal("local reference created a transfer task")
	}
	if _, err = os.Stat(a.s.Config.MediaRoot); !os.IsNotExist(err) {
		t.Fatal("reference import created a download directory")
	}
	if data, _ := os.ReadFile(file); !bytes.Equal(data, payload) {
		t.Fatal("original file changed")
	}
	server := httptest.NewServer(a.handler())
	defer server.Close()
	resp, err := http.Get(server.URL + "/api/files/" + a.s.Files[0].ID + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Equal(data, payload) {
		t.Fatal("referenced video cannot be played")
	}
	reloaded, err := newApp(a.dir)
	if err != nil || !reloaded.s.Files[0].Reference || reloaded.s.Files[0].Destination != file {
		t.Fatal("reference not preserved on restart", err)
	}
}
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func TestLocalNFOPosterAndSubtitleNeedNoCredentials(t *testing.T) {
	a := testApp(t)
	root := t.TempDir()
	original := `<movie><title>原有片名</title><year>2024</year><plot>原有简介</plot><rating>8.2</rating><genre>剧情</genre><actor><name>演员甲</name></actor><uniqueid type="tmdb">123</uniqueid><thumb>poster.png</thumb></movie>`
	fixtures := map[string][]byte{"movie.mp4": []byte("video"), "movie.nfo": []byte(original), "poster.png": tinyPNG(t), "movie.srt": []byte("1\n00:00:00,000 --> 00:00:01,000\n字幕\n")}
	for name, data := range fixtures {
		if err := os.WriteFile(filepath.Join(root, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	var requests atomic.Int32
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, fmt.Errorf("must remain offline")
	})}
	a.s.Sources = append(a.s.Sources, Source{ID: "nfo", Type: "local", Root: root})
	result, err := a.importMedia(context.Background(), ImportRequest{SourceID: "nfo", Type: "movie", Title: "待识别", Files: []Selection{{Key: "/movie.mp4"}}})
	if err != nil {
		t.Fatal(err)
	}
	it, _ := a.item(result["itemId"].(string))
	if it.Title != "原有片名" || it.MetadataProvider != "local" || it.MetadataStatus != "ready" || it.Rating != 8.2 || it.TMDBID != 123 || len(it.Cast) != 1 || !strings.HasPrefix(it.Poster, "local:") {
		t.Fatalf("NFO was not reused: %+v", it)
	}
	if len(a.s.Files[0].Subtitles) != 1 {
		t.Fatal("same-name local subtitle not found")
	}
	a.writeNFO(it.ID)
	for name, want := range fixtures {
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("original changed: %s", name)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("local NFO import contacted an API")
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "http://localhost/api/art?path="+url.QueryEscape(it.Poster), nil)
	r.RemoteAddr = "127.0.0.1:1234"
	a.handler().ServeHTTP(w, r)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), fixtures["poster.png"]) {
		t.Fatal("local poster not accessible")
	}
}
func TestLocalTVNFOAndReferenceEpisodeCollisions(t *testing.T) {
	a := testApp(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Season 01"), 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"tvshow.nfo": "<tvshow><title>原有剧名</title><plot>剧集简介</plot></tvshow>", "Season 01/Show.S01E01.mp4": "video", "Season 01/Show.S01E01.nfo": "<episodedetails><title>第一集名称</title><plot>这一集的简介</plot><aired>2024-01-01</aired></episodedetails>", "Season 01/Show.S01E01.copy.mp4": "second"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	a.s.Sources = append(a.s.Sources, Source{ID: "tv", Type: "local", Root: root})
	r := ImportRequest{SourceID: "tv", Type: "tv", Title: "Show", Season: 1, Files: []Selection{{Key: "/Season 01/Show.S01E01.mp4"}, {Key: "/Season 01/Show.S01E01.copy.mp4"}}}
	p, err := a.preview(context.Background(), r)
	if err != nil || p.Rows[1].Issue == "" {
		t.Fatal("duplicate episode accepted", err)
	}
	r.Files = r.Files[:1]
	result, err := a.importMedia(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	it, _ := a.item(result["itemId"].(string))
	if it.Title != "原有剧名" || len(it.Episodes) != 1 || it.Episodes[0].Name != "第一集名称" {
		t.Fatalf("episode metadata not read: %+v", it)
	}
}
func TestRemoteCannotReferenceAndUnknownModesRejected(t *testing.T) {
	a := testApp(t)
	a.s.Sources = append(a.s.Sources, Source{ID: "remote", Type: "webdav", URL: "https://example.test/dav/"})
	_, err := a.preview(context.Background(), ImportRequest{Mode: "reference", SourceID: "remote", Type: "movie", Title: "Movie", Files: []Selection{{Key: "/movie.mp4"}}})
	if err == nil {
		t.Fatal("remote reference mode accepted")
	}
}
func TestPublicMovieAutoScrapingWithoutAPIKey(t *testing.T) {
	a := testApp(t)
	a.s.Config.AutoMetadata = true
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "movie.mp4"), []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	a.s.Sources = append(a.s.Sources, Source{ID: "local", Type: "local", Root: root})
	picture := tinyPNG(t)
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" || r.URL.Query().Get("api_key") != "" {
			t.Error("keyless provider sent credentials")
		}
		switch r.URL.Host {
		case "zh.wikipedia.org":
			if r.URL.Query().Get("pilicense") != "any" {
				t.Error("nonfree poster lookup not requested")
			}
			if r.URL.Query().Get("generator") == "search" {
				return fakeJSON(`{"query":{"pages":[{"pageid":6345101,"title":"流浪地球 (电影)","index":1,"extract":"《流浪地球》是一部2019年科幻电影。","thumbnail":{"source":"https://upload.wikimedia.org/wikipedia/zh/a/ab/poster.jpg"}}]}}`), nil
			}
			return fakeJSON(`{"query":{"pages":[{"pageid":6345101,"title":"流浪地球 (电影)","extract":"2019年的科幻电影简介。","thumbnail":{"source":"https://upload.wikimedia.org/wikipedia/zh/a/ab/poster.jpg"},"pageprops":{"wikibase_item":"Q57966215"}}]}}`), nil
		case "www.wikidata.org":
			if r.URL.Query().Get("props") == "labels" {
				return fakeJSON(`{"entities":{"Q1":{"labels":{"zh-hans":{"value":"演员甲"}}},"Q2":{"labels":{"zh":{"value":"科幻电影"}}}}}`), nil
			}
			return fakeJSON(`{"entities":{"Q57966215":{"claims":{"P577":[{"mainsnak":{"datavalue":{"value":{"time":"+2019-02-05T00:00:00Z"}}}}],"P161":[{"mainsnak":{"datavalue":{"value":{"id":"Q1"}}}}],"P136":[{"mainsnak":{"datavalue":{"value":{"id":"Q2"}}}}]}}}}`), nil
		case "upload.wikimedia.org":
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"image/png"}}, Body: io.NopCloser(bytes.NewReader(picture))}, nil
		}
		return nil, fmt.Errorf("unexpected provider %s", r.URL.Host)
	})}
	result, err := a.importMedia(context.Background(), ImportRequest{SourceID: "local", Type: "movie", Title: "流浪地球", Year: "2019", Files: []Selection{{Key: "/movie.mp4"}}})
	if err != nil {
		t.Fatal(err)
	}
	id := result["itemId"].(string)
	waitFor(t, 3*time.Second, func() bool { it, _ := a.item(id); return it.MetadataStatus == "ready" })
	it, _ := a.item(id)
	if it.MetadataProvider != "wikipedia" || it.MetadataID != "zh:6345101" || len(it.Cast) != 1 || len(it.Genres) != 1 || it.MetadataURL == "" || it.Poster == "" {
		t.Fatalf("public metadata incomplete: %+v", it)
	}
	if len(a.s.Tasks) != 0 {
		t.Fatal("metadata scraping created download task")
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "http://localhost/api/art?path="+url.QueryEscape(it.Poster), nil)
	r.RemoteAddr = "127.0.0.1:1234"
	a.handler().ServeHTTP(w, r)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), picture) {
		t.Fatal("public poster cannot be cached")
	}
}
func TestTVmazePublicEpisodesWithoutCredentials(t *testing.T) {
	a := testApp(t)
	a.s.Items = append(a.s.Items, Item{ID: "show", Type: "tv", Title: "Sherlock"})
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" || r.URL.Host != "api.tvmaze.com" {
			t.Error("unexpected authenticated request")
		}
		return fakeJSON(`{"id":335,"name":"Sherlock","premiered":"2010-07-25","summary":"<p>A detective series.</p>","genres":["Crime"],"rating":{"average":9.0},"image":{"original":"https://static.tvmaze.com/uploads/images/original_untouched/1/1.jpg"},"_embedded":{"cast":[{"person":{"name":"Actor"}}],"episodes":[{"season":1,"number":1,"name":"A Study in Pink","summary":"<p>Episode summary</p>","airdate":"2010-07-25","image":{"medium":"https://static.tvmaze.com/uploads/images/medium_landscape/1/2.jpg"}}]}}`), nil
	})}
	a.scrapeSelected("show", "tvmaze", "335")
	it, _ := a.item("show")
	if it.MetadataStatus != "ready" || it.MetadataProvider != "tvmaze" || len(it.Episodes) != 1 || it.Episodes[0].Name != "A Study in Pink" || strings.Contains(it.Overview, "<p>") {
		t.Fatalf("TVmaze metadata incorrect: %+v", it)
	}
}
func TestLocalPosterOnlyStillGetsAutomaticDetails(t *testing.T) {
	a := testApp(t)
	a.s.Config.AutoMetadata = true
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Movie.mp4"), []byte("video"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "poster.png"), tinyPNG(t), 0644); err != nil {
		t.Fatal(err)
	}
	a.s.Sources = append(a.s.Sources, Source{ID: "local", Type: "local", Root: root})
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return fakeJSON(`{"query":{"pages":[{"pageid":42,"title":"Movie (2024 film)","index":1,"extract":"A 2024 film with a detailed synopsis.","thumbnail":{"source":"https://upload.wikimedia.org/wikipedia/en/a/ab/poster.jpg"}}]}}`), nil
	})}
	result, err := a.importMedia(context.Background(), ImportRequest{SourceID: "local", Type: "movie", Title: "Movie", Files: []Selection{{Key: "/Movie.mp4"}}})
	if err != nil {
		t.Fatal(err)
	}
	id := result["itemId"].(string)
	waitFor(t, 3*time.Second, func() bool {
		it, _ := a.item(id)
		return it.MetadataStatus == "ready" && it.MetadataProvider == "wikipedia"
	})
	it, _ := a.item(id)
	if !strings.HasPrefix(it.Poster, "local:") || !strings.Contains(it.Overview, "synopsis") {
		t.Fatalf("local cover or online details lost: %+v", it)
	}
}
func TestUncertainPublicMatchesNeedConfirmation(t *testing.T) {
	a := testApp(t)
	a.s.Items = append(a.s.Items, Item{ID: "movie", Type: "movie", Title: "Unrelated"})
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return fakeJSON(`{"query":{"pages":[{"pageid":1,"title":"Another film","extract":"A 2024 film."}]}}`), nil
	})}
	a.autoScrape("movie")
	it, _ := a.item("movie")
	if it.MetadataStatus != "manual" || it.Title != "Unrelated" {
		t.Fatal("low-confidence result was applied")
	}
}
func TestPublicSearchSkipsBooksAndPeople(t *testing.T) {
	a := testApp(t)
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return fakeJSON(`{"query":{"pages":[
			{"pageid":1,"title":"流浪地球","extract":"《流浪地球》是2019年上映的科幻电影，改编自同名小说。"},
			{"pageid":2,"title":"流浪地球 (小说)","extract":"《流浪地球》是小说，2019年改编成同名电影。"},
			{"pageid":3,"title":"赵今麦","extract":"赵今麦（2002年9月29日—），中国演员，主演电影《流浪地球》。"},
			{"pageid":4,"title":"郭帆","extract":"郭帆（1980年12月15日—），中国电影导演，代表作《流浪地球》。"},
			{"pageid":5,"title":"Example Actor","extract":"Example Actor (born 1970) is a film actor."}
		]}}`), nil
	})}
	rows, err := a.searchMetadata(context.Background(), "movie", "流浪地球")
	if err != nil || len(rows) != 1 || rows[0]["title"] != "流浪地球" {
		t.Fatalf("film search includes non-film subjects: %v %v", rows, err)
	}
}
func TestArtworkProviderAllowlist(t *testing.T) {
	for _, bad := range []string{"http://upload.wikimedia.org/wikipedia/a.jpg", "https://127.0.0.1/a.jpg", "https://static.tvmaze.com.evil.test/uploads/images/a.jpg", "https://user:pass@static.tvmaze.com/uploads/images/a.jpg", "https://static.tvmaze.com:444/uploads/images/a.jpg"} {
		if _, err := publicArtURL(bad); err == nil {
			t.Fatalf("unsafe image URL allowed: %s", bad)
		}
	}
}
func TestPublicMetadataLive(t *testing.T) {
	if os.Getenv("YINGKU_LIVE_METADATA") != "1" {
		t.Skip("set YINGKU_LIVE_METADATA=1 for an actual public API smoke test")
	}
	a := testApp(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	rows, err := a.searchMetadata(ctx, "movie", "流浪地球")
	if err != nil || len(rows) == 0 {
		t.Fatalf("movie search: %v", err)
	}
	var selected map[string]any
	for _, row := range rows {
		if row["externalId"] == "zh:6345101" {
			selected = row
		}
	}
	if selected == nil {
		b, _ := json.Marshal(rows)
		t.Fatalf("expected film missing: %s", b)
	}
	info, err := a.fetchWiki(ctx, selected["externalId"].(string))
	if err != nil || info.Poster == "" || info.Overview == "" {
		t.Fatalf("movie details: %+v %v", info, err)
	}
	maze, err := a.fetchMaze(ctx, "335")
	if err != nil || len(maze.Episodes) == 0 || maze.Poster == "" {
		t.Fatalf("TV details: %v", err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "http://localhost/api/art?path="+url.QueryEscape(info.Poster), nil).WithContext(ctx)
	a.handler().ServeHTTP(w, r)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "image/") {
		t.Fatalf("live poster: HTTP %d %s", w.Code, w.Body.String())
	}
	t.Logf("Live public APIs: movie %s (%s), poster present, %d cast; TV %s, %d episodes", info.Title, info.Year, len(info.Cast), maze.Title, len(maze.Episodes))
}
