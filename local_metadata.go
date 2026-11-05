package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

type localNFO struct {
	Title         string   `xml:"title"`
	ShowTitle     string   `xml:"showtitle"`
	OriginalTitle string   `xml:"originaltitle"`
	Year          string   `xml:"year"`
	Plot          string   `xml:"plot"`
	Rating        string   `xml:"rating"`
	Genres        []string `xml:"genre"`
	Actors        []struct {
		Name  string `xml:"name"`
		Role  string `xml:"role"`
		Thumb string `xml:"thumb"`
	} `xml:"actor"`
	Premiered    string   `xml:"premiered"`
	ReleaseDate  string   `xml:"releasedate"`
	EndDate      string   `xml:"enddate"`
	Tagline      string   `xml:"tagline"`
	MPAA         string   `xml:"mpaa"`
	Runtime      string   `xml:"runtime"`
	Countries    []string `xml:"country"`
	Languages    []string `xml:"language"`
	Studios      []string `xml:"studio"`
	Directors    []string `xml:"director"`
	Writers      []string `xml:"writer"`
	Credits      []string `xml:"credits"`
	Tags         []string `xml:"tag"`
	Status       string   `xml:"status"`
	SeasonNumber *int     `xml:"seasonnumber"`
	IDs          []struct {
		Type  string `xml:"type,attr"`
		Value string `xml:",chardata"`
	} `xml:"uniqueid"`
	TMDBID string `xml:"tmdbid"`
	Thumb  string `xml:"thumb"`
	Fanart struct {
		Thumb string `xml:"thumb"`
	} `xml:"fanart"`
	Aired string `xml:"aired"`
}
type localMetadata struct {
	NFO      *localNFO
	Poster   string
	Backdrop string
	Episodes []Episode
	Details  MetadataDetails
}

