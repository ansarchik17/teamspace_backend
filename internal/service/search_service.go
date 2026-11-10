package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

const (
	defaultUserSearchLimit              = 20
	maxUserSearchLimit                  = 50
	defaultMessageSearchLimit           = 50
	maxMessageSearchLimit               = 100
	defaultGlobalSearchLimit            = 10
	maxGlobalSearchLimit                = 50
	defaultGlobalSearchOverallTimeout   = 2 * time.Second
	defaultGlobalSearchProviderTimeout  = 700 * time.Millisecond
	defaultGlobalSearchProviderLimitMax = 20
	defaultGlobalSearchMergeCapMax      = 200
	defaultGlobalSearchConcurrencyLimit = 8
)

type SearchService struct {
	userRepo    *repository.UserRepository
	messageRepo *repository.MessageRepository
	noteService *NoteService
	logger      *zap.Logger
}

type SearchAllDependencies struct {
	Kanban   *KanbanService
	Calendar *CalendarService
	Drive    *DriveService
}

type globalSearchEntityResult struct {
	users         []*domain.UserResponse
	tasks         []*domain.Task
	organizations []domain.SearchResult
	messages      []domain.MessageSearchResult
	events        []domain.Event
	meetings      []domain.Event
	documents     []domain.DriveItem
	notes         []domain.Note
	items         []rankedGlobalSearchItem
}

type rankedGlobalSearchItem struct {
	item     domain.SearchResultItem
	score    int
	sortTime time.Time
}

type globalSearchExecutionOptions struct {
	responseLimit   int
	providerLimit   int
	mergeCap        int
	overallTimeout  time.Duration
	providerTimeout time.Duration
	concurrency     int
}

type globalSearchProvider interface {
	Name() string
	Search(ctx context.Context, userID int64, query string, opts globalSearchExecutionOptions) (map[string]globalSearchEntityResult, error)
	Empty() map[string]globalSearchEntityResult
}

type globalSearchProviderFunc struct {
	name   string
	search func(context.Context, int64, string, globalSearchExecutionOptions) (map[string]globalSearchEntityResult, error)
	empty  map[string]globalSearchEntityResult
}

func (p globalSearchProviderFunc) Name() string {
	return p.name
}

func (p globalSearchProviderFunc) Search(ctx context.Context, userID int64, query string, opts globalSearchExecutionOptions) (map[string]globalSearchEntityResult, error) {
	return p.search(ctx, userID, query, opts)
}

func (p globalSearchProviderFunc) Empty() map[string]globalSearchEntityResult {
	result := make(map[string]globalSearchEntityResult, len(p.empty))
	for key, value := range p.empty {
		result[key] = value
	}
	return result
}

func NewSearchService(
	userRepo *repository.UserRepository,
	messageRepo *repository.MessageRepository,
	noteService *NoteService,
	logger *zap.Logger,
) *SearchService {
	return &SearchService{
		userRepo:    userRepo,
		messageRepo: messageRepo,
		noteService: noteService,
		logger:      logger.With(zap.String("service", "search")),
	}
}

func (s *SearchService) SearchUsers(ctx context.Context, userID int64, query string, limit int) ([]*domain.UserResponse, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, apperrors.BadRequest("search query must not be empty")
	}

	if limit <= 0 || limit > maxUserSearchLimit {
		limit = defaultUserSearchLimit
	}

	users, err := s.userRepo.SearchVisibleTo(ctx, userID, query, limit, 0)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to search users", err)
	}

	responses := make([]*domain.UserResponse, 0, len(users))
	for _, user := range users {
		responses = append(responses, user.ToResponse())
	}

	s.logger.Info("Users searched", zap.Int("results", len(responses)))

	return responses, nil
}

func (s *SearchService) SearchMessages(ctx context.Context, conversationID uuid.UUID, userID int64, query string, limit int) ([]*domain.MessageView, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, apperrors.BadRequest("search query must not be empty")
	}

	if limit <= 0 || limit > maxMessageSearchLimit {
		limit = defaultMessageSearchLimit
	}

	messages, err := s.messageRepo.SearchMessages(ctx, conversationID, userID, query, limit)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to search messages", err)
	}

	s.logger.Info("Messages searched",
		zap.String("conversation_id", conversationID.String()),
		zap.Int("results", len(messages)),
	)

	return messages, nil
}

