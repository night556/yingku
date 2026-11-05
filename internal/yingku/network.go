package yingku

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"yingku/web"
)

type NetworkConfig struct {
	NoPassword bool   `json:"noPassword,omitempty"`
	Salt       string `json:"salt,omitempty"`
	Hash       string `json:"hash,omitempty"`
}

type webSession struct{ Expires time.Time }
type loginAttempt struct {
	Count int
	Since time.Time
}

func networkRequiresPassword(c NetworkConfig) bool {
	return !c.NoPassword && c.Hash != ""
}

func lanIPs() []net.IP {
	var result []net.IP
	interfaces, _ := net.Interfaces()
	for _, nic := range interfaces {
		if nic.Flags&net.FlagUp == 0 || nic.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, _ := nic.Addrs()
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err == nil && ip.To4() != nil && ip.IsPrivate() {
				result = append(result, ip.To4())
			}
		}
	}
	return result
}

func requestIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return nil
	}
	return net.ParseIP(host)
}

func localRequest(r *http.Request) bool {
	ip := requestIP(r)
	if ip == nil || !ip.IsLoopback() || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Forwarded-Host") != "" || r.Header.Get("X-Forwarded-Proto") != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	return strings.EqualFold(host, "localhost") || net.ParseIP(strings.Trim(host, "[]")).IsLoopback()
}

func (a *App) networkState(r *http.Request) map[string]any {
	a.mu.Lock()
	c, address := a.s.Config.Network, a.listenAddress
	publicURL, castBaseURL := a.effectivePublicURLLocked(), a.s.Config.CastBaseURL
	a.mu.Unlock()
	addresses := lanServiceURLs(address)
	if publicURL != "" {
		addresses = []string{publicURL}
	}
	return map[string]any{
		"listenAddress": address, "active": listenerAllowsLAN(address),
		"port": listenerPort(address), "localAddress": browserServiceURL(address),
		"passwordConfigured": c.Hash != "", "passwordRequired": networkRequiresPassword(c),
		"addresses": addresses, "local": localRequest(r),
		"publicURL": publicURL, "publicURLManaged": a.publicURL != "", "castBaseURL": castBaseURL,
	}
}

func passwordHash(password, salt string) (string, error) {
	key, err := pbkdf2.Key(sha256.New, password, []byte(salt), 210000, 32)
	return hex.EncodeToString(key), err
}

