package main

import (
	"context"
	"errors"
	"net/http"
	"time"
)

type taskBatchIssue struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}
type taskBatchResult struct {
	Applied []string         `json:"applied"`
	Skipped []taskBatchIssue `json:"skipped"`
}

func (a *App) controlTaskBatch(ctx context.Context, ids []string, action string) (taskBatchResult, error) {
	result := taskBatchResult{Applied: []string{}, Skipped: []taskBatchIssue{}}
	if len(ids) == 0 || len(ids) > 5000 {
		return result, errors.New("请选择 1 至 5000 个任务")
	}
	if action != "pause" && action != "resume" && action != "cancel" && action != "remove" {
		return result, errors.New("不支持的批量操作")
	}
	requested := map[string]bool{}
	for _, id := range ids {
		requested[id] = true
	}
	a.mu.Lock()
	before := a.s
	a.s.Tasks = append([]Task(nil), a.s.Tasks...)
	a.s.Files = append([]MediaFile(nil), a.s.Files...)
	var stops []func()
	completedFiles := map[string]bool{}
	for _, file := range before.Files {
		if file.Status == "completed" {
			completedFiles[file.ID] = true
		}
	}
	candidates := map[string]bool{}
	completed := map[string]bool{}
	for i := range a.s.Tasks {
		task := &a.s.Tasks[i]
		if !requested[task.ID] {
			continue
		}
		delete(requested, task.ID)
		problem := ""
		next := task.Status
		switch action {
		case "pause":
			if task.Status == "queued" || task.Status == "downloading" {
				next = "paused"
			} else {
				problem = "当前状态无需暂停"
			}
		case "resume":
			if _, active := a.active[task.ID]; active {
				problem = "任务仍在收尾"
			} else if task.Status == "paused" || task.Status == "failed" || task.Status == "cancelled" {
				next = "queued"
			} else {
				problem = "当前状态无需继续"
			}
		case "cancel":
			if task.Status == "completed" || task.Status == "finalizing" {
				problem = "已完成或正在入库，不能取消"
			} else {
				next = "cancelled"
			}
		case "remove":
			if task.Status == "finalizing" {
				problem = "正在入库，请完成后再移除"
			} else {
				candidates[task.ID] = true
				completed[task.ID] = task.Status == "completed"
				if task.Status != "completed" {
					next = "cancelled"
				}
			}
		}
		if problem != "" {
			result.Skipped = append(result.Skipped, taskBatchIssue{task.ID, problem})
			continue
		}
		task.Status = next
		task.Speed = 0
		if next == "queued" {
			task.Error = ""
		}
		if stop, active := a.active[task.ID]; active && stop != nil && (next == "paused" || next == "cancelled") {
			stops = append(stops, stop)
		}
		for j := range a.s.Files {
			if a.s.Files[j].ID == task.FileID && (!completedFiles[task.FileID] || next == "completed") {
				a.s.Files[j].Status = next
			}
		}
		if action != "remove" {
			result.Applied = append(result.Applied, task.ID)
		}
	}
	for id := range requested {
		result.Skipped = append(result.Skipped, taskBatchIssue{id, "任务已不存在"})
	}
	if err := a.saveLocked(); err != nil {
		a.s = before
		a.mu.Unlock()
		return taskBatchResult{}, err
	}
	a.mu.Unlock()
	for _, stop := range stops {
		stop()
	}
	if action != "remove" {
		return result, nil
	}
	// Queued tasks were cancelled together above, so workers cannot pick up another selected episode.
	for {
		a.mu.Lock()
		active := false
		for id := range candidates {
			if _, ok := a.active[id]; ok {
				active = true
				break
			}
		}
		a.mu.Unlock()
		if !active {
			break
		}
		select {
		case <-ctx.Done():
			for id := range candidates {
				result.Skipped = append(result.Skipped, taskBatchIssue{id, "已停止下载，收尾完成后可再次移除记录"})
			}
			return result, nil
		case <-time.After(25 * time.Millisecond):
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	before = a.s
	removing := map[string]bool{}
	pendingFiles := map[string]bool{}
	for _, task := range a.s.Tasks {
		if !candidates[task.ID] {
			continue
		}
		_, active := a.active[task.ID]
		if active || task.Status == "downloading" || task.Status == "queued" || task.Status == "finalizing" {
			result.Skipped = append(result.Skipped, taskBatchIssue{task.ID, "任务状态已变化，未移除"})
			continue
		}
		removing[task.ID] = true
		if !completed[task.ID] {
			pendingFiles[task.FileID] = true
		}
	}
	for _, task := range a.s.Tasks {
		if !removing[task.ID] {
			delete(pendingFiles, task.FileID)
		}
	}
	for _, file := range a.s.Files {
		if file.Status == "completed" {
			delete(pendingFiles, file.ID)
		}
	}
	tasks := make([]Task, 0, len(a.s.Tasks))
	for _, task := range a.s.Tasks {
		if removing[task.ID] {
			result.Applied = append(result.Applied, task.ID)
		} else {
			tasks = append(tasks, task)
		}
	}
	a.s.Tasks = tasks
	a.detachLibraryFilesLocked(pendingFiles)
	if err := a.saveLocked(); err != nil {
		a.s = before
		return taskBatchResult{}, err
	}
	remaining := map[string]bool{}
	for _, item := range a.s.Items {
		remaining[item.ID] = true
	}
	for _, item := range before.Items {
		if !remaining[item.ID] {
			delete(a.metadataJobs, item.ID)
		}
	}
	return result, nil
}

func (a *App) taskBatchRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/tasks/batch", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			IDs    []string `json:"ids"`
			Action string   `json:"action"`
		}
		if err := readJSON(w, r, &body); err != nil {
			fail(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		result, err := a.controlTaskBatch(ctx, body.IDs, body.Action)
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, 200, result)
	})
}
