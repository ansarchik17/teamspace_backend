package alem

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type StreamReceiver interface {
	Recv() (ChatCompletionChunk, error)
	Close() error
}

type streamReceiver struct {
	reader *bufio.Reader
	closer io.Closer
}

func (s *streamReceiver) Recv() (ChatCompletionChunk, error) {
	for {
		line, err := s.reader.ReadBytes('\n')
		if err != nil {
			return ChatCompletionChunk{}, err
		}

		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}

		data := bytes.TrimPrefix(line, []byte("data: "))
		if string(data) == "[DONE]" {
			return ChatCompletionChunk{}, io.EOF
		}

		var chunk ChatCompletionChunk
		if err := json.Unmarshal(data, &chunk); err != nil {
			return ChatCompletionChunk{}, fmt.Errorf("unmarshal chunk error: %w", err)
		}

		return chunk, nil
	}
}

func (s *streamReceiver) Close() error {
	return s.closer.Close()
}

// ListModels GET /v1/models
func (c *Client) ListModels(ctx context.Context) (*ModelListResponse, error) {
	var resp ModelListResponse
	if err := c.sendRequest(ctx, "GET", "/v1/models", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CreateChatCompletion POST /v1/chat/completions (Non-streaming)
func (c *Client) CreateChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	req.Stream = false
	var resp ChatCompletionResponse
	if err := c.sendRequest(ctx, "POST", "/v1/chat/completions", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CreateChatCompletionOnce is used by the accounting adapter: a reservation
// authorizes exactly one HTTP attempt. Redirects and hidden retries cannot
// create additional unaccounted generations. The shared client is not mutated.
func (c *Client) CreateChatCompletionOnce(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	once := *c
	once.maxRetries = 0
	httpClient := *c.httpClient
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	once.httpClient = &httpClient
	return once.CreateChatCompletion(ctx, req)
}

// CreateChatCompletionStream POST /v1/chat/completions (Stream = true)
func (c *Client) CreateChatCompletionStream(ctx context.Context, req ChatCompletionRequest) (StreamReceiver, error) {
	req.Stream = true

	jsonBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request error: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", BaseURL+"/v1/chat/completions", bytes.NewBuffer(jsonBytes))
	if err != nil {
		return nil, fmt.Errorf("create request error: %w", err)
	}

	// Use c.do to get auth & retries
	resp, err := c.do(httpReq)
	if err != nil {
		return nil, err
	}

	return &streamReceiver{
		reader: bufio.NewReader(resp.Body),
		closer: resp.Body,
	}, nil
}

// CreateEmbeddings POST /v1/embeddings
func (c *Client) CreateEmbeddings(ctx context.Context, req EmbeddingRequest) (*EmbeddingResponse, error) {
	var resp EmbeddingResponse
	if err := c.sendRequest(ctx, "POST", "/v1/embeddings", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Rerank POST /v1/rerank
func (c *Client) Rerank(ctx context.Context, req RerankRequest) (*RerankResponse, error) {
	var resp RerankResponse
	if err := c.sendRequest(ctx, "POST", "/v1/rerank", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
