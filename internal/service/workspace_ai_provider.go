package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/west6ide/workspace-backend/internal/alem"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// WorkspaceAIProvider owns provider credentials and accounts for each physical
// model request from the Python tools. Neither payer nor usage comes from Python.
type WorkspaceAIProvider struct {
	paid                     AIPaidCallerService
	users                    aiPayerUsers
	url, token               string
	client                   *http.Client
	openAIKey, elevenLabsKey string
	jobSigningKey            []byte
	probeAudio               func(context.Context, []byte) (int64, error)
	streamingUsageConfirmed  bool
}

func (s *WorkspaceAIProvider) SetStreamingUsageConfirmed(confirmed bool) {
	s.streamingUsageConfirmed = confirmed
}

func (s *WorkspaceAIProvider) SetSpeechCredentials(openAIKey, elevenLabsKey, jobSigningKey string) {
	s.openAIKey, s.elevenLabsKey, s.jobSigningKey = strings.TrimSpace(openAIKey), strings.TrimSpace(elevenLabsKey), []byte(jobSigningKey)
}

func NewWorkspaceAIProvider(paid AIPaidCallerService, users aiPayerUsers, baseURL, token string, timeout time.Duration) *WorkspaceAIProvider {
	if timeout < 120*time.Second {
		timeout = 120 * time.Second
	}
	return &WorkspaceAIProvider{paid: paid, users: users, url: strings.TrimRight(baseURL, "/") + "/v1/chat/completions", probeAudio: probeWorkspaceAIAudio,
		token:  strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(token), "Bearer ")),
		client: &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// Complete preserves provider-specific options and function-call arguments. The
