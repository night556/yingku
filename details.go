package main

import (
	"context"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Person struct {
	Name         string `json:"name"`
	OriginalName string `json:"originalName,omitempty"`
	Role         string `json:"role,omitempty"`
	Type         string `json:"type"`
	Image        string `json:"image,omitempty"`
}
type Season struct {
	Number       int    `json:"number"`
	Year         string `json:"year,omitempty"`
	Name         string `json:"name,omitempty"`
	PremiereDate string `json:"premiereDate,omitempty"`
	EndDate      string `json:"endDate,omitempty"`
	EpisodeCount int    `json:"episodeCount,omitempty"`
	Overview     string `json:"overview,omitempty"`
	Poster       string `json:"poster,omitempty"`
}
type MetadataSource struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}
type MetadataDetails struct {
	Tagline       string            `json:"tagline,omitempty"`
	PremiereDate  string            `json:"premiereDate,omitempty"`
	EndDate       string            `json:"endDate,omitempty"`
	Status        string            `json:"status,omitempty"`
	ContentRating string            `json:"contentRating,omitempty"`
	Runtime       int               `json:"runtime,omitempty"`
	RatingSource  string            `json:"ratingSource,omitempty"`
	Countries     []string          `json:"countries,omitempty"`
	Languages     []string          `json:"languages,omitempty"`
	Studios       []string          `json:"studios,omitempty"`
	Directors     []string          `json:"directors,omitempty"`
	Writers       []string          `json:"writers,omitempty"`
	Creators      []string          `json:"creators,omitempty"`
	Tags          []string          `json:"tags,omitempty"`
	Homepage      string            `json:"homepage,omitempty"`
	ExternalIDs   map[string]string `json:"externalIds,omitempty"`
	People        []Person          `json:"people,omitempty"`
	Seasons       []Season          `json:"seasons,omitempty"`
	TotalSeasons  int               `json:"totalSeasons,omitempty"`
	TotalEpisodes int               `json:"totalEpisodes,omitempty"`
	Sources       []MetadataSource  `json:"sources,omitempty"`
	Updated       string            `json:"updated,omitempty"`
}

func cloneDetails(d MetadataDetails) MetadataDetails {
	d.Countries = append([]string{}, d.Countries...)
	d.Languages = append([]string{}, d.Languages...)
	d.Studios = append([]string{}, d.Studios...)
	d.Directors = append([]string{}, d.Directors...)
	d.Writers = append([]string{}, d.Writers...)
	d.Creators = append([]string{}, d.Creators...)
	d.Tags = append([]string{}, d.Tags...)
	d.People = append([]Person{}, d.People...)
	d.Seasons = append([]Season{}, d.Seasons...)
	d.Sources = append([]MetadataSource{}, d.Sources...)
	ids := map[string]string{}
	for k, v := range d.ExternalIDs {
		ids[k] = v
	}
	d.ExternalIDs = ids
	return d
}
func containsField(fields []string, key string) bool {
	for _, f := range fields {
		if f == key {
			return true
		}
	}
	return false
}
func lockLocal(it *Item, key string, hasValue bool) {
	if hasValue && !containsField(it.LocalFields, key) {
		it.LocalFields = append(it.LocalFields, key)
	}
}
func safeWebURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	return u.String()
}

var imdbIDRE = regexp.MustCompile(`^tt[0-9]{5,12}$`)

