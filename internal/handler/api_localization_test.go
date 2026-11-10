package handler

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/i18n"
	"github.com/west6ide/workspace-backend/internal/middleware"
	"go.uber.org/zap"
)

func TestAPIHandlerErrorsKeepCodeAcrossLocales(t *testing.T) {
	for _, locale := range []string{"ru", "kk"} {
		router := gin.New()
		router.Use(middleware.Localization(nil))
		router.Use(func(c *gin.Context) { c.Set("user_id", int64(7)); c.Next() })
		router.POST("/notes/:id/audio", NewNoteHandler(nil, zap.NewNop()).UploadAudio)
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/notes/invalid-uuid/audio", nil)
		req.Header.Set("Accept-Language", locale)
		router.ServeHTTP(recorder, req)
		if recorder.Code != 400 || strings.Contains(recorder.Body.String(), "invalid note id") {
			t.Fatalf("unexpected response: %s", recorder.Body.String())
		}
		var response struct {
			Error struct{ Code, Message string }
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Error.Code != "BAD_REQUEST" || response.Error.Message == "" {
			t.Fatalf("invalid contract: %+v", response)
		}
	}
}

func TestAPIResponseMessagesAndDiagnostics(t *testing.T) {
	router := gin.New()
	router.Use(middleware.Localization(nil))
	diagnostic := "livekit private provider response"
	original := []PersonalRecordingResponse{{Status: "failed", FailureReason: &diagnostic, FailedReason: &diagnostic, LiveKitErrorMessage: &diagnostic}}
	router.GET("/", func(c *gin.Context) {
		respondJSON(c, 200, gin.H{"message": apiMessage(c, "message deleted successfully"), "data": publicPayload(c, original)})
	})
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Language", "kk")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if !strings.Contains(recorder.Body.String(), "Хабарлама жойылды.") || strings.Contains(recorder.Body.String(), diagnostic) || strings.Contains(recorder.Body.String(), "livekit_error_message") {
		t.Fatalf("bad payload: %s", recorder.Body.String())
	}
	if *original[0].FailureReason != diagnostic {
		t.Fatal("stored diagnostics changed")
	}
	empty := []domain.Notification{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	encoded, err := json.Marshal(publicPayload(c, empty))
	if err != nil || string(encoded) != "[]" {
		t.Fatalf("empty list changed: %s %v", encoded, err)
	}
}

// Prevent new handlers from bypassing localization and diagnostic redaction.
func TestAPIHandlersUsePublicResponseBoundary(t *testing.T) {
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".go") || strings.HasSuffix(file.Name(), "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			receiver, ok := selector.X.(*ast.Ident)
			if ok && receiver.Name == "c" {
				switch selector.Sel.Name {
				case "JSON", "AbortWithStatusJSON", "PureJSON", "IndentedJSON", "SecureJSON":
					t.Errorf("%s bypasses public JSON response boundary", file.Name())
				}
			}
			return true
		})
	}
}

func TestEgovRedirectContainsOnlyLocalizedPublicError(t *testing.T) {
	original := apperrors.Internal("private SQLSTATE error").WithUserMessage("secret provider response")
	for _, locale := range []i18n.Locale{i18n.Russian, i18n.Kazakh} {
		target := buildFrontendEgovTarget("/web/auth/callback", "/web", original, locale)
		parsed, err := url.Parse(target)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(target, "secret") || strings.Contains(target, "SQLSTATE") || parsed.Query().Get("error") != "INTERNAL_ERROR" {
			t.Fatalf("unsafe redirect: %s", target)
		}
		if parsed.Query().Get("message") != apperrors.LocalizedMessage(apperrors.ErrCodeInternal, 500, locale) {
			t.Fatalf("wrong redirect locale: %s", target)
		}
	}
}

func TestProxyErrorsUsePublicBoundary(t *testing.T) {
	for _, payload := range []string{`{"code":"UPSTREAM_FAILURE","message":"private provider response","debug":"secret"}`, strings.Repeat("private diagnostic", 10000)} {
		router := gin.New()
		router.Use(middleware.Localization(nil))
		router.GET("/", func(c *gin.Context) { respondUpstreamError(c, 502, strings.NewReader(payload)) })
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest("GET", "/", nil))
		if recorder.Code != 502 || strings.Contains(recorder.Body.String(), "private") || strings.Contains(recorder.Body.String(), "secret") {
			t.Fatalf("unsafe proxy response: %s", recorder.Body.String())
		}
	}
}
