package service

import (
	"context"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type workspaceAIJobClaims struct {
	UserID         int64     `json:"uid"`
	OrganizationID uuid.UUID `json:"org"`
	JobID          string    `json:"job"`
	jwt.RegisteredClaims
}

// A durable job receives a narrow, expiring model-only capability. User access
// tokens and provider credentials are never persisted in the job table.
func (s *WorkspaceAIProvider) IssueJobGrant(ctx context.Context, userID int64, jobID string) (string, error) {
	if len(s.jobSigningKey) < 32 {
		return "", domain.ErrAIAccountingDisabled
	}
	if _, err := uuid.Parse(jobID); err != nil {
		return "", domain.ErrAIPaidCallInvalidRequest
	}
	ctx, err := aiPaidUserContext(s.paid, s.users, ctx, userID)
	if err != nil {
		return "", err
	}
	caller, _ := domain.AIPaidCallerFromContext(ctx)
	now := time.Now().UTC()
	claims := workspaceAIJobClaims{UserID: userID, OrganizationID: caller.OrganizationID, JobID: jobID,
		RegisteredClaims: jwt.RegisteredClaims{Issuer: "workspace-ai-jobs", Audience: jwt.ClaimStrings{"workspace-ai-model-worker"},
			ID: jobID, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(12 * time.Hour))}}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.jobSigningKey)
}

func (s *WorkspaceAIProvider) AuthenticateJobGrant(ctx context.Context, token, jobID string) (context.Context, int64, error) {
	if len(s.jobSigningKey) < 32 {
		return nil, 0, domain.ErrAIAccountingDisabled
	}
	var claims workspaceAIJobClaims
	parsed, err := jwt.ParseWithClaims(token, &claims, func(*jwt.Token) (any, error) { return s.jobSigningKey, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("workspace-ai-jobs"), jwt.WithAudience("workspace-ai-model-worker"), jwt.WithExpirationRequired())
	if err != nil || !parsed.Valid || claims.JobID != jobID || claims.ID != jobID {
		return nil, 0, domain.ErrAIPaidCallInvalidCaller
	}
	caller, err := domain.NewAIPaidUserCaller(claims.OrganizationID, claims.UserID)
	if err != nil {
		return nil, 0, err
	}
	// Complete/Audio recheck current membership against this pinned identity.
	return domain.WithAIPaidCaller(ctx, caller), claims.UserID, nil
}
