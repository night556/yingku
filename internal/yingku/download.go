package yingku

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

func (a *App) startWorkers(ctx context.Context) {
	ticker := time.NewTicker(350 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.mu.Lock()
			if a.closing {
				a.mu.Unlock()
				return
			}
			for len(a.active) < a.s.Config.Concurrency {
				idx := -1
				for i, t := range a.s.Tasks {
					if t.Status == "queued" {
						idx = i
						break
					}
				}
				if idx < 0 {
					break
				}
				t := a.s.Tasks[idx]
				workerCtx, cancel := context.WithCancel(ctx)
				a.active[t.ID] = cancel
				a.s.Tasks[idx].Status = "downloading"
				a.s.Tasks[idx].Error = ""
				for i := range a.s.Files {
					if a.s.Files[i].ID == t.FileID {
						a.s.Files[i].Status = "downloading"
					}
				}
				if err := a.saveLocked(); err != nil {
					a.s.Tasks[idx].Status = "failed"
					a.s.Tasks[idx].Error = "任务保存失败：" + err.Error()
					delete(a.active, t.ID)
					cancel()
					break
				}
				go a.runTask(workerCtx, t.ID)
			}
			a.mu.Unlock()
		}
	}
}
func (a *App) updateProgress(id string, bytes, total int64, speed float64, etag, modified string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.s.Tasks {
		t := &a.s.Tasks[i]
		if t.ID == id {
			if t.Status != "downloading" {
				return context.Canceled
			}
			t.Bytes = bytes
			t.Total = total
			t.Speed = speed
			if etag != "" {
				t.ETag = etag
			}
			if modified != "" {
				t.LastModified = modified
			}
			return a.saveLocked()
		}
	}
	return errors.New("任务已不存在")
}

type progressWriter struct {
	ctx       context.Context
	writer    io.Writer
	bytes     int64
	lastBytes int64
	last      time.Time
	update    func(int64, float64) error
}

