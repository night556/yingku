package yingku

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

type BaiduAppCredentials struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

type CloudAuthSession struct {
	ID, Provider, SourceID, ClientID, Status, LoginURL, UserCode, Error string
	ExpiresAt                                                           time.Time
	Interval                                                            int
	deviceCode, qrImageURL, secret, sourceIdentity                      string
	client                                                              *http.Client
	nextPoll                                                            time.Time
	polling                                                             bool
	cancel                                                              context.CancelFunc
}

type CloudAuthView struct {
	ID        string `json:"id"`
	Provider  string `json:"provider"`
	Status    string `json:"status"`
	LoginURL  string `json:"loginUrl,omitempty"`
	UserCode  string `json:"userCode,omitempty"`
	QRURL     string `json:"qrUrl,omitempty"`
	ExpiresAt string `json:"expiresAt"`
	Interval  int    `json:"interval"`
	Error     string `json:"error,omitempty"`
}

func (s *CloudAuthSession) view() CloudAuthView {
	v := CloudAuthView{ID: s.ID, Provider: s.Provider, Status: s.Status, ExpiresAt: s.ExpiresAt.UTC().Format(time.RFC3339), Interval: s.Interval, Error: s.Error}
	if s.Status == "pending" {
		v.LoginURL, v.UserCode = s.LoginURL, s.UserCode
		if s.qrImageURL != "" || s.Provider == "quark" && s.LoginURL != "" {
			v.QRURL = "/api/cloud-auth/" + s.ID + "/qr"
		}
	}
	return v
}

func (s *CloudAuthSession) clear(status string) {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.Status, s.secret, s.deviceCode, s.qrImageURL, s.LoginURL, s.UserCode = status, "", "", "", "", ""
	s.client = nil
}

func (a *App) baiduAppCredentials(sourceID string) (BaiduAppCredentials, error) {
	a.mu.Lock()
	global := a.s.Config.BaiduAppSecret
	sources := append([]Source{}, a.s.Sources...)
	a.mu.Unlock()
	fromSource := func(s Source) BaiduAppCredentials {
		c, err := a.credentials(s)
		if err == nil && s.Type == "baidu" && s.ClientID != "" && c.ClientSecret != "" {
			return BaiduAppCredentials{ClientID: s.ClientID, ClientSecret: c.ClientSecret}
		}
		return BaiduAppCredentials{}
	}
	if sourceID != "" {
		for _, s := range sources {
			if s.ID == sourceID {
				if c := fromSource(s); c.ClientID != "" {
					return c, nil
				}
			}
		}
	}
	if global != "" {
		var c BaiduAppCredentials
		if a.decrypt(global, &c) == nil && c.ClientID != "" && c.ClientSecret != "" {
			return c, nil
		}
	}
	if id, key := strings.TrimSpace(os.Getenv("YINGKU_BAIDU_CLIENT_ID")), strings.TrimSpace(os.Getenv("YINGKU_BAIDU_CLIENT_SECRET")); id != "" && key != "" {
		return BaiduAppCredentials{ClientID: id, ClientSecret: key}, nil
	}
	var found BaiduAppCredentials
	for _, s := range sources {
		c := fromSource(s)
		if c.ClientID == "" {
			continue
		}
		if found.ClientID != "" && (found.ClientID != c.ClientID || found.ClientSecret != c.ClientSecret) {
			return BaiduAppCredentials{}, errors.New("存在多份百度应用配置，请在高级选项中指定一次项目应用")
		}
		found = c
	}
	if found.ClientID != "" {
		return found, nil
	}
	return BaiduAppCredentials{}, errors.New("首次部署需要配置一次百度应用 App Key 和 Secret Key，之后即可登录授权")
}

