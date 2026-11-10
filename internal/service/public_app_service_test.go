package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

type stubPublicAppRepo struct {
	createFn       func(ctx context.Context, app *domain.PublicApp) error
	findAllFn      func(ctx context.Context, userID int64, nameQuery string, downloaded *bool, sort string) ([]domain.PublicApp, error)
	getByIDFn      func(ctx context.Context, id int64, userID int64) (*domain.PublicApp, error)
	updateFn       func(ctx context.Context, app *domain.PublicApp) error
	deleteFn       func(ctx context.Context, id int64) error
	addFavoriteFn  func(ctx context.Context, userID, appID int64) error
	removeFavFn    func(ctx context.Context, userID, appID int64) error
	getFavoritesFn func(ctx context.Context, userID int64) ([]domain.PublicApp, error)
	reorderFavFn   func(ctx context.Context, userID int64, appIDs []int64) error
	grantAccessFn  func(ctx context.Context, appID int64, userID int64) error
	revokeAccessFn func(ctx context.Context, appID int64, userID int64) error
	upsertRatingFn func(ctx context.Context, userID, appID int64, rating int) error
	removeRatingFn func(ctx context.Context, userID, appID int64) error
}

func (s *stubPublicAppRepo) Create(ctx context.Context, app *domain.PublicApp) error {
	if s.createFn != nil {
		return s.createFn(ctx, app)
	}
	return nil
}

func (s *stubPublicAppRepo) FindAll(ctx context.Context, userID int64, nameQuery string, downloaded *bool, sort string) ([]domain.PublicApp, error) {
	if s.findAllFn != nil {
		return s.findAllFn(ctx, userID, nameQuery, downloaded, sort)
	}
	return nil, nil
}

func (s *stubPublicAppRepo) GetByID(ctx context.Context, id int64, userID int64) (*domain.PublicApp, error) {
	if s.getByIDFn != nil {
		return s.getByIDFn(ctx, id, userID)
	}
	return nil, nil
}

func (s *stubPublicAppRepo) Update(ctx context.Context, app *domain.PublicApp) error {
	if s.updateFn != nil {
		return s.updateFn(ctx, app)
	}
	return nil
}

func (s *stubPublicAppRepo) Delete(ctx context.Context, id int64) error {
	if s.deleteFn != nil {
		return s.deleteFn(ctx, id)
	}
	return nil
}

func (s *stubPublicAppRepo) AddFavorite(ctx context.Context, userID, appID int64) error {
	if s.addFavoriteFn != nil {
		return s.addFavoriteFn(ctx, userID, appID)
	}
	return nil
}

func (s *stubPublicAppRepo) RemoveFavorite(ctx context.Context, userID, appID int64) error {
	if s.removeFavFn != nil {
		return s.removeFavFn(ctx, userID, appID)
	}
	return nil
}

func (s *stubPublicAppRepo) GetFavorites(ctx context.Context, userID int64) ([]domain.PublicApp, error) {
	if s.getFavoritesFn != nil {
		return s.getFavoritesFn(ctx, userID)
	}
	return nil, nil
}

func (s *stubPublicAppRepo) ReorderFavorites(ctx context.Context, userID int64, appIDs []int64) error {
	if s.reorderFavFn != nil {
		return s.reorderFavFn(ctx, userID, appIDs)
	}
	return nil
}

func (s *stubPublicAppRepo) GrantAccess(ctx context.Context, appID int64, userID int64) error {
	if s.grantAccessFn != nil {
		return s.grantAccessFn(ctx, appID, userID)
	}
	return nil
}

func (s *stubPublicAppRepo) RevokeAccess(ctx context.Context, appID int64, userID int64) error {
	if s.revokeAccessFn != nil {
		return s.revokeAccessFn(ctx, appID, userID)
	}
	return nil
}

func (s *stubPublicAppRepo) UpsertRating(ctx context.Context, userID, appID int64, rating int) error {
	if s.upsertRatingFn != nil {
		return s.upsertRatingFn(ctx, userID, appID, rating)
	}
	return nil
}

func (s *stubPublicAppRepo) RemoveRating(ctx context.Context, userID, appID int64) error {
	if s.removeRatingFn != nil {
		return s.removeRatingFn(ctx, userID, appID)
	}
	return nil
}

func TestNormalizePublicAppTags(t *testing.T) {
	tags := normalizePublicAppTags([]string{"  AI  ", "Demo", "", "ai", "  ", "Документооборот"})
	require.Equal(t, []string{"AI", "Demo", "Документооборот"}, tags)
}

func TestNormalizePublicAppTags_EmptyInputReturnsEmptySlice(t *testing.T) {
	tags := normalizePublicAppTags(nil)
	require.NotNil(t, tags)
	assert.Empty(t, tags)
}

