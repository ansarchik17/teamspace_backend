package middleware

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/config"
)

const ODOAssignmentPrincipalKey = "odo_assignment_principal"

type ODOAssignmentPrincipal struct {
	Issuer, ClientID, Subject string
	Actors                    map[uuid.UUID]int64
	SourceOrigin              string
	BackfillEnabled           bool
}

type odoJWTParser interface {
	Parse(context.Context, string) (*jwt.Token, jwt.MapClaims, error)
}

// Each entry explicitly authorizes one company and its local automation actor.
// There is deliberately no wildcard, default company or actor taken from JSON.
func ParseODOAssignmentCompanies(raw string) (map[uuid.UUID]int64, error) {
	actors := map[uuid.UUID]int64{}
	for _, entry := range strings.Split(raw, ",") {
		parts := strings.Split(strings.TrimSpace(entry), ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid ODO company binding")
		}
		org, err := uuid.Parse(strings.TrimSpace(parts[0]))
		if err != nil || org == uuid.Nil {
			return nil, fmt.Errorf("invalid ODO company binding")
		}
		actor, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil || actor <= 0 {
			return nil, fmt.Errorf("invalid ODO company binding")
		}
		if _, exists := actors[org]; exists {
			return nil, fmt.Errorf("duplicate ODO company binding")
		}
		actors[org] = actor
	}
	return actors, nil
}

func (m *KeycloakAuthMiddleware) AuthenticateODOAssignments(cfg config.InternalConfig) gin.HandlerFunc {
	var parser odoJWTParser
	if m != nil && m.verifier != nil && cfg.ODOAssignmentsClientID != "" && cfg.ODOAssignmentsAudience != "" {
		if verifier := m.verifier.ForServiceClient(cfg.ODOAssignmentsAudience, cfg.ODOAssignmentsClientID); verifier != nil {
			parser = verifier
		}
	}
	return odoAssignmentAuthentication(parser, cfg)
}

func odoAssignmentAuthentication(parser odoJWTParser, cfg config.InternalConfig) gin.HandlerFunc {
	actors, configErr := ParseODOAssignmentCompanies(cfg.ODOAssignmentsCompanies)
	configured := cfg.ODOAssignmentsEnabled && configErr == nil && len(actors) > 0 && parser != nil &&
		strings.TrimSpace(cfg.ODOAssignmentsClientID) != "" && strings.TrimSpace(cfg.ODOAssignmentsSubject) != "" && strings.TrimSpace(cfg.ODOAssignmentsAudience) != ""
	return func(c *gin.Context) {
		if !configured {
			RespondAppError(c, apperrors.Forbidden("ODO assignment integration is not configured"))
			c.Abort()
			return
		}
		parts := strings.Fields(c.GetHeader("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			RespondAppError(c, apperrors.Unauthorized("service bearer token required"))
			c.Abort()
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()
		_, claims, err := parser.Parse(ctx, parts[1])
		if err != nil {
			RespondAppError(c, apperrors.TokenInvalid())
			c.Abort()
			return
		}
		expires, expErr := claims.GetExpirationTime()
		issuer, _ := claims.GetIssuer()
		subject, _ := claims.GetSubject()
		azp, _ := claims["azp"].(string)
		if expErr != nil || expires == nil || !expires.After(time.Now()) || issuer == "" || subject != cfg.ODOAssignmentsSubject || azp != cfg.ODOAssignmentsClientID {
			RespondAppError(c, apperrors.TokenInvalid())
			c.Abort()
			return
		}
		c.Set(ODOAssignmentPrincipalKey, ODOAssignmentPrincipal{Issuer: issuer, ClientID: azp, Subject: subject, Actors: actors, SourceOrigin: cfg.ODOAssignmentsSourceOrigin, BackfillEnabled: cfg.ODOAssignmentsBackfillEnabled})
		// No user lookup/upsert and no ordinary user_id/roles context are created.
		c.Next()
	}
}
