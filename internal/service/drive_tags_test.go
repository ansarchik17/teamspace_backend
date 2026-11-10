package service

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestNormalizeDriveTags(t *testing.T) {
	tags, err := normalizeDriveTags([]string{"  Отчёт  ", "ОТЧЁТ", "Finance"})
	require.NoError(t, err)
	require.Equal(t, []string{"Отчёт", "Finance"}, tags)
	tags, err = normalizeDriveTags(nil)
	require.NoError(t, err)
	require.NotNil(t, tags)
	for _, invalid := range [][]string{{" "}, {strings.Repeat("я", 65)}, {"a\x00b"}, {string([]byte{0xff})}, make([]string, 21)} {
		_, err := normalizeDriveTags(invalid)
		require.Error(t, err)
	}
}
