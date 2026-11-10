package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func int64Ptr(v int64) *int64 {
	return &v
}

func TestNWS004_PublicationVisibilityByAudience(t *testing.T) {
	now := time.Now()
	authorID := int64(10)
	deptA := int64(101)
	deptB := int64(102)
	deptC := int64(103)
	ministryX := int64(201)
	ministryY := int64(202)

	userDeptA := &domain.User{ID: 1, Role: domain.RoleUser, DepartmentID: &deptA, MinistryID: &ministryX}
	userDeptB := &domain.User{ID: 2, Role: domain.RoleUser, DepartmentID: &deptB, MinistryID: &ministryX}
	userDeptC := &domain.User{ID: 3, Role: domain.RoleUser, DepartmentID: &deptC, MinistryID: &ministryY}
	userNoDept := &domain.User{ID: 4, Role: domain.RoleUser, DepartmentID: nil, MinistryID: &ministryX}
	userAuthor := &domain.User{ID: authorID, Role: domain.RoleUser, DepartmentID: &deptB, MinistryID: &ministryX}
	userSuperAdmin := &domain.User{ID: 99, Role: domain.RoleSuperAdmin, DepartmentID: nil, MinistryID: nil}

	t.Run("Audience: all is visible to everyone", func(t *testing.T) {
		pub := &domain.Publication{
			ID:             uuid.New(),
			AuthorID:       authorID,
			Status:         domain.PublicationStatusPublished,
			TargetAudience: domain.PublicationAudienceAll,
			PublishedAt:    now.Add(-time.Hour),
		}

		require.True(t, publicationVisible(pub, userDeptA, now))
		require.True(t, publicationVisible(pub, userDeptB, now))
		require.True(t, publicationVisible(pub, userDeptC, now))
		require.True(t, publicationVisible(pub, userNoDept, now))
		require.True(t, publicationVisible(pub, userAuthor, now))
		require.True(t, publicationVisible(pub, userSuperAdmin, now))
	})

	t.Run("Audience: department with scalar target_department_id", func(t *testing.T) {
		pubDeptA := &domain.Publication{
			ID:                 uuid.New(),
			AuthorID:           authorID,
			Status:             domain.PublicationStatusPublished,
			TargetAudience:     domain.PublicationAudienceDepartment,
			TargetDepartmentID: &deptA,
			PublishedAt:        now.Add(-time.Hour),
		}

		// Member of targeted department can see
		require.True(t, publicationVisible(pubDeptA, userDeptA, now))

		// Other department members cannot see
		require.False(t, publicationVisible(pubDeptA, userDeptB, now))
		require.False(t, publicationVisible(pubDeptA, userDeptC, now))
		require.False(t, publicationVisible(pubDeptA, userNoDept, now))

		// Author and SuperAdmin can always see
		require.True(t, publicationVisible(pubDeptA, userAuthor, now))
		require.True(t, publicationVisible(pubDeptA, userSuperAdmin, now))
	})

	t.Run("Audience: department with multi-target target_department_ids array", func(t *testing.T) {
		pubMultiDept := &domain.Publication{
			ID:                  uuid.New(),
			AuthorID:            authorID,
			Status:              domain.PublicationStatusPublished,
			TargetAudience:      domain.PublicationAudienceDepartment,
			TargetDepartmentIDs: []int64{deptA, deptC},
			PublishedAt:         now.Add(-time.Hour),
		}

		// Members of dept A and dept C can see
		require.True(t, publicationVisible(pubMultiDept, userDeptA, now))
		require.True(t, publicationVisible(pubMultiDept, userDeptC, now))

		// Dept B is not targeted, so cannot see
		require.False(t, publicationVisible(pubMultiDept, userDeptB, now))
		require.False(t, publicationVisible(pubMultiDept, userNoDept, now))

		// Author and SuperAdmin can always see
		require.True(t, publicationVisible(pubMultiDept, userAuthor, now))
		require.True(t, publicationVisible(pubMultiDept, userSuperAdmin, now))
	})

	t.Run("Audience: branch/ministry with scalar target_ministry_id", func(t *testing.T) {
		pubBranchX := &domain.Publication{
			ID:               uuid.New(),
			AuthorID:         authorID,
			Status:           domain.PublicationStatusPublished,
			TargetAudience:   domain.PublicationAudienceBranch,
			TargetMinistryID: &ministryX,
			PublishedAt:      now.Add(-time.Hour),
		}

		// Users in ministry/branch X can see
		require.True(t, publicationVisible(pubBranchX, userDeptA, now))
		require.True(t, publicationVisible(pubBranchX, userDeptB, now))
		require.True(t, publicationVisible(pubBranchX, userNoDept, now))

		// User in ministry Y cannot see
		require.False(t, publicationVisible(pubBranchX, userDeptC, now))

		// Author and SuperAdmin can always see
		require.True(t, publicationVisible(pubBranchX, userAuthor, now))
		require.True(t, publicationVisible(pubBranchX, userSuperAdmin, now))
	})

	t.Run("Audience: ministry with multi-target target_ministry_ids array", func(t *testing.T) {
		pubMultiBranch := &domain.Publication{
			ID:                uuid.New(),
			AuthorID:          authorID,
			Status:            domain.PublicationStatusPublished,
			TargetAudience:    domain.PublicationAudienceMinistry,
			TargetMinistryIDs: []int64{ministryY},
			PublishedAt:       now.Add(-time.Hour),
		}

		// User in ministry Y can see
		require.True(t, publicationVisible(pubMultiBranch, userDeptC, now))

		// Users in ministry X cannot see
		require.False(t, publicationVisible(pubMultiBranch, userDeptA, now))
		require.False(t, publicationVisible(pubMultiBranch, userDeptB, now))

		// Author and SuperAdmin can always see
		require.True(t, publicationVisible(pubMultiBranch, userAuthor, now))
		require.True(t, publicationVisible(pubMultiBranch, userSuperAdmin, now))
	})
}

