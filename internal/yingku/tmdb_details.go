package yingku

import "strconv"

type tmdbDetails struct {
	TMDBResult
	Tagline        string `json:"tagline"`
	Runtime        int    `json:"runtime"`
	EpisodeRuntime []int  `json:"episode_run_time"`
	Status         string `json:"status"`
	EndDate        string `json:"last_air_date"`
	Homepage       string `json:"homepage"`
	IMDb           string `json:"imdb_id"`
	TotalSeasons   int    `json:"number_of_seasons"`
	TotalEpisodes  int    `json:"number_of_episodes"`
	Genres         []struct {
		Name string `json:"name"`
	} `json:"genres"`
	Countries []struct {
		Name string `json:"name"`
	} `json:"production_countries"`
	Languages []struct {
		Name        string `json:"name"`
		EnglishName string `json:"english_name"`
	} `json:"spoken_languages"`
	Studios []struct {
		Name string `json:"name"`
	} `json:"production_companies"`
	CreatedBy []struct {
		Name  string `json:"name"`
		Image string `json:"profile_path"`
	} `json:"created_by"`
	Credits struct {
		Cast []struct {
			Name      string `json:"name"`
			Character string `json:"character"`
			Image     string `json:"profile_path"`
		} `json:"cast"`
		Crew []struct {
			Name       string `json:"name"`
			Job        string `json:"job"`
			Department string `json:"department"`
			Image      string `json:"profile_path"`
		} `json:"crew"`
	} `json:"credits"`
	ExternalIDs struct {
		IMDb string `json:"imdb_id"`
		TVDB int    `json:"tvdb_id"`
	} `json:"external_ids"`
	Seasons []struct {
		Number   int    `json:"season_number"`
		Name     string `json:"name"`
		Date     string `json:"air_date"`
		Count    int    `json:"episode_count"`
		Overview string `json:"overview"`
		Poster   string `json:"poster_path"`
	} `json:"seasons"`
	Keywords struct {
		Keywords []struct {
			Name string `json:"name"`
		} `json:"keywords"`
		Results []struct {
			Name string `json:"name"`
		} `json:"results"`
	} `json:"keywords"`
	ContentRatings struct {
		Results []struct {
			Country string `json:"iso_3166_1"`
			Rating  string `json:"rating"`
		} `json:"results"`
	} `json:"content_ratings"`
	ReleaseDates struct {
		Results []struct {
			Country string `json:"iso_3166_1"`
			Dates   []struct {
				Certification string `json:"certification"`
			} `json:"release_dates"`
		} `json:"results"`
	} `json:"release_dates"`
}

func (d tmdbDetails) values(kind string, id int, episodes []Episode) metadataValues {
	title, original, date := d.Title, d.OriginalTitle, d.Release
	if kind == "tv" {
		title, original, date = d.Name, d.OriginalName, d.AirDate
	}
	v := metadataValues{Title: title, OriginalTitle: original, Year: yearOf(date), Overview: d.Overview, Poster: d.Poster, Backdrop: d.Backdrop, Rating: d.Rating, Episodes: episodes}
	v.CompleteCast = len(d.Credits.Cast) > 0
	m := MetadataDetails{Tagline: d.Tagline, PremiereDate: date, Status: d.Status, Runtime: d.Runtime, RatingSource: "TMDb", Homepage: safeWebURL(d.Homepage), TotalSeasons: d.TotalSeasons, TotalEpisodes: d.TotalEpisodes, Sources: []MetadataSource{{Name: "TMDb", URL: "https://www.themoviedb.org/" + kind + "/" + strconv.Itoa(id)}}}
	// last_air_date may describe the latest episode of an ongoing series, not its end.
	if d.Status == "Ended" || d.Status == "Canceled" {
		m.EndDate = d.EndDate
	}
	if m.Runtime == 0 && len(d.EpisodeRuntime) > 0 {
		m.Runtime = d.EpisodeRuntime[0]
	}
	for _, g := range d.Genres {
		v.Genres = append(v.Genres, g.Name)
	}
	for _, c := range d.Countries {
		m.Countries = append(m.Countries, c.Name)
	}
	for _, l := range d.Languages {
		name := l.Name
		if name == "" {
			name = l.EnglishName
		}
		m.Languages = append(m.Languages, name)
	}
	for _, s := range d.Studios {
		m.Studios = append(m.Studios, s.Name)
	}
	for i, p := range d.Credits.Cast {
		if i >= 24 {
			break
		}
		v.Cast = append(v.Cast, p.Name)
		m.People = append(m.People, Person{Name: p.Name, Role: p.Character, Image: p.Image, Type: "Actor"})
	}
	for _, p := range d.Credits.Crew {
		kind := ""
		if p.Job == "Director" {
			kind = "Director"
			m.Directors = append(m.Directors, p.Name)
		} else if p.Department == "Writing" {
			kind = "Writer"
			if !containsField(m.Writers, p.Name) {
				m.Writers = append(m.Writers, p.Name)
			}
		}
		if kind != "" {
			m.People = mergePeople(m.People, []Person{{Name: p.Name, Role: p.Job, Type: kind, Image: p.Image}})
		}
	}
	for _, p := range d.CreatedBy {
		m.Creators = append(m.Creators, p.Name)
		m.People = mergePeople(m.People, []Person{{Name: p.Name, Type: "Creator", Image: p.Image}})
	}
	for _, s := range d.Seasons {
		m.Seasons = append(m.Seasons, Season{Number: s.Number, Name: s.Name, PremiereDate: s.Date, EpisodeCount: s.Count, Overview: s.Overview, Poster: s.Poster})
	}
	for _, k := range d.Keywords.Keywords {
		if len(m.Tags) < 12 {
			m.Tags = append(m.Tags, k.Name)
		}
	}
	for _, k := range d.Keywords.Results {
		if len(m.Tags) < 12 {
			m.Tags = append(m.Tags, k.Name)
		}
	}
	for _, country := range []string{"CN", "US", "GB"} {
		for _, r := range d.ContentRatings.Results {
			if r.Country == country && r.Rating != "" && m.ContentRating == "" {
				m.ContentRating = r.Country + " · " + r.Rating
			}
		}
		for _, r := range d.ReleaseDates.Results {
			if r.Country == country {
				for _, c := range r.Dates {
					if c.Certification != "" && m.ContentRating == "" {
						m.ContentRating = r.Country + " · " + c.Certification
					}
				}
			}
		}
	}
	addExternalID(&m, "tmdb", strconv.Itoa(id))
	addExternalID(&m, "imdb", d.IMDb)
	addExternalID(&m, "imdb", d.ExternalIDs.IMDb)
	addExternalID(&m, "tvdb", strconv.Itoa(d.ExternalIDs.TVDB))
	v.Details = m
	return v
}
