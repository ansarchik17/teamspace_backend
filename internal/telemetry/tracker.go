package telemetry

import (
	"net/url"
	"strings"
	"sync"
	"time"
)

// RequestLog represents a single HTTP request telemetry record
type RequestLog struct {
	ID           string    `json:"id"`
	Timestamp    time.Time `json:"timestamp"`
	Method       string    `json:"method"`
	Path         string    `json:"path"`
	IP           string    `json:"ip"`
	UserAgent    string    `json:"user_agent"`
	Origin       string    `json:"origin"`
	Referer      string    `json:"referer"`
	StatusCode   int       `json:"status_code"`
	DurationMs   int64     `json:"duration_ms"`
	IsThirdParty bool      `json:"is_third_party"`
	ClientType   string    `json:"client_type"`
}

// EndpointStat represents aggregated stats for a specific API endpoint
type EndpointStat struct {
	Path  string `json:"path"`
	Count int64  `json:"count"`
}

// KeyValueStat represents aggregated key-value counts (e.g. Origin, UserAgent)
type KeyValueStat struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

// StatsResponse represents the JSON output for the monitoring dashboard
type StatsResponse struct {
	TotalRequests       int64            `json:"total_requests"`
	OfficialRequests    int64            `json:"official_requests"`
	ThirdPartyRequests  int64            `json:"third_party_requests"`
	TopEndpoints        []EndpointStat   `json:"top_endpoints"`
	TopOrigins          []KeyValueStat   `json:"top_origins"`
	TopUserAgents       []KeyValueStat   `json:"top_user_agents"`
	ClientTypeBreakdown map[string]int64 `json:"client_type_breakdown"`
	RecentLogs          []RequestLog     `json:"recent_logs"`
	ActiveFilter        string           `json:"active_filter,omitempty"`
}

// APITracker collects non-blocking telemetry on incoming API traffic
type APITracker struct {
	mu                 sync.RWMutex
	recentLogs         []RequestLog
	maxRecentLogs      int
	totalRequests      int64
	officialRequests   int64
	thirdPartyRequests int64
	pathStats          map[string]int64
	originStats        map[string]int64
	userAgentStats     map[string]int64
	clientTypeStats    map[string]int64
	originPathStats    map[string]map[string]int64
	officialDomains    []string
}

// NewAPITracker creates a new thread-safe APITracker instance
func NewAPITracker(officialDomains ...string) *APITracker {
	return &APITracker{
		recentLogs:      make([]RequestLog, 0, 200),
		maxRecentLogs:   200,
		pathStats:       make(map[string]int64),
		originStats:     make(map[string]int64),
		userAgentStats:  make(map[string]int64),
		clientTypeStats: make(map[string]int64),
		originPathStats: make(map[string]map[string]int64),
		officialDomains: officialDomains,
	}
}

// SetOfficialDomains updates the list of official domain names
func (t *APITracker) SetOfficialDomains(domains []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.officialDomains = domains
}

// ClassifyClient determines whether a request comes from the official web client or a third-party/competitor
func (t *APITracker) ClassifyClient(appClientHeader, origin, referer, userAgent string) (bool, string) {
	// Official client header
	if appClientHeader == "alem-web-v1" {
		return false, "official_web"
	}

	cleanOrigin := strings.ToLower(origin)
	cleanReferer := strings.ToLower(referer)

	// Helper to check if domain matches official hosts
	isOfficialHost := func(target string) bool {
		if target == "" {
			return false
		}
		if strings.Contains(target, "alem-workspace") || strings.Contains(target, "alem.kz") || strings.Contains(target, "localhost") || strings.Contains(target, "127.0.0.1") {
			return true
		}
		t.mu.RLock()
		domains := t.officialDomains
		t.mu.RUnlock()

		for _, d := range domains {
			if d == "*" || d == "" {
				continue
			}
			dClean := strings.ToLower(d)
			if parsed, err := url.Parse(dClean); err == nil && parsed.Host != "" {
				dClean = parsed.Host
			}
			if strings.Contains(target, dClean) {
				return true
			}
		}
		return false
	}

	if isOfficialHost(cleanOrigin) || (cleanOrigin == "" && isOfficialHost(cleanReferer)) {
		return false, "official_web"
	}

	// Flag as third party
	isThirdParty := true
	clientType := "unknown_third_party"

	if cleanOrigin != "" {
		clientType = "competitor_web_site"
	} else {
		cleanUA := strings.ToLower(userAgent)
		if strings.Contains(cleanUA, "python") ||
			strings.Contains(cleanUA, "axios") ||
			strings.Contains(cleanUA, "curl") ||
			strings.Contains(cleanUA, "go-http") ||
			strings.Contains(cleanUA, "postman") ||
			strings.Contains(cleanUA, "node-fetch") ||
			strings.Contains(cleanUA, "insomnia") ||
			strings.Contains(cleanUA, "httpx") {
			clientType = "external_script_bot"
		} else if cleanUA != "" {
			clientType = "external_browser_direct"
		}
	}

	return isThirdParty, clientType
}

