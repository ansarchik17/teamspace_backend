package telemetry

import (
	"testing"
	"time"
)

func TestAPITracker(t *testing.T) {
	tracker := NewAPITracker("alem.kz", "localhost")

	// 1. Record official request
	tracker.Record(RequestLog{
		ID:           "req-1",
		Timestamp:    time.Now(),
		Method:       "GET",
		Path:         "/api/v1/news",
		IP:           "127.0.0.1",
		UserAgent:    "Mozilla/5.0",
		Origin:       "http://localhost:3000",
		IsThirdParty: false,
		ClientType:   "official_web",
	})

	// 2. Record third-party request from competitor A
	tracker.Record(RequestLog{
		ID:           "req-2",
		Timestamp:    time.Now(),
		Method:       "POST",
		Path:         "/api/v1/products",
		IP:           "198.51.100.42",
		UserAgent:    "python-requests/2.31.0",
		Origin:       "https://competitor-site.com",
		IsThirdParty: true,
		ClientType:   "competitor_web_site",
	})

	// 3. Record third-party request from competitor B (aitugovworkspace.kz)
	tracker.Record(RequestLog{
		ID:           "req-3",
		Timestamp:    time.Now(),
		Method:       "GET",
		Path:         "/api/v1/notifications",
		IP:           "198.51.100.99",
		UserAgent:    "Mozilla/5.0",
		Origin:       "https://aitugovworkspace.kz",
		IsThirdParty: true,
		ClientType:   "competitor_web_site",
	})

	// General Stats
	stats := tracker.GetStats()
	if stats.TotalRequests != 3 {
		t.Errorf("expected total requests 3, got %d", stats.TotalRequests)
	}

	// Filtered Stats by Origin
	filteredStats := tracker.GetStats("aitugovworkspace.kz")
	if len(filteredStats.TopEndpoints) != 1 || filteredStats.TopEndpoints[0].Path != "/api/v1/notifications" {
		t.Errorf("expected filtered top endpoint /api/v1/notifications, got %v", filteredStats.TopEndpoints)
	}

	if len(filteredStats.RecentLogs) != 1 || filteredStats.RecentLogs[0].Origin != "https://aitugovworkspace.kz" {
		t.Errorf("expected filtered recent logs length 1, got %d", len(filteredStats.RecentLogs))
	}
}

func TestClassifyClient(t *testing.T) {
	tracker := NewAPITracker("alem.kz")

	isThirdParty, clientType := tracker.ClassifyClient("alem-web-v1", "", "", "Mozilla/5.0")
	if isThirdParty || clientType != "official_web" {
		t.Errorf("expected official_web, got isThirdParty=%v, clientType=%s", isThirdParty, clientType)
	}

	isThirdParty, clientType = tracker.ClassifyClient("", "https://competitor.com", "", "Mozilla/5.0")
	if !isThirdParty || clientType != "competitor_web_site" {
		t.Errorf("expected competitor_web_site, got isThirdParty=%v, clientType=%s", isThirdParty, clientType)
	}

	isThirdParty, clientType = tracker.ClassifyClient("", "", "", "python-requests/2.31.0")
	if !isThirdParty || clientType != "external_script_bot" {
		t.Errorf("expected external_script_bot, got isThirdParty=%v, clientType=%s", isThirdParty, clientType)
	}
}
