package yingku

import (
	"errors"
	"maps"
	"net/http"
)

// SkipSegment stores the two positions in seconds in the original video.
type SkipSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Season intros use positions from the start; outros use offsets from the end.
type SeasonSkipSegments struct {
	Intro        *SkipSegment `json:"intro,omitempty"`
	OutroFromEnd *SkipSegment `json:"outroFromEnd,omitempty"`
}

type skipSegmentSetting struct {
	Segment  *SkipSegment `json:"segment"`
	Disabled bool         `json:"disabled"`
}

func (s *SkipSegment) fromEnd(duration float64) *SkipSegment {
	if s == nil {
		return nil
	}
	return &SkipSegment{Start: duration - s.End, End: duration - s.Start}
}

func skipSegmentsOverlap(intro, outro *SkipSegment) bool {
	return intro != nil && outro != nil && intro.End > outro.Start
}

func (a *App) setSkipSegment(id, kind, scope string, setting skipSegmentSetting, duration float64) (skipSegmentSetting, error) {
	if kind != "intro" && kind != "outro" {
		return setting, errors.New("请选择片头或片尾")
	}
	if scope != "" && scope != "season" || scope == "season" && setting.Disabled {
		return setting, errors.New("请选择有效的标记范围")
	}
	if segment := setting.Segment; segment != nil && !(segment.Start >= 0 && segment.Start < segment.End &&
		segment.End <= duration && duration <= 1e8) {
		return setting, errors.New("起点必须早于终点，两个时间都要在视频时长内")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.s.Files {
		file := &a.s.Files[i]
		if file.ID != id {
			continue
		}
		if file.Status != "completed" {
			return setting, errors.New("视频尚未入库完成")
		}
		if scope == "season" || setting.Disabled {
			var series *Item
			for j := range a.s.Items {
				item := &a.s.Items[j]
				if item.ID == file.ItemID && item.Type == "tv" {
					series = item
					break
				}
			}
			if series == nil || file.Season <= 0 || file.Episode <= 0 || fileEpisodeEnd(*file) > file.Episode {
				return setting, errors.New("本季默认仅适用于单集正片")
			}
			if scope == "season" {
				return a.setSeasonSkipSegmentLocked(series, file.Season, kind, setting, duration)
			}
		}
		previous := *file
		updated := previous
		if kind == "intro" {
			updated.Intro, updated.IntroSkipDisabled = setting.Segment, setting.Disabled
		} else {
			updated.Outro, updated.OutroSkipDisabled = setting.Segment, setting.Disabled
		}
		if skipSegmentsOverlap(updated.Intro, updated.Outro) {
			return setting, errors.New("片头需要在片尾之前结束，两个片段不能重叠")
		}
		*file = updated
		if err := a.saveLocked(); err != nil {
			*file = previous
			return setting, err
		}
		return setting, nil
	}
	return setting, errors.New("找不到视频文件")
}

func (a *App) setSeasonSkipSegmentLocked(item *Item, season int, kind string, setting skipSegmentSetting, duration float64) (skipSegmentSetting, error) {
	previous := item.SkipSeasons
	marks := previous[season]
	if kind == "intro" {
		marks.Intro = setting.Segment
	} else {
		setting.Segment = setting.Segment.fromEnd(duration)
		marks.OutroFromEnd = setting.Segment
	}
	if setting.Segment != nil && skipSegmentsOverlap(marks.Intro, marks.OutroFromEnd.fromEnd(duration)) {
		return setting, errors.New("本季片头需要在片尾之前结束，两个片段不能重叠")
	}
	// Published state and library transactions can share the previous map.
	updated := maps.Clone(previous)
	if marks.Intro == nil && marks.OutroFromEnd == nil {
		delete(updated, season)
	} else {
		if updated == nil {
			updated = make(map[int]SeasonSkipSegments)
		}
		updated[season] = marks
	}
	item.SkipSeasons = updated
	if err := a.saveLocked(); err != nil {
		item.SkipSeasons = previous
		return setting, err
	}
	return setting, nil
}

func (a *App) skipSegmentRoutes(mux *http.ServeMux) {
	const route = "/api/files/{id}/skip-segments/{kind}"
	update := func(w http.ResponseWriter, r *http.Request) {
		var setting skipSegmentSetting
		var duration float64
		if r.Method == "POST" {
			var input struct {
				Start    *float64 `json:"start"`
				End      *float64 `json:"end"`
				Duration float64  `json:"duration"`
				Disabled bool     `json:"disabled"`
			}
			if err := readJSON(w, r, &input); err != nil {
				fail(w, err)
				return
			}
			if input.Disabled && input.Start == nil && input.End == nil {
				setting.Disabled = true
			} else if input.Disabled || input.Start == nil || input.End == nil {
				fail(w, errors.New("请填写起点和终点"))
				return
			} else {
				setting.Segment = &SkipSegment{Start: *input.Start, End: *input.End}
			}
			duration = input.Duration
		}
		setting, err := a.setSkipSegment(r.PathValue("id"), r.PathValue("kind"), r.URL.Query().Get("scope"), setting, duration)
		if err != nil {
			fail(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, setting)
	}
	mux.HandleFunc("POST "+route, update)
	mux.HandleFunc("DELETE "+route, update)
}
