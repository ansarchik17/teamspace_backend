package handler

import (
	"fmt"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/render"
	"github.com/golang-jwt/jwt/v5"
)

// DocumentServer signs its callback body either directly or inside payload.
// The URL capability identifies the user; this JWT authenticates server data.
func validateOnlyOfficeCallbackJWT(c *gin.Context, req onlyOfficeCallbackReq) error {
	secret := os.Getenv("ONLYOFFICE_JWT_SECRET")
	if secret == "" {
		return nil
	}
	token := req.Token
	if header := c.GetHeader("Authorization"); strings.HasPrefix(header, "Bearer ") {
		token = strings.TrimPrefix(header, "Bearer ")
	}
	parsed, err := jwt.Parse(token, func(token *jwt.Token) (interface{}, error) { return []byte(secret), nil }, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return err
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok || !parsed.Valid {
		return fmt.Errorf("invalid callback JWT")
	}
	payload := map[string]interface{}(claims)
	if nested, ok := payload["payload"].(map[string]interface{}); ok {
		payload = nested
	}
	status, _ := payload["status"].(float64)
	key, _ := payload["key"].(string)
	url, _ := payload["url"].(string)
	fileType, _ := payload["filetype"].(string)
	if status != float64(req.Status) || key != req.Key || url != req.URL || fileType != req.FileType {
		return fmt.Errorf("callback JWT payload mismatch")
	}
	return nil
}

// DocumentServer protocol reply, not a public API body: "error" must stay the integer 1.
func respondOnlyOfficeError(c *gin.Context, err *apperrors.AppError) {
	c.Render(err.StatusCode, render.JSON{Data: gin.H{"error": 1, "message": err.Message}})
}
