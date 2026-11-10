package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewAssigneeIDs(t *testing.T) {
	got := newAssigneeIDs([]int64{1, 2}, []int64{2, 3, 3, 4})

	assert.Equal(t, []int64{3, 4}, got)
}

func TestUniqueRecipientIDs(t *testing.T) {
	got := uniqueRecipientIDs([]int64{0, 1, 2, 2, 3}, 1)

	assert.Equal(t, []int64{2, 3}, got)
}

func TestNormalizeAssigneeIDs(t *testing.T) {
	legacyID := int64(3)

	got := normalizeAssigneeIDs([]int64{1, 2, 2}, &legacyID)

	assert.Equal(t, []int64{1, 2, 3}, got)
}
