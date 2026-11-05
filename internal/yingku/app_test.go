package yingku

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
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testApp(t *testing.T) *App {
	t.Helper()
	a, err := newApp(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	a.s.Config.AutoMetadata = false
	return a
}
func request(t *testing.T, server *httptest.Server, a *App, method, path string, body any, want int) map[string]json.RawMessage {
	t.Helper()
	var b io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		b = bytes.NewReader(data)
	}
	r, err := http.NewRequest(method, server.URL+path, b)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("X-App-Token", a.csrf)
	r.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("%s %s: got %d, want %d: %s", method, path, resp.StatusCode, want, data)
	}
	var result map[string]json.RawMessage
	if err = json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Invalid JSON: %s", data)
	}
	return result
}
func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("timed out waiting for background task")
}
func workers(t *testing.T, a *App) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	go a.startWorkers(ctx)
	t.Cleanup(func() {
		cancel()
		a.mu.Lock()
		a.closing = true
		for _, stop := range a.active {
			stop()
		}
		a.mu.Unlock()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			a.mu.Lock()
			n := len(a.active)
			a.mu.Unlock()
			if n == 0 {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	return ctx
}
func TestImportDownloadPlaybackAndPersistence(t *testing.T) {
	a := testApp(t)
	src := t.TempDir()
	payload := bytes.Repeat([]byte("test media payload"), 20000)
	for _, name := range []string{"Example.S01E01.mp4", "Example.S01E02.mp4"} {
		if err := os.WriteFile(filepath.Join(src, name), payload, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(src, "Example.S01E01.srt"), []byte("1\n00:00:00,100 --> 00:00:02,000\n你好\n"), 0644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.handler())
	defer server.Close()
	sourceResponse := request(t, server, a, "POST", "/api/sources", map[string]any{"type": "local", "name": "测试来源", "root": src}, 201)
	var sid string
	_ = json.Unmarshal(sourceResponse["id"], &sid)
	listing := request(t, server, a, "GET", "/api/sources/"+sid+"/list", nil, 200)
	var catalog []Entry
	_ = json.Unmarshal(listing["entries"], &catalog)
	if len(catalog) != 3 {
		t.Fatalf("wrong listing: %+v", catalog)
	}
	body := ImportRequest{Mode: "copy", SourceID: sid, Type: "tv", Title: "Example", Year: "2024", Season: 1, Files: []Selection{{Key: "/Example.S01E01.mp4"}, {Key: "/Example.S01E02.mp4"}}}
	p := request(t, server, a, "POST", "/api/import/preview", body, 200)
	var rows []PreviewRow
	_ = json.Unmarshal(p["rows"], &rows)
	if rows[0].Episode != 1 || rows[1].Episode != 2 {
		t.Fatalf("bad episode mapping: %+v", rows)
	}
	request(t, server, a, "POST", "/api/import", body, 201)
	workers(t, a)
	waitFor(t, 5*time.Second, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return len(a.s.Tasks) == 2 && a.s.Tasks[0].Status == "completed" && a.s.Tasks[1].Status == "completed"
	})
	a.mu.Lock()
	file := a.s.Files[0]
	a.mu.Unlock()
	out, err := os.ReadFile(file.Destination)
	if err != nil || !bytes.Equal(out, payload) {
		t.Fatal("downloaded bytes differ", err)
	}
	if _, err = os.Stat(filepath.Join(filepath.Dir(filepath.Dir(file.Destination)), "tvshow.nfo")); err != nil {
		waitFor(t, time.Second, func() bool {
			_, e := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(file.Destination)), "tvshow.nfo"))
			return e == nil
		})
	}
	req, _ := http.NewRequest("GET", server.URL+"/api/files/"+file.ID+"/stream", nil)
	req.Header.Set("Range", "bytes=7-22")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || !bytes.Equal(data, payload[7:23]) {
		t.Fatalf("range streaming failed: %d %q", resp.StatusCode, data)
	}
	req, _ = http.NewRequest("GET", server.URL+"/api/files/"+file.ID+"/stream", nil)
	req.Header.Set("Range", "bytes=-5")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || !bytes.Equal(data, payload[len(payload)-5:]) {
		t.Fatal("suffix range failed")
	}
	resp, err = http.Get(server.URL + "/api/files/" + file.ID + "/subtitles/0")
	if err != nil {
		t.Fatal(err)
	}
	data, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(string(data), "WEBVTT") || !strings.Contains(string(data), "00:00:00.100") {
		t.Fatalf("subtitle conversion failed: %s", data)
	}
	request(t, server, a, "POST", "/api/files/"+file.ID+"/progress", map[string]any{"position": 123, "duration": 500}, 200)
	request(t, server, a, "POST", "/api/import", body, 400)
	reloaded, err := newApp(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.s.Files[0].Position != 123 || len(reloaded.s.Files[0].Subtitles) != 1 {
		t.Fatal("progress or subtitles lost on reload")
	}
	if _, err = os.Stat(filepath.Join(src, "Example.S01E01.mp4")); err != nil {
		t.Fatal("source file was modified")
	}
}
func TestRemotePauseResumePreservesContent(t *testing.T) {
	a := testApp(t)
	payload := bytes.Repeat([]byte("0123456789ABCDEF"), 512*1024)
	var ranges atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"version-1"`)
		start := 0
		if h := r.Header.Get("Range"); h != "" {
			var n int
			if _, err := fmt.Sscanf(h, "bytes=%d-", &n); err != nil {
				t.Error(err)
			}
			start = n
			ranges.Add(1)
			if r.Header.Get("If-Range") != `"version-1"` {
				t.Error("missing range validator")
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(payload)-1, len(payload)))
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)-start))
			w.WriteHeader(206)
		} else {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		}
		for n := start; n < len(payload); n += 32768 {
			end := min(n+32768, len(payload))
			if _, err := w.Write(payload[n:end]); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}))
	defer remote.Close()
	secret, _ := a.encrypt(Credentials{})
	target := filepath.Join(a.s.Config.MediaRoot, "Demo", "Demo.mp4")
	a.mu.Lock()
	a.s.Sources = append(a.s.Sources, Source{ID: "remote", Type: "webdav", URL: remote.URL, Secret: secret})
	a.s.Items = append(a.s.Items, Item{ID: "item", Type: "movie", Title: "Demo"})
	a.s.Files = append(a.s.Files, MediaFile{ID: "file", ItemID: "item", SourceID: "remote", SourceKey: "/movie.mp4", Size: int64(len(payload)), Destination: target, Status: "queued"})
	a.s.Tasks = append(a.s.Tasks, Task{ID: "task", FileID: "file", Status: "queued", Total: int64(len(payload))})
	a.mu.Unlock()
	workers(t, a)
	waitFor(t, 4*time.Second, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.s.Tasks[0].Bytes > 0 && a.s.Tasks[0].Status == "downloading"
	})
	if err := a.controlTask("task", "pause"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool { a.mu.Lock(); defer a.mu.Unlock(); return len(a.active) == 0 })
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("incomplete download became playable")
	}
	if err := a.controlTask("task", "resume"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 6*time.Second, func() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.s.Tasks[0].Status == "completed" })
	data, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatal("resumed file corrupted", err)
	}
	if ranges.Load() == 0 {
		t.Fatal("download did not resume via Range")
	}
}
func TestRemoteIgnoringRangeRestartsSafely(t *testing.T) {
	a := testApp(t)
	payload := []byte("the whole new file")
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) }))
	defer remote.Close()
	secret, _ := a.encrypt(Credentials{})
	info, err := a.openSource(context.Background(), Source{Type: "webdav", URL: remote.URL, Secret: secret}, MediaFile{SourceKey: "/file.mp4", Size: int64(len(payload))}, Task{Bytes: 5, ETag: `"old"`})
	if err != nil {
		t.Fatal(err)
	}
	defer info.Reader.Close()
	if info.Offset != 0 {
		t.Fatal("a full response must reset the partial file")
	}
}
func TestWebDAVDirectoryParsingAndScope(t *testing.T) {
	a := testApp(t)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PROPFIND" || r.Header.Get("Depth") != "1" {
			t.Error("wrong DAV method")
		}
		w.WriteHeader(207)
		_, _ = io.WriteString(w, `<d:multistatus xmlns:d="DAV:"><d:response><d:href>/dav/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response><d:response><d:href>/dav/%E5%89%A7%E9%9B%86/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response><d:response><d:href>/dav/EP02.mp4</d:href><d:propstat><d:prop><d:getcontentlength>123</d:getcontentlength><d:getetag>"e2"</d:getetag></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response><d:response><d:href>/outside.mp4</d:href><d:propstat><d:prop/></d:propstat></d:response></d:multistatus>`)
	}))
	defer remote.Close()
	entries, err := a.list(context.Background(), Source{Type: "webdav", URL: remote.URL + "/dav/"}, "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || !entries[0].Dir || entries[0].Name != "剧集" || entries[1].Key != "/EP02.mp4" || entries[1].Size != 123 {
		t.Fatalf("bad DAV entries: %+v", entries)
	}
}
func TestCSRFOriginAndPathGuards(t *testing.T) {
	a := testApp(t)
	handler := a.handler()
	for _, test := range []struct {
		method, path, host, origin, token string
		status                            int
	}{{"POST", "/api/config", "localhost:8787", "", "", 403}, {"GET", "/api/state", "media.example.com", "https://attacker.example", "", 403}, {"GET", "/api/state", "localhost:8787", "https://attacker.example", "", 403}, {"GET", "/api/state", "localhost/invalid", "", "", 403}} {
		r := httptest.NewRequest(test.method, "http://"+test.host+test.path, nil)
		r.Host = test.host
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Origin", test.origin)
		r.Header.Set("X-App-Token", test.token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatalf("bad protection: %d", w.Code)
		}
	}
	for _, p := range []string{"/../secret", "/a/../../secret", "/a\\..\\secret"} {
		if _, err := cleanKey(p); err == nil {
			t.Fatalf("unsafe path accepted: %s", p)
		}
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.mp4"), []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err == nil {
		if _, err := localPath(Source{Root: root}, "/escape/secret.mp4"); err == nil {
			t.Fatal("symlink escape accepted")
		}
	}
}
func TestCredentialsEncryptedAndNotReturned(t *testing.T) {
	a := testApp(t)
	token := "sample-private-read-token-123456"
	secret, err := a.encrypt(token)
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.s.Config.TMDBSecret = secret
	sourceSecret, _ := a.encrypt(Credentials{Password: "very-secret-password", AccessToken: "baidu-token-secret"})
	a.s.Sources = append(a.s.Sources, Source{ID: "private", Type: "baidu", Secret: sourceSecret})
	if err = a.saveLocked(); err != nil {
		t.Fatal(err)
	}
	a.mu.Unlock()
	disk, err := os.ReadFile(filepath.Join(a.dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := json.Marshal(a.publicState())
	for _, s := range []string{token, "very-secret-password", "baidu-token-secret"} {
		if bytes.Contains(disk, []byte(s)) || bytes.Contains(pub, []byte(s)) {
			t.Fatal("secret leaked")
		}
	}
	var recovered string
	if err = a.decrypt(secret, &recovered); err != nil || recovered != token {
		t.Fatal("credential roundtrip failed")
	}
}
func TestRecognitionAndConflictPreflight(t *testing.T) {
	for _, tc := range []struct {
		name string
		s, e int
	}{{"Show.S02E03.1080p.mkv", 2, 3}, {"剧名.第二季.第十二集.mp4", 2, 12}, {"EP09.mp4", 0, 9}, {"03.mp4", 0, 3}, {"Show.S00E01.mkv", 0, 1}, {"Show.1080p.mp4", 0, 0}} {
		s, e := guessEpisode(tc.name)
		if s != tc.s || e != tc.e {
			t.Errorf("%s: got %d,%d", tc.name, s, e)
		}
	}
	if !naturalLess("EP2.mp4", "EP10.mp4") {
		t.Fatal("numeric ordering failed")
	}
	if safeName("../CON") != ".._CON" {
		t.Log("sanitized", safeName("../CON"))
	}
	if strings.ContainsAny(safeName("test<>:\\*?.mp4"), `<>:"/\|?*`) {
		t.Fatal("unsafe generated filename")
	}
	a := testApp(t)
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "S01E01.mp4"), []byte("x"), 0644)
	a.s.Sources = append(a.s.Sources, Source{ID: "s", Type: "local", Root: root})
	body := ImportRequest{Mode: "copy", SourceID: "s", Type: "tv", Title: "Demo", Season: 1, Files: []Selection{{Key: "/S01E01.mp4"}}}
	p, err := a.preview(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(p.Rows[0].Destination), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p.Rows[0].Destination, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	p, err = a.preview(context.Background(), body)
	if err != nil || p.Rows[0].Issue == "" {
		t.Fatal("existing file conflict not detected")
	}
	if _, err = a.importMedia(context.Background(), body); err == nil {
		t.Fatal("conflicting import was accepted")
	}
	b, _ := os.ReadFile(p.Rows[0].Destination)
	if string(b) != "original" {
		t.Fatal("existing file overwritten")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fakeJSON(s string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(s))}
}
func TestMetadataAndBaiduAdapters(t *testing.T) {
	a := testApp(t)
	tmdbSecret, _ := a.encrypt("test-tmdb-bearer")
	baiduSecret, _ := a.encrypt(Credentials{AccessToken: "test-baidu-access"})
	a.s.Config.TMDBSecret = tmdbSecret
	a.s.Sources = append(a.s.Sources, Source{ID: "bd", Type: "baidu", Root: "/影视", Secret: baiduSecret})
	a.s.Items = append(a.s.Items, Item{ID: "show", Type: "tv", Title: "旧名称"})
	a.s.Files = append(a.s.Files, MediaFile{ID: "f", ItemID: "show", Season: 2, Episode: 3})
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "api.themoviedb.org":
			if r.Header.Get("Authorization") != "Bearer test-tmdb-bearer" {
				t.Error("missing TMDb auth")
			}
			if strings.HasSuffix(r.URL.Path, "/season/2") {
				return fakeJSON(`{"episodes":[{"season_number":2,"episode_number":3,"name":"分集标题","overview":"分集简介","still_path":"/still.jpg","air_date":"2024-01-01"}]}`), nil
			}
			return fakeJSON(`{"id":123,"name":"正确剧名","original_name":"Original","first_air_date":"2024-01-01","overview":"简介","poster_path":"/poster.jpg","vote_average":8.2,"genres":[{"name":"剧情"}],"credits":{"cast":[{"name":"演员甲"}]}}`), nil
		case "pan.baidu.com":
			if r.URL.Query().Get("access_token") != "test-baidu-access" {
				t.Error("missing Baidu token")
			}
			if r.URL.Query().Get("dir") != "/影视" {
				t.Error("wrong scoped directory")
			}
			return fakeJSON(`{"errno":0,"list":[{"fs_id":123456789012345,"server_filename":"S02E03.mp4","path":"/影视/S02E03.mp4","size":42,"isdir":0,"server_mtime":1700000000}]}`), nil
		}
		return nil, fmt.Errorf("unexpected remote request: %s", r.URL.Host)
	})}
	entries, err := a.list(context.Background(), a.s.Sources[0], "/")
	if err != nil || len(entries) != 1 || entries[0].Key != "/S02E03.mp4" || entries[0].RemoteID != "123456789012345" {
		t.Fatalf("bad Baidu list: %+v %v", entries, err)
	}
	a.scrape("show", 123)
	it, err := a.item("show")
	if err != nil || it.MetadataStatus != "ready" || it.Title != "正确剧名" || len(it.Episodes) != 1 || len(it.Cast) != 1 {
		t.Fatalf("bad metadata: %+v %v", it, err)
	}
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) { return nil, fmt.Errorf("offline") })}
	a.scrape("show", 123)
	it, _ = a.item("show")
	if it.MetadataStatus != "failed" || it.Title != "正确剧名" || a.s.Files[0].Status != "" {
		t.Fatal("metadata failure damaged existing library or download state")
	}
}