// callback forwards SSE data, but [DONE] is withheld until settlement commits.
func (s *WorkspaceAIProvider) Complete(ctx context.Context, userID int64, requestID string, raw []byte, emit func([]byte) error) (json.RawMessage, error) {
	ctx, err := aiPaidUserContext(s.paid, s.users, ctx, userID)
	if err != nil {
		return nil, err
	}
	if s.token == "" {
		return nil, domain.ErrAIAccountingDisabled
	}
	payload, model, input, output, stream, err := workspaceAIRequest(raw)
	if err != nil {
		return nil, err
	}
	upstreamStream := stream && s.streamingUsageConfirmed
	if stream && !upstreamStream {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(payload, &fields)
		fields["stream"] = json.RawMessage("false")
		delete(fields, "stream_options")
		payload, _ = json.Marshal(fields)
	}
	if requestID == "" || len(requestID) > 100 {
		return nil, domain.ErrAIPaidCallInvalidRequest
	}
	key := domain.AICreditModelKey{ProviderID: "nitec", EndpointKind: "chat", ModelID: model, PolicyVersion: domain.AICreditPolicyV1}
	estimate, err := domain.CalculateAICredits(key, domain.AICreditUsage{Status: "derived", InputTokens: &input, OutputTokens: &output})
	if err != nil {
		return nil, err
	}
	fingerprint := sha256.Sum256(payload)
	source := "workspace_ai"
	params := domain.AIPaidCallParams{IdempotencyKey: "workspace_ai:" + strconv.FormatInt(userID, 10) + ":" + requestID,
		OperationKind: "chat", Model: key, UsageAdapterVersion: alemChatUsageAdapter, Microcredits: estimate.Microcredits,
		Fingerprint: fingerprint, AttemptFingerprint: fingerprint, AttemptNumber: 1, SourceKind: &source}
	result, reservation, _, err := ExecutePaidCallTyped(ctx, s.paid, params, func(ctx context.Context, _ domain.AIReservation) (json.RawMessage, domain.AISettleRequest, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(payload))
		if err != nil {
			return nil, domain.AISettleRequest{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+s.token)
		resp, err := s.client.Do(req)
		if err != nil {
			return nil, domain.AISettleRequest{}, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, domain.AISettleRequest{}, &alem.APIError{StatusCode: resp.StatusCode}
		}
		if upstreamStream {
			evidence, err := workspaceAIStream(resp.Body, model, emit)
			return nil, evidence, err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
		if err != nil || len(body) > 4*1024*1024 {
			return nil, domain.AISettleRequest{}, errors.Join(err, domain.ErrAICreditUnknownUsage)
		}
		evidence, err := workspaceAIEvidence(body, model)
		return json.RawMessage(body), evidence, err
	})
	if err == nil && !reservation.Created {
		return nil, domain.ErrAIReservationConflict
	}
	if err == nil && stream && emit != nil {
		if !upstreamStream {
			var chunk map[string]any
			if json.Unmarshal(result, &chunk) != nil {
				return nil, domain.ErrAICreditUnknownUsage
			}
			chunk["object"] = "chat.completion.chunk"
			if choices, ok := chunk["choices"].([]any); ok {
				for _, item := range choices {
					choice, ok := item.(map[string]any)
					if !ok {
						continue
					}
					if message, ok := choice["message"].(map[string]any); ok {
						if calls, ok := message["tool_calls"].([]any); ok {
							for index, item := range calls {
								if call, ok := item.(map[string]any); ok {
									call["index"] = index
								}
							}
						}
						choice["delta"] = message
						delete(choice, "message")
					}
				}
			}
			encoded, _ := json.Marshal(chunk)
			err = emit(append(append([]byte("data: "), encoded...), '\n', '\n'))
		}
		if err == nil {
			err = emit([]byte("data: [DONE]\n\n"))
		}
	}
	return result, err
}

func workspaceAIRequest(raw []byte) ([]byte, string, int64, int64, bool, error) {
	invalid := func() ([]byte, string, int64, int64, bool, error) {
		return nil, "", 0, 0, false, domain.ErrAIPaidCallInvalidRequest
	}
	if len(raw) == 0 || len(raw) > 512*1024 {
		return invalid()
	}
	var fields map[string]json.RawMessage
	var req struct {
		Model    string `json:"model"`
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		MaxTokens           int64 `json:"max_tokens"`
		MaxCompletionTokens int64 `json:"max_completion_tokens"`
		N                   *int  `json:"n"`
		Stream              bool  `json:"stream"`
	}
	if json.Unmarshal(raw, &fields) != nil || json.Unmarshal(raw, &req) != nil || len(req.Messages) == 0 || req.Model == "" || (req.N != nil && *req.N != 1) {
		return invalid()
	}
	if req.MaxCompletionTokens != 0 {
		if req.MaxTokens != 0 {
			return invalid()
		}
		req.MaxTokens = req.MaxCompletionTokens
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = 2048
		fields["max_tokens"] = json.RawMessage("2048")
	}
	if req.MaxTokens < 1 || req.MaxTokens > 16384 {
		return invalid()
	}
	input := int64(len(raw) + 512*len(req.Messages))
	for _, message := range req.Messages {
		var parts []struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(message.Content, &parts) == nil {
			for _, part := range parts {
				if part.Type == "image_url" {
					input += 32768
				}
				if part.Type == "input_audio" || part.Type == "video_url" {
					return invalid()
				}
			}
		}
	}
	if req.Stream {
		fields["stream_options"] = json.RawMessage(`{"include_usage":true}`)
	}
	payload, err := json.Marshal(fields)
	return payload, req.Model, input, req.MaxTokens, req.Stream, err
}

func workspaceAIEvidence(raw []byte, requestedModel string) (domain.AISettleRequest, error) {
	var response alem.ChatCompletionResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return domain.AISettleRequest{}, domain.ErrAICreditUnknownUsage
	}
	if response.Model != requestedModel {
		return domain.AISettleRequest{}, domain.ErrAICreditUnknownModel
	}
	return alemChatEvidence(&response)
}

func workspaceAIStream(body io.Reader, model string, emit func([]byte) error) (domain.AISettleRequest, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), 2*1024*1024)
	var evidence domain.AISettleRequest
	var usageErr error = domain.ErrAICreditUnknownUsage
	done := false
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if bytes.Equal(data, []byte("[DONE]")) {
			done = true
			break
		}
		var chunk struct {
			Model string          `json:"model"`
			Usage json.RawMessage `json:"usage"`
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal(data, &chunk) != nil || (len(chunk.Error) != 0 && string(chunk.Error) != "null") {
			return evidence, domain.ErrAICreditUnknownUsage
		}
		if chunk.Model != model {
			return evidence, domain.ErrAICreditUnknownModel
		}
		if len(chunk.Usage) != 0 && string(chunk.Usage) != "null" {
			evidence, usageErr = workspaceAIEvidence(data, model)
		}
		if emit != nil {
			if err := emit(append(append([]byte("data: "), data...), '\n', '\n')); err != nil {
				return evidence, err
			}
		}
	}
	if scanner.Err() != nil {
		return evidence, scanner.Err()
	}
	if !done {
		return evidence, fmt.Errorf("incomplete provider stream: %w", domain.ErrAICreditUnknownUsage)
	}
	return evidence, usageErr
}
