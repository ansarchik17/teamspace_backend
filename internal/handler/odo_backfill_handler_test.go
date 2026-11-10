package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestODOBackfillGateAndEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name    string
		enabled bool
		body    string
		code    int
	}{{"disabled", false, `{}`, 403}, {"extra field", true, `{"operation_id":null,"task_id":null,"expected_task_hash":"","reason":"x","event":{},"extra":true}`, 400}, {"duplicate key", true, `{"event":{},"event":{}}`, 400}, {"invalid event", true, `{"operation_id":null,"task_id":null,"expected_task_hash":"","reason":"x","event":{}}`, 422}} {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			h := &KanbanHandler{}
			router.POST("/backfill", func(c *gin.Context) {
				c.Set(middleware.ODOAssignmentPrincipalKey, middleware.ODOAssignmentPrincipal{BackfillEnabled: test.enabled, Actors: map[uuid.UUID]int64{uuid.New(): 1}})
			}, h.BackfillODOAssignment)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/backfill", strings.NewReader(test.body)))
			require.Equal(t, test.code, response.Code)
		})
	}
}
