package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var numberParts = regexp.MustCompile(`[0-9]+|[^0-9]+`)
var seasonEpisodeRE = regexp.MustCompile(`(?i)S(\d{1,3})[ ._-]*E(\d{1,4})`)
var xEpisodeRE = regexp.MustCompile(`(?i)(\d{1,2})x(\d{1,3})(?:[^0-9]|$)`)
var chineseEpisodeRE = regexp.MustCompile(`第\s*([0-9零〇一二两三四五六七八九十百]+)\s*[集话話]`)
var chineseSeasonRE = regexp.MustCompile(`第\s*([0-9零〇一二两三四五六七八九十百]+)\s*季`)
var episodeRE = regexp.MustCompile(`(?i)(?:^|[ ._\-\[])E(?:P)?[ ._-]*(\d{1,4})(?:[^0-9]|$)`)
var plainEpisodeRE = regexp.MustCompile(`^\s*(\d{1,3})(?:[ ._\-集话話]|$)`)
var yearRE = regexp.MustCompile(`(?:19|20)\d{2}`)
var qualityRE = regexp.MustCompile(`(?i)\b(?:2160p|1080p|720p|480p|4k|bluray|blu-ray|web-dl|webrip|hdtv|x264|x265|h264|h265|hevc|aac|dts)\b.*`)

