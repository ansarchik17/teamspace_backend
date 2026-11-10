package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type ODOCommandClient struct {
	baseURL, issuer string
	client          *http.Client
	enabled         bool
}

func NewODOCommandClient(baseURL, issuer string, enabled bool, timeout time.Duration) (*ODOCommandClient, error) {
	c := &ODOCommandClient{baseURL: strings.TrimRight(baseURL, "/"), issuer: issuer, enabled: enabled}
	if !enabled {
		return c, nil
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || issuer == "" {
		return nil, fmt.Errorf("invalid ODO command configuration")
	}
	identity, err := url.Parse(issuer)
	if err != nil || identity.Scheme != "https" || identity.Host == "" || identity.User != nil || identity.RawQuery != "" || identity.Fragment != "" {
		return nil, fmt.Errorf("invalid ODO command identity issuer")
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	c.client = &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return c, nil
}
func (s *KanbanService) SetODOCommandClient(client *ODOCommandClient) { s.odoCommands = client }

func (s *KanbanService) CompleteODOAssignment(ctx context.Context, actor int64, taskID uuid.UUID, request domain.ODOCompleteRequest, token string) (*domain.ODOCompleteReceipt, error) {
	if !request.Valid() {
		return nil, apperrors.Validation("command id, expected revision and result comment required")
	}
	target, err := s.taskRepo.ODOCompletionTarget(ctx, actor, taskID)
	if err != nil {
		return nil, err
	}
	client := s.odoCommands
	if client == nil || !client.enabled {
		return nil, odoCommandError("ODO_UNAVAILABLE", 503)
	}
	if target.IdentityIssuer != client.issuer || strings.HasPrefix(target.ActorSub, "workspace:") {
		return nil, apperrors.Forbidden("assignment identity issuer mismatch")
	}
	if strings.TrimSpace(token) == "" {
		return nil, apperrors.Unauthorized("user access token required")
	}
	command := domain.ODOCompleteCommand{CommandID: request.CommandID, OrganizationID: target.OrganizationID, IdentityIssuer: target.IdentityIssuer, ActorSub: target.ActorSub, DocumentID: target.DocumentID, ExpectedRevision: request.ExpectedRevision, ResultComment: request.ResultComment}
	return client.complete(ctx, target.AssignmentID, command, token)
}
func odoCommandError(code string, status int) error {
	return apperrors.New(apperrors.ErrorCode(code), "ODO assignment command failed", status)
}

func (c *ODOCommandClient) complete(ctx context.Context, assignment string, command domain.ODOCompleteCommand, token string) (*domain.ODOCompleteReceipt, error) {
	body, err := json.Marshal(command)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/workspace/assignments/"+url.PathEscape(assignment)+"/complete", bytes.NewReader(body))
	if err != nil {
		return nil, odoCommandError("ODO_UNAVAILABLE", 503)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "odo-command:"+command.CommandID.String())
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, odoCommandError("ODO_UNAVAILABLE", 503)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case 400, 422:
			return nil, apperrors.Validation("ODO rejected assignment command")
		case 401:
			return nil, odoCommandError("ODO_REAUTH_REQUIRED", 401)
		case 403:
			return nil, apperrors.Forbidden("ODO assignment completion access denied")
		case 404:
			return nil, apperrors.NotFound("ODO assignment")
		case 409:
			return nil, odoCommandError("ODO_COMMAND_CONFLICT", 409)
		default:
			return nil, odoCommandError("ODO_UNAVAILABLE", 503)
		}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (32<<10)+1))
	if err != nil || len(raw) > 32<<10 {
		return nil, odoCommandError("ODO_INVALID_RECEIPT", 502)
	}
	fields, err := domain.DecodeODOObject(raw)
	if err != nil || len(fields) != 12 {
		return nil, odoCommandError("ODO_INVALID_RECEIPT", 502)
	}
	var receipt domain.ODOCompleteReceipt
	if err = json.Unmarshal(raw, &receipt); err != nil {
		return nil, odoCommandError("ODO_INVALID_RECEIPT", 502)
	}
	if receipt.CommandID != command.CommandID || receipt.OrganizationID != command.OrganizationID || receipt.IdentityIssuer != command.IdentityIssuer || receipt.ActorSub != command.ActorSub || receipt.AssignmentID != assignment || receipt.DocumentID != command.DocumentID || receipt.ExpectedRevision != command.ExpectedRevision || receipt.Revision != command.ExpectedRevision+1 || receipt.Status != "completed" || receipt.EventID == uuid.Nil || receipt.CompletedAt.IsZero() || receipt.CompletedAt.Location() != time.UTC || (receipt.Outcome != "completed" && receipt.Outcome != "duplicate") {
		return nil, odoCommandError("ODO_INVALID_RECEIPT", 502)
	}
	return &receipt, nil
}
