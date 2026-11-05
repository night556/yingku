package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func quotedJSON(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func TestRuntimeConfigurationPrecedence(t *testing.T) {
	env := map[string]string{
		"YINGKU_ADDR": "0.0.0.0:9191", "YINGKU_DATA_DIR": "env-data",
		"YINGKU_MEDIA_DIR": "/media", "YINGKU_PUBLIC_URL": "https://MEDIA.example.com/",
	}
	getenv := func(key string) string { return env[key] }
	options, err := parseRuntimeOptions(nil, getenv, io.Discard)
	if err != nil || options.Address != env["YINGKU_ADDR"] || options.DataDir != "env-data" || options.MediaDir != "/media" || options.PublicURL != "https://media.example.com" {
		t.Fatalf("environment not applied: %+v, %v", options, err)
	}
	options, err = parseRuntimeOptions([]string{"-addr", "127.0.0.1:9090", "-data", "flag-data", "-media=", "-public-url=", "-healthcheck"}, getenv, io.Discard)
	if err != nil || options.Address != "127.0.0.1:9090" || options.DataDir != "flag-data" || options.MediaDir != "" || options.PublicURL != "" || !options.Healthcheck {
		t.Fatalf("flag override not applied: %+v, %v", options, err)
	}
	options, err = parseRuntimeOptions(nil, func(string) string { return "" }, io.Discard)
	if err != nil || options.Address != "0.0.0.0:8787" || options.DataDir != "data" || options.MediaDir != "" {
		t.Fatalf("desktop defaults changed: %+v, %v", options, err)
	}
}

func TestManagedMediaDirectoryPreservesLibraryAndCredentials(t *testing.T) {
	a := testApp(t)
	secret, err := a.encrypt(Credentials{AccessToken: "synthetic-token"})
	if err != nil {
		t.Fatal(err)
	}
	a.s.Sources = []Source{{ID: "source", Type: "baidu", Root: "/tv", Secret: secret}}
	a.s.Files = []MediaFile{{ID: "file", Destination: filepath.Join(t.TempDir(), "existing.mp4"), Position: 42, Status: "completed"}}
	oldDestination := a.s.Files[0].Destination
	if err := a.saveLocked(); err != nil {
		t.Fatal(err)
	}
	keyBefore, _ := os.ReadFile(filepath.Join(a.dir, "secret.key"))
	media := filepath.Join(t.TempDir(), "download", "films")
	b, err := newAppWithOptions(a.dir, appOptions{MediaDir: media, PublicURL: "http://media.home:8787"})
	if err != nil {
		t.Fatal(err)
	}
	if b.s.Config.MediaRoot != media || b.s.Files[0].Destination != oldDestination || b.s.Files[0].Position != 42 || b.s.Sources[0].Root != "/tv" {
		t.Fatal("new defaults changed existing media or source paths")
	}
	keyAfter, _ := os.ReadFile(filepath.Join(a.dir, "secret.key"))
	var credentials Credentials
	if !bytes.Equal(keyBefore, keyAfter) || b.decrypt(b.s.Sources[0].Secret, &credentials) != nil || credentials.AccessToken != "synthetic-token" {
		t.Fatal("persisted source credentials did not survive restart")
	}
	other := filepath.Join(t.TempDir(), "must-not-be-created")
	body := `{"mediaRoot":` + quotedJSON(other) + `,"concurrency":3}`
	if w := networkRequest(b, "POST", "/api/config", body, true, nil, true); w.Code != 400 {
		t.Fatalf("managed directory change accepted: %d", w.Code)
	}
	if _, err := os.Stat(other); !os.IsNotExist(err) || b.s.Config.Concurrency != 2 {
		t.Fatal("rejected settings had side effects")
	}
	body = `{"mediaRoot":` + quotedJSON(media) + `,"concurrency":3}`
	if w := networkRequest(b, "POST", "/api/config", body, true, nil, true); w.Code != 200 {
		t.Fatalf("unrelated settings blocked: %d %s", w.Code, w.Body.String())
	}
	c, err := newApp(a.dir)
	if err != nil || c.s.Config.MediaRoot != media || c.managedMediaDir != "" || c.s.Config.Concurrency != 3 {
		t.Fatalf("removing startup override lost saved settings: %v", err)
	}
}

func TestInvalidDeploymentConfigurationDoesNotCreateLibrary(t *testing.T) {
	for _, raw := range []string{"media.example.com", "https://media.example.com/yingku", "https://user:pass@media.example.com", "https://media.example.com?token=x", "https://media.example.com#fragment", "https://media.example.com:99999", "ftp://media.example.com"} {
		t.Run(raw, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "not-created")
			if _, err := newAppWithOptions(dir, appOptions{PublicURL: raw}); err == nil {
				t.Fatal("invalid deployment URL accepted")
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("invalid startup modified library")
			}
		})
	}
	if _, err := newAppWithOptions(filepath.Join(t.TempDir(), "not-created"), appOptions{MediaDir: "relative-media"}); err == nil {
		t.Fatal("relative managed media directory accepted")
	}
}

