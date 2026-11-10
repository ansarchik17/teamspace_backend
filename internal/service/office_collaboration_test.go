package service

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestOfficeCollaborationCapabilities(t *testing.T) {
	claims := OnlyOfficeTokenClaims{FileID: uuid.NewString(), UserID: 3, Mode: "view", Purpose: ooCollaborationPurpose, DocumentKey: "key", SessionID: uuid.NewString(), Exp: time.Now().Add(time.Hour).Unix()}
	token, err := signOnlyOfficeClaims(claims, "secret")
	require.NoError(t, err)
	parsed, err := ValidateOnlyOfficeCollaborationToken(token, "secret")
	require.NoError(t, err)
	require.Equal(t, claims.SessionID, parsed.SessionID)
	_, err = ValidateOnlyOfficeEditorToken(token, "secret")
	require.Error(t, err)
	_, err = ValidateOnlyOfficeToken(token, "secret")
	require.Error(t, err)
	_, err = ValidateOnlyOfficeCallbackToken(token, "secret")
	require.Error(t, err)
	_, err = ValidateOnlyOfficeCollaborationToken(token, "wrong")
	require.Error(t, err)
	s := &DriveService{}
	require.Error(t, s.UpdateOfficePresence(context.Background(), parsed, "heartbeat"))
	require.Error(t, s.UpdateOfficePresence(context.Background(), nil, "heartbeat"))
	claims.Exp = time.Now().Add(-time.Second).Unix()
	token, err = signOnlyOfficeClaims(claims, "secret")
	require.NoError(t, err)
	_, err = ValidateOnlyOfficeCollaborationToken(token, "secret")
	require.Error(t, err)
}

func TestOfficeAvatarAndUserIDs(t *testing.T) {
	require.Equal(t, "https://cdn.example.com/a.png", officeAvatar(domain.OfficeUser{Image: "https://cdn.example.com/a.png"}, "https://app.example.com"))
	require.Equal(t, "https://app.example.com/a.png", officeAvatar(domain.OfficeUser{Image: "/a.png"}, "https://app.example.com"))
	for _, unsafe := range []string{"javascript:alert(1)", "//evil.example.com/a", "https://user:pass@evil.test/a", ""} {
		result := officeAvatar(domain.OfficeUser{Name: "< &", Image: unsafe}, "https://app.example.com")
		require.True(t, strings.HasPrefix(result, "data:image/svg+xml;base64,"))
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(result, "data:image/svg+xml;base64,"))
		require.NoError(t, err)
		require.Contains(t, string(raw), "&lt;&amp;")
	}
	ids, err := parseOfficeUserIDs([]string{"3", "1", "3"})
	require.NoError(t, err)
	require.Equal(t, []int64{3, 1}, ids)
	for _, raw := range [][]string{{"0"}, {"-1"}, {"abc"}, make([]string, 101)} {
		_, err := parseOfficeUserIDs(raw)
		require.Error(t, err)
	}
}
