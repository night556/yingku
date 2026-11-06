package yingku

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	tsCacheBudget = 12 << 30
	tsJobIdle     = 90 * time.Second
	tsJobLimit    = 15 * time.Minute
)

type tsPlaybackCache struct {
	mu      sync.Mutex
	job     *tsPlaybackJob
	readers map[string]int
	closing bool
}

type tsPlaybackJob struct {
	key       string
	done      chan struct{}
	cancel    context.CancelFunc
	idle      *time.Timer
	err       error
	started   time.Time
	stage     string
	duration  float64
	processed float64
}

type tsPlaybackSource struct {
	path string
	info os.FileInfo
	key  string
}

func (a *App) tsSource(id string) (tsPlaybackSource, func(), error) {
	file, release, err := a.beginMediaRead(id)
	if err != nil {
		return tsPlaybackSource{}, nil, err
	}
	if file.Status != "completed" || !strings.EqualFold(filepath.Ext(file.Destination), ".ts") {
		release()
		return tsPlaybackSource{}, nil, errors.New("兼容播放只适用于已入库的 TS 视频")
	}
	info, err := os.Lstat(file.Destination)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		release()
		return tsPlaybackSource{}, nil, errors.New("无法读取 TS 原文件，请检查文件位置")
	}
	key := sha256.Sum256([]byte(fmt.Sprintf("v1\x00%s\x00%s\x00%d\x00%d", id, file.Destination, info.Size(), info.ModTime().UnixNano())))
	return tsPlaybackSource{file.Destination, info, hex.EncodeToString(key[:])}, release, nil
}

func (a *App) tsCacheDir() string { return filepath.Join(a.dir, "ts-playback") }

func (a *App) tsPlaybackRoutes(mux *http.ServeMux) {
	const route = "/api/files/{id}/ts-playback"
	mux.HandleFunc("POST "+route, a.tsPlaybackStatus)
	mux.HandleFunc("GET "+route, a.tsPlaybackStatus)
	mux.HandleFunc("GET "+route+"/{key}", a.serveTSPlayback)
}

func (a *App) tsPlaybackStatus(w http.ResponseWriter, r *http.Request) {
	source, release, err := a.tsSource(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	defer func() {
		if release != nil {
			release()
		}
	}()
	w.Header().Set("Cache-Control", "no-store")
	cache := &a.tsPlayback
	cache.mu.Lock()
	defer cache.mu.Unlock()
	path := filepath.Join(a.tsCacheDir(), source.key+".mp4")
	if info, statErr := os.Lstat(path); statErr == nil && info.Mode().IsRegular() && info.Size() > 0 {
		now := time.Now()
		_ = os.Chtimes(path, now, now)
		jsonResponse(w, http.StatusOK, map[string]string{"status": "ready", "key": source.key})
		return
	}
	job := cache.job
	if job != nil && job.key == source.key && job.cancel != nil {
		job.idle.Reset(tsJobIdle)
		job.writeProgress(w)
		return
	}
	if r.Method == http.MethodGet {
		if job != nil && job.key == source.key && job.err != nil {
			fail(w, job.err)
		} else {
			fail(w, errors.New("播放缓存已变化，请重新打开视频"))
		}
		return
	}
	if cache.closing || job != nil && job.cancel != nil {
		jsonResponse(w, http.StatusServiceUnavailable, map[string]string{"error": "兼容播放服务正忙，请稍后重新打开视频"})
		return
	}
	ffmpeg, ffmpegErr := exec.LookPath("ffmpeg")
	ffprobe, ffprobeErr := exec.LookPath("ffprobe")
	if ffmpegErr != nil || ffprobeErr != nil {
		fail(w, errors.New("服务器需要 FFmpeg 和 ffprobe 才能兼容播放 TS"))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), tsJobLimit)
	job = &tsPlaybackJob{key: source.key, done: make(chan struct{}), cancel: cancel,
		idle: time.AfterFunc(tsJobIdle, cancel), started: time.Now(), stage: "probing"}
	cache.job = job
	go a.buildTSPlayback(ctx, job, source, path, ffmpeg, ffprobe, release)
	release = nil // The job holds the media read until FFmpeg has exited.
	job.writeProgress(w)
}

