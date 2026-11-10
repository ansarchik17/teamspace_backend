package handler

import "context"

func (h *AuthHandler) LookupUserLanguage(ctx context.Context, userID int64) (string, error) {
	return h.authService.GetUserLanguage(ctx, userID)
}
