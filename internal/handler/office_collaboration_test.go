package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/service"
	"go.uber.org/zap"
)

func TestOfficeCollaborationRejectsEditorCapability(t *testing.T) {
	id := uuid.New()
	token, err := service.GenerateOnlyOfficeEditorToken(id, 1, "edit", "secret")
	require.NoError(t, err)
	h := &DriveHandler{jwtSecret: "secret", logger: zap.NewNop()}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/?file_id="+id.String()+"&token="+token, nil)
	h.OfficeCollaborationUsers(c)
	require.Equal(t, 401, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}

func TestOfficeCollaborationHTMLIntegration(t *testing.T) {
	page := buildOnlyOfficeHTML(&service.OnlyOfficeEditorConfig{UserName: "Ансар", UserImage: "https://example.com/a.png", Mode: "edit", CanEdit: true, CollaborationURL: "https://example.com/api?token=test"}, "https://example.com")
	for _, part := range []string{"onRequestUsers", "setUsers({c: 'info'", "sendPresence('heartbeat')", "sendPresence('leave')", "30000", `"coEditing":{"change":false,"mode":"fast"}`, `"image":"https://example.com/a.png"`} {
		require.Contains(t, page, part)
	}
}
