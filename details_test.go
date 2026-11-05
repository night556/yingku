package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func richPublicClient(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" || r.URL.Query().Get("api_key") != "" {
			t.Error("public enrichment used credentials")
		}
		switch r.URL.Host {
		case "zh.wikipedia.org":
			return fakeJSON(`{"query":{"pages":[{"pageid":42,"title":"测试剧","extract":"《测试剧》是一部电视剧，故事发生在1980年。","pageprops":{"wikibase_item":"Q42"}}]}}`), nil
		case "www.wikidata.org":
			if r.URL.Query().Get("props") == "labels" {
				return fakeJSON(`{"entities":{"Q1":{"labels":{"zh-hans":{"value":"演员甲"},"en":{"value":"Actor One"}}},"Q2":{"labels":{"zh":{"value":"线上导演"},"en":{"value":"Director One"}}},"Q3":{"labels":{"zh":{"value":"美国"}}},"Q4":{"labels":{"zh":{"value":"英语"}}},"Q5":{"labels":{"zh":{"value":"制作公司"}}}}}`), nil
			}
			return fakeJSON(`{"entities":{"Q42":{"claims":{"P580":[{"mainsnak":{"datavalue":{"value":{"time":"+2005-09-22T00:00:00Z"}}}}],"P345":[{"mainsnak":{"datavalue":{"value":"tt0452046"}}}],"P8600":[{"mainsnak":{"datavalue":{"value":"60"}}}],"P161":[{"mainsnak":{"datavalue":{"value":{"id":"Q1"}}}}],"P57":[{"mainsnak":{"datavalue":{"value":{"id":"Q2"}}}}],"P495":[{"mainsnak":{"datavalue":{"value":{"id":"Q3"}}}}],"P364":[{"mainsnak":{"datavalue":{"value":{"id":"Q4"}}}}],"P272":[{"mainsnak":{"datavalue":{"value":{"id":"Q5"}}}}]}}}}`), nil
		case "api.tvmaze.com":
			switch r.URL.Path {
			case "/search/shows":
				return fakeJSON(`[]`), nil
			case "/shows/60":
				return fakeJSON(`{"id":60,"name":"Test Show","premiered":"2005-09-22","status":"Running","language":"English","runtime":45,"officialSite":"https://example.test/show","externals":{"imdb":"tt0452046","thetvdb":75710},"network":{"name":"CBS","country":{"name":"United States"}},"rating":{"average":8.1},"_embedded":{"cast":[{"person":{"name":"Actor One","image":{"medium":"https://static.tvmaze.com/uploads/images/medium_portrait/1/1.jpg"}},"character":{"name":"Detective"}}],"episodes":[{"season":1,"number":1,"name":"Pilot","airdate":"2005-09-22","runtime":42,"summary":"<p>Opening episode.</p>","rating":{"average":8.3}},{"season":2,"number":1,"name":"Return","airdate":"2006-09-20","runtime":45,"summary":"Second season."}]}}`), nil
			case "/shows/60/seasons":
				return fakeJSON(`[{"number":1,"premiereDate":"2005-09-22","endDate":"2006-05-10","episodeOrder":22},{"number":2,"premiereDate":"2006-09-20","endDate":"2007-05-16","episodeOrder":23,"image":{"medium":"https://static.tvmaze.com/uploads/images/medium_portrait/1/2.jpg"}}]`), nil
			case "/shows/60/crew":
				return fakeJSON(`[{"type":"Creator","person":{"name":"Creator One"}},{"type":"Writer","person":{"name":"Writer One"}}]`), nil
			case "/shows/60/images":
				return fakeJSON(`[{"type":"background","resolutions":{"original":{"url":"https://static.tvmaze.com/uploads/images/original_untouched/1/3.jpg"}}}]`), nil
			default:
				t.Errorf("unexpected cross-source lookup: %s", r.URL.Path)
			}
		}
		return nil, fmt.Errorf("unexpected metadata request to %s", r.URL.Host)
	})}
}