func (s *SearchService) FindIdenticalMessages(ctx context.Context, userID int64, content string) ([]*domain.MessageView, error) {
	if content == "" {
		return nil, apperrors.BadRequest("content cannot be empty")
	}

	hash := sha256.Sum256([]byte(strings.TrimSpace(content)))
	hashStr := hex.EncodeToString(hash[:])

	messages, err := s.messageRepo.GetByContentHash(ctx, hashStr, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("failed to find identical messages", err)
	}

	s.logger.Info("Identical messages found", zap.String("hash", hashStr), zap.Int("results", len(messages)))

	return messages, nil
}

func (s *SearchService) SearchGlobal(ctx context.Context, userID int64, query string, limit int) (map[string]interface{}, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, apperrors.BadRequest("search query must not be empty")
	}

	if limit <= 0 || limit > maxUserSearchLimit {
		limit = defaultUserSearchLimit
	}

	users, err := s.SearchUsers(ctx, userID, query, limit)
	if err != nil {
		users = []*domain.UserResponse{}
	}

	result := map[string]interface{}{
		"query": query,
		"users": users,
		"counts": map[string]int{
			"users": len(users),
		},
	}

	return result, nil
}

func (s *SearchService) SearchAll(ctx context.Context, userID int64, query string, limit int, deps SearchAllDependencies) (*domain.SearchAllResponse, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, apperrors.BadRequest("query parameter 'q' is required")
	}

	opts := defaultGlobalSearchExecutionOptions(limit)
	return s.searchAllWithProviders(ctx, userID, query, s.buildGlobalSearchProviders(deps), opts)
}

