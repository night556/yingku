package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func libraryRemovalFixture(t *testing.T) (*App, *httptest.Server, map[string][]byte) {
	t.Helper()
	a := testApp(t)
	a.s.Sources = []Source{{ID: "disk", Type: "local", Root: t.TempDir()}, {ID: "cloud", Type: "baidu"}}
	a.s.Items = []Item{{ID: "movie", Type: "movie", Title: "电影"}, {ID: "show", Type: "tv", Title: "电视剧"}}
	a.s.ItemAliases = map[string]string{"movie-alias": "movie", "show-alias": "show"}
	physical := map[string][]byte{}
	for _, file := range []MediaFile{
		{ID: "movie-video", ItemID: "movie", SourceID: "disk", Reference: true, Status: "completed"},
		{ID: "special", ItemID: "show", SourceID: "disk", Season: 0, Episode: 1, Reference: true, Status: "completed"},
		{ID: "episode1", ItemID: "show", SourceID: "disk", Season: 1, Episode: 1, Reference: true, Status: "completed"},
		{ID: "combined", ItemID: "show", SourceID: "cloud", Season: 1, Episode: 2, EpisodeEnd: 3, Status: "queued"},
		{ID: "season2", ItemID: "show", SourceID: "cloud", Season: 2, Episode: 1, Status: "completed", Position: 25},
	} {
		file.Destination = filepath.Join(a.s.Sources[0].Root, file.ID+".mp4")
		data := []byte("original video: " + file.ID)
		if err := os.WriteFile(file.Destination, data, 0600); err != nil {
			t.Fatal(err)
		}
		physical[file.Destination] = data
		a.s.Files = append(a.s.Files, file)
		subPath := file.Destination + ".srt"
		if err := os.WriteFile(subPath, []byte("original subtitle"), 0600); err != nil {
			t.Fatal(err)
		}
		physical[subPath] = []byte("original subtitle")
		a.s.SubtitlePaths[file.ID] = []SavedSubtitle{{Name: "字幕", Path: subPath}}
		if !file.Reference {
			a.s.Tasks = append(a.s.Tasks, Task{ID: "task-" + file.ID, FileID: file.ID, Status: file.Status})
		}
	}
	part := a.s.Files[3].Destination + ".yingku-task-combined.part"
	if err := os.WriteFile(part, []byte("partial download"), 0600); err != nil {
		t.Fatal(err)
	}
	physical[part] = []byte("partial download")
	a.metadataJobs["movie"], a.metadataJobs["show"] = "movie-job", "show-job"
	if err := a.saveLocked(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.handler())
	t.Cleanup(server.Close)
	return a, server, physical
}

func TestLibraryRemovalScopesPersistAndKeepDiskFiles(t *testing.T) {
	for _, test := range []struct {
		name, path string
		removed    []string
		items      int
	}{
		{"movie alias", "/api/items/movie-alias", []string{"movie-video"}, 1},
		{"entire show", "/api/items/show-alias", []string{"special", "episode1", "combined", "season2"}, 1},
		{"one season", "/api/items/show/seasons/1", []string{"episode1", "combined"}, 2},
		{"specials", "/api/items/show/seasons/0", []string{"special"}, 2},
		{"one episode", "/api/files/episode1", []string{"episode1"}, 2},
		{"combined episode file", "/api/files/combined", []string{"combined"}, 2},
		{"last movie video", "/api/files/movie-video", []string{"movie-video"}, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, server, physical := libraryRemovalFixture(t)
			before := a.s
			result := request(t, server, a, "DELETE", test.path, nil, 200)
			var count int
			json.Unmarshal(result["removedFiles"], &count)
			if count != len(test.removed) || len(a.s.Items) != test.items || len(a.s.Files) != len(before.Files)-count {
				t.Fatalf("incorrect removal scope: %s", result)
			}
			removed := map[string]bool{}
			for _, id := range test.removed {
				removed[id] = true
			}
			for _, file := range a.s.Files {
				if removed[file.ID] {
					t.Fatal("selected file still in library")
				}
				for _, original := range before.Files {
					if original.ID == file.ID && !reflect.DeepEqual(original, file) {
						t.Fatal("unselected video or progress changed")
					}
				}
			}
			for _, task := range a.s.Tasks {
				if removed[task.FileID] {
					t.Fatal("download record was retained")
				}
			}
			for id := range a.s.SubtitlePaths {
				if removed[id] {
					t.Fatal("removed subtitle association was retained")
				}
			}
			if !reflect.DeepEqual(before.Sources, a.s.Sources) {
				t.Fatal("source connections changed")
			}
			for path, expected := range physical {
				actual, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(expected, actual) {
					t.Fatalf("disk file changed: %s (%v)", path, err)
				}
			}
			reloaded, err := newApp(a.dir)
			if err != nil || len(reloaded.s.Files) != len(a.s.Files) || len(reloaded.s.Items) != test.items || len(reloaded.s.SubtitlePaths) != len(a.s.SubtitlePaths) {
				t.Fatal("library removal did not persist", err)
			}
			request(t, server, a, "DELETE", test.path, nil, 400)
		})
	}
}

