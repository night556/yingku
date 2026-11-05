package yingku

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type CastEpisode struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Season     int    `json:"season"`
	Episode    int    `json:"episode"`
	EpisodeEnd int    `json:"episodeEnd,omitempty"`
	Ready      bool   `json:"ready"`
}

// A renderer may prefetch only the single adjacent episode selected by this session.
type castGrant struct {
	fileID, token, address, metadata string
}

func castMetadata(file MediaFile, address string) string {
	return `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/"><item id="0" parentID="-1" restricted="1"><dc:title>` + xmlText(file.Name) + `</dc:title><upnp:class>object.item.videoItem</upnp:class><res protocolInfo="http-get:*:` + mediaType(file.Name) + `:*">` + xmlText(address) + `</res></item></DIDL-Lite>`
}

func (a *App) castNeighborsLocked(fileID string) (bool, string, *CastEpisode, *CastEpisode) {
	itemID := ""
	for _, file := range a.s.Files {
		if file.ID == fileID {
			itemID = file.ItemID
			break
		}
	}
	series := false
	for _, item := range a.s.Items {
		if item.ID == itemID {
			series = item.Type == "tv"
			break
		}
	}
	if !series {
		return false, itemID, nil, nil
	}
	files := []MediaFile{}
	for _, file := range a.s.Files {
		if file.ItemID == itemID {
			files = append(files, file)
		}
	}
	sort.Slice(files, func(i, j int) bool {
		a, b := files[i], files[j]
		if a.Season != b.Season {
			return a.Season < b.Season
		}
		if a.Episode != b.Episode {
			return a.Episode < b.Episode
		}
		if fileEpisodeEnd(a) != fileEpisodeEnd(b) {
			return fileEpisodeEnd(a) < fileEpisodeEnd(b)
		}
		return a.ID < b.ID
	})
	episode := func(index int) *CastEpisode {
		if index < 0 || index >= len(files) {
			return nil
		}
		file := files[index]
		health := a.fileAvailabilityLocked(file)
		return &CastEpisode{ID: file.ID, Name: file.Name, Season: file.Season, Episode: file.Episode, EpisodeEnd: file.EpisodeEnd, Ready: file.Status == "completed" && health.Status != "missing" && health.Status != "unreadable"}
	}
	for i, file := range files {
		if file.ID == fileID {
			return true, itemID, episode(i - 1), episode(i + 1)
		}
	}
	return true, itemID, nil, nil
}

func (a *App) castGrantFor(s *CastSession, fileID string) (*castGrant, MediaFile, error) {
	file, err := a.file(fileID)
	if err != nil || file.Status != "completed" {
		return nil, MediaFile{}, errors.New("这一集尚未入库完成")
	}
	info, err := os.Stat(file.Destination)
	if err != nil || !info.Mode().IsRegular() {
		return nil, MediaFile{}, errors.New("这一集的文件已移动或无法读取")
	}
	token := uid() + uid()
	address := s.baseURL + "/cast/" + token + "/video" + strings.ToLower(filepath.Ext(file.Name))
	return &castGrant{file.ID, token, address, castMetadata(file, address)}, file, nil
}

// The caller holds dlnaMu; no network operation may run while holding mu.
func (a *App) clearCastQueue(ctx context.Context, s *CastSession) error {
	a.mu.Lock()
	hasQueue := s.nextGrant != nil && s.queueSupported
	a.mu.Unlock()
	if hasQueue {
		if _, err := soap(ctx, s.device, false, "SetNextAVTransportURI", [][2]string{{"NextURI", ""}, {"NextURIMetaData", ""}}); err != nil {
			return errors.New("电视未接受清除下一集，请停止投屏后重试")
		}
	}
	a.mu.Lock()
	s.nextGrant = nil
	a.mu.Unlock()
	return nil
}

