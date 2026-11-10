package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/repository"
)

func TestMakeDocumentChunksPreservesOrderAndBounds(t *testing.T) {
	doc := &repository.AIDocument{ID: "doc", ConversationID: "chat", Name: "report.txt"}
	text := "first section\n" + string(make([]rune, AIDocumentChunkChars)) + "\nlast section"
	chunks := makeDocumentChunks(doc, text)
	require.GreaterOrEqual(t, len(chunks), 2)
	require.Equal(t, 0, chunks[0].Ordinal)
	require.Equal(t, 1, chunks[1].Ordinal)
	require.Equal(t, 1, chunks[0].Locator["chunk"])
	for _, chunk := range chunks {
		require.NotEmpty(t, chunk.Text)
		require.LessOrEqual(t, len([]rune(chunk.Text)), AIDocumentChunkChars)
	}
}

func TestValidAIDocumentSignature(t *testing.T) {
	require.True(t, validAIDocumentSignature(".txt", []byte("anything")))
	require.True(t, validAIDocumentSignature(".pdf", []byte("%PDF-1.7")))
	require.False(t, validAIDocumentSignature(".pdf", []byte("not pdf")))
	require.True(t, validAIDocumentSignature(".docx", []byte{'P', 'K', 3, 4}))
	require.False(t, validAIDocumentSignature(".docx", []byte("plain")))
}
