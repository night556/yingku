package yingku

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func addTaskPagePreview(t *testing.T, a *App) {
	t.Helper()
	sample, err := os.ReadFile(a.s.Files[0].Destination)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	a.s.Sources = append(a.s.Sources, Source{ID: "qa-baidu", Name: "模拟百度网盘", Type: "baidu", Root: "/测试影视"})
	a.s.Items = append(a.s.Items, Item{ID: "qa-download-show", Title: "示例下载剧集", Type: "tv"}, Item{ID: "qa-delete-movie", Title: "文件删除选项测试", Type: "movie"})
	for index, status := range []string{"queued", "queued", "downloading", "paused", "failed", "completed"} {
		id := fmt.Sprintf("qa-download-%02d", index+1)
		taskID := fmt.Sprintf("qa-task-%02d", index+1)
		path := filepath.Join(root, id+".mp4")
		if status == "completed" {
			if err = os.WriteFile(path, sample, 0600); err != nil {
				t.Fatal(err)
			}
		} else if err = os.WriteFile(path+".yingku-"+taskID+".part", sample[:len(sample)/2], 0600); err != nil {
			t.Fatal(err)
		}
		a.s.Files = append(a.s.Files, MediaFile{ID: id, ItemID: "qa-download-show", SourceID: "qa-baidu", Name: fmt.Sprintf("示例下载剧集.S01E%02d.mp4", index+1), Season: 1, Episode: index + 1, Destination: path, Status: status})
		a.s.Tasks = append(a.s.Tasks, Task{ID: taskID, FileID: id, Status: status, Bytes: int64(len(sample) / 2), Total: int64(len(sample))})
	}
	path := filepath.Join(root, "独立文件删除预览测试.mp4")
	if err = os.WriteFile(path, sample, 0600); err != nil {
		t.Fatal(err)
	}
	a.s.Files = append(a.s.Files, MediaFile{ID: "qa-delete-file", ItemID: "qa-delete-movie", Name: filepath.Base(path), Destination: path, Reference: true, Status: "completed"})
}
