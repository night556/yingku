package yingku

import (
	"path"
	"strings"
	"testing"
)

func TestSpecialEpisodeLabelsAndExplicitRegularEpisodes(t *testing.T) {
	for _, tc := range []struct {
		name            string
		season, episode int
	}{
		{"Show.S00E01.mkv", 0, 1}, {"Show.SP02.mkv", 0, 2}, {"Show.Special.03.mkv", 0, 3},
		{"测试剧.特别篇01.mp4", 0, 1}, {"测试剧.番外第2集.mp4", 0, 2}, {"测试剧.彩蛋03.mp4", 0, 3},
		{"测试剧.花絮4k.mp4", 0, 0}, {"彩蛋.mp4", 0, 0}, {"特别季.mp4", 0, 0},
		{"Show.S01E02.Specials.mkv", 1, 2}, {"Special.Agent.S02E03.mkv", 2, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			season, episode := guessEpisode(tc.name)
			explicit, ok := seasonFromName(tc.name)
			if season != tc.season || episode != tc.episode || !ok || explicit != tc.season {
				t.Fatalf("unexpected season/episode %d/%d; season label=%d/%v", season, episode, explicit, ok)
			}
		})
	}
	for _, name := range []string{"Special.Agent.2024.mp4", "特别行动.mp4", "彩蛋猎人.mp4", "番外调查.mp4", "Extraordinary.2024.mp4", "Suspense.2024.mp4"} {
		if specialContentName(name) {
			t.Fatalf("ordinary title identified as extras: %s", name)
		}
	}
	title := guessTitle("Special.Agent.2024.1080p.mkv")["title"]
	if title != "Special Agent" {
		t.Fatal("ordinary title was truncated", title)
	}
}

func TestSpecialSeasonsStayInSameShowAndUnnumberedExtrasNeedReview(t *testing.T) {
	files := []ScannedFile{}
	for i, key := range []string{"/测试剧/Season 01/测试剧.S01E01.mkv", "/测试剧/Specials/01.mkv", "/测试剧/特别季/02.mkv", "/测试剧/测试剧.SP03.mkv", "/测试剧/彩蛋.mp4", "/测试剧/花絮.mp4"} {
		files = append(files, ScannedFile{ID: i + 1, Key: key, Relative: strings.TrimPrefix(key, "/"), Entry: Entry{Name: path.Base(key)}})
	}
	plan := localScanPlan("所选目录", files)
	if len(plan.Groups) != 1 || plan.Groups[0].Type != "tv" || plan.Groups[0].Title != "测试剧" || len(plan.Groups[0].Files) != 4 || len(plan.Unassigned) != 2 {
		t.Fatalf("wrong grouping: %+v", plan)
	}
	for _, f := range plan.Groups[0].Files {
		want := 0
		if f.ID == 1 {
			want = 1
		}
		if f.Season != want {
			t.Fatalf("extra mixed into regular season: %+v", f)
		}
	}
	for _, pending := range plan.Unassigned {
		if !strings.Contains(pending.Reason, "季数 0") {
			t.Fatal("missing actionable special-season hint", pending)
		}
	}
	hint := scanEpisodeHint(files[1])
	if hint.Season == nil || *hint.Season != 0 || hint.Episode != 1 {
		t.Fatal("special folder hint missing", hint)
	}
	hint = scanEpisodeHint(files[4])
	if hint.Season == nil || *hint.Season != 0 || hint.Episode != 0 {
		t.Fatal("unnumbered extra must not invent an episode", hint)
	}
}
