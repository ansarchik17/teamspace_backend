package domain

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMaximumCompanyBudget(t *testing.T) {
	c := StorageCapacity{TotalBytes: 1000, FreeBytes: 800, ReserveBytes: 100}
	require.Equal(t, int64(400), MaximumCompanyBudget(c, 100, 500, 400))
	c.FreeBytes = 300
	require.Equal(t, int64(0), MaximumCompanyBudget(c, 100, 500, 400))
	c.FreeBytes = 1000
	require.Equal(t, int64(400), MaximumCompanyBudget(c, 100, 500, 400))
}