func TestInterruptedDownloadRecoveryAndRecordRemoval(t *testing.T) {
	a := testApp(t)
	src := t.TempDir()
	payload := []byte("resumable file contents")
	sourcePath := filepath.Join(src, "film.mp4")
	if err := os.WriteFile(sourcePath, payload, 0644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(a.s.Config.MediaRoot, "Recovery", "Recovery.mp4")
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatal(err)
	}
	part := target + ".yingku-recovery-task.part"
	if err := os.WriteFile(part, payload[:8], 0644); err != nil {
		t.Fatal(err)
	}
	a.s.Sources = append(a.s.Sources, Source{ID: "source", Type: "local", Root: src})
	a.s.Items = append(a.s.Items, Item{ID: "recovery", Type: "movie", Title: "Recovery", MetadataStatus: "loading"})
	a.s.Files = append(a.s.Files, MediaFile{ID: "recovery-file", ItemID: "recovery", SourceID: "source", SourceKey: "/film.mp4", Destination: target, Size: int64(len(payload)), Status: "downloading"})
	a.s.Tasks = append(a.s.Tasks, Task{ID: "recovery-task", FileID: "recovery-file", Status: "downloading", Bytes: 3, Total: int64(len(payload))})
	if err := a.saveLocked(); err != nil {
		t.Fatal(err)
	}
	b, err := newApp(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	if b.s.Tasks[0].Status != "paused" || b.s.Files[0].Status != "paused" || b.s.Items[0].MetadataStatus != "failed" {
		t.Fatal("interrupted jobs were not recovered")
	}
	workers(t, b)
	if err = b.controlTask("recovery-task", "resume"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 4*time.Second, func() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.s.Tasks[0].Status == "completed" })
	result, _ := os.ReadFile(target)
	if !bytes.Equal(result, payload) {
		t.Fatal("recovery ignored actual partial file size")
	}
	if err = b.controlTask("recovery-task", "remove"); err == nil {
		t.Fatal("completed record removal should be refused")
	}
	b.mu.Lock()
	b.s.Files = append(b.s.Files, MediaFile{ID: "failed-file", ItemID: "recovery", Status: "failed"})
	b.s.Tasks = append(b.s.Tasks, Task{ID: "failed-task", FileID: "failed-file", Status: "failed"})
	b.mu.Unlock()
	if err = b.controlTask("failed-task", "remove"); err != nil {
		t.Fatal(err)
	}
	result, _ = os.ReadFile(target)
	original, _ := os.ReadFile(sourcePath)
	if !bytes.Equal(result, payload) || !bytes.Equal(original, payload) {
		t.Fatal("record removal touched media or source files")
	}
}
