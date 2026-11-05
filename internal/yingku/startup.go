package yingku

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

var errOtherLibrary = errors.New("运行中的映库使用了不同的媒体库")

type instanceInfo struct {
	Service  string `json:"service"`
	Protocol int    `json:"protocol"`
	DataID   string `json:"dataId"`
}

func isAddressInUse(err error) bool {
	// Windows bind uses Winsock's WSAEADDRINUSE (10048), not syscall.EADDRINUSE.
	return errors.Is(err, syscall.EADDRINUSE) ||
		runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(10048))
}

// Identify the data directory without publishing its absolute path or secrets.
func dataDirectoryID(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	abs = filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(abs)
	}
	hash := sha256.Sum256([]byte(abs))
	return hex.EncodeToString(hash[:]), nil
}

// Only reuse a confirmed Yingku instance with the same data directory.
func runningInstance(addr, dataDir string) (bool, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Get(browserServiceURL(addr) + "/api/instance")
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, nil
	}
	var info instanceInfo
	if err = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&info); err != nil {
		return false, err
	}
	if info.Service != "yingku" || info.Protocol != 1 || info.DataID == "" {
		return false, nil
	}
	id, err := dataDirectoryID(dataDir)
	if err != nil {
		return false, err
	}
	if info.DataID != id {
		return false, errOtherLibrary
	}
	return true, nil
}
