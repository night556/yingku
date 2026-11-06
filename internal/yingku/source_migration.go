package yingku

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type sourceMoveRule struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type sourceMoveInput struct {
	Root        string            `json:"root"`
	Name        string            `json:"name"`
	Directories []sourceMoveRule  `json:"directories"`
	Files       map[string]string `json:"files"`
}

type sourceMoveFile struct {
	id, key, destination, status string
	size                         int64
}

type sourceMoveRow struct {
	ID      string `json:"id"`
	Old     string `json:"old"`
	New     string `json:"new"`
	Problem string `json:"problem,omitempty"`

	target string
	info   os.FileInfo
}

type sourceMovePlan struct {
	before, after Source
	files         []sourceMoveFile
	rows          []sourceMoveRow
	root          os.FileInfo
	token         string
	expires       time.Time
}

func inspectMoveRoot(source Source) (os.FileInfo, error) {
	dir, err := localPath(source, "/")
	if err != nil {
		return nil, errors.New("新来源目录无法读取，请检查路径和挂载")
	}
	d, err := os.Open(dir)
	if err != nil {
		return nil, errors.New("新来源目录无法读取，请检查路径和权限")
	}
	defer d.Close()
	info, err := d.Stat()
	if err != nil || !info.IsDir() {
		return nil, errors.New("新路径需要是可浏览的目录")
	}
	if _, err = d.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) {
		return nil, errors.New("新来源目录无法读取，请检查权限")
	}
	return info, nil
}

func localReferenceFiles(files []MediaFile, sourceID string) []sourceMoveFile {
	var refs []sourceMoveFile
	for _, f := range files {
		if f.SourceID == sourceID && f.Reference {
			refs = append(refs, sourceMoveFile{f.ID, f.SourceKey, f.Destination, f.Status, f.Size})
		}
	}
	return refs
}

func moveRelativePath(value string) (string, error) {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	if strings.HasPrefix(value, "/") || strings.ContainsRune(value, ':') {
		return "", errors.New("请输入来源目录下的相对路径")
	}
	return cleanKey(value)
}

func prepareMoveRules(input []sourceMoveRule) ([]sourceMoveRule, error) {
	rules := make([]sourceMoveRule, 0, len(input))
	seen := map[string]bool{}
	for _, rule := range input {
		from, err := moveRelativePath(rule.From)
		if err != nil || from == "/" || seen[pathKey(from)] {
			return nil, errors.New("请填写不同的原子目录，不要填写整个来源或上级目录")
		}
		to, err := moveRelativePath(rule.To)
		if err != nil {
			return nil, err
		}
		seen[pathKey(from)] = true
		rules = append(rules, sourceMoveRule{From: from, To: to})
	}
	return rules, nil
}

func movedKey(key string, rules []sourceMoveRule) string {
	best, result := 0, key
	for _, rule := range rules {
		depth := strings.Count(rule.From, "/") + 1
		if depth <= best {
			continue
		}
		parts := strings.SplitN(key, "/", depth+1)
		if len(parts) == depth+1 && pathKey(strings.Join(parts[:depth], "/")) == pathKey(rule.From) {
			best, result = depth, path.Join(rule.To, parts[depth])
		}
	}
	return result
}

func inspectMoveTarget(source Source, key string, size int64) (string, os.FileInfo, error) {
	if !mediaExt(key) {
		return "", nil, errors.New("新路径需要指向视频文件")
	}
	target, err := localPath(source, key)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil, errors.New("新位置找不到文件")
	}
	if err != nil {
		return "", nil, errors.New("新路径无法读取或位于来源目录之外")
	}
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() {
		return "", nil, errors.New("新路径需要指向普通视频文件")
	}
	if size > 0 && info.Size() != size {
		return "", nil, errors.New("文件大小与原记录不同，请核对视频版本")
	}
	f, err := os.Open(target)
	if err != nil {
		return "", nil, errors.New("新文件无法读取")
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || opened.Size() != info.Size() {
		return "", nil, errors.New("检查期间文件已变化，请重试")
	}
	return target, opened, nil
}

