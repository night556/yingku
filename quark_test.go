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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const quarkFixtureCookie = "__pus=fixture-session; __puus=fixture-original"
const quarkFixturePayload = "quark-fixture-video-content"

type quarkFixture struct {
	mu                sync.Mutex
	dirs              map[string][]map[string]any
	rotate            bool
	expired           bool
	apiRedirect       bool
	downloadRedirect  bool
	untrustedRequests int
	pages             []string
	getCookies        []string
}

func quarkFixtureFile(id, name string, file bool) map[string]any {
	return map[string]any{"fid": id, "file_name": name, "file": file, "size": len(quarkFixturePayload), "updated_at": int64(1760000000000)}
}

func newQuarkFixture(t *testing.T) (*App, *quarkFixture) {
	t.Helper()
	a := testApp(t)
	f := &quarkFixture{rotate: true, dirs: map[string][]map[string]any{
		"0":         {quarkFixtureFile("dir_movie", "movie", false), quarkFixtureFile("dir_tv", "tv", false)},
		"dir_movie": {quarkFixtureFile("movie_file", "Example.2024.mp4", true)},
		"dir_tv":    {quarkFixtureFile("dir_show", "Example Show", false)},
		"dir_show":  {quarkFixtureFile("season_1", "Season 01", false), quarkFixtureFile("season_2", "Season 02", false)},
		"season_1":  {quarkFixtureFile("episode_1", "Example.Show.S01E01.mp4", true)},
		"season_2":  {quarkFixtureFile("episode_2", "Example.Show.S02E01.mp4", true)},
	}}
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		jsonResponse := func(v any) *http.Response {
			data, _ := json.Marshal(v)
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(data)))}
		}
		if r.URL.Host == "drive.quark.cn" {
			if r.URL.Query().Get("pr") != "ucpro" || r.URL.Query().Get("fr") != "pc" || r.Header.Get("Referer") != quarkReferer || validateQuarkCookie(r.Header.Get("Cookie")) != nil {
				return nil, errors.New("fixture received incomplete Quark request")
			}
			if f.expired {
				return jsonResponse(map[string]any{"status": 401, "code": 41001, "message": "请登录 " + quarkFixtureCookie}), nil
			}
			if f.apiRedirect {
				return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://untrusted.invalid/"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
			}
			if strings.HasSuffix(r.URL.Path, "/file/sort") {
				parent := r.URL.Query().Get("pdir_fid")
				page, _ := strconv.Atoi(r.URL.Query().Get("_page"))
				if page < 1 || r.URL.Query().Get("_size") != "100" {
					return nil, errors.New("fixture received invalid pagination")
				}
				f.pages = append(f.pages, parent+":"+strconv.Itoa(page))
				all, ok := f.dirs[parent]
				if !ok {
					return jsonResponse(map[string]any{"status": 404, "code": 404, "message": "folder missing"}), nil
				}
				start := (page - 1) * 100
				if start > len(all) {
					start = len(all)
				}
				end := start + 100
				if end > len(all) {
					end = len(all)
				}
				items := append([]map[string]any{}, all[start:end]...)
				response := jsonResponse(map[string]any{"status": 200, "code": 0, "data": map[string]any{"list": items}, "metadata": map[string]any{"_total": len(all)}})
				if f.rotate {
					response.Header.Set("Set-Cookie", "__puus=fixture-updated; Path=/; Domain=.quark.cn; HttpOnly")
					f.rotate = false
				}
				return response, nil
			}
			if strings.HasSuffix(r.URL.Path, "/file/download") && r.Method == "POST" {
				var body struct {
					IDs []string `json:"fids"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.IDs) != 1 {
					return nil, errors.New("invalid fixture download request")
				}
				return jsonResponse(map[string]any{"status": 200, "code": 0, "data": []map[string]any{{"fid": body.IDs[0], "size": len(quarkFixturePayload), "download_url": "https://dl-fixture.qk-cdn.cn/video/" + body.IDs[0] + "?ticket=fixture"}}}), nil
			}
		}
		if r.URL.Host == "dl-fixture.qk-cdn.cn" {
			f.getCookies = append(f.getCookies, r.Header.Get("Cookie"))
			if f.downloadRedirect {
				return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://untrusted.invalid/video"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
			}
			offset := 0
			status := 200
			headers := http.Header{"Content-Type": {"video/mp4"}, "Etag": {`"fixture-v1"`}}
			if raw := r.Header.Get("Range"); raw != "" {
				if r.Header.Get("If-Range") != `"fixture-v1"` {
					return nil, errors.New("resume did not validate the remote file")
				}
				fmt.Sscanf(raw, "bytes=%d-", &offset)
				if offset >= len(quarkFixturePayload) {
					return nil, errors.New("fixture range invalid")
				}
				status = 206
				headers.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, len(quarkFixturePayload)-1, len(quarkFixturePayload)))
			}
			return &http.Response{StatusCode: status, Header: headers, ContentLength: int64(len(quarkFixturePayload) - offset), Body: io.NopCloser(strings.NewReader(quarkFixturePayload[offset:]))}, nil
		}
		if r.URL.Host == "untrusted.invalid" {
			f.untrustedRequests++
		}
		return nil, errors.New("no external network in fixture")
	})}
	return a, f
}

func addQuarkFixtureSource(t *testing.T, a *App, root string) Source {
	t.Helper()
	server := httptest.NewServer(a.handler())
	defer server.Close()
	out := request(t, server, a, "POST", "/api/sources", map[string]any{"type": "quark", "name": "夸克测试网盘", "root": root, "cookie": quarkFixtureCookie}, 201)
	var id string
	json.Unmarshal(out["id"], &id)
	s, err := a.source(id)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestQuarkSourceCredentialsRotationAndPagination(t *testing.T) {
	a, f := newQuarkFixture(t)
	s := addQuarkFixtureSource(t, a, "")
	c, _ := a.credentials(s)
	if s.Root != "0" || !strings.Contains(c.Cookie, "fixture-updated") {
		t.Fatal("cookie rotation during connection test lost")
	}
	for i := 0; i < 105; i++ {
		f.dirs["dir_movie"] = append(f.dirs["dir_movie"], quarkFixtureFile(fmt.Sprintf("extra_%d", i), fmt.Sprintf("Movie.%03d.mp4", i), true))
	}
	entries, err := a.list(context.Background(), s, "/movie")
	if err != nil || len(entries) != 106 {
		t.Fatal("paginated folder incomplete", len(entries), err)
	}
	f.mu.Lock()
	pages := strings.Join(f.pages, ",")
	f.mu.Unlock()
	if !strings.Contains(pages, "dir_movie:2") {
		t.Fatal("second page never fetched")
	}
	public, _ := json.Marshal(a.publicState())
	saved, _ := os.ReadFile(a.dir + "/state.json")
	if strings.Contains(string(public), "fixture-updated") || strings.Contains(string(saved), "fixture-updated") || strings.Contains(string(saved), "fixture-original") {
		t.Fatal("cookie exposed outside encrypted credentials")
	}
	server := httptest.NewServer(a.handler())
	defer server.Close()
	request(t, server, a, "POST", "/api/sources", map[string]any{"id": s.ID, "type": "quark", "name": "编辑测试", "root": "0", "cookie": ""}, 201)
	s, _ = a.source(s.ID)
	c, _ = a.credentials(s)
	if !strings.Contains(c.Cookie, "fixture-updated") {
		t.Fatal("blank edit cleared credentials")
	}
}

func TestQuarkScansSeriesDownloadsAndSkipsRepeatedImport(t *testing.T) {
	a, _ := newQuarkFixture(t)
	source := addQuarkFixtureSource(t, a, "0")
	id, err := a.startScan(source.ID, "/tv", "local")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool { s, _ := a.scanSnapshot(id); return !scanBusy(s.Status) })
	s, _ := a.scanSnapshot(id)
	if s.Status != "ready" || s.VideoCount != 2 || len(s.Plan.Groups) != 1 || s.Plan.Groups[0].Type != "tv" || s.Plan.Groups[0].Files[1].Season != 2 {
		t.Fatal("Quark series grouping failed", s.Plan)
	}
	g := s.Plan.Groups[0]
	g.Manual = true
	body := ScanImportRequest{Mode: "download", Groups: []ScanGroup{g}}
	previews, err := a.checkScanPlan(context.Background(), s, body)
	if err != nil || planHasIssues(previews) || previews[0].Preview.Rows[0].Entry.RemoteID == "" {
		t.Fatal("Quark preview failed", previews, err)
	}
	workers(t, a)
	if err := a.startScanImport(id, body); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return len(a.s.Tasks) == 2 && a.s.Tasks[0].Status == "completed" && a.s.Tasks[1].Status == "completed"
	})
	a.mu.Lock()
	files := append([]MediaFile{}, a.s.Files...)
	a.mu.Unlock()
	for _, f := range files {
		data, err := os.ReadFile(f.Destination)
		if err != nil || string(data) != quarkFixturePayload || f.Reference {
			t.Fatal("Quark video not downloaded correctly", f.Name, err)
		}
	}
	s, _ = a.scanSnapshot(id)
	previews, err = a.checkScanPlan(context.Background(), s, body)
	if err != nil || planHasIssues(previews) || previews[0].Preview.Count != 0 || !previews[0].Preview.Rows[0].Duplicate {
		t.Fatal("duplicate Quark download not skipped", previews, err)
	}
}

func TestQuarkResumeAndRejectChangedFileOrCredentialRedirects(t *testing.T) {
	a, fixture := newQuarkFixture(t)
	source := addQuarkFixtureSource(t, a, "0")
	entries, _ := a.list(context.Background(), source, "/movie")
	e := entries[0]
	f := MediaFile{SourceKey: e.Key, SourceRemoteID: e.RemoteID, Size: e.Size, Name: e.Name}
	task := Task{Bytes: 5, ETag: `"fixture-v1"`, SourceModified: e.Modified}
	reader, err := a.openSource(context.Background(), source, f, task)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(reader.Reader)
	reader.Reader.Close()
	if reader.Offset != 5 || reader.Total != int64(len(quarkFixturePayload)) || string(data) != quarkFixturePayload[5:] {
		t.Fatal("Quark resume failed")
	}
	fixture.mu.Lock()
	cookie := fixture.getCookies[0]
	fixture.mu.Unlock()
	if !strings.Contains(cookie, "fixture-updated") {
		t.Fatal("download used stale Cookie")
	}
	wrong := f
	wrong.SourceRemoteID = "foreign_file"
	if _, err := a.openSource(context.Background(), source, wrong, task); err == nil {
		t.Fatal("changed file identity accepted")
	}
	fixture.downloadRedirect = true
	if _, err := a.openSource(context.Background(), source, f, task); err == nil {
		t.Fatal("untrusted download redirect followed")
	}
	fixture.apiRedirect = true
	if _, err := a.list(context.Background(), source, "/"); err == nil {
		t.Fatal("API redirect followed")
	}
	fixture.apiRedirect = false
	fixture.expired = true
	_, err = a.list(context.Background(), source, "/")
	if err == nil || !strings.Contains(err.Error(), "Cookie") || strings.Contains(err.Error(), "fixture-session") || fixture.untrustedRequests != 0 {
		t.Fatal("expired login error leaked secrets or redirected", err)
	}
}

func TestQuarkValidatesCookiesRootAndDownloadHosts(t *testing.T) {
	for _, raw := range []string{"", "a=b", "__puus=", "__puus=x\r\nX-Leak: y"} {
		if validateQuarkCookie(raw) == nil {
			t.Fatal("invalid cookie accepted")
		}
	}
	for _, raw := range []string{"http://drive.quark.cn/file", "https://quark.cn.evil.invalid/file", "https://127.0.0.1/file", "https://quark.cn@evil.invalid/file", "https://quark.cn:8888/file"} {
		if validQuarkDownloadURL(raw) {
			t.Fatal("invalid download host accepted", raw)
		}
	}
	a, fixture := newQuarkFixture(t)
	source := addQuarkFixtureSource(t, a, "dir_movie")
	fixture.pages = nil
	if _, err := a.list(context.Background(), source, "/tv"); err == nil {
		t.Fatal("source root scope escaped")
	}
	if _, err := a.list(context.Background(), source, "/../tv"); err == nil {
		t.Fatal("path traversal accepted")
	}
	if strings.Contains(strings.Join(fixture.pages, ","), "dir_tv") {
		t.Fatal("folder outside selected root was accessed")
	}
}

func TestQuarkInteractivePreview(t *testing.T) {
	if os.Getenv("YINGKU_QUARK_PREVIEW") != "1" {
		t.Skip("Quark UI fixture is opt-in")
	}
	a, _ := newQuarkFixture(t)
	server := httptest.NewServer(a.handler())
	defer server.Close()
	fmt.Println("QUARK_PREVIEW=" + server.URL)
	<-time.After(20 * time.Minute)
}
