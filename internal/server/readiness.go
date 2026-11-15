package server

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

const readinessTimeout = 2 * time.Second

// readyCheck probes the running API's database pool on every request. Connection
// errors stay private, and the result must not be cached by monitoring proxies.
func readyCheck(pingDatabase func(context.Context) error) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		ctx, cancel := context.WithTimeout(c.Request.Context(), readinessTimeout)
		defer cancel()
		if pingDatabase == nil || pingDatabase(ctx) != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status":  "not_ready",
				"service": "messenger-api",
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status":  "ready",
			"service": "messenger-api",
		})
	}
}
