package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestODOServiceHandlerRejectsUserRolesWithoutServicePrincipal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, role := range []string{"admin", "registrar", "executor", "employee"} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/internal/documentolog/assignments/upsert", nil)
		ctx.Set("roles", []string{role})
		ctx.Set("user_id", int64(1))
		(&KanbanHandler{}).UpsertODOServiceAssignment(ctx)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("user role %s bypassed service principal: %d", role, recorder.Code)
		}
	}
}
