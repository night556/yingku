package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (a *App) tmdb(ctx context.Context, endpoint string, q url.Values, out any) error {
	a.mu.Lock()
	secret := a.s.Config.TMDBSecret
	a.mu.Unlock()
	if secret == "" {
		return errors.New("请先在设置中填写 TMDb API Key 或读取令牌")
	}
	var token string
	if err := a.decrypt(secret, &token); err != nil {
		return err
	}
	if q == nil {
		q = url.Values{}
	}
	q.Set("language", "zh-CN")
	if len(token) == 32 {
		q.Set("api_key", token)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.themoviedb.org/3"+endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	if len(token) != 32 {
		req.Header.Set("Authorization", "Bearer "+strings.TrimPrefix(token, "Bearer "))
	}
	req.Header.Set("Accept", "application/json")
	return a.requestJSON(req, out)
}

type TMDBResult struct {
	ID            int     `json:"id"`
	Title         string  `json:"title"`
	Name          string  `json:"name"`
	OriginalTitle string  `json:"original_title"`
	OriginalName  string  `json:"original_name"`
	Overview      string  `json:"overview"`
	Poster        string  `json:"poster_path"`
	Backdrop      string  `json:"backdrop_path"`
	Release       string  `json:"release_date"`
	AirDate       string  `json:"first_air_date"`
	Rating        float64 `json:"vote_average"`
}

