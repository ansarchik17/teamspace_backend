package service

import (
	"testing"

	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestIsMinisterOrViceMinister(t *testing.T) {
	svc := NewPublicationService(nil, nil, zap.NewNop())

	managerRoleUser := &domain.User{
		Role: domain.RoleManager,
	}
	if !svc.IsMinisterOrViceMinister(managerRoleUser) {
		t.Error("expected true for RoleManager")
	}

	superAdminUser := &domain.User{
		Role: domain.RoleSuperAdmin,
	}
	if !svc.IsMinisterOrViceMinister(superAdminUser) {
		t.Error("expected true for RoleSuperAdmin")
	}

	regularUser := &domain.User{
		Role:     domain.RoleUser,
		Position: nil,
	}
	if svc.IsMinisterOrViceMinister(regularUser) {
		t.Error("expected false for regular user")
	}
}

func TestIsDepartmentLeader(t *testing.T) {
	svc := NewPublicationService(nil, nil, zap.NewNop())

	managerUser := &domain.User{
		Role: domain.RoleManager,
	}
	if !svc.IsDepartmentLeader(managerUser) {
		t.Error("expected true for RoleManager")
	}

	regularUser := &domain.User{
		Role: domain.RoleUser,
	}
	if svc.IsDepartmentLeader(regularUser) {
		t.Error("expected false for RoleUser")
	}
}
