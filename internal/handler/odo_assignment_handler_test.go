package handler

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestODOServiceHandlerRejectsUnboundCompanyAndIssuer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	company := uuid.New()
	for _, test := range []struct {
		name, org, issuer string
		schema            int
		code              int
	}{
		{"foreign company", uuid.NewString(), "https://identity.example.test", 0, 403},
		{"issuer mismatch", company.String(), "https://other.example.test", 0, 403},
		{"incomplete v1", company.String(), "https://identity.example.test", 1, 422},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			h := &KanbanHandler{}
			router.POST("/sync", func(c *gin.Context) {
				c.Set(middleware.ODOAssignmentPrincipalKey, middleware.ODOAssignmentPrincipal{Issuer: "https://identity.example.test", Actors: map[uuid.UUID]int64{company: 1}})
			}, h.UpsertODOServiceAssignment)
			body := `{"assignment_id":"assignment","document_id":"document","author_sub":"author","assignee_sub":"assignee","source_system":"documentolog","organization_id":"` + test.org + `","identity_issuer":"` + test.issuer + `","schema_version":` + string(rune('0'+test.schema)) + `}`
			r := httptest.NewRequest(http.MethodPost, "/sync", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			require.Equal(t, test.code, w.Code)
		})
	}
}

func TestODOServiceHandlerRejectsInvalidIdempotencyKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body, err := os.ReadFile("../../docs/contracts/examples/odo-assignment-events.json")
	require.NoError(t, err)
	var examples []json.RawMessage
	require.NoError(t, json.Unmarshal(body, &examples))
	e, err := domain.DecodeODOAssignmentEvent(examples[0], "https://odo.example.test")
	require.NoError(t, err)
	for _, key := range []string{"", "odo-assignment:" + uuid.NewString(), e.EventID.String()} {
		router := gin.New()
		h := &KanbanHandler{}
		router.POST("/sync", func(c *gin.Context) {
			c.Set(middleware.ODOAssignmentPrincipalKey, middleware.ODOAssignmentPrincipal{Issuer: e.IdentityIssuer, Actors: map[uuid.UUID]int64{e.OrganizationID: 1}, SourceOrigin: "https://odo.example.test"})
		}, h.UpsertODOServiceAssignment)
		r := httptest.NewRequest(http.MethodPost, "/sync", strings.NewReader(string(examples[0])))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		require.Equal(t, 422, w.Code)
	}
}
