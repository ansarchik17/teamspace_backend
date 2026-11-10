package service

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

var ErrInvalidPublicAppRating = errors.New("rating must be between 1 and 5")

const (
	PublicAppCategoryArtificialIntelligence = "ИИ"
	PublicAppCategoryDocumentManagement     = "Документация"
	PublicAppCategoryOther                  = "Прочие"
)

type publicAppCategoryRule struct {
	category string
	keywords []string
}

var publicAppCategoryRules = []publicAppCategoryRule{
	{
		category: PublicAppCategoryArtificialIntelligence,
		keywords: []string{
			"ии",
			"ai",
			"gpt",
			"alemgpt",
			"alemai",
			"datahub",
			"transcribe",
			"протоколирование",
			"искусственный интеллект",
			"искусственный интелект",
			"искуственный интеллект",
			"искуственный интелект",
			"национальная платформа ии",
		},
	},
	{
		category: PublicAppCategoryDocumentManagement,
		keywords: []string{
			"документация",
			"документы",
			"одо",
			"документооборот",
			"документ",
			"qujat",
			"edocs",
			"eqyzmet",
			"e kyzmet",
			"е kyzmet",
			"е обращение",
			"e обращение",
			"eotinish",
			"е лицензия",
			"e лицензия",
			"elicense",
			"salemoffice",
			"decard",
			"eps",
			"adilet",
			"zan",
			"судебный кабинет",
			"erdr",
		},
	},
}

var publicAppOtherCategoryKeywords = []string{
	"other",
	"прочие",
	"прочее",
	"другое",
}

type publicAppRepository interface {
	Create(ctx context.Context, app *domain.PublicApp) error
	FindAll(ctx context.Context, userID int64, nameQuery string, downloaded *bool, sort string) ([]domain.PublicApp, error)
	GetByID(ctx context.Context, id int64, userID int64) (*domain.PublicApp, error)
	Update(ctx context.Context, app *domain.PublicApp) error
	Delete(ctx context.Context, id int64) error
	AddFavorite(ctx context.Context, userID, appID int64) error
	RemoveFavorite(ctx context.Context, userID, appID int64) error
	GetFavorites(ctx context.Context, userID int64) ([]domain.PublicApp, error)
	ReorderFavorites(ctx context.Context, userID int64, appIDs []int64) error
	GrantAccess(ctx context.Context, appID int64, userID int64) error
	RevokeAccess(ctx context.Context, appID int64, userID int64) error
	UpsertRating(ctx context.Context, userID, appID int64, rating int) error
	RemoveRating(ctx context.Context, userID, appID int64) error
}

type PublicAppService struct {
	repo     publicAppRepository
	userRepo *repository.UserRepository
	logger   *zap.Logger
}

func NewPublicAppService(repo publicAppRepository, userRepo *repository.UserRepository, logger *zap.Logger) *PublicAppService {
	return &PublicAppService{
		repo:     repo,
		userRepo: userRepo,
		logger:   logger.With(zap.String("service", "public_app")),
	}
}

