package yingku

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUnavailableFileRecoveryPreservesIdentityAndProgress(t *testing.T) {
	a := testApp(t)
	root := t.TempDir()
	old := filepath.Join(root, "原位置.mp4")
	target := filepath.Join(root, "新位置.mp4")
	if err := os.WriteFile(target, []byte("test video"), 0600); err != nil {
		t.Fatal(err)
	}
	a.s.Items = []Item{{ID: "movie", Title: "保留作品资料", Type: "movie", TMDBID: 42}}
	a.s.Sources = []Source{{ID: "local", Type: "local", Root: root}}
	a.s.Files = []MediaFile{{ID: "file", ItemID: "movie", SourceID: "local", SourceKey: "/原位置.mp4", Name: "原位置.mp4", Destination: old, Status: "completed", Position: 123, Duration: 3600, Reference: true}}
	f, _ := a.file("file")
	health := a.waitFileAvailability(context.Background(), f)
	if health.Status != "missing" {
		t.Fatal("missing file not identified", health)
	}
	if err := a.relocateMedia("file", target); err != nil {
		t.Fatal(err)
	}
	f, _ = a.file("file")
	if f.ID != "file" || f.ItemID != "movie" || f.Position != 123 || f.Duration != 3600 || f.Destination != target || f.SourceKey != "/新位置.mp4" || a.s.Items[0].TMDBID != 42 {
		t.Fatal("recovery lost library identity", f)
	}
	if health = a.waitFileAvailability(context.Background(), f); health.Status != "available" {
		t.Fatal("recovered file still marked missing", health)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatal("recovery altered physical file", err)
	}
	restarted, err := newApp(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	f, _ = restarted.file("file")
	if f.Destination != target || f.Position != 123 {
		t.Fatal("recovery not persisted")
	}
}

func TestFileRelocationRejectsInvalidBusyAndDuplicateTargets(t *testing.T) {
	a := testApp(t)
	root := t.TempDir()
	target := filepath.Join(root, "target.mp4")
	os.WriteFile(target, []byte("video"), 0600)
	original := MediaFile{ID: "file", ItemID: "movie", Destination: filepath.Join(root, "gone.mp4"), Status: "completed", Position: 77}
	a.s.Files = []MediaFile{original}
	for _, bad := range []string{"relative.mp4", filepath.Join(root, "gone.mp4"), filepath.Join(root, "wrong.txt"), root} {
		if err := a.relocateMedia("file", bad); err == nil {
			t.Fatal("invalid relocation accepted", bad)
		}
	}
	a.s.Tasks = []Task{{ID: "task", FileID: "file", Status: "downloading"}}
	if err := a.relocateMedia("file", target); err == nil {
		t.Fatal("active download relocated")
	}
	a.s.Tasks = nil
	a.s.Files = append(a.s.Files, MediaFile{ID: "other", Destination: target, Status: "completed"})
	if err := a.relocateMedia("file", target); err == nil {
		t.Fatal("shared target accepted")
	}
	a.s.Files = a.s.Files[:1]
	if err := os.Remove(filepath.Join(a.dir, "state.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(a.dir, "state.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.relocateMedia("file", target); err == nil {
		t.Fatal("failed save accepted")
	}
	f, _ := a.file("file")
	if f.Destination != original.Destination || f.Position != 77 {
		t.Fatal("failed save changed original record")
	}
}

func TestFileCheckMarksReturnedFilesAvailableAndSkipsDownloads(t *testing.T) {
	a := testApp(t)
	path := filepath.Join(t.TempDir(), "video.mp4")
	a.s.Files = []MediaFile{{ID: "file", Destination: path, Status: "completed"}, {ID: "queued", Destination: path, Status: "queued"}}
	f, _ := a.file("file")
	if a.waitFileAvailability(context.Background(), f).Status != "missing" {
		t.Fatal("missing file not flagged")
	}
	os.WriteFile(path, []byte("video"), 0600)
	a.scheduleFileAvailability("*")
	waitFor(t, time.Second, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.fileAvailabilityLocked(f).Status == "available"
	})
	a.mu.Lock()
	_, queued := a.fileHealth["queued"]
	a.mu.Unlock()
	if queued {
		t.Fatal("unfinished download flagged missing")
	}
	server := httptest.NewServer(a.handler())
	defer server.Close()
	request(t, server, a, "POST", "/api/files/check", map[string]any{}, 202)
}
