package yingku

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type libraryDeleteOptions struct {
	DeleteFiles bool   `json:"deleteFiles"`
	Token       string `json:"deleteToken"`
}
type diskDeleteFile struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
	info     os.FileInfo
}
type diskDeletePlan struct {
	Token string           `json:"token"`
	Files []diskDeleteFile `json:"files"`
	IDs   []string         `json:"-"`
}
type diskDeletePreview struct {
	Plan    diskDeletePlan
	Hash    string
	Expires time.Time
}
type diskDeleteTarget struct {
	Original string `json:"original"`
	Stage    string `json:"stage"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
}
type diskDeleteJob struct {
	ID      string             `json:"id"`
	Targets []diskDeleteTarget `json:"targets"`
}

func pathKey(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}
func deletionPath(path string) (string, os.FileInfo, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", nil, err
	}
	abs = filepath.Clean(abs)
	info, err := os.Lstat(abs)
	if err != nil {
		return abs, nil, err
	}
	if !info.Mode().IsRegular() {
		return "", nil, errors.New("只允许删除普通视频文件，不删除目录或链接")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", nil, err
	}
	if pathKey(resolved) != pathKey(abs) {
		return "", nil, errors.New("文件路径包含链接，请在服务器上手动处理")
	}
	// Windows loads file identity lazily; cache it while the original name still exists.
	if !os.SameFile(info, info) {
		return "", nil, errors.New("无法确认视频文件身份，请检查权限后重试")
	}
	return abs, info, nil
}

func (a *App) diskDeletePlanLocked(ids map[string]bool) (diskDeletePlan, error) {
	plan := diskDeletePlan{Files: []diskDeleteFile{}}
	paths := map[string]string{}
	kinds := map[string]string{}
	for _, file := range a.s.Files {
		if ids[file.ID] {
			plan.IDs = append(plan.IDs, file.ID)
			if file.Destination != "" {
				abs, _ := filepath.Abs(file.Destination)
				paths[pathKey(abs)] = abs
				kinds[pathKey(abs)] = "video"
			}
		}
	}
	for _, file := range a.s.Files {
		if !ids[file.ID] && file.Destination != "" {
			abs, _ := filepath.Abs(file.Destination)
			if paths[pathKey(abs)] != "" {
				return plan, errors.New("视频还被其他媒体库记录引用，只能先移出记录")
			}
		}
	}
	for _, task := range a.s.Tasks {
		if ids[task.FileID] {
			if task.Status == "downloading" || task.Status == "finalizing" {
				return plan, errors.New("请先停止选中视频的下载并等待收尾")
			}
			if _, active := a.active[task.ID]; active {
				return plan, errors.New("下载仍在收尾，请稍后重试")
			}
			if filepath.Base(task.ID) != task.ID || strings.ContainsAny(task.ID, `/\`) {
				return plan, errors.New("下载任务编号无效")
			}
			for _, file := range a.s.Files {
				if file.ID == task.FileID && file.Destination != "" {
					abs, _ := filepath.Abs(file.Destination + ".yingku-" + task.ID + ".part")
					paths[pathKey(abs)] = abs
					kinds[pathKey(abs)] = "partial"
				}
			}
		}
	}
	for _, session := range a.casts {
		if session.ended {
			continue
		}
		if ids[session.FileID] || session.nextGrant != nil && ids[session.nextGrant.fileID] || session.pendingGrant != nil && ids[session.pendingGrant.fileID] {
			return plan, errors.New("选中的视频正在投屏或预备连播，请先停止投屏")
		}
	}
	sort.Strings(plan.IDs)
	keys := make([]string, 0, len(paths))
	for key := range paths {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	stamp := append([]string{}, plan.IDs...)
	for _, key := range keys {
		path, info, err := deletionPath(paths[key])
		if errors.Is(err, os.ErrNotExist) {
			stamp = append(stamp, key+"|missing")
			continue
		}
		if err != nil {
			return plan, err
		}
		plan.Files = append(plan.Files, diskDeleteFile{Path: path, Kind: kinds[key], Size: info.Size(), Modified: info.ModTime().UnixNano(), info: info})
		stamp = append(stamp, fmt.Sprintf("%s|%d|%d", key, info.Size(), info.ModTime().UnixNano()))
	}
	raw, _ := json.Marshal(stamp)
	sum := sha256.Sum256(raw)
	plan.Token = hex.EncodeToString(sum[:])
	return plan, nil
}

func (a *App) deleteJobPath(id string) string { return filepath.Join(a.dir, "delete-jobs", id+".json") }
func (a *App) stageDiskDelete(plan diskDeletePlan) (*diskDeleteJob, error) {
	if len(plan.Files) == 0 {
		return nil, nil
	}
	job := &diskDeleteJob{ID: uid()}
	for index, file := range plan.Files {
		job.Targets = append(job.Targets, diskDeleteTarget{file.Path, filepath.Join(filepath.Dir(file.Path), fmt.Sprintf(".yingku-delete-%s-%d", job.ID, index)), file.Size, file.Modified})
	}
	raw, _ := json.Marshal(job)
	if err := atomicWrite(a.deleteJobPath(job.ID), raw, 0600); err != nil {
		return nil, err
	}
	for i, target := range job.Targets {
		if _, err := os.Lstat(target.Stage); !errors.Is(err, os.ErrNotExist) {
			_ = a.rollbackDiskDelete(job)
			return nil, errors.New("文件删除暂存名称冲突，请刷新重试")
		}
		if err := os.Rename(target.Original, target.Stage); err != nil {
			return nil, errors.Join(errors.New("无法删除视频，可能正在使用或没有权限"), a.rollbackDiskDelete(job))
		}
		info, err := os.Lstat(target.Stage)
		if err != nil || !info.Mode().IsRegular() || !os.SameFile(plan.Files[i].info, info) || info.Size() != target.Size || info.ModTime().UnixNano() != target.Modified {
			return nil, errors.Join(errors.New("视频文件已变化，请刷新后重新确认"), a.rollbackDiskDelete(job))
		}
	}
	return job, nil
}

func (a *App) rollbackDiskDelete(job *diskDeleteJob) error {
	if job == nil {
		return nil
	}
	var problems []error
	for _, target := range job.Targets {
		_, stage, err := deletionPath(target.Stage)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			problems = append(problems, err)
			continue
		}
		original, err := os.Lstat(target.Original)
		if err == nil {
			if os.SameFile(stage, original) {
				if err = os.Remove(target.Stage); err != nil {
					problems = append(problems, err)
				}
			} else {
				problems = append(problems, fmt.Errorf("原路径已有其他文件，删除暂存文件保留：%s", target.Stage))
			}
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			problems = append(problems, err)
			continue
		}
		// Link is an atomic no-overwrite restore. Filesystems without hard links use a checked rename.
		if err = os.Link(target.Stage, target.Original); err == nil {
			err = os.Remove(target.Stage)
		} else if _, statErr := os.Lstat(target.Original); errors.Is(statErr, os.ErrNotExist) {
			err = os.Rename(target.Stage, target.Original)
		}
		if err != nil {
			problems = append(problems, err)
		}
	}
	if len(problems) > 0 {
		return errors.Join(problems...)
	}
	return os.Remove(a.deleteJobPath(job.ID))
}

func (a *App) finishDiskDelete(job *diskDeleteJob) (int, error) {
	count := 0
	for _, target := range job.Targets {
		_, info, err := deletionPath(target.Stage)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return count, err
		}
		if info.Size() != target.Size || info.ModTime().UnixNano() != target.Modified {
			return count, errors.New("删除暂存文件已变化，保留文件等待手动检查")
		}
		if err = os.Remove(target.Stage); err != nil {
			return count, err
		}
		count++
	}
	return count, os.Remove(a.deleteJobPath(job.ID))
}

func (a *App) recoverDiskDeletes() error {
	entries, err := os.ReadDir(filepath.Join(a.dir, "delete-jobs"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		a.fileDeleteWarnings = append(a.fileDeleteWarnings, "暂时无法恢复文件操作，相关文件已保留："+err.Error())
		return nil
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		seen[strings.TrimSuffix(entry.Name(), ".json")] = true
		raw, err := os.ReadFile(filepath.Join(a.dir, "delete-jobs", entry.Name()))
		if err != nil {
			a.fileDeleteWarnings = append(a.fileDeleteWarnings, "无法读取文件操作恢复记录："+err.Error())
			continue
		}
		var job diskDeleteJob
		if json.Unmarshal(raw, &job) != nil || len(job.ID) != 24 || entry.Name() != job.ID+".json" {
			a.fileDeleteWarnings = append(a.fileDeleteWarnings, "文件操作恢复记录损坏，暂存文件已保留："+entry.Name())
			continue
		}
		if _, err = hex.DecodeString(job.ID); err != nil {
			a.fileDeleteWarnings = append(a.fileDeleteWarnings, "文件操作恢复编号无效，暂存文件已保留："+entry.Name())
			continue
		}
		valid := true
		for index, target := range job.Targets {
			if !filepath.IsAbs(target.Original) || target.Stage != filepath.Join(filepath.Dir(target.Original), fmt.Sprintf(".yingku-delete-%s-%d", job.ID, index)) {
				valid = false
				break
			}
		}
		if !valid {
			a.fileDeleteWarnings = append(a.fileDeleteWarnings, "文件操作恢复路径无效，未处理任何文件："+entry.Name())
			continue
		}
		seen[job.ID] = true
		if a.s.FileDeletes[job.ID] {
			if _, err = a.finishDiskDelete(&job); err != nil {
				a.fileDeleteWarnings = append(a.fileDeleteWarnings, "文件清理未完成，暂存文件已保留："+err.Error())
				continue
			}
			delete(a.s.FileDeletes, job.ID)
		} else if err = a.rollbackDiskDelete(&job); err != nil {
			a.fileDeleteWarnings = append(a.fileDeleteWarnings, "视频恢复未完成，暂存文件已保留："+err.Error())
		}
	}
	for id := range a.s.FileDeletes {
		if !seen[id] {
			delete(a.s.FileDeletes, id)
		}
	}
	return nil
}

func (a *App) libraryDeletePreview(itemID string, season *int, fileID string) (diskDeletePlan, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if fileID != "" {
		for _, file := range a.s.Files {
			if file.ID == fileID {
				itemID = file.ItemID
				break
			}
		}
	}
	itemID = a.canonicalItemIDLocked(itemID)
	found := false
	for _, item := range a.s.Items {
		if item.ID == itemID {
			found = true
			if season != nil && (item.Type != "tv" || *season < 0 || *season > 999) {
				return diskDeletePlan{}, errors.New("季数无效")
			}
			break
		}
	}
	if !found {
		return diskDeletePlan{}, errors.New("作品已不存在")
	}
	ids := map[string]bool{}
	for _, file := range a.s.Files {
		if file.ItemID == itemID && (season == nil || file.Season == *season) && (fileID == "" || file.ID == fileID) {
			ids[file.ID] = true
		}
	}
	plan, err := a.diskDeletePlanLocked(ids)
	if err != nil {
		return plan, err
	}
	if a.deletePreviews == nil {
		a.deletePreviews = map[string]diskDeletePreview{}
	}
	for token, preview := range a.deletePreviews {
		if time.Now().After(preview.Expires) {
			delete(a.deletePreviews, token)
		}
	}
	if len(a.deletePreviews) >= 128 {
		oldest := ""
		for token, preview := range a.deletePreviews {
			if oldest == "" || preview.Expires.Before(a.deletePreviews[oldest].Expires) {
				oldest = token
			}
		}
		delete(a.deletePreviews, oldest)
	}
	hash := plan.Token
	plan.Token = uid() + uid()
	a.deletePreviews[plan.Token] = diskDeletePreview{Plan: plan, Hash: hash, Expires: time.Now().Add(5 * time.Minute)}
	return plan, nil
}

func (a *App) validateDeletePreviewLocked(token string, plan diskDeletePlan) error {
	preview, ok := a.deletePreviews[token]
	if !ok || time.Now().After(preview.Expires) || preview.Hash != plan.Token || len(preview.Plan.Files) != len(plan.Files) {
		return errors.New("文件列表已变化或确认已过期，请重新打开移除窗口")
	}
	for i, file := range plan.Files {
		if !os.SameFile(preview.Plan.Files[i].info, file.info) {
			return errors.New("视频文件已被替换，请重新打开移除窗口并确认")
		}
	}
	return nil
}

func readLibraryDeleteOptions(w http.ResponseWriter, r *http.Request) (libraryDeleteOptions, error) {
	var options libraryDeleteOptions
	if r.ContentLength != 0 {
		if err := readJSON(w, r, &options); err != nil {
			return options, err
		}
	}
	return options, nil
}
