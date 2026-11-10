package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (s *OrganizationService) OrganizationTree(ctx context.Context, actor int64) (*domain.OrganizationTree, error) {
	result, nodes, err := s.repo.OrganizationTree(ctx, actor)
	if err != nil {
		return nil, err
	}
	result.Items, err = buildOrganizationTree(nodes)
	if err != nil {
		return nil, err
	}
	for _, node := range nodes {
		if node.Manager != nil {
			contact := domain.Contact{AvatarURL: node.Manager.AvatarURL}
			s.refreshContactAvatar(&contact)
			node.Manager.AvatarURL = contact.AvatarURL
		}
	}
	return result, nil
}

// buildOrganizationTree preserves the query's sibling order, checks every node,
// and aggregates counts bottom-up without recursively walking untrusted data.
func buildOrganizationTree(nodes []*domain.OrganizationTreeNode) ([]*domain.OrganizationTreeNode, error) {
	byID := make(map[uuid.UUID]*domain.OrganizationTreeNode, len(nodes))
	for _, node := range nodes {
		byID[node.ID] = node
		node.Children = []*domain.OrganizationTreeNode{}
		node.SubtreeEmployeeCount = node.EmployeeCount
	}
	roots := []*domain.OrganizationTreeNode{}
	for _, node := range nodes {
		if node.ParentUnitID == nil {
			roots = append(roots, node)
			continue
		}
		parent := byID[*node.ParentUnitID]
		if parent == nil {
			return nil, apperrors.Conflict("organization tree contains a missing or unavailable parent")
		}
		parent.Children = append(parent.Children, node)
	}
	// Kahn's algorithm from leaves; disconnected cycles cannot disappear from the response.
	pending := map[uuid.UUID]int{}
	queue := []*domain.OrganizationTreeNode{}
	for _, node := range nodes {
		pending[node.ID] = len(node.Children)
		if len(node.Children) == 0 {
			queue = append(queue, node)
		}
	}
	for i := 0; i < len(queue); i++ {
		node := queue[i]
		if node.ParentUnitID != nil {
			parent := byID[*node.ParentUnitID]
			parent.SubtreeEmployeeCount += node.SubtreeEmployeeCount
			pending[parent.ID]--
			if pending[parent.ID] == 0 {
				queue = append(queue, parent)
			}
		}
	}
	if len(queue) != len(nodes) {
		return nil, apperrors.Conflict("organization tree contains a cycle")
	}
	return roots, nil
}
