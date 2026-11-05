package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type cloudLoginFixture struct {
	authorized  atomic.Bool
	denied      atomic.Bool
	badRedirect atomic.Bool
	wrongScope  atomic.Bool
	polls       atomic.Int32
}

func newCloudLoginFixture(t *testing.T) (*App, *cloudLoginFixture) {
	t.Helper()
	a, _ := newQuarkFixture(t)
	base := a.client.Transport
	f := &cloudLoginFixture{}
	a.s.Config.BaiduAppSecret, _ = a.encrypt(BaiduAppCredentials{ClientID: "fixture-app", ClientSecret: "fixture-app-secret"})
	a.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		response := func(value any) *http.Response {
			raw, _ := json.Marshal(value)
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(raw))}
		}
		switch r.URL.Host {
		case "openapi.baidu.com":
			if strings.HasSuffix(r.URL.Path, "/device/code") {
				if r.URL.Query().Get("scope") != "basic,netdisk" || r.URL.Query().Get("client_id") != "fixture-app" {
					return nil, errors.New("unexpected app or scopes")
				}
				verification := "https://openapi.baidu.com/device"
				if f.badRedirect.Load() {
					verification = "https://untrusted.invalid/device"
				}
				return response(map[string]any{"device_code": "fixture-private-device-code", "user_code": "USER-CODE", "verification_url": verification, "qrcode_url": "https://openapi.baidu.com/device/qrcode/test/code", "expires_in": 300, "interval": 2}), nil
			}
			if strings.HasPrefix(r.URL.Path, "/device/qrcode/") {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"image/png"}}, Body: io.NopCloser(bytes.NewReader(tinyPNG(t)))}, nil
			}
			if strings.HasSuffix(r.URL.Path, "/token") {
				f.polls.Add(1)
				if err := r.ParseForm(); err != nil {
					return nil, err
				}
				if r.Form.Get("grant_type") != "device_token" || r.Form.Get("client_secret") != "fixture-app-secret" || r.Form.Get("code") != "fixture-private-device-code" {
					return nil, errors.New("invalid device token request")
				}
				if f.denied.Load() {
					return response(map[string]any{"error": "authorization_declined", "error_description": "DO-NOT-ECHO-SECRET"}), nil
				}
				if !f.authorized.Load() {
					return response(map[string]any{"error": "authorization_pending", "error_description": "DO-NOT-ECHO-SECRET"}), nil
				}
				scope := "basic netdisk"
				if f.wrongScope.Load() {
					scope = "basic"
				}
				return response(map[string]any{"access_token": "fixture-access-token", "refresh_token": "fixture-refresh-token", "expires_in": 3600, "scope": scope}), nil
			}
		case "uop.quark.cn":
			if strings.HasSuffix(r.URL.Path, "getTokenForQrcodeLogin") {
				return response(map[string]any{"status": 2000000, "data": map[string]any{"members": map[string]any{"token": "fixture-qr-challenge"}}}), nil
			}
			f.polls.Add(1)
			if f.denied.Load() {
				return response(map[string]any{"status": 50004004}), nil
			}
			if !f.authorized.Load() {
				return response(map[string]any{"status": 50004001}), nil
			}
			return response(map[string]any{"status": 2000000, "data": map[string]any{"members": map[string]any{"service_ticket": "fixture-private-ticket"}}}), nil
		case "pan.quark.cn":
			if r.URL.Query().Get("st") != "fixture-private-ticket" {
				return nil, errors.New("invalid service ticket")
			}
			out := response(map[string]any{"success": true, "data": map[string]any{"nickname": "测试账号"}})
			out.Header.Add("Set-Cookie", "__pus=fixture-session; Domain=.quark.cn; Path=/; HttpOnly; Secure")
			return out, nil
		case "drive.quark.cn":
			if strings.HasSuffix(r.URL.Path, "/config") {
				if !strings.Contains(r.Header.Get("Cookie"), "fixture-session") {
					return nil, errors.New("login Cookie not passed to provider")
				}
				out := response(map[string]any{"status": 200, "code": 0, "data": map[string]any{}})
				out.Header.Add("Set-Cookie", "__puus=fixture-original; Domain=.quark.cn; Path=/; HttpOnly; Secure")
				return out, nil
			}
		case "pan.baidu.com":
			if r.URL.Query().Get("access_token") != "fixture-access-token" {
				return nil, errors.New("access token missing")
			}
			return response(map[string]any{"errno": 0, "list": []map[string]any{{"fs_id": 123, "server_filename": "Movie.mp4", "path": path.Join(r.URL.Query().Get("dir"), "Movie.mp4"), "size": 9, "isdir": 0, "server_mtime": 1760000000}}}), nil
		}
		return base.RoundTrip(r)
	})
	return a, f
}