func (s *PublicAppService) Create(ctx context.Context, appName, link, appPhoto string, webView bool, position int, isGlobal bool, isPrivate bool, tags []string, category string, organizationIDs []int64) (*domain.PublicApp, error) {
	now := time.Now()
	if organizationIDs == nil {
		organizationIDs = []int64{}
	}
	app := &domain.PublicApp{
		AppName:         appName,
		Link:            link,
		AppPhoto:        appPhoto,
		WebView:         webView,
		IsGlobal:        isGlobal,
		IsPrivate:       isPrivate,
		Position:        position, // Note: Repository might override this if it auto-increments
		Category:        normalizePublicAppCategory(category),
		Tags:            normalizePublicAppTags(tags),
		OrganizationIDs: organizationIDs,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if app.Category == "" {
		app.Category = categorizePublicApp(*app)
	}

	if err := s.repo.Create(ctx, app); err != nil {
		return nil, err
	}

	applyPublicAppCategory(app)
	return app, nil
}

func (s *PublicAppService) GetAll(ctx context.Context, userID int64, nameQuery string, downloaded *bool, sort string, category string) ([]domain.PublicApp, error) {
	apps, err := s.repo.FindAll(ctx, userID, nameQuery, downloaded, normalizePublicAppSort(sort))
	if err != nil {
		return nil, err
	}

	return filterPublicAppsByCategory(apps, category), nil
}

func (s *PublicAppService) GetByID(ctx context.Context, id int64, userID int64) (*domain.PublicApp, error) {
	app, err := s.repo.GetByID(ctx, id, userID)
	if err != nil {
		return nil, err
	}

	applyPublicAppCategory(app)
	return app, nil
}

func (s *PublicAppService) Update(ctx context.Context, id int64, appName, link, appPhoto *string, webView *bool, position *int, isGlobal *bool, isPrivate *bool, tags *[]string, category *string, organizationIDs *[]int64) (*domain.PublicApp, error) {
	// We pass 0 as userID because for update logic existence check doesn't strictly need favorite status, but repo signature changed.
	// Or we can just ignore IsFavorite here.
	app, err := s.repo.GetByID(ctx, id, 0)
	if err != nil {
		return nil, err
	}

	if appName != nil {
		app.AppName = *appName
	}
	if link != nil {
		app.Link = *link
	}
	if appPhoto != nil {
		app.AppPhoto = *appPhoto
	}
	if webView != nil {
		app.WebView = *webView
	}
	if position != nil {
		app.Position = *position
	}
	if isGlobal != nil {
		app.IsGlobal = *isGlobal
	}
	if isPrivate != nil {
		app.IsPrivate = *isPrivate
	}
	if tags != nil {
		app.Tags = normalizePublicAppTags(*tags)
	}
	if category != nil {
		app.Category = normalizePublicAppCategory(*category)
	}
	if organizationIDs != nil {
		app.OrganizationIDs = *organizationIDs
	}
	if app.OrganizationIDs == nil {
		app.OrganizationIDs = []int64{}
	}
	app.UpdatedAt = time.Now()

	if err := s.repo.Update(ctx, app); err != nil {
		return nil, err
	}

	applyPublicAppCategory(app)
	return app, nil
}

func (s *PublicAppService) Delete(ctx context.Context, id int64) error {
	return s.repo.Delete(ctx, id)
}

func (s *PublicAppService) AddFavorite(ctx context.Context, userID, appID int64) error {
	return s.repo.AddFavorite(ctx, userID, appID)
}

func (s *PublicAppService) RemoveFavorite(ctx context.Context, userID, appID int64) error {
	return s.repo.RemoveFavorite(ctx, userID, appID)
}

func (s *PublicAppService) GetFavorites(ctx context.Context, userID int64) ([]domain.PublicApp, error) {
	apps, err := s.repo.GetFavorites(ctx, userID)
	if err != nil {
		return nil, err
	}

	return applyPublicAppCategories(apps), nil
}

func (s *PublicAppService) ReorderFavorites(ctx context.Context, userID int64, appIDs []int64) error {
	return s.repo.ReorderFavorites(ctx, userID, appIDs)
}

func (s *PublicAppService) UpdateFavoritePosition(ctx context.Context, userID int64, appID int64, newPosition int) error {
	// 1. Get all favorites for the user
	apps, err := s.repo.GetFavorites(ctx, userID)
	if err != nil {
		return err
	}

	// 2. Find the app in the list and current positions
	var currentApp *domain.PublicApp
	currentIndex := -1

	for i, app := range apps {
		if app.ID == appID {
			currentApp = &apps[i]
			currentIndex = i
			break
		}
	}

	if currentApp == nil {
		return nil
	}

	// 3. Remove from current position
	var newAppIDs []int64
	for i, app := range apps {
		if i == currentIndex {
			continue
		}
		newAppIDs = append(newAppIDs, app.ID)
	}

	// 4. Insert at new position
	if newPosition < 1 {
		newPosition = 1
	}
	if newPosition > len(newAppIDs)+1 {
		newPosition = len(newAppIDs) + 1
	}

	insertIndex := newPosition - 1

	finalAppIDs := make([]int64, 0, len(apps))
	finalAppIDs = append(finalAppIDs, newAppIDs[:insertIndex]...)
	finalAppIDs = append(finalAppIDs, appID)
	finalAppIDs = append(finalAppIDs, newAppIDs[insertIndex:]...)

	// 5. Save new order
	return s.repo.ReorderFavorites(ctx, userID, finalAppIDs)
}

func (s *PublicAppService) GrantAccess(ctx context.Context, performerID, appID, userID int64) error {
	if err := ensureUserScopedAccess(ctx, s.userRepo, performerID, userID); err != nil {
		return err
	}
	return s.repo.GrantAccess(ctx, appID, userID)
}

func (s *PublicAppService) RevokeAccess(ctx context.Context, performerID, appID, userID int64) error {
	if err := ensureUserScopedAccess(ctx, s.userRepo, performerID, userID); err != nil {
		return err
	}
	return s.repo.RevokeAccess(ctx, appID, userID)
}

func (s *PublicAppService) UpsertRating(ctx context.Context, userID, appID int64, rating int) (*domain.PublicApp, error) {
	if rating < 1 || rating > 5 {
		return nil, ErrInvalidPublicAppRating
	}

	if _, err := s.repo.GetByID(ctx, appID, userID); err != nil {
		return nil, err
	}

	if err := s.repo.UpsertRating(ctx, userID, appID, rating); err != nil {
		return nil, err
	}

	return s.GetByID(ctx, appID, userID)
}

func (s *PublicAppService) RemoveRating(ctx context.Context, userID, appID int64) (*domain.PublicApp, error) {
	if _, err := s.repo.GetByID(ctx, appID, userID); err != nil {
		return nil, err
	}

	if err := s.repo.RemoveRating(ctx, userID, appID); err != nil {
		return nil, err
	}

	return s.GetByID(ctx, appID, userID)
}

func applyPublicAppCategory(app *domain.PublicApp) {
	if app == nil {
		return
	}

	app.Category = resolvePublicAppCategory(*app)
}

func applyPublicAppCategories(apps []domain.PublicApp) []domain.PublicApp {
	for i := range apps {
		apps[i].Category = resolvePublicAppCategory(apps[i])
	}
	return apps
}

func filterPublicAppsByCategory(apps []domain.PublicApp, category string) []domain.PublicApp {
	apps = applyPublicAppCategories(apps)

	normalizedCategory := normalizePublicAppCategory(category)
	if normalizedCategory == "" {
		return apps
	}

	filtered := make([]domain.PublicApp, 0, len(apps))
	for _, app := range apps {
		if app.Category == normalizedCategory {
			filtered = append(filtered, app)
		}
	}

	return filtered
}

func categorizePublicApp(app domain.PublicApp) string {
	values := []string{app.AppName, app.Link}
	values = append(values, app.Tags...)
	text := normalizePublicAppCategoryText(values...)

	for _, rule := range publicAppCategoryRules {
		if hasAnyPublicAppCategoryKeyword(text, rule.keywords) {
			return rule.category
		}
	}

	return PublicAppCategoryOther
}

func resolvePublicAppCategory(app domain.PublicApp) string {
	if category := normalizePublicAppCategory(app.Category); category != "" {
		return category
	}

	return categorizePublicApp(app)
}

func normalizePublicAppCategory(category string) string {
	text := normalizePublicAppCategoryText(category)
	if text == "" || hasAnyPublicAppCategoryKeyword(text, []string{"all", "все"}) {
		return ""
	}

	if hasAnyPublicAppCategoryKeyword(text, publicAppOtherCategoryKeywords) {
		return PublicAppCategoryOther
	}

	for _, rule := range publicAppCategoryRules {
		if text == normalizePublicAppCategoryText(rule.category) || hasAnyPublicAppCategoryKeyword(text, rule.keywords) {
			return rule.category
		}
	}

	return strings.TrimSpace(category)
}

func normalizePublicAppCategoryText(values ...string) string {
	var b strings.Builder
	lastWasSpace := true

	for _, value := range values {
		for _, r := range strings.ToLower(strings.ReplaceAll(value, "ё", "е")) {
			if unicode.IsLetter(r) || unicode.IsNumber(r) {
				b.WriteRune(r)
				lastWasSpace = false
				continue
			}

			if !lastWasSpace {
				b.WriteByte(' ')
				lastWasSpace = true
			}
		}

		if !lastWasSpace {
			b.WriteByte(' ')
			lastWasSpace = true
		}
	}

	return strings.TrimSpace(b.String())
}

func hasAnyPublicAppCategoryKeyword(text string, keywords []string) bool {
	for _, keyword := range keywords {
		if hasPublicAppCategoryKeyword(text, keyword) {
			return true
		}
	}

	return false
}

func hasPublicAppCategoryKeyword(text string, keyword string) bool {
	normalizedKeyword := normalizePublicAppCategoryText(keyword)
	if text == "" || normalizedKeyword == "" {
		return false
	}

	return strings.Contains(" "+text+" ", " "+normalizedKeyword+" ")
}

func normalizePublicAppTags(tags []string) []string {
	if len(tags) == 0 {
		return []string{}
	}

	normalized := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		cleanTag := strings.TrimSpace(tag)
		if cleanTag == "" {
			continue
		}

		key := strings.ToLower(cleanTag)
		if _, exists := seen[key]; exists {
			continue
		}

		seen[key] = struct{}{}
		normalized = append(normalized, cleanTag)
	}

	return normalized
}

func normalizePublicAppSort(sort string) string {
	switch strings.ToLower(strings.TrimSpace(sort)) {
	case "rating":
		return "rating"
	default:
		return "position"
	}
}