// Record adds a request log and updates aggregate counters
func (t *APITracker) Record(log RequestLog) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.totalRequests++
	if log.IsThirdParty {
		t.thirdPartyRequests++
		t.pathStats[log.Path]++

		originKey := log.Origin
		if originKey == "" && log.Referer != "" {
			originKey = log.Referer
		}
		if originKey == "" {
			originKey = "Direct / No Origin"
		}
		t.originStats[originKey]++

		if t.originPathStats[originKey] == nil {
			t.originPathStats[originKey] = make(map[string]int64)
		}
		t.originPathStats[originKey][log.Path]++

		uaKey := log.UserAgent
		if uaKey == "" {
			uaKey = "Empty User-Agent"
		}
		t.userAgentStats[uaKey]++
	} else {
		t.officialRequests++
	}

	t.clientTypeStats[log.ClientType]++

	// Keep recent logs ring buffer capped at maxRecentLogs
	if len(t.recentLogs) >= t.maxRecentLogs {
		t.recentLogs = append(t.recentLogs[1:], log)
	} else {
		t.recentLogs = append(t.recentLogs, log)
	}
}

// GetStats returns aggregated telemetry metrics and recent request history, with optional origin filtering
func (t *APITracker) GetStats(filterOrigin ...string) StatsResponse {
	t.mu.RLock()
	defer t.mu.RUnlock()

	cleanFilter := ""
	if len(filterOrigin) > 0 {
		cleanFilter = strings.TrimSpace(strings.ToLower(filterOrigin[0]))
	}

	// 1. Top Origins
	topOrigins := make([]KeyValueStat, 0)
	for o, count := range t.originStats {
		topOrigins = append(topOrigins, KeyValueStat{Key: o, Count: count})
	}
	sortKeyValues(topOrigins)
	if len(topOrigins) > 10 {
		topOrigins = topOrigins[:10]
	}

	// 2. Top Endpoints (filtered if filterOrigin is set)
	topEndpoints := make([]EndpointStat, 0)
	if cleanFilter != "" {
		filteredPaths := make(map[string]int64)
		for oKey, pMap := range t.originPathStats {
			if strings.Contains(strings.ToLower(oKey), cleanFilter) {
				for p, count := range pMap {
					filteredPaths[p] += count
				}
			}
		}
		for p, count := range filteredPaths {
			topEndpoints = append(topEndpoints, EndpointStat{Path: p, Count: count})
		}
	} else {
		for p, count := range t.pathStats {
			topEndpoints = append(topEndpoints, EndpointStat{Path: p, Count: count})
		}
	}
	sortEndpoints(topEndpoints)
	if len(topEndpoints) > 10 {
		topEndpoints = topEndpoints[:10]
	}

	// 3. Top User Agents
	topUserAgents := make([]KeyValueStat, 0)
	for ua, count := range t.userAgentStats {
		topUserAgents = append(topUserAgents, KeyValueStat{Key: ua, Count: count})
	}
	sortKeyValues(topUserAgents)
	if len(topUserAgents) > 10 {
		topUserAgents = topUserAgents[:10]
	}

	// 4. Client Type Breakdown
	breakdown := make(map[string]int64)
	for k, v := range t.clientTypeStats {
		breakdown[k] = v
	}

	// 5. Recent Logs (filtered if filterOrigin is set)
	recentCopy := make([]RequestLog, 0, len(t.recentLogs))
	for i := len(t.recentLogs) - 1; i >= 0; i-- {
		l := t.recentLogs[i]
		if cleanFilter != "" {
			lOrigin := strings.ToLower(l.Origin + " " + l.Referer)
			if !strings.Contains(lOrigin, cleanFilter) {
				continue
			}
		}
		recentCopy = append(recentCopy, l)
	}

	return StatsResponse{
		TotalRequests:       t.totalRequests,
		OfficialRequests:    t.officialRequests,
		ThirdPartyRequests:  t.thirdPartyRequests,
		TopEndpoints:        topEndpoints,
		TopOrigins:          topOrigins,
		TopUserAgents:       topUserAgents,
		ClientTypeBreakdown: breakdown,
		RecentLogs:          recentCopy,
		ActiveFilter:        cleanFilter,
	}
}

func sortEndpoints(slice []EndpointStat) {
	for i := 0; i < len(slice); i++ {
		for j := i + 1; j < len(slice); j++ {
			if slice[j].Count > slice[i].Count {
				slice[i], slice[j] = slice[j], slice[i]
			}
		}
	}
}

func sortKeyValues(slice []KeyValueStat) {
	for i := 0; i < len(slice); i++ {
		for j := i + 1; j < len(slice); j++ {
			if slice[j].Count > slice[i].Count {
				slice[i], slice[j] = slice[j], slice[i]
			}
		}
	}
}
