package yingku

import (
	"path/filepath"
	"regexp"
	"strings"
)

// Match labels used for extras without treating titles such as Special Agent as
// special-season labels. Explicit SxxExx numbering remains authoritative.
var specialNameRE = regexp.MustCompile(`(?i)(?:^|[ ._\-（(\[])(?:specials|extras?|bonus|sp)(?:[ ._\-）)\]]|[0-9]|$)|(?:^|[ ._\-（(\[])special(?:[ ._\-]*[0-9]|$)|(?:特别季|特別季|特别篇|特別篇|特别集|特別集|番外(?:篇)?|彩蛋|花絮)(?:[ ._\-（()）\[\]]|[0-9第零〇一二两三四五六七八九十百]|$)`)
var specialEpisodeRE = regexp.MustCompile(`(?i)(?:^|[ ._\-（(\[])(?:specials?|extras?|bonus|sp)[ ._\-]*(?:ep?[ ._\-]*)?([0-9]{1,4})(?:[ ._\-）)\]]|$)|(?:特别季|特別季|特别篇|特別篇|特别集|特別集|番外(?:篇)?|彩蛋|花絮)[ ._\-]*(?:第[ ._\-]*)?([0-9零〇一二两三四五六七八九十百]+)(?:[集话話 ._\-）)\]]|$)`)

func specialContentName(name string) bool {
	if mediaExt(name) {
		name = strings.TrimSuffix(name, filepath.Ext(name))
	}
	return specialNameRE.MatchString(name)
}

func specialEpisodeNumber(name string) int {
	if mediaExt(name) {
		name = strings.TrimSuffix(name, filepath.Ext(name))
	}
	if m := specialEpisodeRE.FindStringSubmatch(name); m != nil {
		number := chineseNumber(m[1] + m[2])
		if number > 0 && number <= 9999 {
			return number
		}
	}
	return 0
}
