package main

import (
	"errors"
	"os"
	"path/filepath"
)

type sourceRemovalResult struct {
	OK                bool   `json:"ok"`
	RemovedFiles      int    `json:"removedFiles"`
	RemovedItems      int    `json:"removedItems"`
	DeletedDiskFiles  int    `json:"deletedDiskFiles,omitempty"`
	FileDeleteWarning string `json:"fileDeleteWarning,omitempty"`
}

// Only catalogue records are detached. Media, NFO, images and subtitle files stay on disk.
// New slices and maps let callers restore the previous state if persistence fails.
func (a *App) detachLibraryFilesLocked(fileIDs map[string]bool, emptyItemIDs ...string) sourceRemovalResult {
	result := sourceRemovalResult{OK: true}
	affectedItems, remainingItems := map[string]bool{}, map[string]bool{}
	for _, id := range emptyItemIDs {
		affectedItems[id] = true
	}
	files := make([]MediaFile, 0, len(a.s.Files))
	for _, f := range a.s.Files {
		if fileIDs[f.ID] {
			result.RemovedFiles++
			affectedItems[f.ItemID] = true
		} else {
			files = append(files, f)
			remainingItems[f.ItemID] = true
		}
	}
	removedItems := map[string]bool{}
	items := make([]Item, 0, len(a.s.Items))
	for _, it := range a.s.Items {
		if affectedItems[it.ID] && !remainingItems[it.ID] {
			removedItems[it.ID] = true
			result.RemovedItems++
		} else {
			items = append(items, it)
		}
	}
	tasks := make([]Task, 0, len(a.s.Tasks))
	for _, task := range a.s.Tasks {
		if !fileIDs[task.FileID] {
			tasks = append(tasks, task)
		}
	}
	subtitles := make(map[string][]SavedSubtitle, len(a.s.SubtitlePaths))
	for id, paths := range a.s.SubtitlePaths {
		if !fileIDs[id] {
			subtitles[id] = paths
		}
	}
	aliases := make(map[string]string, len(a.s.ItemAliases))
	for id, target := range a.s.ItemAliases {
		if !removedItems[a.canonicalItemIDLocked(id)] {
			aliases[id] = target
		}
	}
	a.s.Files, a.s.Items, a.s.Tasks = files, items, tasks
	a.s.SubtitlePaths, a.s.ItemAliases = subtitles, aliases
	return result
}

func (a *App) removeSource(id string) (sourceRemovalResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var source Source
	for _, candidate := range a.s.Sources {
		if candidate.ID == id {
			source = candidate
			break
		}
	}
	if source.ID == "" {
		return sourceRemovalResult{}, errors.New("来源不存在")
	}
	fileIDs := map[string]bool{}
	for _, f := range a.s.Files {
		if f.SourceID == id {
			fileIDs[f.ID] = true
		}
	}
	for _, task := range a.s.Tasks {
		if !fileIDs[task.FileID] {
			continue
		}
		if _, busy := a.active[task.ID]; busy {
			return sourceRemovalResult{}, errors.New("来源任务正在运行或收尾，请完成或取消后再移除")
		}
		if task.Status != "completed" && task.Status != "cancelled" {
			return sourceRemovalResult{}, errors.New("来源还有未完成任务，请完成或取消后再移除")
		}
	}
	before := a.s
	sources := make([]Source, 0, len(a.s.Sources)-1)
	for _, candidate := range a.s.Sources {
		if candidate.ID != id {
			sources = append(sources, candidate)
		}
	}
	a.s.Sources = sources
	result := sourceRemovalResult{OK: true}
	if source.Type == "local" {
		result = a.detachLibraryFilesLocked(fileIDs)
	}
	if err := a.saveLocked(); err != nil {
		a.s = before
		return sourceRemovalResult{}, err
	}
	remainingIDs := map[string]bool{}
	for _, it := range a.s.Items {
		remainingIDs[it.ID] = true
	}
	for _, it := range before.Items {
		if !remainingIDs[it.ID] {
			delete(a.metadataJobs, it.ID)
		}
	}
	for scanID, scan := range a.scans {
		if scan.SourceID == id {
			if scan.cancel != nil {
				scan.cancel()
			}
			delete(a.scans, scanID)
		}
	}
	return result, nil
}

// Older releases removed the connection but retained direct local references.
// A missing source is only grounds for repair for reference files, never downloaded copies.
func (a *App) repairRemovedLocalSourcesLocked(original []byte) error {
	sources := map[string]bool{}
	for _, source := range a.s.Sources {
		sources[source.ID] = true
	}
	files := map[string]bool{}
	for _, f := range a.s.Files {
		if f.Reference && f.SourceID != "" && !sources[f.SourceID] {
			files[f.ID] = true
		}
	}
	if len(files) == 0 {
		return nil
	}
	backup := filepath.Join(a.dir, "state-before-source-cleanup-v0133.json")
	if _, err := os.Stat(backup); errors.Is(err, os.ErrNotExist) {
		if err := atomicWrite(backup, original, 0600); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	a.detachLibraryFilesLocked(files)
	return nil
}
