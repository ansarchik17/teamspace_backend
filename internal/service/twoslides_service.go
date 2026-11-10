package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/west6ide/workspace-backend/internal/aiaccess"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/config"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type TwoSlidesService struct {
	baseURL string
	apiKey  string
	client  *http.Client
	logger  *zap.Logger
}

func NewTwoSlidesService(cfg config.TwoSlidesConfig, logger *zap.Logger) *TwoSlidesService {
	return &TwoSlidesService{
		baseURL: strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		apiKey:  strings.TrimSpace(cfg.APIKey),
		client: &http.Client{
			Timeout:   cfg.Timeout,
			Transport: aiaccess.BlockedTransport{ReadOnly: true},
		},
		logger: logger,
	}
}

func (s *TwoSlidesService) Generate(ctx context.Context, req domain.TwoSlidesGenerateRequest) (map[string]any, error) {
	if err := s.ensureConfigured(); err != nil {
		return nil, err
	}

	kind := strings.TrimSpace(strings.ToLower(req.Kind))
	if kind == "" {
		kind = "fast_ppt"
	}

	endpoint := "/api/v1/slides/generate"
	payload := map[string]any{
		"userInput": req.UserInput,
	}

	if req.ResponseLanguage != "" {
		payload["responseLanguage"] = req.ResponseLanguage
	}
	if req.Mode != "" {
		payload["mode"] = req.Mode
	}

	switch kind {
	case "fast_ppt":
		if strings.TrimSpace(req.ThemeID) == "" {
			return nil, apperrors.Validation("themeId is required for kind=fast_ppt")
		}
		payload["themeId"] = req.ThemeID
	case "pdf_slides":
		endpoint = "/api/v1/slides/create-pdf-slides"
		if req.DesignStyle != "" {
			payload["designStyle"] = req.DesignStyle
		}
		if req.ImageModel != "" {
			payload["imageModel"] = req.ImageModel
		}
		if req.AspectRatio != "" {
			payload["aspectRatio"] = req.AspectRatio
		}
		if req.Resolution != "" {
			payload["resolution"] = req.Resolution
		}
		if req.Page > 0 {
			payload["page"] = req.Page
		}
		if req.ContentDetail != "" {
			payload["contentDetail"] = req.ContentDetail
		}
	case "like_this":
		endpoint = "/api/v1/slides/create-like-this"
		if strings.TrimSpace(req.ReferenceImageURL) == "" {
			return nil, apperrors.Validation("referenceImageUrl is required for kind=like_this")
		}
		payload["referenceImageUrl"] = req.ReferenceImageURL
		if req.ImageModel != "" {
			payload["imageModel"] = req.ImageModel
		}
		if req.AspectRatio != "" {
			payload["aspectRatio"] = req.AspectRatio
		}
		if req.Resolution != "" {
			payload["resolution"] = req.Resolution
		}
		if req.Page > 0 {
			payload["page"] = req.Page
		}
		if req.ContentDetail != "" {
			payload["contentDetail"] = req.ContentDetail
		}
	default:
		return nil, apperrors.Validation("kind must be one of: fast_ppt, pdf_slides, like_this")
	}

	return s.doJSONRequest(ctx, http.MethodPost, endpoint, payload)
}

func (s *TwoSlidesService) GetJob(ctx context.Context, jobID string) (map[string]any, error) {
	if err := s.ensureConfigured(); err != nil {
		return nil, err
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return nil, apperrors.Validation("jobId is required")
	}
	return s.doJSONRequest(ctx, http.MethodGet, "/api/v1/jobs/"+url.PathEscape(jobID), nil)
}

func (s *TwoSlidesService) SearchThemes(ctx context.Context, req domain.TwoSlidesThemeSearchRequest) (map[string]any, error) {
	if err := s.ensureConfigured(); err != nil {
		return nil, err
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, apperrors.Validation("query is required")
	}

	values := url.Values{}
	values.Set("query", query)
	if req.Limit > 0 {
		values.Set("limit", fmt.Sprintf("%d", req.Limit))
	}
	return s.doJSONRequest(ctx, http.MethodGet, "/api/v1/themes/search?"+values.Encode(), nil)
}

func (s *TwoSlidesService) ensureConfigured() error {
	if s.baseURL == "" || s.apiKey == "" {
		return apperrors.New(apperrors.ErrCodeInternal, "2slides integration is not configured", http.StatusServiceUnavailable)
	}
	return nil
}

func (s *TwoSlidesService) doJSONRequest(ctx context.Context, method, endpoint string, payload any) (map[string]any, error) {
	if _, blocked := s.client.Transport.(aiaccess.BlockedTransport); blocked && method != http.MethodGet && method != http.MethodHead {
		return nil, aiaccess.ContractUnavailable()
	}
	var bodyReader io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, apperrors.InternalWithErr("failed to encode 2slides request", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+endpoint, bodyReader)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to create 2slides request", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, apperrors.New(apperrors.ErrCodeInternal, "2slides request failed", http.StatusBadGateway).WithError(err)
	}
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to read 2slides response", err)
	}

	var parsed map[string]any
	if len(rawBody) > 0 {
		if err := json.Unmarshal(rawBody, &parsed); err != nil {
			s.logger.Warn("failed to decode 2slides response",
				zap.Int("status", resp.StatusCode),
				zap.String("endpoint", endpoint),
				zap.ByteString("body", rawBody),
				zap.Error(err),
			)
			return nil, apperrors.New(apperrors.ErrCodeInternal, "invalid response from 2slides", http.StatusBadGateway).WithError(err)
		}
	} else {
		parsed = map[string]any{}
	}

	if resp.StatusCode >= 400 {
		message := "2slides request failed"
		if msg, ok := parsed["message"].(string); ok && strings.TrimSpace(msg) != "" {
			message = msg
		}
		if errObj, ok := parsed["error"].(map[string]any); ok {
			if msg, ok := errObj["message"].(string); ok && strings.TrimSpace(msg) != "" {
				message = msg
			}
		}
		return nil, apperrors.New(apperrors.ErrCodeInternal, message, http.StatusBadGateway).WithDetails(parsed)
	}

	return parsed, nil
}
