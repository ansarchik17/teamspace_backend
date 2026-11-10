package domain

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestSecurityAuditFilters(t *testing.T) {
	require.True(t, (SecurityAuditFilter{Limit: 50, IP: "2001:db8::/32", Action: "admin.module.update"}).Valid())
	for _, f := range []SecurityAuditFilter{{Limit: 0}, {Limit: 10001}, {Limit: 50, IP: "not an IP"}, {Limit: 50, Action: "x' OR 1=1"}, {Limit: 50, Outcome: "unknown"}, {Limit: 50, BeforeID: -1}} {
		require.False(t, f.Valid())
	}
	now := time.Now()
	require.False(t, (SecurityAuditFilter{Limit: 50, From: &now, To: &now}).Valid())
}
