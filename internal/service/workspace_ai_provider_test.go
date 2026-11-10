package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type workspaceAITransport func(*http.Request) (*http.Response, error)

func (f workspaceAITransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func workspaceAIProviderTest(t *testing.T, store *aiAccountingStub, response string) (*WorkspaceAIProvider, *int) {
	t.Helper()
	org := uuid.New()
	s := NewWorkspaceAIProvider(NewAIPaidCallerService(store), alemPayerStub{user: &domain.User{ID: 42, OrganizationID: &org, IsActive: true}}, "https://provider.example", "server-secret", 0)
	calls := new(int)
	s.client.Transport = workspaceAITransport(func(r *http.Request) (*http.Response, error) {
		*calls++
		require.Equal(t, "Bearer server-secret", r.Header.Get("Authorization"))
		require.Equal(t, "https://provider.example/v1/chat/completions", r.URL.String())
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
	})
	return s, calls
}

const workspaceAIInput = `{"model":"openai/gpt-oss-120b","messages":[{"role":"user","content":"test"}],"max_tokens":20}`
const workspaceAIOutput = `{"id":"req-123","object":"chat.completion","model":"openai/gpt-oss-120b","choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":12,"completion_tokens":2,"total_tokens":14}}`

func TestWorkspaceAIProviderAccountsTrustedUsage(t *testing.T) {
	store := &aiAccountingStub{reservation: domain.AIReservation{Created: true, AttemptID: uuid.New()}}
	s, calls := workspaceAIProviderTest(t, store, workspaceAIOutput)
	body, err := s.Complete(context.Background(), 42, "turn:1", []byte(workspaceAIInput), nil)
	require.NoError(t, err)
	require.JSONEq(t, workspaceAIOutput, string(body))
	require.Equal(t, 1, *calls)
	require.Len(t, store.completed, 1)
	require.EqualValues(t, 12, *store.completed[0].Usage.InputTokens)
	require.Equal(t, "verified", store.completed[0].Usage.Status)
}

func TestWorkspaceAIProviderNoDispatchWithoutBudgetOrOnReplay(t *testing.T) {
	for _, created := range []bool{false, true} {
		store := &aiAccountingStub{reservation: domain.AIReservation{Created: created}}
		if created {
			store.reserveErr = domain.ErrAIAccountingDisabled
		}
		s, calls := workspaceAIProviderTest(t, store, workspaceAIOutput)
		_, err := s.Complete(context.Background(), 42, "turn:1", []byte(workspaceAIInput), nil)
		require.Error(t, err)
		require.Zero(t, *calls)
	}
}

func TestWorkspaceAIProviderRejectsForeignIdentityAndUnpricedModel(t *testing.T) {
	store := &aiAccountingStub{}
	s, calls := workspaceAIProviderTest(t, store, workspaceAIOutput)
	_, err := s.Complete(context.Background(), 7, "turn:1", []byte(workspaceAIInput), nil)
	require.ErrorIs(t, err, domain.ErrAIPaidCallInvalidCaller)
	_, err = s.Complete(context.Background(), 42, "turn:2", []byte(strings.ReplaceAll(workspaceAIInput, "openai/gpt-oss-120b", "unpriced-model")), nil)
	require.ErrorIs(t, err, domain.ErrAICreditUnknownModel)
	require.Zero(t, *calls)
	require.Zero(t, store.reserveCalls)
}

func TestWorkspaceAIProviderUnknownUsageRetainsReservation(t *testing.T) {
	for _, response := range []string{
		`{"model":"openai/gpt-oss-120b","choices":[]}`,
		strings.ReplaceAll(workspaceAIOutput, "openai/gpt-oss-120b", "other-model"),
	} {
		store := &aiAccountingStub{reservation: domain.AIReservation{Created: true, AttemptID: uuid.New()}}
		s, calls := workspaceAIProviderTest(t, store, response)
		_, err := s.Complete(context.Background(), 42, "turn:1", []byte(workspaceAIInput), nil)
		require.Error(t, err)
		require.Equal(t, 1, *calls)
		require.Empty(t, store.completed)
		require.Len(t, store.failures, 1)
		require.Equal(t, domain.AIFailureUnknown, store.failures[0].Outcome)
	}
}

func TestWorkspaceAIProviderStreamSettlesBeforeDone(t *testing.T) {
	store := &aiAccountingStub{reservation: domain.AIReservation{Created: true, AttemptID: uuid.New()}}
	stream := "data: " + workspaceAIOutput + "\n\ndata: [DONE]\n\n"
	s, _ := workspaceAIProviderTest(t, store, stream)
	s.SetStreamingUsageConfirmed(true)
	input := strings.TrimSuffix(workspaceAIInput, "}") + `,"stream":true}`
	var events []string
	_, err := s.Complete(context.Background(), 42, "turn:1", []byte(input), func(data []byte) error {
		events = append(events, string(data))
		if strings.Contains(string(data), "[DONE]") {
			require.Len(t, store.completed, 1)
		}
		return nil
	})
	require.NoError(t, err)
	require.Contains(t, events[len(events)-1], "[DONE]")
	store.completed = nil
	store.settleErr = errors.New("database unavailable")
	events = nil
	_, err = s.Complete(context.Background(), 42, "turn:2", []byte(input), func(data []byte) error { events = append(events, string(data)); return nil })
	require.Error(t, err)
	require.NotContains(t, strings.Join(events, ""), "[DONE]")
}

func TestWorkspaceAIProviderDefaultsToConfirmedUsageContract(t *testing.T) {
	store := &aiAccountingStub{reservation: domain.AIReservation{Created: true, AttemptID: uuid.New()}}
	s, _ := workspaceAIProviderTest(t, store, workspaceAIOutput)
	s.client.Transport = workspaceAITransport(func(r *http.Request) (*http.Response, error) {
		var request map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.Equal(t, false, request["stream"])
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(workspaceAIOutput))}, nil
	})
	input := strings.TrimSuffix(workspaceAIInput, "}") + `,"stream":true}`
	var events []string
	_, err := s.Complete(context.Background(), 42, "buffered:1", []byte(input), func(data []byte) error {
		require.Len(t, store.completed, 1, "no answer is emitted before accounting commits")
		events = append(events, string(data))
		return nil
	})
	require.NoError(t, err)
	require.Contains(t, events[0], `"delta"`)
	require.Contains(t, events[0], `"content":"ok"`)
	require.Contains(t, events[1], "[DONE]")
}

