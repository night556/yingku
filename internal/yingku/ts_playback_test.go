package yingku

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTSCompatibilityPlayback(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg is required for the TS playback integration test")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is required for the TS playback integration test")
	}
	encoders, err := exec.Command(ffmpeg, "-hide_banner", "-encoders").Output()
	if err != nil || !bytes.Contains(encoders, []byte("libx264")) {
		t.Skip("the test sample requires the libx264 encoder")
	}
	a := testApp(t)
	t.Cleanup(a.stopTSPlayback)
	dir := t.TempDir()
	for _, sample := range []struct{ id, codec, audio string }{
		{"compatible", "libx264", "aac"},
		{"mpeg2", "mpeg2video", "mp2"},
	} {
		path := filepath.Join(dir, "示例 "+sample.id+".ts")
		args := []string{"-v", "error", "-f", "lavfi", "-i", "testsrc2=s=160x90:r=25",
			"-f", "lavfi", "-i", "sine=frequency=440", "-t", "2", "-c:v", sample.codec, "-c:a", sample.audio, path}
		if output, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
			t.Fatalf("create %s test sample: %v\n%s", sample.id, err, output)
		}
		a.s.Files = append(a.s.Files, MediaFile{ID: sample.id, Name: filepath.Base(path), Destination: path, Status: "completed"})
	}
	server := httptest.NewServer(a.handler())
	defer server.Close()
	waitForPreparation := func() {
		t.Helper()
		a.tsPlayback.mu.Lock()
		done := a.tsPlayback.job.done
		a.tsPlayback.mu.Unlock()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("TS preparation did not finish")
		}
	}
	const endpoint = "/api/files/compatible/ts-playback"
	original, err := os.ReadFile(a.s.Files[0].Destination)
	if err != nil {
		t.Fatal(err)
	}
	request(t, server, a, "POST", endpoint, nil, http.StatusAccepted)
	waitForPreparation()
	ready := request(t, server, a, "GET", endpoint, nil, http.StatusOK)
	var key string
	if err := json.Unmarshal(ready["key"], &key); err != nil || len(key) != 64 {
		t.Fatalf("missing cache key: %s", ready["key"])
	}
	cached := request(t, server, a, "POST", endpoint, nil, http.StatusOK)
	if string(cached["key"]) != string(ready["key"]) {
		t.Fatal("preparing the same source should reuse its cache")
	}
	read, err := http.NewRequest(http.MethodGet, server.URL+endpoint+"/"+key, nil)
	if err != nil {
		t.Fatal(err)
	}
	read.Header.Set("Range", "bytes=0-15")
	response, err := server.Client().Do(read)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusPartialContent || len(body) != 16 ||
		response.Header.Get("Content-Type") != "video/mp4" || string(body[4:8]) != "ftyp" {
		t.Fatalf("expected seekable MP4 byte ranges, got %d %q: %v", response.StatusCode, body, err)
	}
	path := a.s.Files[0].Destination
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(original, after) {
		t.Fatal("preparation changed the source TS")
	}
	next := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, next, next); err != nil {
		t.Fatal(err)
	}
	response, err = server.Client().Get(server.URL + endpoint + "/" + key)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("old cache key survived a source change: %d", response.StatusCode)
	}
	const unsupported = "/api/files/mpeg2/ts-playback"
	request(t, server, a, "POST", unsupported, nil, http.StatusAccepted)
	waitForPreparation()
	failed := request(t, server, a, "GET", unsupported, nil, http.StatusBadRequest)
	if !strings.Contains(string(failed["error"]), "需要转码") {
		t.Fatalf("expected an explanation for unsupported codecs, got %s", failed["error"])
	}
}
