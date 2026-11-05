package yingku

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRemoveLocalSourceDetachesLibraryWithoutDeletingFiles(t *testing.T) {
	a := testApp(t)
	root, otherRoot := t.TempDir(), t.TempDir()
	a.s.Sources = []Source{{ID: "local", Type: "local", Root: root}, {ID: "other", Type: "local", Root: otherRoot}}
	physicalFiles := map[string][]byte{}
	write := func(path, content string) {
		t.Helper()
		data := []byte(content)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		physicalFiles[path] = data
	}
	write(filepath.Join(root, "Only.S01E01.mp4"), "exclusive video")
	write(filepath.Join(root, "Shared.S01E01.mp4"), "shared first episode")
	write(filepath.Join(otherRoot, "Shared.S01E02.mp4"), "shared second episode")
	server := httptest.NewServer(a.handler())
	defer server.Close()
	importEpisode := func(source, key, title string) string {
		t.Helper()
		result := request(t, server, a, "POST", "/api/import", ImportRequest{SourceID: source, Type: "tv", Title: title, Mode: "reference", Files: []Selection{{Key: key}}}, 201)
		var id string
		if err := json.Unmarshal(result["itemId"], &id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	exclusive := importEpisode("local", "/Only.S01E01.mp4", "独有剧集")
	shared := importEpisode("local", "/Shared.S01E01.mp4", "共享剧集")
	if id := importEpisode("other", "/Shared.S01E02.mp4", "共享剧集"); id != shared {
		t.Fatal("fixture did not share the same show")
	}
	copyPath, subtitlePath, nfoPath := filepath.Join(root, "Copied.mp4"), filepath.Join(root, "Only.srt"), filepath.Join(root, "tvshow.nfo")
	write(copyPath, "completed copy")
	write(subtitlePath, "original subtitle")
	write(nfoPath, "original metadata")
	a.mu.Lock()
	a.s.Items = append(a.s.Items, Item{ID: "copied-item", Type: "movie", Title: "已复制电影"})
	a.s.Files = append(a.s.Files, MediaFile{ID: "copied-file", ItemID: "copied-item", SourceID: "local", Destination: copyPath, Status: "completed"})
	a.s.Tasks = append(a.s.Tasks, Task{ID: "copied-task", FileID: "copied-file", Status: "completed"})
	removedFile := a.s.Files[0].ID
	keptFile := a.s.Files[2].ID
	a.s.Files[2].Position = 42
	a.s.SubtitlePaths[removedFile] = []SavedSubtitle{{Name: "subtitle", Path: subtitlePath}}
	a.s.SubtitlePaths[keptFile] = []SavedSubtitle{{Name: "kept subtitle", Path: subtitlePath}}
	a.s.ItemAliases = map[string]string{"old-exclusive": exclusive, "older-exclusive": "old-exclusive", "old-shared": shared}
	a.metadataJobs[exclusive], a.metadataJobs[shared] = "removed-job", "kept-job"
	ctx, cancel := context.WithCancel(context.Background())
	a.scans = map[string]*ScanSession{"scan": {SourceID: "local", cancel: cancel}, "other-scan": {SourceID: "other"}}
	a.mu.Unlock()
	result := request(t, server, a, "DELETE", "/api/sources/local", nil, 200)
	if string(result["removedFiles"]) != "3" || string(result["removedItems"]) != "2" {
		t.Fatalf("unexpected result: %s", result)
	}
	if ctx.Err() == nil || a.scans["scan"] != nil || a.scans["other-scan"] == nil {
		t.Fatal("source scan was not invalidated independently")
	}
	if len(a.s.Items) != 1 || a.s.Items[0].ID != shared || len(a.s.Files) != 1 || a.s.Files[0].ID != keptFile || a.s.Files[0].Position != 42 || len(a.s.Tasks) != 0 {
		t.Fatal("exclusive records remain or shared records were changed")
	}
	if len(a.s.ItemAliases) != 1 || a.s.ItemAliases["old-shared"] != shared || len(a.s.SubtitlePaths) != 1 || len(a.s.SubtitlePaths[keptFile]) != 1 || a.metadataJobs[exclusive] != "" || a.metadataJobs[shared] != "kept-job" {
		t.Fatal("related indexes were not detached selectively")
	}
	for path, want := range physicalFiles {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("physical file changed: %s (%v)", path, err)
		}
	}
	reloaded, err := newApp(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.s.Sources) != 1 || reloaded.s.Sources[0].ID != "other" || len(reloaded.s.Items) != 1 || len(reloaded.s.Files) != 1 || len(reloaded.s.SubtitlePaths) != 1 {
		t.Fatal("removal did not persist")
	}
}

func TestRemoveSourceKeepsDownloadedLibraryAndBlocksUnfinishedTasks(t *testing.T) {
	for _, kind := range []string{"local", "baidu", "quark", "webdav"} {
		t.Run(kind, func(t *testing.T) {
			a := testApp(t)
			a.s.Sources = []Source{{ID: "source", Type: kind}}
			a.s.Items = []Item{{ID: "item", Type: "movie"}}
			a.s.Files = []MediaFile{{ID: "file", ItemID: "item", SourceID: "source", Status: "completed"}}
			a.s.Tasks = []Task{{ID: "task", FileID: "file", Status: "queued"}}
			server := httptest.NewServer(a.handler())
			defer server.Close()
			for _, status := range []string{"queued", "downloading", "paused", "failed", "finalizing"} {
				a.s.Tasks[0].Status = status
				before, _ := json.Marshal(a.s)
				request(t, server, a, "DELETE", "/api/sources/source", nil, 400)
				after, _ := json.Marshal(a.s)
				if !bytes.Equal(before, after) {
					t.Fatalf("blocked removal changed state for %s", status)
				}
			}
			a.s.Tasks[0].Status = "cancelled"
			a.active["task"] = func() {}
			request(t, server, a, "DELETE", "/api/sources/source", nil, 400)
			delete(a.active, "task")
			a.s.Tasks[0].Status = "completed"
			request(t, server, a, "DELETE", "/api/sources/source", nil, 200)
			if kind != "local" && (len(a.s.Items) != 1 || len(a.s.Files) != 1 || len(a.s.Tasks) != 1) {
				t.Fatal("removing a download connection discarded its library")
			}
			request(t, server, a, "DELETE", "/api/sources/source", nil, 400)
		})
	}
}

func TestRemovedLocalSourceRepairBacksUpAndPreservesDownloadedCopies(t *testing.T) {
	a := testApp(t)
	a.s.Sources = []Source{{ID: "live", Type: "local"}}
	a.s.Items = []Item{{ID: "orphan", Type: "movie"}, {ID: "downloaded", Type: "movie"}, {ID: "shared", Type: "tv"}}
	a.s.Files = []MediaFile{
		{ID: "orphan-ref", ItemID: "orphan", SourceID: "removed-local", Reference: true},
		{ID: "download", ItemID: "downloaded", SourceID: "removed-cloud", Status: "completed"},
		{ID: "shared-removed", ItemID: "shared", SourceID: "removed-local", Reference: true},
		{ID: "shared-kept", ItemID: "shared", SourceID: "live", Reference: true},
	}
	if err := a.saveLocked(); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(filepath.Join(a.dir, "state.json"))
	reloaded, err := newApp(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.s.Items) != 2 || len(reloaded.s.Files) != 2 || reloaded.s.Files[0].ID != "download" || reloaded.s.Files[1].ID != "shared-kept" {
		t.Fatal("repair affected downloaded copies or retained orphan references")
	}
	backupPath := filepath.Join(a.dir, "state-before-source-cleanup-v0133.json")
	backup, err := os.ReadFile(backupPath)
	if err != nil || !bytes.Equal(original, backup) {
		t.Fatal("original catalogue was not backed up intact")
	}
	again, err := newApp(a.dir)
	if err != nil || !reflect.DeepEqual(reloaded.s.Files, again.s.Files) {
		t.Fatal("repeated startup changed the repaired library")
	}
	backupAgain, _ := os.ReadFile(backupPath)
	if !bytes.Equal(backup, backupAgain) {
		t.Fatal("original backup was overwritten")
	}
}

func TestRemoveSourceSaveFailureRestoresCatalogue(t *testing.T) {
	a := testApp(t)
	a.s.Sources = []Source{{ID: "local", Type: "local"}}
	a.s.Items = []Item{{ID: "item", Type: "movie"}}
	a.s.Files = []MediaFile{{ID: "file", ItemID: "item", SourceID: "local", Reference: true}}
	a.s.ItemAliases = map[string]string{"old": "item"}
	a.s.SubtitlePaths["file"] = []SavedSubtitle{{Name: "original"}}
	original, _ := json.Marshal(a.s)
	// An occupied directory at the state-file path makes the final atomic rename fail.
	if err := os.Remove(filepath.Join(a.dir, "state.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(a.dir, "state.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := a.removeSource("local"); err == nil {
		t.Fatal("expected persistence failure")
	}
	after, _ := json.Marshal(a.s)
	if !bytes.Equal(original, after) {
		t.Fatal("failed save changed the in-memory catalogue")
	}
}
