package yingku

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var seasonNameRE = regexp.MustCompile(`(?i)(?:^|[ ._\-（(\[])(?:season[ ._-]*([0-9]{1,3})|s([0-9]{1,3}))(?:[ ._\-）)\]]|$)`)
var seasonSuffixRE = regexp.MustCompile(`(?i)[ ._\-（(\[]*(?:第\s*[0-9零〇一二两三四五六七八九十百]+\s*季|season[ ._-]*[0-9]{1,3}|s[0-9]{1,3})[ ._\-）)\]]*$`)

// The series name and first-air year identify the show; a season is a child of it.
func seriesTitle(title string) string {
	return strings.TrimSpace(seasonSuffixRE.ReplaceAllString(strings.TrimSpace(title), ""))
}

func seasonFromName(name string) (int, bool) {
	if m := seasonEpisodeRE.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n, true
	}
	if m := xEpisodeRE.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n, true
	}
	if m := chineseSeasonRE.FindStringSubmatch(name); m != nil {
		return chineseNumber(m[1]), true
	}
	if m := seasonNameRE.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[1] + m[2])
		return n, true
	}
	if specialContentName(name) {
		return 0, true
	}
	return 0, false
}

func seriesIDs(it Item) map[string]string {
	ids := map[string]string{}
	if it.TMDBID > 0 {
		ids["tmdb"] = strconv.Itoa(it.TMDBID)
	}
	id := strings.TrimSpace(it.MetadataID)
	switch it.MetadataProvider {
	case "tmdb", "tvmaze":
		if n, err := strconv.Atoi(id); err == nil && n > 0 && ids[it.MetadataProvider] == "" {
			ids[it.MetadataProvider] = strconv.Itoa(n)
		}
	case "wikipedia":
		if wikiIDRE.MatchString(id) {
			// Language is part of Wikipedia's page-id namespace.
			language, page, _ := strings.Cut(id, ":")
			ids["wikipedia:"+language] = page
		}
	}
	for provider, value := range it.Details.ExternalIDs {
		var d MetadataDetails
		addExternalID(&d, provider, value)
		if valid := d.ExternalIDs[provider]; valid != "" && ids[provider] == "" {
			ids[provider] = valid
		}
	}
	return ids
}

func seriesIDRelation(a, b Item) (same, conflict bool) {
	aa, bb := seriesIDs(a), seriesIDs(b)
	for provider, id := range aa {
		if other := bb[provider]; other != "" {
			if other == id {
				same = true
			} else {
				conflict = true
			}
		}
	}
	return
}

func sameSeriesName(a, b Item) bool {
	for _, left := range []string{a.Title, a.OriginalTitle} {
		key := comparableTitle(seriesTitle(left))
		if key == "" {
			continue
		}
		for _, right := range []string{b.Title, b.OriginalTitle} {
			if key == comparableTitle(seriesTitle(right)) {
				return true
			}
		}
	}
	return false
}

func seriesCandidatesAmbiguous(items []Item) bool {
	for i, a := range items {
		for _, b := range items[i+1:] {
			_, conflict := seriesIDRelation(a, b)
			if conflict || a.Year != "" && b.Year != "" && a.Year != b.Year {
				return true
			}
		}
	}
	return false
}

// An undated item must not bridge two different remakes or conflicting provider IDs.
func ambiguousUndatedSeries(items []Item, probe Item) bool {
	group := []Item{probe}
	for _, it := range items {
		_, conflict := seriesIDRelation(it, probe)
		if it.ID != probe.ID && it.Type == "tv" && sameSeriesName(it, probe) && !conflict {
			group = append(group, it)
		}
	}
	return seriesCandidatesAmbiguous(group)
}

func seriesQuality(it Item) int {
	n := 0
	if it.MetadataStatus == "ready" {
		n += 40
	}
	if len(seriesIDs(it)) > 0 {
		n += 25
	}
	if it.MetadataProvider != "" && it.MetadataProvider != "local" {
		n += 10
	}
	if it.Year != "" {
		n += 8
	}
	if it.Overview != "" {
		n += 4
	}
	if it.Poster != "" {
		n += 4
	}
	return n
}

func betterSeries(a, b Item) bool {
	if seriesQuality(a) != seriesQuality(b) {
		return seriesQuality(a) > seriesQuality(b)
	}
	if a.Created != b.Created {
		return a.Created < b.Created
	}
	return a.ID < b.ID
}

