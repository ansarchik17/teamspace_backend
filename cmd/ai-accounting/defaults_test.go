package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultsRequireExplicitTargetAndActor(t *testing.T) {
	t.Setenv("AI_OPS_DATABASE_URL", "")
	for _, args := range [][]string{
		{"defaults"},
		{"defaults", "--database-name", "workspace", "--organization", "invalid", "--actor-id", "1"},
		{"defaults", "--database-name", "workspace", "--organization", "10000000-0000-0000-0000-000000000001", "--actor-id", "0"},
		{"defaults", "--database-name", "workspace", "--organization", "10000000-0000-0000-0000-000000000001", "--actor-id", "1"},
	} {
		require.Error(t, run(args, &bytes.Buffer{}))
	}
}
