package main

import (
	"errors"
	"net/http"
	"strconv"
)

// An entire item, a local season, or a single video record can leave the library.
// Sources and all disk files remain available for a later import.
func (a *App) removeLibrary(itemID string, season *int, fileID string, options ...libraryDeleteOptions) (sourceRemovalResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if fileID != "" {
		for _, file := range a.s.Files {
			if file.ID == fileID {
				itemID = file.ItemID
				break
			}
		}
		if itemID == "" {
			return sourceRemovalResult{}, errors.New("视频已移出媒体库或不存在")
		}
	}
	itemID = a.canonicalItemIDLocked(itemID)
	var item Item
	for _, candidate := range a.s.Items {
		if candidate.ID == itemID {
			item = candidate
			break
		}
	}
	if item.ID == "" {
		return sourceRemovalResult{}, errors.New("作品已移出媒体库或不存在")
	}
	if season != nil && (item.Type != "tv" || *season < 0 || *season > 999) {
		return sourceRemovalResult{}, errors.New("请选择电视剧中有效的季")
	}
	files := map[string]bool{}
	for _, file := range a.s.Files {
		if file.ItemID == itemID && (season == nil || file.Season == *season) && (fileID == "" || file.ID == fileID) {
			if file.Status == "downloading" || file.Status == "finalizing" {
				return sourceRemovalResult{}, errors.New("选中的视频正在下载或入库，请先在下载任务页暂停，等待收尾后再移出")
			}
			files[file.ID] = true
		}
	}
	if len(files) == 0 && (season != nil || fileID != "") {
		return sourceRemovalResult{}, errors.New("所选季或视频已移出媒体库")
	}
	for _, task := range a.s.Tasks {
		if !files[task.FileID] {
			continue
		}
		if _, busy := a.active[task.ID]; busy {
			return sourceRemovalResult{}, errors.New("下载任务正在收尾，请稍后再移出")
		}
		if task.Status == "downloading" || task.Status == "finalizing" {
			return sourceRemovalResult{}, errors.New("选中的视频正在下载或入库，请先在下载任务页暂停，等待收尾后再移出")
		}
	}
	var job *diskDeleteJob
	if len(options) > 0 && options[0].DeleteFiles {
		plan, err := a.diskDeletePlanLocked(files)
		if err != nil {
			return sourceRemovalResult{}, err
		}
		if err = a.validateDeletePreviewLocked(options[0].Token, plan); err != nil {
			return sourceRemovalResult{}, err
		}
		job, err = a.stageDiskDelete(plan)
		if err != nil {
			return sourceRemovalResult{}, err
		}
	}
	before := a.s
	result := a.detachLibraryFilesLocked(files, itemID)
	if job != nil {
		pending := map[string]bool{}
		for id, value := range a.s.FileDeletes {
			pending[id] = value
		}
		pending[job.ID] = true
		a.s.FileDeletes = pending
	}
	if err := a.saveLocked(); err != nil {
		a.s = before
		return sourceRemovalResult{}, errors.Join(err, a.rollbackDiskDelete(job))
	}
	if result.RemovedItems > 0 {
		delete(a.metadataJobs, itemID)
	}
	if job != nil {
		count, err := a.finishDiskDelete(job)
		result.DeletedDiskFiles = count
		if err != nil {
			result.FileDeleteWarning = "部分视频文件未能清理，已保留删除暂存文件，重启后会重试：" + err.Error()
		} else {
			delete(a.s.FileDeletes, job.ID)
			if err = a.saveLocked(); err != nil {
				result.FileDeleteWarning = "文件已删除，清理记录将在重启后恢复"
			}
		}
	}
	return result, nil
}

func (a *App) libraryRemovalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("DELETE /api/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		options, err := readLibraryDeleteOptions(w, r)
		if err != nil {
			fail(w, err)
			return
		}
		result, err := a.removeLibrary(r.PathValue("id"), nil, "", options)
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, 200, result)
	})
	mux.HandleFunc("DELETE /api/items/{id}/seasons/{season}", func(w http.ResponseWriter, r *http.Request) {
		season, err := strconv.Atoi(r.PathValue("season"))
		if err != nil {
			fail(w, errors.New("季数无效"))
			return
		}
		options, err := readLibraryDeleteOptions(w, r)
		if err != nil {
			fail(w, err)
			return
		}
		result, err := a.removeLibrary(r.PathValue("id"), &season, "", options)
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, 200, result)
	})
	mux.HandleFunc("DELETE /api/files/{id}", func(w http.ResponseWriter, r *http.Request) {
		options, err := readLibraryDeleteOptions(w, r)
		if err != nil {
			fail(w, err)
			return
		}
		result, err := a.removeLibrary("", nil, r.PathValue("id"), options)
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, 200, result)
	})
	preview := func(w http.ResponseWriter, r *http.Request, itemID string, season *int, fileID string) {
		plan, err := a.libraryDeletePreview(itemID, season, fileID)
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, 200, plan)
	}
	mux.HandleFunc("GET /api/items/{id}/removal-preview", func(w http.ResponseWriter, r *http.Request) { preview(w, r, r.PathValue("id"), nil, "") })
	mux.HandleFunc("GET /api/items/{id}/seasons/{season}/removal-preview", func(w http.ResponseWriter, r *http.Request) {
		season, err := strconv.Atoi(r.PathValue("season"))
		if err != nil {
			fail(w, err)
			return
		}
		preview(w, r, r.PathValue("id"), &season, "")
	})
	mux.HandleFunc("GET /api/files/{id}/removal-preview", func(w http.ResponseWriter, r *http.Request) { preview(w, r, "", nil, r.PathValue("id")) })
}