func TestPublicAppServiceCreateNormalizesTags(t *testing.T) {
	var createdApp *domain.PublicApp
	repo := &stubPublicAppRepo{
		createFn: func(_ context.Context, app *domain.PublicApp) error {
			clonedTags := append([]string(nil), app.Tags...)
			createdApp = &domain.PublicApp{Tags: clonedTags}
			app.ID = 55
			return nil
		},
	}

	svc := NewPublicAppService(repo, nil, zap.NewNop())
	app, err := svc.Create(context.Background(), "AlemGPT", "https://example.com", "icon.png", true, 0, false, false, []string{" AI ", "Demo", "ai", ""}, "", nil)
	require.NoError(t, err)
	require.NotNil(t, createdApp)
	assert.Equal(t, []string{"AI", "Demo"}, createdApp.Tags)
	assert.Equal(t, int64(55), app.ID)
	assert.Equal(t, PublicAppCategoryArtificialIntelligence, app.Category)
}

func TestPublicAppServiceCreateStoresExplicitCategory(t *testing.T) {
	var createdApp *domain.PublicApp
	repo := &stubPublicAppRepo{
		createFn: func(_ context.Context, app *domain.PublicApp) error {
			copied := *app
			createdApp = &copied
			app.ID = 56
			return nil
		},
	}

	svc := NewPublicAppService(repo, nil, zap.NewNop())
	app, err := svc.Create(context.Background(), "Docs", "https://example.com", "icon.png", true, 0, false, false, nil, "Документация", nil)
	require.NoError(t, err)
	require.NotNil(t, createdApp)
	assert.Equal(t, PublicAppCategoryDocumentManagement, createdApp.Category)
	assert.Equal(t, PublicAppCategoryDocumentManagement, app.Category)
}

func TestPublicAppServiceUpsertRatingRejectsOutOfRangeValue(t *testing.T) {
	svc := NewPublicAppService(&stubPublicAppRepo{}, nil, zap.NewNop())

	app, err := svc.UpsertRating(context.Background(), 10, 99, 6)
	require.ErrorIs(t, err, ErrInvalidPublicAppRating)
	assert.Nil(t, app)
}

func TestPublicAppServiceUpsertRatingReturnsUpdatedApp(t *testing.T) {
	var (
		getByIDCalls int
		gotUserID    int64
		gotAppID     int64
		gotRating    int
	)

	repo := &stubPublicAppRepo{
		getByIDFn: func(_ context.Context, id int64, userID int64) (*domain.PublicApp, error) {
			getByIDCalls++
			if getByIDCalls == 1 {
				return &domain.PublicApp{ID: id, AppName: "AlemGPT"}, nil
			}

			myRating := 5
			return &domain.PublicApp{
				ID:            id,
				AppName:       "AlemGPT",
				AverageRating: 4.67,
				RatingCount:   3,
				MyRating:      &myRating,
			}, nil
		},
		upsertRatingFn: func(_ context.Context, userID, appID int64, rating int) error {
			gotUserID = userID
			gotAppID = appID
			gotRating = rating
			return nil
		},
	}

	svc := NewPublicAppService(repo, nil, zap.NewNop())
	app, err := svc.UpsertRating(context.Background(), 17, 42, 5)
	require.NoError(t, err)
	require.NotNil(t, app)
	assert.Equal(t, int64(17), gotUserID)
	assert.Equal(t, int64(42), gotAppID)
	assert.Equal(t, 5, gotRating)
	assert.Equal(t, 2, getByIDCalls)
	require.NotNil(t, app.MyRating)
	assert.Equal(t, 5, *app.MyRating)
	assert.Equal(t, 4.67, app.AverageRating)
	assert.Equal(t, 3, app.RatingCount)
}

func TestPublicAppServiceGetAllDefaultsToPositionSort(t *testing.T) {
	var capturedSort string

	repo := &stubPublicAppRepo{
		findAllFn: func(_ context.Context, userID int64, nameQuery string, downloaded *bool, sort string) ([]domain.PublicApp, error) {
			capturedSort = sort
			return []domain.PublicApp{}, nil
		},
	}

	svc := NewPublicAppService(repo, nil, zap.NewNop())
	_, err := svc.GetAll(context.Background(), 17, "", nil, "", "")
	require.NoError(t, err)
	assert.Equal(t, "position", capturedSort)
}

