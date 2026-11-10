package agent

import (
	"context"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/service"
	"testing"
)

type tenantSearchRepo struct {
	service.UserRepository
	actor int64
}

func (r *tenantSearchRepo) SearchVisibleTo(_ context.Context, actor int64, _ string, _, _ int) ([]*domain.User, error) {
	r.actor = actor
	return []*domain.User{{ID: actor, FirstName: "OWN_CANARY"}}, nil
}
func TestBAS022AgentSearchUsesAuthenticatedActor(t *testing.T) {
	repo := &tenantSearchRepo{}
	for _, tool := range BuildDefaultTools(ToolDependencies{UserRepo: repo}) {
		if tool.Name() != "search" {
			continue
		}
		result, err := tool.Execute(context.Background(), ToolRequest{User: domain.AgentUserContext{UserID: 11}, Request: &domain.AgentChatRequest{Message: "canary"}, Entities: map[string]interface{}{"user_id": 21, "organization_id": "foreign"}})
		if err != nil || result == nil || repo.actor != 11 {
			t.Fatalf("identity changed: actor=%d result=%v error=%v", repo.actor, result, err)
		}
		return
	}
	t.Fatal("search tool missing")
}
