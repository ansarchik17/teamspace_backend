package service

import (
	"context"

	"github.com/west6ide/workspace-backend/internal/aiaccess"
	"github.com/west6ide/workspace-backend/internal/alem"
)

// Until an attributed accounting adapter is installed, Drive retains lexical
// search/local indexing but cannot call chat, embeddings or rerank directly.
type unaccountedDriveProvider struct{}

func (unaccountedDriveProvider) CreateChatCompletion(context.Context, alem.ChatCompletionRequest) (*alem.ChatCompletionResponse, error) {
	return nil, aiaccess.ContractUnavailable()
}
func (unaccountedDriveProvider) CreateEmbeddings(context.Context, alem.EmbeddingRequest) (*alem.EmbeddingResponse, error) {
	return nil, aiaccess.ContractUnavailable()
}
func (unaccountedDriveProvider) Rerank(context.Context, alem.RerankRequest) (*alem.RerankResponse, error) {
	return nil, aiaccess.ContractUnavailable()
}
