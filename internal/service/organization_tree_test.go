package service

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func TestBuildOrganizationTree(t *testing.T) {
	root, child, leaf, other := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	nodes := []*domain.OrganizationTreeNode{{ID: root, EmployeeCount: 1}, {ID: other, EmployeeCount: 4}, {ID: child, ParentUnitID: &root, EmployeeCount: 2}, {ID: leaf, ParentUnitID: &child, EmployeeCount: 3}}
	tree, err := buildOrganizationTree(nodes)
	require.NoError(t, err)
	require.Len(t, tree, 2)
	require.Equal(t, root, tree[0].ID)
	require.Equal(t, 6, tree[0].SubtreeEmployeeCount)
	require.Equal(t, 5, tree[0].Children[0].SubtreeEmployeeCount)
	require.Empty(t, tree[1].Children)
	_, err = buildOrganizationTree([]*domain.OrganizationTreeNode{{ID: root, ParentUnitID: &child}, {ID: child, ParentUnitID: &root}})
	require.Error(t, err)
	_, err = buildOrganizationTree([]*domain.OrganizationTreeNode{{ID: root, ParentUnitID: &other}})
	require.Error(t, err)
	tree, err = buildOrganizationTree(nil)
	require.NoError(t, err)
	require.NotNil(t, tree)
	require.Empty(t, tree)
}