func releaseCloudPoll(a *App, id string) {
	a.mu.Lock()
	a.cloudAuth[id].nextPoll = time.Time{}
	a.mu.Unlock()
}

func TestCloudLoginBaiduDeviceFlowAndEncryptedSingleUseSave(t *testing.T) {
	a, f := newCloudLoginFixture(t)
	server := httptest.NewServer(a.handler())
	defer server.Close()
	out := request(t, server, a, "POST", "/api/cloud-auth", map[string]any{"provider": "baidu"}, 200)
	var view CloudAuthView
	raw, _ := json.Marshal(out)
	json.Unmarshal(raw, &view)
	if view.Status != "pending" || view.UserCode != "USER-CODE" || view.QRURL == "" {
		t.Fatal("device authorization not displayed", view)
	}
	if strings.Contains(string(raw), "fixture-private-device-code") || strings.Contains(string(raw), "fixture-app-secret") {
		t.Fatal("private credentials exposed")
	}
	r, err := http.Get(server.URL + view.QRURL)
	if err != nil {
		t.Fatal(err)
	}
	image, err := png.Decode(r.Body)
	r.Body.Close()
	if err != nil || image.Bounds().Dx() == 0 {
		t.Fatal("device QR image not available", err)
	}
	view, err = a.pollCloudAuth(context.Background(), view.ID)
	if err != nil || view.Status != "pending" {
		t.Fatal("pending response treated as failure", view, err)
	}
	_, _ = a.pollCloudAuth(context.Background(), view.ID)
	if f.polls.Load() != 1 {
		t.Fatal("provider polling interval ignored")
	}
	f.authorized.Store(true)
	releaseCloudPoll(a, view.ID)
	view, err = a.pollCloudAuth(context.Background(), view.ID)
	if err != nil || view.Status != "ready" || view.LoginURL != "" || view.QRURL != "" {
		t.Fatal("authorization result not ready", view, err)
	}
	result := request(t, server, a, "POST", "/api/sources", map[string]any{"type": "baidu", "name": "设备授权来源", "root": "/影视", "authSession": view.ID}, 201)
	var sourceID string
	json.Unmarshal(result["id"], &sourceID)
	s, _ := a.source(sourceID)
	c, _ := a.credentials(s)
	if s.ClientID != "fixture-app" || c.AccessToken != "fixture-access-token" || c.RefreshToken != "fixture-refresh-token" || c.ClientSecret != "fixture-app-secret" {
		t.Fatal("authorized credentials not saved")
	}
	if _, _, err := a.claimCloudAuth(view.ID, "baidu", ""); err == nil {
		t.Fatal("login result replayed")
	}
	public, _ := json.Marshal(a.publicState())
	saved, _ := os.ReadFile(a.dir + "/state.json")
	for _, secret := range []string{"fixture-app-secret", "fixture-private-device-code", "fixture-access-token", "fixture-refresh-token"} {
		if strings.Contains(string(public), secret) || strings.Contains(string(saved), secret) {
			t.Fatal("credential leaked", secret)
		}
	}
}

func TestCloudLoginQuarkQRAndFailedSaveCanRetry(t *testing.T) {
	a, f := newCloudLoginFixture(t)
	view, err := a.startCloudAuth(context.Background(), "quark", "")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.handler())
	defer server.Close()
	r, err := http.Get(server.URL + view.QRURL)
	if err != nil {
		t.Fatal(err)
	}
	image, err := png.Decode(r.Body)
	r.Body.Close()
	if err != nil || image.Bounds().Dx() != 256 {
		t.Fatal("Quark QR not generated locally", err)
	}
	f.authorized.Store(true)
	view, err = a.pollCloudAuth(context.Background(), view.ID)
	if err != nil || view.Status != "ready" {
		t.Fatal("Quark login failed", view, err)
	}
	request(t, server, a, "POST", "/api/sources", map[string]any{"type": "quark", "name": "扫码夸克", "root": "bad/../root", "authSession": view.ID}, 400)
	a.mu.Lock()
	status := a.cloudAuth[view.ID].Status
	a.mu.Unlock()
	if status != "ready" {
		t.Fatal("validation failure discarded login result")
	}
	result := request(t, server, a, "POST", "/api/sources", map[string]any{"type": "quark", "name": "扫码夸克", "root": "0", "authSession": view.ID}, 201)
	var sourceID string
	json.Unmarshal(result["id"], &sourceID)
	s, _ := a.source(sourceID)
	c, _ := a.credentials(s)
	if !strings.Contains(c.Cookie, "fixture-updated") || !strings.Contains(c.Cookie, "fixture-session") {
		t.Fatal("QR login did not reuse encrypted Cookie connection")
	}
	request(t, server, a, "POST", "/api/sources", map[string]any{"type": "quark", "name": "重放", "root": "0", "authSession": view.ID}, 400)
	if len(a.s.Sources) != 1 {
		t.Fatal("replayed login created another source")
	}
}

