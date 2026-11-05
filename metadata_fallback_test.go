package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTMDBEmptySearchFallsBackToPublicMetadata(t *testing.T) {
	a := testApp(t)
	a.s.Config.TMDBSecret, _ = a.encrypt("synthetic-key")
	var tmdbCalls, wikiCalls atomic.Int32
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Hostname() {
		case "api.themoviedb.org":
			tmdbCalls.Add(1)
			return fakeJSON(`{"results":[]}`), nil
		case "zh.wikipedia.org":
			wikiCalls.Add(1)
			return fakeJSON(`{"query":{"pages":[{"pageid":171008,"title":"铁齿铜牙纪晓岚","extract":"《铁齿铜牙纪晓岚》是中国古装电视剧，于2001年首播。"}]}}`), nil
		case "api.tvmaze.com":
			return fakeJSON(`[]`), nil
		}
		return nil, errors.New("unexpected request")
	})}
	rows, err := a.searchMetadata(context.Background(), "tv", "铁齿铜牙纪晓岚")
	if err != nil || len(rows) != 1 || rows[0]["provider"] != "wikipedia" || rows[0]["externalId"] != "zh:171008" || tmdbCalls.Load() != 1 || wikiCalls.Load() != 1 {
		t.Fatalf("fallback lost provider identity: %+v, %v", rows, err)
	}
}

func TestTMDBSearchKeepsPriorityAndAuthenticationErrors(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		a := testApp(t)
		a.s.Config.TMDBSecret, _ = a.encrypt("synthetic-key")
		a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Hostname() != "api.themoviedb.org" {
				t.Fatal("successful search or authentication error triggered fallback")
			}
			response := fakeJSON(`{"results":[{"id":123,"name":"测试电视剧","first_air_date":"2001-01-01"}]}`)
			if unavailable {
				response.StatusCode = 401
			}
			return response, nil
		})}
		rows, err := a.searchMetadata(context.Background(), "tv", "测试电视剧")
		if unavailable {
			if err == nil {
				t.Fatal("TMDb authentication error was hidden")
			}
		} else if err != nil || len(rows) != 1 || rows[0]["provider"] != "tmdb" {
			t.Fatal("TMDb result did not keep priority")
		}
	}
}

func TestTMDBEmptyAndFailedPublicFallbackReportsBoth(t *testing.T) {
	a := testApp(t)
	a.s.Config.TMDBSecret, _ = a.encrypt("synthetic-key")
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() == "api.themoviedb.org" {
			return fakeJSON(`{"results":[]}`), nil
		}
		return nil, errors.New("offline")
	})}
	_, err := a.searchMetadata(context.Background(), "movie", "测试电影")
	if err == nil || !strings.Contains(err.Error(), "TMDb 未找到") || !strings.Contains(err.Error(), "公开资料") {
		t.Fatal("failed fallback presented as an empty successful search")
	}
}

func TestChineseTelevisionSerialSurvivesScanCandidateFiltering(t *testing.T) {
	a, id := scanFixture(t)
	s, err := a.scanSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	groupID := ""
	for _, group := range s.Plan.Groups {
		if group.Type == "tv" {
			groupID = group.ID
			break
		}
	}
	if groupID == "" {
		t.Fatal("fixture has no TV group")
	}
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() == "zh.wikipedia.org" {
			return fakeJSON(`{"query":{"pages":[{"pageid":171008,"title":"鐵齒銅牙紀曉嵐","extract":"《铁齿铜牙纪晓岚》是一部中国大陆电视连续剧。"}]}}`), nil
		}
		if r.URL.Hostname() == "api.tvmaze.com" {
			return fakeJSON(`[]`), nil
		}
		return nil, errors.New("unexpected request")
	})}
	choices, err := a.searchScanMatches(context.Background(), id, scanMatchQuery{GroupID: groupID, Type: "tv", Query: "铁齿铜牙纪晓岚"})
	if err != nil || len(choices) != 1 || choices[0].Provider != "wikipedia" || choices[0].ExternalID != "zh:171008" || choices[0].Token == "" {
		t.Fatalf("a Chinese TV serial disappeared between search and scan matching: %+v, %v", choices, err)
	}
}
