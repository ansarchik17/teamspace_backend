package auth

import (
	"encoding/base64"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"strings"
	"testing"
)

func TestInvitationSecretsAndTrustedOrigin(t *testing.T) {
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	hash, err := invitationTokenHash(token)
	require.NoError(t, err)
	require.Len(t, hash, 64)
	require.NotEqual(t, token, hash)
	a, err := invitationCodeHash(token, "012345")
	require.NoError(t, err)
	b, err := invitationCodeHash(strings.Repeat("B", 43), "012345")
	require.NoError(t, err)
	require.NotEqual(t, a, b)
	for _, invalid := range []string{"", "a", strings.Repeat("A", 42), strings.Repeat("!", 43)} {
		_, err = invitationTokenHash(invalid)
		require.Error(t, err)
	}
	for _, invalid := range []string{"12345", "1234567", "abcdef", "１２３４５６"} {
		_, err = invitationCodeHash(token, invalid)
		require.Error(t, err)
	}
	for _, input := range []string{"https://teamspace.kz/api?x=1#bad", "https://teamspace.kz/web/"} {
		s := NewInvitationService(nil, nil, nil, input, zap.NewNop())
		require.Equal(t, "https://teamspace.kz/web/join", s.joinURL)
	}
	for _, input := range []string{"//evil.example", "http://evil.example", "https://user:pass@teamspace.kz", "javascript:alert(1)", ""} {
		s := NewInvitationService(nil, nil, nil, input, zap.NewNop())
		require.Empty(t, s.joinURL)
	}
}
