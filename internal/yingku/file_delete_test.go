package yingku

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLibraryDiskDeleteRequiresOptInAndKeepsSidecars(t *testing.T) {
	for _, id := range []string{"movie-video", "combined"} {
		t.Run(id, func(t *testing.T) {
			a, server, physical := libraryRemovalFixture(t)
			file, _ := a.file(id)
			preview, err := a.libraryDeletePreview("", nil, id)
			if err != nil {
				t.Fatal(err)
			}
			request(t, server, a, "DELETE", "/api/files/"+id, map[string]any{"deleteFiles": true}, 400)
			if _, err = os.Stat(file.Destination); err != nil {
				t.Fatal("unconfirmed deletion changed the file")
			}
			response := request(t, server, a, "DELETE", "/api/files/"+id, map[string]any{"deleteFiles": true, "deleteToken": preview.Token}, 200)
			var deleted int
			_ = json.Unmarshal(response["deletedDiskFiles"], &deleted)
			if deleted != len(preview.Files) {
				t.Fatalf("deleted %d, wanted %d", deleted, len(preview.Files))
			}
			removed := map[string]bool{}
			for _, entry := range preview.Files {
				removed[entry.Path] = true
			}
			for path, original := range physical {
				data, err := os.ReadFile(path)
				if removed[path] {
					if !os.IsNotExist(err) {
						t.Fatalf("selected server file remains: %s", path)
					}
				} else if err != nil || !bytes.Equal(data, original) {
					t.Fatalf("unselected video or sidecar changed: %s", path)
				}
			}
			if len(a.s.Sources) != 2 || len(a.s.FileDeletes) != 0 {
				t.Fatal("source configuration changed or delete journal was not cleared")
			}
		})
	}
}

func TestLibraryDiskDeleteRejectsChangedAndReplacedFiles(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "changed", true: "replaced"}[replace], func(t *testing.T) {
			a, _, _ := libraryRemovalFixture(t)
			file, _ := a.file("movie-video")
			preview, err := a.libraryDeletePreview("movie", nil, "")
			if err != nil {
				t.Fatal(err)
			}
			info, _ := os.Stat(file.Destination)
			content, _ := os.ReadFile(file.Destination)
			if replace {
				if err = os.Rename(file.Destination, file.Destination+".old"); err != nil {
					t.Fatal(err)
				}
				err = os.WriteFile(file.Destination, content, 0600)
				if err == nil {
					err = os.Chtimes(file.Destination, info.ModTime(), info.ModTime())
				}
			} else {
				err = os.WriteFile(file.Destination, []byte("different content"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.removeLibrary("movie", nil, "", libraryDeleteOptions{true, preview.Token}); err == nil {
				t.Fatal("stale confirmation allowed file deletion")
			}
			if _, err = os.Stat(file.Destination); err != nil {
				t.Fatal("changed file disappeared")
			}
			if _, err = a.item("movie"); err != nil {
				t.Fatal("stale preview removed catalogue records")
			}
		})
	}
}

func TestLibraryDiskDeleteRejectsSharedPathsLinksAndDirectories(t *testing.T) {
	t.Run("shared", func(t *testing.T) {
		a, _, _ := libraryRemovalFixture(t)
		file, _ := a.file("movie-video")
		file.ID = "shared-file"
		file.ItemID = "show"
		a.s.Files = append(a.s.Files, file)
		if _, err := a.libraryDeletePreview("movie", nil, ""); err == nil {
			t.Fatal("shared physical path can be deleted")
		}
		if _, err := a.removeLibrary("movie", nil, ""); err != nil {
			t.Fatal("record-only removal should remain possible")
		}
		if _, err := os.Stat(file.Destination); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("directory", func(t *testing.T) {
		a, _, _ := libraryRemovalFixture(t)
		a.s.Files[0].Destination = t.TempDir()
		if _, err := a.libraryDeletePreview("movie", nil, ""); err == nil {
			t.Fatal("directory accepted")
		}
	})
	t.Run("link", func(t *testing.T) {
		a, _, _ := libraryRemovalFixture(t)
		file, _ := a.file("movie-video")
		link := file.Destination + ".link.mp4"
		if err := os.Symlink(file.Destination, link); err != nil {
			t.Skip("symlink creation is unavailable")
		}
		a.s.Files[0].Destination = link
		if _, err := a.libraryDeletePreview("movie", nil, ""); err == nil {
			t.Fatal("link accepted")
		}
	})
	t.Run("casting", func(t *testing.T) {
		a, _, _ := libraryRemovalFixture(t)
		a.casts = map[string]*CastSession{"active": {FileID: "movie-video", Status: "PLAYING"}}
		if _, err := a.libraryDeletePreview("movie", nil, ""); err == nil {
			t.Fatal("actively cast video can be deleted")
		}
	})
}

func TestLibraryDiskDeleteRollsBackOnStateSaveFailure(t *testing.T) {
	a, _, physical := libraryRemovalFixture(t)
	preview, err := a.libraryDeletePreview("show", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(a.s)
	if err = os.Remove(filepath.Join(a.dir, "state.json")); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(a.dir, "state.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = a.removeLibrary("show", nil, "", libraryDeleteOptions{true, preview.Token}); err == nil {
		t.Fatal("expected persistence failure")
	}
	after, _ := json.Marshal(a.s)
	if !bytes.Equal(before, after) {
		t.Fatal("failed deletion changed the catalogue")
	}
	for path, original := range physical {
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, original) {
			t.Fatalf("original file was not restored: %s (%v)", path, err)
		}
	}
}

func TestLibraryDiskDeleteRecoveryBeforeAndAfterCommit(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "restore-uncommitted", true: "finish-committed"}[committed], func(t *testing.T) {
			a, _, _ := libraryRemovalFixture(t)
			file, _ := a.file("movie-video")
			plan, err := a.diskDeletePlanLocked(map[string]bool{file.ID: true})
			if err != nil {
				t.Fatal(err)
			}
			job, err := a.stageDiskDelete(plan)
			if err != nil {
				t.Fatal(err)
			}
			if committed {
				a.detachLibraryFilesLocked(map[string]bool{file.ID: true})
				a.s.FileDeletes = map[string]bool{job.ID: true}
				if err = a.saveLocked(); err != nil {
					t.Fatal(err)
				}
			}
			reopened, err := newApp(a.dir)
			if err != nil {
				t.Fatal(err)
			}
			_, err = os.Stat(file.Destination)
			if committed && !os.IsNotExist(err) || !committed && err != nil {
				t.Fatalf("incorrect recovery: committed=%v, %v", committed, err)
			}
			if _, err = os.Stat(a.deleteJobPath(job.ID)); !os.IsNotExist(err) {
				t.Fatal("recovery left journal")
			}
			if len(reopened.s.FileDeletes) != 0 {
				t.Fatal("commit marker remains")
			}
			if _, err = os.Stat(file.Destination + ".srt"); err != nil {
				t.Fatal("subtitle was deleted")
			}
		})
	}
}

