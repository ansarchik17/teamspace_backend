package agent

import "github.com/west6ide/workspace-backend/internal/domain"

type toolRegistry struct {
	tools        []Tool
	intentMap    map[domain.AgentIntentName]Tool
	actionMap    map[string]Tool
	capabilities []domain.AgentCapability
}

func NewToolRegistry(tools ...Tool) ToolRegistry {
	registry := &toolRegistry{
		tools:     tools,
		intentMap: make(map[domain.AgentIntentName]Tool),
		actionMap: make(map[string]Tool),
	}
	for _, tool := range tools {
		if provider, ok := tool.(interface {
			Intents() []domain.AgentIntentName
		}); ok {
			for _, intent := range provider.Intents() {
				registry.intentMap[intent] = tool
			}
		}
		if provider, ok := tool.(interface{ Actions() []string }); ok {
			for _, action := range provider.Actions() {
				registry.actionMap[action] = tool
			}
		}
		requiresConfirmation := false
		if provider, ok := tool.(interface{ RequiresConfirmation() bool }); ok {
			requiresConfirmation = provider.RequiresConfirmation()
		}
		registry.capabilities = append(registry.capabilities, domain.AgentCapability{
			Name:                 tool.Name(),
			Description:          tool.Description(),
			RequiresConfirmation: requiresConfirmation,
			SupportedInputTypes: []string{
				"text", "voice",
			},
		})
	}
	return registry
}

func (r *toolRegistry) FindByIntent(intent domain.AgentIntentName) Tool {
	return r.intentMap[intent]
}

func (r *toolRegistry) FindByAction(action string) Tool {
	return r.actionMap[action]
}

func (r *toolRegistry) Capabilities() []domain.AgentCapability {
	return append([]domain.AgentCapability(nil), r.capabilities...)
}