func (a *App) saveNetworkConfig(next NetworkConfig) error {
	if !next.NoPassword && (next.Hash == "" || next.Salt == "") {
		return errors.New("已选择密码保护，请设置至少 10 个字符的访问密码，或取消勾选“需要访问密码”")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	old := a.s.Config.Network
	a.s.Config.Network = next
	if err := a.saveLocked(); err != nil {
		a.s.Config.Network = old
		return err
	}
	if next.Hash != old.Hash || next.NoPassword != old.NoPassword {
		a.sessions = map[string]webSession{}
	}
	return nil
}

func (a *App) closeNetwork() {
	a.mu.Lock()
	a.listenAddress = ""
	a.sessions = nil
	a.casts = nil
	a.mu.Unlock()
}

func (a *App) authorizeNetwork(w http.ResponseWriter, r *http.Request) bool {
	if localRequest(r) {
		return true
	}
	ip := requestIP(r)
	a.mu.Lock()
	config, address := a.s.Config.Network, a.listenAddress
	a.mu.Unlock()
	if ip == nil || !ip.IsLoopback() && (!listenerAllowsLAN(address) || !(ip.IsPrivate() || ip.IsLinkLocalUnicast())) {
		http.Error(w, "仅允许本机或局域网访问", http.StatusForbidden)
		return false
	}
	if !networkRequiresPassword(config) {
		return true
	}
	// A TV receives an unpredictable, expiring URL for one video, never a login session.
	if (r.Method == "GET" || r.Method == "HEAD") && strings.HasPrefix(r.URL.Path, "/cast/") {
		return true
	}
	switch r.URL.Path {
	case "/login", "/login.js", "/style.css", "/favicon.svg":
		if r.Method == "GET" || r.Method == "HEAD" {
			return true
		}
	case "/api/session":
		if r.Method == "POST" {
			return true
		}
	}
	cookie, err := r.Cookie("yingku_session")
	if err == nil {
		a.mu.Lock()
		session, exists := a.sessions[cookie.Value]
		valid := exists && time.Now().Before(session.Expires)
		if exists && !valid {
			delete(a.sessions, cookie.Value)
		}
		a.mu.Unlock()
		if valid {
			return true
		}
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		jsonResponse(w, http.StatusUnauthorized, map[string]string{"error": "请先输入访问密码", "code": "login_required"})
	} else {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
	return false
}

func (a *App) networkRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/network", func(w http.ResponseWriter, r *http.Request) { jsonResponse(w, 200, a.networkState(r)) })
	mux.HandleFunc("POST /api/config/addresses", a.addressSettingsHandler)
	mux.HandleFunc("POST /api/network", func(w http.ResponseWriter, r *http.Request) {
		if !localRequest(r) {
			jsonResponse(w, 403, map[string]string{"error": "请在运行映库的电脑上修改局域网设置"})
			return
		}
		var body struct {
			Password        string `json:"password"`
			RequirePassword *bool  `json:"requirePassword"`
		}
		if err := readJSON(w, r, &body); err != nil {
			fail(w, err)
			return
		}
		a.mu.Lock()
		next := a.s.Config.Network
		a.mu.Unlock()
		if body.RequirePassword != nil {
			next.NoPassword = !*body.RequirePassword
		} else if body.Password != "" {
			next.NoPassword = false
		} else if next.Hash == "" {
			next.NoPassword = true
		}
		if body.Password != "" {
			if len([]rune(body.Password)) < 10 || len(body.Password) > 256 {
				fail(w, errors.New("访问密码需要至少 10 个字符，且不超过 256 字节"))
				return
			}
			salt := make([]byte, 24)
			if _, err := rand.Read(salt); err != nil {
				fail(w, err)
				return
			}
			next.Salt = hex.EncodeToString(salt)
			var err error
			next.Hash, err = passwordHash(body.Password, next.Salt)
			if err != nil {
				fail(w, err)
				return
			}
		}
		if err := a.saveNetworkConfig(next); err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, 200, a.networkState(r))
	})
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		noPassword := !networkRequiresPassword(a.s.Config.Network)
		a.mu.Unlock()
		if noPassword {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		b, _ := web.Files.ReadFile("login.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("POST /api/session", a.loginHandler)
	mux.HandleFunc("DELETE /api/session", func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie("yingku_session"); err == nil {
			a.mu.Lock()
			delete(a.sessions, cookie.Value)
			a.mu.Unlock()
		}
		http.SetCookie(w, &http.Cookie{Name: "yingku_session", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		jsonResponse(w, 200, map[string]bool{"ok": true})
	})
}

func (a *App) loginHandler(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		jsonResponse(w, 400, map[string]string{"error": "请求格式不正确"})
		return
	}
	ip := requestIP(r)
	if ip == nil {
		http.Error(w, "无效地址", 400)
		return
	}
	a.mu.Lock()
	noPassword := !networkRequiresPassword(a.s.Config.Network)
	a.mu.Unlock()
	if noPassword {
		jsonResponse(w, 200, map[string]bool{"ok": true})
		return
	}
	key := ip.String()
	a.mu.Lock()
	if a.loginAttempts == nil {
		a.loginAttempts = map[string]loginAttempt{}
	}
	for k, v := range a.loginAttempts {
		if time.Since(v.Since) > 5*time.Minute {
			delete(a.loginAttempts, k)
		}
	}
	attempt := a.loginAttempts[key]
	if attempt.Since.IsZero() {
		attempt.Since = time.Now()
	}
	if attempt.Count >= 10 || len(a.loginAttempts) >= 256 {
		a.mu.Unlock()
		jsonResponse(w, 429, map[string]string{"error": "尝试次数过多，请 5 分钟后再试"})
		return
	}
	attempt.Count++
	a.loginAttempts[key] = attempt
	c := a.s.Config.Network
	a.mu.Unlock()
	var body struct {
		Password string `json:"password"`
	}
	if err := readJSON(w, r, &body); err != nil {
		fail(w, err)
		return
	}
	if len(body.Password) > 256 {
		jsonResponse(w, 401, map[string]string{"error": "访问密码不正确"})
		return
	}
	hash, err := passwordHash(body.Password, c.Salt)
	if err != nil || c.Hash == "" || subtle.ConstantTimeCompare([]byte(hash), []byte(c.Hash)) != 1 {
		jsonResponse(w, 401, map[string]string{"error": "访问密码不正确"})
		return
	}
	token := uid() + uid()
	a.mu.Lock()
	if a.s.Config.Network.Hash != c.Hash || a.s.Config.Network.NoPassword != c.NoPassword {
		a.mu.Unlock()
		jsonResponse(w, 401, map[string]string{"error": "设置已改变，请重新登录"})
		return
	}
	if a.sessions == nil {
		a.sessions = map[string]webSession{}
	}
	for k, v := range a.sessions {
		if time.Now().After(v.Expires) {
			delete(a.sessions, k)
		}
	}
	if len(a.sessions) >= 128 {
		a.mu.Unlock()
		jsonResponse(w, 429, map[string]string{"error": "登录设备数量已达上限"})
		return
	}
	a.sessions[token] = webSession{Expires: time.Now().Add(7 * 24 * time.Hour)}
	delete(a.loginAttempts, key)
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "yingku_session", Value: token, Path: "/", MaxAge: 7 * 24 * 3600, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
	jsonResponse(w, 200, map[string]bool{"ok": true})
}