func TestWorkspaceAIRequestPreservesOptionsAndBoundsEveryGeneration(t *testing.T) {
	input := strings.TrimSuffix(workspaceAIInput, "}") + `,"response_format":{"type":"json_object"},"chat_template_kwargs":{"enable_thinking":false},"stream":true}`
	payload, _, _, _, _, err := workspaceAIRequest([]byte(input))
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(payload, &fields))
	require.JSONEq(t, `{"type":"json_object"}`, string(fields["response_format"]))
	require.JSONEq(t, `{"include_usage":true}`, string(fields["stream_options"]))
	for _, invalid := range []string{
		strings.ReplaceAll(workspaceAIInput, `"max_tokens":20`, `"max_tokens":17000`),
		strings.TrimSuffix(workspaceAIInput, "}") + `,"n":2}`,
		strings.ReplaceAll(workspaceAIInput, `"max_tokens":20`, `"max_tokens":-1`),
	} {
		_, _, _, _, _, err := workspaceAIRequest([]byte(invalid))
		require.ErrorIs(t, err, domain.ErrAIPaidCallInvalidRequest)
	}
}

func TestWorkspaceAIStreamRequiresFinalUsageAndDone(t *testing.T) {
	for _, data := range []string{
		"data: " + workspaceAIOutput + "\n\n",
		"data: {\"model\":\"openai/gpt-oss-120b\",\"choices\":[]}\n\ndata: [DONE]\n\n",
	} {
		_, err := workspaceAIStream(strings.NewReader(data), "openai/gpt-oss-120b", nil)
		require.Error(t, err)
	}
}