func TestLibraryRemovalRejectsInvalidScopesAndBusyTasks(t *testing.T) {
	a, server, _ := libraryRemovalFixture(t)
	for _, path := range []string{"/api/items/missing", "/api/files/missing", "/api/items/movie/seasons/1", "/api/items/show/seasons/-1", "/api/items/show/seasons/1000", "/api/items/show/seasons/not-a-number", "/api/items/show/seasons/9"} {
		request(t, server, a, "DELETE", path, nil, 400)
	}
	for _, status := range []string{"downloading", "finalizing", "paused"} {
		a.s.Tasks[0].Status = status
		if status == "paused" {
			a.active[a.s.Tasks[0].ID] = func() {}
		}
		before, _ := json.Marshal(a.s)
		for _, path := range []string{"/api/items/show", "/api/items/show/seasons/1", "/api/files/combined"} {
			request(t, server, a, "DELETE", path, nil, 400)
		}
		after, _ := json.Marshal(a.s)
		if !bytes.Equal(before, after) {
			t.Fatal("rejected removal changed library or task state")
		}
	}
	delete(a.active, a.s.Tasks[0].ID)
	request(t, server, a, "DELETE", "/api/files/combined", nil, 200)
}

func TestLibraryRemovalLastTVEpisodeAndEmptyItem(t *testing.T) {
	a, server, _ := libraryRemovalFixture(t)
	request(t, server, a, "DELETE", "/api/items/show/seasons/0", nil, 200)
	request(t, server, a, "DELETE", "/api/items/show/seasons/1", nil, 200)
	request(t, server, a, "DELETE", "/api/files/season2", nil, 200)
	if len(a.s.Items) != 1 || a.s.ItemAliases["show-alias"] != "" || a.metadataJobs["show"] != "" {
		t.Fatal("empty TV parent or aliases were retained")
	}
	a.s.Items = append(a.s.Items, Item{ID: "empty", Type: "movie"})
	request(t, server, a, "DELETE", "/api/items/empty", nil, 200)
	if len(a.s.Items) != 1 {
		t.Fatal("empty item could not be removed")
	}
}

func TestLibraryRemovalSaveFailureRollsBack(t *testing.T) {
	a, _, _ := libraryRemovalFixture(t)
	before, _ := json.Marshal(a.s)
	if err := os.Remove(filepath.Join(a.dir, "state.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(a.dir, "state.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := a.removeLibrary("show", nil, ""); err == nil {
		t.Fatal("expected save error")
	}
	after, _ := json.Marshal(a.s)
	if !bytes.Equal(before, after) || a.metadataJobs["show"] == "" {
		t.Fatal("save failure changed catalogue or invalidated metadata jobs")
	}
}
