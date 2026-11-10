package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestAICalendarLocalDayRanges(t *testing.T) {
	for _, tc := range []struct{ name, from, to, zone, wantFrom, wantTo string }{
		{"reported tomorrow", "2026-10-24", "2026-10-24", "Asia/Almaty", "2026-10-24T00:00:00+05:00", "2026-10-25T00:00:00+05:00"},
		{"reported week", "2026-10-19", "2026-10-25", "Asia/Almaty", "2026-10-19T00:00:00+05:00", "2026-10-26T00:00:00+05:00"},
		{"default zone", "2026-10-24", "2026-10-24", "", "2026-10-24T00:00:00+05:00", "2026-10-25T00:00:00+05:00"},
		{"DST day", "2026-03-29", "2026-03-29", "Europe/Berlin", "2026-03-29T00:00:00+01:00", "2026-03-30T00:00:00+02:00"},
		{"exact timestamps", "2026-10-24T09:30:00.123+05:00", "2026-10-24T10:30:00+05:00", "Asia/Almaty", "2026-10-24T09:30:00.123+05:00", "2026-10-24T10:30:00+05:00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := url.Values{"from": {tc.from}, "to": {tc.to}, "timezone": {tc.zone}}
			if err := normalizeAICalendarRange(p); err != nil {
				t.Fatal(err)
			}
			if p.Get("from") != tc.wantFrom || p.Get("to") != tc.wantTo {
				t.Fatalf("wrong range: %v", p)
			}
		})
	}
}

func TestAIReaderCalendarFindsTenAMInFullLocalDay(t *testing.T) {
	for _, dataset := range []string{"events", "event_search"} {
		t.Run(dataset, func(t *testing.T) {
			reader := NewHTTPAIUserDataReader(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				from, err := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
				if err != nil {
					t.Fatal(err)
				}
				to, err := time.Parse(time.RFC3339, r.URL.Query().Get("to"))
				if err != nil {
					t.Fatal(err)
				}
				meeting, _ := time.Parse(time.RFC3339, "2026-10-24T10:00:00+05:00")
				if meeting.Before(from) || !meeting.Before(to) {
					t.Fatalf("10AM excluded: %s", r.URL)
				}
				json.NewEncoder(w).Encode([]map[string]string{{"id": "meeting-1", "title": "Test Company", "start_time": meeting.Format(time.RFC3339)}})
			}), nil)
			got := reader.Read(context.Background(), AIDataIdentity{UserID: 2, Authorization: "Bearer current"}, AIDataQuery{Dataset: dataset, Params: map[string]string{"from": "2026-10-24", "to": "2026-10-24", "timezone": "Asia/Almaty"}})
			if got.Status != "ok" || len(got.Data.([]any)) != 1 {
				t.Fatal(got)
			}
		})
	}
}

func TestAIReaderCalendarRejectsInvalidRangesBeforeRead(t *testing.T) {
	reader := NewHTTPAIUserDataReader(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("invalid range reached API") }), nil)
	for _, p := range []map[string]string{
		{"from": "2026-10-24", "to": "2026-10-23"},
		{"from": "2026-10-24T10:00:00+05:00", "to": "2026-10-24T10:00:00+05:00"},
		{"from": "2026-10-24", "to": "2026-10-24", "timezone": "invalid/zone"},
		{"from": "2026-02-30", "to": "2026-03-01"},
		{"from": "2026-10-24"},
	} {
		got := reader.Read(context.Background(), AIDataIdentity{UserID: 2, Authorization: "Bearer current"}, AIDataQuery{Dataset: "events", Params: p})
		if got.Status != "invalid_request" {
			t.Fatal(got)
		}
	}
}
