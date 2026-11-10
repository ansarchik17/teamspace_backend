package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestToolRegistry_CapabilitiesIncludeConfirmationMetadata(t *testing.T) {
	registry := NewToolRegistry(BuildDefaultTools(ToolDependencies{})...)

	capabilities := registry.Capabilities()
	var reminderCapability *domain.AgentCapability
	var tasksCapability *domain.AgentCapability
	var notesCapability *domain.AgentCapability
	for i := range capabilities {
		switch capabilities[i].Name {
		case "create_reminder":
			reminderCapability = &capabilities[i]
		case "tasks_overview":
			tasksCapability = &capabilities[i]
		case "notes_list":
			notesCapability = &capabilities[i]
		}
	}

	require.NotNil(t, reminderCapability)
	require.True(t, reminderCapability.RequiresConfirmation)
	require.NotNil(t, tasksCapability)
	require.False(t, tasksCapability.RequiresConfirmation)
	require.NotNil(t, notesCapability)
	require.False(t, notesCapability.RequiresConfirmation)
}
