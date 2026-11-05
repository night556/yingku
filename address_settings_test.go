package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAddressSettingsSaveRestoreAndClear(t *testing.T) {
	a := testApp(t)
	a.listenAddress = "0.0.0.0:8787"
	call := func(body string, token bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://media.example.com/api/config/addresses", strings.NewReader(body))
		r.RemoteAddr = "172.20.0.2:1234"
		r.Header.Set("Origin", "https://media.example.com")
		if token {
			r.Header.Set("X-App-Token", a.csrf)
		}
		w := httptest.NewRecorder()
		a.handler().ServeHTTP(w, r)
		return w
	}
	body := `{"publicURL":"https://MEDIA.example.com/","castBaseURL":"http://192.168.1.100:18787/"}`
	if w := call(body, false); w.Code != 403 || a.s.Config.PublicURL != "" {
		t.Fatal("address settings bypassed page token")
	}
	if w := call(body, true); w.Code != 200 {
		t.Fatalf("cannot save addresses through unconfigured proxy domain: %d %s", w.Code, w.Body.String())
	}
	b, err := newApp(a.dir)
	if err != nil || b.s.Config.PublicURL != "https://media.example.com" || b.s.Config.CastBaseURL != "http://192.168.1.100:18787" {
		t.Fatalf("addresses did not survive restart: %v", err)
	}
	before := a.s.Config
	if w := call(`{"publicURL":"https://changed.example.com","castBaseURL":"http://192.168.1.100:8787/bad-path"}`, true); w.Code != 400 || a.s.Config.PublicURL != before.PublicURL || a.s.Config.CastBaseURL != before.CastBaseURL {
		t.Fatal("invalid address partially changed settings")
	}
	if w := call(`{}`, true); w.Code != 200 || a.s.Config.PublicURL != "" || a.s.Config.CastBaseURL != "" {
		t.Fatal("clearing addresses did not restore automatic defaults")
	}
	a.publicURL = "https://fixed.example.com"
	if w := call(`{"publicURL":"https://other.example.com"}`, true); w.Code != 400 || a.s.Config.PublicURL != "" {
		t.Fatal("startup display override was silently replaced")
	}
}

func TestLoopbackProxyDoesNotBypassPasswordOrGainLocalPrivileges(t *testing.T) {
	a := testApp(t)
	configureTestPassword(t, a)
	a.listenAddress = "127.0.0.1:8787"
	for _, host := range []string{"media.example.com", "localhost:8787"} {
		r := httptest.NewRequest("GET", "http://"+host+"/api/state", nil)
		r.RemoteAddr = "127.0.0.1:1234"
		if host == "localhost:8787" {
			r.Header.Set("X-Forwarded-For", "192.168.1.20")
		}
		w := httptest.NewRecorder()
		a.handler().ServeHTTP(w, r)
		if localRequest(r) || w.Code != 401 {
			t.Fatalf("proxy treated as local administrator for %s: %d", host, w.Code)
		}
	}
}

func TestProxyDomainWithoutURLCanSaveSettingsButRejectsCrossSite(t *testing.T) {
	a := testApp(t)
	a.listenAddress = "0.0.0.0:8787"
	for _, crossSite := range []bool{false, true} {
		r := httptest.NewRequest("POST", "https://media.example.com/api/config/addresses", strings.NewReader(`{}`))
		r.RemoteAddr = "172.20.0.2:1234"
		r.Header.Set("Origin", "https://media.example.com")
		r.Header.Set("X-App-Token", a.csrf)
		want := 200
		if crossSite {
			r.Header.Set("Sec-Fetch-Site", "cross-site")
			want = 403
		}
		w := httptest.NewRecorder()
		a.handler().ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("cross-site=%v: got %d, want %d", crossSite, w.Code, want)
		}
	}
}

func TestDLNACallbackAddressIsSeparateFromDisplayURL(t *testing.T) {
	a, server, tv, device := castingFixture(t)
	a.s.Config.PublicURL = "https://media.example.com"
	if _, err := a.startCast(context.Background(), device.ID, "cast-file", nil); err != nil {
		t.Fatal(err)
	}
	tv.mu.Lock()
	autoURI := tv.URI
	tv.mu.Unlock()
	if !strings.HasPrefix(autoURI, server.URL+"/cast/") {
		t.Fatalf("display URL changed automatic LAN callback: %s", autoURI)
	}
	a.s.Config.CastBaseURL = "http://192.168.1.100:18787"
	if _, err := a.startCast(context.Background(), device.ID, "cast-file", nil); err != nil {
		t.Fatal(err)
	}
	tv.mu.Lock()
	uri, metadata := tv.URI, tv.Metadata
	tv.mu.Unlock()
	if !strings.HasPrefix(uri, a.s.Config.CastBaseURL+"/cast/") || !strings.Contains(metadata, uri) {
		t.Fatalf("TV did not receive overridden address: %s", uri)
	}
	r := httptest.NewRequest("GET", uri, nil)
	r.RemoteAddr = "192.168.1.40:1234"
	r.Header.Set("Range", "bytes=2-5")
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	if w.Code != 206 || w.Body.String() != "2345" {
		t.Fatalf("custom callback could not serve TV media: %d", w.Code)
	}
	public, _ := json.Marshal(a.publicState())
	if strings.Contains(string(public), "/cast/") {
		t.Fatal("TV's media grant leaked through public state")
	}
}