func (a *App) searchMetadata(ctx context.Context, kind, query string) ([]map[string]any, error) {
	if kind != "movie" && kind != "tv" {
		return nil, errors.New("无效作品类型")
	}
	if strings.TrimSpace(query) == "" {
		return []map[string]any{}, nil
	}
	a.mu.Lock()
	configured := a.s.Config.TMDBSecret != ""
	a.mu.Unlock()
	if !configured {
		return a.searchPublicMetadata(ctx, kind, query)
	}
	var response struct {
		Results []TMDBResult `json:"results"`
	}
	if err := a.tmdb(ctx, "/search/"+kind, url.Values{"query": {query}, "include_adult": {"false"}}, &response); err != nil {
		return nil, err
	}
	results := []map[string]any{}
	for _, r := range response.Results {
		title, date := r.Title, r.Release
		if kind == "tv" {
			title, date = r.Name, r.AirDate
		}
		year := ""
		if len(date) >= 4 {
			year = date[:4]
		}
		results = append(results, map[string]any{"id": r.ID, "externalId": strconv.Itoa(r.ID), "provider": "tmdb", "providerName": "TMDb", "title": title, "year": year, "poster": r.Poster, "overview": r.Overview, "type": kind})
	}
	if len(results) == 0 {
		fallback, err := a.searchPublicMetadata(ctx, kind, query)
		if err != nil {
			return nil, fmt.Errorf("TMDb 未找到匹配作品，补充检索公开资料时失败：%w", err)
		}
		return fallback, nil
	}
	return results, nil
}
func (a *App) scrape(itemID string, tmdbID int, expectedJob ...string) {
	a.mu.Lock()
	itemID = a.canonicalItemIDLocked(itemID)
	if len(expectedJob) > 0 && a.metadataJobs[itemID] != expectedJob[0] {
		a.mu.Unlock()
		return
	}
	job := uid()
	a.metadataJobs[itemID] = job
	var item Item
	found := false
	for i := range a.s.Items {
		if a.s.Items[i].ID == itemID {
			a.s.Items[i].TMDBID = tmdbID
			a.s.Items[i].MetadataProvider = "tmdb"
			a.s.Items[i].MetadataID = strconv.Itoa(tmdbID)
			a.s.Items[i].MetadataURL = fmt.Sprintf("https://www.themoviedb.org/%s/%d", a.s.Items[i].Type, tmdbID)
			a.s.Items[i].MetadataNote = "资料来自 TMDb。"
			a.s.Items[i].MetadataStatus = "loading"
			a.s.Items[i].MetadataError = ""
			item = a.s.Items[i]
			found = true
			break
		}
	}
	if !found {
		a.mu.Unlock()
		return
	}
	err := a.saveLocked()
	a.mu.Unlock()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var details tmdbDetails
	appendices := "credits,external_ids,keywords,release_dates"
	if item.Type == "tv" {
		appendices = "credits,external_ids,keywords,content_ratings"
	}
	err = a.tmdb(ctx, fmt.Sprintf("/%s/%d", item.Type, tmdbID), url.Values{"append_to_response": {appendices}}, &details)
	episodes := []Episode{}
	if err == nil && item.Type == "tv" {
		a.mu.Lock()
		seasons := map[int]bool{}
		for _, f := range a.s.Files {
			if f.ItemID == itemID {
				seasons[f.Season] = true
			}
		}
		a.mu.Unlock()
		numbers := []int{}
		for season := range seasons {
			numbers = append(numbers, season)
		}
		sort.Ints(numbers)
		for _, season := range numbers {
			var data struct {
				Episodes []struct {
					Season   int     `json:"season_number"`
					Number   int     `json:"episode_number"`
					Name     string  `json:"name"`
					Overview string  `json:"overview"`
					Still    string  `json:"still_path"`
					AirDate  string  `json:"air_date"`
					Runtime  int     `json:"runtime"`
					Rating   float64 `json:"vote_average"`
				} `json:"episodes"`
			}
			if err = a.tmdb(ctx, fmt.Sprintf("/tv/%d/season/%d", tmdbID, season), nil, &data); err != nil {
				break
			}
			for _, ep := range data.Episodes {
				episodes = append(episodes, Episode{Season: ep.Season, Number: ep.Number, Name: ep.Name, Overview: ep.Overview, Still: ep.Still, AirDate: ep.AirDate, Runtime: ep.Runtime, Rating: ep.Rating, RatingSource: "TMDb"})
			}
		}
	}
	a.mu.Lock()
	for i := range a.s.Items {
		it := &a.s.Items[i]
		if it.ID != itemID || it.TMDBID != tmdbID || a.metadataJobs[itemID] != job {
			continue
		}
		if details.ID > 0 {
			applyRemoteMetadata(it, details.values(it.Type, tmdbID, episodes))
		}
		if err != nil {
			it.MetadataStatus = "failed"
			it.MetadataError = err.Error()
		} else {
			it.MetadataStatus = "ready"
			it.MetadataError = ""
		}
		break
	}
	if groupErr := a.regroupSeriesLocked(); groupErr != nil {
		for i := range a.s.Items {
			if a.s.Items[i].ID == itemID {
				a.s.Items[i].MetadataError = groupErr.Error()
			}
		}
	}
	itemID = a.canonicalItemIDLocked(itemID)
	_ = a.saveLocked()
	a.mu.Unlock()
	a.writeNFO(itemID)
}

type nfoID struct {
	Type    string `xml:"type,attr"`
	Default string `xml:"default,attr,omitempty"`
	Value   string `xml:",chardata"`
}
type nfo struct {
	XMLName       xml.Name
	Title         string     `xml:"title"`
	OriginalTitle string     `xml:"originaltitle,omitempty"`
	Year          string     `xml:"year,omitempty"`
	Plot          string     `xml:"plot,omitempty"`
	Season        *int       `xml:"season,omitempty"`
	Episode       int        `xml:"episode,omitempty"`
	Aired         string     `xml:"aired,omitempty"`
	IDs           []nfoID    `xml:"uniqueid,omitempty"`
	Genres        []string   `xml:"genre,omitempty"`
	Thumb         string     `xml:"thumb,omitempty"`
	Tagline       string     `xml:"tagline,omitempty"`
	Premiered     string     `xml:"premiered,omitempty"`
	EndDate       string     `xml:"enddate,omitempty"`
	Runtime       int        `xml:"runtime,omitempty"`
	Rating        float64    `xml:"rating,omitempty"`
	MPAA          string     `xml:"mpaa,omitempty"`
	Countries     []string   `xml:"country,omitempty"`
	Languages     []string   `xml:"language,omitempty"`
	Studios       []string   `xml:"studio,omitempty"`
	Directors     []string   `xml:"director,omitempty"`
	Writers       []string   `xml:"writer,omitempty"`
	Tags          []string   `xml:"tag,omitempty"`
	SeasonNumber  *int       `xml:"seasonnumber,omitempty"`
	Actors        []nfoActor `xml:"actor,omitempty"`
}
type nfoActor struct {
	Name  string `xml:"name"`
	Role  string `xml:"role,omitempty"`
	Thumb string `xml:"thumb,omitempty"`
}

