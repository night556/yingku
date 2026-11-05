package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This fixture serves only synthetic media records and local test artwork.
func TestCinemaInteractivePreview(t *testing.T) {
	if os.Getenv("YINGKU_CINEMA_PREVIEW") != "1" {
		t.Skip("interactive media UI fixture is opt-in")
	}
	a := testApp(t)
	video := filepath.Join(t.TempDir(), "界面测试样片.mp4")
	content := []byte("synthetic-video")
	if path := os.Getenv("YINGKU_QA_VIDEO"); path != "" {
		var err error
		content, err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(video, content, 0600); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 18; n++ {
		kind, title := "movie", fmt.Sprintf("示例电影 %02d", n)
		if n%3 == 0 {
			kind, title = "tv", fmt.Sprintf("示例剧集 %02d", n)
		}
		if n == 1 {
			title = "长夜之后 · 界面测试作品"
		}
		if n == 2 {
			title = "ORBIT & DAWN · TEST"
		}
		it := Item{ID: fmt.Sprintf("qa-item-%02d", n), Type: kind, Title: title, OriginalTitle: fmt.Sprintf("Cinema test %02d", n), Year: fmt.Sprint(2000 + n), Poster: fmt.Sprintf("qa:poster:%d", n), Backdrop: fmt.Sprintf("qa:backdrop:%d", n), Rating: 8.1, Genres: []string{"剧情", "悬疑"}, Overview: "这是界面测试用的占位作品，用来验证海报墙、详情页和不同屏幕的布局。它不会加入你的真实媒体库。", MetadataStatus: "ready", Created: fmt.Sprintf("2026-10-%02dT12:00:00Z", n)}
		details := map[string]any{"premiereDate": fmt.Sprintf("%s-09-20", it.Year), "runtime": 43, "status": "Running", "countries": []string{"中国"}, "languages": []string{"中文"}, "directors": []string{"测试导演"}, "people": []map[string]any{}}
		people := []map[string]any{}
		for i := 1; i <= 8; i++ {
			people = append(people, map[string]any{"name": fmt.Sprintf("示例演员 %02d", i), "type": "Actor", "role": fmt.Sprintf("角色 %d", i), "image": fmt.Sprintf("qa:person:%d", i)})
		}
		details["people"] = people
		if kind == "tv" {
			details["seasons"] = []map[string]any{{"number": 1, "year": it.Year, "poster": it.Poster, "premiereDate": it.Year + "-09-20"}, {"number": 2, "year": fmt.Sprint(2001 + n), "poster": fmt.Sprintf("qa:poster:%d", n+1), "premiereDate": fmt.Sprint(2001+n) + "-09-20"}}
		}
		raw, _ := json.Marshal(details)
		if err := json.Unmarshal(raw, &it.Details); err != nil {
			t.Fatal(err)
		}
		count := 1
		if kind == "tv" {
			count = 8
		}
		for ep := 1; ep <= count; ep++ {
			season, number := 1, ep
			if ep > 4 {
				season, number = 2, ep-4
			}
			file := MediaFile{ID: fmt.Sprintf("qa-file-%02d-%02d", n, ep), ItemID: it.ID, Name: fmt.Sprintf("界面测试 S%02dE%02d.mp4", season, number), Season: season, Episode: number, Destination: video, Status: "completed", Reference: true, Size: int64(len(content)), Duration: 300}
			if file.ID == os.Getenv("YINGKU_QA_PENDING_EPISODE") {
				file.Status = "queued"
			}
			if n >= 16 && ep == 1 {
				file.Position = 85
			}
			a.s.Files = append(a.s.Files, file)
			it.Episodes = append(it.Episodes, Episode{Season: season, Number: number, Name: fmt.Sprintf("界面测试 · 第 %d 集", number), Overview: "分集简介也会在这里展示。此内容仅用于测试详情页面的卡片排版、文件信息和播放入口。", Still: fmt.Sprintf("qa:still:%d", n+ep), AirDate: it.Year + "-09-20", Runtime: 43})
		}
		if n == 4 {
			it.Poster, it.Backdrop = "", ""
		}
		a.s.Items = append(a.s.Items, it)
	}
	handler := a.handler()
	if os.Getenv("YINGKU_QA_MISSING_FILE") == "1" {
		for i := range a.s.Files {
			if a.s.Files[i].ID == "qa-file-04-01" || a.s.Files[i].ID == "qa-file-05-01" {
				a.s.Files[i].Destination = filepath.Join(filepath.Dir(video), a.s.Files[i].ID+"-已移动.mp4")
			}
		}
		replacement := filepath.Join(filepath.Dir(video), "重新找到的视频.mp4")
		if err := os.WriteFile(replacement, content, 0600); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("RECOVERY_QA_NEW_PATH=%s\n", replacement)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/art" && strings.HasPrefix(r.URL.Query().Get("path"), "qa:") {
			seed := 0
			for _, v := range r.URL.Query().Get("path") {
				seed += int(v)
			}
			hue := (seed * 37) % 360
			w.Header().Set("Content-Type", "image/svg+xml")
			fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" width="800" height="1000" viewBox="0 0 800 1000"><defs><linearGradient id="g" x2="1" y2="1"><stop stop-color="hsl(%d,40%%,28%%)"/><stop offset="1" stop-color="#0a101d"/></linearGradient></defs><rect width="800" height="1000" fill="url(#g)"/><circle cx="570" cy="230" r="180" fill="hsl(%d,60%%,65%%)" opacity=".5"/><path d="M-50 840 270 260 700 1000Z" fill="hsl(%d,40%%,70%%)" opacity=".35"/><path d="M200 1000 620 400 940 1000Z" fill="#020813" opacity=".8"/><path d="M0 780 800 600" stroke="#fff" opacity=".15" stroke-width="2"/><text x="65" y="845" fill="#fff" font-family="sans-serif" font-size="42" letter-spacing="12">CINEMA</text><text x="65" y="905" fill="#fff" opacity=".5" font-family="sans-serif" font-size="20">UI TEST COLLECTION %d</text></svg>`, hue, (hue+35)%360, (hue+180)%360, seed%100)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	a.listenAddress = "127.0.0.1:" + u.Port()
	defer a.closeNetwork()
	fmt.Printf("CINEMA_QA=%s\n", server.URL)
	<-time.After(20 * time.Minute)
}
