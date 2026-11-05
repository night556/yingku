package yingku

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestSeriesIdentityAndRemakes(t *testing.T) {
	cases := []struct {
		name      string
		items     []Item
		probe     Item
		want      string
		ambiguous bool
	}{
		{"missing year after scrape", []Item{{ID: "a", Type: "tv", Title: "犯罪心理", Year: "2005", MetadataProvider: "wikipedia", MetadataID: "zh:323405"}}, Item{Type: "tv", Title: "犯罪心理 第二季"}, "a", false},
		{"stable provider identity", []Item{{ID: "a", Type: "tv", Title: "犯罪心理", Year: "2005", TMDBID: 4057}}, Item{Type: "tv", Title: "Criminal Minds Season 02", Year: "2006", TMDBID: 4057}, "a", false},
		{"conflicting provider identity", []Item{{ID: "a", Type: "tv", Title: "Drama", Year: "2005", TMDBID: 10}}, Item{Type: "tv", Title: "Drama", Year: "2005", TMDBID: 20}, "", false},
		{"different providers share numeric id", []Item{{ID: "a", Type: "tv", Title: "A", MetadataProvider: "tvmaze", MetadataID: "10"}}, Item{Type: "tv", Title: "B", TMDBID: 10}, "", false},
		{"known remakes", []Item{{ID: "a", Type: "tv", Title: "Drama", Year: "2005"}}, Item{Type: "tv", Title: "Drama", Year: "2015"}, "", false},
		{"ambiguous remake missing year", []Item{{ID: "a", Type: "tv", Title: "Drama", Year: "2005"}, {ID: "b", Type: "tv", Title: "Drama", Year: "2015"}}, Item{Type: "tv", Title: "Drama"}, "", true},
		{"undated item cannot bridge remakes", []Item{{ID: "a", Type: "tv", Title: "Drama"}, {ID: "b", Type: "tv", Title: "Drama", Year: "2015"}}, Item{Type: "tv", Title: "Drama", Year: "2005"}, "", false},
		{"ambiguous provider identities", []Item{{ID: "a", Type: "tv", Title: "Drama", TMDBID: 10}, {ID: "b", Type: "tv", Title: "Drama", TMDBID: 20}}, Item{Type: "tv", Title: "Drama"}, "", true},
		{"movie must remain separate", []Item{{ID: "a", Type: "movie", Title: "Drama"}}, Item{Type: "tv", Title: "Drama"}, "", false},
		{"original title alias", []Item{{ID: "a", Type: "tv", Title: "犯罪心理", OriginalTitle: "Criminal Minds", Year: "2005"}}, Item{Type: "tv", Title: "Criminal.Minds.S02"}, "a", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, found, ambiguous := findSeries(tc.items, tc.probe)
			if got.ID != tc.want || found != (tc.want != "") || ambiguous != tc.ambiguous {
				t.Fatalf("got %s, found=%v ambiguous=%v", got.ID, found, ambiguous)
			}
		})
	}
}