func (a *App) writeNFO(itemID string) {
	a.mu.Lock()
	itemID = a.canonicalItemIDLocked(itemID)
	enabled := a.s.Config.ExportNFO
	var item Item
	for _, it := range a.s.Items {
		if it.ID == itemID {
			item = it
		}
	}
	files := []MediaFile{}
	for _, f := range a.s.Files {
		if f.ItemID == itemID && f.Status == "completed" && !f.Reference {
			files = append(files, f)
		}
	}
	a.mu.Unlock()
	if !enabled || item.ID == "" {
		return
	}
	ids := []nfoID{}
	if item.TMDBID > 0 {
		ids = append(ids, nfoID{Type: "tmdb", Default: "true", Value: strconv.Itoa(item.TMDBID)})
	}
	if item.MetadataID != "" && item.MetadataProvider != "" && item.MetadataProvider != "tmdb" && item.MetadataProvider != "local" {
		ids = append(ids, nfoID{Type: item.MetadataProvider, Value: item.MetadataID})
	}
	for provider, value := range item.Details.ExternalIDs {
		found := false
		for _, id := range ids {
			if id.Type == provider {
				found = true
				break
			}
		}
		if !found {
			ids = append(ids, nfoID{Type: provider, Value: value})
		}
	}
	roots := map[string]bool{}
	seasonRoots := map[string]bool{}
	write := func(file string, n nfo) {
		b, err := xml.MarshalIndent(n, "", "  ")
		if err == nil {
			_ = atomicWrite(file, append([]byte(xml.Header), b...), 0644)
		}
	}
	for _, f := range files {
		root := filepath.Dir(f.Destination)
		if item.Type == "tv" {
			root = filepath.Dir(root)
			season := f.Season
			n := nfo{XMLName: xml.Name{Local: "episodedetails"}, Title: fmt.Sprintf("第 %d 集", f.Episode), Season: &season, Episode: f.Episode}
			for _, ep := range item.Episodes {
				if ep.Season == f.Season && ep.Number == f.Episode {
					n.Title = ep.Name
					n.Plot = ep.Overview
					n.Aired = ep.AirDate
					n.Runtime = ep.Runtime
					n.Rating = ep.Rating
					if ep.Still != "" {
						n.Thumb = exportedArt(ep.Still)
					}
					break
				}
			}
			write(strings.TrimSuffix(f.Destination, filepath.Ext(f.Destination))+".nfo", n)
			seasonRoot := filepath.Dir(f.Destination)
			if !seasonRoots[seasonRoot] {
				seasonRoots[seasonRoot] = true
				for _, s := range item.Details.Seasons {
					if s.Number == f.Season {
						number := s.Number
						write(filepath.Join(seasonRoot, "season.nfo"), nfo{XMLName: xml.Name{Local: "season"}, Title: s.Name, Year: s.Year, SeasonNumber: &number, Plot: s.Overview, Premiered: s.PremiereDate, EndDate: s.EndDate, Thumb: exportedArt(s.Poster)})
						break
					}
				}
			}
		}
		if roots[root] {
			continue
		}
		roots[root] = true
		n := nfo{Title: item.Title, OriginalTitle: item.OriginalTitle, Year: item.Year, Plot: item.Overview, IDs: ids, Genres: item.Genres}
		d := item.Details
		n.Tagline = d.Tagline
		n.Premiered = d.PremiereDate
		n.EndDate = d.EndDate
		n.Runtime = d.Runtime
		n.Rating = item.Rating
		n.MPAA = d.ContentRating
		n.Countries = d.Countries
		n.Languages = d.Languages
		n.Studios = d.Studios
		n.Directors = d.Directors
		n.Writers = d.Writers
		n.Tags = d.Tags
		for _, p := range d.People {
			if p.Type == "Actor" {
				n.Actors = append(n.Actors, nfoActor{Name: p.Name, Role: p.Role, Thumb: exportedArt(p.Image)})
			}
		}
		if item.Poster != "" {
			n.Thumb = exportedArt(item.Poster)
		}
		if item.Type == "tv" {
			n.XMLName = xml.Name{Local: "tvshow"}
			write(filepath.Join(root, "tvshow.nfo"), n)
		} else {
			n.XMLName = xml.Name{Local: "movie"}
			write(filepath.Join(root, "movie.nfo"), n)
		}
	}
}

