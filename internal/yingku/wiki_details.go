package yingku

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

func claimString(e wikiEntity, property string) string {
	for _, c := range e.Claims[property] {
		if c.Rank == "deprecated" {
			continue
		}
		var v string
		if json.Unmarshal(c.Main.Data.Value, &v) == nil && v != "" {
			return v
		}
	}
	return ""
}
func claimDate(e wikiEntity, properties ...string) (string, string) {
	year, date := "", ""
	for _, property := range properties {
		for _, c := range e.Claims[property] {
			if c.Rank == "deprecated" {
				continue
			}
			var v struct {
				Time string `json:"time"`
			}
			if json.Unmarshal(c.Main.Data.Value, &v) != nil {
				continue
			}
			text := strings.TrimPrefix(v.Time, "+")
			y := yearOf(text)
			if y != "" && (year == "" || y < year) {
				year = y
			}
			if len(text) >= 10 {
				candidate := text[:10]
				if _, err := time.Parse("2006-01-02", candidate); err == nil && (date == "" || candidate < date) {
					date = candidate
				}
			}
		}
	}
	return year, date
}
func (a *App) addWikidataDetails(ctx context.Context, p wikiPage, v *metadataValues) {
	if !entityIDRE.MatchString(p.PageProps.Entity) {
		return
	}
	entities, err := a.wikiEntities(ctx, []string{p.PageProps.Entity}, "claims|labels")
	if err != nil {
		return
	}
	e, found := entities[p.PageProps.Entity]
	if !found {
		return
	}
	d := &v.Details
	addExternalID(d, "wikidata", p.PageProps.Entity)
	d.Sources = append(d.Sources, MetadataSource{Name: "Wikidata", URL: "https://www.wikidata.org/wiki/" + p.PageProps.Entity})
	if year, date := claimDate(e, "P580", "P577"); year != "" {
		v.Year = year
		d.PremiereDate = date
	}
	_, d.EndDate = claimDate(e, "P582")
	for _, c := range e.Claims["P1476"] {
		if c.Rank == "deprecated" {
			continue
		}
		var title struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(c.Main.Data.Value, &title) == nil && title.Text != "" {
			v.OriginalTitle = title.Text
			break
		}
	}
	d.Homepage = safeWebURL(claimString(e, "P856"))
	addExternalID(d, "imdb", claimString(e, "P345"))
	addExternalID(d, "tvmaze", claimString(e, "P8600"))
	addExternalID(d, "tvdb", claimString(e, "P4835"))
	if id := claimString(e, "P4983"); id != "" {
		addExternalID(d, "tmdb", id)
	} else {
		addExternalID(d, "tmdb", claimString(e, "P4947"))
	}
	for _, c := range e.Claims["P2047"] {
		if c.Rank == "deprecated" {
			continue
		}
		var runtime struct {
			Amount string `json:"amount"`
			Unit   string `json:"unit"`
		}
		if json.Unmarshal(c.Main.Data.Value, &runtime) == nil && strings.HasSuffix(runtime.Unit, "/Q7727") {
			if n, e := strconv.ParseFloat(runtime.Amount, 64); e == nil && n > 0 && n <= 1500 {
				d.Runtime = int(n + 0.5)
				break
			}
		}
	}
	type labelGroup struct {
		property string
		limit    int
		values   []string
	}
	groups := []labelGroup{{property: "P161", limit: 16}, {property: "P136", limit: 6}, {property: "P57", limit: 6}, {property: "P58", limit: 6}, {property: "P170", limit: 4}, {property: "P495", limit: 4}, {property: "P364", limit: 4}, {property: "P272", limit: 6}, {property: "P921", limit: 4}}
	ids := []string{}
	seen := map[string]bool{}
	for i := range groups {
		g := &groups[i]
		g.values = entityClaims(e, g.property)
		if len(g.values) > g.limit {
			g.values = g.values[:g.limit]
		}
		for _, id := range g.values {
			if !seen[id] && len(ids) < 50 {
				ids = append(ids, id)
				seen[id] = true
			}
		}
	}
	if len(ids) > 0 {
		if labels, e := a.wikiEntities(ctx, ids, "labels"); e == nil {
			for _, g := range groups {
				for _, id := range g.values {
					entity := labels[id]
					name := entityLabel(entity)
					if name == "" {
						continue
					}
					switch g.property {
					case "P161":
						v.Cast = append(v.Cast, name)
						d.People = append(d.People, Person{Name: name, OriginalName: entity.Labels["en"].Value, Type: "Actor"})
					case "P136":
						v.Genres = append(v.Genres, name)
					case "P57":
						d.Directors = append(d.Directors, name)
						d.People = append(d.People, Person{Name: name, OriginalName: entity.Labels["en"].Value, Type: "Director"})
					case "P58":
						d.Writers = append(d.Writers, name)
						d.People = append(d.People, Person{Name: name, OriginalName: entity.Labels["en"].Value, Type: "Writer"})
					case "P170":
						d.Creators = append(d.Creators, name)
						d.People = append(d.People, Person{Name: name, OriginalName: entity.Labels["en"].Value, Type: "Creator"})
					case "P495":
						d.Countries = append(d.Countries, name)
					case "P364":
						d.Languages = append(d.Languages, name)
					case "P272":
						d.Studios = append(d.Studios, name)
					case "P921":
						d.Tags = append(d.Tags, name)
					}
				}
			}
		}
	}
	mazeID := d.ExternalIDs["tvmaze"]
	if mazeID == "" && d.ExternalIDs["tvdb"] != "" { // A TV-series identifier keeps movie IMDb entries out of the lookup.
		mazeID = a.mazeIDForIMDb(ctx, d.ExternalIDs["imdb"])
	}
	if mazeID != "" {
		if maze, e := a.fetchMaze(ctx, mazeID); e == nil {
			localPeople := d.People
			*d = mergeDetails(*d, maze.Details)
			people := []Person{}
			for _, person := range maze.Details.People {
				if person.Type != "Actor" {
					continue
				}
				localized := person
				for _, known := range localPeople {
					if known.Type == "Actor" && samePerson(known, person) {
						localized = mergePeople([]Person{known}, []Person{person})[0]
						break
					}
				}
				people = append(people, localized)
			}
			for _, known := range localPeople {
				if known.Type != "Actor" {
					people = mergePeople(people, []Person{known})
				}
			}
			for _, person := range maze.Details.People {
				if person.Type != "Actor" {
					people = mergePeople(people, []Person{person})
				}
			}
			d.People = people
			v.CompleteCast = maze.CompleteCast
			// These dates describe the whole series in the linked TV database.
			if maze.Details.PremiereDate != "" {
				d.PremiereDate = maze.Details.PremiereDate
				v.Year = maze.Year
			}
			d.Status = maze.Details.Status
			d.EndDate = maze.Details.EndDate
			if v.OriginalTitle == "" {
				v.OriginalTitle = maze.Title
			}
			if maze.Poster != "" {
				v.Poster = maze.Poster
			}
			if v.Backdrop == "" {
				v.Backdrop = maze.Backdrop
			}
			if maze.Rating > 0 {
				v.Rating = maze.Rating
				d.RatingSource = "TVmaze"
			}
			v.Episodes = mergeEpisodes(v.Episodes, maze.Episodes)
			v.Note += " 剧集海报、季集、角色、人物照片及评分补充自 TVmaze（CC BY-SA）。"
		}
	}
}
