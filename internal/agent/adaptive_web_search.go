package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/aiaccess"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/config"
)

const defaultAdaptiveWebSearchURL = "https://alem-workspace.gov.kz/v1/chat/ask_adaptive"

type adaptiveWebSearchRequest struct {
	Question             string      `json:"question"`
	ChatID               string      `json:"chat_id"`
	TopK                 int         `json:"top_k"`
	Temperature          float64     `json:"temperature"`
	Learn                bool        `json:"learn"`
	WebCacheProject      interface{} `json:"web_cache_project"`
	IncludeWebCacheInRAG bool        `json:"include_web_cache_in_rag"`
	Fast                 bool        `json:"fast"`
}

type adaptiveWebSearchSource struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

type adaptiveWebSearchResponse struct {
	Answer        string                    `json:"answer"`
	UsedWeb       bool                      `json:"used_web"`
	SearchBackend interface{}               `json:"search_backend"`
	Sources       []adaptiveWebSearchSource `json:"sources"`
	ESChunksUsed  int                       `json:"es_chunks_used"`
	SearxNGHits   int                       `json:"searxng_hits"`
	Gatekeeper    map[string]interface{}    `json:"gatekeeper"`
	IndexedChunks int                       `json:"indexed_chunks"`
	FastMode      bool                      `json:"fast_mode"`
	Route         string                    `json:"route"`
}

func (r adaptiveWebSearchResponse) SourcesAsPayload() []map[string]interface{} {
	payload := make([]map[string]interface{}, 0, len(r.Sources))
	for _, source := range r.Sources {
		payload = append(payload, map[string]interface{}{
			"title":   source.Title,
			"url":     source.URL,
			"content": source.Content,
		})
	}
	return payload
}

func executeAdaptiveWebSearch(
	ctx context.Context,
	cfg *config.AgentConfig,
	query string,
	authorizationHeader string,
	cookieHeader string,
) (*adaptiveWebSearchResponse, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("empty query")
	}
	if strings.TrimSpace(authorizationHeader) == "" {
		return nil, fmt.Errorf("missing Authorization header")
	}

	endpoint := defaultAdaptiveWebSearchURL
	timeout := 15 * time.Second
	if cfg != nil {
		if strings.TrimSpace(cfg.AdaptiveWebSearchURL) != "" {
			endpoint = strings.TrimSpace(cfg.AdaptiveWebSearchURL)
		}
		if cfg.ExternalTimeout > 0 {
			timeout = cfg.ExternalTimeout
		}
	}

	body, err := json.Marshal(adaptiveWebSearchRequest{
		Question:             query,
		ChatID:               uuid.NewString(),
		TopK:                 20,
		Temperature:          0.15,
		Learn:                true,
		WebCacheProject:      nil,
		IncludeWebCacheInRAG: true,
		Fast:                 true,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")
	req.Header.Set("Authorization", strings.TrimSpace(authorizationHeader))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://alem-workspace.gov.kz")
	req.Header.Set("Referer", "https://alem-workspace.gov.kz/web/team-rag")
	req.Header.Set("User-Agent", "alem-smart-agent/1.0")
	if strings.TrimSpace(cookieHeader) != "" {
		req.Header.Set("Cookie", strings.TrimSpace(cookieHeader))
	}

	client := &http.Client{Timeout: timeout, Transport: aiaccess.BlockedTransport{}}
	resp, err := client.Do(req)
	if err != nil {
		var accountingError *apperrors.AppError
		if errors.As(err, &accountingError) {
			return nil, accountingError
		}
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var payload adaptiveWebSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if strings.TrimSpace(payload.Answer) == "" {
		payload.Answer = "Интернет-поиск выполнен, но backend не вернул текстовый ответ."
	}
	return &payload, nil
}
