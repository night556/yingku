package yingku

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type FileAvailability struct {
	Status    string `json:"status"`
	Message   string `json:"message,omitempty"`
	CheckedAt string `json:"checkedAt,omitempty"`
}
type fileHealthEntry struct {
	Path    string
	Checked time.Time
	Value   FileAvailability
}

func checkMediaAvailability(path string) FileAvailability {
	result := FileAvailability{Status: "available", CheckedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	info, err := os.Lstat(path)
	if err == nil && !info.Mode().IsRegular() {
		result.Status = "unreadable"
		result.Message = "此路径不是普通视频文件"
		return result
	}
	if err == nil {
		var file *os.File
		file, err = os.Open(path)
		if file != nil {
			file.Close()
		}
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			result.Status = "missing"
			result.Message = "文件不存在，可能已移动、删除，或存储尚未挂载"
		} else {
			result.Status = "unreadable"
			result.Message = "暂时无法读取文件，请检查存储连接与读取权限"
		}
	}
	return result
}

func (a *App) fileAvailabilityLocked(f MediaFile) FileAvailability {
	if cached, ok := a.fileHealth[f.ID]; ok && cached.Path == f.Destination {
		return cached.Value
	}
	return FileAvailability{Status: "checking", Message: "正在检查服务器上的视频文件"}
}

// At most two filesystem checks run at once. Offline network storage never
// blocks /api/state or spawns more workers on each browser poll.
func (a *App) scheduleFileAvailability(force string) {
	a.mu.Lock()
	if a.fileHealth == nil {
		a.fileHealth = map[string]fileHealthEntry{}
	}
	if force != "" {
		for id, entry := range a.fileHealth {
			if force == "*" || force == id {
				entry.Checked = time.Time{}
				a.fileHealth[id] = entry
			}
		}
	}
	if a.fileHealthBusy || a.closing {
		a.mu.Unlock()
		return
	}
	jobs := []MediaFile{}
	liveIDs := map[string]bool{}
	for _, f := range a.s.Files {
		liveIDs[f.ID] = true
		if f.Status != "completed" {
			continue
		}
		cached, ok := a.fileHealth[f.ID]
		if !ok || cached.Path != f.Destination || time.Since(cached.Checked) > time.Minute {
			jobs = append(jobs, f)
		}
	}
	for id := range a.fileHealth {
		if !liveIDs[id] {
			delete(a.fileHealth, id)
		}
	}
	if len(jobs) == 0 {
		a.mu.Unlock()
		return
	}
	// Explicit checks put the requested file first.
	if force != "" && force != "*" {
		for i := range jobs {
			if jobs[i].ID == force {
				jobs[0], jobs[i] = jobs[i], jobs[0]
				break
			}
		}
	}
	a.fileHealthBusy = true
	a.mu.Unlock()
	go func() {
		queue := make(chan MediaFile, len(jobs))
		for _, f := range jobs {
			queue <- f
		}
		close(queue)
		var workers sync.WaitGroup
		for i := 0; i < 2; i++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for f := range queue {
					value := checkMediaAvailability(f.Destination)
					a.mu.Lock()
					if !a.closing {
						for _, live := range a.s.Files {
							if live.ID == f.ID && live.Destination == f.Destination && live.Status == "completed" {
								a.fileHealth[f.ID] = fileHealthEntry{f.Destination, time.Now(), value}
								break
							}
						}
					}
					a.mu.Unlock()
				}
			}()
		}
		workers.Wait()
		a.mu.Lock()
		a.fileHealthBusy = false
		a.mu.Unlock()
	}()
}