func TestWikiTVEnrichmentUsesStableIDsAndStructuredDates(t *testing.T) {
	a := testApp(t)
	a.client = richPublicClient(t)
	v, err := a.fetchWiki(context.Background(), "zh:42")
	if err != nil {
		t.Fatal(err)
	}
	if v.Year != "2005" || v.Details.PremiereDate != "2005-09-22" {
		t.Fatalf("year taken from plot instead of premiere: %+v", v)
	}
	if v.Title != "测试剧" || v.OriginalTitle != "Test Show" || len(v.Details.Seasons) != 2 || v.Details.Seasons[1].Year != "2006" {
		t.Fatalf("Chinese title or season dates lost: %+v", v.Details)
	}
	if len(v.Episodes) != 2 || v.Episodes[0].Runtime != 42 || v.Episodes[0].Rating != 8.3 || v.Episodes[0].Overview != "Opening episode." {
		t.Fatal("episode details not supplemented")
	}
	if v.Details.ExternalIDs["tvmaze"] != "60" || v.Details.ExternalIDs["imdb"] != "tt0452046" || len(v.Details.Sources) != 3 || v.Rating != 8.1 || v.Details.RatingSource != "TVmaze" {
		t.Fatalf("identity, sources or rating not retained: %+v", v.Details)
	}
	found := false
	for _, p := range v.Details.People {
		if p.Name == "演员甲" && p.OriginalName == "Actor One" && p.Role == "Detective" && p.Image != "" {
			found = true
		}
	}
	if !found || v.Backdrop == "" || len(v.Details.Directors) == 0 || len(v.Details.Writers) == 0 || v.Details.Runtime != 45 {
		t.Fatal("people roles/photos or production details not enriched")
	}
}

func TestLocalNFOGetsMissingYearWithoutLosingItsFields(t *testing.T) {
	a := testApp(t)
	a.client = richPublicClient(t)
	a.s.Config.AutoMetadata = true
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "测试剧", "Season 02"), 0700); err != nil {
		t.Fatal(err)
	}
	nfo := []byte(`<tvshow><title>测试剧</title><plot>本地自定义简介</plot><director>本地导演</director><mpaa>本地分级</mpaa><actor><name>演员甲</name><role>本地角色名</role></actor></tvshow>`)
	if err := os.WriteFile(filepath.Join(root, "测试剧", "tvshow.nfo"), nfo, 0600); err != nil {
		t.Fatal(err)
	}
	video := []byte("original local episode")
	file := filepath.Join(root, "测试剧", "Season 02", "E01.mp4")
	if err := os.WriteFile(file, video, 0600); err != nil {
		t.Fatal(err)
	}
	a.s.Sources = []Source{{ID: "local", Type: "local", Root: root}}
	r, err := a.importMedia(context.Background(), ImportRequest{SourceID: "local", Type: "tv", Title: "测试剧", Season: 2, Files: []Selection{{Key: "/测试剧/Season 02/E01.mp4"}}})
	if err != nil {
		t.Fatal(err)
	}
	id := r["itemId"].(string)
	waitFor(t, 3*time.Second, func() bool { it, _ := a.item(id); return it.Details.Updated != "" })
	it, _ := a.item(id)
	if it.Title != "测试剧" || it.Year != "2005" || it.Overview != "本地自定义简介" || it.Details.Directors[0] != "本地导演" || it.Details.ContentRating != "本地分级" || it.Details.Runtime != 45 {
		t.Fatalf("missing year not filled or local fields overwritten: %+v", it)
	}
	if len(it.Details.People) == 0 || it.Details.People[0].Role != "本地角色名" || it.Details.People[0].Image == "" {
		t.Fatal("local role lost instead of supplementing portrait")
	}
	a.mu.Lock()
	tasks := len(a.s.Tasks)
	a.mu.Unlock()
	if tasks != 0 {
		t.Fatal("metadata supplement downloaded video")
	}
	got, _ := os.ReadFile(file)
	gotNFO, _ := os.ReadFile(filepath.Join(root, "测试剧", "tvshow.nfo"))
	if !bytes.Equal(got, video) || !bytes.Equal(gotNFO, nfo) {
		t.Fatal("enrichment wrote to source")
	}
}

