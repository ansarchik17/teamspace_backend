package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

func TestAdministrationErrorContractAcrossLanguages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   apperrors.ErrorCode
	}{
		{"forbidden", apperrors.Forbidden("private-diagnostic-token"), http.StatusForbidden, apperrors.ErrCodeForbidden},
		{"not found", domain.ErrOrganizationNotFound, http.StatusNotFound, apperrors.ErrCodeNotFound},
		{"revision conflict", apperrors.Conflict("private-diagnostic-token"), http.StatusConflict, apperrors.ErrCodeConflict},
		{"invalid request", apperrors.Validation("private-diagnostic-token"), http.StatusBadRequest, apperrors.ErrCodeValidation},
		{"database failure", errors.New("connection failed private-diagnostic-token"), http.StatusInternalServerError, apperrors.ErrCodeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			messages := map[string]string{}
			for _, lang := range []string{"ru", "kk"} {
				router := gin.New()
				router.Use(middleware.Localization(nil))
				router.GET("/administration", func(c *gin.Context) { (&OrganizationHandler{}).respondError(c, organizationAppError(tc.err)) })
				request := httptest.NewRequest(http.MethodGet, "/administration", nil)
				request.Header.Set("Accept-Language", lang)
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, request)
				require.Equal(t, tc.status, recorder.Code)
				require.Equal(t, lang, recorder.Header().Get("Content-Language"))
				var body struct {
					Error struct {
						Code        string `json:"code"`
						Message     string `json:"message"`
						UserMessage string `json:"user_message"`
					} `json:"error"`
				}
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
				require.Equal(t, string(tc.code), body.Error.Code)
				require.NotEmpty(t, body.Error.Message)
				require.NotEmpty(t, body.Error.UserMessage)
				require.NotContains(t, recorder.Body.String(), "private-diagnostic-token")
				messages[lang] = body.Error.UserMessage
			}
			require.NotEqual(t, messages["ru"], messages["kk"], "public messages must follow the requested language")
		})
	}
}