// The caller holds the cache mutex while taking a snapshot of the job.
func (job *tsPlaybackJob) writeProgress(w http.ResponseWriter) {
	jsonResponse(w, http.StatusAccepted, map[string]any{
		"status": "preparing", "stage": job.stage, "processed": job.processed,
		"duration": job.duration, "elapsed": time.Since(job.started).Seconds(),
	})
}

func (a *App) buildTSPlayback(ctx context.Context, job *tsPlaybackJob, source tsPlaybackSource, path, ffmpeg, ffprobe string, release func()) {
	err := a.remuxTS(ctx, job, source, path, ffmpeg, ffprobe)
	if ctx.Err() != nil && err != nil {
		err = errors.New("TS 播放准备已取消或超时，请重新打开视频")
	}
	release()
	a.tsPlayback.mu.Lock()
	defer a.tsPlayback.mu.Unlock()
	job.idle.Stop()
	job.cancel()
	job.cancel, job.err = nil, err
	close(job.done)
}

func (a *App) stopTSPlayback() {
	cache := &a.tsPlayback
	cache.mu.Lock()
	cache.closing = true
	job := cache.job
	if job != nil && job.cancel != nil {
		job.cancel()
	}
	cache.mu.Unlock()
	if job != nil {
		select {
		case <-job.done:
		case <-time.After(3 * time.Second):
		}
	}
}