func findSeries(items []Item, probe Item) (Item, bool, bool) {
	strong, fallback := []Item{}, []Item{}
	context := append(append([]Item{}, items...), probe)
	for _, it := range items {
		if it.Type != "tv" || it.ID == probe.ID && probe.ID != "" {
			continue
		}
		same, conflict := seriesIDRelation(it, probe)
		if conflict {
			continue
		}
		if same {
			strong = append(strong, it)
			continue
		}
		if !sameSeriesName(it, probe) || it.Year != "" && probe.Year != "" && it.Year != probe.Year {
			continue
		}
		if it.Year == "" && ambiguousUndatedSeries(context, it) {
			continue
		}
		fallback = append(fallback, it)
	}
	candidates := strong
	for i, left := range strong {
		for _, right := range strong[i+1:] {
			if _, conflict := seriesIDRelation(left, right); conflict {
				return Item{}, false, true
			}
		}
	}
	if len(candidates) == 0 {
		candidates = fallback
		if probe.Year == "" && ambiguousUndatedSeries(context, probe) || seriesCandidatesAmbiguous(candidates) {
			return Item{}, false, true
		}
	}
	if len(candidates) == 0 {
		return Item{}, false, false
	}
	best := candidates[0]
	if len(candidates) > 1 {
		for _, it := range candidates {
			if it.SeparateSeries {
				return Item{}, false, true
			}
		}
	}
	for _, it := range candidates[1:] {
		if betterSeries(it, best) {
			best = it
		}
	}
	return best, true, false
}

func importItem(items []Item, r ImportRequest) (Item, error) {
	probe := Item{Type: r.Type, Title: r.Title, Year: r.Year, TMDBID: r.TMDBID, MetadataProvider: r.MetadataProvider, MetadataID: r.MetadataID}
	if r.ItemID != "" {
		for _, it := range items {
			if it.ID == r.ItemID && it.Type == "tv" && r.Type == "tv" {
				if _, conflict := seriesIDRelation(it, probe); conflict {
					return Item{}, errors.New("所选资料与已有剧集的编号不同，请核对所属剧集")
				}
				return it, nil
			}
		}
		return Item{}, errors.New("所选剧集已不存在，请重新选择")
	}
	if r.Type == "tv" {
		if r.NewItem {
			return Item{}, nil
		}
		it, found, ambiguous := findSeries(items, probe)
		if ambiguous {
			return Item{}, errors.New("存在同名但年份或资料编号不同的剧集，请选择“所属剧集”，或新建独立剧集")
		}
		if found {
			return it, nil
		}
		return Item{}, nil
	}
	for _, it := range items {
		if it.Type == r.Type && ((r.MetadataID != "" && it.MetadataProvider == r.MetadataProvider && it.MetadataID == r.MetadataID) || (r.TMDBID > 0 && it.TMDBID == r.TMDBID) || (it.TMDBID == 0 && strings.EqualFold(it.Title, r.Title) && it.Year == r.Year)) {
			return it, nil
		}
	}
	return Item{}, nil
}

func mergeEpisodes(primary, extra []Episode) []Episode {
	result := append([]Episode{}, primary...)
	for _, ep := range extra {
		found := false
		for i := range result {
			v := &result[i]
			if v.Season != ep.Season || v.Number != ep.Number {
				continue
			}
			if v.Name == "" {
				v.Name = ep.Name
			}
			if v.Overview == "" {
				v.Overview = ep.Overview
			}
			if v.Still == "" {
				v.Still = ep.Still
			}
			if v.AirDate == "" {
				v.AirDate = ep.AirDate
			}
			if v.Runtime == 0 {
				v.Runtime = ep.Runtime
			}
			if v.Rating == 0 {
				v.Rating = ep.Rating
				v.RatingSource = ep.RatingSource
			}
			found = true
			break
		}
		if !found {
			result = append(result, ep)
		}
	}
	return result
}

