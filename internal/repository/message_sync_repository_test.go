package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMessageSyncRepository_buildConversationSyncQuery tests SQL query generation
func TestMessageSyncRepository_buildConversationSyncQuery(t *testing.T) {
	// This is a simple test to ensure the SQL query is well-formed
	repo := &MessageSyncRepository{}

	query := repo.buildConversationSyncQuery()

	assert.NotEmpty(t, query)
	assert.Contains(t, query, "SELECT")
	assert.Contains(t, query, "FROM messages m")
	assert.Contains(t, query, "sequence_number")
	assert.Contains(t, query, "ORDER BY m.sequence_number ASC")
	assert.Contains(t, query, "m.is_deleted = false")
	assert.Contains(t, query, "dm.message_id IS NULL")
	assert.Contains(t, query, "cp.left_at IS NULL")
	assert.Contains(t, query, "gm.left_at IS NULL")
}

// TestMessageSyncRepository_buildGlobalSyncQuery tests global sync SQL query
func TestMessageSyncRepository_buildGlobalSyncQuery(t *testing.T) {
	repo := &MessageSyncRepository{}

	query := repo.buildGlobalSyncQuery()

	assert.NotEmpty(t, query)
	assert.Contains(t, query, "SELECT")
	assert.Contains(t, query, "FROM messages m")
	assert.Contains(t, query, "sequence_number")
	assert.Contains(t, query, "conversation_participants")
	assert.Contains(t, query, "group_members")
	assert.Contains(t, query, "m.created_at >= cp.joined_at")
	assert.Contains(t, query, "m.created_at >= gm.joined_at")
	assert.Contains(t, query, "dm.message_id IS NULL")
}

// Integration tests would go here with actual database connection
// Example:
// func TestMessageSyncRepository_GetMissedMessages_Integration(t *testing.T) {
//     if testing.Short() {
//         t.Skip("Skipping integration test")
//     }
//     // Setup test database
//     // Insert test data
//     // Call GetMissedMessages
//     // Assert results
// }
