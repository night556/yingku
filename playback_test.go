package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestPlaybackOwnershipProtectsTVProgressAndAllowsWebContinuation(t *testing.T) {
	a, server, tv, device := castingFixture(t)
	first := request(t, server, a, "POST", "/api/files/cast-file/playback", map[string]any{}, 200)
	var oldToken string
	json.Unmarshal(first["token"], &oldToken)
	request(t, server, a, "POST", "/api/files/cast-file/progress", map[string]any{"position": 12, "duration": 3600, "token": oldToken}, 200)
	cast, err := a.startCast(context.Background(), device.ID, "cast-file", nil)
	if err != nil {
		t.Fatal(err)
	}
	tv.mu.Lock()
	tv.Position = "00:12:34"
	tv.mu.Unlock()
	request(t, server, a, "POST", "/api/dlna/sessions/"+cast.ID+"/sync", map[string]any{}, 200)
	for _, body := range []map[string]any{{"position": 12, "duration": 3600, "token": oldToken}, {"position": 0, "duration": 3600}} {
		reply := request(t, server, a, "POST", "/api/files/cast-file/progress", body, 200)
		if string(reply["ignored"]) != "true" {
			t.Fatal("old player progress accepted", reply)
		}
	}
	f, _ := a.file("cast-file")
	if f.Position != 754 {
		t.Fatal("TV progress overwritten", f.Position)
	}
	fresh := request(t, server, a, "POST", "/api/files/cast-file/playback", map[string]any{}, 200)
	var token string
	var position float64
	json.Unmarshal(fresh["token"], &token)
	json.Unmarshal(fresh["position"], &position)
	if position != 754 || token == oldToken || token == "" {
		t.Fatal("web did not take over latest TV progress")
	}
	request(t, server, a, "POST", "/api/files/cast-file/progress", map[string]any{"position": 800, "duration": 3600, "token": token}, 200)
	tv.mu.Lock()
	tv.Position = "00:12:35"
	tv.mu.Unlock()
	if _, err := a.castStatus(context.Background(), cast.ID); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.saveCastProgressLocked(a.casts[cast.ID])
	a.mu.Unlock()
	f, _ = a.file("cast-file")
	if f.Position != 800 {
		t.Fatal("old TV session overwrote web playback", f.Position)
	}
	again := request(t, server, a, "POST", "/api/files/cast-file/playback", map[string]any{"token": token}, 200)
	var retained string
	json.Unmarshal(again["token"], &retained)
	if retained != token {
		t.Fatal("pause/resume changed ownership unnecessarily")
	}
	if _, err := a.castControl(context.Background(), cast.ID, "seek", 850); err != nil {
		t.Fatal(err)
	}
	f, _ = a.file("cast-file")
	if f.Position != 850 || f.ProgressToken == token {
		t.Fatal("explicit TV seek did not reclaim progress")
	}
	// Once web playback takes over again, an observed native pause/play on the
	// TV is another explicit playback transition, unlike an old PLAYING poll.
	request(t, server, a, "POST", "/api/files/cast-file/playback", map[string]any{}, 200)
	tv.mu.Lock()
	tv.Status = "PAUSED_PLAYBACK"
	tv.mu.Unlock()
	a.castStatus(context.Background(), cast.ID)
	tv.mu.Lock()
	tv.Status = "PLAYING"
	tv.Position = "00:14:20"
	tv.mu.Unlock()
	a.castStatus(context.Background(), cast.ID)
	a.mu.Lock()
	a.saveCastProgressLocked(a.casts[cast.ID])
	a.mu.Unlock()
	f, _ = a.file("cast-file")
	if f.Position != 860 {
		t.Fatal("native TV play did not reclaim progress", f.Position)
	}
}

func TestCastSwitchProtectsBothOldAndNewEpisodes(t *testing.T) {
	a, server, tv, device := castSeriesFixture(t, true)
	old := request(t, server, a, "POST", "/api/files/cast-2/playback", map[string]any{}, 200)
	var oldToken string
	json.Unmarshal(old["token"], &oldToken)
	s := queueStart(t, a, tv, device)
	if _, err := a.castControl(context.Background(), s.ID, "next", 0); err != nil {
		t.Fatal(err)
	}
	request(t, server, a, "POST", "/api/files/cast-2/progress", map[string]any{"position": 99, "duration": 600, "token": oldToken}, 200)
	f, _ := a.file("cast-2")
	if f.Position == 99 {
		t.Fatal("stale web position replaced newly cast episode")
	}
}
