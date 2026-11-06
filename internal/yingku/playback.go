package yingku

import (
	"errors"
	"net/http"
	"time"
)

type webPlaybackActivity struct {
	fileID  string
	expires time.Time
}

func (a *App) beginMediaRead(id string) (MediaFile, func(), error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, f := range a.s.Files {
		if f.ID != id {
			continue
		}
		if a.streamReaders == nil {
			a.streamReaders = map[string]int{}
		}
		a.streamReaders[id]++
		return f, func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			a.streamReaders[id]--
			if a.streamReaders[id] == 0 {
				delete(a.streamReaders, id)
			}
		}, nil
	}
	return MediaFile{}, nil, errors.New("文件不存在")
}

const webPlaybackTimeout = 2 * time.Minute

func (a *App) registerWebPlaybackLocked(id string) string {
	if a.webPlayback == nil {
		a.webPlayback = map[string]webPlaybackActivity{}
	}
	now := time.Now()
	for key, activity := range a.webPlayback {
		if now.After(activity.expires) {
			delete(a.webPlayback, key)
		}
	}
	token := uid()
	a.webPlayback[token] = webPlaybackActivity{id, now.Add(webPlaybackTimeout)}
	return token
}

// Each explicit playback takes ownership. Delayed requests from an old web
// player or TV session can no longer overwrite the newer playback's position.
func (a *App) claimProgressLocked(fileID string) (string, error) {
	for i := range a.s.Files {
		file := &a.s.Files[i]
		if file.ID != fileID {
			continue
		}
		before := *file
		file.ProgressToken = uid()
		file.LastPlayed = time.Now().UTC().Format(time.RFC3339Nano)
		if err := a.saveLocked(); err != nil {
			*file = before
			return "", err
		}
		return file.ProgressToken, nil
	}
	return "", errors.New("找不到视频文件")
}

func (a *App) claimCastProgressLocked(s *CastSession) {
	token, err := a.claimProgressLocked(s.FileID)
	if err != nil {
		s.progressToken = ""
		s.Warning = "电视正在播放，但观看进度暂时无法保存"
		return
	}
	s.progressToken = token
}

func (a *App) playbackRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/files/{id}/playback", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Token         string `json:"token"`
			TrackActivity bool   `json:"trackActivity"`
		}
		if err := readJSON(w, r, &body); err != nil {
			fail(w, err)
			return
		}
		f, err := a.file(r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		if f.Status != "completed" {
			fail(w, errors.New("视频尚未入库完成"))
			return
		}
		health := a.waitFileAvailability(r.Context(), f)
		if health.Status != "available" {
			jsonResponse(w, 409, map[string]any{"error": health.Message, "availability": health})
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		for _, live := range a.s.Files {
			if live.ID == f.ID && live.Destination == f.Destination && live.Status == "completed" {
				token := live.ProgressToken
				if token == "" || body.Token != token {
					var e error
					token, e = a.claimProgressLocked(f.ID)
					if e != nil {
						fail(w, e)
						return
					}
				}
				result := map[string]any{"token": token, "position": live.Position, "duration": live.Duration}
				if body.TrackActivity {
					result["activityToken"] = a.registerWebPlaybackLocked(f.ID)
				}
				jsonResponse(w, 200, result)
				return
			}
		}
		fail(w, errors.New("视频记录已变化，请重新打开"))
	})
	mux.HandleFunc("POST /api/files/{id}/playback/activity", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Token string `json:"token"`
			Ended bool   `json:"ended"`
		}
		if err := readJSON(w, r, &body); err != nil {
			fail(w, err)
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if active, ok := a.webPlayback[body.Token]; ok && active.fileID == r.PathValue("id") {
			if body.Ended {
				delete(a.webPlayback, body.Token)
			} else {
				active.expires = time.Now().Add(webPlaybackTimeout)
				a.webPlayback[body.Token] = active
			}
		}
		jsonResponse(w, http.StatusOK, map[string]bool{"ok": true})
	})
}
