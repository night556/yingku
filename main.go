package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

//go:embed web/*
var frontend embed.FS

const appVersion = "0.13.17"

func main() {
	options, err := parseRuntimeOptions(os.Args[1:], os.Getenv, os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	if options.Healthcheck {
		if err := serviceHealth(options.Address); err != nil {
			log.Fatal(err)
		}
		return
	}
	addr, data, open := &options.Address, &options.DataDir, &options.OpenBrowser
	listener, err := listenService(*addr)
	if err != nil {
		if isAddressInUse(err) {
			running, probeErr := runningInstance(*addr, *data)
			if running {
				fmt.Printf("映库已在运行，打开已有服务：%s\n", browserServiceURL(*addr))
				if *open {
					openBrowser(browserServiceURL(*addr))
				}
				return
			}
			if errors.Is(probeErr, errOtherLibrary) {
				log.Fatalf("另一个媒体库的映库正在使用 %s，请用 -addr 为当前媒体库指定一个未被使用的本机端口。", *addr)
			}
			log.Fatalf("端口 %s 已被其他程序占用，无法启动映库。请关闭占用端口的程序，或用 -addr 指定一个未被使用的本机端口。", *addr)
		}
		log.Fatal("启动失败：", err)
	}
	defer listener.Close()
	app, err := newAppWithOptions(*data, appOptions{MediaDir: options.MediaDir, PublicURL: options.PublicURL})
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	server := &http.Server{Handler: app.handler(), ReadHeaderTimeout: 8 * time.Second, ReadTimeout: 60 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 32 << 10}
	app.listenAddress = listener.Addr().String()
	go app.startWorkers(ctx)
	go app.backfillDetails(ctx)
	go func() {
		<-ctx.Done()
		app.mu.Lock()
		app.closing = true
		for _, stop := range app.active {
			stop()
		}
		app.mu.Unlock()
		shutdown, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_ = server.Shutdown(shutdown)
		app.closeNetwork()
	}()
	fmt.Printf("\n  映库 YINGKU · %s\n  监听地址：%s\n  本机地址：%s\n  数据目录：%s\n  按 Ctrl+C 停止。\n\n", appVersion, listener.Addr(), browserServiceURL(listener.Addr().String()), app.dir)
	fmt.Printf("  默认下载目录：%s\n", app.s.Config.MediaRoot)
	if app.publicURL != "" {
		fmt.Printf("  部署访问地址：%s\n", app.publicURL)
	}
	for _, address := range lanServiceURLs(listener.Addr().String()) {
		fmt.Printf("  手机地址：%s\n", address)
	}
	if *open {
		openBrowser(browserServiceURL(listener.Addr().String()))
	}
	if err = server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	// Let cancelled workers flush their checkpoints before the process exits.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		app.mu.Lock()
		n := len(app.active)
		app.mu.Unlock()
		if n == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
}
func openBrowser(address string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", address)
	case "darwin":
		cmd = exec.Command("open", address)
	default:
		cmd = exec.Command("xdg-open", address)
	}
	if err := cmd.Start(); err == nil {
		go cmd.Wait()
	}
}
func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, err error) {
	jsonResponse(w, 400, map[string]any{"error": err.Error()})
}
func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	if err := d.Decode(v); err != nil {
		return errors.New("请求内容格式不正确或过大")
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return errors.New("请求包含多余内容")
	}
	return nil
}
func (a *App) handler() http.Handler {
	mux := http.NewServeMux()
	a.cloudAuthRoutes(mux)
	a.scanRoutes(mux)
	a.networkRoutes(mux)
	a.dlnaRoutes(mux)
	a.playbackRoutes(mux)
	a.fileAvailabilityRoutes(mux)
	a.libraryRemovalRoutes(mux)
	a.taskBatchRoutes(mux)
	mux.HandleFunc("GET /healthz", a.healthHandler)
	mux.HandleFunc("GET /api/instance", func(w http.ResponseWriter, r *http.Request) {
		id, err := dataDirectoryID(a.dir)
		if err != nil {
			jsonResponse(w, 500, map[string]string{"error": "无法确认媒体库位置"})
			return
		}
		jsonResponse(w, 200, instanceInfo{Service: "yingku", Protocol: 1, DataID: id})
	})
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		a.scheduleFileAvailability("")
		jsonResponse(w, 200, a.publicState())
	})
	mux.HandleFunc("POST /api/config", a.configHandler)
	mux.HandleFunc("POST /api/config/test", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		configured := a.s.Config.TMDBSecret != ""
		a.mu.Unlock()
		if !configured {
			if _, err := a.searchWiki(r.Context(), "movie", "流浪地球"); err != nil {
				fail(w, err)
				return
			}
			jsonResponse(w, 200, map[string]any{"ok": true, "provider": "public"})
			return
		}
		var out map[string]any
		if err := a.tmdb(r.Context(), "/configuration", nil, &out); err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, 200, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/sources", a.sourceHandler)
	mux.HandleFunc("DELETE /api/sources/{id}", func(w http.ResponseWriter, r *http.Request) {
		result, err := a.removeSource(r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, 200, result)
	})
	mux.HandleFunc("GET /api/sources/{id}/list", func(w http.ResponseWriter, r *http.Request) {
		s, err := a.source(r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		key, err := cleanKey(r.URL.Query().Get("path"))
		if err != nil {
			fail(w, err)
			return
		}
		entries, err := a.list(r.Context(), s, key)
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, 200, map[string]any{"path": key, "entries": entries, "guess": guessTitle(filepath.Base(key))})
	})
	mux.HandleFunc("GET /api/metadata/search", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		if len(query) > 300 {
			fail(w, errors.New("搜索词过长"))
			return
		}
		results, err := a.searchMetadata(r.Context(), r.URL.Query().Get("type"), query)
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, 200, map[string]any{"results": results})
	})
	mux.HandleFunc("POST /api/import/preview", func(w http.ResponseWriter, r *http.Request) {
		var body ImportRequest
		if err := readJSON(w, r, &body); err != nil {
			fail(w, err)
			return
		}
		p, err := a.preview(r.Context(), body)
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, 200, p)
	})
	mux.HandleFunc("POST /api/import", func(w http.ResponseWriter, r *http.Request) {
		var body ImportRequest
		if err := readJSON(w, r, &body); err != nil {
			fail(w, err)
			return
		}
		result, err := a.importMedia(r.Context(), body)
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, 201, result)
	})
	mux.HandleFunc("POST /api/tasks/{id}/{action}", func(w http.ResponseWriter, r *http.Request) {
		if err := a.controlTask(r.PathValue("id"), r.PathValue("action")); err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, 200, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/items/{id}/metadata", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			TMDBID     int    `json:"tmdbId"`
			Provider   string `json:"provider"`
			ExternalID string `json:"externalId"`
			Auto       bool   `json:"auto"`
		}
		if err := readJSON(w, r, &body); err != nil {
			fail(w, err)
			return
		}
		if _, err := a.item(r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		if body.Auto {
			go a.refreshMetadata(r.PathValue("id"), "", "", 0, true)
			jsonResponse(w, 202, map[string]any{"ok": true})
			return
		}
		if body.Provider == "tvmaze" || body.Provider == "wikipedia" {
			if body.ExternalID == "" {
				fail(w, errors.New("请搜索并选择作品"))
				return
			}
			go a.refreshMetadata(r.PathValue("id"), body.Provider, body.ExternalID, 0, false)
			jsonResponse(w, 202, map[string]any{"ok": true})
			return
		}
		if body.TMDBID < 1 {
			fail(w, errors.New("请搜索并选择作品"))
			return
		}
		go a.refreshMetadata(r.PathValue("id"), "tmdb", "", body.TMDBID, false)
		jsonResponse(w, 202, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/files/{id}/progress", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Position float64 `json:"position"`
			Duration float64 `json:"duration"`
			Token    string  `json:"token"`
		}
		if err := readJSON(w, r, &body); err != nil {
			fail(w, err)
			return
		}
		if body.Position < 0 || body.Duration < 0 || body.Position > 1e8 || body.Duration > 1e8 {
			fail(w, errors.New("播放进度无效"))
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		for i := range a.s.Files {
			if a.s.Files[i].ID == r.PathValue("id") {
				if a.s.Files[i].ProgressToken != "" && body.Token != a.s.Files[i].ProgressToken {
					jsonResponse(w, 200, map[string]any{"ok": true, "ignored": true})
					return
				}
				before := a.s.Files[i]
				a.s.Files[i].Position = body.Position
				a.s.Files[i].Duration = body.Duration
				a.s.Files[i].LastPlayed = time.Now().UTC().Format(time.RFC3339Nano)
				if err := a.saveLocked(); err != nil {
					a.s.Files[i] = before
					fail(w, err)
					return
				}
				jsonResponse(w, 200, map[string]any{"ok": true})
				return
			}
		}
		fail(w, errors.New("找不到文件"))
	})
	mux.HandleFunc("POST /api/files/{id}/video-info", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		}
		if err := readJSON(w, r, &body); err != nil {
			fail(w, err)
			return
		}
		if body.Width < 1 || body.Height < 1 || body.Width > 32768 || body.Height > 32768 {
			fail(w, errors.New("视频分辨率无效"))
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		for i := range a.s.Files {
			file := &a.s.Files[i]
			if file.ID != r.PathValue("id") {
				continue
			}
			if file.Status != "completed" {
				fail(w, errors.New("视频尚未入库完成"))
				return
			}
			if file.VideoWidth != body.Width || file.VideoHeight != body.Height {
				before := *file
				file.VideoWidth, file.VideoHeight = body.Width, body.Height
				if err := a.saveLocked(); err != nil {
					*file = before
					fail(w, err)
					return
				}
			}
			jsonResponse(w, 200, map[string]any{"ok": true})
			return
		}
		fail(w, errors.New("找不到视频文件"))
	})
	mux.HandleFunc("GET /api/files/{id}/stream", func(w http.ResponseWriter, r *http.Request) {
		f, err := a.file(r.PathValue("id"))
		if err != nil {
			http.Error(w, "找不到文件", 404)
			return
		}
		if f.Status != "completed" {
			http.Error(w, "下载尚未完成", 409)
			return
		}
		info, err := os.Lstat(f.Destination)
		if err != nil || !info.Mode().IsRegular() {
			http.Error(w, "本地文件已移动或无法读取", 404)
			return
		}
		file, err := os.Open(f.Destination)
		if err != nil {
			http.Error(w, "文件无法读取", 404)
			return
		}
		defer file.Close()
		types := map[string]string{".mp4": "video/mp4", ".m4v": "video/mp4", ".webm": "video/webm", ".mkv": "video/x-matroska", ".mov": "video/quicktime", ".ts": "video/mp2t", ".avi": "video/x-msvideo"}
		if typ := types[strings.ToLower(filepath.Ext(f.Destination))]; typ != "" {
			w.Header().Set("Content-Type", typ)
		}
		w.Header().Set("Cache-Control", "no-store")
		http.ServeContent(w, r, filepath.Base(f.Destination), info.ModTime(), file)
	})
	mux.HandleFunc("POST /api/files/{id}/subtitles", func(w http.ResponseWriter, r *http.Request) {
		name, err := url.QueryUnescape(r.Header.Get("X-Filename"))
		if err != nil {
			name = "subtitle.srt"
		}
		b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
		if err != nil {
			fail(w, errors.New("字幕不能超过 8 MB"))
			return
		}
		if err = a.saveSubtitle(r.PathValue("id"), filepath.Base(name), b); err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, 201, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /api/files/{id}/subtitles/{index}", func(w http.ResponseWriter, r *http.Request) {
		f, err := a.file(r.PathValue("id"))
		index, e := strconv.Atoi(r.PathValue("index"))
		if err != nil || e != nil || index < 0 || index >= len(f.Subtitles) {
			http.NotFound(w, r)
			return
		}
		b, err := os.ReadFile(f.Subtitles[index].Path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		s := strings.TrimPrefix(string(b), "\ufeff")
		if strings.EqualFold(filepath.Ext(f.Subtitles[index].Name), ".srt") {
			s = "WEBVTT\n\n" + regexp.MustCompile(`(\d{2}:\d{2}:\d{2}),(\d{3})`).ReplaceAllString(s, "$1.$2")
		}
		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
		_, _ = io.WriteString(w, s)
	})
	mux.HandleFunc("GET /api/art", a.artwork)
	assets, _ := fs.Sub(frontend, "web")
	static := http.FileServer(http.FS(assets))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		if r.URL.Path != "/scan-manual.js" && r.URL.Path != "/player-keyboard.js" && r.URL.Path != "/tasks.js" && r.URL.Path != "/tasks.css" && r.URL.Path != "/" && r.URL.Path != "/cinema.js" && r.URL.Path != "/cinema.css" && r.URL.Path != "/player.js" && r.URL.Path != "/player-gestures.js" && r.URL.Path != "/player.css" && r.URL.Path != "/cloud-auth.js" && r.URL.Path != "/cloud-auth.css" && r.URL.Path != "/app.js" && r.URL.Path != "/scan-matching.js" && r.URL.Path != "/scan-matching.css" && r.URL.Path != "/scan.js" && r.URL.Path != "/casting.js" && r.URL.Path != "/network.js" && r.URL.Path != "/login.js" && r.URL.Path != "/style.css" && r.URL.Path != "/favicon.svg" {
			http.NotFound(w, r)
			return
		}
		static.ServeHTTP(w, r)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; media-src 'self' blob:; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		// The proxy may serve any domain; a display URL is not an access allowlist.
		if _, err := normalizePublicURL("http://" + r.Host); err != nil {
			http.Error(w, "无效的访问地址", 403)
			return
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" && ((r.Method != "GET" && r.Method != "HEAD") || r.Header.Get("Sec-Fetch-Mode") != "navigate") {
			http.Error(w, "拒绝跨站请求", 403)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host || (u.Scheme != "http" && u.Scheme != "https") {
				http.Error(w, "拒绝跨站请求", 403)
				return
			}
		}
		if !a.authorizeNetwork(w, r) {
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && !(r.Method == "POST" && r.URL.Path == "/api/session") && r.Header.Get("X-App-Token") != a.csrf {
			jsonResponse(w, 403, map[string]any{"error": "页面已过期，请刷新后重试"})
			return
		}
		mux.ServeHTTP(w, r)
	})
}
func (a *App) configHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MediaRoot    string           `json:"mediaRoot"`
		Concurrency  int              `json:"concurrency"`
		TMDBToken    string           `json:"tmdbToken"`
		ClearTMDB    bool             `json:"clearTmdb"`
		ExportNFO    bool             `json:"exportNfo"`
		AutoMetadata *bool            `json:"autoMetadata"`
		AI           *AISettingsInput `json:"ai"`
	}
	if err := readJSON(w, r, &body); err != nil {
		fail(w, err)
		return
	}
	root := strings.TrimSpace(body.MediaRoot)
	if !filepath.IsAbs(root) {
		fail(w, errors.New("媒体目录需要是绝对路径"))
		return
	}
	if body.Concurrency < 1 || body.Concurrency > 4 {
		fail(w, errors.New("同时下载数需要是 1 至 4"))
		return
	}
	root, err := filepath.Abs(root)
	if err != nil {
		fail(w, err)
		return
	}
	if a.managedMediaDir != "" && root != a.managedMediaDir {
		fail(w, errors.New("默认保存位置由启动配置固定，请修改 YINGKU_MEDIA_DIR 或 -media 后重启"))
		return
	}
	if err = os.MkdirAll(root, 0755); err != nil {
		fail(w, fmt.Errorf("无法创建媒体目录：%w", err))
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	next := a.s.Config
	next.MediaRoot = root
	next.Concurrency = body.Concurrency
	next.ExportNFO = body.ExportNFO
	if body.AutoMetadata != nil {
		next.AutoMetadata = *body.AutoMetadata
	}
	if body.ClearTMDB {
		next.TMDBSecret = ""
	} else if strings.TrimSpace(body.TMDBToken) != "" {
		next.TMDBSecret, err = a.encrypt(strings.TrimSpace(body.TMDBToken))
		if err != nil {
			fail(w, err)
			return
		}
	}
	if body.AI != nil {
		next.AI, err = a.nextAIConfig(next.AI, *body.AI)
		if err != nil {
			fail(w, err)
			return
		}
	}
	old := a.s.Config
	a.s.Config = next
	if err = a.saveLocked(); err != nil {
		a.s.Config = old
		fail(w, err)
		return
	}
	jsonResponse(w, 200, map[string]any{"ok": true})
}
func (a *App) sourceHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Source
		Password     string `json:"password"`
		Cookie       string `json:"cookie"`
		ClientSecret string `json:"clientSecret"`
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		AuthCode     string `json:"authCode"`
		AuthSession  string `json:"authSession"`
	}
	if err := readJSON(w, r, &body); err != nil {
		fail(w, err)
		return
	}
	s := body.Source
	s.Pending = true
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" || len([]rune(s.Name)) > 80 {
		fail(w, errors.New("请填写 1 至 80 字的来源名称"))
		return
	}
	s.Secret = ""
	c := Credentials{}
	if s.ID != "" {
		old, err := a.source(s.ID)
		if err != nil {
			fail(w, err)
			return
		}
		if old.Type != s.Type {
			fail(w, errors.New("已有来源不能更改类型"))
			return
		}
		c, err = a.credentials(old)
		if err != nil {
			fail(w, err)
			return
		}
		s.Created = old.Created
	} else {
		s.ID = uid()
		s.Created = now()
	}
	if body.Password != "" {
		c.Password = body.Password
	}
	if body.ClientSecret != "" {
		c.ClientSecret = body.ClientSecret
	}
	if body.AccessToken != "" {
		c.AccessToken = strings.TrimSpace(body.AccessToken)
		c.Expires = 0
	}
	if body.RefreshToken != "" {
		c.RefreshToken = strings.TrimSpace(body.RefreshToken)
	}
	authSaved := false
	if body.AuthSession != "" {
		var err error
		c, s.ClientID, err = a.claimCloudAuth(body.AuthSession, s.Type, body.ID)
		if err != nil {
			fail(w, err)
			return
		}
		defer func() { a.finishCloudAuth(body.AuthSession, authSaved) }()
	}
	switch s.Type {
	case "local":
		if !filepath.IsAbs(s.Root) {
			fail(w, errors.New("本地来源需要填写绝对路径"))
			return
		}
		root, err := filepath.Abs(s.Root)
		if err != nil {
			fail(w, err)
			return
		}
		s.Root = root
	case "webdav":
		if _, err := davURL(s, "/"); err != nil {
			fail(w, err)
			return
		}
		s.Root = "/"
	case "quark":
		if body.Cookie != "" && body.AuthSession == "" {
			c.Cookie = strings.TrimSpace(body.Cookie)
		}
		if err := validateQuarkCookie(c.Cookie); err != nil {
			fail(w, err)
			return
		}
		s.Root = strings.TrimSpace(s.Root)
		if s.Root == "" || s.Root == "/" {
			s.Root = "0"
		}
		if !quarkIDRE.MatchString(s.Root) {
			fail(w, errors.New("夸克根文件夹 ID 无效，浏览整个网盘请填 0"))
			return
		}
		s.URL, s.Username, s.ClientID = "", "", ""
		c = Credentials{Cookie: c.Cookie}
	case "baidu":
		s.Root = pathClean(s.Root)
		if body.AuthCode != "" && body.AuthSession == "" {
			if s.ClientID == "" || c.ClientSecret == "" {
				fail(w, errors.New("请先填写自己的百度应用 App Key 和 Secret Key"))
				return
			}
			q := url.Values{"grant_type": {"authorization_code"}, "code": {strings.TrimSpace(body.AuthCode)}, "client_id": {s.ClientID}, "client_secret": {c.ClientSecret}, "redirect_uri": {"oob"}}
			req, _ := http.NewRequestWithContext(r.Context(), "POST", "https://openapi.baidu.com/oauth/2.0/token", strings.NewReader(q.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			var result struct {
				Access  string `json:"access_token"`
				Refresh string `json:"refresh_token"`
				Expires int64  `json:"expires_in"`
				Error   string `json:"error"`
			}
			if err := a.requestPrivateJSON(req, &result); err != nil {
				fail(w, err)
				return
			}
			if result.Access == "" {
				fail(w, errors.New("授权码交换失败，请检查应用回调是否允许 oob，并重新获取授权码"))
				return
			}
			c.AccessToken = result.Access
			c.RefreshToken = result.Refresh
			c.Expires = time.Now().Unix() + result.Expires
		}
		if c.AccessToken == "" && c.RefreshToken == "" {
			fail(w, errors.New("请完成百度授权，或填写自己的访问令牌"))
			return
		}
	default:
		fail(w, errors.New("不支持的来源类型"))
		return
	}
	secret, err := a.encrypt(c)
	if err != nil {
		fail(w, err)
		return
	}
	s.Secret = secret
	if s.Type == "baidu" && c.AccessToken == "" {
		c, err = a.refreshBaidu(r.Context(), s)
		if err != nil {
			fail(w, err)
			return
		}
		s.Secret, err = a.encrypt(c)
		if err != nil {
			fail(w, err)
			return
		}
	}
	if s.Type == "quark" {
		_, err = a.listQuark(r.Context(), &s, "/")
	} else {
		_, err = a.list(r.Context(), s, "/")
	}
	if err != nil {
		fail(w, err)
		return
	}
	s.Pending = false
	a.mu.Lock()
	defer a.mu.Unlock()
	found := false
	for i := range a.s.Sources {
		if a.s.Sources[i].ID == s.ID {
			a.s.Sources[i] = s
			found = true
			break
		}
	}
	if !found {
		a.s.Sources = append(a.s.Sources, s)
	}
	if err = a.saveLocked(); err != nil {
		fail(w, err)
		return
	}
	authSaved = true
	jsonResponse(w, 201, map[string]any{"id": s.ID})
}
func pathClean(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	k, err := cleanKey(p)
	if err != nil {
		return "/"
	}
	return k
}
