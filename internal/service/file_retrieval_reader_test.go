package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type testFileSearcher struct {
	user  int64
	req   domain.DriveSearchQueryRequest
	calls int
}

func (f *testFileSearcher) Search(_ context.Context, user int64, req domain.DriveSearchQueryRequest) (*domain.DriveSearchQueryResponse, error) {
	f.user = user
	f.req = req
	f.calls++
	return &domain.DriveSearchQueryResponse{Question: req.Question, Indexing: true}, nil
}
func TestFileReaderAndAgentShareAuthorizedSearchContract(t *testing.T) {
	search := &testFileSearcher{}
	reader := NewHTTPAIUserDataReader(nil, nil)
	RegisterFileSearchReader(reader, search)
	identity := AIDataIdentity{UserID: 42, Authorization: "Bearer current"}
	result := reader.Read(context.Background(), identity, AIDataQuery{Dataset: "drive_search", Params: map[string]string{"query": "порядок увольнения"}})
	require.Equal(t, "ok", result.Status)
	require.True(t, result.Truncated)
	require.Equal(t, int64(42), search.user)
	require.Equal(t, "порядок увольнения", search.req.Question)
	for _, q := range []AIDataQuery{
		{Dataset: "drive_search", Params: map[string]string{"query": "test", "user_id": "7"}},
		{Dataset: "drive_search", Params: map[string]string{"query": "test", "limit": "999"}},
		{Dataset: "drive_search", Params: map[string]string{"query": ""}},
	} {
		require.Equal(t, "invalid_request", reader.Read(context.Background(), identity, q).Status)
	}
	require.Equal(t, 1, search.calls)
	agent := &SmartAgentService{fileSearch: search}
	reply, widget := agent.executeTool(context.Background(), 42, "search_file_content", `{"question":"порядок увольнения"}`)
	require.Contains(t, reply, `"indexing":true`)
	require.Equal(t, "drive_content_answer", widget.Type)
	require.Equal(t, 2, search.calls)
	_, _ = agent.executeTool(context.Background(), 42, "search_file_content", `{"question":"test","user_id":7}`)
	require.Equal(t, 2, search.calls, "model cannot choose a different identity")
}
