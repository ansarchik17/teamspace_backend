package service

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

//go:embed ai_context/catalog.json
var aiDataCatalogJSON []byte

type AIDataDataset struct {
	Key             string            `json:"key"`
	Title           string            `json:"title"`
	Connector       string            `json:"connector"`
	Path            string            `json:"path"`
	DetailPath      string            `json:"detail_path"`
	Description     string            `json:"description"`
	ResponseType    string            `json:"response_type,omitempty"`
	Params          []string          `json:"params"`
	Defaults        map[string]string `json:"defaults"`
	ZeroBasedPage   bool              `json:"zero_based_page,omitempty"`
	MineFilter      string            `json:"mine_filter,omitempty"`
	ActorFilter     string            `json:"actor_filter,omitempty"`
	CardViews       []AIDataCardView  `json:"card_views,omitempty"`
	RequiredContext []string          `json:"required_context,omitempty"`
}

// Identity is supplied only by an authenticated handler, never by model arguments.
type AIDataIdentity struct {
	UserID                      int64
	ChatID                      string
	Authorization, Cookie, Host string
}
type aiIdentityKey struct{}

func WithAIDataIdentity(ctx context.Context, identity AIDataIdentity) context.Context {
	return context.WithValue(ctx, aiIdentityKey{}, identity)
}

