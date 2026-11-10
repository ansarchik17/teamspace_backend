package handler

// import (
// 	"net/http"
// 	"net/http/httptest"
// 	"testing"

// 	"github.com/gin-gonic/gin"
// 	"go.uber.org/zap"
// )

// func TestParseLimitOffset(t *testing.T) {
// 	gin.SetMode(gin.TestMode)

// 	tests := []struct {
// 		name         string
// 		rawQuery     string
// 		defaultLimit int
// 		maxLimit     int
// 		wantLimit    int
// 		wantOffset   int
// 	}{
// 		{
// 			name:         "uses defaults",
// 			rawQuery:     "",
// 			defaultLimit: 50,
// 			maxLimit:     200,
// 			wantLimit:    50,
// 			wantOffset:   0,
// 		},
// 		{
// 			name:         "uses provided values",
// 			rawQuery:     "limit=25&offset=10",
// 			defaultLimit: 50,
// 			maxLimit:     200,
// 			wantLimit:    25,
// 			wantOffset:   10,
// 		},
// 		{
// 			name:         "clamps invalid values",
// 			rawQuery:     "limit=-1&offset=-2",
// 			defaultLimit: 50,
// 			maxLimit:     200,
// 			wantLimit:    50,
// 			wantOffset:   0,
// 		},
// 		{
// 			name:         "clamps to max limit",
// 			rawQuery:     "limit=999&offset=5",
// 			defaultLimit: 50,
// 			maxLimit:     200,
// 			wantLimit:    200,
// 			wantOffset:   5,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			rec := httptest.NewRecorder()
// 			ctx, _ := gin.CreateTestContext(rec)
// 			req := httptest.NewRequest(http.MethodGet, "/?"+tt.rawQuery, nil)
// 			ctx.Request = req

// 			limit, offset := parseLimitOffset(ctx, tt.defaultLimit, tt.maxLimit)
// 			if limit != tt.wantLimit || offset != tt.wantOffset {
// 				t.Fatalf("parseLimitOffset() = (%d, %d), want (%d, %d)", limit, offset, tt.wantLimit, tt.wantOffset)
// 			}
// 		})
// 	}
// }

// func TestEmailLogHandlerHandleIncomingEmailInvalidBody(t *testing.T) {
// 	gin.SetMode(gin.TestMode)

// 	rec := httptest.NewRecorder()
// 	ctx, _ := gin.CreateTestContext(rec)
// 	req := httptest.NewRequest(http.MethodPost, "/users/notification", http.NoBody)
// 	ctx.Request = req

// 	h := NewEmailLogHandler(nil, zap.NewNop())
// 	h.HandleIncomingEmail(ctx)

// 	if rec.Code != http.StatusBadRequest {
// 		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
// 	}
// }