func cloudJSON(ctx context.Context, client *http.Client, method, endpoint string, q url.Values, form url.Values, out any) error {
	if q != nil {
		endpoint += "?" + q.Encode()
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return errors.New("无法生成登录请求")
	}
	req.Header.Set("User-Agent", quarkUserAgent)
	if strings.HasSuffix(req.URL.Hostname(), ".quark.cn") {
		req.Header.Set("Referer", quarkReferer)
	}
	req.Header.Set("Accept", "application/json")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("登录服务无法连接，请检查网络后重试")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 400 {
		return fmt.Errorf("登录服务返回 HTTP %d，请稍后重试", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 || json.Unmarshal(data, out) != nil {
		return errors.New("登录服务返回的数据无效，请重试")
	}
	return nil
}

func baiduLoginURL(raw string, image bool) string {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Host != "openapi.baidu.com" || u.Fragment != "" || u.Scheme != "https" && u.Scheme != "http" {
		return ""
	}
	if image && !strings.HasPrefix(u.Path, "/device/qrcode/") || !image && u.Path != "/device" && u.Path != "/device/" {
		return ""
	}
	u.Scheme = "https"
	return u.String()
}

func cloudLoginRequestID() string {
	id := (uid() + uid())[:32]
	return id[:8] + "-" + id[8:12] + "-4" + id[13:16] + "-8" + id[17:20] + "-" + id[20:]
}

func (a *App) startCloudAuth(ctx context.Context, provider, sourceID string) (CloudAuthView, error) {
	if provider != "baidu" && provider != "quark" {
		return CloudAuthView{}, errors.New("此来源不支持登录授权")
	}
	identity := ""
	if sourceID != "" {
		s, err := a.source(sourceID)
		if err != nil || s.Type != provider {
			return CloudAuthView{}, errors.New("来源与登录方式不一致")
		}
		identity = scanSourceIdentity(s)
	}
	var app BaiduAppCredentials
	var err error
	if provider == "baidu" {
		app, err = a.baiduAppCredentials(sourceID)
		if err != nil {
			return CloudAuthView{}, err
		}
	}
	jar, _ := cookiejar.New(nil)
	client := *a.client
	client.Timeout, client.Jar = 20*time.Second, jar
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	s := &CloudAuthSession{ID: uid() + uid(), Provider: provider, SourceID: sourceID, ClientID: app.ClientID, Status: "starting", Interval: 3, ExpiresAt: time.Now().Add(5 * time.Minute), sourceIdentity: identity, client: &client}
	a.mu.Lock()
	if a.cloudAuth == nil {
		a.cloudAuth = map[string]*CloudAuthSession{}
	}
	for id, old := range a.cloudAuth {
		if time.Now().After(old.ExpiresAt.Add(time.Minute)) {
			old.clear("expired")
			delete(a.cloudAuth, id)
		}
	}
	if len(a.cloudAuth) >= 32 {
		a.mu.Unlock()
		return CloudAuthView{}, errors.New("登录会话较多，请关闭旧登录或稍后再试")
	}
	reservation := *s
	a.cloudAuth[s.ID] = &reservation
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if provider == "baidu" {
		var r struct {
			DeviceCode      string `json:"device_code"`
			UserCode        string `json:"user_code"`
			VerificationURL string `json:"verification_url"`
			QRURL           string `json:"qrcode_url"`
			Expires         int    `json:"expires_in"`
			Interval        int    `json:"interval"`
			Error           string `json:"error"`
		}
		err = cloudJSON(ctx, &client, "GET", "https://openapi.baidu.com/oauth/2.0/device/code", url.Values{"response_type": {"device_code"}, "client_id": {app.ClientID}, "scope": {"basic,netdisk"}}, nil, &r)
		if err == nil && (r.Error != "" || r.DeviceCode == "" || r.UserCode == "" || r.Expires <= 0 || baiduLoginURL(r.VerificationURL, false) == "") {
			err = errors.New("百度设备授权未能开启，请检查应用是否开通网盘及设备授权权限")
		}
		if err == nil {
			s.deviceCode, s.UserCode, s.LoginURL, s.qrImageURL = r.DeviceCode, r.UserCode, baiduLoginURL(r.VerificationURL, false), baiduLoginURL(r.QRURL, true)
			if r.Expires > 900 {
				r.Expires = 900
			}
			s.ExpiresAt = time.Now().Add(time.Duration(r.Expires) * time.Second)
			if r.Interval > 0 && r.Interval <= 30 {
				s.Interval = r.Interval
			}
			s.secret, err = a.encrypt(Credentials{ClientSecret: app.ClientSecret})
		}
	} else {
		var r struct {
			Status int `json:"status"`
			Data   struct {
				Members struct {
					Token string `json:"token"`
				} `json:"members"`
			} `json:"data"`
		}
		err = cloudJSON(ctx, &client, "GET", "https://uop.quark.cn/cas/ajax/getTokenForQrcodeLogin", url.Values{"client_id": {"532"}, "v": {"1.2"}, "request_id": {cloudLoginRequestID()}}, nil, &r)
		if err == nil && (r.Status != 2000000 || r.Data.Members.Token == "" || len(r.Data.Members.Token) > 1024) {
			err = errors.New("夸克未能生成登录二维码，请稍后重试或使用高级连接方式")
		}
		if err == nil {
			s.deviceCode = r.Data.Members.Token
			q := url.Values{"token": {s.deviceCode}, "client_id": {"532"}, "ssb": {"weblogin"}, "uc_param_str": {""}, "uc_biz_str": {"S:custom|OPT:SAREA@0|OPT:IMMERSIVE@1|OPT:BACK_BTN_STYLE@0"}}
			s.LoginURL = "https://su.quark.cn/4_eMHBJ?" + q.Encode()
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	live := a.cloudAuth[s.ID]
	if live == nil || live.Status != "starting" {
		return CloudAuthView{}, errors.New("登录请求已取消，请重新开始")
	}
	if err != nil {
		live.clear("failed")
		live.Error = err.Error()
		return CloudAuthView{}, err
	}
	*live = *s
	live.Status = "pending"
	return live.view(), nil
}

func pollBaiduLogin(ctx context.Context, s CloudAuthSession, a *App) (string, Credentials, error) {
	var c Credentials
	if a.decrypt(s.secret, &c) != nil {
		return "failed", c, errors.New("无法读取应用配置，请重新开始授权")
	}
	var r struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Expires int64  `json:"expires_in"`
		Error   string `json:"error"`
		Scope   string `json:"scope"`
	}
	err := cloudJSON(ctx, s.client, "POST", "https://openapi.baidu.com/oauth/2.0/token", nil, url.Values{"grant_type": {"device_token"}, "code": {s.deviceCode}, "client_id": {s.ClientID}, "client_secret": {c.ClientSecret}}, &r)
	if err != nil {
		return "failed", Credentials{}, err
	}
	switch r.Error {
	case "authorization_pending":
		return "pending", Credentials{}, nil
	case "slow_down":
		return "slow_down", Credentials{}, nil
	case "authorization_declined", "access_denied":
		return "denied", Credentials{}, errors.New("已拒绝百度授权，可以重新登录")
	case "expired_token", "invalid_grant":
		return "expired", Credentials{}, errors.New("百度授权已过期，请重新获取二维码")
	case "":
	default:
		return "failed", Credentials{}, errors.New("百度授权失败，请检查应用权限后重新登录")
	}
	if r.Access == "" || r.Expires <= 0 || r.Scope != "" && !strings.Contains(r.Scope, "netdisk") {
		return "failed", Credentials{}, errors.New("百度没有返回有效的网盘授权，请检查应用权限")
	}
	c.AccessToken, c.RefreshToken, c.Expires = r.Access, r.Refresh, time.Now().Unix()+r.Expires
	return "ready", c, nil
}

func pollQuarkLogin(ctx context.Context, s CloudAuthSession) (string, Credentials, error) {
	var r struct {
		Status int `json:"status"`
		Data   struct {
			Members struct {
				Ticket string `json:"service_ticket"`
			} `json:"members"`
		} `json:"data"`
	}
	err := cloudJSON(ctx, s.client, "GET", "https://uop.quark.cn/cas/ajax/getServiceTicketByQrcodeToken", url.Values{"client_id": {"532"}, "v": {"1.2"}, "token": {s.deviceCode}, "request_id": {cloudLoginRequestID()}}, nil, &r)
	if err != nil {
		return "failed", Credentials{}, err
	}
	switch r.Status {
	case 50004001:
		return "pending", Credentials{}, nil
	case 50004002:
		return "expired", Credentials{}, errors.New("夸克二维码已过期，请重新获取")
	case 50004003, 50004004:
		return "denied", Credentials{}, errors.New("夸克登录未完成或已取消，可以重新扫码")
	case 2000000:
		if r.Data.Members.Ticket == "" {
			return "pending", Credentials{}, nil
		}
	default:
		return "failed", Credentials{}, errors.New("夸克登录状态无法确认，请重新扫码")
	}
	var account json.RawMessage
	err = cloudJSON(ctx, s.client, "GET", "https://pan.quark.cn/account/info", url.Values{"st": {r.Data.Members.Ticket}, "lw": {"scan"}}, nil, &account)
	if err != nil {
		return "failed", Credentials{}, err
	}
	var warmup json.RawMessage
	if err = cloudJSON(ctx, s.client, "GET", quarkAPIBase+"/config", url.Values{"pr": {"ucpro"}, "fr": {"pc"}}, nil, &warmup); err != nil {
		return "failed", Credentials{}, err
	}
	u, _ := url.Parse(quarkAPIBase)
	parts := []string{}
	for _, c := range s.client.Jar.Cookies(u) {
		parts = append(parts, c.String())
	}
	c := Credentials{Cookie: strings.Join(parts, "; ")}
	if validateQuarkCookie(c.Cookie) != nil {
		return "failed", Credentials{}, errors.New("夸克未返回可用登录态，请重新扫码或使用高级连接方式")
	}
	return "ready", c, nil
}

func (a *App) pollCloudAuth(ctx context.Context, id string) (CloudAuthView, error) {
	a.mu.Lock()
	s := a.cloudAuth[id]
	if s == nil {
		a.mu.Unlock()
		return CloudAuthView{}, errors.New("登录会话不存在，请重新开始登录")
	}
	if time.Now().After(s.ExpiresAt) {
		s.clear("expired")
		s.Error = "登录会话已过期，请重新登录"
	}
	if s.Status != "pending" || s.polling || time.Now().Before(s.nextPoll) {
		v := s.view()
		a.mu.Unlock()
		return v, nil
	}
	s.polling = true
	s.nextPoll = time.Now().Add(time.Duration(s.Interval) * time.Second)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	s.cancel = cancel
	snapshot := *s
	a.mu.Unlock()
	defer cancel()
	var status string
	var c Credentials
	var err error
	if snapshot.Provider == "baidu" {
		status, c, err = pollBaiduLogin(ctx, snapshot, a)
	} else {
		status, c, err = pollQuarkLogin(ctx, snapshot)
	}
	var secret string
	if status == "ready" && err == nil {
		secret, err = a.encrypt(c)
		if err != nil {
			status = "failed"
			err = errors.New("无法保存登录凭证，请重试")
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s.polling = false
	s.cancel = nil
	if s.Status != "pending" {
		return s.view(), nil
	}
	if time.Now().After(s.ExpiresAt) {
		s.clear("expired")
		s.Error = "登录会话已过期，请重新登录"
		return s.view(), nil
	}
	if status == "slow_down" {
		if s.Interval < 30 {
			s.Interval = min(30, s.Interval+5)
		}
		s.nextPoll = time.Now().Add(time.Duration(s.Interval) * time.Second)
		return s.view(), nil
	}
	if err != nil {
		s.clear(status)
		s.Error = err.Error()
	} else if status == "ready" {
		s.secret = secret
		s.Status = "ready"
		s.deviceCode, s.qrImageURL, s.LoginURL = "", "", ""
		s.ExpiresAt = time.Now().Add(15 * time.Minute)
	}
	return s.view(), nil
}

func (a *App) claimCloudAuth(id, provider, sourceID string) (Credentials, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.cloudAuth[id]
	if s == nil || s.Provider != provider || s.SourceID != sourceID || s.Status != "ready" || time.Now().After(s.ExpiresAt) {
		return Credentials{}, "", errors.New("登录结果不可用或已过期，请重新登录")
	}
	if sourceID != "" {
		found := false
		for _, v := range a.s.Sources {
			if v.ID == sourceID && scanSourceIdentity(v) == s.sourceIdentity {
				found = true
				break
			}
		}
		if !found {
			return Credentials{}, "", errors.New("来源设置已变化，请重新登录后保存")
		}
	}
	var c Credentials
	if err := a.decrypt(s.secret, &c); err != nil {
		return c, "", errors.New("无法读取登录结果，请重新登录")
	}
	s.Status = "saving"
	return c, s.ClientID, nil
}

func (a *App) finishCloudAuth(id string, success bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s := a.cloudAuth[id]; s != nil && s.Status == "saving" {
		if success {
			s.clear("complete")
		} else {
			s.Status = "ready"
		}
	}
}

func (a *App) cloudAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cloud-auth/options", func(w http.ResponseWriter, r *http.Request) {
		c, err := a.baiduAppCredentials(r.URL.Query().Get("sourceId"))
		jsonResponse(w, 200, map[string]any{"baiduConfigured": err == nil, "baiduClientId": c.ClientID, "message": func() string {
			if err != nil {
				return err.Error()
			}
			return "已配置百度应用，登录时无需再次填写"
		}()})
	})
	mux.HandleFunc("POST /api/config/cloud-apps", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ClientID     string `json:"clientId"`
			ClientSecret string `json:"clientSecret"`
			SourceID     string `json:"sourceId"`
		}
		if err := readJSON(w, r, &body); err != nil {
			fail(w, err)
			return
		}
		body.ClientID, body.ClientSecret = strings.TrimSpace(body.ClientID), strings.TrimSpace(body.ClientSecret)
		if body.ClientSecret == "" {
			if old, e := a.baiduAppCredentials(body.SourceID); e == nil && old.ClientID == body.ClientID {
				body.ClientSecret = old.ClientSecret
			}
		}
		if body.ClientID == "" || body.ClientSecret == "" || len(body.ClientID) > 512 || len(body.ClientSecret) > 512 || strings.ContainsAny(body.ClientID+body.ClientSecret, "\r\n\x00 ") {
			fail(w, errors.New("请填写完整的百度应用 App Key 和 Secret Key"))
			return
		}
		secret, err := a.encrypt(BaiduAppCredentials{ClientID: body.ClientID, ClientSecret: body.ClientSecret})
		if err != nil {
			fail(w, errors.New("无法保存应用配置"))
			return
		}
		a.mu.Lock()
		old := a.s.Config.BaiduAppSecret
		a.s.Config.BaiduAppSecret = secret
		err = a.saveLocked()
		if err != nil {
			a.s.Config.BaiduAppSecret = old
		}
		a.mu.Unlock()
		if err != nil {
			fail(w, errors.New("无法保存应用配置"))
			return
		}
		jsonResponse(w, 200, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/cloud-auth", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Provider string `json:"provider"`
			SourceID string `json:"sourceId"`
		}
		if err := readJSON(w, r, &body); err != nil {
			fail(w, err)
			return
		}
		v, err := a.startCloudAuth(r.Context(), body.Provider, body.SourceID)
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, 200, v)
	})
	mux.HandleFunc("POST /api/cloud-auth/{id}/poll", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.pollCloudAuth(r.Context(), r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, 200, v)
	})
	mux.HandleFunc("POST /api/cloud-auth/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		if s := a.cloudAuth[r.PathValue("id")]; s != nil && s.Status != "saving" {
			s.clear("cancelled")
		}
		a.mu.Unlock()
		jsonResponse(w, 200, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /api/cloud-auth/{id}/qr", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		s := a.cloudAuth[r.PathValue("id")]
		if s == nil || s.Status != "pending" || time.Now().After(s.ExpiresAt) {
			a.mu.Unlock()
			http.Error(w, "二维码已失效", 410)
			return
		}
		snapshot := *s
		a.mu.Unlock()
		var data []byte
		var err error
		if snapshot.Provider == "quark" {
			data, err = qrcode.Encode(snapshot.LoginURL, qrcode.Medium, 256)
		} else {
			if snapshot.qrImageURL == "" {
				http.Error(w, "请使用授权链接和设备码", 404)
				return
			}
			req, _ := http.NewRequestWithContext(r.Context(), "GET", snapshot.qrImageURL, nil)
			resp, e := snapshot.client.Do(req)
			if e != nil {
				err = errors.New("二维码加载失败")
			} else {
				defer resp.Body.Close()
				if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") {
					err = errors.New("二维码加载失败")
				} else {
					data, err = io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
					if len(data) > 1<<20 {
						err = errors.New("二维码数据过大")
					}
				}
			}
		}
		if err != nil {
			http.Error(w, "二维码暂时无法加载，请打开登录页面或重新获取", 502)
			return
		}
		mime := http.DetectContentType(data)
		if mime != "image/png" && mime != "image/jpeg" && mime != "image/gif" {
			http.Error(w, "二维码格式不支持", 502)
			return
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
	})
}
