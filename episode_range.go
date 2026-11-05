package main

import (
	"regexp"
	"strconv"
)

var multiEpisodeRE = regexp.MustCompile(`(?i)S([0-9]{1,3})[ ._-]*E([0-9]{1,4})(?:[ ._]*-[ ._]*E?|[ ._]*E)([0-9]{1,4})(?:[^0-9]|$)`)
var chineseRangeRE = regexp.MustCompile(`第\s*([0-9零〇一二两三四五六七八九十百]+)\s*[-~～至]\s*([0-9零〇一二两三四五六七八九十百]+)\s*[集话話]`)
var epRangeRE = regexp.MustCompile(`(?i)(?:^|[ ._\-\[])EP?[ ._-]*([0-9]{1,4})[ ._]*-[ ._]*E?P?([0-9]{1,4})(?:[^0-9]|$)`)

func validEpisodeEnd(start, end int) bool {
	return end == 0 || end >= start && end <= 9999 && end-start < 100
}
func episodeLast(start, end int) int {
	if end >= start && validEpisodeEnd(start, end) {
		return end
	}
	return start
}
func episodeOverlap(start, end, otherStart, otherEnd int) bool {
	return start <= episodeLast(otherStart, otherEnd) && otherStart <= episodeLast(start, end)
}
func guessEpisodeRange(name string) (season, episode, end int) {
	season, episode = guessEpisode(name)
	if m := multiEpisodeRE.FindStringSubmatch(name); m != nil {
		season, _ = strconv.Atoi(m[1])
		episode, _ = strconv.Atoi(m[2])
		end, _ = strconv.Atoi(m[3])
	} else if m := chineseRangeRE.FindStringSubmatch(name); m != nil {
		episode = chineseNumber(m[1])
		end = chineseNumber(m[2])
	} else if m := epRangeRE.FindStringSubmatch(name); m != nil {
		episode, _ = strconv.Atoi(m[1])
		end, _ = strconv.Atoi(m[2])
	}
	if end == episode {
		end = 0
	}
	if !validEpisodeEnd(episode, end) {
		end = 0
	}
	return
}
func fileEpisodeEnd(f MediaFile) int {
	if f.EpisodeEnd > 0 {
		return f.EpisodeEnd
	}
	season, start, end := guessEpisodeRange(f.Name)
	if start == f.Episode && (season == 0 || season == f.Season) {
		return end
	}
	return 0
}