var artPathRE = regexp.MustCompile(`^/[A-Za-z0-9_-]+\.(?:jpg|jpeg|png|webp)$`)
var localArtRE = regexp.MustCompile(`^local-[a-f0-9]{64}\.(?:jpg|png|gif|webp)$`)

func exportedArt(s string) string {
	if strings.HasPrefix(s, "https://") {
		if u, err := publicArtURL(s); err == nil {
			return u.String()
		}
		return ""
	}
	if artPathRE.MatchString(s) {
		return "https://image.tmdb.org/t/p/original" + s
	}
	return ""
}

func (a *App) artwork(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if strings.HasPrefix(p, "local:") {
		name := strings.TrimPrefix(p, "local:")
		if !localArtRE.MatchString(name) {
			http.Error(w, "图片路径无效", 400)
			return
		}
		w.Header().Set("Cache-Control", "private, max-age=86400")
		http.ServeFile(w, r, filepath.Join(a.dir, "art", name))
		return
	}
	remote := ""
	if strings.HasPrefix(p, "https://") {
		u, err := publicArtURL(p)
		if err != nil {
			http.Error(w, "图片来源不支持", 400)
			return
		}
		remote = u.String()
	}
	if remote == "" && !artPathRE.MatchString(p) {
		http.Error(w, "图片路径无效", 400)
		return
	}
	size := "w500"
	if r.URL.Query().Get("wide") == "1" {
		size = "w1280"
	}
	if remote == "" {
		remote = "https://image.tmdb.org/t/p/" + size + p
	}
	sum := sha256.Sum256([]byte(remote))
	name := filepath.Join(a.dir, "art", hex.EncodeToString(sum[:])+".image")
	if _, err := os.Stat(name); err != nil {
		req, _ := http.NewRequestWithContext(r.Context(), "GET", remote, nil)
		req.Header.Set("User-Agent", metadataUserAgent)
		client := *a.client
		client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
			if len(via) > 5 {
				return errors.New("图片重定向过多")
			}
			_, err := publicArtURL(next.URL.String())
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, "图片暂时无法获取", 502)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			http.Error(w, "图片暂时无法获取", 502)
			return
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, (20<<20)+1))
		if err != nil || len(b) > 20<<20 {
			http.Error(w, "图片获取失败", 502)
			return
		}
		switch http.DetectContentType(b) {
		case "image/jpeg", "image/png", "image/gif", "image/webp":
		default:
			http.Error(w, "图片格式不支持", 502)
			return
		}
		if err = atomicWrite(name, b, 0644); err != nil {
			http.Error(w, "图片缓存失败", 500)
			return
		}
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, name)
}
