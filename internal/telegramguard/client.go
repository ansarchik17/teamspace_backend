package telegramguard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/west6ide/workspace-backend/internal/auth"
	"go.uber.org/zap"
)

type Client struct {
	baseURL   string
	apiSecret string
	client    *http.Client
	logger    *zap.Logger
}

func New(baseURL, apiSecret string, timeout time.Duration, logger *zap.Logger) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Client{
		baseURL:   baseURL,
		apiSecret: strings.TrimSpace(apiSecret),
		client: &http.Client{
			Timeout: timeout,
		},
		logger: logger.With(zap.String("component", "telegram_guard_client")),
	}
}

func (c *Client) Enabled() bool {
	return c != nil && c.baseURL != "" && c.apiSecret != ""
}

func (c *Client) CheckBlocked(ctx context.Context, userID int64, deviceID string) (bool, error) {
	if !c.Enabled() {
		return false, nil
	}

	endpoint := fmt.Sprintf("%s/api/v1/users/%s/status?device_id=%s",
		c.baseURL,
		url.PathEscape(strconv.FormatInt(userID, 10)),
		url.QueryEscape(strings.TrimSpace(deviceID)),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("X-API-Secret", c.apiSecret)

	resp, err := c.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("telegram guard status failed: %s", strings.TrimSpace(string(body)))
	}

	var payload struct {
		Blocked bool `json:"blocked"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return false, err
	}
	return payload.Blocked, nil
}

func (c *Client) NotifyLogin(ctx context.Context, event auth.LoginEvent) error {
	if !c.Enabled() {
		return nil
	}

	payload := map[string]any{
		"user_id":    strconv.FormatInt(event.UserID, 10),
		"username":   event.Username,
		"full_name":  event.FullName,
		"source":     fallback(event.Source, "unknown"),
		"ip":         event.IP,
		"user_agent": event.UserAgent,
		"device_id":  event.DeviceID,
		"session_id": event.SessionID,
		"extra": map[string]string{
			"email":       event.Email,
			"auth_method": fallback(event.AuthMethod, "password"),
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/events/login", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Secret", c.apiSecret)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram guard login event failed: %s", strings.TrimSpace(string(body)))
	}
	return nil
}

func fallback(value, defaultValue string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultValue
	}
	return value
}
