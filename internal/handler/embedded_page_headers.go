package handler

import "github.com/gin-gonic/gin"

func allowEmbeddedAppCSP(c *gin.Context) {
	c.Header(
		"Content-Security-Policy",
		"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; font-src 'self' data:; connect-src 'self' https:;",
	)
}
