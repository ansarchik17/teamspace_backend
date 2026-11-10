package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestODOCommandRejectsInjectedIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := &KanbanHandler{}
	router.POST("/assignments/:id/complete", h.CompleteODOAssignment)
	for _, body := range []string{`{"command_id":"` + uuid.NewString() + `","expected_revision":1,"result_comment":"done","actor_sub":"other"}`, `{"command_id":null,"expected_revision":1,"result_comment":"done"}`, `{"command_id":"` + uuid.NewString() + `","expected_revision":1,"expected_revision":2,"result_comment":"done"}`, `{"command_id":"` + uuid.NewString() + `","expected_revision":1,"result_comment":""}`} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("POST", "/assignments/"+uuid.NewString()+"/complete", strings.NewReader(body)))
		require.Equal(t, 400, response.Code)
	}
}
