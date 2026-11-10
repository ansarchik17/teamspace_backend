package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/config"
	"go.uber.org/zap"
)

func TestUnaccountedProviderBoundaries(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(200) }))
	defer srv.Close()
	drive := NewDriveSearchService(config.DriveSearchConfig{AlemAPIKey: "test-only", EmbeddingModel: "BAAI/bge-m3", EmbeddingDims: 3}, zap.NewNop())
	require.IsType(t, unaccountedDriveProvider{}, drive.embedder)
	_, err := drive.embedder.CreateChatCompletion(ctx, alem.ChatCompletionRequest{})
	require.Error(t, err)
	_, err = drive.embedder.CreateEmbeddings(ctx, alem.EmbeddingRequest{})
	require.Error(t, err)
	_, err = drive.embedder.Rerank(ctx, alem.RerankRequest{})
	require.Error(t, err)
	parser := NewContentExtractionService(config.DocumentParseConfig{URL: srv.URL, Timeout: time.Second}, zap.NewNop())
	_, err = parser.extractViaDocumentParse(ctx, "example.txt", []byte("private document"))
	require.Error(t, err)
	slides := NewTwoSlidesService(config.TwoSlidesConfig{BaseURL: srv.URL, APIKey: "test-only", Timeout: time.Second}, zap.NewNop())
	_, err = slides.doJSONRequest(ctx, http.MethodPost, "/generate", map[string]string{"prompt": "test"})
	require.Error(t, err)
	require.Zero(t, calls.Load(), "no unaccounted generation or parsing may reach a network transport")
	legacy := &SmartAgentService{cfg: &config.Config{Agent: config.AgentConfig{AdaptiveWebSearchURL: srv.URL}}, httpClient: srv.Client()}
	message, widget := legacy.toolWebSearch(ctx, "test query")
	require.NotEmpty(t, message)
	require.Nil(t, widget)
	require.Zero(t, calls.Load(), "legacy Smart Agent web tool must also be gated")
}
