package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const metadataUserAgent = "Yingku/0.2 (personal desktop media library)"

var htmlTagRE = regexp.MustCompile(`<[^>]*>`)
var wikiIDRE = regexp.MustCompile(`^(zh|en):([1-9][0-9]{0,11})$`)
var entityIDRE = regexp.MustCompile(`^Q[1-9][0-9]*$`)
var disambiguationRE = regexp.MustCompile(`(?i)\s*[（(](?:[12][0-9]{3}年?\s*)?(?:电影|電影|电视剧|電視劇|电视连续剧|電視連續劇|电视系列剧|電視系列劇|电视影集|電視影集|film|TV series|television series)[）)]$`)
var nonScreenTitleRE = regexp.MustCompile(`(?i)[（(](?:小说|小說|novel|book|short story|actor|actress|director)[）)]$`)
var biographyLeadRE = regexp.MustCompile(`(?i)[（(]\s*(?:born\b|[12][0-9]{3}年[0-9]{1,2}月[0-9]{1,2}日\s*[—–－-]|[12][0-9]{3}\s*[—–－-]\s*[12][0-9]{3})`)

func plainSummary(s string) string {
	s = html.UnescapeString(htmlTagRE.ReplaceAllString(s, " "))
	return strings.Join(strings.Fields(s), " ")
}
func publicRequest(a *App, ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", metadataUserAgent)
	req.Header.Set("Accept", "application/json")
	return a.requestJSON(req, out)
}
func yearOf(s string) string { return yearRE.FindString(s) }
func displayWikiTitle(s string) string {
	return strings.TrimSpace(disambiguationRE.ReplaceAllString(s, ""))
}
func comparableTitle(s string) string {
	s = displayWikiTitle(strings.ToLower(s))
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, s)
}

type mazeShow struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	Summary        string `json:"summary"`
	Premiered      string `json:"premiered"`
	Ended          string `json:"ended"`
	Status         string `json:"status"`
	Language       string `json:"language"`
	Runtime        int    `json:"runtime"`
	AverageRuntime int    `json:"averageRuntime"`
	OfficialSite   string `json:"officialSite"`
	Network        struct {
		Name    string `json:"name"`
		Country struct {
			Name string `json:"name"`
		} `json:"country"`
	} `json:"network"`
	WebChannel struct {
		Name    string `json:"name"`
		Country struct {
			Name string `json:"name"`
		} `json:"country"`
	} `json:"webChannel"`
	Externals struct {
		IMDb string `json:"imdb"`
		TVDB int    `json:"thetvdb"`
	} `json:"externals"`
	Genres []string `json:"genres"`
	Image  struct {
		Medium   string `json:"medium"`
		Original string `json:"original"`
	} `json:"image"`
	Rating struct {
		Average float64 `json:"average"`
	} `json:"rating"`
	Embedded struct {
		Episodes []struct {
			Season  int    `json:"season"`
			Number  *int   `json:"number"`
			Name    string `json:"name"`
			Summary string `json:"summary"`
			Airdate string `json:"airdate"`
			Runtime int    `json:"runtime"`
			Rating  struct {
				Average float64 `json:"average"`
			} `json:"rating"`
			Image struct {
				Medium string `json:"medium"`
			} `json:"image"`
		} `json:"episodes"`
		Cast []struct {
			Person struct {
				Name  string `json:"name"`
				Image struct {
					Medium   string `json:"medium"`
					Original string `json:"original"`
				} `json:"image"`
			} `json:"person"`
			Character struct {
				Name string `json:"name"`
			} `json:"character"`
		} `json:"cast"`
	} `json:"_embedded"`
}

func (a *App) searchMaze(ctx context.Context, query string) ([]map[string]any, error) {
	var response []struct {
		Show mazeShow `json:"show"`
	}
	if err := publicRequest(a, ctx, "https://api.tvmaze.com/search/shows?q="+url.QueryEscape(query), &response); err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, r := range response {
		v := r.Show
		if v.ID < 1 {
			continue
		}
		out = append(out, map[string]any{"id": v.ID, "externalId": strconv.Itoa(v.ID), "provider": "tvmaze", "providerName": "TVmaze", "title": v.Name, "year": yearOf(v.Premiered), "poster": v.Image.Medium, "overview": plainSummary(v.Summary), "type": "tv"})
	}
	return out, nil
}

