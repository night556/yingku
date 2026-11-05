package main

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAddressInUseFromRealListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	duplicate, err := net.Listen("tcp", listener.Addr().String())
	if err == nil {
		duplicate.Close()
		t.Fatal("duplicate listener unexpectedly succeeded")
	}
	if !isAddressInUse(err) {
		t.Fatalf("platform's bind error not recognized: %v", err)
	}
	if isAddressInUse(errors.New("other startup failure")) {
		t.Fatal("unrelated error identified as occupied port")
	}
}

func TestRunningInstanceSameLibrary(t *testing.T) {
	a := testApp(t)
	server := httptest.NewServer(a.handler())
	defer server.Close()
	addr := strings.TrimPrefix(server.URL, "http://")
	running, err := runningInstance(addr, filepath.Join(a.dir, "..", "data"))
	if err != nil || !running {
		t.Fatalf("same library: running=%v, err=%v", running, err)
	}
	running, err = runningInstance(addr, filepath.Join(t.TempDir(), "other-library"))
	if running || !errors.Is(err, errOtherLibrary) {
		t.Fatalf("different library: running=%v, err=%v", running, err)
	}
}

func TestRunningInstanceRejectsOtherService(t *testing.T) {
	for _, body := range []string{
		`<html>Another service</html>`,
		`{"service":"other","protocol":1,"dataId":"anything"}`,
		`{"service":"yingku","protocol":1}`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			if running, _ := runningInstance(strings.TrimPrefix(server.URL, "http://"), t.TempDir()); running {
				t.Fatal("unrelated server identified as Yingku")
			}
		})
	}
}

func TestRunningInstanceDoesNotFollowRedirect(t *testing.T) {
	var followed atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		followed.Store(true)
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer server.Close()
	running, err := runningInstance(strings.TrimPrefix(server.URL, "http://"), t.TempDir())
	if running || err != nil || followed.Load() {
		t.Fatalf("redirect: running=%v, err=%v, followed=%v", running, err, followed.Load())
	}
}
