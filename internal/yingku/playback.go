package yingku

import (
	"errors"
	"net/http"
	"time"
)

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
			Token string `json:"token"`
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
				if body.Token != "" && body.Token == live.ProgressToken {
					jsonResponse(w, 200, map[string]any{"token": live.ProgressToken, "position": live.Position, "duration": live.Duration})
					return
				}
				token, e := a.claimProgressLocked(f.ID)
				if e != nil {
					fail(w, e)
					return
				}
				jsonResponse(w, 200, map[string]any{"token": token, "position": live.Position, "duration": live.Duration})
				return
			}
		}
		fail(w, errors.New("视频记录已变化，请重新打开"))
	})
}
