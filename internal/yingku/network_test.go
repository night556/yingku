package yingku

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func networkRequest(a *App, method, path, body string, local bool, cookie *http.Cookie, csrf bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
	r.RemoteAddr = "192.168.50.20:1234"
	if local {
		r.RemoteAddr = "127.0.0.1:1234"
	}
	r.Header.Set("Content-Type", "application/json")
	if csrf {
		r.Header.Set("X-App-Token", a.csrf)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	return w
}

func configureTestPassword(t *testing.T, a *App) {
	t.Helper()
	hash, err := passwordHash("test-password-123", "synthetic-salt-for-tests")
	if err != nil {
		t.Fatal(err)
	}
	a.s.Config.Network = NetworkConfig{Salt: "synthetic-salt-for-tests", Hash: hash}
}

func TestNetworkLoginAndIsolation(t *testing.T) {
	a := testApp(t)
	a.listenAddress = "0.0.0.0:8787"
	if w := networkRequest(a, "GET", "/api/state", "", false, nil, false); w.Code != 200 {
		t.Fatalf("default LAN access failed: %d", w.Code)
	}
	configureTestPassword(t, a)
	if w := networkRequest(a, "GET", "/api/state", "", false, nil, false); w.Code != 401 {
		t.Fatalf("unauthenticated library: %d", w.Code)
	}
	if w := networkRequest(a, "GET", "/", "", false, nil, false); w.Code != 303 || w.Header().Get("Location") != "/login" {
		t.Fatal("mobile login redirect missing")
	}
	if w := networkRequest(a, "POST", "/api/session", `{"password":"wrong"}`, false, nil, false); w.Code != 401 {
		t.Fatal("wrong password accepted")
	}
	w := networkRequest(a, "POST", "/api/session", `{"password":"test-password-123"}`, false, nil, false)
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("session cookie protections missing")
	}
	cookie := cookies[0]
	w = networkRequest(a, "GET", "/api/state", "", false, cookie, false)
	if w.Code != 200 || strings.Contains(w.Body.String(), a.s.Config.Network.Hash) || strings.Contains(w.Body.String(), "synthetic-salt") {
		t.Fatal("authenticated state missing or password verifier leaked")
	}
	if w := networkRequest(a, "POST", "/api/network", `{"enabled":false}`, false, cookie, true); w.Code != 403 {
		t.Fatal("remote user changed network settings")
	}
	if w := networkRequest(a, "POST", "/api/config", `{}`, false, cookie, false); w.Code != 403 {
		t.Fatal("CSRF protection bypassed on LAN")
	}
	r := httptest.NewRequest("POST", "http://localhost/api/session", strings.NewReader(`{"password":"test-password-123"}`))
	r.RemoteAddr = "192.168.50.20:1234"
	r.Header.Set("Origin", "http://attacker.example")
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.handler().ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Fatal("cross-site login accepted")
	}
	if w := networkRequest(a, "DELETE", "/api/session", "", false, cookie, true); w.Code != 200 {
		t.Fatal("logout failed")
	}
	if w := networkRequest(a, "GET", "/api/state", "", false, cookie, false); w.Code != 401 {
		t.Fatal("logged-out session remains valid")
	}
	a.loginAttempts["192.168.50.20"] = loginAttempt{Count: 10, Since: time.Now()}
	if w := networkRequest(a, "POST", "/api/session", `{"password":"wrong"}`, false, nil, false); w.Code != 429 {
		t.Fatal("login rate limit not enforced")
	}
}

func TestNetworkSettingsKeepSingleListener(t *testing.T) {
	a := testApp(t)
	a.listenAddress = "0.0.0.0:45678"
	t.Cleanup(a.closeNetwork)
	w := networkRequest(a, "POST", "/api/network", `{"requirePassword":true}`, true, nil, true)
	if w.Code != 400 {
		t.Fatal("password protection enabled without password")
	}
	w = networkRequest(a, "POST", "/api/network", `{"requirePassword":true,"password":"new-test-password-123","enabled":false,"port":8789}`, true, nil, true)
	if w.Code != 200 {
		t.Fatalf("save access settings failed: %d %s", w.Code, w.Body)
	}
	if a.listenAddress != "0.0.0.0:45678" || !a.lanReady() {
		t.Fatal("saving access settings changed listener")
	}
	raw, err := os.ReadFile(filepath.Join(a.dir, "state.json"))
	if err != nil || strings.Contains(string(raw), "new-test-password-123") {
		t.Fatal("plaintext password stored")
	}
	if strings.Contains(string(raw), `"port":`) || strings.Contains(string(raw), `"enabled": true`) {
		t.Fatal("obsolete listener configuration saved")
	}
	w = networkRequest(a, "POST", "/api/network", `{"requirePassword":false}`, true, nil, true)
	if w.Code != 200 || !a.lanReady() || a.listenAddress != "0.0.0.0:45678" {
		t.Fatal("password mode changed server address")
	}
}

