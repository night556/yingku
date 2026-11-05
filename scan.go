package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ScannedFile struct {
	ID          int              `json:"id"`
	Key         string           `json:"key"`
	Relative    string           `json:"relative"`
	Entry       Entry            `json:"entry"`
	EpisodeHint *ScanEpisodeHint `json:"episodeHint,omitempty"`
}
type ScanEpisodeHint struct {
	Season     *int `json:"season,omitempty"`
	Episode    int  `json:"episode"`
	EpisodeEnd int  `json:"episodeEnd,omitempty"`
}

// Episode hints do not claim to identify the work. They remain useful when AI
// leaves a file unassigned and the user supplies the work identity themselves.
func scanEpisodeHint(f ScannedFile) *ScanEpisodeHint {
	_, episode, end := guessEpisodeRange(f.Entry.Name)
	if episode < 1 || episode > 9999 {
		episode, end = 0, 0
	}
	hint := &ScanEpisodeHint{Episode: episode, EpisodeEnd: end}
	season, found := seasonFromName(f.Entry.Name)
	if !found {
		folders := strings.Split(strings.Trim(path.Dir(f.Key), "/"), "/")
		for i := len(folders) - 1; i >= 0; i-- {
			if season, found = seasonFromName(folders[i]); found {
				break
			}
		}
	}
	if found && season >= 0 && season <= 999 {
		hint.Season = &season
	}
	return hint
}

