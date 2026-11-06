package yingku

import (
	"net/http/httptest"
	"testing"
)

func TestSkipSegmentsArePerVideoAndPersist(t *testing.T) {
	a := testApp(t)
	a.s.Files = []MediaFile{
		{ID: "episode-1", ItemID: "series", Status: "completed", Position: 42, Duration: 120, Episode: 1},
		{ID: "episode-2", ItemID: "series", Status: "completed", Duration: 120, Episode: 2},
	}
	server := httptest.NewServer(a.handler())
	defer server.Close()
	const base = "/api/files/episode-1/skip-segments/"
	post := func(kind string, start, end float64, want int) {
		t.Helper()
		request(t, server, a, "POST", base+kind, map[string]float64{"start": start, "end": end, "duration": 120}, want)
	}

	post("intro", 0, 15.5, 200)
	post("outro", 10, 119, 400) // overlaps the intro
	post("outro", 90, 121, 400)
	post("intro", 12, 12, 400)
	post("credits", 100, 120, 400)
	request(t, server, a, "POST", base+"intro", map[string]float64{"end": 10, "duration": 120}, 400)
	post("outro", 90, 120, 200)

	loaded, err := newApp(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	first, err := loaded.file("episode-1")
	if err != nil || first.Intro == nil || *first.Intro != (SkipSegment{Start: 0, End: 15.5}) ||
		first.Outro == nil || *first.Outro != (SkipSegment{Start: 90, End: 120}) || first.Position != 42 {
		t.Fatalf("expected both marks and the existing position after reload: %+v (%v)", first, err)
	}
	second, _ := loaded.file("episode-2")
	if second.Intro != nil || second.Outro != nil {
		t.Fatal("marks should not carry over to other episodes")
	}
	request(t, server, a, "DELETE", base+"intro", nil, 200)
	first, _ = a.file("episode-1")
	if first.Intro != nil || first.Outro == nil || first.Outro.End != 120 {
		t.Fatal("clearing the intro must preserve the outro")
	}
}

func TestSeasonSkipDefaultsAndEpisodeExceptions(t *testing.T) {
	a := testApp(t)
	a.s.Items = []Item{{ID: "series", Type: "tv"}, {ID: "movie", Type: "movie"}}
	a.s.Files = []MediaFile{
		{ID: "episode-1", ItemID: "series", Status: "completed", Season: 1, Episode: 1},
		{ID: "episode-2", ItemID: "series", Status: "completed", Season: 1, Episode: 2},
		{ID: "next-season", ItemID: "series", Status: "completed", Season: 2, Episode: 1},
		{ID: "special", ItemID: "series", Status: "completed", Episode: 1},
		{ID: "combined", ItemID: "series", Status: "completed", Season: 1, Episode: 3, EpisodeEnd: 4},
		{ID: "movie-file", ItemID: "movie", Status: "completed", Season: 1, Episode: 1},
	}
	server := httptest.NewServer(a.handler())
	defer server.Close()
	const route = "/api/files/episode-1/skip-segments/"
	intro := map[string]float64{"start": 0, "end": 12, "duration": 120}
	request(t, server, a, "POST", route+"intro?scope=season", intro, 200)
	request(t, server, a, "POST", route+"outro?scope=season", map[string]float64{"start": 10, "end": 110, "duration": 120}, 400)
	request(t, server, a, "POST", route+"outro?scope=season", map[string]float64{"start": 90, "end": 118, "duration": 120}, 200)
	for _, id := range []string{"movie-file", "special", "combined"} {
		request(t, server, a, "POST", "/api/files/"+id+"/skip-segments/intro?scope=season", intro, 400)
	}
	request(t, server, a, "POST", route+"intro", map[string]float64{"start": 2, "end": 8, "duration": 120}, 200)
	const second = "/api/files/episode-2/skip-segments/intro"
	request(t, server, a, "POST", second, map[string]bool{"disabled": true}, 200)

	loaded, err := newApp(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	series, _ := loaded.item("series")
	marks := series.SkipSeasons[1]
	if len(series.SkipSeasons) != 1 || marks.Intro == nil || *marks.Intro != (SkipSegment{Start: 0, End: 12}) ||
		marks.OutroFromEnd == nil || *marks.OutroFromEnd != (SkipSegment{Start: 2, End: 30}) {
		t.Fatalf("expected one set of season defaults with relative credits: %+v", series.SkipSeasons)
	}
	firstFile, _ := loaded.file("episode-1")
	secondFile, _ := loaded.file("episode-2")
	if firstFile.Intro == nil || *firstFile.Intro != (SkipSegment{Start: 2, End: 8}) ||
		secondFile.Intro != nil || !secondFile.IntroSkipDisabled || secondFile.Outro != nil {
		t.Fatalf("expected separate per-episode exceptions: %+v / %+v", firstFile, secondFile)
	}
	request(t, server, a, "DELETE", second, nil, 200)
	request(t, server, a, "DELETE", route+"intro?scope=season", nil, 200)
	series, _ = a.item("series")
	firstFile, _ = a.file("episode-1")
	secondFile, _ = a.file("episode-2")
	if series.SkipSeasons[1].Intro != nil || series.SkipSeasons[1].OutroFromEnd == nil ||
		firstFile.Intro == nil || secondFile.IntroSkipDisabled {
		t.Fatal("clearing one scope must preserve the other scope and the other kind")
	}
}
