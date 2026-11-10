package service

import (
	"net/url"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestOfficeReviewIntegration(t *testing.T) {
	t.Setenv("ONLYOFFICE_JWT_SECRET", "office-secret")
	ctx, _, s := newDrivePostgresTest(t)
	for _, ext := range []string{"docx", "xlsx", "pptx"} {
		file := &domain.DriveFile{ID: uuid.New(), OwnerID: 1, RealName: "review." + ext, ObjectName: uuid.NewString(), ContentHash: uuid.NewString(), Size: 1, MimeType: "application/zip", Scope: "personal", Version: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
		require.NoError(t, s.repo.CreateFileWithInitialVersion(ctx, file, 1))
		require.NoError(t, s.GrantPermission(ctx, 1, domain.ResourceFile, file.ID, 3, domain.RoleViewer))
		for _, mode := range []string{"view", "edit", "comment", "review"} {
			t.Run(ext+"/"+mode, func(t *testing.T) {
				cfg, err := s.BuildOnlyOfficeConfig(ctx, 1, file.ID, mode, "secret", "https://api.example.com")
				if mode == "review" && ext != "docx" {
					require.Equal(t, 415, apperrors.ToAppError(err).StatusCode)
					return
				}
				require.NoError(t, err)
				require.Equal(t, mode, cfg.Mode)
				browser := cfg.BrowserConfig()
				permissions := browser["document"].(map[string]interface{})["permissions"].(map[string]interface{})
				editor := browser["editorConfig"].(map[string]interface{})
				require.Equal(t, mode == "edit", permissions["edit"])
				require.Equal(t, mode != "view", permissions["comment"])
				require.Equal(t, ext == "docx" && (mode == "edit" || mode == "review"), permissions["review"])
				require.Equal(t, mode == "edit", permissions["fillForms"])
				if mode == "view" {
					require.Equal(t, "view", editor["mode"])
					require.NotContains(t, editor, "callbackUrl")
				} else {
					require.Equal(t, "edit", editor["mode"])
					require.NotEmpty(t, editor["callbackUrl"])
					u, err := url.Parse(cfg.CallbackURL)
					require.NoError(t, err)
					claims, err := ValidateOnlyOfficeCallbackToken(u.Query().Get("token"), "secret")
					require.NoError(t, err)
					require.Equal(t, mode, claims.Mode)
					u, err = url.Parse(cfg.CollaborationURL)
					require.NoError(t, err)
					claims, err = ValidateOnlyOfficeCollaborationToken(u.Query().Get("token"), "secret")
					require.NoError(t, err)
					require.NoError(t, s.UpdateOfficePresence(ctx, claims, "heartbeat"))
					require.NoError(t, s.UpdateOfficePresence(ctx, claims, "leave"))
				}
				if mode == "review" {
					require.Equal(t, []string{}, permissions["reviewGroups"])
					review := editor["customization"].(map[string]interface{})["review"].(map[string]interface{})
					require.Equal(t, true, review["trackChanges"])
				}
				signed, err := jwt.Parse(cfg.Token, func(*jwt.Token) (interface{}, error) { return []byte("office-secret"), nil }, jwt.WithValidMethods([]string{"HS256"}))
				require.NoError(t, err)
				require.Equal(t, permissions["edit"], signed.Claims.(jwt.MapClaims)["document"].(map[string]interface{})["permissions"].(map[string]interface{})["edit"])
				token, err := GenerateOnlyOfficeEditorToken(file.ID, 1, cfg.Mode, "secret")
				require.NoError(t, err)
				claims, err := ValidateOnlyOfficeEditorToken(token, "secret")
				require.NoError(t, err)
				require.Equal(t, mode, claims.Mode)
			})
			viewer, err := s.BuildOnlyOfficeConfig(ctx, 3, file.ID, mode, "secret", "https://api.example.com")
			require.NoError(t, err)
			require.Equal(t, "view", viewer.Mode)
			require.False(t, viewer.canSave())
			_, err = s.BuildOnlyOfficeConfig(ctx, 5, file.ID, mode, "secret", "https://api.example.com")
			require.Equal(t, 403, apperrors.ToAppError(err).StatusCode)
		}
	}
}