type PlannedFile struct {
	ID         int    `json:"id"`
	Key        string `json:"key"`
	Name       string `json:"name"`
	Season     int    `json:"season"`
	Episode    int    `json:"episode"`
	EpisodeEnd int    `json:"episodeEnd,omitempty"`
	Selected   bool   `json:"selected"`
}
type ScanGroup struct {
	ID         string        `json:"id"`
	Type       string        `json:"type"`
	Title      string        `json:"title"`
	Year       string        `json:"year"`
	ItemID     string        `json:"itemId,omitempty"`
	MatchToken string        `json:"matchToken,omitempty"`
	Manual     bool          `json:"manual,omitempty"`
	Confidence float64       `json:"confidence"`
	Note       string        `json:"note,omitempty"`
	Selected   bool          `json:"selected"`
	Files      []PlannedFile `json:"files"`
}
type UnassignedFile struct {
	ID     int    `json:"id"`
	Key    string `json:"key"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}
type ScanPlan struct {
	Method     string           `json:"method"`
	Groups     []ScanGroup      `json:"groups"`
	Unassigned []UnassignedFile `json:"unassigned"`
}
type ScanImportResult struct {
	GroupID string `json:"groupId"`
	Title   string `json:"title"`
	ItemID  string `json:"itemId,omitempty"`
	Status  string `json:"status"`
	Count   int    `json:"count"`
	Skipped int    `json:"skipped"`
	Error   string `json:"error,omitempty"`
}
type ScanSession struct {
	ID             string             `json:"id"`
	SourceID       string             `json:"sourceId"`
	SourceType     string             `json:"sourceType"`
	Path           string             `json:"path"`
	Folder         string             `json:"folder"`
	Status         string             `json:"status"`
	Folders        int                `json:"folders"`
	VideoCount     int                `json:"videoCount"`
	Processed      int                `json:"processed"`
	Tokens         int                `json:"tokens"`
	Model          string             `json:"model,omitempty"`
	AIInstructions string             `json:"aiInstructions,omitempty"`
	Error          string             `json:"error,omitempty"`
	Files          []ScannedFile      `json:"files"`
	Plan           ScanPlan           `json:"plan"`
	Previews       []ScanGroupPreview `json:"previews"`
	Results        []ScanImportResult `json:"results"`
	Created        string             `json:"created"`
	sourceIdentity string
	matches        map[string]scanMatchRecord
	cancel         context.CancelFunc
	run            string
}
type ScanGroupPreview struct {
	GroupID string  `json:"groupId"`
	Title   string  `json:"title"`
	Preview Preview `json:"preview"`
	Error   string  `json:"error,omitempty"`
}

func scanBusy(status string) bool {
	return status == "scanning" || status == "recognizing" || status == "checking" || status == "importing"
}
func scanSourceIdentity(s Source) string {
	return s.Type + "\x00" + s.Root + "\x00" + s.URL + "\x00" + s.Username
}
func (a *App) scanSnapshot(id string) (ScanSession, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.scans[id]
	if s == nil {
		return ScanSession{}, errors.New("扫描记录已过期，请重新扫描")
	}
	b, _ := json.Marshal(s)
	var copy ScanSession
	_ = json.Unmarshal(b, &copy)
	for i := range copy.Files {
		copy.Files[i].EpisodeHint = scanEpisodeHint(copy.Files[i])
	}
	copy.sourceIdentity = s.sourceIdentity
	copy.run = s.run
	copy.matches = make(map[string]scanMatchRecord, len(s.matches))
	for token, match := range s.matches {
		copy.matches[token] = match
	}
	return copy, nil
}
func (a *App) updateScan(id, run string, fn func(*ScanSession)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s := a.scans[id]; s != nil && s.run == run && s.Status != "cancelled" {
		fn(s)
	}
}

func (a *App) startScan(sourceID, key, method string) (string, error) {
	source, err := a.source(sourceID)
	if err != nil {
		return "", err
	}
	key, err = cleanKey(key)
	if err != nil {
		return "", err
	}
	if method != "local" && method != "ai" {
		return "", errors.New("请选择本地识别或 AI 识别")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if method == "ai" && !a.s.Config.AI.Enabled {
		return "", errors.New("请先在设置中启用并保存 AI 配置")
	}
	if a.scans == nil {
		a.scans = map[string]*ScanSession{}
	}
	for id, s := range a.scans {
		created, _ := time.Parse(time.RFC3339, s.Created)
		if !scanBusy(s.Status) && time.Since(created) > time.Hour {
			delete(a.scans, id)
		}
	}
	if len(a.scans) >= 8 {
		oldestID, oldest := "", ""
		for id, s := range a.scans {
			if !scanBusy(s.Status) && (oldest == "" || s.Created < oldest) {
				oldestID, oldest = id, s.Created
			}
		}
		if oldestID != "" {
			delete(a.scans, oldestID)
		}
	}
	if len(a.scans) >= 8 {
		return "", errors.New("扫描记录较多，请关闭旧扫描或稍后再试")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	id, run := uid(), uid()
	folder := path.Base(key)
	if key == "/" {
		cleanRoot := strings.TrimRight(strings.ReplaceAll(source.Root, "\\", "/"), "/")
		folder = path.Base(cleanRoot)
		if _, season := seasonFromName(folder); season && (seriesTitle(folder) == "" || genericFolder(folder)) {
			folder = path.Base(path.Dir(cleanRoot)) + "/" + folder
		}
		if folder == "." || folder == "/" || folder == "" {
			folder = "所选目录"
		}
		if source.Type == "quark" {
			folder = source.Name
		}
	}
	a.scans[id] = &ScanSession{ID: id, SourceID: sourceID, SourceType: source.Type, Path: key, Folder: folder, Status: "scanning", Files: []ScannedFile{}, Plan: ScanPlan{Groups: []ScanGroup{}, Unassigned: []UnassignedFile{}}, Previews: []ScanGroupPreview{}, Results: []ScanImportResult{}, Created: now(), cancel: cancel, run: run, sourceIdentity: scanSourceIdentity(source)}
	go a.runScan(ctx, id, run, source, key, method, cancel)
	return id, nil
}
func (a *App) runScan(ctx context.Context, id, run string, source Source, root, method string, cancel context.CancelFunc) {
	defer cancel()
	files := []ScannedFile{}
	folders := 0
	seen := map[string]bool{}
	var walk func(string, int) error
	walk = func(key string, depth int) error {
		if ctx.Err() != nil {
			return errors.New("扫描已取消或超时")
		}
		if depth > 20 {
			return errors.New("目录层级超过 20 层，请选择更小的影视目录")
		}
		if seen[key] {
			return nil
		}
		seen[key] = true
		folders++
		if folders > 1200 {
			return errors.New("子目录过多，请缩小扫描范围")
		}
		entries, err := a.list(ctx, source, key)
		if err != nil {
			return fmt.Errorf("读取目录 %s 失败：%w", key, err)
		}
		sort.SliceStable(entries, func(i, j int) bool { return naturalLess(entries[i].Key, entries[j].Key) })
		for _, entry := range entries {
			clean, err := cleanKey(entry.Key)
			if err != nil {
				return err
			}
			if clean == root || !(root == "/" || strings.HasPrefix(clean, strings.TrimRight(root, "/")+"/")) {
				return errors.New("来源返回了超出扫描范围的路径")
			}
			if entry.Dir {
				if strings.HasPrefix(entry.Name, ".") || entry.Name == "@eaDir" || strings.EqualFold(entry.Name, "$RECYCLE.BIN") {
					continue
				}
				if err = walk(clean, depth+1); err != nil {
					return err
				}
			} else if entry.Media {
				if len(files) >= 1000 {
					return errors.New("视频超过 1000 个，请按分类或剧集分批扫描")
				}
				entry.Key = clean
				files = append(files, ScannedFile{ID: len(files) + 1, Key: clean, Relative: manifestRelative(root, clean), Entry: entry})
			}
		}
		a.updateScan(id, run, func(s *ScanSession) { s.Folders = folders; s.VideoCount = len(files) })
		return nil
	}
	if err := walk(root, 0); err != nil {
		a.updateScan(id, run, func(s *ScanSession) { s.Status = "failed"; s.Error = err.Error() })
		return
	}
	snapshot, _ := a.scanSnapshot(id)
	plan := localScanPlan(snapshot.Folder, files)
	a.updateScan(id, run, func(s *ScanSession) {
		s.Files = files
		s.Plan = plan
		s.Status = "ready"
		s.Folders = folders
		s.VideoCount = len(files)
		s.Processed = len(files)
		s.cancel = nil
		if len(files) == 0 {
			s.Error = "所选目录中没有支持的视频文件"
		}
	})
	if method == "ai" && len(files) > 0 {
		aiCtx, aiCancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer aiCancel()
		a.runAIRecognition(aiCtx, id, run, files, aiCancel)
	}
}

func genericFolder(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", ".", "movies", "movie", "tv", "shows", "tv shows", "series", "videos", "video", "media", "电影", "电视剧", "剧集", "影视", "视频", "下载", "downloads", "special", "specials", "extras", "bonus", "sp", "特别季", "特別季", "特别篇", "特別篇", "特别集", "特別集", "番外", "番外篇", "彩蛋", "花絮":
		return true
	}
	return false
}
func localFileGuess(root string, f ScannedFile) (string, string, string, int, int, int, float64) {
	season, episode, end := guessEpisodeRange(f.Entry.Name)
	parts := strings.Split(strings.Trim(f.Key, "/"), "/")
	folders := append(strings.Split(strings.Trim(root, "/"), "/"), parts[:len(parts)-1]...)
	parent := path.Base(path.Dir(f.Key))
	folderSeason, hasFolderSeason := seasonFromName(parent)
	_, hasFileSeason := seasonFromName(f.Entry.Name)
	if !hasFileSeason && hasFolderSeason {
		season = folderSeason
	}
	kind := "movie"
	if episode > 0 || hasFileSeason || hasFolderSeason {
		kind = "tv"
	}
	title, year := "", ""
	confidence := 0.95
	if kind == "tv" {
		g := guessTitle(f.Entry.Name)
		title, _ = g["title"].(string)
		year, _ = g["year"].(string)
		if !hasFileSeason && !hasFolderSeason {
			season = 1
		}
		if title == "" || plainEpisodeRE.MatchString(strings.TrimSuffix(f.Entry.Name, path.Ext(f.Entry.Name))) || strings.EqualFold(title, "E") || strings.EqualFold(title, "EP") {
			title = ""
			for i := len(folders) - 1; i >= 0; i-- {
				folder := folders[i]
				if genericFolder(folder) {
					continue
				}
				if _, isSeason := seasonFromName(folder); isSeason && seriesTitle(folder) == "" {
					continue
				}
				g = guessTitle(folder)
				candidate, _ := g["title"].(string)
				if candidate != "" && !genericFolder(candidate) {
					title = candidate
					year, _ = g["year"].(string)
					break
				}
			}
		}
		if title == "" {
			confidence = 0.3
		}
		if episode == 0 {
			confidence = 0.3
		}
	} else {
		g := guessTitle(f.Entry.Name)
		title, _ = g["title"].(string)
		year, _ = g["year"].(string)
		if title == "" || len([]rune(title)) < 2 || plainEpisodeRE.MatchString(strings.TrimSuffix(f.Entry.Name, path.Ext(f.Entry.Name))) {
			title = ""
			confidence = 0.3
		}
	}
	if kind == "tv" {
		title = seriesTitle(title)
	}
	return kind, title, year, season, episode, end, confidence
}
func localScanPlan(root string, files []ScannedFile) ScanPlan {
	plan := ScanPlan{Method: "local", Groups: []ScanGroup{}, Unassigned: []UnassignedFile{}}
	for _, f := range files {
		kind, title, year, season, episode, end, confidence := localFileGuess(root, f)
		if title == "" || (kind == "tv" && episode == 0) {
			reason := "文件名或目录不足以确定作品与集号，可使用 AI 或手动归类"
			if kind == "tv" && season == 0 && episode == 0 {
				reason = "已识别为特别篇或附加内容，缺少明确集号；可批量归类到原剧的特别篇（季数 0），再填写集号"
			}
			plan.Unassigned = append(plan.Unassigned, UnassignedFile{ID: f.ID, Key: f.Key, Name: f.Relative, Reason: reason})
			continue
		}
		plan.Groups = append(plan.Groups, ScanGroup{ID: uid(), Type: kind, Title: title, Year: year, Confidence: confidence, Selected: true, Note: "根据目录和文件名识别", Files: []PlannedFile{{ID: f.ID, Key: f.Key, Name: f.Entry.Name, Season: season, Episode: episode, EpisodeEnd: end, Selected: true}}})
	}
	plan.Groups = mergeScanGroups(plan.Groups)
	return plan
}
func mergeScanGroups(groups []ScanGroup) []ScanGroup {
	out := []ScanGroup{}
	indexes := map[string]int{}
	for _, g := range groups {
		// Initialize each batch before appending it to an existing show.
		// Otherwise only the first batch's files receive their checked state.
		for i := range g.Files {
			g.Files[i].Selected = true
		}
		if g.Type == "tv" {
			key := comparableTitle(seriesTitle(g.Title)) + ":" + g.Year
			if i, ok := indexes[key]; ok {
				out[i].Files = append(out[i].Files, g.Files...)
				if g.Confidence < out[i].Confidence {
					out[i].Confidence = g.Confidence
				}
				out[i].Selected = out[i].Confidence >= 0.75
				continue
			}
			indexes[key] = len(out)
		}
		g.Selected = g.Confidence >= 0.75
		out = append(out, g)
	}
	for i := range out {
		if out[i].Type == "tv" {
			sort.SliceStable(out[i].Files, func(left, right int) bool {
				a, b := out[i].Files[left], out[i].Files[right]
				if a.Season != b.Season {
					return a.Season < b.Season
				}
				if a.Episode != b.Episode {
					return a.Episode < b.Episode
				}
				return naturalLess(a.Key, b.Key)
			})
		}
	}
	return out
}
func (a *App) runAIRecognition(ctx context.Context, id, run string, files []ScannedFile, cancel context.CancelFunc, instructions ...string) {
	a.mu.Lock()
	config := a.s.Config.AI
	s := a.scans[id]
	if s == nil || s.run != run || s.Status == "cancelled" {
		a.mu.Unlock()
		return
	}
	folder := s.Folder
	s.Status = "recognizing"
	s.Processed = 0
	s.Error = ""
	s.Model = config.Model
	s.cancel = cancel
	a.mu.Unlock()
	if !config.Enabled {
		a.updateScan(id, run, func(s *ScanSession) {
			s.Status = "ready"
			s.Error = "AI 配置未启用，已保留本地识别结果"
			s.cancel = nil
		})
		return
	}
	plan, err := a.recognizeWithAI(ctx, config, folder, files, func(done int, usage aiUsage) {
		a.updateScan(id, run, func(s *ScanSession) { s.Processed = done; s.Tokens += usage.Total })
	}, instructions...)
	a.updateScan(id, run, func(s *ScanSession) {
		s.Status = "ready"
		s.cancel = nil
		if err != nil {
			s.Error = err.Error() + "；已保留原识别结果，可以修改或重试"
		} else {
			s.Plan = plan
			s.matches = nil
			s.Error = ""
			s.Previews = []ScanGroupPreview{}
		}
	})
}
func (a *App) reidentifyScan(id, method string, instructions ...string) error {
	note, err := normalizeAIInstructions(strings.Join(instructions, "\n"))
	if err != nil {
		return err
	}
	if method != "ai" && note != "" {
		return errors.New("补充说明仅用于 AI 识别")
	}
	a.mu.Lock()
	s := a.scans[id]
	if s == nil {
		a.mu.Unlock()
		return errors.New("扫描记录不存在")
	}
	if scanBusy(s.Status) {
		a.mu.Unlock()
		return errors.New("当前任务仍在进行")
	}
	if len(s.Files) == 0 {
		a.mu.Unlock()
		return errors.New("请先完成目录扫描")
	}
	if method != "ai" && method != "local" {
		a.mu.Unlock()
		return errors.New("未知识别方式")
	}
	if method == "ai" && !a.s.Config.AI.Enabled {
		a.mu.Unlock()
		return errors.New("请先启用 AI 配置")
	}
	s.run = uid()
	if method == "ai" {
		s.AIInstructions = note
	}
	run := s.run
	files := append([]ScannedFile{}, s.Files...)
	root := s.Folder
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	s.cancel = cancel
	s.Status = "recognizing"
	s.Error = ""
	a.mu.Unlock()
	go func() {
		defer cancel()
		if method == "ai" {
			a.runAIRecognition(ctx, id, run, files, cancel, note)
		} else {
			plan := localScanPlan(root, files)
			a.updateScan(id, run, func(s *ScanSession) {
				s.Plan = plan
				s.matches = nil
				s.Status = "ready"
				s.cancel = nil
				s.Previews = []ScanGroupPreview{}
			})
		}
	}()
	return nil
}

type ScanImportRequest struct {
	Mode       string      `json:"mode"`
	OutputRoot string      `json:"outputRoot"`
	Groups     []ScanGroup `json:"groups"`
}

func scanImportBody(sourceID string, g ScanGroup, r ScanImportRequest) ImportRequest {
	files := []Selection{}
	for _, f := range g.Files {
		if f.Selected {
			season := f.Season
			end := f.EpisodeEnd
			files = append(files, Selection{Key: f.Key, Season: &season, Episode: f.Episode, EpisodeEnd: &end})
		}
	}
	return ImportRequest{SourceID: sourceID, ItemID: g.ItemID, Type: g.Type, Title: g.Title, Year: g.Year, Mode: r.Mode, OutputRoot: r.OutputRoot, Files: files, SkipAutoMetadata: g.Manual}
}

func resolvedScanImport(s ScanSession, g ScanGroup, r ScanImportRequest) (ImportRequest, error) {
	body := scanImportBody(s.SourceID, g, r)
	if g.MatchToken != "" {
		match, ok := s.matches[g.MatchToken]
		if !ok || match.GroupID != g.ID || match.Type != g.Type {
			return body, errors.New("作品匹配已失效，请重新检索并确认")
		}
		c := match.Candidate
		if g.Manual || g.Title != c.Title || g.Year != c.Year {
			return body, errors.New("名称、年份或类型已修改，请重新确认匹配")
		}
		body.MetadataProvider, body.MetadataID = c.Provider, c.ExternalID
		body.scanMatch = &c
		if c.Provider == "tmdb" {
			body.TMDBID, _ = strconv.Atoi(c.ExternalID)
		}
	} else if !g.Manual {
		return body, errors.New("请确认一个在线候选，或选择“暂不匹配，按当前名称入库”")
	}
	return body, nil
}
func validateScanEdits(s ScanSession, r ScanImportRequest) ([]ScanGroup, error) {
	allowed := map[int]ScannedFile{}
	for _, f := range s.Files {
		allowed[f.ID] = f
	}
	seen := map[int]bool{}
	ids := map[string]bool{}
	groups := []ScanGroup{}
	if len(r.Groups) > 1000 {
		return nil, errors.New("作品分组过多")
	}
	for _, g := range r.Groups {
		if !g.Selected {
			continue
		}
		if g.ID == "" || ids[g.ID] {
			return nil, errors.New("作品分组编号重复或为空")
		}
		ids[g.ID] = true
		selected := []PlannedFile{}
		for _, f := range g.Files {
			if !f.Selected {
				continue
			}
			original, ok := allowed[f.ID]
			if !ok || original.Key != f.Key || seen[f.ID] {
				return nil, errors.New("选择中包含不属于本次扫描或重复的视频")
			}
			seen[f.ID] = true
			f.Name = original.Entry.Name
			selected = append(selected, f)
		}
		if len(selected) == 0 {
			continue
		}
		g.Files = selected
		groups = append(groups, g)
	}
	if len(groups) == 0 {
		return nil, errors.New("请至少选择一部作品的视频")
	}
	return groups, nil
}
func (a *App) checkScanPlan(ctx context.Context, s ScanSession, r ScanImportRequest) ([]ScanGroupPreview, error) {
	source, sourceErr := a.source(s.SourceID)
	if sourceErr != nil {
		return nil, sourceErr
	}
	if s.sourceIdentity != "" && s.sourceIdentity != scanSourceIdentity(source) {
		return nil, errors.New("来源目录或地址已更改，请重新扫描后再入库")
	}
	groups, err := validateScanEdits(s, r)
	if err != nil {
		return nil, err
	}
	previews := []ScanGroupPreview{}
	destinations := map[string]bool{}
	episodes := map[string]bool{}
	for _, g := range groups {
		body, e := resolvedScanImport(s, g, r)
		if e != nil {
			previews = append(previews, ScanGroupPreview{GroupID: g.ID, Title: g.Title, Error: e.Error()})
			continue
		}
		p, e := a.preview(ctx, body)
		record := ScanGroupPreview{GroupID: g.ID, Title: g.Title, Preview: p}
		if e != nil {
			record.Error = e.Error()
			previews = append(previews, record)
			continue
		}
		identity := g.Type + ":" + comparableTitle(p.ItemTitle) + ":" + p.ItemYear
		if p.ItemID != "" {
			identity = p.ItemID
		}
		for i := range p.Rows {
			row := &p.Rows[i]
			if row.Duplicate {
				continue
			}
			target := strings.ToLower(row.Destination)
			if destinations[target] {
				row.Issue = "多个分组使用了同一保存位置"
			}
			destinations[target] = true
			if g.Type == "tv" {
				for ep := row.Episode; ep <= episodeLast(row.Episode, row.EpisodeEnd); ep++ {
					key := fmt.Sprintf("%s:%d:%d", identity, row.Season, ep)
					if episodes[key] {
						row.Issue = "不同分组中有重复或重叠的季集号"
					}
					episodes[key] = true
				}
			}
		}
		record.Preview = p
		previews = append(previews, record)
	}
	return previews, nil
}
func planHasIssues(previews []ScanGroupPreview) bool {
	for _, p := range previews {
		if p.Error != "" {
			return true
		}
		for _, row := range p.Preview.Rows {
			if row.Issue != "" && !row.Duplicate {
				return true
			}
		}
	}
	return false
}
func (a *App) startScanImport(id string, r ScanImportRequest) error {
	s, err := a.scanSnapshot(id)
	if err != nil {
		return err
	}
	if scanBusy(s.Status) {
		return errors.New("当前扫描仍在进行")
	}
	groups, err := validateScanEdits(s, r)
	if err != nil {
		return err
	}
	r.Groups = groups
	a.mu.Lock()
	live := a.scans[id]
	if live == nil || scanBusy(live.Status) {
		a.mu.Unlock()
		return errors.New("任务状态已变化，请刷新")
	}
	run := uid()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	live.run = run
	live.Status = "checking"
	live.cancel = cancel
	live.Error = ""
	live.Plan.Groups = r.Groups
	a.mu.Unlock()
	go func() {
		defer cancel()
		previews, e := a.checkScanPlan(ctx, s, r)
		if e != nil || planHasIssues(previews) {
			a.updateScan(id, run, func(s *ScanSession) {
				s.Status = "ready"
				s.Previews = previews
				s.cancel = nil
				if e != nil {
					s.Error = e.Error()
				} else {
					s.Error = "有文件需要核对，请修改或取消勾选后再入库"
				}
			})
			return
		}
		a.updateScan(id, run, func(s *ScanSession) { s.Status = "importing"; s.Previews = previews; s.Results = []ScanImportResult{} })
		for i, g := range r.Groups {
			if ctx.Err() != nil {
				a.updateScan(id, run, func(s *ScanSession) {
					s.Status = "ready"
					s.Error = "入库操作已取消或超时；已完成的分组会保留，可再次检查后继续"
					s.cancel = nil
				})
				return
			}
			result := ScanImportResult{GroupID: g.ID, Title: g.Title}
			p := previews[i].Preview
			if p.Count == 0 {
				result.Status = "skipped"
				result.Skipped = len(p.Rows)
			} else {
				body, e := resolvedScanImport(s, g, r)
				var out map[string]any
				if e == nil {
					out, e = a.importMedia(ctx, body)
				}
				if e != nil {
					result.Status = "failed"
					result.Error = e.Error()
				} else {
					result.Status = "completed"
					result.Count, _ = out["count"].(int)
					result.Skipped, _ = out["skipped"].(int)
					result.ItemID, _ = out["itemId"].(string)
				}
			}
			a.updateScan(id, run, func(s *ScanSession) { s.Results = append(s.Results, result) })
		}
		a.updateScan(id, run, func(s *ScanSession) { s.Status = "complete"; s.cancel = nil })
	}()
	return nil
}

func (a *App) scanRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/scans/{id}/matches", a.scanMatchHandler)
	mux.HandleFunc("POST /api/config/ai", a.aiConfigHandler)
	mux.HandleFunc("POST /api/config/ai/test", a.aiTestHandler)
	mux.HandleFunc("POST /api/scans", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			SourceID string `json:"sourceId"`
			Path     string `json:"path"`
			Method   string `json:"method"`
		}
		if e := readJSON(w, r, &body); e != nil {
			fail(w, e)
			return
		}
		if body.Method == "" {
			body.Method = "local"
		}
		id, e := a.startScan(body.SourceID, body.Path, body.Method)
		if e != nil {
			fail(w, e)
			return
		}
		jsonResponse(w, 202, map[string]any{"id": id})
	})
	mux.HandleFunc("GET /api/scans/{id}", func(w http.ResponseWriter, r *http.Request) {
		s, e := a.scanSnapshot(r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		jsonResponse(w, 200, s)
	})
	mux.HandleFunc("POST /api/scans/{id}/recognize", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Method       string `json:"method"`
			Instructions string `json:"instructions"`
		}
		if e := readJSON(w, r, &body); e != nil {
			fail(w, e)
			return
		}
		if e := a.reidentifyScan(r.PathValue("id"), body.Method, body.Instructions); e != nil {
			fail(w, e)
			return
		}
		jsonResponse(w, 202, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/scans/{id}/preview", func(w http.ResponseWriter, r *http.Request) {
		var body ScanImportRequest
		if e := readJSON(w, r, &body); e != nil {
			fail(w, e)
			return
		}
		s, e := a.scanSnapshot(r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		if scanBusy(s.Status) {
			fail(w, errors.New("请等待当前扫描或识别结束"))
			return
		}
		previews, e := a.checkScanPlan(r.Context(), s, body)
		if e != nil {
			fail(w, e)
			return
		}
		jsonResponse(w, 200, map[string]any{"previews": previews, "valid": !planHasIssues(previews)})
	})
	mux.HandleFunc("POST /api/scans/{id}/import", func(w http.ResponseWriter, r *http.Request) {
		var body ScanImportRequest
		if e := readJSON(w, r, &body); e != nil {
			fail(w, e)
			return
		}
		if e := a.startScanImport(r.PathValue("id"), body); e != nil {
			fail(w, e)
			return
		}
		jsonResponse(w, 202, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/scans/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		s := a.scans[r.PathValue("id")]
		if s == nil {
			fail(w, errors.New("扫描记录不存在"))
			return
		}
		if s.cancel != nil {
			s.cancel()
		}
		s.Status = "cancelled"
		s.cancel = nil
		jsonResponse(w, 200, map[string]any{"ok": true})
	})
}
