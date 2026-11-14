package service

import (
	"context"
	"testing"
	"time"

	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestSearchAllRejectsEmptyQuery(t *testing.T) {
	svc := NewSearchService(nil, nil, zap.NewNop())

	_, err := svc.SearchAll(context.Background(), 1, "   ", 10, SearchAllDependencies{})
	if err == nil {
		t.Fatalf("expected error for empty query")
	}
}

func TestNormalizeSearchText(t *testing.T) {
	got := normalizeSearchText("  ДоГоВоР   аренды  ")
	if got != "договор аренды" {
		t.Fatalf("normalizeSearchText() = %q", got)
	}
}

func TestAssembleGlobalSearchResponse(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	response := assembleGlobalSearchResponse("договор", globalSearchExecutionOptions{
		responseLimit: 3,
		mergeCap:      10,
	}, map[string]globalSearchEntityResult{
		"users": {
			users: []*domain.UserResponse{{ID: 1, FullName: "Тамерлан Картаев", FirstName: "Тамерлан", LastName: "Картаев", Username: "tkartaev", Email: "t@example.com", CreatedAt: now}},
			items: []rankedGlobalSearchItem{{
				item:  domain.SearchResultItem{Type: "user", ID: "1", Title: "Тамерлан Картаев", MatchedField: "full_name"},
				score: 90, sortTime: now,
			}},
		},
		"tasks": {
			tasks: []*domain.Task{{Title: "Договор на поставку", CreatedAt: now, UpdatedAt: now}},
			items: []rankedGlobalSearchItem{{
				item:  domain.SearchResultItem{Type: "task", ID: "task-1", Title: "Договор на поставку", MatchedField: "title"},
				score: 80, sortTime: now.Add(-time.Minute),
			}},
		},
		"organizations": {
			organizations: []domain.SearchResult{{ID: 10, Name: "Управление договорной работы", Type: "division"}},
			items: []rankedGlobalSearchItem{{
				item:  domain.SearchResultItem{Type: "organization", ID: "10", Title: "Управление договорной работы", MatchedField: "name"},
				score: 70, sortTime: now.Add(-2 * time.Minute),
			}},
		},
		"events": {
			events: []domain.Event{{Title: "Встреча по договору", CreatedAt: now, UpdatedAt: now}},
			items: []rankedGlobalSearchItem{{
				item:  domain.SearchResultItem{Type: "event", ID: "event-1", Title: "Встреча по договору", MatchedField: "title"},
				score: 50, sortTime: now.Add(-4 * time.Minute),
			}},
		},
		"meetings": {
			meetings: []domain.Event{{Title: "Онлайн встреча по договору", CreatedAt: now, UpdatedAt: now}},
			items: []rankedGlobalSearchItem{{
				item:  domain.SearchResultItem{Type: "meeting", ID: "meeting-1", Title: "Онлайн встреча по договору", MatchedField: "title"},
				score: 55, sortTime: now.Add(-5 * time.Minute),
			}},
		},
		"documents": {
			documents: []domain.DriveItem{{}},
			items: []rankedGlobalSearchItem{{
				item:  domain.SearchResultItem{Type: "document", ID: "doc-1", Title: "Договор аренды", MatchedField: "name"},
				score: 100, sortTime: now.Add(-6 * time.Minute),
			}},
		},
		"notes": {
			notes: []domain.Note{{Title: "Заметка про договор", CreatedAt: now, UpdatedAt: now}},
			items: []rankedGlobalSearchItem{{
				item:  domain.SearchResultItem{Type: "note", ID: "note-1", Title: "Заметка про договор", MatchedField: "title"},
				score: 40, sortTime: now.Add(-7 * time.Minute),
			}},
		},
	})

	if !response.Success {
		t.Fatalf("expected success response")
	}
	if response.Data.Query != "договор" {
		t.Fatalf("unexpected query: %q", response.Data.Query)
	}
	if response.Data.Total != 7 {
		t.Fatalf("unexpected total: %d", response.Data.Total)
	}
	if len(response.Data.Items) != 3 {
		t.Fatalf("unexpected items len: %d", len(response.Data.Items))
	}
	if response.Data.Items[0].Type != "document" {
		t.Fatalf("expected most relevant item to be document, got %q", response.Data.Items[0].Type)
	}
	if len(response.Users) != 1 || len(response.Tasks) != 1 || len(response.Organizations) != 1 {
		t.Fatalf("expected legacy buckets to be populated")
	}
	if len(response.Events) != 1 || len(response.Meetings) != 1 || len(response.Documents) != 1 || len(response.Notes) != 1 {
		t.Fatalf("expected all legacy buckets to be populated")
	}
}

func TestSearchAllWithProvidersSkipsTimedOutProvider(t *testing.T) {
	svc := NewSearchService(nil, nil, zap.NewNop())
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)

	providers := []globalSearchProvider{
		globalSearchProviderFunc{
			name:  "fast",
			empty: map[string]globalSearchEntityResult{"users": {users: []*domain.UserResponse{}}},
			search: func(ctx context.Context, _ int64, _ string, _ globalSearchExecutionOptions) (map[string]globalSearchEntityResult, error) {
				return map[string]globalSearchEntityResult{
					"users": {
						users: []*domain.UserResponse{{ID: 1, FullName: "Тамерлан Картаев", CreatedAt: now}},
						items: []rankedGlobalSearchItem{{
							item:     domain.SearchResultItem{Type: "user", ID: "1", Title: "Тамерлан Картаев"},
							score:    90,
							sortTime: now,
						}},
					},
				}, nil
			},
		},
		globalSearchProviderFunc{
			name:  "slow",
			empty: map[string]globalSearchEntityResult{"documents": {documents: []domain.DriveItem{}}},
			search: func(ctx context.Context, _ int64, _ string, _ globalSearchExecutionOptions) (map[string]globalSearchEntityResult, error) {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(200 * time.Millisecond):
					return map[string]globalSearchEntityResult{
						"documents": {
							documents: []domain.DriveItem{{}},
						},
					}, nil
				}
			},
		},
	}

	start := time.Now()
	response, err := svc.searchAllWithProviders(context.Background(), 1, "договор", providers, globalSearchExecutionOptions{
		responseLimit:   10,
		providerLimit:   10,
		mergeCap:        20,
		overallTimeout:  250 * time.Millisecond,
		providerTimeout: 40 * time.Millisecond,
		concurrency:     4,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if time.Since(start) >= 150*time.Millisecond {
		t.Fatalf("expected timed out provider not to block overall search")
	}
	if len(response.Users) != 1 {
		t.Fatalf("expected fast provider result to survive")
	}
	if len(response.Documents) != 0 {
		t.Fatalf("expected timed out provider bucket to stay empty")
	}
}

func TestSearchAllWithProvidersRespectsSortingAndLimit(t *testing.T) {
	svc := NewSearchService(nil, nil, zap.NewNop())
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)

	providers := []globalSearchProvider{
		globalSearchProviderFunc{
			name:  "provider-a",
			empty: map[string]globalSearchEntityResult{"users": {users: []*domain.UserResponse{}}},
			search: func(ctx context.Context, _ int64, _ string, _ globalSearchExecutionOptions) (map[string]globalSearchEntityResult, error) {
				return map[string]globalSearchEntityResult{
					"users": {
						users: []*domain.UserResponse{{ID: 1, FullName: "Тамерлан Картаев", CreatedAt: now}},
						items: []rankedGlobalSearchItem{
							{item: domain.SearchResultItem{Type: "user", ID: "1", Title: "Тамерлан Картаев"}, score: 95, sortTime: now},
							{item: domain.SearchResultItem{Type: "user", ID: "2", Title: "Картаев"}, score: 40, sortTime: now.Add(-time.Minute)},
						},
					},
				}, nil
			},
		},
		globalSearchProviderFunc{
			name:  "provider-b",
			empty: map[string]globalSearchEntityResult{"documents": {documents: []domain.DriveItem{}}},
			search: func(ctx context.Context, _ int64, _ string, _ globalSearchExecutionOptions) (map[string]globalSearchEntityResult, error) {
				return map[string]globalSearchEntityResult{
					"documents": {
						documents: []domain.DriveItem{{}},
						items: []rankedGlobalSearchItem{
							{item: domain.SearchResultItem{Type: "document", ID: "doc-1", Title: "Договор аренды"}, score: 100, sortTime: now.Add(-time.Hour)},
							{item: domain.SearchResultItem{Type: "document", ID: "doc-2", Title: "Старый договор"}, score: 60, sortTime: now.Add(-2 * time.Hour)},
						},
					},
				}, nil
			},
		},
	}

	response, err := svc.searchAllWithProviders(context.Background(), 1, "договор", providers, globalSearchExecutionOptions{
		responseLimit:   2,
		providerLimit:   10,
		mergeCap:        10,
		overallTimeout:  time.Second,
		providerTimeout: time.Second,
		concurrency:     2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(response.Data.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(response.Data.Items))
	}
	if response.Data.Items[0].ID != "doc-1" || response.Data.Items[1].ID != "1" {
		t.Fatalf("unexpected sort order: %#v", response.Data.Items)
	}
	if response.Data.Total != 4 {
		t.Fatalf("unexpected total: %d", response.Data.Total)
	}
}

func TestGlobalSearchHasNoMessengerProvider(t *testing.T) {
	svc := NewSearchService(nil, nil, zap.NewNop())
	for _, provider := range svc.buildGlobalSearchProviders(SearchAllDependencies{}) {
		if provider.Name() == "messages" {
			t.Fatal("messenger search provider remains")
		}
	}
	response := assembleGlobalSearchResponse("query", defaultGlobalSearchExecutionOptions(10), defaultGlobalSearchResults())
	if response.Messages == nil || len(response.Messages) != 0 {
		t.Fatal("legacy message bucket must be an empty JSON array")
	}
}
