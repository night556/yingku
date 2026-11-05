package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

func seasonsFromEpisodes(episodes []Episode) []Season {
	seasons := map[int]Season{}
	seen := map[string]bool{}
	for _, ep := range episodes {
		key := fmt.Sprintf("%d:%d", ep.Season, ep.Number)
		if seen[key] {
			continue
		}
		seen[key] = true
		s := seasons[ep.Season]
		s.Number = ep.Season
		s.EpisodeCount++
		if ep.AirDate != "" {
			if s.PremiereDate == "" || ep.AirDate < s.PremiereDate {
				s.PremiereDate = ep.AirDate
			}
			if ep.AirDate > s.EndDate {
				s.EndDate = ep.AirDate
			}
		}
		seasons[ep.Season] = s
	}
	out := []Season{}
	for _, s := range seasons {
		out = append(out, s)
	}
	return mergeSeasons(nil, out)
}
func (a *App) addMazeDetails(ctx context.Context, id int, v *metadataValues) {
	type result struct {
		seasons                      []Season
		people                       []Person
		directors, writers, creators []string
		backdrop                     string
	}
	ch := make(chan result, 3)
	go func() {
		var response []struct {
			Number   int    `json:"number"`
			Name     string `json:"name"`
			Premiere string `json:"premiereDate"`
			End      string `json:"endDate"`
			Count    int    `json:"episodeOrder"`
			Summary  string `json:"summary"`
			Image    struct {
				Medium   string `json:"medium"`
				Original string `json:"original"`
			} `json:"image"`
		}
		r := result{}
		if publicRequest(a, ctx, fmt.Sprintf("https://api.tvmaze.com/shows/%d/seasons", id), &response) == nil {
			for _, s := range response {
				pic := s.Image.Medium
				if pic == "" {
					pic = s.Image.Original
				}
				r.seasons = append(r.seasons, Season{Number: s.Number, Name: s.Name, PremiereDate: s.Premiere, EndDate: s.End, EpisodeCount: s.Count, Overview: plainSummary(s.Summary), Poster: pic})
			}
		}
		ch <- r
	}()
	go func() {
		var response []struct {
			Type   string `json:"type"`
			Person struct {
				Name  string `json:"name"`
				Image struct {
					Medium string `json:"medium"`
				} `json:"image"`
			} `json:"person"`
		}
		r := result{}
		if publicRequest(a, ctx, fmt.Sprintf("https://api.tvmaze.com/shows/%d/crew", id), &response) == nil {
			for _, p := range response {
				kind := "Crew"
				switch strings.ToLower(p.Type) {
				case "director":
					kind = "Director"
					r.directors = append(r.directors, p.Person.Name)
				case "writer":
					kind = "Writer"
					r.writers = append(r.writers, p.Person.Name)
				case "creator":
					kind = "Creator"
					r.creators = append(r.creators, p.Person.Name)
				case "executive producer", "producer":
					kind = "Producer"
				}
				if strings.Contains(strings.ToLower(p.Type), "producer") {
					kind = "Producer"
				}
				if len(r.people) < 24 {
					r.people = mergePeople(r.people, []Person{{Name: p.Person.Name, Type: kind, Role: p.Type, Image: p.Person.Image.Medium}})
				}
			}
		}
		ch <- r
	}()
	go func() {
		var response []struct {
			Type        string `json:"type"`
			Resolutions struct {
				Original struct {
					URL string `json:"url"`
				} `json:"original"`
			} `json:"resolutions"`
		}
		r := result{}
		if publicRequest(a, ctx, fmt.Sprintf("https://api.tvmaze.com/shows/%d/images", id), &response) == nil {
			for _, p := range response {
				if p.Type == "background" {
					if _, e := publicArtURL(p.Resolutions.Original.URL); e == nil {
						r.backdrop = p.Resolutions.Original.URL
						break
					}
				}
			}
		}
		ch <- r
	}()
	for i := 0; i < 3; i++ {
		r := <-ch
		v.Details.Seasons = mergeSeasons(v.Details.Seasons, r.seasons)
		v.Details.People = mergePeople(v.Details.People, r.people)
		v.Details.Directors = append(v.Details.Directors, r.directors...)
		v.Details.Writers = append(v.Details.Writers, r.writers...)
		v.Details.Creators = append(v.Details.Creators, r.creators...)
		if r.backdrop != "" {
			v.Backdrop = r.backdrop
		}
	}
	v.Details.Seasons = mergeSeasons(v.Details.Seasons, seasonsFromEpisodes(v.Episodes))
	v.Details.TotalEpisodes = len(v.Episodes)
	for _, s := range v.Details.Seasons {
		if s.Number > 0 {
			v.Details.TotalSeasons++
		}
	}
}
func (a *App) mazeIDForIMDb(ctx context.Context, id string) string {
	if !imdbIDRE.MatchString(id) {
		return ""
	}
	var show mazeShow
	if publicRequest(a, ctx, "https://api.tvmaze.com/lookup/shows?imdb="+id, &show) != nil || show.ID < 1 {
		return ""
	}
	return strconv.Itoa(show.ID)
}