func TestCloudLoginCancelExpiryScopeAndSourceBinding(t *testing.T) {
	a, f := newCloudLoginFixture(t)
	server := httptest.NewServer(a.handler())
	defer server.Close()
	view, _ := a.startCloudAuth(context.Background(), "quark", "")
	request(t, server, a, "POST", "/api/cloud-auth/"+view.ID+"/cancel", map[string]any{}, 200)
	f.authorized.Store(true)
	view, _ = a.pollCloudAuth(context.Background(), view.ID)
	if view.Status != "cancelled" || f.polls.Load() != 0 {
		t.Fatal("cancelled login still polled provider")
	}
	view, _ = a.startCloudAuth(context.Background(), "baidu", "")
	a.mu.Lock()
	a.cloudAuth[view.ID].ExpiresAt = time.Now().Add(-time.Second)
	a.mu.Unlock()
	view, _ = a.pollCloudAuth(context.Background(), view.ID)
	if view.Status != "expired" {
		t.Fatal("expired login remained usable")
	}
	view, _ = a.startCloudAuth(context.Background(), "baidu", "")
	f.wrongScope.Store(true)
	view, _ = a.pollCloudAuth(context.Background(), view.ID)
	if view.Status != "failed" {
		t.Fatal("missing netdisk permission accepted")
	}
	f.wrongScope.Store(false)
	view, _ = a.startCloudAuth(context.Background(), "quark", "")
	view, _ = a.pollCloudAuth(context.Background(), view.ID)
	if _, _, err := a.claimCloudAuth(view.ID, "baidu", ""); err == nil {
		t.Fatal("wrong provider accepted")
	}
	if _, _, err := a.claimCloudAuth(view.ID, "quark", "another-source"); err == nil {
		t.Fatal("login result used for a different source")
	}
	if _, _, err := a.claimCloudAuth(view.ID, "quark", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.claimCloudAuth(view.ID, "quark", ""); err == nil {
		t.Fatal("concurrent credential claim accepted")
	}
}

func TestCloudLoginRejectsUntrustedLinksAndRawProviderErrors(t *testing.T) {
	a, f := newCloudLoginFixture(t)
	f.badRedirect.Store(true)
	if _, err := a.startCloudAuth(context.Background(), "baidu", ""); err == nil {
		t.Fatal("untrusted authorization URL accepted")
	}
	f.badRedirect.Store(false)
	f.denied.Store(true)
	view, _ := a.startCloudAuth(context.Background(), "baidu", "")
	view, _ = a.pollCloudAuth(context.Background(), view.ID)
	raw, _ := json.Marshal(view)
	if view.Status != "denied" || strings.Contains(string(raw), "DO-NOT-ECHO-SECRET") {
		t.Fatal("raw provider error leaked", view)
	}
	for _, value := range []string{"https://evil.invalid/device", "https://openapi.baidu.com.evil.invalid/device", "https://user@openapi.baidu.com/device", "https://openapi.baidu.com:8888/device"} {
		if baiduLoginURL(value, false) != "" {
			t.Fatal("unsafe authorization link accepted")
		}
	}
}

func TestCloudLoginBaiduProjectSetupAndExistingAppReuse(t *testing.T) {
	a, f := newCloudLoginFixture(t)
	a.s.Config.BaiduAppSecret = ""
	t.Setenv("YINGKU_BAIDU_CLIENT_ID", "")
	t.Setenv("YINGKU_BAIDU_CLIENT_SECRET", "")
	if _, err := a.startCloudAuth(context.Background(), "baidu", ""); err == nil {
		t.Fatal("unconfigured app pretended to support OAuth")
	}
	server := httptest.NewServer(a.handler())
	defer server.Close()
	request(t, server, a, "POST", "/api/config/cloud-apps", map[string]any{"clientId": "fixture-app", "clientSecret": "fixture-app-secret"}, 200)
	view, err := a.startCloudAuth(context.Background(), "baidu", "")
	if err != nil {
		t.Fatal(err)
	}
	f.authorized.Store(true)
	view, _ = a.pollCloudAuth(context.Background(), view.ID)
	result := request(t, server, a, "POST", "/api/sources", map[string]any{"type": "baidu", "name": "百度来源", "root": "/", "authSession": view.ID}, 201)
	var id string
	json.Unmarshal(result["id"], &id)
	a.s.Config.BaiduAppSecret = ""
	app, err := a.baiduAppCredentials("")
	if err != nil || app.ClientID != "fixture-app" {
		t.Fatal("existing source app not reused", err)
	}
	view, err = a.startCloudAuth(context.Background(), "baidu", id)
	if err != nil {
		t.Fatal(err)
	}
	view, _ = a.pollCloudAuth(context.Background(), view.ID)
	a.mu.Lock()
	a.s.Sources[0].Root = "/changed"
	a.mu.Unlock()
	if _, _, err := a.claimCloudAuth(view.ID, "baidu", id); err == nil {
		t.Fatal("changed source accepted stale authorization")
	}
}

func TestCloudLoginQuarkLiveAnonymous(t *testing.T) {
	if os.Getenv("YINGKU_LIVE_CLOUD_LOGIN") != "1" {
		t.Skip("anonymous live login check is opt-in")
	}
	a := testApp(t)
	view, err := a.startCloudAuth(context.Background(), "quark", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { a.mu.Lock(); a.cloudAuth[view.ID].clear("cancelled"); a.mu.Unlock() }()
	view, err = a.pollCloudAuth(context.Background(), view.ID)
	if err != nil || view.Status != "pending" {
		t.Fatal("anonymous QR challenge not pending", err, view.Status)
	}
}

func TestCloudLoginCancelDuringPollDoesNotRestoreCredentials(t *testing.T) {
	a, f := newCloudLoginFixture(t)
	view, err := a.startCloudAuth(context.Background(), "quark", "")
	if err != nil {
		t.Fatal(err)
	}
	base := a.client.Transport
	started, release := make(chan struct{}), make(chan struct{})
	a.mu.Lock()
	a.cloudAuth[view.ID].client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "getServiceTicketByQrcodeToken") {
			close(started)
			<-release
		}
		return base.RoundTrip(r)
	})
	a.mu.Unlock()
	f.authorized.Store(true)
	done := make(chan CloudAuthView, 1)
	go func() { result, _ := a.pollCloudAuth(context.Background(), view.ID); done <- result }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("poll not started")
	}
	a.mu.Lock()
	a.cloudAuth[view.ID].clear("cancelled")
	a.mu.Unlock()
	close(release)
	select {
	case result := <-done:
		if result.Status != "cancelled" {
			t.Fatal("cancelled poll revived login", result.Status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled poll did not stop")
	}
	a.mu.Lock()
	secret := a.cloudAuth[view.ID].secret
	a.mu.Unlock()
	if secret != "" {
		t.Fatal("credentials restored after cancellation")
	}
}

func TestBaiduPrivateRequestsDoNotFollowCredentialRedirects(t *testing.T) {
	a := testApp(t)
	var other atomic.Int32
	a.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "openapi.baidu.com" {
			other.Add(1)
		}
		return &http.Response{StatusCode: 307, Header: http.Header{"Location": {"https://untrusted.invalid/token"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	secret, _ := a.encrypt(Credentials{RefreshToken: "private-refresh", ClientSecret: "private-app-secret"})
	_, err := a.refreshBaidu(context.Background(), Source{ID: "test", Type: "baidu", ClientID: "test-app", Secret: secret, Pending: true})
	if err == nil || other.Load() != 0 || strings.Contains(err.Error(), "private-") {
		t.Fatal("credential redirect not rejected", err)
	}
}

func TestCloudLoginInteractivePreview(t *testing.T) {
	if os.Getenv("YINGKU_CLOUD_LOGIN_PREVIEW") != "1" {
		t.Skip("interactive cloud login fixture is opt-in")
	}
	a, f := newCloudLoginFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fixture/approve" && r.Method == "POST" {
			f.authorized.Store(true)
			jsonResponse(w, 200, map[string]bool{"ok": true})
			return
		}
		if r.URL.Path == "/fixture/deny" && r.Method == "POST" {
			f.denied.Store(true)
			jsonResponse(w, 200, map[string]bool{"ok": true})
			return
		}
		a.handler().ServeHTTP(w, r)
	}))
	defer server.Close()
	fmt.Println("CLOUD_LOGIN_PREVIEW=" + server.URL)
	<-time.After(20 * time.Minute)
}
