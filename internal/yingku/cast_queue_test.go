package yingku

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func castSeriesFixture(t *testing.T, native bool) (*App, *httptest.Server, *mockTV, DLNADevice) {
	t.Helper()
	a, server, tv, device := castingFixture(t)
	a.mu.Lock()
	path := a.s.Files[0].Destination
	a.s.Items = []Item{{ID: "cast-show", Title: "投屏剧集测试", Type: "tv"}}
	a.s.Files = []MediaFile{
		{ID: "cast-201", ItemID: "cast-show", Name: "第二季第1集.mp4", Season: 2, Episode: 1, Destination: path, Status: "completed", Reference: true},
		{ID: "cast-34", ItemID: "cast-show", Name: "第3–4集合并.mp4", Season: 1, Episode: 3, EpisodeEnd: 4, Destination: path, Status: "completed", Reference: true},
		{ID: "cast-1", ItemID: "cast-show", Name: "第1集.mp4", Season: 1, Episode: 1, Destination: path, Status: "completed", Reference: true},
		{ID: "cast-2", ItemID: "cast-show", Name: "第2集.mp4", Season: 1, Episode: 2, Destination: path, Status: "completed", Reference: true},
	}
	a.mu.Unlock()
	tv.mu.Lock()
	tv.SupportsNext = native
	tv.ResetOnSet = true
	tv.Duration = "00:01:00"
	tv.Position = "00:00:00"
	tv.mu.Unlock()
	return a, server, tv, device
}

