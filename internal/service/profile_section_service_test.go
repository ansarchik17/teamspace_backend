package service

import (
	"net/url"
	"strings"
	"testing"
)

func normalizeProfileAssetInput(rawURL, rawObjectName *string) (*string, *string) {
	objectName := normalizeProfileOptionalString(rawObjectName)
	if objectName != nil {
		return nil, objectName
	}

	urlValue := normalizeProfileOptionalString(rawURL)
	if urlValue == nil {
		return nil, nil
	}

	parsed, err := url.Parse(*urlValue)
	if err != nil {
		return urlValue, nil
	}

	extracted := strings.TrimSpace(parsed.Query().Get("object_name"))
	if extracted == "" {
		return urlValue, nil
	}

	return nil, &extracted
}

func TestNormalizeProfileAssetInputPrefersObjectName(t *testing.T) {
	rawURL := "https://example.com/api/v1/files/download?object_name=image/1/legacy.png"
	rawObjectName := "image/1/new.png"

	urlValue, objectName := normalizeProfileAssetInput(&rawURL, &rawObjectName)

	if urlValue != nil {
		t.Fatalf("expected url to be nil when object_name is provided, got %v", *urlValue)
	}
	if objectName == nil || *objectName != rawObjectName {
		t.Fatalf("expected object_name %q, got %#v", rawObjectName, objectName)
	}
}

func TestNormalizeProfileAssetInputExtractsObjectNameFromURL(t *testing.T) {
	rawURL := "https://example.com/api/v1/files/download?object_name=image/42/avatar.png"

	urlValue, objectName := normalizeProfileAssetInput(&rawURL, nil)

	if urlValue != nil {
		t.Fatalf("expected url to be nil after object_name extraction, got %v", *urlValue)
	}
	if objectName == nil || *objectName != "image/42/avatar.png" {
		t.Fatalf("expected extracted object_name, got %#v", objectName)
	}
}

func TestNormalizeProfileAssetInputKeepsExternalURL(t *testing.T) {
	rawURL := "https://cdn.example.com/images/avatar.png"

	urlValue, objectName := normalizeProfileAssetInput(&rawURL, nil)

	if objectName != nil {
		t.Fatalf("expected object_name to stay nil, got %v", *objectName)
	}
	if urlValue == nil || *urlValue != rawURL {
		t.Fatalf("expected url %q, got %#v", rawURL, urlValue)
	}
}
