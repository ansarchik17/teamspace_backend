package service

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"

	"github.com/west6ide/workspace-backend/internal/config"
)

const (
	apnsHostProduction = "https://api.push.apple.com"
	apnsHostSandbox    = "https://api.sandbox.push.apple.com"
	apnsTokenTTL       = 50 * time.Minute // Apple JWT expires in 1h, обновляем заранее
)

var ErrAPNSVoIPTokenUnregistered = errors.New("apns voip token unregistered")

type apnsVoIPClient struct {
	host     string
	bundleID string
	keyID    string
	teamID   string
	key      *ecdsa.PrivateKey
	http     *http.Client

	mu          sync.Mutex
	cachedToken string
	tokenExpiry time.Time
}

type apnsVoIPPayload struct {
	Aps          map[string]interface{} `json:"aps"`
	Event        string                 `json:"event"`
	CallID       string                 `json:"call_id"`
	CallerName   string                 `json:"caller_name"`
	FromFullName string                 `json:"from_full_name"`
	FromUserID   string                 `json:"from_user_id"`
	CallerID     string                 `json:"caller_id"`
	RoomName     string                 `json:"room_name"`
	RoomID       string                 `json:"room_id"`
	ChatID       string                 `json:"chat_id,omitempty"`
	IsVideo      bool                   `json:"is_video"`
	LivekitURL   string                 `json:"livekit_url,omitempty"`
	LivekitToken string                 `json:"livekit_token,omitempty"`
	CallerAvatar string                 `json:"caller_avatar,omitempty"`
}

func newAPNSVoIPClientFromConfig(cfg config.APNSConfig, logger *zap.Logger) *apnsVoIPClient {
	if (cfg.KeyPath == "" && cfg.KeyContent == "") || cfg.KeyID == "" || cfg.TeamID == "" || cfg.BundleID == "" {
		logger.Warn("APNS VoIP not configured: missing APNS_KEY_CONTENT or APNS_KEY_PATH, or APNS_KEY_ID/APNS_TEAM_ID/APNS_BUNDLE_ID")
		return nil
	}

	keyData := []byte(cfg.KeyContent)
	if len(keyData) == 0 {
		data, err := os.ReadFile(cfg.KeyPath)
		if err != nil {
			logger.Error("APNS VoIP: cannot read .p8 key file", zap.String("path", cfg.KeyPath), zap.Error(err))
			return nil
		}
		keyData = data
	}

	key, err := parseAPNSECKey(keyData)
	if err != nil {
		logger.Error("APNS VoIP: cannot parse EC key", zap.Error(err))
		return nil
	}

	host := apnsHostProduction
	if cfg.Sandbox {
		host = apnsHostSandbox
	}

	httpClient := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			ForceAttemptHTTP2:   true,
			TLSHandshakeTimeout: 5 * time.Second,
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}

	logger.Info("APNS VoIP client initialized",
		zap.String("bundle_id", cfg.BundleID),
		zap.Bool("sandbox", cfg.Sandbox),
	)

	return &apnsVoIPClient{
		host:     host,
		bundleID: cfg.BundleID,
		keyID:    cfg.KeyID,
		teamID:   cfg.TeamID,
		key:      key,
		http:     httpClient,
	}
}

func parseAPNSECKey(data []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	raw, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := raw.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is not ECDSA")
	}
	return key, nil
}

func (c *apnsVoIPClient) getJWT() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cachedToken != "" && time.Now().Before(c.tokenExpiry) {
		return c.cachedToken, nil
	}

	now := time.Now()
	t := jwt.New(jwt.SigningMethodES256)
	t.Header["kid"] = c.keyID
	t.Claims = jwt.MapClaims{
		"iss": c.teamID,
		"iat": now.Unix(),
	}

	signed, err := t.SignedString(c.key)
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}

	c.cachedToken = signed
	c.tokenExpiry = now.Add(apnsTokenTTL)
	return signed, nil
}

func (c *apnsVoIPClient) sendVoIP(ctx context.Context, deviceToken string, p apnsVoIPPayload, expiration time.Time, logger *zap.Logger) error {
	body, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	url := fmt.Sprintf("%s/3/device/%s", c.host, deviceToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}

	bearerToken, err := c.getJWT()
	if err != nil {
		return fmt.Errorf("jwt: %w", err)
	}

	req.Header.Set("authorization", "bearer "+bearerToken)
	req.Header.Set("apns-push-type", "voip")
	req.Header.Set("apns-topic", c.bundleID+".voip")
	req.Header.Set("apns-priority", "10")
	req.Header.Set("apns-expiration", fmt.Sprintf("%d", expiration.Unix()))
	req.Header.Set("content-type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return nil
	}

	var errResp struct {
		Reason    string `json:"reason"`
		Timestamp int64  `json:"timestamp"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&errResp)

	// 410 = DeviceTokenNoLongerActive — токен устарел, можно удалить
	if resp.StatusCode == http.StatusGone {
		logger.Warn("APNS VoIP: device token expired",
			zap.String("token_prefix", safePrefix(deviceToken)),
			zap.String("reason", errResp.Reason),
		)
		return fmt.Errorf("%w: apns %d: %s", ErrAPNSVoIPTokenUnregistered, resp.StatusCode, errResp.Reason)
	}
	if resp.StatusCode == http.StatusBadRequest && (errResp.Reason == "BadDeviceToken" || errResp.Reason == "Unregistered") {
		logger.Warn("APNS VoIP: invalid device token",
			zap.String("token_prefix", safePrefix(deviceToken)),
			zap.String("reason", errResp.Reason),
			zap.String("host", c.host),
		)
		return fmt.Errorf("%w: apns %d: %s", ErrAPNSVoIPTokenUnregistered, resp.StatusCode, errResp.Reason)
	}

	return fmt.Errorf("apns %d: %s", resp.StatusCode, errResp.Reason)
}

func safePrefix(s string) string {
	if len(s) > 8 {
		return s[:8] + "..."
	}
	return s
}
