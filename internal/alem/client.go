package alem

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

var BaseURL = "https://llm.nitec.kz"

type Client struct {
	token          string
	httpClient     *http.Client
	requestTimeout time.Duration
	maxRetries     int
}

func NewClient(token string, requestTimeout time.Duration, maxRetries int) *Client {
	if requestTimeout <= 0 {
		requestTimeout = 15 * time.Second
	}
	if maxRetries < 0 {
		maxRetries = 0
	}

	return &Client{
		token:          normalizeAPIToken(token),
		requestTimeout: requestTimeout,
		maxRetries:     maxRetries,
		httpClient: &http.Client{
			Timeout: requestTimeout,
		},
	}
}

// normalizeAPIToken accepts both the secret itself and a value copied from an
// Authorization header. Configuration must have one canonical representation
// at the HTTP boundary; otherwise "Bearer Bearer ..." is sent upstream.
func normalizeAPIToken(raw string) string {
	token := strings.TrimSpace(raw)
	if len(token) >= len("Bearer ") && strings.EqualFold(token[:len("Bearer ")], "Bearer ") {
		return strings.TrimSpace(token[len("Bearer "):])
	}
	return token
}

// do handles authentication, retries, and basic error parsing
func (c *Client) do(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	var err error

	req.Header.Set("Authorization", "Bearer "+normalizeAPIToken(c.token))
	req.Header.Set("Content-Type", "application/json")

	for i := 0; i <= c.maxRetries; i++ {
		if i > 0 {
			if req.GetBody != nil {
				body, bodyErr := req.GetBody()
				if bodyErr != nil {
					return nil, fmt.Errorf("recreate request body: %w", bodyErr)
				}
				req.Body = body
			}
			// Exponential backoff: 1s, 2s, 4s...
			backoff := time.Duration(math.Pow(2, float64(i))) * time.Second
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(backoff):
			}
		}

		resp, err = c.httpClient.Do(req)
		if err != nil {
			// Network error, usually retryable
			continue
		}

		// Check for specific retryable status codes
		// 429: Too Many Requests
		// 5xx: Server Errors
		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500) && i < c.maxRetries {
			resp.Body.Close()
			continue
		}

		// If we got a response that isn't retryable, we break the loop
		break
	}

	if err != nil {
		return nil, fmt.Errorf("request failed after retries: %w", err)
	}

	// Handle 4xx errors by parsing the body
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		var errorResp ErrorResponse
		bodyBytes, _ := io.ReadAll(resp.Body)

		if jsonErr := json.Unmarshal(bodyBytes, &errorResp); jsonErr == nil && errorResp.Error.Message != "" {
			return nil, &APIError{
				StatusCode: resp.StatusCode,
				Details:    errorResp.Error,
				Response:   errorResp,
			}
		}

		// Fallback if JSON parse fails
		return nil, &APIError{
			StatusCode: resp.StatusCode,
			Details: ErrorDetails{
				Message: fmt.Sprintf("Request failed with status %d: %s", resp.StatusCode, string(bodyBytes)),
				Type:    "api_error",
			},
		}
	}

	return resp, nil
}

func (c *Client) sendRequest(ctx context.Context, method, endpoint string, body interface{}, result interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		jsonBytes, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal error: %w", err)
		}
		bodyReader = bytes.NewBuffer(jsonBytes)
	}

	if _, hasDeadline := ctx.Deadline(); !hasDeadline && c.requestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.requestTimeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, method, BaseURL+endpoint, bodyReader)
	if err != nil {
		return fmt.Errorf("create request error: %w", err)
	}

	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("decode response error: %w", err)
		}
	}

	return nil
}