func TestOptionalPasswordAndModeChanges(t *testing.T) {
	a := testApp(t)
	configureTestPassword(t, a)
	a.listenAddress = "0.0.0.0:8787"
	t.Cleanup(a.closeNetwork)
	login := networkRequest(a, "POST", "/api/session", `{"password":"test-password-123"}`, false, nil, false)
	if login.Code != 200 {
		t.Fatal("protected login failed")
	}
	cookie := login.Result().Cookies()[0]
	beforeHash := a.s.Config.Network.Hash
	for _, test := range []struct {
		required bool
		status   int
	}{{false, 200}, {true, 401}} {
		body := fmt.Sprintf(`{"requirePassword":%t}`, test.required)
		w := networkRequest(a, "POST", "/api/network", body, true, nil, true)
		if w.Code != 200 {
			t.Fatalf("mode change failed: %d %s", w.Code, w.Body)
		}
		if a.s.Config.Network.Hash != beforeHash {
			t.Fatal("mode change discarded saved password")
		}
		if w := networkRequest(a, "GET", "/api/state", "", false, nil, false); w.Code != test.status {
			t.Fatalf("passwordRequired=%v: got %d", test.required, w.Code)
		}
	}
	if w := networkRequest(a, "GET", "/api/state", "", false, cookie, false); w.Code != 401 {
		t.Fatal("session survived protection mode change")
	}
	// A legacy client that omits the new option must preserve existing protection.
	if w := networkRequest(a, "POST", "/api/network", `{"enabled":true,"port":8789,"password":""}`, true, nil, true); w.Code != 200 || a.s.Config.Network.NoPassword {
		t.Fatal("legacy settings update disabled protection")
	}
}

func TestPasswordFreeLANStillChecksRequestBoundaries(t *testing.T) {
	a := testApp(t)
	a.listenAddress = "0.0.0.0:8787"
	t.Cleanup(a.closeNetwork)
	w := networkRequest(a, "POST", "/api/network", `{"enabled":true,"port":8789,"requirePassword":false}`, true, nil, true)
	if w.Code != 200 || !a.s.Config.Network.NoPassword || a.s.Config.Network.Hash != "" {
		t.Fatalf("password-free LAN setup: %d %s", w.Code, w.Body)
	}
	if w := networkRequest(a, "GET", "/api/state", "", false, nil, false); w.Code != 200 {
		t.Fatal("password-free LAN still requires login")
	}
	if w := networkRequest(a, "GET", "/", "", false, nil, false); w.Code != 200 {
		t.Fatal("password-free home page unavailable")
	}
	if w := networkRequest(a, "GET", "/login", "", false, nil, false); w.Code != 303 || w.Header().Get("Location") != "/" {
		t.Fatal("obsolete login bookmark not redirected")
	}
	if w := networkRequest(a, "POST", "/api/config", `{}`, false, nil, false); w.Code != 403 {
		t.Fatal("password-free mode bypassed CSRF protection")
	}
	if w := networkRequest(a, "POST", "/api/network", `{"enabled":false}`, false, nil, true); w.Code != 403 {
		t.Fatal("remote client changed network settings")
	}
	r := httptest.NewRequest("GET", "http://localhost/api/state", nil)
	r.RemoteAddr = "203.0.113.8:1234"
	r.Header.Set("X-Forwarded-For", "192.168.50.20")
	rec := httptest.NewRecorder()
	a.handler().ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Fatal("public address accepted as LAN client")
	}
	r = httptest.NewRequest("GET", "http://localhost/api/state", nil)
	r.RemoteAddr = "192.168.50.20:1234"
	r.Header.Set("Origin", "http://attacker.example")
	rec = httptest.NewRecorder()
	a.handler().ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Fatal("cross-origin access accepted in password-free mode")
	}
	// Explicitly selecting protection without configuring a password must fail atomically.
	if w := networkRequest(a, "POST", "/api/network", `{"enabled":true,"port":8789,"requirePassword":true}`, true, nil, true); w.Code != 400 || !a.s.Config.Network.NoPassword {
		t.Fatal("invalid protected settings changed working access mode")
	}
}
