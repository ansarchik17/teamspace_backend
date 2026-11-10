package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestSecurityCSVPreventsFormulaInjection(t *testing.T) {
	for _, v := range []string{"=1+1", " +CMD", "-1", "@formula", "\t=1", "first\n=1"} {
		require.Equal(t, "'"+v, securityCSVCell(v))
	}
	require.Equal(t, "admin.module.update", securityCSVCell("admin.module.update"))
}
func TestSecurityAuditQueryValidation(t *testing.T) {
	for _, q := range []string{"limit=101", "before_id=-1", "ip=invalid", "from=yesterday", "actor_id=0", "snapshot_id=-1", "outcome=unknown"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/audit?"+q, nil)
		_, err := securityAuditFilter(c, false)
		require.Error(t, err, q)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/audit?ip=192.0.2.0%2F24&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z", nil)
	f, err := securityAuditFilter(c, false)
	require.NoError(t, err)
	require.Equal(t, 50, f.Limit)
}