func (p *progressWriter) Write(b []byte) (int, error) {
	if err := p.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := p.writer.Write(b)
	p.bytes += int64(n)
	if time.Since(p.last) > 600*time.Millisecond {
		speed := float64(p.bytes-p.lastBytes) / time.Since(p.last).Seconds()
		p.last = time.Now()
		p.lastBytes = p.bytes
		if e := p.update(p.bytes, speed); e != nil && err == nil {
			err = e
		}
	}
	return n, err
}
func ensureDestination(root, target string) error {
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	if !within(root, target) {
		return errors.New("保存位置超出目标目录")
	}
	parent := filepath.Dir(target)
	if err = os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	realParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return err
	}
	if !within(realRoot, realParent) {
		return errors.New("目标目录包含指向外部的链接")
	}
	return nil
}
func (a *App) runTask(ctx context.Context, id string) {
	var task Task
	a.mu.Lock()
	for _, t := range a.s.Tasks {
		if t.ID == id {
			task = t
			break
		}
	}
	a.mu.Unlock()
	err := a.download(ctx, task)
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.active, id)
	for i := range a.s.Tasks {
		t := &a.s.Tasks[i]
		if t.ID != id {
			continue
		}
		t.Speed = 0
		if t.Status == "paused" || t.Status == "cancelled" {
			_ = a.saveLocked()
			return
		}
		if err != nil {
			if ctx.Err() != nil {
				t.Status = "paused"
				t.Error = "服务已暂停，点击继续恢复"
			} else {
				t.Status = "failed"
				t.Error = err.Error()
			}
		} else {
			t.Status = "completed"
			t.Bytes = t.Total
			t.Error = ""
		}
		for j := range a.s.Files {
			if a.s.Files[j].ID == t.FileID {
				a.s.Files[j].Status = t.Status
				if err == nil {
					a.s.Files[j].Size = t.Total
					f := a.s.Files[j]
					go a.writeNFO(f.ItemID)
				}
			}
		}
		_ = a.saveLocked()
		break
	}
}
func (a *App) download(ctx context.Context, t Task) error {
	f, err := a.file(t.FileID)
	if err != nil {
		return err
	}
	s, err := a.source(f.SourceID)
	if err != nil {
		return err
	}
	// All generated files live below the selected media root; infer it from the fixed layout.
	root := filepath.Dir(filepath.Dir(f.Destination))
	it, err := a.item(f.ItemID)
	if err != nil {
		return err
	}
	if it.Type == "tv" {
		root = filepath.Dir(root)
	}
	if err = ensureDestination(root, f.Destination); err != nil {
		return fmt.Errorf("无法创建下载目录：%w", err)
	}
	if _, err = os.Lstat(f.Destination); err == nil {
		return errors.New("目标文件已存在，已停止以避免覆盖")
	}
	part := f.Destination + ".yingku-" + t.ID + ".part"
	info, err := os.Lstat(part)
	if err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("临时下载位置不是普通文件")
		}
		t.Bytes = info.Size()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else {
		t.Bytes = 0
	}
	reader, err := a.openSource(ctx, s, f, t)
	if err != nil {
		return err
	}
	defer reader.Reader.Close()
	flags := os.O_CREATE | os.O_WRONLY
	if reader.Offset == 0 {
		flags |= os.O_TRUNC
	}
	out, err := os.OpenFile(part, flags, 0644)
	if err != nil {
		return fmt.Errorf("无法写入下载目录：%w", err)
	}
	defer out.Close()
	if _, err = out.Seek(reader.Offset, 0); err != nil {
		return err
	}
	if err = a.updateProgress(t.ID, reader.Offset, reader.Total, 0, reader.ETag, reader.LastModified); err != nil {
		return err
	}
	pw := &progressWriter{ctx: ctx, writer: out, bytes: reader.Offset, lastBytes: reader.Offset, last: time.Now(), update: func(n int64, speed float64) error { return a.updateProgress(t.ID, n, reader.Total, speed, "", "") }}
	_, err = io.CopyBuffer(pw, reader.Reader, make([]byte, 256<<10))
	if err != nil {
		return fmt.Errorf("下载未完成：%w", err)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if reader.Total > 0 && pw.bytes != reader.Total {
		return fmt.Errorf("下载长度不完整（%d / %d 字节），可重试", pw.bytes, reader.Total)
	}
	if err = out.Sync(); err != nil {
		return fmt.Errorf("磁盘写入失败：%w", err)
	}
	if err = out.Close(); err != nil {
		return err
	}
	a.mu.Lock()
	for i := range a.s.Tasks {
		if a.s.Tasks[i].ID == t.ID {
			if a.s.Tasks[i].Status != "downloading" {
				a.mu.Unlock()
				return context.Canceled
			}
			a.s.Tasks[i].Status = "finalizing"
			a.s.Tasks[i].Bytes = pw.bytes
			a.s.Tasks[i].Total = pw.bytes
			a.s.Tasks[i].Speed = 0
		}
	}
	err = a.saveLocked()
	a.mu.Unlock()
	if err != nil {
		return err
	}
	// A hard link commits without overwriting an existing file. Other filesystems use exclusive creation.
	if err = os.Link(part, f.Destination); err != nil {
		in, e := os.Open(part)
		if e != nil {
			return e
		}
		defer in.Close()
		final, e := os.OpenFile(f.Destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if e != nil {
			return fmt.Errorf("无法完成入库（目标可能已存在）：%w", e)
		}
		_, copyErr := io.Copy(final, in)
		if copyErr == nil {
			copyErr = final.Sync()
		}
		closeErr := final.Close()
		if copyErr != nil || closeErr != nil {
			_ = os.Remove(f.Destination)
			if copyErr != nil {
				return copyErr
			}
			return closeErr
		}
	}
	_ = os.Remove(part)
	if s.Type == "local" {
		a.copyLocalSubtitles(s, f)
	}
	return nil
}
func (a *App) controlTask(id, action string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.s.Tasks {
		t := &a.s.Tasks[i]
		if t.ID != id {
			continue
		}
		switch action {
		case "pause":
			if t.Status != "queued" && t.Status != "downloading" {
				return errors.New("此任务当前不能暂停")
			}
			t.Status = "paused"
			t.Speed = 0
			if cancel, ok := a.active[id]; ok {
				cancel()
			}
		case "resume":
			if _, busy := a.active[id]; busy {
				return errors.New("任务正在收尾，请稍后再继续")
			}
			if t.Status != "paused" && t.Status != "failed" && t.Status != "cancelled" {
				return errors.New("此任务无需继续")
			}
			t.Status = "queued"
			t.Error = ""
		case "remove":
			if _, busy := a.active[id]; busy {
				return errors.New("任务仍在收尾，请稍后再移除")
			}
			if t.Status != "failed" && t.Status != "cancelled" {
				return errors.New("请先取消任务；已完成的任务不在这里移除")
			}
			fileID := t.FileID
			itemID := ""
			for j, f := range a.s.Files {
				if f.ID == fileID {
					itemID = f.ItemID
					a.s.Files = append(a.s.Files[:j], a.s.Files[j+1:]...)
					break
				}
			}
			a.s.Tasks = append(a.s.Tasks[:i], a.s.Tasks[i+1:]...)
			delete(a.s.SubtitlePaths, fileID)
			stillUsed := false
			for _, f := range a.s.Files {
				if f.ItemID == itemID {
					stillUsed = true
					break
				}
			}
			if !stillUsed {
				for j, it := range a.s.Items {
					if it.ID == itemID {
						a.s.Items = append(a.s.Items[:j], a.s.Items[j+1:]...)
						break
					}
				}
			}
			return a.saveLocked()
		case "cancel":
			if t.Status == "completed" || t.Status == "finalizing" {
				return errors.New("已完成或正在入库的任务不能取消")
			}
			t.Status = "cancelled"
			t.Speed = 0
			if cancel, ok := a.active[id]; ok {
				cancel()
			}
		default:
			return errors.New("无效操作")
		}
		for j := range a.s.Files {
			if a.s.Files[j].ID == t.FileID {
				a.s.Files[j].Status = t.Status
			}
		}
		return a.saveLocked()
	}
	return errors.New("找不到任务")
}
func (a *App) copyLocalSubtitles(s Source, f MediaFile) {
	p, err := localPath(s, f.SourceKey)
	if err != nil {
		return
	}
	stem := strings.TrimSuffix(p, filepath.Ext(p))
	for _, ext := range []string{".srt", ".vtt"} {
		sub := stem + ext
		info, e := os.Lstat(sub)
		if e != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 {
			continue
		}
		b, e := os.ReadFile(sub)
		if e == nil {
			_ = a.saveSubtitle(f.ID, filepath.Base(sub), b)
		}
	}
}
func (a *App) saveSubtitle(fileID, name string, b []byte) error {
	if !utf8.Valid(b) {
		return errors.New("字幕需要使用 UTF-8 编码，请转换后再添加")
	}
	ext := strings.ToLower(filepath.Ext(name))
	if ext != ".srt" && ext != ".vtt" {
		return errors.New("目前支持 UTF-8 编码的 SRT、VTT 字幕")
	}
	if len(b) > 8<<20 {
		return errors.New("字幕文件不能超过 8 MB")
	}
	if _, err := a.file(fileID); err != nil {
		return err
	}
	target := filepath.Join(a.dir, "subtitles", fileID, safeName(name))
	if err := atomicWrite(target, b, 0644); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	subs := a.s.SubtitlePaths[fileID]
	replaced := false
	for i := range subs {
		if subs[i].Name == name {
			subs[i].Path = target
			replaced = true
		}
	}
	if !replaced {
		subs = append(subs, SavedSubtitle{Name: name, Path: target})
	}
	a.s.SubtitlePaths[fileID] = subs
	for i := range a.s.Files {
		if a.s.Files[i].ID == fileID {
			a.s.Files[i].Subtitles = []Subtitle{}
			for _, sub := range subs {
				a.s.Files[i].Subtitles = append(a.s.Files[i].Subtitles, Subtitle{Name: sub.Name, Path: sub.Path})
			}
		}
	}
	return a.saveLocked()
}
