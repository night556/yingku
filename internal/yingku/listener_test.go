package yingku

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWildcardListenerAndBrowserAddress(t *testing.T) {
	listener, err := listenService("0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	address := listener.Addr().(*net.TCPAddr)
	if !address.IP.Equal(net.IPv4zero) || address.Port == 0 {
		t.Fatalf("unexpected listener: %v", address)
	}
	for address, want := range map[string]string{
		"0.0.0.0:8787":      "http://127.0.0.1:8787",
		":8787":             "http://127.0.0.1:8787",
		"[::]:8787":         "http://[::1]:8787",
		"127.0.0.1:8788":    "http://127.0.0.1:8788",
		"192.168.1.20:8787": "http://192.168.1.20:8787",
	} {
		if got := browserServiceURL(address); got != want {
			t.Fatalf("%s: got %q want %q", address, got, want)
		}
	}
}

func TestDuplicateStartupWithWildcardAddress(t *testing.T) {
	a := testApp(t)
	server := httptest.NewServer(a.handler())
	defer server.Close()
	u, _ := url.Parse(server.URL)
	running, err := runningInstance("0.0.0.0:"+u.Port(), a.dir)
	if err != nil || !running {
		t.Fatalf("wildcard duplicate startup failed: %v", err)
	}
}

func TestExplicitLoopbackListenerKeepsLANUnavailable(t *testing.T) {
	a := testApp(t)
	a.listenAddress = "127.0.0.1:8787"
	if a.lanReady() || len(lanServiceURLs(a.listenAddress)) != 0 {
		t.Fatal("loopback listener advertised as LAN")
	}
	if w := networkRequest(a, "GET", "/api/state", "", false, nil, false); w.Code != http.StatusForbidden {
		t.Fatal("loopback listener accepted LAN client")
	}
	if w := networkRequest(a, "GET", "/api/state", "", true, nil, false); w.Code != 200 {
		t.Fatal("loopback listener rejected local client")
	}
}

func TestListenerMigrationPreservesProtectedLibrary(t *testing.T) {
	a := testApp(t)
	configureTestPassword(t, a)
	a.s.Version = 6
	raw, err := json.Marshal(a.s)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err = json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	network := state["config"].(map[string]any)["network"].(map[string]any)
	network["enabled"], network["port"] = false, 8789
	raw, err = json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(a.dir, "state.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	upgraded, err := newApp(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	if upgraded.s.Version != 7 || upgraded.s.Config.Network != a.s.Config.Network || !networkRequiresPassword(upgraded.s.Config.Network) {
		t.Fatal("upgrade changed saved access protection")
	}
	backup, err := os.ReadFile(filepath.Join(a.dir, "state-before-listener-v7.json"))
	if err != nil || !bytes.Equal(backup, raw) {
		t.Fatal("upgrade backup does not match old state")
	}
	saved, err := os.ReadFile(filepath.Join(a.dir, "state.json"))
	if err != nil || strings.Contains(string(saved), `"port": 8789`) {
		t.Fatal("obsolete secondary listener settings remain")
	}
}
