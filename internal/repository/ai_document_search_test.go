package repository

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDocumentSearchRejectsInvalidScope(t *testing.T) {
	ids, err := documentSearchIDs([]string{"bad-id"})
	require.Error(t, err)
	require.Nil(t, ids)
}

func TestDocumentFusionPromotesAgreementAndPreservesSemanticOnlyHits(t *testing.T) {
	lexical := []AIDocumentChunk{{ID: "exact"}, {ID: "both"}}
	semantic := []AIDocumentChunk{{ID: "meaning"}, {ID: "both"}}
	hits := fuseDocumentChunks(lexical, semantic, 3)
	require.Len(t, hits, 3)
	require.Equal(t, "both", hits[0].ID)
	require.Equal(t, "exact", hits[1].ID)
	require.Equal(t, "meaning", hits[2].ID)
	require.Len(t, fuseDocumentChunks(lexical, semantic, 1), 1)
}

func TestDocumentJobRejectsMissingAttemptBeforeDatabase(t *testing.T) {
	repo := &AIDocumentRepository{}
	require.ErrorIs(t, repo.MarkReady(context.Background(), "job", AIDocument{}, nil), ErrAIDocumentLeaseLost)
	require.ErrorIs(t, repo.MarkFailed(context.Background(), "job", "doc", "code", "message", true, 0), ErrAIDocumentLeaseLost)
}
