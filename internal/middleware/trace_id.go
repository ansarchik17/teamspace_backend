package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const TraceIDHeader = "X-Trace-ID"

// TraceID middleware — генерирует или прокидывает X-Trace-ID через весь pipeline.
// Позволяет коррелировать запросы между alemai (Python) и backend (Go).
//
// Логика:
//  1. Если запрос содержит X-Trace-ID / X-Request-ID — используем его.
//  2. Если нет — генерируем новый UUID.
//  3. Устанавливаем в gin context под ключом "trace_id".
//  4. Добавляем X-Trace-ID в response headers.
func TraceID() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID := c.GetHeader(TraceIDHeader)
		if traceID == "" {
			traceID = c.GetHeader("X-Request-ID")
		}
		if traceID == "" {
			traceID = uuid.New().String()
		}

		c.Set("trace_id", traceID)
		c.Header(TraceIDHeader, traceID)

		c.Next()
	}
}

// GetTraceID возвращает trace_id из gin context (устанавливается TraceID middleware).
func GetTraceID(c *gin.Context) string {
	if v, exists := c.Get("trace_id"); exists {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
