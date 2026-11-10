package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestOrganizationParentAllowsExistingAncestorAndRejectsDescendants(t *testing.T) {
	_, orgs, db, company, other := companyOwnerServiceFixture(t)
	ctx := context.Background()
	root, child, grandchild, sibling, foreign := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, item := range []struct {
		id, company uuid.UUID
		parent      *uuid.UUID
	}{
		{root, company, nil}, {child, company, &root}, {grandchild, company, &child},
		{sibling, company, &root}, {foreign, other, nil},
	} {
		_, err := db.Exec(ctx, `INSERT INTO teams(id, organization_id, parent_team_id, name) VALUES($1,$2,$3,'Test')`, item.id, item.company, item.parent)
		require.NoError(t, err)
	}
	for _, item := range []struct {
		name         string
		self, parent uuid.UUID
		allowed      bool
	}{
		{"keep existing parent while editing manager", child, root, true},
		{"move to ancestor", grandchild, root, true},
		{"move to sibling", grandchild, sibling, true},
		{"self cycle", child, child, false},
		{"direct descendant cycle", root, child, false},
		{"deep descendant cycle", root, grandchild, false},
		{"foreign parent", child, foreign, false},
		{"missing parent", child, uuid.New(), false},
	} {
		t.Run(item.name, func(t *testing.T) {
			err := orgs.ensureParent(ctx, company, &item.parent, &item.self)
			if item.allowed {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
	require.NoError(t, orgs.ensureParent(ctx, company, &root, nil), "new units may have an existing parent")
}