func addExternalID(d *MetadataDetails, provider, value string) {
	value = strings.TrimSpace(value)
	valid := false
	switch provider {
	case "imdb":
		valid = imdbIDRE.MatchString(value)
	case "wikidata":
		valid = entityIDRE.MatchString(value)
	case "tmdb", "tvdb", "tvmaze":
		n, e := strconv.Atoi(value)
		valid = e == nil && n > 0
		if valid {
			value = strconv.Itoa(n)
		}
	}
	if valid {
		if d.ExternalIDs == nil {
			d.ExternalIDs = map[string]string{}
		}
		d.ExternalIDs[provider] = value
	}
}
func samePerson(a, b Person) bool {
	for _, left := range []string{a.Name, a.OriginalName} {
		for _, right := range []string{b.Name, b.OriginalName} {
			if left != "" && right != "" && comparableTitle(left) == comparableTitle(right) {
				return true
			}
		}
	}
	return false
}
func mergePeople(primary, extra []Person) []Person {
	result := append([]Person{}, primary...)
	for _, p := range extra {
		found := false
		for i := range result {
			v := &result[i]
			if !samePerson(*v, p) || v.Type != p.Type {
				continue
			}
			if v.Image == "" {
				v.Image = p.Image
			}
			if v.Role == "" {
				v.Role = p.Role
			} else if p.Type != "Actor" && p.Role != "" && !strings.Contains(v.Role, p.Role) {
				v.Role += " / " + p.Role
			}
			if v.OriginalName == "" {
				v.OriginalName = p.OriginalName
			}
			found = true
			break
		}
		if !found && p.Name != "" {
			result = append(result, p)
		}
	}
	return result
}
func mergeSeasons(primary, extra []Season) []Season {
	result := append([]Season{}, primary...)
	for _, s := range extra {
		found := false
		for i := range result {
			v := &result[i]
			if v.Number != s.Number {
				continue
			}
			if v.Year == "" {
				v.Year = s.Year
			}
			if v.Name == "" {
				v.Name = s.Name
			}
			if v.PremiereDate == "" {
				v.PremiereDate = s.PremiereDate
			}
			if v.EndDate == "" {
				v.EndDate = s.EndDate
			}
			if v.Overview == "" {
				v.Overview = s.Overview
			}
			if v.Poster == "" {
				v.Poster = s.Poster
			}
			if v.EpisodeCount == 0 {
				v.EpisodeCount = s.EpisodeCount
			}
			found = true
			break
		}
		if !found {
			result = append(result, s)
		}
	}
	for i := range result {
		if result[i].Year == "" {
			result[i].Year = yearOf(result[i].PremiereDate)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Number < result[j].Number })
	return result
}
func mergeDetails(primary, extra MetadataDetails) MetadataDetails {
	d := cloneDetails(primary)
	for _, pair := range [][2]*string{{&d.Tagline, &extra.Tagline}, {&d.PremiereDate, &extra.PremiereDate}, {&d.EndDate, &extra.EndDate}, {&d.Status, &extra.Status}, {&d.ContentRating, &extra.ContentRating}, {&d.RatingSource, &extra.RatingSource}, {&d.Homepage, &extra.Homepage}, {&d.Updated, &extra.Updated}} {
		if *pair[0] == "" {
			*pair[0] = *pair[1]
		}
	}
	for _, pair := range [][2]*[]string{{&d.Countries, &extra.Countries}, {&d.Languages, &extra.Languages}, {&d.Studios, &extra.Studios}, {&d.Directors, &extra.Directors}, {&d.Writers, &extra.Writers}, {&d.Creators, &extra.Creators}, {&d.Tags, &extra.Tags}} {
		if len(*pair[0]) == 0 {
			*pair[0] = append([]string{}, (*pair[1])...)
		}
	}
	if d.Runtime == 0 {
		d.Runtime = extra.Runtime
	}
	if d.TotalSeasons == 0 {
		d.TotalSeasons = extra.TotalSeasons
	}
	if d.TotalEpisodes == 0 {
		d.TotalEpisodes = extra.TotalEpisodes
	}
	for k, v := range extra.ExternalIDs {
		if d.ExternalIDs[k] == "" {
			addExternalID(&d, k, v)
		}
	}
	d.People = mergePeople(d.People, extra.People)
	d.Seasons = mergeSeasons(d.Seasons, extra.Seasons)
	for _, s := range extra.Sources {
		found := false
		for _, old := range d.Sources {
			if old.Name == s.Name && old.URL == s.URL {
				found = true
				break
			}
		}
		if !found {
			d.Sources = append(d.Sources, s)
		}
	}
	return d
}
func applyRemoteMetadata(it *Item, v metadataValues) {
	for _, field := range []struct {
		key string
		dst *string
		src string
	}{{"title", &it.Title, v.Title}, {"originalTitle", &it.OriginalTitle, v.OriginalTitle}, {"year", &it.Year, v.Year}, {"overview", &it.Overview, v.Overview}} {
		if field.src != "" && (!containsField(it.LocalFields, field.key) || *field.dst == "") {
			*field.dst = field.src
		}
	}
	if v.Poster != "" && !strings.HasPrefix(it.Poster, "local:") {
		it.Poster = v.Poster
	}
	if v.Backdrop != "" && !strings.HasPrefix(it.Backdrop, "local:") {
		it.Backdrop = v.Backdrop
	}
	if v.Rating > 0 && !containsField(it.LocalFields, "rating") {
		it.Rating = v.Rating
	}
	if len(v.Genres) > 0 && !containsField(it.LocalFields, "genres") {
		it.Genres = append([]string{}, v.Genres...)
	}
	if len(v.Cast) > 0 && !containsField(it.LocalFields, "cast") {
		it.Cast = append([]string{}, v.Cast...)
	}
	old := it.Details
	it.Details = mergeDetails(v.Details, old)
	for _, field := range []struct {
		key string
		dst *string
		src string
	}{{"tagline", &it.Details.Tagline, old.Tagline}, {"premiereDate", &it.Details.PremiereDate, old.PremiereDate}, {"endDate", &it.Details.EndDate, old.EndDate}, {"contentRating", &it.Details.ContentRating, old.ContentRating}, {"rating", &it.Details.RatingSource, old.RatingSource}} {
		if containsField(it.LocalFields, field.key) && field.src != "" {
			*field.dst = field.src
		}
	}
	for _, field := range []struct {
		key string
		dst *[]string
		src []string
	}{{"countries", &it.Details.Countries, old.Countries}, {"languages", &it.Details.Languages, old.Languages}, {"studios", &it.Details.Studios, old.Studios}, {"directors", &it.Details.Directors, old.Directors}, {"writers", &it.Details.Writers, old.Writers}, {"tags", &it.Details.Tags, old.Tags}} {
		if containsField(it.LocalFields, field.key) && len(field.src) > 0 {
			*field.dst = append([]string{}, field.src...)
		}
	}
	if containsField(it.LocalFields, "runtime") && old.Runtime > 0 {
		it.Details.Runtime = old.Runtime
	}
	if v.CompleteCast && !containsField(it.LocalFields, "cast") {
		extra := []Person{}
		for _, p := range old.People {
			if p.Type != "Actor" {
				extra = append(extra, p)
				continue
			}
			for _, current := range v.Details.People {
				if current.Type == "Actor" && samePerson(p, current) {
					extra = append(extra, p)
					break
				}
			}
		}
		it.Details.People = mergePeople(v.Details.People, extra)
	} else {
		it.Details.People = mergePeople(old.People, it.Details.People)
	}
	it.Episodes = mergeEpisodes(it.Episodes, v.Episodes)
	it.Details.Updated = now()
}