func TestNWS004_JSONSerialization(t *testing.T) {
	t.Run("CreatePublicationRequest with department targeting", func(t *testing.T) {
		deptID := int64(15)
		deptIDs := []int64{15, 16, 17}
		req := domain.CreatePublicationRequest{
			SourceType:          "department",
			Type:                "news",
			Title:               "Новость отдела",
			Content:             "Текст для конкретных отделов",
			TargetAudience:      domain.PublicationAudienceDepartment,
			TargetDepartmentID:  &deptID,
			TargetDepartmentIDs: deptIDs,
			Importance:          domain.PublicationImportanceNormal,
		}

		data, err := json.Marshal(req)
		require.NoError(t, err)

		var parsed domain.CreatePublicationRequest
		require.NoError(t, json.Unmarshal(data, &parsed))
		require.Equal(t, domain.PublicationAudienceDepartment, parsed.TargetAudience)
		require.NotNil(t, parsed.TargetDepartmentID)
		require.Equal(t, deptID, *parsed.TargetDepartmentID)
		require.Equal(t, deptIDs, parsed.TargetDepartmentIDs)
	})

	t.Run("CreatePublicationRequest with branch targeting", func(t *testing.T) {
		ministryIDs := []int64{101, 102}
		req := domain.CreatePublicationRequest{
			SourceType:        "ministry",
			Type:              "announcement",
			Title:             "Объявление для филиалов",
			Content:           "Текст для филиалов",
			TargetAudience:    domain.PublicationAudienceBranch,
			TargetMinistryIDs: ministryIDs,
			Importance:        domain.PublicationImportanceImportant,
		}

		data, err := json.Marshal(req)
		require.NoError(t, err)

		var parsed domain.CreatePublicationRequest
		require.NoError(t, json.Unmarshal(data, &parsed))
		require.Equal(t, domain.PublicationAudienceBranch, parsed.TargetAudience)
		require.Equal(t, ministryIDs, parsed.TargetMinistryIDs)
	})

	t.Run("PublicationResponse includes department and branch target fields", func(t *testing.T) {
		deptID := int64(25)
		deptIDs := []int64{25, 26}
		resp := domain.PublicationResponse{
			ID:                  uuid.New(),
			SourceType:          "department",
			Type:                "news",
			Title:               "Отделочная новость",
			TargetAudience:      domain.PublicationAudienceDepartment,
			TargetDepartmentID:  &deptID,
			TargetDepartmentIDs: deptIDs,
			Importance:          domain.PublicationImportanceNormal,
		}

		data, err := json.Marshal(resp)
		require.NoError(t, err)

		var parsed domain.PublicationResponse
		require.NoError(t, json.Unmarshal(data, &parsed))
		require.Equal(t, domain.PublicationAudienceDepartment, parsed.TargetAudience)
		require.NotNil(t, parsed.TargetDepartmentID)
		require.Equal(t, deptID, *parsed.TargetDepartmentID)
		require.Equal(t, deptIDs, parsed.TargetDepartmentIDs)
	})
}
