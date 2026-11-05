package main

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A token refers to an actual search result held by this scan, never client-supplied metadata.
type ScanMatchCandidate struct {
	Token        string `json:"token"`
	Type         string `json:"type"`
	Title        string `json:"title"`
	Year         string `json:"year"`
	Provider     string `json:"provider"`
	ProviderName string `json:"providerName"`
	ExternalID   string `json:"externalId"`
	Poster       string `json:"poster"`
	Overview     string `json:"overview"`
	URL          string `json:"url"`
}

type scanMatchRecord struct {
	GroupID   string
	Type      string
	Candidate ScanMatchCandidate
}

type scanMatchQuery struct {
	GroupID string `json:"groupId"`
	Type    string `json:"type"`
	Query   string `json:"query"`
	Year    string `json:"year"`
}

func scanCandidateURL(provider, id, kind string) string {
	if provider == "wikipedia" {
		if m := wikiIDRE.FindStringSubmatch(id); m != nil {
			return "https://" + m[1] + ".wikipedia.org/?curid=" + m[2]
		}
		return ""
	}
	n, err := strconv.Atoi(id)
	if err != nil || n < 1 {
		return ""
	}
	if provider == "tmdb" {
		return "https://www.themoviedb.org/" + kind + "/" + id
	}
	if provider == "tvmaze" && kind == "tv" {
		return "https://www.tvmaze.com/shows/" + id
	}
	return ""
}

func (a *App) searchScanMatches(ctx context.Context, id string, q scanMatchQuery) ([]ScanMatchCandidate, error) {
	s, err := a.scanSnapshot(id)
	if err != nil {
		return nil, err
	}
	if scanBusy(s.Status) || s.Status == "cancelled" {
		return nil, errors.New("请等待当前操作结束，再检索作品")
	}
	q.Query = strings.TrimSpace(q.Query)
	if q.GroupID == "" || len(q.GroupID) > 100 || len(q.Query) > 300 || q.Query == "" || q.Type != "movie" && q.Type != "tv" {
		return nil, errors.New("请填写作品名称，并选择电影或电视剧")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := a.searchMetadata(ctx, q.Type, q.Query)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return matchScore(rows[i], q.Query, q.Year) > matchScore(rows[j], q.Query, q.Year)
	})
	results := []ScanMatchCandidate{}
	seen := map[string]bool{}
	for _, row := range rows {
		value := func(key string) string { s, _ := row[key].(string); return s }
		provider, external := value("provider"), value("externalId")
		if provider == "wikipedia" {
			if hint, _ := row["mediaHint"].(bool); !hint {
				continue
			}
		}
		link := scanCandidateURL(provider, external, q.Type)
		title := strings.TrimSpace(value("title"))
		if link == "" || title == "" || len([]rune(title)) > 150 || seen[provider+":"+external] {
			continue
		}
		year := value("year")
		if n, e := strconv.Atoi(year); e != nil || n < 1800 || n > 2200 {
			year = ""
		}
		poster := value("poster")
		if strings.HasPrefix(poster, "/") {
			poster = "https://image.tmdb.org/t/p/w500" + poster
		}
		if poster != "" {
			if _, e := publicArtURL(poster); e != nil {
				poster = ""
			}
		}
		results = append(results, ScanMatchCandidate{Token: uid(), Type: q.Type, Title: title, Year: year, Provider: provider, ProviderName: value("providerName"), ExternalID: external, Poster: poster, Overview: value("overview"), URL: link})
		seen[provider+":"+external] = true
		if len(results) == 8 {
			break
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	live := a.scans[id]
	if live == nil || live.run != s.run || scanBusy(live.Status) || live.Status == "cancelled" {
		return nil, errors.New("扫描内容已变化，请重新检索")
	}
	if live.matches == nil {
		live.matches = map[string]scanMatchRecord{}
	}
	if len(live.matches)+len(results) > 16384 {
		return nil, errors.New("本次检索记录较多，请重新扫描目录")
	}
	for _, c := range results {
		live.matches[c.Token] = scanMatchRecord{GroupID: q.GroupID, Type: q.Type, Candidate: c}
	}
	return results, nil
}

func (a *App) scanMatchHandler(w http.ResponseWriter, r *http.Request) {
	var q scanMatchQuery
	if err := readJSON(w, r, &q); err != nil {
		fail(w, err)
		return
	}
	results, err := a.searchScanMatches(r.Context(), r.PathValue("id"), q)
	if err != nil {
		fail(w, err)
		return
	}
	jsonResponse(w, 200, map[string]any{"results": results})
}