func (a *App) syncCastQueue(ctx context.Context, s *CastSession) error {
	a.mu.Lock()
	_, _, _, next := a.castNeighborsLocked(s.FileID)
	wanted := ""
	if s.AutoNext && !s.ended && s.uriConfirmed && next != nil && next.Ready {
		wanted = next.ID
	}
	current := s.nextGrant
	unsupported := s.queueTried && !s.queueSupported
	a.mu.Unlock()
	if current != nil && current.fileID == wanted {
		return nil
	}
	if current != nil {
		if err := a.clearCastQueue(ctx, s); err != nil {
			return err
		}
	}
	if wanted == "" || unsupported {
		return nil
	}
	grant, _, err := a.castGrantFor(s, wanted)
	if err != nil {
		return err
	}
	a.mu.Lock()
	s.nextGrant = grant
	a.mu.Unlock()
	_, err = soap(ctx, s.device, false, "SetNextAVTransportURI", [][2]string{{"NextURI", grant.address}, {"NextURIMetaData", grant.metadata}})
	a.mu.Lock()
	s.queueTried = true
	s.queueSupported = err == nil
	if err != nil {
		s.nextGrant = nil
	}
	a.mu.Unlock()
	// This action is optional. Rejection leaves ordinary playback and server continuation available.
	return nil
}

func (a *App) switchCastEpisode(ctx context.Context, s *CastSession, fileID string, completed bool) error {
	grant, file, err := a.castGrantFor(s, fileID)
	if err != nil {
		return err
	}
	if err = a.clearCastQueue(ctx, s); err != nil {
		return err
	}
	a.mu.Lock()
	if completed && s.Duration > 0 {
		s.Position = s.Duration
	}
	a.saveCastProgressLocked(s)
	// Publish the new grant before SetAVTransportURI: renderers may immediately fetch it.
	s.pendingGrant = grant
	a.mu.Unlock()
	_, err = soap(ctx, s.device, false, "SetAVTransportURI", [][2]string{{"CurrentURI", grant.address}, {"CurrentURIMetaData", grant.metadata}})
	if err != nil {
		a.mu.Lock()
		s.pendingGrant = nil
		a.mu.Unlock()
		return err
	}
	_, playErr := soap(ctx, s.device, false, "Play", [][2]string{{"Speed", "1"}})
	a.mu.Lock()
	s.token, s.FileID, s.mediaURL = grant.token, file.ID, grant.address
	if playErr == nil {
		a.claimCastProgressLocked(s)
	}
	s.pendingGrant = nil
	s.Name = file.Name
	s.Position = 0
	s.Duration = 0
	s.lastSaved = time.Time{}
	s.ended = false
	s.observedPlaying = false
	s.playingProgress = false
	s.uriConfirmed = false
	s.lastPlayingAt = time.Time{}
	s.lastPlayingPosition = 0
	s.terminalSince = time.Time{}
	s.Status = "PLAYING"
	s.Warning = ""
	if playErr != nil {
		s.Status = "STOPPED"
		s.ended = true
		s.Warning = "下一集已载入，电视未接受播放，请重新投屏"
	}
	a.mu.Unlock()
	if playErr != nil {
		return playErr
	}
	return a.syncCastQueue(ctx, s)
}

// Some TVs reset RelTime to zero at EOF. Require a recent PLAYING sample and enough
// elapsed playback time to reach the reported duration; a mid-episode Stop is never EOF.
func castReachedEnd(s *CastSession, position float64, validPosition bool, now time.Time) bool {
	if !s.observedPlaying || s.Duration <= 0 || s.lastPlayingAt.IsZero() {
		return false
	}
	elapsed := now.Sub(s.lastPlayingAt).Seconds()
	if elapsed < 0 || elapsed > 15 {
		return false
	}
	if validPosition && position >= s.Duration && position > s.lastPlayingPosition {
		return true
	}
	return validPosition && position == 0 && s.playingProgress && s.lastPlayingPosition > 0 && s.Duration-s.lastPlayingPosition <= 8 && s.lastPlayingPosition+elapsed >= s.Duration
}