func TestPublicAppServiceGetAllSupportsRatingSort(t *testing.T) {
	var capturedSort string

	repo := &stubPublicAppRepo{
		findAllFn: func(_ context.Context, userID int64, nameQuery string, downloaded *bool, sort string) ([]domain.PublicApp, error) {
			capturedSort = sort
			return []domain.PublicApp{}, nil
		},
	}

	svc := NewPublicAppService(repo, nil, zap.NewNop())
	_, err := svc.GetAll(context.Background(), 17, "", nil, " rating ", "")
	require.NoError(t, err)
	assert.Equal(t, "rating", capturedSort)
}

func TestCategorizePublicApp(t *testing.T) {
	tests := []struct {
		name string
		app  domain.PublicApp
		want string
	}{
		{
			name: "artificial intelligence by name",
			app:  domain.PublicApp{AppName: "AlemGPT"},
			want: PublicAppCategoryArtificialIntelligence,
		},
		{
			name: "artificial intelligence by tag",
			app:  domain.PublicApp{AppName: "Internal tool", Tags: []string{"ИИ"}},
			want: PublicAppCategoryArtificialIntelligence,
		},
		{
			name: "document management by ODO acronym",
			app:  domain.PublicApp{AppName: "ОДО NITEC"},
			want: PublicAppCategoryDocumentManagement,
		},
		{
			name: "document management by qujat link",
			app:  domain.PublicApp{AppName: "МинТранспорт", Link: "http://transport.qujat.gov.kz"},
			want: PublicAppCategoryDocumentManagement,
		},
		{
			name: "MIICR acronym does not match standalone AI",
			app:  domain.PublicApp{AppName: "ОДО МИИЦР"},
			want: PublicAppCategoryDocumentManagement,
		},
		{
			name: "mail does not match AI token",
			app:  domain.PublicApp{AppName: "АП", Link: "https://mail.akorda.kz/"},
			want: PublicAppCategoryOther,
		},
		{
			name: "unknown app",
			app:  domain.PublicApp{AppName: "Tengri News"},
			want: PublicAppCategoryOther,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, categorizePublicApp(tt.app))
		})
	}
}

func TestPublicAppServiceGetAllAddsAndFiltersCategories(t *testing.T) {
	repo := &stubPublicAppRepo{
		findAllFn: func(_ context.Context, userID int64, nameQuery string, downloaded *bool, sort string) ([]domain.PublicApp, error) {
			return []domain.PublicApp{
				{ID: 1, AppName: "AlemGPT"},
				{ID: 2, AppName: "ОДО NITEC"},
				{ID: 3, AppName: "Tengri News"},
			}, nil
		},
	}

	svc := NewPublicAppService(repo, nil, zap.NewNop())
	apps, err := svc.GetAll(context.Background(), 17, "", nil, "", "ИИ")
	require.NoError(t, err)
	require.Len(t, apps, 1)
	assert.Equal(t, int64(1), apps[0].ID)
	assert.Equal(t, PublicAppCategoryArtificialIntelligence, apps[0].Category)
}

func TestPublicAppServiceCreateWithOrganizationIDs(t *testing.T) {
	var createdApp *domain.PublicApp
	repo := &stubPublicAppRepo{
		createFn: func(_ context.Context, app *domain.PublicApp) error {
			createdApp = app
			return nil
		},
	}

	svc := NewPublicAppService(repo, nil, zap.NewNop())
	organizationIDs := []int64{1, 2, 3}
	app, err := svc.Create(context.Background(), "AlemGPT", "https://example.com", "icon.png", true, 0, false, false, nil, "", organizationIDs)
	require.NoError(t, err)
	require.NotNil(t, createdApp)
	assert.Equal(t, organizationIDs, createdApp.OrganizationIDs)
	assert.Equal(t, organizationIDs, app.OrganizationIDs)
}

func TestPublicAppServiceUpdateWithOrganizationIDs(t *testing.T) {
	var updatedApp *domain.PublicApp
	repo := &stubPublicAppRepo{
		getByIDFn: func(_ context.Context, id int64, userID int64) (*domain.PublicApp, error) {
			return &domain.PublicApp{ID: id, AppName: "AlemGPT", OrganizationIDs: []int64{1}}, nil
		},
		updateFn: func(_ context.Context, app *domain.PublicApp) error {
			updatedApp = app
			return nil
		},
	}

	svc := NewPublicAppService(repo, nil, zap.NewNop())
	newOrganizationIDs := []int64{2, 3}
	app, err := svc.Update(context.Background(), 42, nil, nil, nil, nil, nil, nil, nil, nil, nil, &newOrganizationIDs)
	require.NoError(t, err)
	require.NotNil(t, updatedApp)
	assert.Equal(t, newOrganizationIDs, updatedApp.OrganizationIDs)
	assert.Equal(t, newOrganizationIDs, app.OrganizationIDs)
}