func TestSeriesImportNewSeasonsAndCanonicalDestination(t *testing.T) {
	a := testApp(t)
	root := t.TempDir()
	for _, season := range []string{"Season 01", "第二季", "S03"} {
		if err := os.MkdirAll(filepath.Join(root, season), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, season, "E01.mp4"), []byte("local episode"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a.s.Sources = []Source{{ID: "local", Type: "local", Root: root}}
	first, err := a.importMedia(context.Background(), ImportRequest{SourceID: "local", Type: "tv", Title: "犯罪心理 第一季", Season: 1, Files: []Selection{{Key: "/Season 01/E01.mp4"}}})
	if err != nil {
		t.Fatal(err)
	}
	id := first["itemId"].(string)
	a.s.Items[0].Year = "2005"
	a.s.Items[0].MetadataProvider = "wikipedia"
	a.s.Items[0].MetadataID = "zh:323405"
	a.s.Items[0].MetadataStatus = "ready"
	second := ImportRequest{SourceID: "local", Type: "tv", Title: "犯罪心理 第二季", Season: 1, Files: []Selection{{Key: "/第二季/E01.mp4"}}}
	p, err := a.preview(context.Background(), second)
	if err != nil || p.ItemID != id || p.Rows[0].Season != 2 || p.Count != 1 {
		t.Fatalf("bad season preview: %+v %v", p, err)
	}
	if _, err = a.importMedia(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if len(a.s.Items) != 1 || len(a.s.Files) != 2 || len(a.s.Tasks) != 0 || a.s.Files[1].Season != 2 {
		t.Fatal("new season split the series or started a copy")
	}
	third := ImportRequest{Mode: "copy", SourceID: "local", Type: "tv", Title: "犯罪心理", Season: 1, OutputRoot: t.TempDir(), Files: []Selection{{Key: "/S03/E01.mp4"}}}
	p, err = a.preview(context.Background(), third)
	want := filepath.Join(third.OutputRoot, "犯罪心理 (2005)", "Season 03", "犯罪心理 (2005) - S03E01.mp4")
	if err != nil || p.Rows[0].Destination != want || p.ItemID != id {
		t.Fatalf("series folder changed between seasons: %+v %v", p, err)
	}
	duplicate := third
	duplicate.Mode = "reference"
	s := 2
	duplicate.Files = []Selection{{Key: "/S03/E01.mp4", Season: &s}}
	p, err = a.preview(context.Background(), duplicate)
	if err != nil || p.Rows[0].Issue == "" {
		t.Fatal("existing season/episode not detected")
	}
}

func TestSeriesMigrationPreservesLibraryAndBackup(t *testing.T) {
	a := testApp(t)
	a.s.Version = 2
	a.s.Sources = []Source{{ID: "disk", Type: "local", Root: t.TempDir()}}
	a.s.Items = []Item{
		{ID: "first", Type: "tv", Title: "犯罪心理", Year: "2005", MetadataProvider: "wikipedia", MetadataID: "zh:323405", MetadataStatus: "ready", Episodes: []Episode{{Season: 1, Number: 1, Name: "S1 episode"}}},
		{ID: "second", Type: "tv", Title: "犯罪心理 第二季", MetadataProvider: "local", MetadataStatus: "ready", Episodes: []Episode{{Season: 2, Number: 1, Name: "S2 episode", Overview: "local NFO"}}},
	}
	a.s.Files = []MediaFile{{ID: "file1", ItemID: "first", Season: 1, Episode: 1, SourceID: "disk", SourceKey: "/Season 01/a.mkv", Destination: "original1.mkv", Reference: true, Status: "completed", Position: 121, Duration: 2400, Subtitles: []Subtitle{}}, {ID: "file2", ItemID: "second", Season: 2, Episode: 1, SourceID: "disk", SourceKey: "/Season 02/b.mkv", Destination: "original2.mkv", Reference: true, Status: "completed", Position: 567, Duration: 2500, Subtitles: []Subtitle{{Name: "Chinese", Path: "subtitle.vtt"}}}}
	a.s.SubtitlePaths["file2"] = []SavedSubtitle{{Name: "Chinese", Path: "subtitle.vtt"}}
	a.s.Tasks = []Task{{ID: "task2", FileID: "file2", Status: "paused", Bytes: 123}}
	if err := a.saveLocked(); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(filepath.Join(a.dir, "state.json"))
	wantFiles := append([]MediaFile{}, a.s.Files...)
	wantFiles[1].ItemID = "first"
	wantTasks := append([]Task{}, a.s.Tasks...)
	reloaded, err := newApp(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.s.Items) != 1 || reloaded.s.Items[0].ID != "first" || reloaded.s.Items[0].Title != "犯罪心理" || len(reloaded.s.Items[0].Episodes) != 2 {
		t.Fatalf("series not reconciled: %+v", reloaded.s.Items)
	}
	if !reflect.DeepEqual(wantFiles, reloaded.s.Files) || !reflect.DeepEqual(wantTasks, reloaded.s.Tasks) {
		t.Fatal("merge changed file, progress, subtitle or task data")
	}
	oldID, err := reloaded.item("second")
	if err != nil || oldID.ID != "first" {
		t.Fatal("old item ID no longer resolves")
	}
	backup, _ := os.ReadFile(filepath.Join(a.dir, "state-before-series-v3.json"))
	if !bytes.Equal(original, backup) {
		t.Fatal("original snapshot not backed up exactly")
	}
	again, err := newApp(a.dir)
	if err != nil || len(again.s.Items) != 1 || again.s.Version != 7 {
		t.Fatal("regrouping is not persistent/idempotent", err)
	}
	backupAgain, _ := os.ReadFile(filepath.Join(a.dir, "state-before-series-v3.json"))
	if !bytes.Equal(backup, backupAgain) {
		t.Fatal("migration overwrote original backup")
	}
}

func TestSeriesAmbiguityManualChoiceAndSeparateSeries(t *testing.T) {
	a := testApp(t)
	a.s.Items = []Item{{ID: "old", Type: "tv", Title: "同名剧", Year: "2005"}, {ID: "remake", Type: "tv", Title: "同名剧", Year: "2015"}, {ID: "unknown", Type: "tv", Title: "同名剧"}}
	if err := a.regroupSeriesLocked(); err != nil {
		t.Fatal(err)
	}
	if len(a.s.Items) != 3 {
		t.Fatal("an undated item bridged different remakes")
	}
	it, err := importItem(a.s.Items, ImportRequest{Type: "tv", Title: "同名剧", ItemID: "remake"})
	if err != nil || it.ID != "remake" {
		t.Fatal("explicit series selection ignored", err)
	}
	_, err = importItem(a.s.Items, ImportRequest{Type: "tv", Title: "同名剧"})
	if err == nil {
		t.Fatal("ambiguous import silently chose a remake")
	}
	a.s.Items = []Item{{ID: "normal", Type: "tv", Title: "Drama"}, {ID: "separate", Type: "tv", Title: "Drama", SeparateSeries: true}}
	if err = a.regroupSeriesLocked(); err != nil || len(a.s.Items) != 2 {
		t.Fatal("explicit standalone series merged", err)
	}
	if err = a.saveLocked(); err != nil {
		t.Fatal(err)
	}
	loaded, err := newApp(a.dir)
	if err != nil || len(loaded.s.Items) != 2 {
		t.Fatal("standalone setting lost on restart", err)
	}
}

func TestSeriesConcurrentImportsAndEpisodeCollision(t *testing.T) {
	for _, sameEpisode := range []bool{false, true} {
		t.Run(fmt.Sprintf("same episode=%v", sameEpisode), func(t *testing.T) {
			a := testApp(t)
			root := t.TempDir()
			a.s.Sources = []Source{{ID: "local", Type: "local", Root: root}}
			for i := 1; i <= 2; i++ {
				if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("source%d.mp4", i)), []byte("episode"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			start := make(chan struct{})
			errs := make(chan error, 2)
			var wg sync.WaitGroup
			for i := 1; i <= 2; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					s := i
					if sameEpisode {
						s = 1
					}
					_, err := a.importMedia(context.Background(), ImportRequest{SourceID: "local", Type: "tv", Title: "Drama", Season: s, Files: []Selection{{Key: fmt.Sprintf("/source%d.mp4", i), Episode: 1}}})
					errs <- err
				}(i)
			}
			close(start)
			wg.Wait()
			close(errs)
			success := 0
			for err := range errs {
				if err == nil {
					success++
				}
			}
			want := 2
			if sameEpisode {
				want = 1
			}
			if success != want || len(a.s.Items) != 1 || len(a.s.Files) != want {
				t.Fatalf("concurrent import: success=%d items=%d files=%d", success, len(a.s.Items), len(a.s.Files))
			}
		})
	}
}

func TestSeriesMetadataRefreshRegroupsAndRetainsLocalEpisodes(t *testing.T) {
	a := testApp(t)
	a.s.Items = []Item{{ID: "first", Type: "tv", Title: "Drama", Year: "2005", MetadataProvider: "tvmaze", MetadataID: "50", MetadataStatus: "ready", Episodes: []Episode{{Season: 1, Number: 1, Name: "local episode", Overview: "local synopsis"}}}, {ID: "second", Type: "tv", Title: "Second season", Episodes: []Episode{{Season: 2, Number: 1, Name: "season two local episode"}}}}
	a.s.Files = []MediaFile{{ID: "f1", ItemID: "first", Season: 1, Episode: 1, Position: 123}, {ID: "f2", ItemID: "second", Season: 2, Episode: 1, Position: 321}}
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return fakeJSON(`{"id":50,"name":"Drama","premiered":"2005-01-01","_embedded":{"episodes":[],"cast":[]}}`), nil
	})}
	a.scrapePublic("second", "tvmaze", "50", "")
	if len(a.s.Items) != 1 || len(a.s.Items[0].Episodes) != 2 || a.s.Files[0].ItemID != a.s.Files[1].ItemID || a.s.Files[1].Position != 321 {
		t.Fatal("refresh split seasons or lost local episodes/progress")
	}
}

func TestSeriesBackupFailureLeavesCatalogueIntact(t *testing.T) {
	a := testApp(t)
	a.s.Items = []Item{{ID: "first", Type: "tv", Title: "Drama", Year: "2005"}, {ID: "second", Type: "tv", Title: "Drama"}}
	a.s.Files = []MediaFile{{ID: "f2", ItemID: "second", Season: 2, Episode: 1}}
	a.metadataJobs["second"] = "active-job"
	if err := os.Mkdir(filepath.Join(a.dir, "state-before-series-v3.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.regroupSeriesLocked(); err == nil {
		t.Fatal("merge proceeded without a usable backup")
	}
	if len(a.s.Items) != 2 || a.s.Files[0].ItemID != "second" || a.metadataJobs["second"] != "active-job" {
		t.Fatal("failed merge changed current catalogue")
	}
}
