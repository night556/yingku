package yingku

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalSourceMigrationThroughAPI(t *testing.T) {
	a := testApp(t)
	root := t.TempDir()
	oldRoot, newRoot := filepath.Join(root, "old"), filepath.Join(root, "new")
	put := func(base, key, content string) string {
		t.Helper()
		target := filepath.Join(base, filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		return target
	}
	const oldOne = "原目录/某剧/01.mp4"
	const oldTwo = "原目录/某剧/02.mp4"
	const newOne = "TV/某剧/01.mp4"
	const newTwo = "TV/某剧/第二集.mp4"
	videoOne, videoTwo := "video one", "video two"
	original := put(oldRoot, oldOne, videoOne)
	second := put(oldRoot, oldTwo, videoTwo)
	nextOne := put(newRoot, newOne, videoOne)
	nextTwo := put(newRoot, newTwo, videoTwo)
	oldSame := put(oldRoot, "电影.mp4", "movie")
	newSame := put(newRoot, "电影.mp4", "movie")
	copyPath := put(root, "copies/copied.mp4", "copy")
	a.s.Sources = []Source{{ID: "local", Type: "local", Name: "旧来源", Root: oldRoot}}
	a.s.Items = []Item{{ID: "series", Title: "某剧", Type: "tv", Overview: "已有资料"}}
	a.s.Files = []MediaFile{
		{ID: "one", ItemID: "series", SourceID: "local", SourceKey: "/" + oldOne, Destination: original, Name: "01.mp4", Reference: true, Status: "completed", Size: int64(len(videoOne)), Season: 1, Episode: 1, Position: 120},
		{ID: "two", ItemID: "series", SourceID: "local", SourceKey: "/" + oldTwo, Destination: second, Name: "02.mp4", Reference: true, Status: "completed", Size: int64(len(videoTwo)), Season: 1, Episode: 2},
		{ID: "same", ItemID: "series", SourceID: "local", SourceKey: "/电影.mp4", Destination: oldSame, Name: "电影.mp4", Reference: true, Status: "completed", Size: 5},
		{ID: "copy", ItemID: "series", SourceID: "local", SourceKey: "/copied.mp4", Destination: copyPath, Status: "completed"},
	}
	a.s.Tasks = []Task{{ID: "copy-task", FileID: "copy", Status: "completed"}}
	defaultDownload := a.s.Config.MediaRoot
	if err := a.saveSubtitle("one", "中文字幕.srt", []byte("1\n00:00:00,000 --> 00:00:01,000\n字幕\n")); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.handler())
	defer server.Close()

	const base = "/api/sources/local/migration"
	request(t, server, a, "POST", "/api/sources", Source{ID: "local", Type: "local", Name: "旧来源", Root: newRoot}, 400)
	input := sourceMoveInput{Root: newRoot, Name: "新来源"}
	getPreview := func() map[string]json.RawMessage {
		return request(t, server, a, "POST", base+"/preview", input, 200)
	}
	stringField := func(data map[string]json.RawMessage, field string) string {
		t.Helper()
		var value string
		if err := json.Unmarshal(data[field], &value); err != nil {
			t.Fatalf("missing %s: %v", field, err)
		}
		return value
	}
	originalState := func() {
		t.Helper()
		source, _ := a.source("local")
		f, _ := a.file("one")
		if source.Root != oldRoot || f.Destination != original || f.Position != 120 {
			t.Fatal("migration changed state before a successful commit")
		}
	}

	if preview := getPreview(); string(preview["problems"]) != "2" || stringField(preview, "token") != "" {
		t.Fatal("missing files must block migration")
	}
	input.Directories = []sourceMoveRule{{From: "原目录", To: "TV"}}
	if preview := getPreview(); string(preview["problems"]) != "1" || stringField(preview, "token") != "" {
		t.Fatal("directory mapping did not resolve the first video")
	}
	input.Files = map[string]string{"two": newTwo}
	token := stringField(getPreview(), "token")
	if token == "" {
		t.Fatal("valid mapping and override should produce a confirmable preview")
	}
	originalState()

	put(newRoot, newTwo, "changed to a different file")
	request(t, server, a, "POST", base+"/commit", map[string]string{"token": token}, 400)
	originalState()
	put(newRoot, newTwo, videoTwo)
	token = stringField(getPreview(), "token")
	body := map[string]string{"token": token}

	_, endRead, err := a.beginMediaRead("one")
	if err != nil {
		t.Fatal(err)
	}
	request(t, server, a, "POST", base+"/commit", body, 400)
	endRead()
	claim := request(t, server, a, "POST", "/api/files/one/playback", map[string]bool{"trackActivity": true}, 200)
	lease := stringField(claim, "activityToken")
	if lease == "" {
		t.Fatal("web player did not obtain an activity lease")
	}
	request(t, server, a, "POST", base+"/commit", body, 400)
	request(t, server, a, "POST", "/api/files/one/playback/activity", map[string]any{"token": lease, "ended": true}, 200)
	a.mu.Lock()
	a.casts = map[string]*CastSession{"tv": {FileID: "two", Created: time.Now(), Status: "PLAYING"}}
	a.mu.Unlock()
	request(t, server, a, "POST", base+"/commit", body, 400)
	a.mu.Lock()
	delete(a.casts, "tv")
	a.mu.Unlock()
	originalState()
	for _, old := range []string{original, second, oldSame} {
		if err := os.Remove(old); err != nil {
			t.Fatal(err)
		}
	}

	result := request(t, server, a, "POST", base+"/commit", body, 200)
	if string(result["count"]) != "3" {
		t.Fatal("expected three relocated videos")
	}
	reloaded, err := newApp(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	source, _ := reloaded.source("local")
	first, _ := reloaded.file("one")
	secondFile, _ := reloaded.file("two")
	sameFile, _ := reloaded.file("same")
	copyFile, _ := reloaded.file("copy")
	if pathKey(source.Root) != pathKey(newRoot) || source.Name != "新来源" ||
		pathKey(first.Destination) != pathKey(nextOne) || first.SourceKey != "/"+newOne ||
		pathKey(secondFile.Destination) != pathKey(nextTwo) || secondFile.SourceKey != "/"+newTwo ||
		pathKey(sameFile.Destination) != pathKey(newSame) || sameFile.SourceKey != "/电影.mp4" ||
		secondFile.Name != "第二集.mp4" || first.Position != 120 || first.Episode != 1 ||
		len(first.Subtitles) != 1 || reloaded.s.Items[0].Overview != "已有资料" ||
		copyFile.Destination != copyPath || reloaded.s.Config.MediaRoot != defaultDownload {
		t.Fatal("relocated library did not preserve state or references")
	}
	r, err := http.Get(server.URL + "/api/files/two/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	content, err := io.ReadAll(r.Body)
	if err != nil || r.StatusCode != 200 || string(content) != videoTwo {
		t.Fatalf("stream did not use relocated file: %d %q %v", r.StatusCode, content, err)
	}
}