type AIDataQuery struct {
	Dataset string            `json:"dataset"`
	ID      string            `json:"id,omitempty"`
	Params  map[string]string `json:"params,omitempty"`
}
type AIDataResult struct {
	Status      string `json:"status"`
	Data        any    `json:"data,omitempty"`
	Message     string `json:"message,omitempty"`
	TextPage    int    `json:"text_page,omitempty"`
	HasMoreText bool   `json:"has_more_text,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
}
type AIUserDataReader interface {
	Catalog() []AIDataDataset
	Read(context.Context, AIDataIdentity, AIDataQuery) AIDataResult
}
type AIConnectorAuthorization struct{ Token, Host string }
type AIConnector struct {
	BaseURL     string
	ForwardHost bool
	Authorize   func(context.Context, AIDataIdentity) (AIConnectorAuthorization, error)
}

// Workspace reads pass through the same router, authentication, tenant and
// object permission checks as the UI. Remote reads also use user-scoped tokens.
type HTTPAIUserDataReader struct {
	workspace  http.Handler
	connectors map[string]AIConnector
	client     *http.Client
	catalog    []AIDataDataset
	extractor  *ContentExtractionService
	extra      map[string]func(context.Context, AIDataIdentity, AIDataQuery) AIDataResult
}

func NewHTTPAIUserDataReader(workspace http.Handler, connectors map[string]AIConnector) *HTTPAIUserDataReader {
	var catalog []AIDataDataset
	if err := json.Unmarshal(aiDataCatalogJSON, &catalog); err != nil {
		panic(err)
	}
	return &HTTPAIUserDataReader{workspace: workspace, connectors: connectors, catalog: catalog, extra: make(map[string]func(context.Context, AIDataIdentity, AIDataQuery) AIDataResult),
		client: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (r *HTTPAIUserDataReader) Catalog() []AIDataDataset { return r.catalog }

// Register is used at startup to attach typed readers with object-level ACLs.
func (r *HTTPAIUserDataReader) Register(dataset AIDataDataset, read func(context.Context, AIDataIdentity, AIDataQuery) AIDataResult) {
	r.catalog = append(r.catalog, dataset)
	r.extra[dataset.Key] = read
}

var aiResourceID = regexp.MustCompile(`^[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)*$`)

func aiDatasetPath(dataset AIDataDataset, identity AIDataIdentity, query AIDataQuery) (string, error) {
	path := dataset.Path
	if query.ID != "" {
		if dataset.DetailPath == "" || (len(query.ID) > 1024 || !aiResourceID.MatchString(query.ID)) {
			return "", fmt.Errorf("invalid resource id")
		}
		path = strings.ReplaceAll(dataset.DetailPath, "{id}", url.PathEscape(query.ID))
	}
	if path == "" {
		return "", fmt.Errorf("resource id is required")
	}
	params := url.Values{}
	allowed := make(map[string]bool)
	for _, key := range dataset.Params {
		allowed[key] = true
	}
	for key, value := range query.Params {
		if !allowed[key] || len(value) > 1024 {
			return "", fmt.Errorf("unsupported query parameter: %s", key)
		}
		if key == "attachment_id" && strings.Contains(path, "{attachment_id}") {
			if !aiResourceID.MatchString(value) {
				return "", fmt.Errorf("invalid attachment id")
			}
			path = strings.ReplaceAll(path, "{attachment_id}", url.PathEscape(value))
			continue
		}
		if key == "text_page" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || n > 10000 {
				return "", fmt.Errorf("invalid text page")
			}
			continue
		}
		if key == "mine" {
			if value != "true" && value != "false" {
				return "", fmt.Errorf("mine must be true or false")
			}
			if value == "true" && dataset.MineFilter == "" {
				params.Set("assignee_ids", strconv.FormatInt(identity.UserID, 10))
			}
			continue
		}
		if key == "limit" || key == "page_size" || key == "pageSize" || key == "page" || key == "offset" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 || n > 10000 || ((key == "page" || key == "page_size" || key == "pageSize" || key == "limit") && n == 0) {
				return "", fmt.Errorf("invalid pagination")
			}
			if (key == "limit" || key == "page_size" || key == "pageSize") && n > 25 {
				n = 25
			}
			if key == "page" && dataset.ZeroBasedPage {
				n--
			}
			value = strconv.Itoa(n)
		}
		params.Set(key, value)
	}
	for key, value := range dataset.Defaults {
		if params.Get(key) == "" {
			params.Set(key, value)
		}
	}
	for _, key := range []string{"limit", "page_size", "pageSize"} {
		if allowed[key] && params.Get(key) == "" {
			params.Set(key, "25")
		}
	}
	if strings.ContainsAny(path, "{}") {
		return "", fmt.Errorf("attachment_id is required")
	}
	if query.ID == "" && (dataset.Key == "events" || dataset.Key == "event_search") {
		if err := normalizeAICalendarRange(params); err != nil {
			return "", err
		}
	}
	if len(params) > 0 {
		path += "?" + params.Encode()
	}
	return path, nil
}

func (r *HTTPAIUserDataReader) Read(ctx context.Context, identity AIDataIdentity, query AIDataQuery) AIDataResult {
	if identity.UserID <= 0 || (identity.Authorization == "" && identity.Cookie == "") {
		return AIDataResult{Status: "forbidden", Message: "Authenticated user context is required"}
	}
	var dataset *AIDataDataset
	for i := range r.catalog {
		if r.catalog[i].Key == query.Dataset {
			dataset = &r.catalog[i]
			break
		}
	}
	if dataset == nil {
		return AIDataResult{Status: "invalid_request", Message: "Unknown data source"}
	}
	if extra := r.extra[dataset.Key]; extra != nil {
		return extra(ctx, identity, query)
	}
	path, err := aiDatasetPath(*dataset, identity, query)
	if err != nil {
		return AIDataResult{Status: "invalid_request", Message: err.Error(), Data: map[string]any{"allowed_params": dataset.Params, "values_type": "string"}}
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	if dataset.MineFilter != "" && query.Params["mine"] == "true" {
		if query.ID != "" {
			return AIDataResult{Status: "invalid_request", Message: "mine is supported only for collection reads"}
		}
		// Growy has its own user IDs. Resolve the manager from the live,
		// authenticated tenant context, never from model arguments or Workspace ID.
		crmContext := r.Read(ctx, identity, AIDataQuery{Dataset: "crm_context"})
		if crmContext.Status != "ok" {
			return AIDataResult{Status: crmContext.Status, Message: "Unable to resolve current CRM user; do not interpret this as zero records"}
		}
		data, _ := crmContext.Data.(map[string]any)
		userID, ok := data["user_id"].(float64)
		if !ok || userID <= 0 || userID > 9007199254740991 || userID != float64(int64(userID)) {
			return AIDataResult{Status: "unavailable", Message: "CRM context did not contain a valid current user ID; do not interpret this as zero records"}
		}
		scopedURL, _ := url.Parse(path)
		filters := scopedURL.Query()
		actorValue := strconv.FormatInt(int64(userID), 10)
		if dataset.MineFilter == "assigned_to" {
			// Match the CRM UI's self-assignment filter; Growy resolves "me".
			actorValue = "me"
		}
		filters.Set(dataset.MineFilter, actorValue)
		scopedURL.RawQuery = filters.Encode()
		path = scopedURL.String()
	}
	target := "http://workspace.internal" + path
	auth, host := identity.Authorization, identity.Host
	if dataset.Connector != "workspace" {
		connector, ok := r.connectors[dataset.Connector]
		if !ok || connector.BaseURL == "" {
			return AIDataResult{Status: "unavailable", Message: "Integration is not configured"}
		}
		base, parseErr := url.Parse(connector.BaseURL)
		if parseErr != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
			return AIDataResult{Status: "unavailable", Message: "Invalid integration configuration"}
		}
		target = strings.TrimRight(connector.BaseURL, "/") + path
		host = ""
		if connector.ForwardHost {
			host = identity.Host
		}
		// Never send Workspace session cookies to a different application.
		if connector.Authorize != nil {
			credentials, err := connector.Authorize(ctx, identity)
			if err != nil {
				return AIDataResult{Status: "unavailable", Message: "Unable to authenticate this user's integration"}
			}
			auth, host = credentials.Token, credentials.Host
		}
		if dataset.ActorFilter != "" && query.ID == "" {
			// ODO uses its own stable user ID. Resolve it with the same live
			// Workspace session instead of accepting a model-selected executor.
			profileReq, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(connector.BaseURL, "/")+"/api/v1/auth/me", nil)
			if err != nil {
				return AIDataResult{Status: "unavailable", Message: "Unable to resolve integration identity"}
			}
			profileReq.Header.Set("Authorization", auth)
			profileReq.Header.Set("Accept", "application/json")
			if host != "" {
				profileReq.Host = host
			}
			profileResp, err := r.client.Do(profileReq)
			if err != nil {
				return AIDataResult{Status: "unavailable", Message: "Unable to resolve integration identity"}
			}
			var profile struct {
				ID string `json:"id"`
			}
			decodeErr := json.NewDecoder(io.LimitReader(profileResp.Body, 64<<10)).Decode(&profile)
			profileResp.Body.Close()
			if profileResp.StatusCode == http.StatusUnauthorized || profileResp.StatusCode == http.StatusForbidden {
				return AIDataResult{Status: "forbidden", Message: "This user's integration access is not active"}
			}
			if profileResp.StatusCode != http.StatusOK || decodeErr != nil || len(profile.ID) > 1024 || !aiResourceID.MatchString(profile.ID) {
				return AIDataResult{Status: "unavailable", Message: "Unable to resolve integration identity"}
			}
			scopedURL, _ := url.Parse(target)
			filters := scopedURL.Query()
			filters.Set(dataset.ActorFilter, profile.ID)
			scopedURL.RawQuery = filters.Encode()
			target = scopedURL.String()
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return AIDataResult{Status: "unavailable", Message: "Unable to build data request"}
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Accept", "application/json")
	if host != "" {
		req.Host = host
	}
	var status int
	var body []byte
	var headers http.Header
	byteLimit := int64(2 * 1024 * 1024)
	if dataset.ResponseType == "document" {
		byteLimit = AIDocumentMaxBytes
	}
	if dataset.Connector == "workspace" {
		if r.workspace == nil {
			return AIDataResult{Status: "unavailable", Message: "Workspace is unavailable"}
		}
		req.Header.Set("Cookie", identity.Cookie)
		recorder := &aiReadRecorder{header: make(http.Header), status: 200}
		r.workspace.ServeHTTP(recorder, req)
		status, body = recorder.status, recorder.body.Bytes()
		headers = recorder.header
		if recorder.overflow {
			return AIDataResult{Status: "too_large", Message: "Response exceeded the safe limit; narrow the query"}
		}
	} else {
		resp, err := r.client.Do(req)
		if err != nil {
			return AIDataResult{Status: "unavailable", Message: "Data source did not respond"}
		}
		defer resp.Body.Close()
		status = resp.StatusCode
		headers = resp.Header
		body, err = io.ReadAll(io.LimitReader(resp.Body, byteLimit+1))
		if err != nil || int64(len(body)) > byteLimit {
			return AIDataResult{Status: "too_large", Message: "Narrow the query; response is too large"}
		}
	}
	if status == 401 || status == 403 {
		return AIDataResult{Status: "forbidden", Message: "This user's current permissions do not allow this read"}
	}
	if status == 404 {
		return AIDataResult{Status: "not_found", Message: "Resource or integration endpoint was not found"}
	}
	if status < 200 || status >= 300 {
		return AIDataResult{Status: "unavailable", Message: "Data source returned an error"}
	}

	if dataset.ResponseType == "document" {
		if r.extractor == nil {
			return AIDataResult{Status: "unavailable", Message: "Document parser is unavailable"}
		}
		_, disposition, _ := mime.ParseMediaType(headers.Get("Content-Disposition"))
		filename := filepath.Base(disposition["filename"])
		if filename == "." || filename == "" {
			return AIDataResult{Status: "unavailable", Message: "Attachment filename is missing"}
		}
		text, parseErr := r.extractor.ExtractTextBytes(filename, body)
		if parseErr != nil {
			return AIDataResult{Status: "unavailable", Message: "File format cannot be read; do not infer its contents"}
		}
		page := 1
		if raw := query.Params["text_page"]; raw != "" {
			page, _ = strconv.Atoi(raw)
		}
		data := aiTextPage(text, page)
		data["name"] = filename
		return AIDataResult{Status: "ok", Data: data, Truncated: aiDataHasMore(data)}
	}
	var data any
	if json.Unmarshal(body, &data) != nil {
		return AIDataResult{Status: "unavailable", Message: "Data source did not return JSON"}
	}
	if dataset.ZeroBasedPage && query.ID == "" {
		if items, ok := data.([]any); ok {
			currentPage, size := 1, 25
			if raw := query.Params["page"]; raw != "" {
				currentPage, _ = strconv.Atoi(raw)
			}
			if raw := query.Params["pageSize"]; raw != "" {
				size, _ = strconv.Atoi(raw)
				if size > 25 {
					size = 25
				}
			}
			data = map[string]any{"items": items, "page": currentPage, "page_size": size, "has_next": len(items) >= size}
		}
	}
	truncated := false
	page := 1
	if raw := query.Params["text_page"]; raw != "" {
		page, _ = strconv.Atoi(raw)
	}
	more := false
	data = boundedAIDataPage(data, page, &truncated, &more)
	return AIDataResult{Status: "ok", Data: data, Truncated: truncated || aiDataHasMore(data), TextPage: page, HasMoreText: more}
}

type aiReadRecorder struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func (r *aiReadRecorder) Header() http.Header    { return r.header }
func (r *aiReadRecorder) WriteHeader(status int) { r.status = status }
func (r *aiReadRecorder) Write(p []byte) (int, error) {
	if r.body.Len()+len(p) > 2*1024*1024 {
		r.overflow = true
		return len(p), nil
	}
	return r.body.Write(p)
}

// Remove credential material and bound bulky strings before they enter a model
// prompt. Encrypted message envelopes cannot be interpreted as plaintext.
func boundedAIData(value any, truncated *bool) any {
	more := false
	return boundedAIDataPage(value, 1, truncated, &more)
}
func boundedAIDataPage(value any, page int, truncated, more *bool) any {
	switch v := value.(type) {
	case map[string]any:
		// Workspace E2EE messages expose opaque content alongside key envelopes.
		encrypted := v["encrypted_keys"] != nil || v["encryption_version"] != nil
		out := make(map[string]any, len(v))
		for key, item := range v {
			lower := strings.ToLower(key)
			if encrypted && (lower == "content" || lower == "text" || lower == "body") {
				out["content_unavailable"] = "end_to_end_encrypted"
				continue
			}
			if strings.Contains(lower, "encrypted") || lower == "object_name" || lower == "object_key" {
				continue
			}
			if (lower == "next" || lower == "previous") && item != nil {
				out["has_"+lower] = true
				continue
			}
			if strings.Contains(lower, "password") || strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "cipher") || strings.Contains(lower, "private_key") || lower == "key" || lower == "cookie" || lower == "authorization" || strings.HasSuffix(lower, "url") {
				continue
			}
			out[key] = boundedAIDataPage(item, page, truncated, more)
		}
		return out
	case []any:
		if len(v) > 50 {
			v = v[:50]
			*truncated = true
		}
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, boundedAIDataPage(item, page, truncated, more))
		}
		return out
	case string:
		runes := []rune(v)
		if len(runes) > 4000 {
			*truncated = true
			start := (page - 1) * 4000
			if start > len(runes) {
				start = len(runes)
			}
			end := start + 4000
			if end > len(runes) {
				end = len(runes)
			}
			if end < len(runes) {
				*more = true
			}
			return string(runes[start:end])
		}
		return v
	default:
		return value
	}
}

func aiDataHasMore(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if key == "has_next" || key == "has_more" || key == "has_more_text" {
				if flag, ok := item.(bool); ok && flag {
					return true
				}
			}
			if aiDataHasMore(item) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if aiDataHasMore(item) {
				return true
			}
		}
	}
	return false
}