func (a *App) remuxTS(ctx context.Context, job *tsPlaybackJob, source tsPlaybackSource, path, ffmpeg, ffprobe string) error {
	// Force local MPEG-TS input so even a mislabeled file cannot load a playlist or URL.
	input := []string{"-protocol_whitelist", "file", "-f", "mpegts", "-i", source.path}
	probe := append([]string{"-v", "error", "-show_entries", "stream=index,codec_name,codec_type:format=duration", "-of", "json"}, input...)
	result, err := exec.CommandContext(ctx, ffprobe, probe...).Output()
	if err != nil {
		return errors.New("无法分析 TS 文件，请检查文件是否完整")
	}
	var media struct {
		Streams []struct {
			Index int    `json:"index"`
			Codec string `json:"codec_name"`
			Type  string `json:"codec_type"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(result, &media); err != nil {
		return errors.New("无法识别 TS 文件的音视频编码")
	}
	video, audio, hasAudio := -1, -1, false
	for _, stream := range media.Streams {
		switch stream.Type {
		case "video":
			if video < 0 && stream.Codec == "h264" {
				video = stream.Index
			}
		case "audio":
			hasAudio = true
			if audio < 0 && stream.Codec == "aac" {
				audio = stream.Index
			}
		}
	}
	if video < 0 || hasAudio && audio < 0 {
		return errors.New("这集 TS 的编码需要转码。当前兼容播放支持 H.264 视频和 AAC 音轨")
	}
	const overhead = 64 << 20
	limit := min(source.info.Size(), tsCacheBudget-overhead) + overhead
	if err := a.reserveTSCache(limit); err != nil {
		return err
	}
	// A single job owns this file. The next job also clears it after an unclean shutdown.
	partial := filepath.Join(a.tsCacheDir(), ".preparing.part")
	if err := os.Remove(partial); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("无法清理 TS 播放临时文件")
	}
	defer os.Remove(partial)
	args := append([]string{"-nostdin", "-v", "error", "-n", "-nostats", "-stats_period", "0.5", "-progress", "pipe:1"}, input...)
	args = append(args, "-map", fmt.Sprintf("0:%d", video))
	if audio >= 0 {
		args = append(args, "-map", fmt.Sprintf("0:%d", audio))
	}
	args = append(args, "-c", "copy", "-movflags", "+faststart", "-fs", strconv.FormatInt(limit, 10), "-f", "mp4", partial)
	duration, _ := strconv.ParseFloat(media.Format.Duration, 64)
	a.tsPlayback.mu.Lock()
	job.stage = "remuxing"
	if duration > 0 && duration <= 1e8 {
		job.duration = duration
	}
	a.tsPlayback.mu.Unlock()
	if err := a.runTSRemux(exec.CommandContext(ctx, ffmpeg, args...), job); err != nil {
		return errors.New("TS 换封装失败，请检查文件完整性和服务器磁盘空间")
	}
	a.tsPlayback.mu.Lock()
	job.stage = "finalizing"
	a.tsPlayback.mu.Unlock()
	output, err := os.Stat(partial)
	if err != nil || output.Size() == 0 || output.Size() >= limit {
		return errors.New("TS 播放缓存生成失败，文件可能超过缓存容量")
	}
	current, err := os.Lstat(source.path)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(source.info, current) ||
		current.Size() != source.info.Size() || !current.ModTime().Equal(source.info.ModTime()) {
		return errors.New("TS 原文件在准备期间发生变化，请重新打开视频")
	}
	if err := os.Rename(partial, path); err != nil {
		return errors.New("无法保存 TS 播放缓存，请检查数据目录")
	}
	return nil
}

func (a *App) runTSRemux(cmd *exec.Cmd, job *tsPlaybackJob) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		value, ok := strings.CutPrefix(scanner.Text(), "out_time_us=")
		if !ok {
			continue
		}
		micros, err := strconv.ParseInt(value, 10, 64)
		if err != nil || micros < 0 {
			continue
		}
		a.tsPlayback.mu.Lock()
		job.processed = max(job.processed, float64(micros)/1e6)
		a.tsPlayback.mu.Unlock()
	}
	if err := scanner.Err(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	return cmd.Wait()
}

func (a *App) reserveTSCache(required int64) error {
	cache := &a.tsPlayback
	cache.mu.Lock()
	defer cache.mu.Unlock()
	dir := a.tsCacheDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errors.New("无法创建 TS 播放缓存目录")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return errors.New("无法读取 TS 播放缓存目录")
	}
	var files []os.FileInfo
	var total int64
	for _, entry := range entries {
		key, ok := strings.CutSuffix(entry.Name(), ".mp4")
		if !ok || len(key) != sha256.Size*2 {
			continue
		}
		if _, err := hex.DecodeString(key); err != nil {
			continue
		}
		if info, err := entry.Info(); err == nil && info.Mode().IsRegular() {
			files = append(files, info)
			total += info.Size()
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].ModTime().Before(files[j].ModTime()) })
	recent := time.Now().Add(-webPlaybackTimeout)
	for _, info := range files {
		if total+required <= tsCacheBudget {
			break
		}
		if cache.readers[info.Name()] == 0 && info.ModTime().Before(recent) && os.Remove(filepath.Join(dir, info.Name())) == nil {
			total -= info.Size()
		}
	}
	if total+required > tsCacheBudget {
		return errors.New("近期使用的 TS 播放缓存占满了空间，请稍后重试")
	}
	return nil
}

func (a *App) serveTSPlayback(w http.ResponseWriter, r *http.Request) {
	source, release, err := a.tsSource(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer release()
	if source.key != r.PathValue("key") {
		http.NotFound(w, r)
		return
	}
	name := source.key + ".mp4"
	path := filepath.Join(a.tsCacheDir(), name)
	cache := &a.tsPlayback
	cache.mu.Lock()
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		cache.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		cache.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	if cache.readers == nil {
		cache.readers = make(map[string]int)
	}
	cache.readers[name]++
	cache.mu.Unlock()
	defer func() {
		file.Close()
		cache.mu.Lock()
		defer cache.mu.Unlock()
		now := time.Now()
		_ = os.Chtimes(path, now, now)
		cache.readers[name]--
		if cache.readers[name] == 0 {
			delete(cache.readers, name)
		}
	}()
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, name, time.Time{}, file)
}
