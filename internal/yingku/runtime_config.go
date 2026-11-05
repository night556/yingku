package yingku

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type runtimeOptions struct {
	Address, DataDir, MediaDir, PublicURL string
	OpenBrowser, Healthcheck              bool
}

// Command-line flags override environment variables; desktop defaults stay intact.
func parseRuntimeOptions(args []string, getenv func(string) string, output io.Writer) (runtimeOptions, error) {
	value := func(name, fallback string) string {
		if v := strings.TrimSpace(getenv(name)); v != "" {
			return v
		}
		return fallback
	}
	var options runtimeOptions
	flags := flag.NewFlagSet("yingku", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&options.Address, "addr", value("YINGKU_ADDR", "0.0.0.0:8787"), "服务监听地址（YINGKU_ADDR）")
	flags.StringVar(&options.DataDir, "data", value("YINGKU_DATA_DIR", "data"), "配置、密钥与任务资料目录（YINGKU_DATA_DIR）")
	flags.StringVar(&options.MediaDir, "media", value("YINGKU_MEDIA_DIR", ""), "固定默认下载目录；留空沿用网页设置（YINGKU_MEDIA_DIR）")
	flags.StringVar(&options.PublicURL, "public-url", value("YINGKU_PUBLIC_URL", ""), "可选的访问地址展示；不限制域名或影响反代访问（YINGKU_PUBLIC_URL）")
	flags.BoolVar(&options.OpenBrowser, "open", false, "启动后打开浏览器")
	flags.BoolVar(&options.Healthcheck, "healthcheck", false, "检查已运行的服务，然后退出")
	if err := flags.Parse(args); err != nil {
		return options, err
	}
	if flags.NArg() != 0 {
		return options, errors.New("不支持位置参数，请使用 -data、-media 等选项")
	}
	if strings.TrimSpace(options.DataDir) == "" {
		return options, errors.New("资料目录不能为空")
	}
	var err error
	options.PublicURL, err = normalizePublicURL(options.PublicURL)
	return options, err
}

func normalizePublicURL(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("访问地址需要是完整的 HTTP 或 HTTPS 地址，不带账号、子路径或查询参数")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", errors.New("访问地址的端口需要是 1 至 65535")
		}
	}
	u.Host = strings.ToLower(u.Host)
	u.Path, u.RawPath = "", ""
	return u.String(), nil
}

// Caller holds a.mu. Startup configuration only overrides the displayed address.
func (a *App) effectivePublicURLLocked() string {
	if a.publicURL != "" {
		return a.publicURL
	}
	return a.s.Config.PublicURL
}

func (a *App) addressSettingsHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PublicURL   string `json:"publicURL"`
		CastBaseURL string `json:"castBaseURL"`
	}
	if err := readJSON(w, r, &body); err != nil {
		fail(w, err)
		return
	}
	publicURL, err := normalizePublicURL(body.PublicURL)
	if err != nil {
		fail(w, fmt.Errorf("访问地址：%w", err))
		return
	}
	castBaseURL, err := normalizePublicURL(body.CastBaseURL)
	if err != nil {
		fail(w, fmt.Errorf("投屏回连地址：%w", err))
		return
	}
	a.mu.Lock()
	if a.publicURL != "" && publicURL != a.publicURL {
		a.mu.Unlock()
		fail(w, errors.New("访问地址展示由启动配置固定，请修改 YINGKU_PUBLIC_URL 或 -public-url"))
		return
	}
	old := a.s.Config
	if a.publicURL == "" {
		a.s.Config.PublicURL = publicURL
	}
	a.s.Config.CastBaseURL = castBaseURL
	if err := a.saveLocked(); err != nil {
		a.s.Config = old
		a.mu.Unlock()
		fail(w, err)
		return
	}
	a.mu.Unlock()
	jsonResponse(w, http.StatusOK, a.networkState(r))
}

func (a *App) healthHandler(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	closing := a.closing
	a.mu.Unlock()
	if closing {
		jsonResponse(w, http.StatusServiceUnavailable, map[string]string{"status": "stopping"})
		return
	}
	jsonResponse(w, http.StatusOK, map[string]string{"status": "ok"})
}

func serviceHealth(address string) error {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	response, err := client.Get(browserServiceURL(address) + "/healthz")
	if err != nil {
		return fmt.Errorf("服务未就绪：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("服务健康检查失败：HTTP %d", response.StatusCode)
	}
	var status struct {
		Status string `json:"status"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&status) != nil || status.Status != "ok" {
		return errors.New("未收到映库的健康状态")
	}
	return nil
}