func (s *SearchService) searchAllWithProviders(ctx context.Context, userID int64, query string, providers []globalSearchProvider, opts globalSearchExecutionOptions) (*domain.SearchAllResponse, error) {
	if strings.TrimSpace(query) == "" {
		return nil, apperrors.BadRequest("query parameter 'q' is required")
	}

	if opts.responseLimit <= 0 {
		opts = defaultGlobalSearchExecutionOptions(defaultGlobalSearchLimit)
	}

	searchCtx := ctx
	cancel := func() {}
	if opts.overallTimeout > 0 {
		searchCtx, cancel = context.WithTimeout(ctx, opts.overallTimeout)
	}
	defer cancel()

	results := defaultGlobalSearchResults()
	var mu sync.Mutex

	var g errgroup.Group
	if opts.concurrency > 0 {
		g.SetLimit(opts.concurrency)
	}

	for _, provider := range providers {
		provider := provider
		g.Go(func() error {
			startedAt := time.Now()
			providerCtx := searchCtx
			providerCancel := func() {}
			if opts.providerTimeout > 0 {
				providerCtx, providerCancel = context.WithTimeout(searchCtx, opts.providerTimeout)
			}
			defer providerCancel()

			providerResults, err := provider.Search(providerCtx, userID, query, opts)
			if err != nil {
				s.logGlobalSearchProviderError(provider.Name(), time.Since(startedAt), err)
				providerResults = provider.Empty()
			}

			mu.Lock()
			for key, value := range providerResults {
				results[key] = value
			}
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, apperrors.InternalWithErr("failed to run global search providers", err)
	}

	return assembleGlobalSearchResponse(query, opts, results), nil
}

func (s *SearchService) buildGlobalSearchProviders(deps SearchAllDependencies) []globalSearchProvider {
	return []globalSearchProvider{
		globalSearchProviderFunc{
			name:  "users",
			empty: map[string]globalSearchEntityResult{"users": {users: []*domain.UserResponse{}}},
			search: func(ctx context.Context, userID int64, query string, opts globalSearchExecutionOptions) (map[string]globalSearchEntityResult, error) {
				result, err := s.searchUsersForGlobal(ctx, userID, query, opts.providerLimit)
				if err != nil {
					return nil, err
				}
				return map[string]globalSearchEntityResult{"users": result}, nil
			},
		},
		globalSearchProviderFunc{
			name:  "tasks",
			empty: map[string]globalSearchEntityResult{"tasks": {tasks: []*domain.Task{}}},
			search: func(ctx context.Context, userID int64, query string, opts globalSearchExecutionOptions) (map[string]globalSearchEntityResult, error) {
				result, err := s.searchTasksForGlobal(ctx, userID, query, opts.providerLimit, deps.Kanban)
				if err != nil {
					return nil, err
				}
				return map[string]globalSearchEntityResult{"tasks": result}, nil
			},
		},
		globalSearchProviderFunc{
			name:  "organizations",
			empty: map[string]globalSearchEntityResult{"organizations": {organizations: []domain.SearchResult{}}},
			search: func(ctx context.Context, _ int64, query string, opts globalSearchExecutionOptions) (map[string]globalSearchEntityResult, error) {
				result, err := s.searchOrganizationsForGlobal(ctx, query, opts.providerLimit)
				if err != nil {
					return nil, err
				}
				return map[string]globalSearchEntityResult{"organizations": result}, nil
			},
		},
		globalSearchProviderFunc{
			name:  "messages",
			empty: map[string]globalSearchEntityResult{"messages": {messages: []domain.MessageSearchResult{}}},
			search: func(ctx context.Context, userID int64, query string, opts globalSearchExecutionOptions) (map[string]globalSearchEntityResult, error) {
				result, err := s.searchMessagesForGlobal(ctx, userID, query, opts.providerLimit)
				if err != nil {
					return nil, err
				}
				return map[string]globalSearchEntityResult{"messages": result}, nil
			},
		},
		globalSearchProviderFunc{
			name: "calendar",
			empty: map[string]globalSearchEntityResult{
				"events":   {events: []domain.Event{}},
				"meetings": {meetings: []domain.Event{}},
			},
			search: func(ctx context.Context, userID int64, query string, opts globalSearchExecutionOptions) (map[string]globalSearchEntityResult, error) {
				eventsResult, meetingsResult, err := s.searchEventsForGlobal(ctx, userID, query, opts.providerLimit, deps.Calendar)
				if err != nil {
					return nil, err
				}
				return map[string]globalSearchEntityResult{
					"events":   eventsResult,
					"meetings": meetingsResult,
				}, nil
			},
		},
		globalSearchProviderFunc{
			name:  "documents",
			empty: map[string]globalSearchEntityResult{"documents": {documents: []domain.DriveItem{}}},
			search: func(ctx context.Context, userID int64, query string, opts globalSearchExecutionOptions) (map[string]globalSearchEntityResult, error) {
				result, err := s.searchDocumentsForGlobal(ctx, userID, query, opts.providerLimit, deps.Drive)
				if err != nil {
					return nil, err
				}
				return map[string]globalSearchEntityResult{"documents": result}, nil
			},
		},
		globalSearchProviderFunc{
			name:  "notes",
			empty: map[string]globalSearchEntityResult{"notes": {notes: []domain.Note{}}},
			search: func(ctx context.Context, userID int64, query string, opts globalSearchExecutionOptions) (map[string]globalSearchEntityResult, error) {
				result, err := s.searchNotesForGlobal(ctx, userID, query, opts.providerLimit)
				if err != nil {
					return nil, err
				}
				return map[string]globalSearchEntityResult{"notes": result}, nil
			},
		},
	}
}

func (s *SearchService) searchUsersForGlobal(ctx context.Context, userID int64, query string, limit int) (globalSearchEntityResult, error) {
	users, err := s.userRepo.SearchVisibleTo(ctx, userID, query, limit, 0)
	if err != nil {
		return globalSearchEntityResult{users: []*domain.UserResponse{}}, err
	}

	responses := make([]*domain.UserResponse, 0, len(users))
	items := make([]rankedGlobalSearchItem, 0, len(users))
	for _, user := range users {
		response := user.ToResponse()
		responses = append(responses, response)

		fullName := strings.TrimSpace(response.FullName)
		description := strings.TrimSpace(firstNonEmptyString(ptrStringValue(response.Position), response.Email, response.Username))
		item := domain.SearchResultItem{
			Type:         "user",
			ID:           fmt.Sprintf("%d", response.ID),
			Title:        fullName,
			Description:  description,
			MatchedField: bestMatchedField(query, []globalSearchFieldCandidate{{Name: "full_name", Value: fullName}, {Name: "first_name", Value: response.FirstName}, {Name: "last_name", Value: response.LastName}, {Name: "username", Value: response.Username}, {Name: "email", Value: response.Email}, {Name: "full_name_local", Value: ptrStringValue(response.FullNameLocal)}}),
			CreatedAt:    copyTimePtr(&response.CreatedAt),
			Payload: map[string]any{
				"id":       response.ID,
				"username": response.Username,
				"email":    response.Email,
			},
		}
		items = append(items, rankedGlobalSearchItem{
			item:     item,
			score:    scoreGlobalSearchItem(query, item, []globalSearchFieldCandidate{{Name: "full_name", Value: fullName}, {Name: "description", Value: description}}),
			sortTime: response.CreatedAt,
		})
	}

	return globalSearchEntityResult{users: responses, items: items}, nil
}

func (s *SearchService) searchTasksForGlobal(ctx context.Context, userID int64, query string, limit int, kanbanService *KanbanService) (globalSearchEntityResult, error) {
	if kanbanService == nil {
		return globalSearchEntityResult{tasks: []*domain.Task{}}, nil
	}

	tasks, err := kanbanService.taskRepo.SearchLightweight(ctx, userID, domain.TaskFilters{
		Query: strings.TrimSpace(query),
		Limit: limit,
	})
	if err != nil {
		return globalSearchEntityResult{tasks: []*domain.Task{}}, err
	}

	items := make([]rankedGlobalSearchItem, 0, len(tasks))
	for _, task := range tasks {
		description := truncateSnippet(ptrStringValue(task.Description), 200)
		item := domain.SearchResultItem{
			Type:         "task",
			ID:           task.ID.String(),
			Title:        strings.TrimSpace(task.Title),
			Description:  description,
			MatchedField: bestMatchedField(query, []globalSearchFieldCandidate{{Name: "title", Value: task.Title}, {Name: "description", Value: ptrStringValue(task.Description)}, {Name: "status", Value: task.Status}, {Name: "task_key", Value: task.TaskKey}}),
			CreatedAt:    copyTimePtr(&task.CreatedAt),
			UpdatedAt:    copyTimePtr(&task.UpdatedAt),
			Payload: map[string]any{
				"id":       task.ID.String(),
				"board_id": task.BoardID.String(),
				"task_key": task.TaskKey,
				"status":   task.Status,
			},
		}
		items = append(items, rankedGlobalSearchItem{
			item:     item,
			score:    scoreGlobalSearchItem(query, item, []globalSearchFieldCandidate{{Name: "title", Value: task.Title}, {Name: "description", Value: ptrStringValue(task.Description)}, {Name: "status", Value: task.Status}}),
			sortTime: latestTime(task.UpdatedAt, task.CreatedAt),
		})
	}

	return globalSearchEntityResult{tasks: tasks, items: items}, nil
}

func (s *SearchService) searchOrganizationsForGlobal(ctx context.Context, query string, limit int) (globalSearchEntityResult, error) {
	return globalSearchEntityResult{organizations: []domain.SearchResult{}}, nil
}

func (s *SearchService) searchMessagesForGlobal(ctx context.Context, userID int64, query string, limit int) (globalSearchEntityResult, error) {
	results, err := s.messageRepo.SearchGlobal(ctx, userID, query, limit)
	if err != nil {
		return globalSearchEntityResult{messages: []domain.MessageSearchResult{}}, err
	}

	items := make([]rankedGlobalSearchItem, 0, len(results))
	for _, result := range results {
		title := strings.TrimSpace(firstNonEmptyString(result.SenderFullName, result.SenderUsername, "Сообщение"))
		description := truncateSnippet(result.Content, 200)
		item := domain.SearchResultItem{
			Type:         "message",
			ID:           result.ID,
			Title:        title,
			Description:  description,
			MatchedField: bestMatchedField(query, []globalSearchFieldCandidate{{Name: "text", Value: result.Content}, {Name: "sender", Value: title}}),
			CreatedAt:    copyTimePtr(&result.CreatedAt),
			UpdatedAt:    copyTimePtr(&result.UpdatedAt),
			Payload: map[string]any{
				"id":              result.ID,
				"conversation_id": result.ConversationID,
				"sender_id":       result.SenderID,
				"type":            result.Type,
			},
		}
		items = append(items, rankedGlobalSearchItem{
			item:     item,
			score:    scoreGlobalSearchItem(query, item, []globalSearchFieldCandidate{{Name: "text", Value: result.Content}, {Name: "sender", Value: title}}),
			sortTime: latestTime(result.UpdatedAt, result.CreatedAt),
		})
	}

	return globalSearchEntityResult{messages: results, items: items}, nil
}

func (s *SearchService) searchEventsForGlobal(ctx context.Context, userID int64, query string, limit int, calendarService *CalendarService) (globalSearchEntityResult, globalSearchEntityResult, error) {
	emptyEvents := globalSearchEntityResult{events: []domain.Event{}}
	emptyMeetings := globalSearchEntityResult{meetings: []domain.Event{}}
	if calendarService == nil {
		return emptyEvents, emptyMeetings, nil
	}

	from := time.Now().AddDate(-1, 0, 0)
	to := time.Now().AddDate(2, 0, 0)
	events, err := calendarService.SearchEvents(ctx, userID, query, from, to, limit)
	if err != nil {
		return emptyEvents, emptyMeetings, err
	}

	eventItems := make([]rankedGlobalSearchItem, 0, len(events))
	meetingLegacy := make([]domain.Event, 0)
	meetingItems := make([]rankedGlobalSearchItem, 0)

	for _, event := range events {
		description := truncateSnippet(firstNonEmptyString(ptrStringValue(event.Description), ptrStringValue(event.Location)), 200)
		fieldCandidates := []globalSearchFieldCandidate{{Name: "title", Value: event.Title}, {Name: "description", Value: ptrStringValue(event.Description)}, {Name: "location", Value: ptrStringValue(event.Location)}}
		baseItem := domain.SearchResultItem{
			ID:           event.ID.String(),
			Title:        strings.TrimSpace(event.Title),
			Description:  description,
			MatchedField: bestMatchedField(query, fieldCandidates),
			CreatedAt:    copyTimePtr(&event.CreatedAt),
			UpdatedAt:    copyTimePtr(&event.UpdatedAt),
			Payload: map[string]any{
				"id":        event.ID.String(),
				"user_id":   event.UserID,
				"is_online": event.IsOnline,
			},
		}

		eventItem := baseItem
		eventItem.Type = "event"
		eventItems = append(eventItems, rankedGlobalSearchItem{
			item:     eventItem,
			score:    scoreGlobalSearchItem(query, eventItem, fieldCandidates),
			sortTime: latestTime(event.UpdatedAt, event.CreatedAt),
		})

		if event.IsOnline {
			meetingLegacy = append(meetingLegacy, event)
			meetingItem := baseItem
			meetingItem.Type = "meeting"
			meetingItems = append(meetingItems, rankedGlobalSearchItem{
				item:     meetingItem,
				score:    scoreGlobalSearchItem(query, meetingItem, fieldCandidates) + 5,
				sortTime: latestTime(event.UpdatedAt, event.CreatedAt),
			})
		}
	}

	return globalSearchEntityResult{events: events, items: eventItems}, globalSearchEntityResult{meetings: meetingLegacy, items: meetingItems}, nil
}

func (s *SearchService) searchDocumentsForGlobal(ctx context.Context, userID int64, query string, limit int, driveService *DriveService) (globalSearchEntityResult, error) {
	if driveService == nil {
		return globalSearchEntityResult{documents: []domain.DriveItem{}}, nil
	}

	items, err := driveService.SearchAndFilter(ctx, userID, query, "", limit, 0, nil, "")
	if err != nil {
		return globalSearchEntityResult{documents: []domain.DriveItem{}}, err
	}

	results := make([]rankedGlobalSearchItem, 0, len(items))
	for _, driveItem := range items {
		if driveItem.File != nil {
			file := driveItem.File
			searchItem := domain.SearchResultItem{
				Type:         "document",
				ID:           file.ID.String(),
				Title:        strings.TrimSpace(file.RealName),
				Description:  strings.TrimSpace(file.MimeType),
				MatchedField: bestMatchedField(query, []globalSearchFieldCandidate{{Name: "name", Value: file.RealName}, {Name: "mime_type", Value: file.MimeType}}),
				CreatedAt:    copyTimePtr(&file.CreatedAt),
				UpdatedAt:    copyTimePtr(&file.UpdatedAt),
				Payload: map[string]any{
					"id":        file.ID.String(),
					"folder_id": uuidString(file.FolderID),
					"scope":     file.Scope,
					"type":      "file",
				},
			}
			results = append(results, rankedGlobalSearchItem{
				item:     searchItem,
				score:    scoreGlobalSearchItem(query, searchItem, []globalSearchFieldCandidate{{Name: "title", Value: file.RealName}, {Name: "description", Value: file.MimeType}}),
				sortTime: latestTime(file.UpdatedAt, file.CreatedAt),
			})
			continue
		}

		if driveItem.Folder != nil {
			folder := driveItem.Folder
			searchItem := domain.SearchResultItem{
				Type:         "document",
				ID:           folder.ID.String(),
				Title:        strings.TrimSpace(folder.Name),
				Description:  "folder",
				MatchedField: bestMatchedField(query, []globalSearchFieldCandidate{{Name: "name", Value: folder.Name}}),
				CreatedAt:    copyTimePtr(&folder.CreatedAt),
				UpdatedAt:    copyTimePtr(&folder.UpdatedAt),
				Payload: map[string]any{
					"id":        folder.ID.String(),
					"parent_id": uuidString(folder.ParentID),
					"type":      "folder",
				},
			}
			results = append(results, rankedGlobalSearchItem{
				item:     searchItem,
				score:    scoreGlobalSearchItem(query, searchItem, []globalSearchFieldCandidate{{Name: "title", Value: folder.Name}}),
				sortTime: latestTime(folder.UpdatedAt, folder.CreatedAt),
			})
		}
	}

	return globalSearchEntityResult{documents: items, items: results}, nil
}

func (s *SearchService) searchNotesForGlobal(ctx context.Context, userID int64, query string, limit int) (globalSearchEntityResult, error) {
	if s.noteService == nil {
		return globalSearchEntityResult{notes: []domain.Note{}}, nil
	}

	notes, _, err := s.noteService.repo.ListNotes(ctx, domain.NoteListFilter{
		UserID: userID,
		Query:  query,
		Limit:  limit,
	})
	if err != nil {
		return globalSearchEntityResult{notes: []domain.Note{}}, err
	}

	items := make([]rankedGlobalSearchItem, 0, len(notes))
	for _, note := range notes {
		description := truncateSnippet(firstNonEmptyString(note.ContentText, note.Content), 200)
		searchItem := domain.SearchResultItem{
			Type:         "note",
			ID:           note.ID.String(),
			Title:        strings.TrimSpace(note.Title),
			Description:  description,
			MatchedField: bestMatchedField(query, []globalSearchFieldCandidate{{Name: "title", Value: note.Title}, {Name: "content", Value: firstNonEmptyString(note.ContentText, note.Content)}}),
			CreatedAt:    copyTimePtr(&note.CreatedAt),
			UpdatedAt:    copyTimePtr(&note.UpdatedAt),
			Payload: map[string]any{
				"id":        note.ID.String(),
				"folder_id": uuidString(note.FolderID),
				"type":      note.Type,
			},
		}
		items = append(items, rankedGlobalSearchItem{
			item:     searchItem,
			score:    scoreGlobalSearchItem(query, searchItem, []globalSearchFieldCandidate{{Name: "title", Value: note.Title}, {Name: "content", Value: firstNonEmptyString(note.ContentText, note.Content)}}),
			sortTime: latestTime(note.UpdatedAt, note.CreatedAt),
		})
	}

	return globalSearchEntityResult{notes: notes, items: items}, nil
}

func defaultGlobalSearchExecutionOptions(limit int) globalSearchExecutionOptions {
	if limit <= 0 || limit > maxGlobalSearchLimit {
		limit = defaultGlobalSearchLimit
	}

	providerLimit := limit * 2
	if providerLimit < limit {
		providerLimit = limit
	}
	if providerLimit < defaultGlobalSearchLimit {
		providerLimit = defaultGlobalSearchLimit
	}
	if providerLimit > defaultGlobalSearchProviderLimitMax {
		providerLimit = defaultGlobalSearchProviderLimitMax
	}

	mergeCap := providerLimit * defaultGlobalSearchConcurrencyLimit
	if mergeCap < limit {
		mergeCap = limit
	}
	if mergeCap > defaultGlobalSearchMergeCapMax {
		mergeCap = defaultGlobalSearchMergeCapMax
	}

	return globalSearchExecutionOptions{
		responseLimit:   limit,
		providerLimit:   providerLimit,
		mergeCap:        mergeCap,
		overallTimeout:  defaultGlobalSearchOverallTimeout,
		providerTimeout: defaultGlobalSearchProviderTimeout,
		concurrency:     defaultGlobalSearchConcurrencyLimit,
	}
}

func defaultGlobalSearchResults() map[string]globalSearchEntityResult {
	return map[string]globalSearchEntityResult{
		"users":         {users: []*domain.UserResponse{}},
		"tasks":         {tasks: []*domain.Task{}},
		"organizations": {organizations: []domain.SearchResult{}},
		"messages":      {messages: []domain.MessageSearchResult{}},
		"events":        {events: []domain.Event{}},
		"meetings":      {meetings: []domain.Event{}},
		"documents":     {documents: []domain.DriveItem{}},
		"notes":         {notes: []domain.Note{}},
	}
}

func (s *SearchService) logGlobalSearchProviderError(providerName string, duration time.Duration, err error) {
	if err == nil {
		return
	}

	level := zap.WarnLevel
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		level = zap.ErrorLevel
	}

	log := s.logger.With(
		zap.String("provider", providerName),
		zap.Duration("duration", duration),
	)
	if ce := log.Check(level, "Global search provider failed"); ce != nil {
		ce.Write(zap.Error(err))
	}
}

func assembleGlobalSearchResponse(query string, opts globalSearchExecutionOptions, entities map[string]globalSearchEntityResult) *domain.SearchAllResponse {
	rankedItems := make([]rankedGlobalSearchItem, 0, opts.mergeCap)
	total := 0
	for _, entity := range entities {
		total += len(entity.items)
		rankedItems = append(rankedItems, entity.items...)
	}

	sort.SliceStable(rankedItems, func(i, j int) bool {
		if rankedItems[i].score != rankedItems[j].score {
			return rankedItems[i].score > rankedItems[j].score
		}
		return rankedItems[i].sortTime.After(rankedItems[j].sortTime)
	})

	if opts.mergeCap > 0 && len(rankedItems) > opts.mergeCap {
		rankedItems = rankedItems[:opts.mergeCap]
	}
	if opts.responseLimit > 0 && len(rankedItems) > opts.responseLimit {
		rankedItems = rankedItems[:opts.responseLimit]
	}

	items := make([]domain.SearchResultItem, 0, len(rankedItems))
	for _, item := range rankedItems {
		items = append(items, item.item)
	}

	return &domain.SearchAllResponse{
		Success: true,
		Data: domain.SearchAllData{
			Query: query,
			Total: total,
			Items: items,
		},
		Users:         entities["users"].users,
		Tasks:         entities["tasks"].tasks,
		Organizations: entities["organizations"].organizations,
		Messages:      entities["messages"].messages,
		Events:        entities["events"].events,
		Meetings:      entities["meetings"].meetings,
		Documents:     entities["documents"].documents,
		Notes:         entities["notes"].notes,
	}
}

type globalSearchFieldCandidate struct {
	Name  string
	Value string
}

func bestMatchedField(query string, candidates []globalSearchFieldCandidate) string {
	bestName := ""
	bestScore := -1
	for _, candidate := range candidates {
		score := candidateMatchScore(query, candidate.Value, candidate.Name == "title" || candidate.Name == "name" || candidate.Name == "full_name")
		if score > bestScore {
			bestScore = score
			bestName = candidate.Name
		}
	}
	return bestName
}

func scoreGlobalSearchItem(query string, item domain.SearchResultItem, candidates []globalSearchFieldCandidate) int {
	score := 0
	for _, candidate := range candidates {
		score += candidateMatchScore(query, candidate.Value, candidate.Name == "title" || candidate.Name == "name" || candidate.Name == "full_name")
	}
	if item.Type == "meeting" {
		score += 3
	}
	return score
}

func candidateMatchScore(query, value string, highPriority bool) int {
	normalizedQuery := normalizeSearchText(query)
	normalizedValue := normalizeSearchText(value)
	if normalizedQuery == "" || normalizedValue == "" {
		return 0
	}

	score := 0
	switch {
	case normalizedValue == normalizedQuery:
		score += 100
	case strings.HasPrefix(normalizedValue, normalizedQuery):
		score += 70
	case strings.Contains(normalizedValue, normalizedQuery):
		score += 45
	}

	for _, token := range strings.Fields(normalizedQuery) {
		switch {
		case normalizedValue == token:
			score += 35
		case strings.HasPrefix(normalizedValue, token):
			score += 20
		case strings.Contains(normalizedValue, token):
			score += 10
		}
	}

	if highPriority {
		score += 15
	}

	return score
}

func normalizeSearchText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func truncateSnippet(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" || limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit])) + "..."
}

func latestTime(primary, fallback time.Time) time.Time {
	if !primary.IsZero() {
		return primary
	}
	return fallback
}

func copyTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func ptrStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func uuidString(value *uuid.UUID) string {
	if value == nil {
		return ""
	}
	return value.String()
}

// SearchMessagesGlobal выполняет глобальный поиск по всем доступным пользователю чатам
func (s *SearchService) SearchMessagesGlobal(ctx context.Context, userID int64, query string, filters map[string]interface{}, limit, offset int) ([]*domain.MessageView, int, error) {
	query = strings.TrimSpace(query)
	queryLength := utf8.RuneCountInString(query)
	if queryLength < 2 {
		return nil, 0, apperrors.BadRequest("search query must be at least 2 characters")
	}
	if queryLength > 256 {
		return nil, 0, apperrors.BadRequest("search query must not exceed 256 characters")
	}

	if limit <= 0 || limit > maxMessageSearchLimit {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	if offset > 10000 {
		return nil, 0, apperrors.BadRequest("search offset must not exceed 10000")
	}

	messages, totalCount, err := s.messageRepo.SearchMessagesGlobal(ctx, userID, query, filters, limit, offset)
	if err != nil {
		return nil, 0, apperrors.InternalWithErr("failed to search messages globally", err)
	}

	s.logger.Info("Global message search completed",
		zap.Int64("user_id", userID),
		zap.Int("query_length", queryLength),
		zap.Int("results", len(messages)),
		zap.Int("total_count", totalCount),
	)

	return messages, totalCount, nil
}

// SearchAttachments ищет вложения в доступных пользователю чатах
func (s *SearchService) SearchAttachments(ctx context.Context, userID int64, filters map[string]interface{}, limit, offset int) ([]*domain.MessageView, int, error) {
	if limit <= 0 || limit > maxMessageSearchLimit {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	if offset > 10000 {
		return nil, 0, apperrors.BadRequest("search offset must not exceed 10000")
	}
	if query, ok := filters["query"].(string); ok && utf8.RuneCountInString(strings.TrimSpace(query)) > 256 {
		return nil, 0, apperrors.BadRequest("attachment query must not exceed 256 characters")
	}

	attachments, totalCount, err := s.messageRepo.SearchAttachments(ctx, userID, filters, limit, offset)
	if err != nil {
		return nil, 0, apperrors.InternalWithErr("failed to search attachments", err)
	}

	s.logger.Info("Attachment search completed",
		zap.Int64("user_id", userID),
		zap.Int("results", len(attachments)),
		zap.Int("total_count", totalCount),
	)

	return attachments, totalCount, nil
}

// GetAttachmentStats возвращает статистику по вложениям в чате
func (s *SearchService) GetAttachmentStats(ctx context.Context, chatID uuid.UUID, userID int64) (map[string]int, int64, error) {
	stats, totalSize, err := s.messageRepo.GetAttachmentStats(ctx, chatID, userID)
	if err != nil {
		if errors.Is(err, repository.ErrMessageSearchAccessDenied) {
			return nil, 0, apperrors.Forbidden("you do not have access to this chat")
		}
		return nil, 0, apperrors.InternalWithErr("failed to get attachment stats", err)
	}

	s.logger.Info("Attachment stats retrieved",
		zap.Int64("user_id", userID),
		zap.String("chat_id", chatID.String()),
		zap.Int("total_files", len(stats)),
	)

	return stats, totalSize, nil
}