func TestDeploymentHostBehindProxyKeepsRequestGuards(t *testing.T) {
	a, err := newAppWithOptions(filepath.Join(t.TempDir(), "config"), appOptions{PublicURL: "https://media.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	a.listenAddress = "0.0.0.0:8787"
	for _, tt := range []struct {
		host, origin, remote, method, path string
		token                              bool
		want                               int
	}{
		{"media.example.com", "", "172.20.0.2:1234", "GET", "/api/state", false, 200},
		{"192.168.1.100:8787", "", "172.20.0.2:1234", "GET", "/api/state", false, 200},
		{"other-media.example.com", "", "172.20.0.2:1234", "GET", "/api/state", false, 200},
		{"media.example.com", "https://evil.example.com", "172.20.0.2:1234", "GET", "/api/state", false, 403},
		{"media.example.com", "", "172.20.0.2:1234", "POST", "/api/config", false, 403},
		{"media.example.com", "https://media.example.com", "172.20.0.2:1234", "POST", "/api/network", true, 403},
		{"media.example.com", "", "8.8.8.8:1234", "GET", "/api/state", false, 403},
	} {
		r := httptest.NewRequest(tt.method, "http://localhost"+tt.path, strings.NewReader(`{}`))
		r.Host, r.RemoteAddr = tt.host, tt.remote
		r.Header.Set("Origin", tt.origin)
		r.Header.Set("X-Forwarded-For", "127.0.0.1")
		if tt.token {
			r.Header.Set("X-App-Token", a.csrf)
		}
		w := httptest.NewRecorder()
		a.handler().ServeHTTP(w, r)
		if w.Code != tt.want {
			t.Fatalf("%s %s host %s: got %d, want %d", tt.method, tt.path, tt.host, w.Code, tt.want)
		}
	}
	configureTestPassword(t, a)
	r := httptest.NewRequest("GET", "http://media.example.com/api/state", nil)
	r.RemoteAddr = "172.20.0.2:1234"
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("configured host bypassed existing password")
	}
}

func TestContainerLANAccessWithoutPublicURL(t *testing.T) {
	a := testApp(t)
	a.listenAddress = "0.0.0.0:8787"
	for _, tt := range []struct {
		host, remote, origin string
		want                 int
	}{
		{"192.168.1.100:8787", "192.168.1.20:1234", "", 200},
		{"10.0.0.100:9876", "172.20.0.1:1234", "http://10.0.0.100:9876", 200},
		{"192.168.1.100:8787", "8.8.8.8:1234", "", 403},
		{"192.168.1.100:8787", "192.168.1.20:1234", "https://attacker.example", 403},
		{"media.example.com", "172.20.0.1:1234", "", 200},
	} {
		r := httptest.NewRequest("GET", "http://localhost/api/state", nil)
		r.Host, r.RemoteAddr = tt.host, tt.remote
		r.Header.Set("Origin", tt.origin)
		w := httptest.NewRecorder()
		a.handler().ServeHTTP(w, r)
		if w.Code != tt.want {
			t.Fatalf("host=%s remote=%s: got %d, want %d", tt.host, tt.remote, w.Code, tt.want)
		}
	}
	// A LAN Host value must not turn a localhost-only service into a LAN service.
	a.listenAddress = "127.0.0.1:8787"
	r := httptest.NewRequest("GET", "http://192.168.1.100:8787/api/state", nil)
	r.RemoteAddr = "192.168.1.20:1234"
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("localhost-only listener accepted LAN access")
	}
}

func TestHealthcheckHandlesShutdownAndRedirects(t *testing.T) {
	a := testApp(t)
	server := httptest.NewServer(a.handler())
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "http://")
	if err := serviceHealth(address); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.closing = true
	a.mu.Unlock()
	if err := serviceHealth(address); err == nil {
		t.Fatal("stopping service reported healthy")
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/", http.StatusFound)
	}))
	defer redirect.Close()
	if err := serviceHealth(strings.TrimPrefix(redirect.URL, "http://")); err == nil || !strings.Contains(err.Error(), "302") {
		t.Fatal("healthcheck followed redirect")
	}
}
