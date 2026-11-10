package fileurl

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

func SignLegacyDownloadURL(rawURL *string) *string {
	if rawURL == nil {
		return nil
	}

	trimmed := strings.TrimSpace(*rawURL)
	if trimmed == "" {
		return rawURL
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return rawURL
	}

	query := parsed.Query()
	if query.Get("object_name") == "" {
		return rawURL
	}

	path := strings.TrimSpace(parsed.Path)
	if !strings.HasSuffix(path, "/api/v1/files/download") && path != "/api/v1/files/download" {
		return rawURL
	}

	objectName := query.Get("object_name")
	if objectName == "" {
		return rawURL
	}

	publicURL := strings.TrimRight(strings.TrimSpace(os.Getenv("APP_PUBLIC_URL")), "/")
	if publicURL == "" {
		publicURL = strings.TrimRight(parsed.Scheme+"://"+parsed.Host, "/")
	}
	if publicURL == "" {
		return rawURL
	}

	expires := time.Now().Add(24 * time.Hour).Unix()
	signature := generateHMAC(objectName, expires)
	signed := fmt.Sprintf("%s/api/v1/files/download?object_name=%s&expires=%d&signature=%s",
		publicURL,
		url.QueryEscape(objectName),
		expires,
		signature,
	)

	return &signed
}

func StringValue(rawURL *string, fallback string) string {
	if rawURL == nil {
		return fallback
	}
	return *rawURL
}

func generateHMAC(objectName string, expires int64) string {
	secret := strings.TrimSpace(os.Getenv("MINIO_SECRET_KEY"))
	if secret == "" {
		secret = "alem_file_download_secret_key"
	}

	h := hmac.New(sha256.New, []byte(secret+":file_download"))
	h.Write([]byte(fmt.Sprintf("%s:%d", objectName, expires)))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
