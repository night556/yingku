package yingku

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestVideoDimensionsPersistWithoutChangingProgressOrMedia(t *testing.T) {
	a := testApp(t)
	file := filepath.Join(t.TempDir(), "sample.mp4")
	content := []byte("original media bytes")
	if err := os.WriteFile(file, content, 0600); err != nil {
		t.Fatal(err)
	}
	a.s.Items = []Item{{ID: "movie", Type: "movie"}}
	a.s.Files = []MediaFile{{ID: "video", ItemID: "movie", Destination: file, Status: "completed", Position: 123, Duration: 7200}}
	server := httptest.NewServer(a.handler())
	defer server.Close()
	request(t, server, a, "POST", "/api/files/video/video-info", map[string]any{"width": 1920, "height": 1080}, 200)
	request(t, server, a, "POST", "/api/files/video/video-info", map[string]any{"width": 1920, "height": 1080}, 200)
	reloaded, err := newApp(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	got := reloaded.s.Files[0]
	if got.VideoWidth != 1920 || got.VideoHeight != 1080 || got.Position != 123 || got.Duration != 7200 {
		t.Fatal("video dimensions or original playback position were not preserved", got)
	}
	actual, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(actual, content) {
		t.Fatal("media file was changed")
	}
	for _, input := range []map[string]any{{}, {"width": 1920}, {"width": -1, "height": 1080}, {"width": 1920, "height": 32769}, {"width": 1920.5, "height": 1080}} {
		request(t, server, a, "POST", "/api/files/video/video-info", input, 400)
	}
	request(t, server, a, "POST", "/api/files/missing/video-info", map[string]int{"width": 1920, "height": 1080}, 400)
	a.s.Files[0].Status = "downloading"
	request(t, server, a, "POST", "/api/files/video/video-info", map[string]int{"width": 3840, "height": 2160}, 400)
	if a.s.Files[0].VideoWidth != 1920 || a.s.Files[0].VideoHeight != 1080 {
		t.Fatal("invalid metadata overwrote the saved dimensions")
	}
}