func TestSparseMetadataDoesNotEraseExistingRichDetails(t *testing.T) {
	it := Item{Title: "Local title", Year: "2005", Overview: "Local plot", Rating: 8.2, Genres: []string{"Crime"}, LocalFields: []string{"title", "overview", "rating"}, Details: MetadataDetails{Runtime: 45, RatingSource: "本地 NFO", Directors: []string{"Director"}, People: []Person{{Name: "Actor", Role: "Detective", Type: "Actor"}}, Seasons: []Season{{Number: 2, Year: "2006"}}}, Episodes: []Episode{{Season: 2, Number: 1, Overview: "Local episode plot"}}}
	applyRemoteMetadata(&it, metadataValues{Title: "English title", Overview: "Remote plot", Details: MetadataDetails{People: []Person{{Name: "Actor", Type: "Actor", Image: "https://static.tvmaze.com/uploads/images/1.jpg"}}}})
	if it.Title != "Local title" || it.Overview != "Local plot" || it.Year != "2005" || it.Rating != 8.2 || it.Details.RatingSource != "本地 NFO" || it.Details.Runtime != 45 || it.Details.Seasons[0].Year != "2006" || it.Details.People[0].Role != "Detective" || it.Details.People[0].Image == "" || len(it.Episodes) != 1 {
		t.Fatal("sparse refresh erased details or local priority")
	}
	var d MetadataDetails
	addExternalID(&d, "imdb", "javascript:alert(1)")
	if len(d.ExternalIDs) != 0 || safeWebURL("javascript:alert(1)") != "" {
		t.Fatal("unsafe external link accepted")
	}
}

func TestTMDBExtendedDetailsExportAsNFO(t *testing.T) {
	a := testApp(t)
	var response tmdbDetails
	if err := json.Unmarshal([]byte(`{"id":10,"title":"Movie","original_title":"Original Movie","release_date":"2020-02-03","runtime":104,"tagline":"A story","status":"Released","vote_average":7.5,"production_countries":[{"name":"United States"}],"spoken_languages":[{"english_name":"English"}],"production_companies":[{"name":"Studio"}],"credits":{"cast":[{"name":"Actor","character":"Detective","profile_path":"/actor.jpg"}],"crew":[{"name":"Director","job":"Director"},{"name":"Writer","job":"Screenplay","department":"Writing"}]},"external_ids":{"imdb_id":"tt1234567"},"release_dates":{"results":[{"iso_3166_1":"US","release_dates":[{"certification":"PG-13"}]}]}}`), &response); err != nil {
		t.Fatal(err)
	}
	it := Item{ID: "movie", Type: "movie"}
	applyRemoteMetadata(&it, response.values("movie", 10, nil))
	a.s.Items = []Item{it}
	root := t.TempDir()
	a.s.Files = []MediaFile{{ID: "file", ItemID: "movie", Destination: filepath.Join(root, "Movie.mp4"), Status: "completed"}}
	a.writeNFO("movie")
	n := readNFO(Source{Type: "local", Root: root}, "/movie.nfo")
	if n == nil || n.Year != "2020" || n.Runtime != "104" || n.Premiered != "2020-02-03" || n.MPAA != "US · PG-13" || len(n.Directors) != 1 || len(n.Writers) != 1 || len(n.Actors) != 1 || n.Actors[0].Role != "Detective" || len(n.Countries) != 1 {
		t.Fatalf("extended fields not exported: %+v", n)
	}
	if it.Details.RatingSource != "TMDb" || it.Details.ExternalIDs["imdb"] != "tt1234567" || !strings.HasSuffix(it.Details.People[0].Image, "actor.jpg") {
		t.Fatal("TMDb metadata incomplete")
	}
}

