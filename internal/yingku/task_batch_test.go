package yingku

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTaskBatchRemoveRetainsCompletedLibraryAndAllFiles(t *testing.T) {
	a, server, physical := libraryRemovalFixture(t)
	request(t, server, a, "POST", "/api/tasks/batch", map[string]any{"action": "remove", "ids": []string{"task-combined", "task-season2"}}, 200)
	if len(a.s.Tasks) != 0 {
		t.Fatal("task history remains")
	}
	if _, err := a.file("season2"); err != nil {
		t.Fatal("completed video removed from library")
	}
	if _, err := a.file("combined"); err == nil {
		t.Fatal("unfinished placeholder retained")
	}
	for path, original := range physical {
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, original) {
			t.Fatalf("batch task removal changed disk file: %s", path)
		}
	}
}

func TestTaskBatchStopsQueuedTogetherAndWaitsForWorker(t *testing.T) {
	a, _, _ := libraryRemovalFixture(t)
	a.s.Tasks[0].Status = "downloading"
	a.s.Files[3].Status = "downloading"
	a.s.Tasks = append(a.s.Tasks, Task{ID: "another", FileID: "special", Status: "queued"})
	stopped := false
	a.active["task-combined"] = func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		for _, task := range a.s.Tasks {
			if task.ID == "task-combined" || task.ID == "another" {
				if task.Status != "cancelled" {
					t.Error("worker stopped before the entire selection was cancelled")
				}
			}
		}
		stopped = true
		delete(a.active, "task-combined")
	}
	result, err := a.controlTaskBatch(context.Background(), []string{"task-combined", "another"}, "remove")
	if err != nil {
		t.Fatal(err)
	}
	if !stopped || len(result.Applied) != 2 {
		t.Fatal("did not drain and remove selected tasks")
	}
}

func TestTaskBatchSaveFailureDoesNotCancelWorker(t *testing.T) {
	a, _, _ := libraryRemovalFixture(t)
	a.s.Tasks[0].Status = "downloading"
	called := false
	a.active["task-combined"] = func() { called = true }
	before, _ := json.Marshal(a.s)
	_ = os.Remove(filepath.Join(a.dir, "state.json"))
	_ = os.Mkdir(filepath.Join(a.dir, "state.json"), 0700)
	if _, err := a.controlTaskBatch(context.Background(), []string{"task-combined"}, "pause"); err == nil {
		t.Fatal("expected save failure")
	}
	after, _ := json.Marshal(a.s)
	if called || !bytes.Equal(before, after) {
		t.Fatal("failed batch changed state or stopped a worker")
	}
}

func TestTaskBatchSkipsFinalizingAndReportsUnknown(t *testing.T) {
	a, _, _ := libraryRemovalFixture(t)
	a.s.Tasks[0].Status = "finalizing"
	result, err := a.controlTaskBatch(context.Background(), []string{"task-combined", "missing", "task-season2"}, "cancel")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Applied) != 0 || len(result.Skipped) != 3 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestTaskBatchTimeoutStopsWithoutRemovingActiveRecord(t *testing.T) {
	a, _, _ := libraryRemovalFixture(t)
	a.s.Tasks[0].Status = "downloading"
	a.active["task-combined"] = func() {}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	result, err := a.controlTaskBatch(ctx, []string{"task-combined"}, "remove")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Applied) != 0 || len(result.Skipped) != 1 {
		t.Fatal("unfinished worker removal not reported")
	}
	if a.s.Tasks[0].Status != "cancelled" {
		t.Fatal("timed-out removal did not stop download")
	}
	if _, err = a.file("combined"); err != nil {
		t.Fatal("active file record was removed")
	}
}