func mergeSeriesValues(dst *Item, src Item) {
	dst.Title = seriesTitle(dst.Title)
	for _, pair := range [][2]*string{{&dst.Year, &src.Year}, {&dst.OriginalTitle, &src.OriginalTitle}, {&dst.Overview, &src.Overview}, {&dst.Poster, &src.Poster}, {&dst.Backdrop, &src.Backdrop}} {
		if *pair[0] == "" {
			*pair[0] = *pair[1]
		}
	}
	if dst.TMDBID == 0 {
		dst.TMDBID = src.TMDBID
	}
	if dst.Rating == 0 {
		dst.Rating = src.Rating
	}
	if len(dst.Genres) == 0 {
		dst.Genres = append([]string{}, src.Genres...)
	}
	if len(dst.Cast) == 0 {
		dst.Cast = append([]string{}, src.Cast...)
	}
	dst.Episodes = mergeEpisodes(dst.Episodes, src.Episodes)
	dst.Details = mergeDetails(dst.Details, src.Details)
	if len(src.SkipSeasons) > 0 {
		seasons := make(map[int]SeasonSkipSegments, len(dst.SkipSeasons)+len(src.SkipSeasons))
		maps.Copy(seasons, dst.SkipSeasons)
		for number, incoming := range src.SkipSeasons {
			marks := seasons[number]
			if marks.Intro == nil {
				marks.Intro = incoming.Intro
			}
			if marks.OutroFromEnd == nil {
				marks.OutroFromEnd = incoming.OutroFromEnd
			}
			seasons[number] = marks
		}
		dst.SkipSeasons = seasons
	}
	for _, field := range src.LocalFields {
		lockLocal(dst, field, true)
	}
	if src.Created != "" && (dst.Created == "" || src.Created < dst.Created) {
		dst.Created = src.Created
	}
}

func (a *App) canonicalItemIDLocked(id string) string {
	for count := 0; count <= len(a.s.ItemAliases); count++ {
		next := a.s.ItemAliases[id]
		if next == "" {
			return id
		}
		id = next
	}
	return id
}

// Only catalogue parents change. File IDs, paths, tasks, subtitles and progress stay intact.
func (a *App) reconcileSeriesLocked() int {
	merged := 0
	for {
		changed := false
		for _, probe := range a.s.Items {
			if probe.Type != "tv" || probe.SeparateSeries {
				continue
			}
			candidates := []Item{}
			for _, it := range a.s.Items {
				if !it.SeparateSeries {
					candidates = append(candidates, it)
				}
			}
			target, found, _ := findSeries(candidates, probe)
			if !found {
				continue
			}
			if betterSeries(probe, target) {
				probe, target = target, probe
			}
			for i := range a.s.Items {
				if a.s.Items[i].ID == target.ID {
					mergeSeriesValues(&a.s.Items[i], probe)
				}
			}
			for i := range a.s.Files {
				if a.s.Files[i].ItemID == probe.ID {
					a.s.Files[i].ItemID = target.ID
				}
			}
			if a.s.ItemAliases == nil {
				a.s.ItemAliases = map[string]string{}
			}
			for id, to := range a.s.ItemAliases {
				if to == probe.ID {
					a.s.ItemAliases[id] = target.ID
				}
			}
			a.s.ItemAliases[probe.ID] = target.ID
			delete(a.metadataJobs, probe.ID)
			for i := range a.s.Items {
				if a.s.Items[i].ID == probe.ID {
					a.s.Items = append(a.s.Items[:i:i], a.s.Items[i+1:]...)
					break
				}
			}
			merged++
			changed = true
			break
		}
		if !changed {
			return merged
		}
	}
}

func (a *App) seriesBackupLocked() error {
	backup := filepath.Join(a.dir, "state-before-series-v3.json")
	if st, err := os.Stat(backup); err == nil {
		if !st.Mode().IsRegular() || st.Size() == 0 {
			return errors.New("剧集合并备份路径不是有效的备份文件")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	b, err := os.ReadFile(filepath.Join(a.dir, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = atomicWrite(backup, b, 0600); err != nil {
		return fmt.Errorf("保存剧集合并前备份失败：%w", err)
	}
	return nil
}

func (a *App) regroupSeriesLocked() error {
	// Reconcile on a copy so a failed backup leaves the current catalogue unchanged.
	before := a.s
	jobs := a.metadataJobs
	a.s.Items = append([]Item{}, a.s.Items...)
	a.s.Files = append([]MediaFile{}, a.s.Files...)
	a.s.ItemAliases = map[string]string{}
	for k, v := range before.ItemAliases {
		a.s.ItemAliases[k] = v
	}
	a.metadataJobs = map[string]string{}
	for k, v := range jobs {
		a.metadataJobs[k] = v
	}
	if a.reconcileSeriesLocked() > 0 {
		if err := a.seriesBackupLocked(); err != nil {
			a.s, a.metadataJobs = before, jobs
			return err
		}
	}
	return nil
}