func queueStart(t *testing.T, a *App, tv *mockTV, device DLNADevice) CastSession {
	t.Helper()
	s, err := a.startCast(context.Background(), device.ID, "cast-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	return queueObserve(t, a, tv, s.ID, "PLAYING", "00:00:10")
}
func queueObserve(t *testing.T, a *App, tv *mockTV, id, status, position string) CastSession {
	t.Helper()
	tv.mu.Lock()
	tv.Status = status
	tv.Position = position
	tv.mu.Unlock()
	s, err := a.castStatus(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func castHTTPStatus(t *testing.T, address string) int {
	t.Helper()
	response, err := http.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	return response.StatusCode
}

func TestCastNativeQueueTracksPromotionAndScopesGrants(t *testing.T) {
	a, _, tv, device := castSeriesFixture(t, true)
	s := queueStart(t, a, tv, device)
	if !s.Series || !s.AutoNext || !s.NextOnDevice || s.Previous != nil || s.Next.ID != "cast-2" {
		t.Fatalf("unexpected session: %+v", s)
	}
	tv.mu.Lock()
	firstURI, nextURI, metadata := tv.URI, tv.NextURI, tv.NextMetadata
	initialSets := strings.Count(strings.Join(tv.Actions, ","), "SetAVTransportURI")
	tv.mu.Unlock()
	if castHTTPStatus(t, nextURI) != 200 || !strings.Contains(metadata, "第2集.mp4") {
		t.Fatal("next episode cannot be prefetched")
	}
	s, err := a.castControl(context.Background(), s.ID, "auto-next", 0)
	if err != nil {
		t.Fatal(err)
	}
	if s.AutoNext || s.NextOnDevice || castHTTPStatus(t, nextURI) != 404 || castHTTPStatus(t, firstURI) != 200 {
		t.Fatal("disabling autoplay did not revoke only the queued grant")
	}
	s, err = a.castControl(context.Background(), s.ID, "auto-next", 1)
	if err != nil {
		t.Fatal(err)
	}
	s = queueObserve(t, a, tv, s.ID, "PLAYING", "00:00:58")
	a.mu.Lock()
	a.casts[s.ID].lastPlayingAt = time.Now().Add(-3 * time.Second)
	a.mu.Unlock()
	tv.mu.Lock()
	tv.URI = tv.NextURI
	tv.NextURI = ""
	tv.mu.Unlock()
	s = queueObserve(t, a, tv, s.ID, "PLAYING", "00:00:01")
	if s.FileID != "cast-2" || s.Previous.ID != "cast-1" || s.Next.ID != "cast-34" || !s.NextOnDevice {
		t.Fatalf("native promotion not adopted: %+v", s)
	}
	tv.mu.Lock()
	setCount := strings.Count(strings.Join(tv.Actions, ","), "SetAVTransportURI")
	tv.mu.Unlock()
	if setCount != initialSets || castHTTPStatus(t, firstURI) != 404 {
		t.Fatal("native promotion restarted playback or retained the old grant")
	}
	data, _ := json.Marshal(s)
	if strings.Contains(string(data), "/cast/") || strings.Contains(string(data), "/av/control") {
		t.Fatal("public session leaked grants or control URLs")
	}
	f, _ := a.file("cast-1")
	if f.Position != 60 {
		t.Fatal("completed episode progress was not saved")
	}
}

func TestCastServerContinuationAndManualCrossSeasonNavigation(t *testing.T) {
	a, _, tv, device := castSeriesFixture(t, false)
	s := queueStart(t, a, tv, device)
	if s.NextOnDevice {
		t.Fatal("unsupported queue advertised")
	}
	s = queueObserve(t, a, tv, s.ID, "PLAYING", "00:00:58")
	s = queueObserve(t, a, tv, s.ID, "STOPPED", "00:01:00")
	if s.FileID != "cast-2" || s.Status != "PLAYING" || s.Position != 0 {
		t.Fatalf("server did not continue: %+v", s)
	}
	for _, expected := range []string{"cast-34", "cast-201"} {
		var err error
		s, err = a.castControl(context.Background(), s.ID, "next", 0)
		if err != nil || s.FileID != expected {
			t.Fatalf("next: %+v %v", s, err)
		}
	}
	if s.Next != nil {
		t.Fatal("last episode must not wrap")
	}
	if _, err := a.castControl(context.Background(), s.ID, "next", 0); err == nil {
		t.Fatal("last episode next accepted")
	}
	s, err := a.castControl(context.Background(), s.ID, "previous", 0)
	if err != nil || s.FileID != "cast-34" || s.Next.Season != 2 {
		t.Fatalf("previous across season failed: %+v %v", s, err)
	}
}

func TestCastEndDetectionDoesNotTreatPauseOrStopAsEOF(t *testing.T) {
	for _, action := range []string{"remote-stop", "pause", "explicit-stop", "disabled", "missing-next", "uncertain-reset"} {
		t.Run(action, func(t *testing.T) {
			a, _, tv, device := castSeriesFixture(t, false)
			s := queueStart(t, a, tv, device)
			s = queueObserve(t, a, tv, s.ID, "PLAYING", "00:00:58")
			switch action {
			case "pause":
				if _, err := a.castControl(context.Background(), s.ID, "pause", 0); err != nil {
					t.Fatal(err)
				}
			case "explicit-stop":
				if _, err := a.castControl(context.Background(), s.ID, "stop", 0); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				if _, err := a.castControl(context.Background(), s.ID, "auto-next", 0); err != nil {
					t.Fatal(err)
				}
			case "missing-next":
				a.mu.Lock()
				for i := range a.s.Files {
					if a.s.Files[i].ID == "cast-2" {
						a.s.Files[i].Status = "queued"
					}
				}
				a.mu.Unlock()
			}
			position := "00:01:00"
			if action == "remote-stop" {
				position = "00:00:20"
			}
			if action == "uncertain-reset" {
				position = "00:00:00"
			}
			s = queueObserve(t, a, tv, s.ID, "STOPPED", position)
			if s.FileID != "cast-1" || s.Status != "STOPPED" {
				t.Fatalf("unexpected continuation after %s: %+v", action, s)
			}
		})
	}
}

func TestCastEOFResetAndNativeQueueGrace(t *testing.T) {
	t.Run("reset", func(t *testing.T) {
		a, _, tv, device := castSeriesFixture(t, false)
		s := queueStart(t, a, tv, device)
		s = queueObserve(t, a, tv, s.ID, "PLAYING", "00:00:58")
		a.mu.Lock()
		a.casts[s.ID].lastPlayingAt = time.Now().Add(-3 * time.Second)
		a.mu.Unlock()
		s = queueObserve(t, a, tv, s.ID, "STOPPED", "00:00:00")
		if s.FileID != "cast-2" {
			t.Fatalf("EOF reset not recognized: %+v", s)
		}
	})
	t.Run("native-grace", func(t *testing.T) {
		a, _, tv, device := castSeriesFixture(t, true)
		s := queueStart(t, a, tv, device)
		s = queueObserve(t, a, tv, s.ID, "PLAYING", "00:00:58")
		s = queueObserve(t, a, tv, s.ID, "STOPPED", "00:01:00")
		if s.FileID != "cast-1" || s.Status != "TRANSITIONING" {
			t.Fatal("did not allow renderer time to transition")
		}
		a.mu.Lock()
		a.casts[s.ID].terminalSince = time.Now().Add(-9 * time.Second)
		a.mu.Unlock()
		s = queueObserve(t, a, tv, s.ID, "STOPPED", "00:01:00")
		if s.FileID != "cast-2" || s.Status != "PLAYING" {
			t.Fatalf("nonfunctional native queue did not fall back: %+v", s)
		}
		s = queueObserve(t, a, tv, s.ID, "PLAYING", "00:00:01")
		if s.NextOnDevice {
			t.Fatal("nonfunctional native queue was re-enabled after falling back")
		}
	})
}

func TestCastQueueRejectsPendingFilesClearFailuresAndOtherControllers(t *testing.T) {
	a, _, tv, device := castSeriesFixture(t, true)
	s := queueStart(t, a, tv, device)
	tv.mu.Lock()
	tv.Faults = map[string]string{"ClearNext": "701"}
	tv.mu.Unlock()
	if _, err := a.castControl(context.Background(), s.ID, "auto-next", 0); err == nil {
		t.Fatal("disabling an uncleared native queue must not pretend to succeed")
	}
	a.mu.Lock()
	enabled := a.casts[s.ID].AutoNext
	a.mu.Unlock()
	if !enabled {
		t.Fatal("failed toggle not rolled back")
	}
	tv.mu.Lock()
	tv.Faults = nil
	tv.URI = "http://127.0.0.1:1234/another-app.mp4"
	tv.mu.Unlock()
	s = queueObserve(t, a, tv, s.ID, "PLAYING", "00:00:10")
	if s.Status != "STOPPED" || !strings.Contains(s.Warning, "其他内容") {
		t.Fatal("another controller was overwritten")
	}
	// A pending adjacent file remains in ordering; later completed files cannot be skipped.
	a2, _, tv2, device2 := castSeriesFixture(t, true)
	a2.mu.Lock()
	for i := range a2.s.Files {
		if a2.s.Files[i].ID == "cast-2" {
			a2.s.Files[i].Status = "queued"
		}
	}
	a2.mu.Unlock()
	s2 := queueStart(t, a2, tv2, device2)
	if s2.Next == nil || s2.Next.Ready || s2.NextOnDevice {
		t.Fatal("pending adjacent episode was skipped")
	}
	if _, err := a2.castControl(context.Background(), s2.ID, "next", 0); err == nil {
		t.Fatal("pending next accepted")
	}
}

func TestCastContinuesWithNoPhoneRequests(t *testing.T) {
	a, _, tv, device := castSeriesFixture(t, false)
	s, err := a.startCast(context.Background(), device.ID, "cast-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	tv.mu.Lock()
	tv.Position = "00:00:58"
	tv.mu.Unlock()
	waitFor(t, 8*time.Second, func() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.casts[s.ID].observedPlaying })
	tv.mu.Lock()
	tv.Status = "STOPPED"
	tv.Position = "00:01:00"
	tv.mu.Unlock()
	waitFor(t, 8*time.Second, func() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.casts[s.ID].FileID == "cast-2" })
}

func TestCastNativeSkipKeepsProgressAndUncertainStopDoesNotResume(t *testing.T) {
	t.Run("remote-next", func(t *testing.T) {
		a, _, tv, device := castSeriesFixture(t, true)
		s := queueStart(t, a, tv, device)
		tv.mu.Lock()
		tv.URI = tv.NextURI
		tv.NextURI = ""
		tv.mu.Unlock()
		s = queueObserve(t, a, tv, s.ID, "PLAYING", "00:00:01")
		file, _ := a.file("cast-1")
		if s.FileID != "cast-2" || file.Position != 10 {
			t.Fatal("remote skip incorrectly marked the previous episode completed")
		}
	})
	t.Run("native-uncertain-stop", func(t *testing.T) {
		a, _, tv, device := castSeriesFixture(t, true)
		s := queueStart(t, a, tv, device)
		s = queueObserve(t, a, tv, s.ID, "PLAYING", "00:00:58")
		s = queueObserve(t, a, tv, s.ID, "STOPPED", "00:00:00")
		a.mu.Lock()
		a.casts[s.ID].terminalSince = time.Now().Add(-9 * time.Second)
		a.casts[s.ID].lastPlayingAt = time.Now().Add(-3 * time.Second)
		a.mu.Unlock()
		s = queueObserve(t, a, tv, s.ID, "STOPPED", "00:00:00")
		if s.FileID != "cast-1" || s.Status != "STOPPED" {
			t.Fatal("waiting for a native queue turned an uncertain stop into EOF")
		}
	})
}

func TestCastQueueAPIAndStartDisabled(t *testing.T) {
	a, server, tv, device := castSeriesFixture(t, true)
	response := request(t, server, a, "POST", "/api/dlna/cast", map[string]any{"deviceId": device.ID, "fileId": "cast-1", "autoNext": false}, 200)
	var id string
	_ = json.Unmarshal(response["id"], &id)
	s := queueObserve(t, a, tv, id, "PLAYING", "00:00:10")
	if s.AutoNext || s.NextOnDevice {
		t.Fatal("initial disabled setting ignored")
	}
	request(t, server, a, "POST", "/api/dlna/sessions/"+id, map[string]any{"action": "auto-next", "value": 1}, 200)
	request(t, server, a, "POST", "/api/dlna/sessions/"+id, map[string]any{"action": "next"}, 200)
	response = request(t, server, a, "GET", "/api/dlna/sessions/"+id, nil, 200)
	var fileID string
	_ = json.Unmarshal(response["fileId"], &fileID)
	if fileID != "cast-2" {
		t.Fatal("HTTP navigation did not retain session ID")
	}
}

func TestCastReplacementStopsStrictTVAndPlaysSelectedEpisode(t *testing.T) {
	a, _, tv, device := castSeriesFixture(t, true)
	first := queueStart(t, a, tv, device)
	tv.mu.Lock()
	oldURI, oldNext := tv.URI, tv.NextURI
	tv.RequireStop = true
	firstAction := len(tv.Actions)
	tv.mu.Unlock()

	second, err := a.startCast(context.Background(), device.ID, "cast-2", nil)
	if err != nil || second.FileID != "cast-2" || second.Status != "PLAYING" || second.ID == first.ID {
		t.Fatalf("expected the selected episode to replace the first cast: %+v (%v)", second, err)
	}
	tv.mu.Lock()
	actions, uri := strings.Join(tv.Actions[firstAction:], ","), tv.URI
	tv.mu.Unlock()
	if !strings.Contains(actions, "SetAVTransportURI,Stop,SetAVTransportURI,Play") || uri == oldURI || uri == oldNext {
		t.Fatalf("expected an automatic stop and fresh URI after 701, got %s, %s", actions, uri)
	}
	if castHTTPStatus(t, uri) != 200 || castHTTPStatus(t, oldURI) != 404 || castHTTPStatus(t, oldNext) != 404 {
		t.Fatal("the replacement must revoke the previous video and queued grant")
	}
}

func TestCastRejectedReplacementKeepsPreviousSession(t *testing.T) {
	a, _, tv, device := castSeriesFixture(t, false)
	first := queueStart(t, a, tv, device)
	tv.mu.Lock()
	oldURI := tv.URI
	tv.Faults = map[string]string{"SetAVTransportURI": "714"}
	tv.mu.Unlock()
	if _, err := a.startCast(context.Background(), device.ID, "cast-2", nil); err == nil {
		t.Fatal("expected the TV to reject the replacement")
	}
	snapshot, err := a.castControl(context.Background(), first.ID, "pause", 0)
	if err != nil || snapshot.FileID != "cast-1" || snapshot.Status != "PAUSED_PLAYBACK" || castHTTPStatus(t, oldURI) != 200 {
		t.Fatalf("the previous video should still be controllable: %+v (%v)", snapshot, err)
	}
}

func TestCastStopRevokesPreparedGrantEvenWhenQueueCannotClear(t *testing.T) {
	a, _, tv, device := castSeriesFixture(t, true)
	s := queueStart(t, a, tv, device)
	tv.mu.Lock()
	currentURI, nextURI := tv.URI, tv.NextURI
	tv.Faults = map[string]string{"ClearNext": "701"}
	tv.mu.Unlock()
	if _, err := a.startCast(context.Background(), device.ID, "cast-2", nil); err == nil {
		t.Fatal("replacement cast must not inherit an uncleared queue")
	}
	s, err := a.castControl(context.Background(), s.ID, "stop", 0)
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != "STOPPED" || s.NextOnDevice || castHTTPStatus(t, currentURI) != 404 || castHTTPStatus(t, nextURI) != 404 {
		t.Fatal("stop left a current or queued grant usable")
	}
	tv.mu.Lock()
	status := tv.Status
	tv.mu.Unlock()
	if status != "STOPPED" {
		t.Fatal("queue clear failure prevented the Stop command")
	}
}