func TestLibraryDiskDeleteRecoveryDoesNotOverwriteNewOriginal(t *testing.T) {
	a, _, _ := libraryRemovalFixture(t)
	file, _ := a.file("movie-video")
	plan, err := a.diskDeletePlanLocked(map[string]bool{file.ID: true})
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.stageDiskDelete(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file.Destination, []byte("new original"), 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := newApp(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.fileDeleteWarnings) == 0 {
		t.Fatal("conflicting recovery should require attention")
	}
	data, _ := os.ReadFile(file.Destination)
	if string(data) != "new original" {
		t.Fatal("new original overwritten")
	}
	if _, err = os.Stat(job.Targets[0].Stage); err != nil {
		t.Fatal("old video not retained")
	}
}

func TestLibraryDeletePreviewHTTPDoesNotExposeStagingPaths(t *testing.T) {
	a, server, _ := libraryRemovalFixture(t)
	response := request(t, server, a, "GET", "/api/items/movie/removal-preview", nil, 200)
	raw, _ := json.Marshal(response)
	if strings.Contains(string(raw), "yingku-delete-") || strings.Contains(string(raw), "delete-jobs") {
		t.Fatal("preview exposes internal transaction details")
	}
	if len(response["token"]) == 0 || len(response["files"]) == 0 {
		t.Fatal("preview incomplete")
	}
}

func TestLibraryDiskDeleteExpiredConfirmationAndLongFilename(t *testing.T) {
	t.Run("expired", func(t *testing.T) {
		a, _, _ := libraryRemovalFixture(t)
		preview, err := a.libraryDeletePreview("movie", nil, "")
		if err != nil {
			t.Fatal(err)
		}
		entry := a.deletePreviews[preview.Token]
		entry.Expires = time.Time{}
		a.deletePreviews[preview.Token] = entry
		if _, err = a.removeLibrary("movie", nil, "", libraryDeleteOptions{true, preview.Token}); err == nil {
			t.Fatal("expired confirmation was accepted")
		}
	})
	t.Run("long-name", func(t *testing.T) {
		a, _, _ := libraryRemovalFixture(t)
		path := filepath.Join(t.TempDir(), strings.Repeat("v", 220)+".mp4")
		if err := os.WriteFile(path, []byte("long video name"), 0600); err != nil {
			t.Skip("long names not supported by this filesystem")
		}
		a.s.Files[0].Destination = path
		preview, err := a.libraryDeletePreview("movie", nil, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = a.removeLibrary("movie", nil, "", libraryDeleteOptions{true, preview.Token}); err != nil {
			t.Fatal(err)
		}
		if _, err = os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("long file remains")
		}
	})
}

func TestLibraryDiskDeleteUnfinishedCleanupDoesNotBlockStartup(t *testing.T) {
	a, _, _ := libraryRemovalFixture(t)
	plan, err := a.diskDeletePlanLocked(map[string]bool{"movie-video": true})
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.stageDiskDelete(plan)
	if err != nil {
		t.Fatal(err)
	}
	a.detachLibraryFilesLocked(map[string]bool{"movie-video": true})
	a.s.FileDeletes = map[string]bool{job.ID: true}
	if err = a.saveLocked(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(job.Targets[0].Stage, []byte("changed staged video"), 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := newApp(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.fileDeleteWarnings) == 0 || !reopened.s.FileDeletes[job.ID] {
		t.Fatal("pending cleanup or warning was lost")
	}
	if _, err = os.Stat(job.Targets[0].Stage); err != nil {
		t.Fatal("changed staged file was not retained")
	}
}