// Re-read NFO when enriching an existing library, including older imports without field provenance.
func (a *App) refreshLocalDetails(id string) {
	a.mu.Lock()
	id = a.canonicalItemIDLocked(id)
	kind := ""
	sources := append([]Source{}, a.s.Sources...)
	files := append([]MediaFile{}, a.s.Files...)
	for _, it := range a.s.Items {
		if it.ID == id {
			kind = it.Type
		}
	}
	a.mu.Unlock()
	if kind == "" {
		return
	}
	for _, s := range sources {
		if s.Type != "local" {
			continue
		}
		rows := []PreviewRow{}
		for _, f := range files {
			if f.ItemID == id && f.SourceID == s.ID {
				rows = append(rows, PreviewRow{Entry: Entry{Key: f.SourceKey, Name: f.Name}, Season: f.Season, Episode: f.Episode})
			}
		}
		if len(rows) == 0 {
			continue
		}
		info := a.readLocalMetadata(s, rows, kind)
		if info == nil {
			continue
		}
		a.mu.Lock()
		for i := range a.s.Items {
			it := &a.s.Items[i]
			if it.ID != id {
				continue
			}
			provider, external, link, note, status := it.MetadataProvider, it.MetadataID, it.MetadataURL, it.MetadataNote, it.MetadataStatus
			info.apply(it)
			if kind == "tv" {
				it.Title = seriesTitle(it.Title)
			}
			if provider != "" && provider != "local" {
				it.MetadataProvider, it.MetadataID, it.MetadataURL, it.MetadataNote, it.MetadataStatus = provider, external, link, note, status
			}
			break
		}
		_ = a.saveLocked()
		a.mu.Unlock()
	}
}
func (a *App) backfillDetails(ctx context.Context) {
	a.mu.Lock()
	enabled := a.s.Config.AutoMetadata
	items := append([]Item{}, a.s.Items...)
	a.mu.Unlock()
	if !enabled {
		return
	}
	for _, it := range items {
		if ctx.Err() != nil {
			return
		}
		if it.Details.Updated != "" {
			continue
		}
		a.refreshLocalDetails(it.ID)
		if it.MetadataProvider != "" && it.MetadataProvider != "local" && it.MetadataID != "" {
			a.scrapeSelected(it.ID, it.MetadataProvider, it.MetadataID)
		} else if it.TMDBID > 0 {
			a.mu.Lock()
			configured := a.s.Config.TMDBSecret != ""
			a.mu.Unlock()
			if configured {
				a.scrape(it.ID, it.TMDBID)
			} else {
				a.autoScrape(it.ID)
			}
		} else {
			a.autoScrape(it.ID)
		}
	}
}

func (a *App) refreshMetadata(id, provider, external string, tmdbID int, automatic bool) {
	a.refreshLocalDetails(id)
	if automatic {
		it, err := a.item(id)
		if err != nil {
			return
		}
		if it.MetadataProvider != "" && it.MetadataProvider != "local" && it.MetadataID != "" {
			a.scrapeSelected(it.ID, it.MetadataProvider, it.MetadataID)
		} else {
			a.autoScrape(it.ID)
		}
		return
	}
	if provider == "tvmaze" || provider == "wikipedia" {
		a.scrapeSelected(id, provider, external)
	} else {
		a.scrape(id, tmdbID)
	}
}