func TestDetailsSnapshotsDoNotAliasMutableFields(t *testing.T) {
	a := testApp(t)
	a.s.Items = []Item{{ID: "x", Details: MetadataDetails{ExternalIDs: map[string]string{"imdb": "tt1234567"}, People: []Person{{Name: "Original"}}, Sources: []MetadataSource{{Name: "Source"}}}, LocalFields: []string{"title"}}}
	it, _ := a.item("x")
	it.Details.ExternalIDs["imdb"] = "changed"
	it.Details.People[0].Name = "changed"
	it.LocalFields[0] = "changed"
	public := a.publicState().(map[string]any)["items"].([]Item)
	public[0].Details.Sources[0].Name = "changed"
	if a.s.Items[0].Details.ExternalIDs["imdb"] != "tt1234567" || a.s.Items[0].Details.People[0].Name != "Original" || a.s.Items[0].LocalFields[0] != "title" || a.s.Items[0].Details.Sources[0].Name != "Source" {
		t.Fatal("public or worker snapshot mutated library")
	}
}

func TestRichMetadataLive(t *testing.T) {
	if os.Getenv("YINGKU_LIVE_METADATA") != "1" {
		t.Skip("live metadata opt-in")
	}
	a := testApp(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	v, err := a.fetchWiki(ctx, "zh:323405")
	if err != nil {
		t.Fatal(err)
	}
	var seasonTwo Season
	for _, s := range v.Details.Seasons {
		if s.Number == 2 {
			seasonTwo = s
		}
	}
	portraits := 0
	for _, p := range v.Details.People {
		if p.Type == "Actor" && p.Image != "" && p.Role != "" {
			portraits++
		}
	}
	if v.Year != "2005" || seasonTwo.Year != "2006" || v.Details.Runtime == 0 || len(v.Episodes) < 40 || portraits == 0 {
		t.Fatalf("rich series metadata incomplete: year=%s season2=%+v runtime=%d episodes=%d portraits=%d", v.Year, seasonTwo, v.Details.Runtime, len(v.Episodes), portraits)
	}
	t.Logf("Criminal Minds: premiere %s, S2 %s, %d-min runtime, %d seasons, %d episodes, %d portraits with roles", v.Details.PremiereDate, seasonTwo.PremiereDate, v.Details.Runtime, v.Details.TotalSeasons, len(v.Episodes), portraits)
}

func TestCastAliasesAndAuthoritativeCastList(t *testing.T) {
	merged := mergePeople([]Person{{Name: "库克", OriginalName: "A. J. Cook", Type: "Actor"}}, []Person{{Name: "A.J. Cook", Type: "Actor", Role: "JJ", Image: "portrait"}})
	if len(merged) != 1 || merged[0].Role != "JJ" || merged[0].Image != "portrait" {
		t.Fatal("punctuation created duplicate cast members")
	}
	it := Item{Details: MetadataDetails{People: []Person{{Name: "Old guest actor", Type: "Actor"}, {Name: "库克", OriginalName: "A. J. Cook", Type: "Actor"}}}}
	applyRemoteMetadata(&it, metadataValues{CompleteCast: true, Details: MetadataDetails{People: []Person{{Name: "库克", OriginalName: "A. J. Cook", Type: "Actor", Role: "JJ"}}}})
	if len(it.Details.People) != 1 {
		t.Fatal("stale guest cast kept as main cast")
	}
	if _, err := publicArtURL("https://thumb.wikimedia.org/wikipedia/commons/thumb/a/ab/Poster.jpg/500px-Poster.jpg?tracking=1"); err != nil {
		t.Fatal("current Wikimedia thumbnail CDN rejected", err)
	}
	if _, err := publicArtURL("https://thumb.wikimedia.org.evil.test/wikipedia/a.jpg"); err == nil {
		t.Fatal("lookalike image host accepted")
	}
}