func (m *localMetadata) apply(it *Item) {
	if n := m.NFO; n != nil {
		for _, f := range []struct {
			key     string
			present bool
		}{{"title", n.Title != ""}, {"originalTitle", n.OriginalTitle != ""}, {"overview", n.Plot != ""}, {"rating", n.Rating != ""}, {"genres", len(n.Genres) > 0}, {"cast", len(n.Actors) > 0}} {
			lockLocal(it, f.key, f.present)
		}
		if n.Title != "" {
			it.Title = n.Title
		}
		if n.OriginalTitle != "" {
			it.OriginalTitle = n.OriginalTitle
		}
		if year, err := strconv.Atoi(strings.TrimSpace(n.Year)); err == nil && year >= 1800 && year <= 2200 {
			it.Year = strconv.Itoa(year)
			lockLocal(it, "year", true)
		}
		if n.Plot != "" {
			it.Overview = n.Plot
		}
		if rating, e := strconv.ParseFloat(n.Rating, 64); e == nil && rating >= 0 && rating <= 10 {
			it.Rating = rating
		}
		if len(n.Genres) > 0 {
			it.Genres = n.Genres
		}
		if len(n.Actors) > 0 {
			it.Cast = []string{}
			for _, actor := range n.Actors {
				if actor.Name != "" {
					it.Cast = append(it.Cast, actor.Name)
				}
			}
		}
		tmdbID := n.TMDBID
		for _, id := range n.IDs {
			if strings.EqualFold(id.Type, "tmdb") {
				tmdbID = id.Value
				break
			}
		}
		if id, e := strconv.Atoi(strings.TrimSpace(tmdbID)); e == nil && id > 0 {
			it.TMDBID = id
		}
		d := mergeDetails(m.Details, nfoDetails(n))
		it.Details = mergeDetails(d, it.Details)
		for _, f := range []struct {
			key     string
			present bool
		}{{"tagline", d.Tagline != ""}, {"premiereDate", d.PremiereDate != ""}, {"endDate", d.EndDate != ""}, {"contentRating", d.ContentRating != ""}, {"runtime", d.Runtime > 0}, {"countries", len(d.Countries) > 0}, {"languages", len(d.Languages) > 0}, {"studios", len(d.Studios) > 0}, {"directors", len(d.Directors) > 0}, {"writers", len(d.Writers) > 0}, {"tags", len(d.Tags) > 0}} {
			lockLocal(it, f.key, f.present)
		}
		if it.Year == "" && d.PremiereDate != "" {
			it.Year = yearOf(d.PremiereDate)
			lockLocal(it, "year", true)
		}
	}
	it.Details = mergeDetails(m.Details, it.Details)
	if m.Poster != "" {
		it.Poster = m.Poster
	}
	if m.Backdrop != "" {
		it.Backdrop = m.Backdrop
	}
	for _, ep := range m.Episodes {
		replaced := false
		for i := range it.Episodes {
			if it.Episodes[i].Season == ep.Season && it.Episodes[i].Number == ep.Number {
				it.Episodes[i] = ep
				replaced = true
				break
			}
		}
		if !replaced {
			it.Episodes = append(it.Episodes, ep)
		}
	}
	it.MetadataProvider = "local"
	it.MetadataID = ""
	it.MetadataURL = ""
	it.MetadataStatus = "ready"
	it.MetadataError = ""
	it.MetadataNote = "已读取原目录的 NFO 或海报，原文件保持不变。"
}
func readNFO(s Source, key string) *localNFO {
	p, err := localPath(s, key)
	if err != nil {
		return nil
	}
	st, err := os.Lstat(p)
	if err != nil || !st.Mode().IsRegular() || st.Size() > 2<<20 {
		return nil
	}
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	var n localNFO
	if xml.NewDecoder(io.LimitReader(f, 2<<20)).Decode(&n) != nil {
		return nil
	}
	return &n
}
func (a *App) localArt(s Source, key string) string {
	p, err := localPath(s, key)
	if err != nil {
		return ""
	}
	st, err := os.Lstat(p)
	if err != nil || !st.Mode().IsRegular() || st.Size() > 15<<20 {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	ext := ""
	switch http.DetectContentType(b) {
	case "image/jpeg":
		ext = ".jpg"
	case "image/png":
		ext = ".png"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		ext = ".webp"
	default:
		return ""
	}
	hash := sha256.Sum256(b)
	name := "local-" + hex.EncodeToString(hash[:]) + ext
	target := filepath.Join(a.dir, "art", name)
	if _, err = os.Stat(target); err != nil {
		if atomicWrite(target, b, 0644) != nil {
			return ""
		}
	}
	return "local:" + name
}
func (a *App) nfoArt(s Source, dir, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if strings.HasPrefix(ref, "https://") {
		if _, err := publicArtURL(ref); err == nil {
			return ref
		}
		return ""
	}
	ref = strings.ReplaceAll(ref, "\\", "/")
	if filepath.IsAbs(ref) {
		root, _ := filepath.Abs(s.Root)
		if !within(root, filepath.FromSlash(ref)) {
			return ""
		}
		relative, err := filepath.Rel(root, filepath.FromSlash(ref))
		if err != nil {
			return ""
		}
		return a.localArt(s, "/"+filepath.ToSlash(relative))
	}
	// NFO image references may use a parent directory, but the resolved path must stay inside the selected source.
	joined := path.Join(dir, ref)
	if strings.HasPrefix(joined, "../") || joined == ".." {
		return ""
	}
	return a.localArt(s, joined)
}
func (a *App) readLocalMetadata(s Source, rows []PreviewRow, kind string) *localMetadata {
	info := &localMetadata{Episodes: []Episode{}}
	found := false
	imageDirs := []string{}
	seasonDirs := map[string]bool{}
	for _, row := range rows {
		if row.Duplicate {
			continue
		}
		dir := path.Dir(row.Entry.Key)
		imageDirs = append(imageDirs, dir)
		if kind == "tv" {
			imageDirs = append(imageDirs, path.Dir(dir))
			if !seasonDirs[dir] {
				seasonDirs[dir] = true
				if n := readNFO(s, path.Join(dir, "season.nfo")); n != nil {
					number := row.Season
					if n.SeasonNumber != nil {
						number = *n.SeasonNumber
					}
					d := nfoDetails(n)
					info.Details.Seasons = mergeSeasons(info.Details.Seasons, []Season{{Number: number, Year: n.Year, Name: n.Title, PremiereDate: d.PremiereDate, EndDate: d.EndDate, Overview: n.Plot, Poster: a.nfoArt(s, dir, n.Thumb)}})
					found = true
				}
			}
		}
		candidates := []string{strings.TrimSuffix(row.Entry.Key, path.Ext(row.Entry.Key)) + ".nfo", path.Join(dir, "movie.nfo")}
		if kind == "tv" {
			candidates = []string{path.Join(path.Dir(dir), "tvshow.nfo"), path.Join(dir, "tvshow.nfo")}
		}
		if info.NFO == nil {
			for _, key := range candidates {
				if n := readNFO(s, key); n != nil {
					info.NFO = n
					found = true
					nd := path.Dir(key)
					info.Details = mergeDetails(a.localNFODetails(s, n, nd), info.Details)
					info.Poster = a.nfoArt(s, nd, n.Thumb)
					info.Backdrop = a.nfoArt(s, nd, n.Fanart.Thumb)
					imageDirs = append([]string{nd}, imageDirs...)
					break
				}
			}
		}
		if kind == "tv" {
			key := strings.TrimSuffix(row.Entry.Key, path.Ext(row.Entry.Key)) + ".nfo"
			if n := readNFO(s, key); n != nil {
				runtime, _ := strconv.Atoi(n.Runtime)
				rating, _ := strconv.ParseFloat(n.Rating, 64)
				ep := Episode{Season: row.Season, Number: row.Episode, Name: n.Title, Overview: n.Plot, AirDate: n.Aired, Still: a.nfoArt(s, dir, n.Thumb), Runtime: runtime, Rating: rating, RatingSource: "本地 NFO"}
				if ep.Still == "" {
					ep.Still = a.localArt(s, strings.TrimSuffix(row.Entry.Key, path.Ext(row.Entry.Key))+".jpg")
				}
				info.Episodes = append(info.Episodes, ep)
				found = true
			}
		}
	}
	for _, dir := range imageDirs {
		if info.Poster == "" {
			for _, name := range []string{"poster.jpg", "folder.jpg", "poster.png", "folder.png", "poster.webp"} {
				if pic := a.localArt(s, path.Join(dir, name)); pic != "" {
					info.Poster = pic
					found = true
					break
				}
			}
		}
		if info.Backdrop == "" {
			for _, name := range []string{"fanart.jpg", "backdrop.jpg", "fanart.png", "backdrop.png"} {
				if pic := a.localArt(s, path.Join(dir, name)); pic != "" {
					info.Backdrop = pic
					found = true
					break
				}
			}
		}
	}
	if !found {
		return nil
	}
	info.Details.Seasons = mergeSeasons(info.Details.Seasons, seasonsFromEpisodes(info.Episodes))
	return info
}

func nfoDetails(n *localNFO) MetadataDetails {
	d := MetadataDetails{Tagline: n.Tagline, PremiereDate: n.Premiered, EndDate: n.EndDate, Status: n.Status, ContentRating: n.MPAA, Countries: n.Countries, Languages: n.Languages, Studios: n.Studios, Directors: n.Directors, Writers: append(append([]string{}, n.Writers...), n.Credits...), Tags: n.Tags, Sources: []MetadataSource{{Name: "本地 NFO"}}}
	if d.PremiereDate == "" {
		d.PremiereDate = n.ReleaseDate
	}
	d.Runtime, _ = strconv.Atoi(strings.TrimSpace(n.Runtime))
	if n.Rating != "" {
		d.RatingSource = "本地 NFO"
	}
	for _, id := range n.IDs {
		addExternalID(&d, strings.ToLower(id.Type), id.Value)
	}
	addExternalID(&d, "tmdb", n.TMDBID)
	for _, p := range n.Actors {
		d.People = append(d.People, Person{Name: p.Name, Role: p.Role, Type: "Actor"})
	}
	return d
}
func (a *App) localNFODetails(s Source, n *localNFO, dir string) MetadataDetails {
	d := nfoDetails(n)
	for i, p := range n.Actors {
		d.People[i].Image = a.nfoArt(s, dir, p.Thumb)
	}
	return d
}