func (a *App) waitFileAvailability(ctx context.Context, f MediaFile) FileAvailability {
	a.mu.Lock()
	cached, exists := a.fileHealth[f.ID]
	a.mu.Unlock()
	if exists && cached.Path == f.Destination && !cached.Checked.IsZero() && time.Since(cached.Checked) < time.Minute {
		return cached.Value
	}
	a.scheduleFileAvailability(f.ID)
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		a.mu.Lock()
		entry, ok := a.fileHealth[f.ID]
		a.mu.Unlock()
		if ok && entry.Path == f.Destination && !entry.Checked.IsZero() {
			return entry.Value
		}
		select {
		case <-ctx.Done():
			return FileAvailability{Status: "checking", Message: "文件检查尚未完成，请稍后重试"}
		case <-deadline.C:
			return FileAvailability{Status: "checking", Message: "文件检查超时，请确认存储在线后重新检查"}
		case <-ticker.C:
		}
	}
}

func (a *App) relocateMedia(fileID, target string) error {
	target = strings.TrimSpace(target)
	if !filepath.IsAbs(target) || !mediaExt(target) {
		return errors.New("请填写服务器上的完整视频路径，支持 MP4、MKV 等视频文件")
	}
	target = filepath.Clean(target)
	health := checkMediaAvailability(target)
	if health.Status != "available" {
		return errors.New("新路径无法使用：" + health.Message)
	}
	info, err := os.Stat(target)
	if err != nil {
		return errors.New("新文件无法读取")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	index := -1
	for i, f := range a.s.Files {
		if f.ID == fileID {
			index = i
			break
		}
	}
	if index < 0 {
		return errors.New("视频记录已不存在")
	}
	f := &a.s.Files[index]
	if f.Status != "completed" {
		return errors.New("请等待视频入库完成后再重新定位")
	}
	for _, task := range a.s.Tasks {
		if task.FileID == fileID {
			if _, busy := a.active[task.ID]; busy || task.Status == "queued" || task.Status == "downloading" || task.Status == "finalizing" {
				return errors.New("相关下载任务仍在运行，请先暂停并等待收尾")
			}
		}
	}
	for _, cast := range a.casts {
		if !cast.ended && (cast.FileID == fileID || cast.nextGrant != nil && cast.nextGrant.fileID == fileID || cast.pendingGrant != nil && cast.pendingGrant.fileID == fileID) {
			return errors.New("此视频正在投屏或已预备投屏，请先停止投屏")
		}
	}
	for _, other := range a.s.Files {
		if other.ID != fileID && pathKey(other.Destination) == pathKey(target) {
			return errors.New("新路径已关联媒体库中的另一个视频，请勿重复定位")
		}
	}
	before := *f
	f.Destination = target
	f.Name = filepath.Base(target)
	f.Size = info.Size()
	for _, source := range a.s.Sources {
		if source.ID == f.SourceID && source.Type == "local" && within(source.Root, target) {
			if relative, e := filepath.Rel(source.Root, target); e == nil {
				f.SourceKey = "/" + filepath.ToSlash(relative)
			}
		}
	}
	if err := a.saveLocked(); err != nil {
		*f = before
		return err
	}
	if a.fileHealth == nil {
		a.fileHealth = map[string]fileHealthEntry{}
	}
	a.fileHealth[fileID] = fileHealthEntry{target, time.Now(), health}
	return nil
}

func (a *App) fileAvailabilityRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/files/check", func(w http.ResponseWriter, r *http.Request) {
		a.scheduleFileAvailability("*")
		jsonResponse(w, 202, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/files/{id}/availability", func(w http.ResponseWriter, r *http.Request) {
		f, e := a.file(r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		if r.URL.Query().Get("refresh") == "1" {
			a.scheduleFileAvailability(f.ID)
		}
		jsonResponse(w, 200, a.waitFileAvailability(r.Context(), f))
	})
	mux.HandleFunc("POST /api/files/{id}/relocate", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Path string `json:"path"`
		}
		if e := readJSON(w, r, &body); e != nil {
			fail(w, e)
			return
		}
		if e := a.relocateMedia(r.PathValue("id"), body.Path); e != nil {
			fail(w, e)
			return
		}
		jsonResponse(w, 200, map[string]bool{"ok": true})
	})
}