type wikiPage struct {
	ID        int    `json:"pageid"`
	Title     string `json:"title"`
	Extract   string `json:"extract"`
	Index     int    `json:"index"`
	Thumbnail struct {
		Source string `json:"source"`
	} `json:"thumbnail"`
	Original struct {
		Source string `json:"source"`
	} `json:"original"`
	PageProps struct {
		Entity         string  `json:"wikibase_item"`
		Disambiguation *string `json:"disambiguation"`
	} `json:"pageprops"`
}

func (a *App) wikiQuery(ctx context.Context, language string, q url.Values) ([]wikiPage, error) {
	q.Set("action", "query")
	q.Set("format", "json")
	q.Set("formatversion", "2")
	q.Set("prop", "pageimages|extracts|pageprops")
	q.Set("piprop", "thumbnail|original")
	q.Set("pilicense", "any")
	q.Set("pithumbsize", "500")
	q.Set("exintro", "1")
	q.Set("explaintext", "1")
	q.Set("exchars", "750")
	q.Set("redirects", "1")
	if language == "zh" {
		q.Set("variant", "zh-cn")
	}
	var data struct {
		Query struct {
			Pages []wikiPage `json:"pages"`
		} `json:"query"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := publicRequest(a, ctx, "https://"+language+".wikipedia.org/w/api.php?"+q.Encode(), &data); err != nil {
		return nil, err
	}
	if data.Error != nil {
		return nil, errors.New("维基百科检索暂时不可用")
	}
	sort.SliceStable(data.Query.Pages, func(i, j int) bool { return data.Query.Pages[i].Index < data.Query.Pages[j].Index })
	return data.Query.Pages, nil
}
func (a *App) searchWiki(ctx context.Context, kind, query string) ([]map[string]any, error) {
	lang := "en"
	for _, r := range query {
		if unicode.Is(unicode.Han, r) {
			lang = "zh"
			break
		}
	}
	pages, err := a.wikiQuery(ctx, lang, url.Values{"generator": {"search"}, "gsrsearch": {query}, "gsrnamespace": {"0"}, "gsrlimit": {"10"}})
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, p := range pages {
		if p.ID < 1 || p.PageProps.Disambiguation != nil {
			continue
		}
		pic := p.Thumbnail.Source
		if pic == "" {
			pic = p.Original.Source
		}
		if pic != "" {
			if _, e := publicArtURL(pic); e != nil {
				pic = ""
			}
		}
		// Avoid obviously unrelated books/people when a film or series was requested.
		lead := []rune(p.Extract)
		if len(lead) > 140 {
			lead = lead[:140]
		}
		// A biography may mention many films, and a novel may mention its adaptation.
		// Those mentions do not make the subject a screen work.
		if nonScreenTitleRE.MatchString(p.Title) || biographyLeadRE.MatchString(string(lead)) {
			continue
		}
		text := strings.ToLower(p.Title + " " + string(lead))
		mediaHint := strings.Contains(text, "电影") || strings.Contains(text, "電影") || strings.Contains(text, " film") || strings.Contains(text, "电视剧") || strings.Contains(text, "电视连续剧") || strings.Contains(text, "电视系列剧") || strings.Contains(text, "电视影集") || strings.Contains(text, "网络剧") || strings.Contains(text, "網路劇") || strings.Contains(text, "網絡劇") || strings.Contains(text, "電視") || strings.Contains(text, "television") || strings.Contains(text, "series")
		if !mediaHint && (strings.Contains(text, "小说") || strings.Contains(text, "小說") || strings.Contains(text, "novel") || strings.Contains(text, "演员") || strings.Contains(text, "演員")) {
			continue
		}
		out = append(out, map[string]any{"id": p.ID, "externalId": lang + ":" + strconv.Itoa(p.ID), "provider": "wikipedia", "providerName": "维基百科", "title": displayWikiTitle(p.Title), "year": yearOf(p.Extract), "poster": pic, "overview": p.Extract, "type": kind, "mediaHint": mediaHint})
	}
	return out, nil
}
func (a *App) searchPublicMetadata(ctx context.Context, kind, query string) ([]map[string]any, error) {
	type result struct {
		rows []map[string]any
		err  error
	}
	count := 1
	if kind == "tv" {
		count = 2
	}
	ch := make(chan result, count)
	go func() { rows, err := a.searchWiki(ctx, kind, query); ch <- result{rows, err} }()
	if kind == "tv" {
		go func() { rows, err := a.searchMaze(ctx, query); ch <- result{rows, err} }()
	}
	rows := []map[string]any{}
	failed := 0
	for i := 0; i < count; i++ {
		r := <-ch
		if r.err != nil {
			failed++
		} else {
			rows = append(rows, r.rows...)
		}
	}
	if failed == count {
		return nil, errors.New("公开资料服务暂时无法连接，请检查网络后重试；本地 NFO 和视频入库仍可使用")
	}
	sort.SliceStable(rows, func(i, j int) bool { return matchScore(rows[i], query, "") > matchScore(rows[j], query, "") })
	return rows, nil
}
func matchScore(m map[string]any, title, year string) int {
	if m["provider"] == "wikipedia" {
		if hint, _ := m["mediaHint"].(bool); !hint {
			return -100
		}
	}
	name, _ := m["title"].(string)
	score := 0
	if comparableTitle(name) == comparableTitle(title) && comparableTitle(title) != "" {
		score = 100
	} else if strings.Contains(comparableTitle(name), comparableTitle(title)) && len([]rune(title)) >= 3 {
		score = 30
	}
	matchedYear, _ := m["year"].(string)
	if year != "" && matchedYear != "" {
		if year != matchedYear {
			return -100
		}
		score += 20
	}
	if pic, _ := m["poster"].(string); pic != "" {
		score += 2
	}
	if hint, _ := m["mediaHint"].(bool); hint {
		score += 4
	}
	if m["provider"] == "tvmaze" {
		score += 4
	}
	return score
}

type metadataValues struct {
	Title, OriginalTitle, Year, Overview, Poster, Backdrop string
	Rating                                                 float64
	Genres, Cast                                           []string
	Episodes                                               []Episode
	URL, Note                                              string
	Details                                                MetadataDetails
	CompleteCast                                           bool
}

func (a *App) beginMetadata(id, provider, externalID, expected string) (Item, string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id = a.canonicalItemIDLocked(id)
	if expected != "" && a.metadataJobs[id] != expected {
		return Item{}, "", false
	}
	for i := range a.s.Items {
		it := &a.s.Items[i]
		if it.ID != id {
			continue
		}
		job := uid()
		a.metadataJobs[id] = job
		it.MetadataStatus = "loading"
		it.MetadataError = ""
		if provider != "" {
			it.MetadataProvider = provider
			it.MetadataID = externalID
		}
		_ = a.saveLocked()
		return *it, job, true
	}
	return Item{}, "", false
}
func (a *App) metadataProblem(id, job, message string, failed bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.metadataJobs[id] != job {
		return
	}
	for i := range a.s.Items {
		it := &a.s.Items[i]
		if it.ID == id {
			if failed {
				it.MetadataStatus = "failed"
				it.MetadataError = message
			} else {
				it.MetadataStatus = "manual"
				it.MetadataNote = message
				it.MetadataError = ""
			}
			break
		}
	}
	_ = a.saveLocked()
}
func (a *App) autoScrape(id string) {
	it, job, ok := a.beginMetadata(id, "", "", "")
	if !ok {
		return
	}
	id = it.ID
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	rows, err := a.searchMetadata(ctx, it.Type, it.Title)
	if err != nil {
		a.metadataProblem(id, job, err.Error(), true)
		return
	}
	best, second := -1, -1000
	score := -1000
	for i, row := range rows {
		n := matchScore(row, it.Title, it.Year)
		if n > score {
			second = score
			score = n
			best = i
		} else if n > second {
			second = n
		}
	}
	if best < 0 || score < 100 || score == second {
		a.metadataProblem(id, job, "没有找到足够确定的作品，请在详情中选择匹配结果。", false)
		return
	}
	row := rows[best]
	provider, _ := row["provider"].(string)
	external, _ := row["externalId"].(string)
	if provider == "tmdb" {
		tmdbID, _ := strconv.Atoi(external)
		a.scrape(id, tmdbID, job)
		return
	}
	a.scrapePublic(id, provider, external, job)
}
func (a *App) scrapeSelected(id, provider, externalID string) {
	if provider == "tmdb" {
		n, err := strconv.Atoi(externalID)
		if err == nil && n > 0 {
			a.scrape(id, n)
		}
		return
	}
	a.scrapePublic(id, provider, externalID, "")
}
func (a *App) scrapePublic(id, provider, externalID, expected string) {
	it, job, ok := a.beginMetadata(id, provider, externalID, expected)
	if !ok {
		return
	}
	id = it.ID
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	var values metadataValues
	var err error
	switch provider {
	case "tvmaze":
		if it.Type != "tv" {
			err = errors.New("TVmaze 仅提供剧集资料")
		} else {
			values, err = a.fetchMaze(ctx, externalID)
		}
	case "wikipedia":
		values, err = a.fetchWiki(ctx, externalID)
	default:
		err = errors.New("不支持的资料来源")
	}
	if err != nil {
		a.metadataProblem(id, job, err.Error(), true)
		return
	}
	a.mu.Lock()
	if a.metadataJobs[id] != job {
		a.mu.Unlock()
		return
	}
	for i := range a.s.Items {
		v := &a.s.Items[i]
		if v.ID != id {
			continue
		}
		localPoster := strings.HasPrefix(v.Poster, "local:")
		applyRemoteMetadata(v, values)
		v.MetadataProvider = provider
		v.MetadataID = externalID
		v.MetadataURL = values.URL
		v.MetadataNote = values.Note
		if localPoster {
			v.MetadataNote += " 封面沿用本地图片。"
		}
		v.MetadataStatus = "ready"
		v.MetadataError = ""
		break
	}
	groupErr := a.regroupSeriesLocked()
	if groupErr != nil {
		for i := range a.s.Items {
			if a.s.Items[i].ID == id {
				a.s.Items[i].MetadataError = groupErr.Error()
			}
		}
	}
	id = a.canonicalItemIDLocked(id)
	_ = a.saveLocked()
	a.mu.Unlock()
	a.writeNFO(id)
}
func (a *App) fetchMaze(ctx context.Context, id string) (metadataValues, error) {
	v := metadataValues{}
	n, err := strconv.Atoi(id)
	if err != nil || n < 1 {
		return v, errors.New("剧集编号无效")
	}
	var data mazeShow
	if err = publicRequest(a, ctx, fmt.Sprintf("https://api.tvmaze.com/shows/%d?embed[]=episodes&embed[]=cast", n), &data); err != nil {
		return v, err
	}
	if data.ID != n {
		return v, errors.New("剧集资料编号不匹配")
	}
	v = metadataValues{Title: data.Name, Year: yearOf(data.Premiered), Overview: plainSummary(data.Summary), Poster: data.Image.Original, Rating: data.Rating.Average, Genres: data.Genres, URL: fmt.Sprintf("https://www.tvmaze.com/shows/%d", n), Note: "资料来自 TVmaze（CC BY-SA），部分简介和集名为英文。"}
	v.OriginalTitle = data.Name
	v.CompleteCast = len(data.Embedded.Cast) > 0
	v.Details = MetadataDetails{PremiereDate: data.Premiered, EndDate: data.Ended, Status: data.Status, Runtime: data.Runtime, RatingSource: "TVmaze", Homepage: safeWebURL(data.OfficialSite), Sources: []MetadataSource{{Name: "TVmaze · CC BY-SA", URL: v.URL}}}
	if v.Details.Runtime == 0 {
		v.Details.Runtime = data.AverageRuntime
	}
	if data.Language != "" {
		v.Details.Languages = []string{data.Language}
	}
	for _, studio := range []string{data.Network.Name, data.WebChannel.Name} {
		if studio != "" {
			v.Details.Studios = append(v.Details.Studios, studio)
		}
	}
	for _, country := range []string{data.Network.Country.Name, data.WebChannel.Country.Name} {
		if country != "" && !containsField(v.Details.Countries, country) {
			v.Details.Countries = append(v.Details.Countries, country)
		}
	}
	addExternalID(&v.Details, "tvmaze", id)
	addExternalID(&v.Details, "imdb", data.Externals.IMDb)
	addExternalID(&v.Details, "tvdb", strconv.Itoa(data.Externals.TVDB))
	if v.Poster == "" {
		v.Poster = data.Image.Medium
	}
	for _, c := range data.Embedded.Cast {
		if len(v.Cast) == 24 {
			break
		}
		v.Cast = append(v.Cast, c.Person.Name)
		pic := c.Person.Image.Medium
		if pic == "" {
			pic = c.Person.Image.Original
		}
		v.Details.People = append(v.Details.People, Person{Name: c.Person.Name, Role: c.Character.Name, Type: "Actor", Image: pic})
	}
	for _, ep := range data.Embedded.Episodes {
		if ep.Number == nil || *ep.Number < 1 {
			continue
		}
		v.Episodes = append(v.Episodes, Episode{Season: ep.Season, Number: *ep.Number, Name: ep.Name, Overview: plainSummary(ep.Summary), Still: ep.Image.Medium, AirDate: ep.Airdate, Runtime: ep.Runtime, Rating: ep.Rating.Average, RatingSource: "TVmaze"})
	}
	a.addMazeDetails(ctx, n, &v)
	return v, nil
}

type wikiEntity struct {
	Labels map[string]struct {
		Value string `json:"value"`
	} `json:"labels"`
	Claims map[string][]struct {
		Rank string `json:"rank"`
		Main struct {
			Data struct {
				Value json.RawMessage `json:"value"`
			} `json:"datavalue"`
		} `json:"mainsnak"`
	} `json:"claims"`
}

func (a *App) wikiEntities(ctx context.Context, ids []string, props string) (map[string]wikiEntity, error) {
	var result struct {
		Entities map[string]wikiEntity `json:"entities"`
	}
	q := url.Values{"action": {"wbgetentities"}, "ids": {strings.Join(ids, "|")}, "props": {props}, "languages": {"zh-hans|zh|en"}, "format": {"json"}}
	err := publicRequest(a, ctx, "https://www.wikidata.org/w/api.php?"+q.Encode(), &result)
	return result.Entities, err
}
func entityClaims(e wikiEntity, property string) []string {
	out := []string{}
	for _, c := range e.Claims[property] {
		if c.Rank == "deprecated" {
			continue
		}
		var value struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(c.Main.Data.Value, &value) == nil && entityIDRE.MatchString(value.ID) {
			out = append(out, value.ID)
		}
	}
	return out
}
func entityLabel(e wikiEntity) string {
	for _, language := range []string{"zh-hans", "zh", "en"} {
		if v := e.Labels[language].Value; v != "" {
			return v
		}
	}
	return ""
}
func (a *App) fetchWiki(ctx context.Context, id string) (metadataValues, error) {
	v := metadataValues{}
	m := wikiIDRE.FindStringSubmatch(id)
	if m == nil {
		return v, errors.New("百科资料编号无效")
	}
	pages, err := a.wikiQuery(ctx, m[1], url.Values{"pageids": {m[2]}})
	if err != nil {
		return v, err
	}
	if len(pages) != 1 || pages[0].ID < 1 || pages[0].PageProps.Disambiguation != nil {
		return v, errors.New("没有找到对应的作品条目")
	}
	p := pages[0]
	pic := p.Thumbnail.Source
	if pic == "" {
		pic = p.Original.Source
	}
	v = metadataValues{Title: displayWikiTitle(p.Title), Overview: p.Extract, Poster: pic, URL: "https://" + m[1] + ".wikipedia.org/?curid=" + m[2], Note: "简介与图片来自维基百科，结构化资料来自 Wikidata；图片及文字请参见原条目许可。"}
	v.Details.Sources = []MetadataSource{{Name: "维基百科", URL: v.URL}}
	a.addWikidataDetails(ctx, p, &v)
	return v, nil
}
func publicArtURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" && u.Port() != "443" {
		return nil, errors.New("图片来源不支持")
	}
	allowed := false
	switch strings.ToLower(u.Hostname()) {
	case "upload.wikimedia.org", "thumb.wikimedia.org":
		allowed = strings.HasPrefix(u.Path, "/wikipedia/")
	case "static.tvmaze.com":
		allowed = strings.HasPrefix(u.Path, "/uploads/images/")
	case "image.tmdb.org":
		allowed = strings.HasPrefix(u.Path, "/t/p/")
	}
	if !allowed {
		return nil, errors.New("图片来源不支持")
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u, nil
}