func naturalLess(a, b string) bool {
	aa, bb := numberParts.FindAllString(strings.ToLower(a), -1), numberParts.FindAllString(strings.ToLower(b), -1)
	for i := 0; i < len(aa) && i < len(bb); i++ {
		if aa[i] == bb[i] {
			continue
		}
		n, e1 := strconv.Atoi(aa[i])
		m, e2 := strconv.Atoi(bb[i])
		if e1 == nil && e2 == nil && n != m {
			return n < m
		}
		return aa[i] < bb[i]
	}
	return len(aa) < len(bb)
}
func chineseNumber(s string) int {
	if n, e := strconv.Atoi(s); e == nil {
		return n
	}
	digits := map[rune]int{'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	sum, last := 0, 0
	for _, r := range s {
		if r == '十' || r == '百' {
			if last == 0 {
				last = 1
			}
			if r == '十' {
				sum += last * 10
			} else {
				sum += last * 100
			}
			last = 0
		} else if d, ok := digits[r]; ok {
			last = d
		} else {
			return 0
		}
	}
	return sum + last
}
func guessEpisode(name string) (season, episode int) {
	if m := seasonEpisodeRE.FindStringSubmatch(name); m != nil {
		season, _ = strconv.Atoi(m[1])
		episode, _ = strconv.Atoi(m[2])
		return
	}
	if m := xEpisodeRE.FindStringSubmatch(name); m != nil {
		season, _ = strconv.Atoi(m[1])
		episode, _ = strconv.Atoi(m[2])
		return
	}
	if m := chineseSeasonRE.FindStringSubmatch(name); m != nil {
		season = chineseNumber(m[1])
	}
	if m := chineseEpisodeRE.FindStringSubmatch(name); m != nil {
		episode = chineseNumber(m[1])
		return
	}
	if m := episodeRE.FindStringSubmatch(name); m != nil {
		episode, _ = strconv.Atoi(m[1])
		return
	}
	if specialContentName(name) {
		season = 0
		if episode = specialEpisodeNumber(name); episode > 0 {
			return
		}
	}
	base := strings.TrimSuffix(name, filepath.Ext(name))
	if m := plainEpisodeRE.FindStringSubmatch(base); m != nil {
		episode, _ = strconv.Atoi(m[1])
	}
	return
}
func guessTitle(name string) map[string]any {
	t := name
	if mediaExt(name) {
		t = strings.TrimSuffix(name, filepath.Ext(name))
	}
	year := yearRE.FindString(t)
	idx := len(t)
	for _, re := range []*regexp.Regexp{seasonEpisodeRE, seasonNameRE, xEpisodeRE, chineseSeasonRE, chineseEpisodeRE, episodeRE, qualityRE, specialNameRE} {
		if loc := re.FindStringIndex(t); loc != nil && loc[0] < idx {
			idx = loc[0]
		}
	}
	if year != "" {
		if i := strings.Index(t, year); i < idx {
			idx = i
		}
	}
	t = t[:idx]
	t = strings.NewReplacer(".", " ", "_", " ", "[", " ", "]", " ", "(", " ", ")", " ").Replace(t)
	t = strings.Trim(t, " -")
	return map[string]any{"title": t, "year": year}
}

type Selection struct {
	Key        string `json:"key"`
	Season     *int   `json:"season,omitempty"`
	Episode    int    `json:"episode"`
	EpisodeEnd *int   `json:"episodeEnd,omitempty"`
}
type ImportRequest struct {
	ItemID           string      `json:"itemId"`
	NewItem          bool        `json:"newItem"`
	Mode             string      `json:"mode"`
	SourceID         string      `json:"sourceId"`
	Type             string      `json:"type"`
	Title            string      `json:"title"`
	Year             string      `json:"year"`
	TMDBID           int         `json:"tmdbId"`
	MetadataProvider string      `json:"metadataProvider"`
	MetadataID       string      `json:"metadataId"`
	SkipAutoMetadata bool        `json:"skipAutoMetadata,omitempty"`
	Season           int         `json:"season"`
	OutputRoot       string      `json:"outputRoot"`
	Files            []Selection `json:"files"`
	scanMatch        *ScanMatchCandidate
}
type PreviewRow struct {
	Entry       Entry  `json:"entry"`
	Season      int    `json:"season"`
	Episode     int    `json:"episode"`
	EpisodeEnd  int    `json:"episodeEnd,omitempty"`
	Destination string `json:"destination"`
	Issue       string `json:"issue,omitempty"`
	Duplicate   bool   `json:"duplicate"`
}
type Preview struct {
	Mode      string       `json:"mode"`
	Rows      []PreviewRow `json:"rows"`
	Root      string       `json:"root"`
	ItemID    string       `json:"itemId"`
	Total     int64        `json:"total"`
	Count     int          `json:"count"`
	ItemTitle string       `json:"itemTitle"`
	ItemYear  string       `json:"itemYear"`
	request   ImportRequest
	lookup    ImportRequest
}

func (a *App) preview(ctx context.Context, r ImportRequest) (Preview, error) {
	p := Preview{Rows: []PreviewRow{}}
	if r.Type != "movie" && r.Type != "tv" {
		return p, errors.New("请选择电影或电视剧")
	}
	r.Title = strings.TrimSpace(r.Title)
	if r.Type == "tv" {
		r.Title = seriesTitle(r.Title)
	}
	if r.Title == "" {
		return p, errors.New("请填写作品名称，或搜索并选择作品")
	}
	if len([]rune(r.Title)) > 150 {
		return p, errors.New("作品名称过长")
	}
	if r.Year != "" {
		year, err := strconv.Atoi(r.Year)
		if err != nil || year < 1800 || year > 2200 {
			return p, errors.New("年份需要是四位数字")
		}
	}
	if len(r.Files) == 0 || len(r.Files) > 1000 {
		return p, errors.New("请选择 1 至 1000 个视频文件")
	}
	if r.Type == "movie" && len(r.Files) != 1 {
		return p, errors.New("一次导入一部电影，请只选择一个电影视频文件")
	}
	if r.Season < 0 || r.Season > 999 {
		return p, errors.New("季数需要在 0 至 999 之间（0 为特别篇）")
	}
	s, err := a.source(r.SourceID)
	if err != nil {
		return p, err
	}
	if s.Type == "local" {
		p.Mode = r.Mode
		if p.Mode == "" {
			p.Mode = "reference"
		}
		if p.Mode != "reference" && p.Mode != "copy" {
			return p, errors.New("本地来源支持直接入库或复制整理")
		}
	} else {
		p.Mode = "download"
		if r.Mode != "" && r.Mode != "download" {
			return p, errors.New("网盘来源需要先下载到本地")
		}
	}
	a.mu.Lock()
	r.ItemID = a.canonicalItemIDLocked(r.ItemID)
	configRoot := a.s.Config.MediaRoot
	existingItems := append([]Item{}, a.s.Items...)
	existingFiles := append([]MediaFile{}, a.s.Files...)
	a.mu.Unlock()
	root := strings.TrimSpace(r.OutputRoot)
	if root == "" {
		root = configRoot
	}
	if p.Mode == "reference" {
		root = s.Root
	}
	if !filepath.IsAbs(root) {
		return p, errors.New("下载位置需要是本机绝对路径")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return p, err
	}
	p.Root = root
	lookup := r
	if s.Type == "local" && r.Type == "tv" && r.MetadataProvider == "" && r.TMDBID == 0 {
		key, keyErr := cleanKey(r.Files[0].Key)
		if keyErr != nil {
			return p, keyErr
		}
		dir := path.Dir(key)
		for _, candidate := range []string{path.Join(path.Dir(dir), "tvshow.nfo"), path.Join(dir, "tvshow.nfo")} {
			if n := readNFO(s, candidate); n != nil {
				probe := Item{Type: "tv", Title: r.Title, Year: r.Year}
				(&localMetadata{NFO: n}).apply(&probe)
				if title := seriesTitle(probe.Title); title != "" && len([]rune(title)) <= 150 {
					lookup.Title = title
				}
				if year, yearErr := strconv.Atoi(strings.TrimSpace(probe.Year)); yearErr == nil && year >= 1800 && year <= 2200 {
					lookup.Year = strconv.Itoa(year)
				}
				lookup.TMDBID = probe.TMDBID
				break
			}
		}
	}
	matched, err := importItem(existingItems, lookup)
	if err != nil {
		return p, err
	}
	if matched.ID != "" {
		p.ItemID = matched.ID
		r.Title, r.Year = matched.Title, matched.Year
		if r.Type == "tv" {
			r.Title = seriesTitle(r.Title)
		}
	} else if r.Type == "tv" {
		r.Title, r.Year = lookup.Title, lookup.Year
	}
	p.ItemTitle, p.ItemYear = r.Title, r.Year
	p.request, p.lookup = r, lookup
	folder := safeName(r.Title)
	if r.Year != "" {
		folder += " (" + safeName(r.Year) + ")"
	}
	if r.TMDBID > 0 {
		folder += fmt.Sprintf(" [tmdbid-%d]", r.TMDBID)
	}
	catalog := map[string][]Entry{}
	usedKeys := map[string]bool{}
	usedDest := map[string]bool{}
	usedEpisodes := map[string]bool{}
	for _, selection := range r.Files {
		key, err := cleanKey(selection.Key)
		if err != nil {
			return p, err
		}
		if usedKeys[key] {
			return p, errors.New("选择中包含重复文件")
		}
		usedKeys[key] = true
		parent := path.Dir(key)
		entries, ok := catalog[parent]
		if !ok {
			entries, err = a.list(ctx, s, parent)
			if err != nil {
				return p, err
			}
			catalog[parent] = entries
		}
		var entry Entry
		found := false
		for _, e := range entries {
			if e.Key == key && e.Media && !e.Dir {
				entry = e
				found = true
				break
			}
		}
		if !found {
			return p, fmt.Errorf("文件已不存在或不是支持的视频：%s", path.Base(key))
		}
		season, episode, episodeEnd := guessEpisodeRange(entry.Name)
		if parsed, ok := seasonFromName(entry.Name); ok {
			season = parsed
		} else if parsed, ok := seasonFromName(path.Base(parent)); ok {
			season = parsed
		} else {
			season = r.Season
		}
		if selection.Season != nil {
			season = *selection.Season
		}
		if selection.Episode > 0 {
			if selection.Episode != episode {
				episodeEnd = 0
			}
			episode = selection.Episode
		}
		if selection.EpisodeEnd != nil {
			episodeEnd = *selection.EpisodeEnd
		}
		row := PreviewRow{Entry: entry, Season: season, Episode: episode, EpisodeEnd: episodeEnd}
		ext := strings.ToLower(filepath.Ext(entry.Name))
		name := safeName(r.Title)
		if r.Year != "" {
			name += " (" + safeName(r.Year) + ")"
		}
		if r.Type == "tv" {
			if season < 0 || season > 999 || episode < 1 || episode > 9999 || !validEpisodeEnd(episode, episodeEnd) {
				row.Issue = "请确认季数和集号"
			}
			row.Destination = filepath.Join(root, folder, fmt.Sprintf("Season %02d", season), fmt.Sprintf("%s - S%02dE%02d%s", name, season, episode, ext))
			if episodeEnd > episode {
				row.Destination = filepath.Join(root, folder, fmt.Sprintf("Season %02d", season), fmt.Sprintf("%s - S%02dE%02d-E%02d%s", name, season, episode, episodeEnd, ext))
			}
		} else {
			row.Season = 0
			row.Episode = 0
			row.EpisodeEnd = 0
			row.Destination = filepath.Join(root, folder, name+ext)
		}
		if p.Mode == "reference" {
			row.Destination, err = localPath(s, key)
			if err != nil {
				return p, err
			}
		}
		for _, f := range existingFiles {
			if f.SourceID == s.ID && f.SourceKey == key {
				row.Duplicate = true
				row.Issue = "已添加，自动跳过"
				break
			}
			if f.ItemID == p.ItemID && f.Season == row.Season && episodeOverlap(f.Episode, fileEpisodeEnd(f), row.Episode, row.EpisodeEnd) {
				row.Issue = "媒体库已有这一集或重叠的合并集，请先核对"
			}
			if strings.EqualFold(f.Destination, row.Destination) {
				row.Issue = "目标位置已被其他任务使用"
			}
		}
		if !row.Duplicate {
			if r.Type == "tv" {
				for ep := row.Episode; ep <= episodeLast(row.Episode, row.EpisodeEnd); ep++ {
					episodeKey := fmt.Sprintf("%d:%d", row.Season, ep)
					if usedEpisodes[episodeKey] {
						row.Issue = "同一次导入有重复或重叠的季集号，请先核对"
					}
					usedEpisodes[episodeKey] = true
				}
			}
			if usedDest[strings.ToLower(row.Destination)] {
				row.Issue = "同一次导入有重复集号或保存位置"
			}
			usedDest[strings.ToLower(row.Destination)] = true
			if _, er := os.Lstat(row.Destination); er == nil && p.Mode != "reference" {
				row.Issue = "目标文件已存在，不会覆盖"
			}
			if row.Issue == "" {
				p.Count++
				p.Total += entry.Size
			}
		}
		p.Rows = append(p.Rows, row)
	}
	return p, nil
}
func (a *App) importMedia(ctx context.Context, r ImportRequest) (map[string]any, error) {
	p, err := a.preview(ctx, r)
	if err != nil {
		return nil, err
	}
	for _, row := range p.Rows {
		if row.Issue != "" && !row.Duplicate {
			return nil, errors.New(row.Entry.Name + "：" + row.Issue)
		}
	}
	if p.Count == 0 {
		return nil, errors.New("所选文件都已添加，请在媒体库或任务页查看")
	}
	r = p.request
	source, err := a.source(r.SourceID)
	if err != nil {
		return nil, err
	}
	var localInfo *localMetadata
	if source.Type == "local" {
		localInfo = a.readLocalMetadata(source, p.Rows, r.Type)
	}
	a.mu.Lock()
	sourceStillPresent := false
	for _, current := range a.s.Sources {
		if current.ID == source.ID && scanSourceIdentity(current) == scanSourceIdentity(source) {
			sourceStillPresent = true
			break
		}
	}
	if !sourceStillPresent {
		a.mu.Unlock()
		return nil, errors.New("来源已移除或修改，请重新选择来源")
	}
	matched, matchErr := importItem(a.s.Items, p.lookup)
	if matchErr != nil {
		a.mu.Unlock()
		return nil, matchErr
	}
	id := matched.ID
	newItem := id == ""
	if newItem {
		id = uid()
	}
	// Recheck after the network-bound preview: simultaneous submissions must not create duplicate tasks.
	for _, row := range p.Rows {
		if row.Duplicate {
			continue
		}
		for _, f := range a.s.Files {
			if (f.SourceID == r.SourceID && f.SourceKey == row.Entry.Key) || strings.EqualFold(f.Destination, row.Destination) {
				a.mu.Unlock()
				return nil, errors.New("文件已在另一个导入任务中，请刷新后重试")
			}
			if r.Type == "tv" && f.ItemID == id && f.Season == row.Season && episodeOverlap(f.Episode, fileEpisodeEnd(f), row.Episode, row.EpisodeEnd) {
				a.mu.Unlock()
				return nil, errors.New("媒体库已有这一集，请刷新预览后核对")
			}
		}
	}
	oldState := a.s
	a.s.Items = append([]Item{}, a.s.Items...)
	for i := range a.s.Items {
		a.s.Items[i].Episodes = append([]Episode{}, a.s.Items[i].Episodes...)
	}
	a.s.Files = append([]MediaFile{}, a.s.Files...)
	a.s.Tasks = append([]Task{}, a.s.Tasks...)
	if newItem {
		a.s.Items = append(a.s.Items, Item{ID: id, SeparateSeries: r.Type == "tv" && r.NewItem, Type: r.Type, Title: strings.TrimSpace(r.Title), Year: r.Year, TMDBID: r.TMDBID, MetadataProvider: r.MetadataProvider, MetadataID: r.MetadataID, MetadataStatus: "manual", Created: now(), Genres: []string{}, Cast: []string{}, Episodes: []Episode{}})
	}
	if localInfo != nil {
		for i := range a.s.Items {
			if a.s.Items[i].ID == id {
				it := &a.s.Items[i]
				if newItem || it.MetadataStatus != "ready" || it.MetadataProvider == "local" {
					localInfo.apply(it)
				} else {
					it.Episodes = mergeEpisodes(it.Episodes, localInfo.Episodes)
				}
				if it.Type == "tv" {
					if title := seriesTitle(it.Title); title != "" {
						it.Title = title
					} else {
						it.Title = r.Title
					}
				}
				break
			}
		}
	}
	if newItem && r.scanMatch != nil {
		c := r.scanMatch
		it := &a.s.Items[len(a.s.Items)-1]
		it.Title, it.Year = c.Title, c.Year
		it.MetadataProvider, it.MetadataID, it.MetadataURL = c.Provider, c.ExternalID, c.URL
		it.TMDBID = r.TMDBID
		it.MetadataStatus = "matched"
		if !strings.HasPrefix(it.Poster, "local:") {
			it.Poster = c.Poster
		}
		if it.Overview == "" {
			it.Overview = c.Overview
		}
	}
	added := []MediaFile{}
	for _, row := range p.Rows {
		if row.Duplicate {
			continue
		}
		fid := uid()
		status := "queued"
		if p.Mode == "reference" {
			status = "completed"
		}
		f := MediaFile{ID: fid, ItemID: id, SourceID: r.SourceID, SourceKey: row.Entry.Key, SourceRemoteID: row.Entry.RemoteID, Name: row.Entry.Name, Season: row.Season, Episode: row.Episode, EpisodeEnd: row.EpisodeEnd, Destination: row.Destination, Reference: p.Mode == "reference", Status: status, Size: row.Entry.Size, Subtitles: []Subtitle{}, Created: now()}
		a.s.Files = append(a.s.Files, f)
		added = append(added, f)
		if p.Mode != "reference" {
			a.s.Tasks = append(a.s.Tasks, Task{ID: uid(), FileID: fid, Status: "queued", Total: row.Entry.Size, SourceModified: row.Entry.Modified, Created: now()})
		}
	}
	if err = a.saveLocked(); err != nil {
		a.s = oldState
		a.mu.Unlock()
		return nil, err
	}
	automatic := a.s.Config.AutoMetadata
	a.mu.Unlock()
	if p.Mode == "reference" {
		for _, f := range added {
			a.copyLocalSubtitles(source, f)
		}
	}
	if r.MetadataProvider != "" && r.MetadataID != "" {
		go a.scrapeSelected(id, r.MetadataProvider, r.MetadataID)
	} else if r.TMDBID > 0 {
		go a.scrape(id, r.TMDBID)
	} else if automatic && !r.SkipAutoMetadata {
		if matched.MetadataID != "" && matched.MetadataProvider != "local" {
			go a.scrapeSelected(id, matched.MetadataProvider, matched.MetadataID)
		} else {
			go a.autoScrape(id)
		}
	}
	return map[string]any{"itemId": id, "count": p.Count, "skipped": len(p.Rows) - p.Count, "mode": p.Mode}, nil
}