// Called with a.mu held. Copied and downloaded videos keep their own paths.
// An unfinished local copy still needs the old source to resume.
func (a *App) sourceMoveBlockedLocked(sourceID string, refs []sourceMoveFile) string {
	if a.closing {
		return "映库正在停止，请稍后重试"
	}
	for _, scan := range a.scans {
		if scan.SourceID == sourceID && scanBusy(scan.Status) {
			return "此来源正在扫描或入库，请完成或取消后再迁移"
		}
	}
	sourceFiles := map[string]bool{}
	for _, f := range a.s.Files {
		if f.SourceID == sourceID {
			sourceFiles[f.ID] = true
		}
	}
	for _, task := range a.s.Tasks {
		if sourceFiles[task.FileID] && (a.active[task.ID] != nil || task.Status != "completed") {
			return "此来源还有未完成的复制任务，请完成或移除任务后再迁移"
		}
	}
	affected := map[string]bool{}
	for _, f := range refs {
		affected[f.id] = true
		if f.status != "completed" {
			return "此来源还有未完成的视频记录，请处理后再迁移"
		}
		if a.streamReaders[f.id] > 0 {
			return "有视频正在读取，请关闭受影响的视频播放器后再迁移"
		}
	}
	for token, active := range a.webPlayback {
		if time.Now().After(active.expires) {
			delete(a.webPlayback, token)
		} else if affected[active.fileID] {
			return "有视频正在网页播放，请关闭受影响的视频播放器后再迁移"
		}
	}
	for _, cast := range a.casts {
		if !cast.ended && cast.Status != "STOPPED" && time.Since(cast.Created) < 24*time.Hour &&
			(affected[cast.FileID] || cast.nextGrant != nil && affected[cast.nextGrant.fileID] ||
				cast.pendingGrant != nil && affected[cast.pendingGrant.fileID]) {
			return "有视频正在投屏或已排入投屏队列，请先停止投屏"
		}
	}
	return ""
}

func moveConflicts(sourceID string, files []MediaFile, rows []sourceMoveRow) map[string]string {
	affected := make(map[string]bool, len(rows))
	for _, row := range rows {
		affected[row.ID] = true
	}
	paths, keys := map[string]string{}, map[string]string{}
	for _, f := range files {
		if affected[f.ID] {
			continue
		}
		if f.Destination != "" {
			paths[pathKey(f.Destination)] = f.ID
		}
		if f.SourceID == sourceID {
			keys[pathKey(f.SourceKey)] = f.ID
		}
	}
	problems := map[string]string{}
	for _, row := range rows {
		if row.Problem != "" {
			continue
		}
		p, k := pathKey(row.target), pathKey(row.New)
		if id := paths[p]; id != "" {
			problems[row.ID] = "新路径已关联另一条媒体记录"
			if affected[id] {
				problems[id] = problems[row.ID]
			}
		}
		if id := keys[k]; id != "" {
			problems[row.ID] = "新相对路径与此来源中的另一条记录冲突"
			if affected[id] {
				problems[id] = problems[row.ID]
			}
		}
		paths[p], keys[k] = row.ID, row.ID
	}
	return problems
}

func (a *App) previewSourceMove(ctx context.Context, id string, input sourceMoveInput) (map[string]any, error) {
	source, err := a.source(id)
	if err != nil || source.Type != "local" {
		return nil, errors.New("请选择要迁移的本地来源")
	}
	root, name := strings.TrimSpace(input.Root), strings.TrimSpace(input.Name)
	if !filepath.IsAbs(root) || len([]rune(name)) < 1 || len([]rune(name)) > 80 {
		return nil, errors.New("请填写来源名称和服务器上的新目录完整路径")
	}
	root = filepath.Clean(root)
	rules, err := prepareMoveRules(input.Directories)
	if err != nil {
		return nil, err
	}
	next := source
	next.Name, next.Root = name, root
	rootInfo, err := inspectMoveRoot(next)
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	files := localReferenceFiles(a.s.Files, id)
	delete(a.sourceMoves, id)
	a.mu.Unlock()

	rows := make([]sourceMoveRow, 0, len(files))
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := sourceMoveRow{ID: f.id, Old: f.key}
		key, err := cleanKey(f.key)
		if err != nil || key == "/" {
			row.Old = f.destination
			key = "/"
		}
		row.New = movedKey(key, rules)
		if override, ok := input.Files[f.id]; ok && strings.TrimSpace(override) != "" {
			row.New, err = moveRelativePath(override)
			if err != nil {
				row.Problem = err.Error()
			}
		}
		if row.Problem == "" {
			row.target, row.info, err = inspectMoveTarget(next, row.New, f.size)
			if err != nil {
				row.Problem = err.Error()
			}
		}
		rows = append(rows, row)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if !slices.Contains(a.s.Sources, source) || !slices.Equal(localReferenceFiles(a.s.Files, id), files) {
		return nil, errors.New("检查期间来源或视频记录已变化，请重新预览")
	}
	conflicts := moveConflicts(id, a.s.Files, rows)
	problemCount := 0
	for i := range rows {
		if issue := conflicts[rows[i].ID]; issue != "" {
			rows[i].Problem = issue
		}
		if rows[i].Problem != "" {
			problemCount++
		}
	}
	blocked := a.sourceMoveBlockedLocked(id, files)
	token := ""
	if problemCount == 0 && blocked == "" {
		token = uid()
		if a.sourceMoves == nil {
			a.sourceMoves = map[string]*sourceMovePlan{}
		}
		for id, plan := range a.sourceMoves {
			if time.Now().After(plan.expires) {
				delete(a.sourceMoves, id)
			}
		}
		a.sourceMoves[id] = &sourceMovePlan{
			before: source, after: next, files: files, rows: rows, root: rootInfo, token: token,
			expires: time.Now().Add(10 * time.Minute),
		}
	}
	return map[string]any{"oldRoot": source.Root, "newRoot": root, "rows": rows, "problems": problemCount, "blocked": blocked, "token": token}, nil
}

func (a *App) commitSourceMove(ctx context.Context, id, token string) (int, error) {
	a.mu.Lock()
	plan := a.sourceMoves[id]
	a.mu.Unlock()
	if plan == nil || token == "" || plan.token != token || time.Now().After(plan.expires) {
		return 0, errors.New("迁移预览已过期，请重新检查")
	}
	if root, err := inspectMoveRoot(plan.after); err != nil || !os.SameFile(plan.root, root) {
		return 0, errors.New("新来源目录已变化，请重新预览")
	}
	for i, row := range plan.rows {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		p, info, err := inspectMoveTarget(plan.after, row.New, plan.files[i].size)
		if err != nil || pathKey(p) != pathKey(row.target) || !os.SameFile(row.info, info) ||
			info.Size() != row.info.Size() || !info.ModTime().Equal(row.info.ModTime()) {
			return 0, fmt.Errorf("新位置已变化，请重新检查 %s", row.New)
		}
	}
	if !a.dlnaMu.TryLock() {
		return 0, errors.New("电视投屏状态正在更新，请稍后重试")
	}
	defer a.dlnaMu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	index := slices.Index(a.s.Sources, plan.before)
	if a.sourceMoves[id] != plan || time.Now().After(plan.expires) || index < 0 ||
		!slices.Equal(localReferenceFiles(a.s.Files, id), plan.files) ||
		len(moveConflicts(id, a.s.Files, plan.rows)) > 0 {
		return 0, errors.New("来源或视频记录已变化，请重新预览")
	}
	if blocked := a.sourceMoveBlockedLocked(id, plan.files); blocked != "" {
		return 0, errors.New(blocked)
	}

	before := a.s
	a.s.Sources = slices.Clone(a.s.Sources)
	a.s.Files = slices.Clone(a.s.Files)
	a.s.Sources[index] = plan.after
	targets := make(map[string]sourceMoveRow, len(plan.rows))
	for _, row := range plan.rows {
		targets[row.ID] = row
	}
	for i := range a.s.Files {
		f := &a.s.Files[i]
		if row, ok := targets[f.ID]; ok {
			f.SourceKey, f.Destination = row.New, row.target
			f.Name, f.Size = path.Base(row.New), row.info.Size()
		}
	}
	if err := a.saveLocked(); err != nil {
		a.s = before
		return 0, err
	}
	delete(a.sourceMoves, id)
	for scanID, scan := range a.scans {
		if scan.SourceID == id {
			delete(a.scans, scanID)
		}
	}
	for _, row := range plan.rows {
		delete(a.fileHealth, row.ID)
	}
	return len(plan.rows), nil
}

func (a *App) sourceMoveRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/sources/{id}/migration/preview", func(w http.ResponseWriter, r *http.Request) {
		var input sourceMoveInput
		if err := readJSON(w, r, &input); err != nil {
			fail(w, err)
			return
		}
		if !a.sourceMoveMu.TryLock() {
			fail(w, errors.New("正在检查另一个来源的迁移，请稍后再试"))
			return
		}
		defer a.sourceMoveMu.Unlock()
		result, err := a.previewSourceMove(r.Context(), r.PathValue("id"), input)
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, result)
	})
	mux.HandleFunc("POST /api/sources/{id}/migration/commit", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Token string `json:"token"`
		}
		if err := readJSON(w, r, &input); err != nil {
			fail(w, err)
			return
		}
		if !a.sourceMoveMu.TryLock() {
			fail(w, errors.New("正在检查另一个来源的迁移，请稍后再试"))
			return
		}
		defer a.sourceMoveMu.Unlock()
		count, err := a.commitSourceMove(r.Context(), r.PathValue("id"), input.Token)
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{"id": r.PathValue("id"), "count": count})
	})
}
