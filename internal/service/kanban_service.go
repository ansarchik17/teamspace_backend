package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"github.com/west6ide/workspace-backend/internal/websocket"
	"go.uber.org/zap"
)

type EventPublisher interface {
	Publish(ctx context.Context, routingKey string, data any) error
}

type KanbanService struct {
	odoCommands    *ODOCommandClient
	boardRepo      *repository.BoardRepository
	taskRepo       *repository.TaskRepository
	userRepo       *repository.UserRepository
	driveRepo      *repository.DriveRepository
	notifService   NotificationSender
	fileService    *FileService
	gamification   *GamificationService // новое поле
	eventPublisher EventPublisher
	logger         *zap.Logger
	realtime       *kanbanRealtimeNotifier
}

const (
	kanbanTaskCreatedNotificationType  = "kanban_task_created"
	kanbanTaskAssignedNotificationType = "kanban_task_assigned"
)

func NewKanbanService(
	boardRepo *repository.BoardRepository,
	taskRepo *repository.TaskRepository,
	userRepo *repository.UserRepository,
	driveRepo *repository.DriveRepository,
	notifService NotificationSender,
	fileService *FileService,
	gamification *GamificationService,
	eventPublisher EventPublisher,
	logger *zap.Logger,
) *KanbanService {
	return &KanbanService{
		boardRepo:      boardRepo,
		taskRepo:       taskRepo,
		userRepo:       userRepo,
		driveRepo:      driveRepo,
		notifService:   notifService,
		fileService:    fileService,
		gamification:   gamification,
		eventPublisher: eventPublisher,
		logger:         logger.With(zap.String("service", "kanban")),
	}
}

type kanbanTaskCreatedEventData struct {
	TaskID      uuid.UUID  `json:"task_id"`
	BoardID     uuid.UUID  `json:"board_id"`
	GoalID      *uuid.UUID `json:"goal_id,omitempty"`
	ProjectID   *uuid.UUID `json:"project_id,omitempty"`
	SprintID    *uuid.UUID `json:"sprint_id,omitempty"`
	ColumnID    uuid.UUID  `json:"column_id"`
	CreatedBy   int64      `json:"created_by"`
	AssigneeIDs []int64    `json:"assignee_ids,omitempty"`
	Priority    string     `json:"priority"`
	Title       string     `json:"title"`
}

type CreateBoardRequest struct {
	Name string `json:"name" binding:"required"`
}

type UpdateBoardRequest struct {
	Name string `json:"name" binding:"required"`
}

type MemberItem struct {
	UserID   int64  `json:"user_id" binding:"required"`
	Role     string `json:"role"` // Можно добавить дефолтное значение, если пусто
	TeamRole string `json:"team_role,omitempty"`
}

// AddMembersRequest теперь содержит список
type AddMembersRequest struct {
	Members []MemberItem `json:"members" binding:"required,dive"`
}

type CreateSprintRequest struct {
	Name      string     `json:"name" binding:"required"`
	GoalID    *uuid.UUID `json:"goal_id"`
	ProjectID *uuid.UUID `json:"project_id"`
	Status    string     `json:"status"`
	StartDate *time.Time `json:"start_date"`
	EndDate   *time.Time `json:"end_date"`
}

type CreateGoalRequest struct {
	Name        string  `json:"name" binding:"required"`
	Description *string `json:"description"`
}

type UpdateGoalRequest struct {
	Name        string  `json:"name" binding:"required"`
	Description *string `json:"description"`
}

type CreateProjectRequest struct {
	GoalID      *uuid.UUID `json:"goal_id"`
	Name        string     `json:"name" binding:"required"`
	Description *string    `json:"description"`
	Status      string     `json:"status"`
}

type UpdateProjectRequest struct {
	GoalID      *uuid.UUID `json:"goal_id"`
	Name        string     `json:"name" binding:"required"`
	Description *string    `json:"description"`
	Status      string     `json:"status"`
}

type UpdateSprintRequest struct {
	Name      string     `json:"name" binding:"required"`
	GoalID    *uuid.UUID `json:"goal_id"`
	ProjectID *uuid.UUID `json:"project_id"`
	Status    string     `json:"status"`
	StartDate *time.Time `json:"start_date"`
	EndDate   *time.Time `json:"end_date"`
}

type CreateTaskRequest struct {
	BoardID             uuid.UUID   `json:"board_id" binding:"required"`
	GoalID              *uuid.UUID  `json:"goal_id"`
	ProjectID           *uuid.UUID  `json:"project_id"`
	SprintID            *uuid.UUID  `json:"sprint_id"`
	Title               string      `json:"title" binding:"required"`
	Description         *string     `json:"description"`
	AssigneeUserIDs     []int64     `json:"assignee_ids"`
	AssigneeUserID      *int64      `json:"assignee_id,omitempty"`
	Priority            string      `json:"priority"`
	OriginalEstimateSec int         `json:"original_estimate_sec"`
	DueAt               *time.Time  `json:"due_at"`
	AttachmentFileIDs   []uuid.UUID `json:"attachment_file_ids"`
	ParentID            *uuid.UUID  `json:"parent_id"`
}

type DocumentologAssignmentUpsertRequest struct {
	OrganizationID string `json:"organization_id"`
	IdentityIssuer string `json:"identity_issuer"`
	SourceSystem   string `json:"source_system"`
	SchemaVersion  int    `json:"schema_version"`
	AssignmentID   string `json:"assignment_id" binding:"required"`
	DocumentID     string `json:"document_id" binding:"required"`
	AuthorSub      string `json:"author_sub" binding:"required"`
	AssigneeSub    string `json:"assignee_sub" binding:"required"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	Priority       string `json:"priority"`
	DueDate        string `json:"due_date"`
	DepartmentID   string `json:"department_id"`
	SourceURL      string `json:"source_url"`
	Status         string `json:"status"`
	BoardID        string `json:"board_id"`
}

type MoveTaskRequest struct {
	ToColumnID uuid.UUID `json:"to_column_id" binding:"required"`
}

type CreateColumnRequest struct {
	BoardID uuid.UUID `json:"board_id" binding:"required"`
	Name    string    `json:"name" binding:"required"`
	Key     string    `json:"key"`
}

type SetBoardPresetRequest struct {
	PresetID string `json:"preset_id" binding:"required"`
}

type UpdateMemberRoleRequest struct {
	Role string `json:"role" binding:"required"`
}

type UpdateMemberTeamRoleRequest struct {
	TeamRole string `json:"team_role"`
}

type AttachFileRequest struct {
	FileID uuid.UUID `json:"file_id" binding:"required"`
}

type SearchTasksRequest struct {
	AuthorIDs   []int64    `form:"author_ids" json:"author_ids"`
	Statuses    []string   `form:"statuses" json:"statuses"`
	Status      string     `form:"status" json:"status"`
	Query       string     `form:"q" json:"q"`
	AssigneeIDs []int64    `form:"assignee_ids" json:"assignee_ids"`
	Priorities  []string   `form:"priorities" json:"priorities"`
	ColumnIDs   []string   `form:"column_ids" json:"column_ids"`
	DateFrom    *time.Time `form:"date_from" json:"date_from"`
	DateTo      *time.Time `form:"date_to" json:"date_to"`
	BoardID     *string    `form:"board_id" json:"board_id"`
	ColumnKeys  []string   `form:"column_keys" json:"column_keys"`
	LabelIDs    []string   `form:"label_ids" json:"label_ids"`
	SprintIDs   []string   `form:"sprint_ids" json:"sprint_ids"`
	SprintID    *string    `form:"sprint_id" json:"sprint_id"`
	Limit       int        `form:"limit" json:"limit"`
	Offset      int        `form:"offset" json:"offset"`
}

type CreateCommentRequest struct {
	Content       string  `json:"content"`
	AttachmentURL *string `json:"attachment_url,omitempty"`
}

type UpdateCommentRequest struct {
	Content string `json:"content" binding:"required"`
}

type UpdateTaskStatusRequest struct {
	Status string `json:"status" binding:"required"`
}

type CreateLabelRequest struct {
	Name  string `json:"name" binding:"required"`
	Color string `json:"color" binding:"required"`
}

type AddLabelToTaskRequest struct {
	LabelID uuid.UUID `json:"label_id" binding:"required"`
}

type AssistantCreateTaskRequest struct {
	BoardID     uuid.UUID  `json:"board_id" binding:"required"`
	GoalID      *uuid.UUID `json:"goal_id,omitempty"`
	ProjectID   *uuid.UUID `json:"project_id,omitempty"`
	SprintID    *uuid.UUID `json:"sprint_id,omitempty"`
	Title       string     `json:"title" binding:"required"`
	Description *string    `json:"description"`
	AssigneeID  *int64     `json:"assignee_id,omitempty"`
	Priority    string     `json:"priority"`
	DueAt       *time.Time `json:"due_at,omitempty"`
}

type AssistantAssignTaskRequest struct {
	AssigneeID int64 `json:"assignee_id" binding:"required"`
}

type AssistantBoardMemberRequest struct {
	UserID   int64   `json:"user_id" binding:"required"`
	Role     string  `json:"role"`
	TeamRole *string `json:"team_role,omitempty"`
}

func (s *KanbanService) GetMyTaskBoardsSummary(ctx context.Context, userID int64) (*domain.UserTaskBoardsSummaryResponse, error) {
	return s.GetMyTaskBoardsSummaryFiltered(ctx, userID, nil, nil)
}

func (s *KanbanService) GetMyTaskBoardsSummaryFiltered(
	ctx context.Context,
	userID int64,
	dateFrom *time.Time,
	dateTo *time.Time,
) (*domain.UserTaskBoardsSummaryResponse, error) {
	items, err := s.taskRepo.GetUserTaskBoardsSummaryFiltered(ctx, userID, dateFrom, dateTo)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось получить агрегированную сводку задач", err)
	}

	resp := &domain.UserTaskBoardsSummaryResponse{
		UserID:      userID,
		TotalBoards: len(items),
		Boards:      items,
	}

	for _, item := range items {
		resp.TotalTasks += item.TaskCount
	}

	return resp, nil
}

func (s *KanbanService) getEffectiveRole(ctx context.Context, boardID uuid.UUID, userID int64) (string, error) {
	b, err := s.boardRepo.GetBoard(ctx, boardID)
	if err != nil {
		return "", apperrors.NotFound("Доска не найдена")
	}
	// ODO projection is exposed only through the company/assignee scoped API.
	// Board ownership or retained membership cannot expose another assignment
	// or turn a local Kanban mutation into a source-system command.
	if b.SystemKey == "documentolog-assignments" || strings.HasPrefix(b.SystemKey, "documentolog-assignments:") {
		return "", apperrors.Forbidden("use the ODO assignment API")
	}
	if b.OwnerID == userID {
		return domain.BoardRoleOwner, nil
	}
	role, err := s.boardRepo.GetBoardMemberRole(ctx, boardID, userID)
	if err != nil {
		// fallback to general access (e.g. leadership access) as "member"
		ok, _ := s.boardRepo.HasKanbanAccess(ctx, boardID, userID)
		if ok {
			return domain.BoardRoleMember, nil
		}
		return "", apperrors.Forbidden("У вас нет доступа к этой доске")
	}
	switch role {
	case domain.BoardRoleAdmin, domain.BoardRoleEditor, domain.BoardRoleViewer, domain.BoardRoleMember:
		return role, nil
	default:
		return "", apperrors.Forbidden("Неизвестная роль участника доски")
	}
}

func (s *KanbanService) checkAccess(ctx context.Context, boardID uuid.UUID, userID int64) error {
	_, err := s.getEffectiveRole(ctx, boardID, userID)
	return err
}

func (s *KanbanService) checkAccessModifyBoard(ctx context.Context, boardID uuid.UUID, userID int64) error {
	role, err := s.getEffectiveRole(ctx, boardID, userID)
	if err != nil {
		return err
	}
	if role != domain.BoardRoleOwner && role != domain.BoardRoleAdmin {
		return apperrors.Forbidden("Требуется роль Admin")
	}
	return nil
}
func (s *KanbanService) checkAccessManageBoardMembers(ctx context.Context, boardID uuid.UUID, userID int64) error {
	return s.checkAccessModifyBoard(ctx, boardID, userID)
}
func (s *KanbanService) checkAccessEditTask(ctx context.Context, boardID uuid.UUID, userID int64) error {
	role, err := s.getEffectiveRole(ctx, boardID, userID)
	if err != nil {
		return err
	}
	if role != domain.BoardRoleOwner && role != domain.BoardRoleAdmin && role != domain.BoardRoleEditor {
		return apperrors.Forbidden("Требуется роль Editor или Admin")
	}
	return nil
}
func (s *KanbanService) checkAccessDeleteTask(ctx context.Context, boardID uuid.UUID, userID int64) error {
	return s.checkAccessEditTask(ctx, boardID, userID)
}

// Legacy member means read-only viewer. Owner cannot be assigned through membership APIs.
func normalizeBoardMemberRole(role string) (string, error) {
	role = strings.ToLower(strings.TrimSpace(role))
	switch role {
	case domain.BoardRoleViewer, domain.BoardRoleEditor, domain.BoardRoleAdmin, domain.BoardRoleMember:
		return role, nil
	}
	return "", apperrors.BadRequest("Допустимые роли: viewer, editor, admin (member — прежнее имя viewer)")
}

func trimOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func normalizeTaskColumnKey(columnKey, status string) string {
	value := strings.TrimSpace(strings.ToUpper(columnKey))
	if value == "" {
		value = strings.TrimSpace(strings.ToUpper(status))
	}

	switch value {
	case domain.ColTODO, "TO DO", domain.StatusTodo:
		return domain.ColTODO
	case domain.ColInProgress, "IN PROGRESS", domain.StatusWorking:
		return domain.ColInProgress
	case domain.ColTest, "TESTING", domain.StatusReview, "ТЕСТИРОВАНИЕ":
		return domain.ColTest
	default:
		return value
	}
}

func (s *KanbanService) validateTaskRelations(ctx context.Context, boardID uuid.UUID, goalID, projectID, sprintID *uuid.UUID) error {
	var goal *domain.Goal
	if goalID != nil && *goalID != uuid.Nil {
		var err error
		goal, err = s.boardRepo.GetGoal(ctx, *goalID)
		if err != nil || goal == nil {
			return apperrors.BadRequest("Цель не найдена")
		}
		if goal.BoardID != boardID {
			return apperrors.BadRequest("Цель принадлежит другой доске")
		}
	}

	if projectID != nil && *projectID != uuid.Nil {
		project, err := s.boardRepo.GetProject(ctx, *projectID)
		if err != nil || project == nil {
			return apperrors.BadRequest("Проект не найден")
		}
		if project.BoardID != boardID {
			return apperrors.BadRequest("Проект принадлежит другой доске")
		}
		if goal != nil && project.GoalID != nil && *project.GoalID != goal.ID {
			return apperrors.BadRequest("Проект не относится к выбранной цели")
		}
	}

	if sprintID != nil && *sprintID != uuid.Nil {
		sprint, err := s.boardRepo.GetSprint(ctx, *sprintID)
		if err != nil || sprint == nil {
			return apperrors.BadRequest("Спринт не найден")
		}
		if sprint.BoardID != boardID {
			return apperrors.BadRequest("Спринт принадлежит другой доске")
		}
	}

	return nil
}

func (s *KanbanService) GetMyTodoTasks(ctx context.Context, userID int64) ([]*domain.Task, error) {
	tasks, err := s.taskRepo.GetTasksByUserAndColumnKeys(ctx, userID, []string{domain.ColTODO})
	if err != nil {
		return nil, apperrors.InternalWithErr("Ошибка получения TODO задач: "+err.Error(), err)
	}
	return tasks, nil
}

// ListMyDocumentologAssignments returns assignments that were received from
// Documentolog and are explicitly assigned to the current user.
func (s *KanbanService) ListMyDocumentologAssignments(ctx context.Context, userID int64) ([]*domain.Task, error) {
	if _, err := s.taskRepo.ODOReaderOrganization(ctx, userID); err != nil {
		if apperrors.IsAppError(err) {
			return nil, err
		}
		return nil, apperrors.InternalWithErr("Не удалось проверить доступ к ЭДО", err)
	}
	tasks, err := s.taskRepo.ListDocumentologAssignmentsByAssignee(ctx, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("Ошибка получения поручений: "+err.Error(), err)
	}
	if tasks == nil {
		tasks = []*domain.Task{}
	}
	return tasks, nil
}

func (s *KanbanService) GetMyTasksByStatus(ctx context.Context, userID int64) (*domain.MyTasksByStatusResponse, error) {
	tasks, err := s.taskRepo.GetTasksByUserAndColumnKeys(ctx, userID, []string{
		domain.ColTODO,
		domain.ColInProgress,
		domain.ColTest,
	})
	if err != nil {
		return nil, apperrors.InternalWithErr("Ошибка получения моих задач: "+err.Error(), err)
	}

	resp := &domain.MyTasksByStatusResponse{
		UserID:     userID,
		Todo:       make([]*domain.Task, 0),
		InProgress: make([]*domain.Task, 0),
		Test:       make([]*domain.Task, 0),
	}

	for _, task := range tasks {
		if task == nil {
			continue
		}

		resp.TotalTasks++

		switch normalizeTaskColumnKey(task.ColumnKey, task.Status) {
		case domain.ColTODO:
			resp.Todo = append(resp.Todo, task)
			resp.TodoCount++
		case domain.ColInProgress:
			resp.InProgress = append(resp.InProgress, task)
			resp.InProgressCount++
		case domain.ColTest:
			resp.Test = append(resp.Test, task)
			resp.TestCount++
		}
	}

	return resp, nil
}

func (s *KanbanService) ListUserBoards(ctx context.Context, userID int64) ([]*domain.Board, error) {
	boards, err := s.boardRepo.ListBoards(ctx, userID)
	if err != nil {
		return nil, err
	}

	for _, board := range boards {
		board.Progress.CompletionPercent = computeBoardCompletionPercent(
			board.ManualCompletionPercent,
			board.Progress.CompletedTasks,
			board.Progress.TotalTasks,
		)
	}

	return boards, nil
}

func computeBoardCompletionPercent(manualCompletionPercent *int, completedTasks, totalTasks int) int {
	if manualCompletionPercent != nil {
		if *manualCompletionPercent < 0 {
			return 0
		}
		if *manualCompletionPercent > 100 {
			return 100
		}
		return *manualCompletionPercent
	}

	if totalTasks <= 0 {
		return 0
	}

	percent := int(math.Round(float64(completedTasks) * 100 / float64(totalTasks)))
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}

	return percent
}

func (s *KanbanService) GetBoardTasksByUser(ctx context.Context, userID int64, boardID uuid.UUID, dateFrom, dateTo *time.Time) (*domain.BoardTasksByUserResponse, error) {
	if err := s.checkAccess(ctx, boardID, userID); err != nil {
		return nil, err
	}

	items, err := s.taskRepo.ListBoardTasksByUser(ctx, boardID, dateFrom, dateTo)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось получить задачи доски по пользователям", err)
	}

	return &domain.BoardTasksByUserResponse{Data: items}, nil
}

func (s *KanbanService) GetAssistantBoardAnalysis(ctx context.Context, userID int64, boardID uuid.UUID, assigneeID *int64, dateFrom, dateTo *time.Time) (*domain.AssistantBoardTasksResponse, error) {
	if err := s.checkAccess(ctx, boardID, userID); err != nil {
		return nil, err
	}

	items, err := s.taskRepo.ListAssistantBoardTasks(ctx, boardID, assigneeID, dateFrom, dateTo)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось получить lightweight-анализ доски", err)
	}

	return &domain.AssistantBoardTasksResponse{Data: items}, nil
}

func (s *KanbanService) GetAssistantBoardMyTasks(ctx context.Context, userID int64, boardID uuid.UUID, dateFrom, dateTo *time.Time) (*domain.AssistantBoardTasksResponse, error) {
	return s.GetAssistantBoardAnalysis(ctx, userID, boardID, &userID, dateFrom, dateTo)
}

func (s *KanbanService) CreateAssistantTask(ctx context.Context, userID int64, req AssistantCreateTaskRequest) (*domain.AssistantTask, error) {
	createReq := CreateTaskRequest{
		BoardID:     req.BoardID,
		GoalID:      req.GoalID,
		ProjectID:   req.ProjectID,
		SprintID:    req.SprintID,
		Title:       strings.TrimSpace(req.Title),
		Description: req.Description,
		Priority:    strings.TrimSpace(req.Priority),
		DueAt:       req.DueAt,
	}
	if req.AssigneeID != nil {
		createReq.AssigneeUserID = req.AssigneeID
	}

	task, err := s.CreateTask(ctx, userID, createReq)
	if err != nil {
		return nil, err
	}

	return &domain.AssistantTask{
		ID:        task.ID,
		Number:    task.TaskNumber,
		Title:     task.Title,
		Status:    task.Status,
		CreatedAt: task.CreatedAt,
	}, nil
}

func (s *KanbanService) AssignAssistantTask(ctx context.Context, userID int64, taskID uuid.UUID, assigneeID int64) (*domain.AssistantTask, error) {
	updated, err := s.UpdateTask(ctx, userID, taskID, &domain.UpdateTaskPatch{
		AssigneeIDs: []int64{assigneeID},
	})
	if err != nil {
		return nil, err
	}

	return &domain.AssistantTask{
		ID:        updated.ID,
		Number:    updated.TaskNumber,
		Title:     updated.Title,
		Status:    updated.Status,
		CreatedAt: updated.CreatedAt,
	}, nil
}

func (s *KanbanService) UpdateAssistantTaskStatus(ctx context.Context, userID int64, taskID uuid.UUID, status string) (*domain.AssistantTask, error) {
	updated, err := s.UpdateTaskStatus(ctx, userID, taskID, UpdateTaskStatusRequest{Status: status})
	if err != nil {
		return nil, err
	}

	return &domain.AssistantTask{
		ID:        updated.ID,
		Number:    updated.TaskNumber,
		Title:     updated.Title,
		Status:    updated.Status,
		CreatedAt: updated.CreatedAt,
	}, nil
}

func (s *KanbanService) AddAssistantBoardMember(ctx context.Context, userID int64, boardID uuid.UUID, req AssistantBoardMemberRequest) error {
	role := strings.TrimSpace(req.Role)
	if role == "" {
		role = domain.BoardRoleMember
	}

	return s.AddMembers(ctx, userID, boardID, AddMembersRequest{
		Members: []MemberItem{{
			UserID:   req.UserID,
			Role:     role,
			TeamRole: strings.TrimSpace(derefString(req.TeamRole)),
		}},
	})
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *KanbanService) CreateBoard(ctx context.Context, userID int64, req CreateBoardRequest) (*domain.Board, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return nil, apperrors.BadRequest("Название доски не может быть пустым")
	}
	if len(req.Name) > 255 {
		return nil, apperrors.BadRequest("Название доски не должно быть длиннее 255 символов")
	}

	b := &domain.Board{
		OwnerID:  userID,
		Name:     req.Name,
		BgMode:   domain.BgModePresetColor,
		BgPreset: domain.BgPresetDefault,
	}
	if err := s.boardRepo.CreateBoard(ctx, b); err != nil {
		return nil, apperrors.InternalWithErr("Не удалось создать доску", err)
	}
	// Гамификация: создание доски
	if s.gamification != nil {
		go func() { _ = s.gamification.TrackActivity(context.Background(), userID, domain.ActionBoardCreated) }()
	}
	return b, nil
}

func (s *KanbanService) UpdateBoard(ctx context.Context, userID int64, boardID uuid.UUID, req UpdateBoardRequest) (*domain.Board, error) {
	if err := s.checkAccessModifyBoard(ctx, boardID, userID); err != nil {
		return nil, err
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return nil, apperrors.BadRequest("Название доски не может быть пустым")
	}
	if len(req.Name) > 255 {
		return nil, apperrors.BadRequest("Название доски не должно быть длиннее 255 символов")
	}

	if err := s.boardRepo.UpdateBoard(ctx, boardID, req.Name); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "board not found") {
			return nil, apperrors.NotFound("Доска не найдена")
		}
		return nil, apperrors.InternalWithErr("Не удалось обновить доску", err)
	}

	board, err := s.boardRepo.GetBoard(ctx, boardID)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось получить обновленную доску", err)
	}

	return board, nil
}

func (s *KanbanService) DeleteBoard(ctx context.Context, userID int64, boardID uuid.UUID) error {
	if err := s.checkAccessManageBoardMembers(ctx, boardID, userID); err != nil {
		return err
	}

	board, err := s.boardRepo.GetBoard(ctx, boardID)
	if err != nil {
		return apperrors.NotFound("Р”РѕСЃРєР° РЅРµ РЅР°Р№РґРµРЅР°")
	}

	tasks, err := s.taskRepo.ListByBoard(ctx, boardID, nil)
	if err != nil {
		return apperrors.InternalWithErr("РќРµ СѓРґР°Р»РѕСЃСЊ РїРѕР»СѓС‡РёС‚СЊ Р·Р°РґР°С‡Рё РґРѕСЃРєРё РїРµСЂРµРґ СѓРґР°Р»РµРЅРёРµРј", err)
	}

	if s.fileService != nil {
		if board.BgURL != nil {
			if err := s.fileService.DeleteKanbanFile(ctx, *board.BgURL); err != nil {
				s.logger.Warn("failed to delete board background file before board removal", zap.String("board_id", boardID.String()), zap.String("object", *board.BgURL), zap.Error(err))
			}
		}

		for _, task := range tasks {
			for _, att := range task.Attachments {
				if att == nil || att.ObjectName == "" {
					continue
				}
				if err := s.fileService.DeleteKanbanFile(ctx, att.ObjectName); err != nil {
					s.logger.Warn("failed to delete task attachment before board removal", zap.String("board_id", boardID.String()), zap.String("task_id", task.ID.String()), zap.String("attachment_id", att.ID.String()), zap.String("object", att.ObjectName), zap.Error(err))
				}
			}
		}
	}

	if err := s.boardRepo.DeleteBoard(ctx, boardID, userID); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "board not found") {
			return apperrors.NotFound("Р”РѕСЃРєР° РЅРµ РЅР°Р№РґРµРЅР°")
		}
		return apperrors.InternalWithErr("РќРµ СѓРґР°Р»РѕСЃСЊ СѓРґР°Р»РёС‚СЊ РґРѕСЃРєСѓ", err)
	}

	return nil
}

func (s *KanbanService) GetBoardDetails(ctx context.Context, userID int64, boardID uuid.UUID) (*domain.BoardResponse, error) {
	if err := s.checkKanbanAccess(ctx, boardID, userID); err != nil {
		return nil, err
	}

	b, err := s.boardRepo.GetBoard(ctx, boardID)
	if err != nil {
		return nil, apperrors.NotFound("Доска не найдена")
	}

	cols, err := s.boardRepo.ListColumns(ctx, boardID)
	if err != nil {
		return nil, apperrors.Internal("Не удалось получить список колонок")
	}

	sprints, err := s.boardRepo.ListSprints(ctx, boardID)
	if err != nil {
		return nil, apperrors.Internal("Не удалось получить список спринтов")
	}

	goals, err := s.boardRepo.ListGoals(ctx, boardID)
	if err != nil {
		return nil, apperrors.Internal("Не удалось получить список целей")
	}

	projects, err := s.boardRepo.ListProjects(ctx, boardID)
	if err != nil {
		return nil, apperrors.Internal("Не удалось получить список проектов")
	}

	members, err := s.boardRepo.GetMembers(ctx, boardID)
	if err != nil {
		return nil, apperrors.Internal("Не удалось получить список участников")
	}

	requester, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, apperrors.NotFound("Пользователь не найден")
	}

	_ = requester

	tasks, err := s.taskRepo.ListByBoardTopTasks(ctx, boardID, 10)
	if err != nil {
		return nil, apperrors.Internal("Не удалось получить список задач")
	}

	allTasks, err := s.taskRepo.ListByBoard(ctx, boardID, nil)
	if err != nil {
		return nil, apperrors.Internal("Не удалось получить задачи доски для аналитики целей")
	}

	counts, err := s.taskRepo.GetColumnTaskCounts(ctx, boardID)
	if err != nil {
		s.logger.Warn("Failed to fetch column task counts, using loaded lengths as fallback", zap.Error(err))
	}

	colMap := make(map[uuid.UUID][]*domain.Task)
	taskMap := make(map[uuid.UUID]*domain.Task)

	// First pass: map tasks and filter out subtasks for initial column grouping
	for _, t := range tasks {
		taskMap[t.ID] = t
	}

	// Second pass: nest subtasks
	var rootTasks []*domain.Task
	for _, t := range tasks {
		if t.ParentID != nil {
			if parent, ok := taskMap[*t.ParentID]; ok {
				parent.Subtasks = append(parent.Subtasks, t)
				continue // Don't add to column map directly
			}
		}
		rootTasks = append(rootTasks, t)
	}

	for _, t := range rootTasks {
		colMap[t.ColumnID] = append(colMap[t.ColumnID], t)
	}

	colResp := make([]*domain.ColumnWithTasks, len(cols))
	for i, c := range cols {
		colResp[i] = &domain.ColumnWithTasks{
			Column:     c,
			Tasks:      colMap[c.ID],
			TotalTasks: counts[c.ID],
		}
	}

	var imageUrl *string
	if b.BgURL != nil {
		url := fmt.Sprintf("/api/v1/kanban/boards/%s/background/image/download", boardID)
		imageUrl = &url
	}

	bg := &domain.BoardBackground{
		Mode:      b.BgMode,
		PresetID:  b.BgPreset,
		PresetHex: domain.GetPresetHex(b.BgPreset),
		ImageURL:  imageUrl,
	}

	populateGoalInsights(goals, projects, allTasks, time.Now())

	return &domain.BoardResponse{
		Board:      b,
		Background: bg,
		Goals:      goals,
		Projects:   projects,
		Sprints:    sprints,
		Columns:    colResp,
		Members:    members,
	}, nil
}

func populateGoalInsights(goals []*domain.Goal, projects []*domain.Project, tasks []*domain.Task, now time.Time) {
	projectTaskCounts := make(map[uuid.UUID]int, len(projects))

	for _, task := range tasks {
		if task.ProjectID != nil {
			projectTaskCounts[*task.ProjectID]++
		}
	}

	for _, goal := range goals {
		if goal == nil {
			continue
		}

		goalProjectCount := 0
		topProjectName := ""
		topProjectTaskCount := 0
		for _, project := range projects {
			if project == nil || project.GoalID == nil || *project.GoalID != goal.ID {
				continue
			}
			goalProjectCount++
			if count := projectTaskCounts[project.ID]; count > topProjectTaskCount {
				topProjectTaskCount = count
				topProjectName = project.Name
			}
		}

		sprintIDs := make(map[uuid.UUID]struct{})
		totalTasks := 0
		completedTasks := 0
		activeTasks := 0
		overdueTasks := 0

		for _, task := range tasks {
			if task == nil || task.GoalID == nil || *task.GoalID != goal.ID {
				continue
			}

			totalTasks++
			status := normalizeKanbanTaskStatus(task.Status)
			if status == domain.ColDone {
				completedTasks++
			}
			if status == domain.ColInProgress || status == domain.ColTest {
				activeTasks++
			}
			if task.SprintID != nil {
				sprintIDs[*task.SprintID] = struct{}{}
			}
			if task.DueAt != nil && status != domain.ColDone && task.DueAt.Before(now) {
				overdueTasks++
			}
		}

		completionPercent := 0
		if totalTasks > 0 {
			completionPercent = int(math.Round(float64(completedTasks*100) / float64(totalTasks)))
		}

		predictionLabel := "Нужна структура"
		switch {
		case completionPercent >= 75:
			predictionLabel = "Идёт по плану"
		case activeTasks > 0 || completionPercent >= 35:
			predictionLabel = "В рабочем темпе"
		case overdueTasks > 0:
			predictionLabel = "Требует внимания"
		}

		nextActionLabel := "Добавьте первую задачу"
		switch {
		case topProjectName != "":
			nextActionLabel = topProjectName
		case goalProjectCount > 0:
			nextActionLabel = "Связать задачи с проектами"
		case totalTasks > 0:
			nextActionLabel = "Сгруппировать задачи по проектам"
		}

		goal.TaskCount = totalTasks
		goal.CompletedTasks = completedTasks
		goal.ActiveTasks = activeTasks
		goal.OverdueTasks = overdueTasks
		goal.ProjectCount = goalProjectCount
		goal.SprintCount = len(sprintIDs)
		goal.CompletionPercent = completionPercent
		goal.TopProjectName = topProjectName
		goal.PredictionLabel = predictionLabel
		goal.NextActionLabel = nextActionLabel
	}
}

func normalizeKanbanTaskStatus(status string) string {
	s := strings.ToUpper(strings.TrimSpace(status))

	switch s {
	case domain.ColDone, domain.StatusDone:
		return domain.ColDone
	case domain.ColInProgress, domain.StatusWorking:
		return domain.ColInProgress
	case domain.ColTest, domain.StatusReview, "REVIEW", "ПРОВЕРКА":
		return domain.ColTest
	case domain.ColTODO, domain.StatusTodo:
		return domain.ColTODO
	default:
		return s
	}
}

func normalizeKanbanColumnKey(name, key string) string {
	trimmedKey := strings.TrimSpace(key)
	if trimmedKey == "" {
		trimmedKey = name
	}

	normalizedKey := normalizeKanbanTaskStatus(trimmedKey)
	switch normalizedKey {
	case domain.ColTODO, domain.ColInProgress, domain.ColTest, domain.ColDone:
		return normalizedKey
	default:
		return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(trimmedKey), " ", "_"))
	}
}

func (s *KanbanService) GetBoard(ctx context.Context, userID int64, boardID uuid.UUID) (*domain.BoardResponse, error) {
	return s.GetBoardDetails(ctx, userID, boardID)
}

func (s *KanbanService) AddMembers(ctx context.Context, userID int64, boardID uuid.UUID, req AddMembersRequest) error {
	if len(req.Members) == 0 {
		return nil
	}

	if err := s.checkAccessManageBoardMembers(ctx, boardID, userID); err != nil {
		return err
	}

	members := make([]*domain.BoardMember, 0, len(req.Members))
	for _, item := range req.Members {
		// Scope по иерархии
		// if err := ensureUserScopedAccess(ctx, s.userRepo, userID, item.UserID); err != nil {
		// 	return err
		// }
		var teamRole *string
		if trimmed := strings.TrimSpace(item.TeamRole); trimmed != "" {
			teamRole = &trimmed
		}
		role, err := normalizeBoardMemberRole(item.Role)
		if err != nil {
			return err
		}
		if item.UserID <= 0 {
			return apperrors.BadRequest("Некорректный ID участника")
		}
		members = append(members, &domain.BoardMember{
			BoardID:  boardID,
			UserID:   item.UserID,
			Role:     role,
			TeamRole: teamRole,
		})
	}

	return s.boardRepo.AddMembers(ctx, members)
}

func (s *KanbanService) checkManageMember(ctx context.Context, userID int64, boardID uuid.UUID, targetUserID int64) error {
	if err := s.checkAccessManageBoardMembers(ctx, boardID, userID); err != nil {
		return err
	}
	board, err := s.boardRepo.GetBoard(ctx, boardID)
	if err != nil {
		return apperrors.NotFound("Доска не найдена")
	}
	if targetUserID == board.OwnerID {
		return apperrors.Forbidden("Нельзя удалить владельца доски или изменить его роль")
	}
	return nil
}
func (s *KanbanService) RemoveMember(ctx context.Context, userID int64, boardID uuid.UUID, targetUserID int64) error {
	if err := s.checkManageMember(ctx, userID, boardID, targetUserID); err != nil {
		return err
	}
	return s.boardRepo.RemoveMember(ctx, boardID, targetUserID)
}
func (s *KanbanService) UpdateMemberRole(ctx context.Context, userID int64, boardID uuid.UUID, targetUserID int64, role string) error {
	if err := s.checkManageMember(ctx, userID, boardID, targetUserID); err != nil {
		return err
	}
	role, err := normalizeBoardMemberRole(role)
	if err != nil {
		return err
	}
	return s.boardRepo.UpdateMemberRole(ctx, boardID, targetUserID, role)
}
func (s *KanbanService) UpdateMemberTeamRole(ctx context.Context, userID int64, boardID uuid.UUID, targetUserID int64, teamRole string) error {
	if err := s.checkManageMember(ctx, userID, boardID, targetUserID); err != nil {
		return err
	}
	var normalized *string
	if trimmed := strings.TrimSpace(teamRole); trimmed != "" {
		normalized = &trimmed
	}
	return s.boardRepo.UpdateMemberTeamRole(ctx, boardID, targetUserID, normalized)
}

func (s *KanbanService) CreateSprint(ctx context.Context, userID int64, boardID uuid.UUID, req CreateSprintRequest) (*domain.Sprint, error) {
	if err := s.checkAccessEditTask(ctx, boardID, userID); err != nil {
		return nil, err
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, apperrors.BadRequest("Название спринта не может быть пустым")
	}

	var goalID *uuid.UUID
	if req.GoalID != nil {
		goal, err := s.boardRepo.GetGoal(ctx, *req.GoalID)
		if err != nil {
			return nil, apperrors.BadRequest("Указанная цель не найдена")
		}
		if goal.BoardID != boardID {
			return nil, apperrors.BadRequest("Цель не принадлежит этой доске")
		}
		goalID = req.GoalID
	}

	var projectID *uuid.UUID
	if req.ProjectID != nil {
		project, err := s.boardRepo.GetProject(ctx, *req.ProjectID)
		if err != nil {
			return nil, apperrors.BadRequest("Указанный проект не найден")
		}
		if project.BoardID != boardID {
			return nil, apperrors.BadRequest("Проект не принадлежит этой доске")
		}
		if goalID != nil && project.GoalID != nil && *project.GoalID != *goalID {
			return nil, apperrors.BadRequest("Проект не связан с выбранной целью")
		}
		if goalID == nil && project.GoalID != nil {
			goalID = project.GoalID
		}
		projectID = req.ProjectID
	}

	sp := &domain.Sprint{
		BoardID:   boardID,
		GoalID:    goalID,
		ProjectID: projectID,
		Name:      name,
		Status:    domain.SprintStatusPlanned,
		StartDate: req.StartDate,
		EndDate:   req.EndDate,
	}
	if status := strings.TrimSpace(req.Status); status != "" {
		sp.Status = status
	}
	if err := s.boardRepo.CreateSprint(ctx, sp); err != nil {
		return nil, apperrors.Internal("Не удалось создать спринт")
	}
	// Гамификация: создание спринта
	if s.gamification != nil {
		// Находим владельца доски для начисления
		board, err := s.boardRepo.GetBoard(ctx, boardID)
		if err == nil {
			go func(ownerID int64) {
				_ = s.gamification.TrackActivity(context.Background(), ownerID, domain.ActionSprintCreated)
			}(board.OwnerID)
		}
	}
	return sp, nil
}

func (s *KanbanService) CreateGoal(ctx context.Context, userID int64, boardID uuid.UUID, req CreateGoalRequest) (*domain.Goal, error) {
	if err := s.checkAccessEditTask(ctx, boardID, userID); err != nil {
		return nil, err
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, apperrors.BadRequest("Название цели не может быть пустым")
	}
	if len([]rune(name)) > 255 {
		return nil, apperrors.BadRequest("Название цели не должно превышать 255 символов")
	}

	goal := &domain.Goal{
		BoardID:     boardID,
		Name:        name,
		Description: trimOptionalString(req.Description),
		CreatedBy:   userID,
	}
	if err := s.boardRepo.CreateGoal(ctx, goal); err != nil {
		return nil, apperrors.InternalWithErr("Не удалось создать цель", err)
	}
	return goal, nil
}

func (s *KanbanService) CreateProject(ctx context.Context, userID int64, boardID uuid.UUID, req CreateProjectRequest) (*domain.Project, error) {
	if err := s.checkAccessEditTask(ctx, boardID, userID); err != nil {
		return nil, err
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, apperrors.BadRequest("Название проекта не может быть пустым")
	}
	if len([]rune(name)) > 255 {
		return nil, apperrors.BadRequest("Название проекта не должно превышать 255 символов")
	}

	if req.GoalID != nil {
		goal, err := s.boardRepo.GetGoal(ctx, *req.GoalID)
		if err != nil || goal == nil {
			return nil, apperrors.BadRequest("Цель не найдена")
		}
		if goal.BoardID != boardID {
			return nil, apperrors.BadRequest("Цель принадлежит другой доске")
		}
	}

	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = "active"
	}

	project := &domain.Project{
		BoardID:     boardID,
		GoalID:      req.GoalID,
		Name:        name,
		Description: trimOptionalString(req.Description),
		Status:      status,
		CreatedBy:   userID,
	}
	if err := s.boardRepo.CreateProject(ctx, project); err != nil {
		return nil, apperrors.InternalWithErr("Не удалось создать проект", err)
	}
	return project, nil
}

func (s *KanbanService) UpdateGoal(ctx context.Context, userID int64, boardID, goalID uuid.UUID, req UpdateGoalRequest) (*domain.Goal, error) {
	if err := s.checkAccessEditTask(ctx, boardID, userID); err != nil {
		return nil, err
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, apperrors.BadRequest("Название цели не может быть пустым")
	}
	if len([]rune(name)) > 255 {
		return nil, apperrors.BadRequest("Название цели не должно превышать 255 символов")
	}

	if err := s.boardRepo.UpdateGoal(ctx, boardID, goalID, name, trimOptionalString(req.Description)); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "goal not found") {
			return nil, apperrors.NotFound("Цель не найдена")
		}
		return nil, apperrors.InternalWithErr("Не удалось обновить цель", err)
	}
	return s.boardRepo.GetGoal(ctx, goalID)
}

func (s *KanbanService) DeleteGoal(ctx context.Context, userID int64, boardID, goalID uuid.UUID) error {
	if err := s.checkAccessEditTask(ctx, boardID, userID); err != nil {
		return err
	}
	if err := s.boardRepo.DeleteGoal(ctx, boardID, goalID, userID); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "goal not found") {
			return apperrors.NotFound("Цель не найдена")
		}
		return apperrors.InternalWithErr("Не удалось удалить цель", err)
	}
	return nil
}

func (s *KanbanService) ListMyGoals(ctx context.Context, userID int64) ([]*domain.GoalBoardItem, error) {
	start := time.Now()
	goals, err := s.boardRepo.ListGoalsForUser(ctx, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось загрузить цели пользователя", err)
	}
	goalsFetchedAt := time.Now()

	// Group goals by board ID to query projects and tasks per board
	boardGoalsMap := make(map[uuid.UUID][]*domain.GoalBoardItem)
	for _, g := range goals {
		if g != nil {
			boardGoalsMap[g.BoardID] = append(boardGoalsMap[g.BoardID], g)
		}
	}

	now := time.Now()
	const maxConcurrentGoalInsightLoads = 6
	sem := make(chan struct{}, maxConcurrentGoalInsightLoads)
	var wg sync.WaitGroup

	for boardID, boardGoals := range boardGoalsMap {
		wg.Add(1)
		go func(boardID uuid.UUID, boardGoals []*domain.GoalBoardItem) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			var (
				projects []*domain.Project
				tasks    []*domain.Task
				projErr  error
				taskErr  error
				innerWG  sync.WaitGroup
			)

			innerWG.Add(2)
			go func() {
				defer innerWG.Done()
				projects, projErr = s.boardRepo.ListProjects(ctx, boardID)
			}()
			go func() {
				defer innerWG.Done()
				tasks, taskErr = s.taskRepo.ListByBoard(ctx, boardID, nil)
			}()
			innerWG.Wait()

			if projErr != nil {
				s.logger.Warn("Failed to list projects for board in ListMyGoals", zap.String("board_id", boardID.String()), zap.Error(projErr))
				return
			}
			if taskErr != nil {
				s.logger.Warn("Failed to list tasks for board in ListMyGoals", zap.String("board_id", boardID.String()), zap.Error(taskErr))
				return
			}

			goalsForInsight := make([]*domain.Goal, len(boardGoals))
			for i, bg := range boardGoals {
				goalsForInsight[i] = &bg.Goal
			}

			populateGoalInsights(goalsForInsight, projects, tasks, now)
		}(boardID, boardGoals)
	}

	wg.Wait()

	s.logger.Info("kanban_my_goals_built",
		zap.Int64("user_id", userID),
		zap.Int("goals_count", len(goals)),
		zap.Int("boards_count", len(boardGoalsMap)),
		zap.Int64("goals_fetch_ms", goalsFetchedAt.Sub(start).Milliseconds()),
		zap.Int64("total_ms", time.Since(start).Milliseconds()),
	)

	return goals, nil
}

func (s *KanbanService) UpdateProject(ctx context.Context, userID int64, boardID, projectID uuid.UUID, req UpdateProjectRequest) (*domain.Project, error) {
	if err := s.checkAccessEditTask(ctx, boardID, userID); err != nil {
		return nil, err
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, apperrors.BadRequest("Название проекта не может быть пустым")
	}
	if len([]rune(name)) > 255 {
		return nil, apperrors.BadRequest("Название проекта не должно превышать 255 символов")
	}

	if req.GoalID != nil {
		goal, err := s.boardRepo.GetGoal(ctx, *req.GoalID)
		if err != nil || goal == nil {
			return nil, apperrors.BadRequest("Цель не найдена")
		}
		if goal.BoardID != boardID {
			return nil, apperrors.BadRequest("Цель принадлежит другой доске")
		}
	}

	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = "active"
	}

	if err := s.boardRepo.UpdateProject(ctx, boardID, projectID, req.GoalID, name, trimOptionalString(req.Description), status); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "project not found") {
			return nil, apperrors.NotFound("Проект не найден")
		}
		return nil, apperrors.InternalWithErr("Не удалось обновить проект", err)
	}
	return s.boardRepo.GetProject(ctx, projectID)
}

func (s *KanbanService) DeleteProject(ctx context.Context, userID int64, boardID, projectID uuid.UUID) error {
	if err := s.checkAccessEditTask(ctx, boardID, userID); err != nil {
		return err
	}
	if err := s.boardRepo.DeleteProject(ctx, boardID, projectID, userID); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "project not found") {
			return apperrors.NotFound("Проект не найден")
		}
		return apperrors.InternalWithErr("Не удалось удалить проект", err)
	}
	return nil
}

func (s *KanbanService) UpdateSprint(ctx context.Context, userID int64, boardID, sprintID uuid.UUID, req UpdateSprintRequest) (*domain.Sprint, error) {
	if err := s.checkAccessEditTask(ctx, boardID, userID); err != nil {
		return nil, err
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, apperrors.BadRequest("Название спринта не может быть пустым")
	}

	var goalID *uuid.UUID
	if req.GoalID != nil {
		goal, err := s.boardRepo.GetGoal(ctx, *req.GoalID)
		if err != nil {
			return nil, apperrors.BadRequest("Указанная цель не найдена")
		}
		if goal.BoardID != boardID {
			return nil, apperrors.BadRequest("Цель не принадлежит этой доске")
		}
		goalID = req.GoalID
	}

	var projectID *uuid.UUID
	if req.ProjectID != nil {
		project, err := s.boardRepo.GetProject(ctx, *req.ProjectID)
		if err != nil {
			return nil, apperrors.BadRequest("Указанный проект не найден")
		}
		if project.BoardID != boardID {
			return nil, apperrors.BadRequest("Проект не принадлежит этой доске")
		}
		if goalID != nil && project.GoalID != nil && *project.GoalID != *goalID {
			return nil, apperrors.BadRequest("Проект не связан с выбранной целью")
		}
		if goalID == nil && project.GoalID != nil {
			goalID = project.GoalID
		}
		projectID = req.ProjectID
	}

	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = domain.SprintStatusPlanned
	}

	if err := s.boardRepo.UpdateSprint(ctx, boardID, sprintID, goalID, projectID, name, status, req.StartDate, req.EndDate); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "sprint not found") {
			return nil, apperrors.NotFound("Спринт не найден")
		}
		return nil, apperrors.InternalWithErr("Не удалось обновить спринт", err)
	}
	return s.boardRepo.GetSprint(ctx, sprintID)
}

func (s *KanbanService) DeleteSprint(ctx context.Context, userID int64, boardID, sprintID uuid.UUID) error {
	if err := s.checkAccessEditTask(ctx, boardID, userID); err != nil {
		return err
	}

	if err := s.boardRepo.DeleteSprint(ctx, boardID, sprintID, userID); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "sprint not found") {
			return apperrors.NotFound("Спринт не найден")
		}
		return apperrors.InternalWithErr("Не удалось удалить спринт", err)
	}

	return nil
}

func (s *KanbanService) ListUserSprints(ctx context.Context, userID int64) ([]*domain.Sprint, error) {
	return s.boardRepo.ListUserSprints(ctx, userID)
}

func (s *KanbanService) CreateTask(ctx context.Context, userID int64, req CreateTaskRequest) (*domain.Task, error) {
	if err := validateCreateTask(&req); err != nil {
		return nil, err
	}
	if err := s.checkAccessEditTask(ctx, req.BoardID, userID); err != nil {
		return nil, err
	}
	if err := s.validateAttachmentSources(ctx, userID, req.AttachmentFileIDs); err != nil {
		return nil, err
	}

	if err := s.validateTaskRelations(ctx, req.BoardID, req.GoalID, req.ProjectID, req.SprintID); err != nil {
		return nil, err
	}

	assigneeIDs := normalizeAssigneeIDs(req.AssigneeUserIDs, req.AssigneeUserID)
	for _, assigneeID := range assigneeIDs {
		if err := ensureUserScopedAccess(ctx, s.userRepo, userID, assigneeID); err != nil {
			return nil, err
		}
	}

	if req.ParentID != nil {
		parent, err := s.taskRepo.GetByID(ctx, *req.ParentID)
		if err != nil || parent == nil {
			return nil, apperrors.BadRequest("Родительская задача не найдена")
		}
		if parent.BoardID != req.BoardID {
			return nil, apperrors.BadRequest("Родительская задача должна быть на той же доске")
		}
	}

	cols, err := s.boardRepo.ListColumns(ctx, req.BoardID)
	if err != nil || len(cols) == 0 {
		return nil, apperrors.BadRequest("На доске нет колонок для создания задачи")
	}
	firstCol := cols[0]

	t := &domain.Task{
		BoardID:             req.BoardID,
		GoalID:              req.GoalID,
		ProjectID:           req.ProjectID,
		SprintID:            req.SprintID,
		ColumnID:            firstCol.ID,
		CreatedBy:           userID,
		Title:               req.Title,
		Description:         req.Description,
		Priority:            req.Priority,
		AssigneeIDs:         assigneeIDs,
		OriginalEstimateSec: req.OriginalEstimateSec,
		DueAt:               req.DueAt,
		ParentID:            req.ParentID,
		Status:              domain.StatusTodo,
	}

	if err := s.taskRepo.Create(ctx, t); err != nil {
		return nil, apperrors.InternalWithErr("Не удалось создать задачу", err)
	}

	// Handle attachments if any
	if len(req.AttachmentFileIDs) > 0 && s.driveRepo != nil {
		for _, fileID := range req.AttachmentFileIDs {
			fileMeta, err := s.driveRepo.GetFileByID(ctx, fileID)
			if err != nil {
				s.logger.Warn("Failed to fetch file meta for attachment", zap.String("file_id", fileID.String()), zap.Error(err))
				continue
			}

			att := &domain.TaskAttachment{
				ID:         uuid.New(),
				TaskID:     t.ID,
				UploadedBy: userID,
				CreatedAt:  time.Now(),
				FileName:   fileMeta.RealName,
				ObjectName: fileMeta.ObjectName,
				FileSize:   fileMeta.Size,
				MimeType:   fileMeta.MimeType,
			}

			if err := s.taskRepo.AddAttachment(ctx, att); err != nil {
				s.logger.Error("Failed to add attachment during task creation", zap.String("task_id", t.ID.String()), zap.String("file_id", fileID.String()), zap.Error(err))
			}
		}
	}

	s.publishTaskCreatedEvent(t)
	s.notifySubtaskParents(t, userID, nil)

	s.notifyKanbanTaskCreated(userID, t)
	if len(t.AssigneeIDs) > 0 {
		s.notifyKanbanTaskAssigned(userID, t, t.AssigneeIDs)
	}

	// Гамификация: создание задачи
	if s.gamification != nil {
		go func() { _ = s.gamification.TrackActivity(context.Background(), userID, domain.ActionGamifTaskCreated) }()
	}

	return t, nil
}

func (s *KanbanService) UpsertDocumentologAssignment(ctx context.Context, serviceUserID int64, req DocumentologAssignmentUpsertRequest) (*domain.Task, *domain.Board, error) {
	return s.upsertDocumentologAssignment(ctx, serviceUserID, req, "documentolog-assignments")
}

func (s *KanbanService) upsertDocumentologAssignment(ctx context.Context, serviceUserID int64, req DocumentologAssignmentUpsertRequest, systemKey string) (*domain.Task, *domain.Board, error) {
	if existing, err := s.taskRepo.GetByDocumentologAssignment(ctx, req.AssignmentID); err == nil {
		if err = s.checkAccessEditTask(ctx, existing.BoardID, serviceUserID); err != nil {
			return nil, nil, err
		}
		if req.Status == "completed" || req.Status == "closed" {
			columnID := s.documentologDoneColumn(ctx, existing.BoardID, existing.ColumnID)
			if err := s.taskRepo.Move(ctx, serviceUserID, existing.ID, columnID, domain.StatusDone); err != nil {
				return nil, nil, err
			}
			existing.ColumnID, existing.Status = columnID, domain.StatusDone
		}
		if req.Status == "cancelled" {
			if err := s.taskRepo.Move(ctx, serviceUserID, existing.ID, existing.ColumnID, "cancelled"); err != nil {
				return nil, nil, err
			}
			existing.Status = "cancelled"
		}
		board, boardErr := s.boardRepo.GetBoard(ctx, existing.BoardID)
		if boardErr != nil {
			return nil, nil, boardErr
		}
		return existing, board, nil
	}
	var board *domain.Board
	var err error
	if strings.TrimSpace(req.BoardID) != "" {
		boardID, parseErr := uuid.Parse(req.BoardID)
		if parseErr != nil {
			return nil, nil, apperrors.BadRequest("Некорректный ID доски поручений")
		}
		board, err = s.boardRepo.GetSystemBoardByID(ctx, boardID, systemKey)
	} else {
		board, err = s.boardRepo.EnsureSystemBoard(ctx, serviceUserID, systemKey, "Поручения")
	}
	if err != nil {
		return nil, nil, err
	}
	if err = s.checkAccessManageBoardMembers(ctx, board.ID, serviceUserID); err != nil {
		return nil, nil, err
	}
	assignee, err := s.userRepo.GetByKeycloakID(ctx, req.AssigneeSub)
	if err != nil || assignee == nil {
		return nil, nil, apperrors.NotFound("Исполнитель Documentolog не найден в Team Workspace")
	}
	if err := s.boardRepo.AddMembers(ctx, []*domain.BoardMember{{BoardID: board.ID, UserID: assignee.ID, Role: "member"}}); err != nil {
		return nil, nil, err
	}
	var due *time.Time
	if strings.TrimSpace(req.DueDate) != "" {
		if value, parseErr := time.Parse("2006-01-02", req.DueDate); parseErr == nil {
			due = &value
		}
	}
	description := strings.TrimSpace(req.Description)
	if description == "" {
		description = "Поручение Documentolog: " + req.DocumentID
	}
	task := &domain.Task{BoardID: board.ID, CreatedBy: serviceUserID, Title: firstNonEmpty(strings.TrimSpace(req.Title), "Поручение"), Description: &description, Priority: firstNonEmpty(req.Priority, "medium"), DueAt: due, AssigneeIDs: []int64{assignee.ID}, Status: domain.StatusTodo, SourceSystem: "documentolog", SourceAssignmentID: req.AssignmentID, SourceDocumentID: req.DocumentID, SourceURL: req.SourceURL, SourceAuthorSub: req.AuthorSub, SourceDepartmentID: req.DepartmentID}
	cols, err := s.boardRepo.ListColumns(ctx, board.ID)
	if err != nil || len(cols) == 0 {
		return nil, nil, apperrors.BadRequest("На системной доске нет колонок")
	}
	task.ColumnID = cols[0].ID
	if err := s.taskRepo.Create(ctx, task); err != nil {
		if existing, lookupErr := s.taskRepo.GetByDocumentologAssignment(ctx, req.AssignmentID); lookupErr == nil {
			if accessErr := s.checkAccessEditTask(ctx, existing.BoardID, serviceUserID); accessErr != nil {
				return nil, nil, accessErr
			}
			return existing, board, nil
		}
		return nil, nil, err
	}
	if req.Status == "completed" || req.Status == "closed" {
		columnID := documentologDoneColumnFromList(cols, task.ColumnID)
		if err := s.taskRepo.Move(ctx, serviceUserID, task.ID, columnID, domain.StatusDone); err != nil {
			return nil, nil, err
		}
		task.ColumnID, task.Status = columnID, domain.StatusDone
	}
	if req.Status == "cancelled" {
		if err := s.taskRepo.Move(ctx, serviceUserID, task.ID, task.ColumnID, "cancelled"); err != nil {
			return nil, nil, err
		}
		task.Status = "cancelled"
	}
	return task, board, nil
}

func (s *KanbanService) documentologDoneColumn(ctx context.Context, boardID, fallback uuid.UUID) uuid.UUID {
	columns, err := s.boardRepo.ListColumns(ctx, boardID)
	if err != nil {
		return fallback
	}
	return documentologDoneColumnFromList(columns, fallback)
}

func documentologDoneColumnFromList(columns []*domain.KanbanColumn, fallback uuid.UUID) uuid.UUID {
	for _, column := range columns {
		if column != nil && column.Key == domain.ColDone {
			return column.ID
		}
	}
	return fallback
}

func (s *KanbanService) publishTaskCreatedEvent(task *domain.Task) {
	if s.eventPublisher == nil || task == nil {
		return
	}

	payload := kanbanTaskCreatedEventData{
		TaskID:      task.ID,
		BoardID:     task.BoardID,
		GoalID:      task.GoalID,
		ProjectID:   task.ProjectID,
		SprintID:    task.SprintID,
		ColumnID:    task.ColumnID,
		CreatedBy:   task.CreatedBy,
		AssigneeIDs: append([]int64(nil), task.AssigneeIDs...),
		Priority:    task.Priority,
		Title:       task.Title,
	}

	publishEventAsync(s.logger, s.eventPublisher, "kanban.task.created", payload, zap.String("task_id", task.ID.String()))
}

func (s *KanbanService) notifyKanbanTaskCreated(actorID int64, task *domain.Task) {
	s.sendKanbanTaskNotifications(
		actorID,
		task,
		task.AssigneeIDs,
		kanbanTaskCreatedNotificationType,
		func(taskLabel string) string {
			return fmt.Sprintf("Новая задача %s", taskLabel)
		},
		func(actorName, boardName, taskLabel, taskTitle string) string {
			return fmt.Sprintf("%s создал задачу %s на доске «%s»: %s", actorName, taskLabel, boardName, taskTitle)
		},
		"created",
	)
}

func (s *KanbanService) notifyKanbanTaskAssigned(actorID int64, task *domain.Task, assigneeIDs []int64) {
	s.sendKanbanTaskNotifications(
		actorID,
		task,
		assigneeIDs,
		kanbanTaskAssignedNotificationType,
		func(taskLabel string) string {
			return fmt.Sprintf("Вам назначили задачу %s", taskLabel)
		},
		func(actorName, boardName, taskLabel, taskTitle string) string {
			return fmt.Sprintf("%s назначил вас исполнителем по задаче %s на доске «%s»: %s", actorName, taskLabel, boardName, taskTitle)
		},
		"assigned",
	)
}

func (s *KanbanService) sendKanbanTaskNotifications(
	actorID int64,
	task *domain.Task,
	recipientIDs []int64,
	notificationType string,
	title func(taskLabel string) string,
	content func(actorName, boardName, taskLabel, taskTitle string) string,
	action string,
) {
	if s.notifService == nil || task == nil || len(recipientIDs) == 0 {
		return
	}

	recipients := uniqueRecipientIDs(recipientIDs, actorID)
	if len(recipients) == 0 {
		return
	}

	taskID := task.ID
	boardID := task.BoardID
	taskTitle := strings.TrimSpace(task.Title)
	if taskTitle == "" {
		taskTitle = "Без названия"
	}
	taskLabel := strings.TrimSpace(task.TaskKey)
	if taskLabel == "" {
		taskLabel = task.ID.String()
	}

	go func() {
		asyncCtx := context.Background()
		actorName := s.kanbanActorDisplayName(asyncCtx, actorID)
		boardName := "TeamBoard"
		if s.boardRepo != nil {
			if board, err := s.boardRepo.GetBoard(asyncCtx, boardID); err == nil && board != nil {
				boardName = board.Name
			}
		}

		for _, recipientID := range recipients {
			notifReq := domain.CreateNotificationRequest{
				UserID:          recipientID,
				OrganizerID:     &actorID,
				Type:            notificationType,
				TitleTemplate:   &domain.NotificationTemplate{Source: "Новая задача %s", Args: []any{taskLabel}},
				Title:           title(taskLabel),
				ContentTemplate: &domain.NotificationTemplate{Source: "%s создал задачу %s на доске «%s»: %s", Args: []any{actorName, taskLabel, boardName, taskTitle}},
				Content:         content(actorName, boardName, taskLabel, taskTitle),
				Metadata: map[string]string{
					"kind":       "kanban_task",
					"action":     action,
					"task_id":    taskID.String(),
					"task_key":   taskLabel,
					"board_id":   boardID.String(),
					"board_name": boardName,
					"task_title": taskTitle,
				},
			}

			if action == "assigned" {
				notifReq.TitleTemplate.Source = "Вам назначили задачу %s"
				notifReq.ContentTemplate.Source = "%s назначил вас исполнителем по задаче %s на доске «%s»: %s"
			}
			if err := s.notifService.CreateAndSendNotification(asyncCtx, notifReq); err != nil {
				s.logger.Error("failed to send kanban task notification",
					zap.Int64("recipient_id", recipientID),
					zap.String("task_id", taskID.String()),
					zap.String("notification_type", notificationType),
					zap.Error(err),
				)
			}
		}
	}()
}

func (s *KanbanService) kanbanActorDisplayName(ctx context.Context, actorID int64) string {
	if s.userRepo == nil {
		return "Коллега"
	}

	user, err := s.userRepo.GetByID(ctx, actorID)
	if err != nil || user == nil {
		return "Коллега"
	}

	name := strings.TrimSpace(user.FirstName + " " + user.LastName)
	if name != "" {
		return name
	}
	if strings.TrimSpace(user.Username) != "" {
		return strings.TrimSpace(user.Username)
	}
	return "Коллега"
}

func uniqueRecipientIDs(ids []int64, excludeID int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	recipients := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id == 0 || id == excludeID {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		recipients = append(recipients, id)
	}
	return recipients
}

func normalizeAssigneeIDs(ids []int64, legacyID *int64) []int64 {
	if legacyID != nil {
		ids = append(ids, *legacyID)
	}
	return uniqueRecipientIDs(ids, 0)
}

func newAssigneeIDs(oldIDs, updatedIDs []int64) []int64 {
	oldSet := make(map[int64]struct{}, len(oldIDs))
	for _, id := range oldIDs {
		oldSet[id] = struct{}{}
	}

	added := make([]int64, 0, len(updatedIDs))
	seenAdded := make(map[int64]struct{}, len(updatedIDs))
	for _, id := range updatedIDs {
		if _, ok := oldSet[id]; ok {
			continue
		}
		if _, ok := seenAdded[id]; ok {
			continue
		}
		seenAdded[id] = struct{}{}
		added = append(added, id)
	}
	return added
}

func (s *KanbanService) MoveTask(ctx context.Context, userID int64, taskID uuid.UUID, toColumnID uuid.UUID) error {
	t, err := s.taskRepo.GetByID(ctx, taskID)
	if err != nil || t == nil {
		return apperrors.NotFound("Задача не найдена")
	}

	if err := s.checkAccessEditTask(ctx, t.BoardID, userID); err != nil {
		return err
	}

	col, err := s.boardRepo.GetColumn(ctx, toColumnID)
	if err != nil || col == nil {
		return apperrors.NotFound("Целевая колонка не найдена")
	}
	if col.BoardID != t.BoardID {
		return apperrors.BadRequest("Целевая колонка принадлежит другой доске")
	}

	newStatus := mapColumnKeyToStatus(col.Key)
	if err := s.taskRepo.Move(ctx, userID, taskID, toColumnID, newStatus); err != nil {
		return apperrors.Internal("Не удалось переместить задачу")
	}
	s.realtime.notify(ctx, &websocket.Message{
		Type: "kanban_task_moved", From: userID, BoardID: &t.BoardID,
		TaskID: &taskID, FromColumnID: &t.ColumnID, ToColumnID: &toColumnID,
	})
	s.notifySubtaskParents(t, userID, nil)

	s.trackTaskMoveCompletion(userID, t, col.Key)
	return nil
}

func (s *KanbanService) trackTaskMoveCompletion(userID int64, t *domain.Task, columnKey string) {
	if s.gamification != nil && columnKey == domain.ColDone {
		go func() { _ = s.gamification.TrackActivity(context.Background(), userID, domain.ActionTaskClosed) }()
		if t.DueAt != nil && t.DueAt.After(time.Now()) {
			go func() { _ = s.gamification.TrackActivity(context.Background(), userID, domain.ActionTaskClosedFast) }()
		}
	}
}

func (s *KanbanService) UpdateTask(ctx context.Context, userID int64, taskID uuid.UUID, patch *domain.UpdateTaskPatch) (*domain.Task, error) {
	if err := validateTaskPatch(patch); err != nil {
		return nil, err
	}
	t, err := s.taskRepo.GetByID(ctx, taskID)
	if err != nil || t == nil {
		return nil, apperrors.NotFound("Задача не найдена")
	}

	if err := s.checkAccessEditTask(ctx, t.BoardID, userID); err != nil {
		return nil, err
	}
	if err := s.validateAttachmentSources(ctx, userID, patch.AddAttachmentFileIDs); err != nil {
		return nil, err
	}

	goalID := t.GoalID
	if patch.GoalID != nil {
		goalID = patch.GoalID
	}
	projectID := t.ProjectID
	if patch.ProjectID != nil {
		projectID = patch.ProjectID
	}
	sprintID := t.SprintID
	if patch.SprintID != nil {
		sprintID = patch.SprintID
	}
	if err := s.validateTaskRelations(ctx, t.BoardID, goalID, projectID, sprintID); err != nil {
		return nil, err
	}

	if patch.ParentID != nil {
		if *patch.ParentID == taskID {
			return nil, apperrors.BadRequest("Задача не может быть родителем самой себя")
		}
		parent, err := s.taskRepo.GetByID(ctx, *patch.ParentID)
		if err != nil || parent == nil {
			return nil, apperrors.BadRequest("Родительская задача не найдена")
		}
		if parent.BoardID != t.BoardID {
			return nil, apperrors.BadRequest("Родительская задача должна быть на той же доске")
		}
		for parent.ParentID != nil {
			if *parent.ParentID == taskID {
				return nil, apperrors.BadRequest("Нельзя назначить дочернюю задачу родителем")
			}
			nextParent, err := s.taskRepo.GetByID(ctx, *parent.ParentID)
			if err != nil || nextParent == nil {
				break
			}
			parent = nextParent
		}
		patch.ClearParentID = false
	}

	for _, assigneeID := range patch.AssigneeIDs {
		if err := ensureUserScopedAccess(ctx, s.userRepo, userID, assigneeID); err != nil {
			return nil, err
		}
	}

	var targetColumn *domain.KanbanColumn
	if patch.ColumnID != nil {
		if *patch.ColumnID == uuid.Nil {
			return nil, apperrors.BadRequest("Неверный ID колонки")
		}
		targetColumn, err = s.boardRepo.GetColumn(ctx, *patch.ColumnID)
		if err != nil || targetColumn == nil {
			return nil, apperrors.NotFound("Колонка не найдена")
		}
		if targetColumn.BoardID != t.BoardID {
			return nil, apperrors.BadRequest("Колонка принадлежит другой доске")
		}
		if patch.Status != nil && normalizeKanbanTaskStatus(*patch.Status) != normalizeKanbanTaskStatus(targetColumn.Key) {
			return nil, apperrors.BadRequest("Статус не соответствует выбранной колонке")
		}
	} else if patch.Status != nil {
		key := normalizeKanbanTaskStatus(*patch.Status)
		cols, err := s.boardRepo.ListColumns(ctx, t.BoardID)
		if err != nil {
			return nil, apperrors.InternalWithErr("Не удалось получить колонки", err)
		}
		for _, col := range cols {
			if col != nil && normalizeKanbanTaskStatus(col.Key) == key {
				if targetColumn != nil {
					return nil, apperrors.BadRequest("Неоднозначный статус: передайте column_id")
				}
				targetColumn = col
			}
		}
		if targetColumn == nil {
			return nil, apperrors.BadRequest("Статус не соответствует колонке этой доски")
		}
	}
	if targetColumn != nil {
		status := mapColumnKeyToStatus(targetColumn.Key)
		patch.Status = &status
		patch.ColumnID = &targetColumn.ID
	}

	var addedAssigneeIDs []int64
	if patch.AssigneeIDs != nil {
		addedAssigneeIDs = newAssigneeIDs(t.AssigneeIDs, patch.AssigneeIDs)
	}

	if err := s.taskRepo.Update(ctx, userID, taskID, patch); err != nil {
		return nil, apperrors.Internal("Не удалось обновить задачу")
	}

	if targetColumn != nil && targetColumn.ID != t.ColumnID {
		s.realtime.notify(ctx, &websocket.Message{Type: "kanban_task_moved", From: userID, BoardID: &t.BoardID, TaskID: &taskID, FromColumnID: &t.ColumnID, ToColumnID: &targetColumn.ID})
		s.trackTaskMoveCompletion(userID, t, targetColumn.Key)
	}

	// Handle adding new attachments if any
	if len(patch.AddAttachmentFileIDs) > 0 && s.driveRepo != nil {
		for _, fileID := range patch.AddAttachmentFileIDs {
			fileMeta, err := s.driveRepo.GetFileByID(ctx, fileID)
			if err != nil {
				s.logger.Warn("Failed to fetch file meta for attachment during update", zap.String("file_id", fileID.String()), zap.Error(err))
				continue
			}

			att := &domain.TaskAttachment{
				ID:         uuid.New(),
				TaskID:     taskID,
				UploadedBy: userID,
				CreatedAt:  time.Now(),
				FileName:   fileMeta.RealName,
				ObjectName: fileMeta.ObjectName,
				FileSize:   fileMeta.Size,
				MimeType:   fileMeta.MimeType,
			}

			if err := s.taskRepo.AddAttachment(ctx, att); err != nil {
				s.logger.Error("Failed to add attachment during task update", zap.String("task_id", taskID.String()), zap.String("file_id", fileID.String()), zap.Error(err))
			}
		}
	}

	updated, err := s.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if targetColumn != nil {
		updated.ColumnKey = targetColumn.Key
	}
	s.notifyKanbanTaskAssigned(userID, updated, addedAssigneeIDs)
	s.notifySubtaskParents(updated, userID, t.ParentID)

	return updated, nil
}

func (s *KanbanService) UpdateTaskStatus(ctx context.Context, userID int64, taskID uuid.UUID, req UpdateTaskStatusRequest) (*domain.Task, error) {
	t, err := s.taskRepo.GetByID(ctx, taskID)
	if err != nil || t == nil {
		return nil, apperrors.NotFound("Задача не найдена")
	}

	// Status changes require write access.
	if err := s.checkAccessEditTask(ctx, t.BoardID, userID); err != nil {
		return nil, err
	}

	key := normalizeKanbanTaskStatus(req.Status)
	if key == "" {
		return nil, apperrors.BadRequest("Статус не может быть пустым")
	}
	columns, err := s.boardRepo.ListColumns(ctx, t.BoardID)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось получить колонки", err)
	}
	var target *domain.KanbanColumn
	for _, column := range columns {
		if column != nil && strings.EqualFold(strings.TrimSpace(column.Key), key) {
			if target != nil {
				return nil, apperrors.BadRequest("Статусу соответствуют несколько колонок; используйте перемещение по ID колонки")
			}
			target = column
		}
	}
	if target == nil {
		return nil, apperrors.BadRequest("Статус не соответствует колонке этой доски")
	}
	// Reuse the drag-and-drop path: atomic status/column/audit update and WS event.
	if err = s.MoveTask(ctx, userID, taskID, target.ID); err != nil {
		return nil, err
	}

	return s.taskRepo.GetByID(ctx, taskID)
}

func (s *KanbanService) DeleteTask(ctx context.Context, userID int64, taskID uuid.UUID) error {
	t, err := s.taskRepo.GetByID(ctx, taskID)
	if err != nil || t == nil {
		return apperrors.NotFound("Задача не найдена")
	}

	if err := s.checkAccessDeleteTask(ctx, t.BoardID, userID); err != nil {
		return err
	}

	if err := s.taskRepo.Delete(ctx, userID, taskID); err != nil {
		return apperrors.Internal("Не удалось удалить задачу")
	}
	s.notifySubtaskParents(t, userID, nil)
	return nil
}

func (s *KanbanService) AttachFile(ctx context.Context, userID int64, taskID uuid.UUID, fileID uuid.UUID) error {
	t, err := s.taskRepo.GetByID(ctx, taskID)
	if err != nil || t == nil {
		return apperrors.NotFound("Задача не найдена")
	}

	if err := s.checkAccessEditTask(ctx, t.BoardID, userID); err != nil {
		return err
	}

	if s.driveRepo == nil || s.fileService == nil {
		return apperrors.Internal("Хранилище файлов недоступно")
	}
	file, err := s.driveRepo.GetFileByID(ctx, fileID)
	if err != nil || file == nil {
		return apperrors.NotFound("Файл не найден")
	}
	if file.OwnerID != userID || file.Scope != "kanban" {
		return apperrors.Forbidden("Можно прикреплять только свои загруженные файлы Kanban")
	}
	stream, mimeType, size, err := s.fileService.DownloadKanbanFile(ctx, file.ObjectName)
	if err != nil {
		return apperrors.Internal("Не удалось прочитать загруженный файл")
	}
	defer stream.Close()
	// Each attachment owns its object, so removing it cannot break another task.
	_, err = s.UploadTaskFile(ctx, taskID, userID, file.RealName, size, mimeType, stream)
	return err
}

func (s *KanbanService) CreateColumn(ctx context.Context, userID int64, req CreateColumnRequest) (*domain.KanbanColumn, error) {
	if err := s.checkAccessEditTask(ctx, req.BoardID, userID); err != nil {
		return nil, err
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return nil, apperrors.BadRequest("Название колонки не может быть пустым")
	}
	if len([]rune(req.Name)) > 50 {
		return nil, apperrors.BadRequest("Название колонки не должно превышать 50 символов")
	}

	key := normalizeKanbanColumnKey(req.Name, req.Key)

	c := &domain.KanbanColumn{
		BoardID:  req.BoardID,
		Name:     req.Name,
		UserID:   userID,
		IsSystem: false,
		Key:      key,
	}

	if err := s.boardRepo.CreateColumn(ctx, c); err != nil {
		return nil, apperrors.InternalWithErr("Не удалось создать колонку", err)
	}
	return c, nil
}

func (s *KanbanService) DeleteColumn(ctx context.Context, userID int64, columnID uuid.UUID) error {
	col, err := s.boardRepo.GetColumn(ctx, columnID)
	if err != nil {
		return apperrors.NotFound("Колонка не найдена")
	}

	if err := s.checkAccessEditTask(ctx, col.BoardID, userID); err != nil {
		return err
	}

	tasks, err := s.taskRepo.ListByBoard(ctx, col.BoardID, nil) // Можно оптимизировать отдельным методом CountByColumn
	if err == nil {
		for _, t := range tasks {
			if t.ColumnID == columnID {
				return apperrors.BadRequest("Нельзя удалить колонку, в которой есть задачи. Сначала переместите или удалите их.")
			}
		}
	}

	if err := s.boardRepo.DeleteColumn(ctx, columnID); err != nil {
		return apperrors.InternalWithErr("Не удалось удалить колонку", err)
	}
	return nil
}

func (s *KanbanService) SearchTasks(ctx context.Context, userID int64, req SearchTasksRequest) ([]*domain.Task, error) {
	filters, err := parseTaskFilters(req)
	if err != nil {
		return nil, err
	}

	return s.taskRepo.Search(ctx, userID, filters)
}

func (s *KanbanService) GetTask(ctx context.Context, userID int64, taskID uuid.UUID) (*domain.Task, error) {
	t, err := s.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return nil, apperrors.NotFound("Задача не найдена")
	}

	if err := s.checkAccess(ctx, t.BoardID, userID); err != nil {
		return nil, err
	}

	return t, nil
}

func (s *KanbanService) GetTaskByKey(ctx context.Context, userID int64, taskKey string) (*domain.Task, error) {
	t, err := s.taskRepo.GetByTaskKey(ctx, userID, taskKey)
	if err != nil {
		return nil, apperrors.NotFound("Задача не найдена")
	}

	if err := s.checkAccess(ctx, t.BoardID, userID); err != nil {
		return nil, err
	}

	return t, nil
}

func (s *KanbanService) GetBoardStats(ctx context.Context, userID int64, boardID uuid.UUID, dateFrom, dateTo *time.Time) (*domain.BoardStatsResponse, error) {
	if err := s.checkAccess(ctx, boardID, userID); err != nil {
		return nil, err
	}

	stats, err := s.taskRepo.GetBoardStats(ctx, boardID, dateFrom, dateTo)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось получить статистику доски", err)
	}
	return stats, nil
}

type FileContent struct {
	Filename    string
	ContentType string
	Size        int64
	Stream      io.ReadCloser
}

// UploadTaskFile загружает файл в MinIO (kanban bucket) и сохраняет информацию в БД
func (s *KanbanService) UploadTaskFile(ctx context.Context, taskID uuid.UUID, userID int64, filename string, size int64, contentType string, reader io.Reader) (*domain.TaskAttachment, error) {
	if s.fileService == nil {
		return nil, apperrors.Internal("Хранилище файлов недоступно")
	}
	filename = filepath.Base(strings.ReplaceAll(filename, "\\", "/"))
	if strings.TrimSpace(filename) == "" || filename == "." || filename == "/" || strings.ContainsAny(filename, "\r\n\x00") || size < 0 || size > MaxFileSize {
		return nil, apperrors.BadRequest("Некорректное имя или размер файла (максимум 500 МБ)")
	}
	t, err := s.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return nil, apperrors.NotFound("Задача не найдена")
	}
	if err := s.checkAccessEditTask(ctx, t.BoardID, userID); err != nil {
		return nil, err
	}

	objectName := fmt.Sprintf("tasks/%s/%s_%s", taskID, uuid.New(), filename)

	uploadedPath, err := s.fileService.UploadKanbanFile(ctx, userID, filename, objectName, size, reader)
	if err != nil {
		return nil, apperrors.InternalWithErr("Ошибка загрузки файла в хранилище", err)
	}

	att := &domain.TaskAttachment{
		ID:         uuid.New(),
		TaskID:     taskID,
		UploadedBy: userID,
		CreatedAt:  time.Now(),
		FileName:   filename,
		ObjectName: uploadedPath,
		FileSize:   size,
		MimeType:   contentType,
	}

	if err := s.taskRepo.AddAttachment(ctx, att); err != nil {
		_ = s.fileService.DeleteKanbanFile(ctx, uploadedPath)
		return nil, apperrors.Internal("Не удалось сохранить информацию о файле")
	}

	att.DownloadURL = fmt.Sprintf("/api/v1/kanban/tasks/%s/files/%s/download", taskID, att.ID)
	s.notifyTaskContent(t, userID, "kanban_attachment_created", att)

	return att, nil
}

// GetTaskFileContent возвращает поток данных файла для скачивания
func (s *KanbanService) GetTaskFileContent(ctx context.Context, userID int64, taskID uuid.UUID, fileID uuid.UUID) (*FileContent, error) {
	t, err := s.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return nil, apperrors.NotFound("Задача не найдена")
	}
	if err := s.checkAccess(ctx, t.BoardID, userID); err != nil {
		return nil, err
	}

	att, err := s.taskRepo.GetAttachmentByID(ctx, fileID)
	if err != nil || att == nil || att.TaskID != taskID {
		return nil, apperrors.NotFound("Файл не найден")
	}
	if s.fileService == nil {
		return nil, apperrors.Internal("Хранилище файлов недоступно")
	}

	stream, contentType, size, err := s.fileService.DownloadKanbanFile(ctx, att.ObjectName)
	if err != nil {
		s.logger.Error("failed to download file from kanban bucket", zap.String("object", att.ObjectName), zap.Error(err))
		return nil, apperrors.Internal("Ошибка получения файла из хранилища")
	}

	return &FileContent{
		Filename:    att.FileName,
		ContentType: contentType,
		Size:        size,
		Stream:      stream,
	}, nil
}

// DeleteTaskFile удаляет файл и из хранилища, и из БД
func (s *KanbanService) DeleteTaskFile(ctx context.Context, userID int64, taskID uuid.UUID, fileID uuid.UUID) error {
	t, err := s.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return apperrors.NotFound("Задача не найдена")
	}

	att, err := s.taskRepo.GetAttachmentByID(ctx, fileID)
	if err != nil || att == nil || att.TaskID != taskID {
		return apperrors.NotFound("Файл не найден")
	}

	if err := s.checkAccessEditTask(ctx, t.BoardID, userID); err != nil {
		return err
	}

	if s.fileService == nil {
		return apperrors.Internal("Хранилище файлов недоступно")
	}
	if err := s.taskRepo.DeleteAttachment(ctx, fileID, userID); err != nil {
		return apperrors.Internal("Не удалось удалить запись о файле")
	}
	// Legacy attachments may reference shared standalone uploads. Only remove
	// objects owned by this task; shared source files keep their own lifecycle.
	if strings.HasPrefix(att.ObjectName, "tasks/"+taskID.String()+"/") {
		if err := s.fileService.DeleteKanbanFile(ctx, att.ObjectName); err != nil {
			s.logger.Warn("failed to delete file from kanban bucket", zap.String("object", att.ObjectName), zap.Error(err))
		}
	}
	s.notifyTaskContent(t, userID, "kanban_attachment_deleted", map[string]any{"attachment_id": fileID})

	return nil
}

// UploadFile загружает файл в бакет kanban без привязки к задаче
func (s *KanbanService) UploadFile(ctx context.Context, userID int64, filename string, size int64, contentType string, reader io.Reader) (*domain.DriveFile, error) {
	driveFile, err := s.fileService.UploadKanbanFileStandalone(ctx, userID, filename, size, reader)
	if err != nil {
		return nil, err
	}

	return driveFile, nil
}

func (s *KanbanService) SetBoardBackgroundPreset(ctx context.Context, userID int64, boardID uuid.UUID, presetID string) (*domain.BoardBackground, error) {
	if err := s.checkAccessModifyBoard(ctx, boardID, userID); err != nil {
		return nil, err
	}

	b, err := s.boardRepo.GetBoard(ctx, boardID)
	if err != nil {
		return nil, apperrors.NotFound("Доска не найдена")
	}

	// When setting a preset, we always switch to preset_color mode.
	mode := domain.BgModePresetColor

	// If there was a custom image, we remove it to properly switch to preset color.
	if b.BgURL != nil {
		_ = s.fileService.DeleteKanbanFile(ctx, *b.BgURL)
	}

	if err := s.boardRepo.UpdateBoardBackground(ctx, boardID, mode, presetID, nil); err != nil {
		return nil, apperrors.Internal("Ошибка сохранения настроек фона")
	}

	var imageUrl *string
	if b.BgURL != nil {
		url := fmt.Sprintf("/api/v1/kanban/boards/%s/background/image/download", boardID)
		imageUrl = &url
	}

	return &domain.BoardBackground{
		Mode:      mode,
		PresetID:  presetID,
		PresetHex: domain.GetPresetHex(presetID),
		ImageURL:  imageUrl,
	}, nil
}

func (s *KanbanService) UploadBoardBackgroundImage(ctx context.Context, userID int64, boardID uuid.UUID, filename string, size int64, contentType string, reader io.Reader) (*domain.BoardBackground, error) {
	if err := s.checkAccessModifyBoard(ctx, boardID, userID); err != nil {
		return nil, err
	}

	b, err := s.boardRepo.GetBoard(ctx, boardID)
	if err != nil {
		return nil, apperrors.NotFound("Доска не найдена")
	}

	objectName := fmt.Sprintf("kanban/boards/%s/bg_%d_%s", boardID, time.Now().Unix(), filename)
	uploadedPath, err := s.fileService.UploadKanbanFile(ctx, userID, filename, objectName, size, reader)
	if err != nil {
		return nil, apperrors.InternalWithErr("Ошибка загрузки фонового изображения", err)
	}

	// Delete old background if it was custom
	if b.BgURL != nil {
		_ = s.fileService.DeleteKanbanFile(ctx, *b.BgURL)
	}

	preset := b.BgPreset
	if preset == "" {
		preset = domain.BgPresetDefault
	}

	if err := s.boardRepo.UpdateBoardBackground(ctx, boardID, domain.BgModeCustomImage, preset, &uploadedPath); err != nil {
		_ = s.fileService.DeleteKanbanFile(ctx, uploadedPath)
		return nil, apperrors.Internal("Ошибка сохранения файла фона в базу данных")
	}

	imageURL := fmt.Sprintf("/api/v1/kanban/boards/%s/background/image/download", boardID)

	return &domain.BoardBackground{
		Mode:      domain.BgModeCustomImage,
		PresetID:  preset,
		PresetHex: domain.GetPresetHex(preset),
		ImageURL:  &imageURL,
	}, nil
}

func (s *KanbanService) GetBoardBackgroundImage(ctx context.Context, userID int64, boardID uuid.UUID) (*FileContent, error) {
	if err := s.checkAccess(ctx, boardID, userID); err != nil {
		return nil, err
	}

	b, err := s.boardRepo.GetBoard(ctx, boardID)
	if err != nil {
		return nil, apperrors.NotFound("Доска не найдена")
	}

	if b.BgURL == nil {
		return nil, apperrors.NotFound("Фоновое изображение не найдено")
	}

	stream, contentType, size, err := s.fileService.DownloadKanbanFile(ctx, *b.BgURL)
	if err != nil {
		s.logger.Error("failed to download background image from kanban bucket", zap.String("object", *b.BgURL), zap.Error(err))
		return nil, apperrors.Internal("Ошибка получения файла из хранилища")
	}

	// Extract filename from object path
	parts := strings.Split(*b.BgURL, "/")
	filename := parts[len(parts)-1]

	return &FileContent{
		Filename:    filename,
		ContentType: contentType,
		Size:        size,
		Stream:      stream,
	}, nil
}

func (s *KanbanService) RemoveBoardBackgroundImage(ctx context.Context, userID int64, boardID uuid.UUID) (*domain.BoardBackground, error) {
	if err := s.checkAccessModifyBoard(ctx, boardID, userID); err != nil {
		return nil, err
	}

	b, err := s.boardRepo.GetBoard(ctx, boardID)
	if err != nil {
		return nil, apperrors.NotFound("Доска не найдена")
	}

	if b.BgURL != nil {
		if err := s.fileService.DeleteKanbanFile(ctx, *b.BgURL); err != nil {
			s.logger.Warn("failed to delete file from kanban bucket", zap.String("object", *b.BgURL), zap.Error(err))
		}
	}

	preset := b.BgPreset
	if preset == "" {
		preset = domain.BgPresetDefault
	}

	if err := s.boardRepo.UpdateBoardBackground(ctx, boardID, domain.BgModePresetColor, preset, nil); err != nil {
		return nil, apperrors.Internal("Ошибка обновления настроек фона")
	}

	return &domain.BoardBackground{
		Mode:      domain.BgModePresetColor,
		PresetID:  preset,
		PresetHex: domain.GetPresetHex(preset),
		ImageURL:  nil,
	}, nil
}

func (s *KanbanService) ListTaskFiles(ctx context.Context, userID int64, taskID uuid.UUID) ([]*domain.TaskAttachment, error) {
	t, err := s.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return nil, apperrors.NotFound("Задача не найдена")
	}
	if err := s.checkAccess(ctx, t.BoardID, userID); err != nil {
		return nil, err
	}

	return s.taskRepo.GetAttachments(ctx, taskID)
}

func (s *KanbanService) AddWorklog(ctx context.Context, userID int64, taskID uuid.UUID, w *domain.TaskWorklog) error {
	t, err := s.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return apperrors.NotFound("Задача не найдена")
	}
	if err := s.checkAccessEditTask(ctx, t.BoardID, userID); err != nil {
		return err
	}

	w.TaskID = taskID
	w.UserID = userID

	if err := s.taskRepo.AddWorklog(ctx, w); err != nil {
		return apperrors.InternalWithErr("Не удалось добавить ворклог", err)
	}
	return nil
}

func (s *KanbanService) ListWorklogs(ctx context.Context, userID int64, taskID uuid.UUID) ([]*domain.TaskWorklog, error) {
	t, err := s.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return nil, apperrors.NotFound("Задача не найдена")
	}
	if err := s.checkAccess(ctx, t.BoardID, userID); err != nil {
		return nil, err
	}

	return s.taskRepo.ListWorklogs(ctx, taskID)
}

type UpdateColumnRequest struct {
	Name string `json:"name" binding:"required"`
	Key  string `json:"key"`
}

type MoveColumnRequest struct {
	NewPosition *int `json:"new_position" binding:"required"`
}

func (s *KanbanService) UpdateColumn(ctx context.Context, userID int64, columnID uuid.UUID, req UpdateColumnRequest) (*domain.KanbanColumn, error) {
	col, err := s.boardRepo.GetColumn(ctx, columnID)
	if err != nil {
		return nil, apperrors.NotFound("Колонка не найдена")
	}

	if err := s.checkAccessEditTask(ctx, col.BoardID, userID); err != nil {
		return nil, err
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return nil, apperrors.BadRequest("Название колонки не может быть пустым")
	}
	if len([]rune(req.Name)) > 50 {
		return nil, apperrors.BadRequest("Название колонки не должно превышать 50 символов")
	}

	// User asked to allow editing system columns too, so no IsSystem check here.
	key := col.Key
	if strings.TrimSpace(req.Key) != "" {
		key = normalizeKanbanColumnKey(req.Name, req.Key)
	}
	if err := s.boardRepo.UpdateColumn(ctx, columnID, req.Name, key, mapColumnKeyToStatus(key), userID); err != nil {
		return nil, apperrors.Internal("Не удалось обновить колонку")
	}

	s.realtime.notify(ctx, &websocket.Message{Type: "kanban_column_updated", From: userID, BoardID: &col.BoardID, ColumnID: &columnID})
	return s.boardRepo.GetColumn(ctx, columnID)
}

func (s *KanbanService) MoveColumn(ctx context.Context, userID int64, columnID uuid.UUID, req MoveColumnRequest) error {
	if req.NewPosition == nil {
		return apperrors.BadRequest("Не указана новая позиция колонки")
	}
	col, err := s.boardRepo.GetColumn(ctx, columnID)
	if err != nil || col == nil {
		return apperrors.NotFound("Колонка не найдена")
	}
	if err := s.checkAccessEditTask(ctx, col.BoardID, userID); err != nil {
		return err
	}

	if err := s.boardRepo.MoveColumn(ctx, col.BoardID, columnID, *req.NewPosition); err != nil {
		return apperrors.Internal("Не удалось переместить колонку")
	}
	// Clients reload the authoritative order; the repository clamps the requested
	// index and a concurrent move may already have changed it again.
	s.realtime.notify(ctx, &websocket.Message{
		Type: "kanban_column_moved", From: userID, BoardID: &col.BoardID, ColumnID: &columnID,
	})

	return nil
}

// GetTaskHistory получает историю изменений задачи
func (s *KanbanService) GetTaskHistory(ctx context.Context, userID int64, taskID uuid.UUID) ([]*domain.TaskHistoryResponse, error) {
	boardID, err := s.taskRepo.GetHistoryBoardID(ctx, taskID)
	if err != nil {
		return nil, apperrors.NotFound("История задачи не найдена")
	}
	if err = s.checkAccess(ctx, boardID, userID); err != nil {
		return nil, err
	}

	// Получаем историю
	history, err := s.taskRepo.GetHistory(ctx, taskID)
	if err != nil {
		return nil, apperrors.Internal("Не удалось получить историю задачи")
	}

	return history, nil
}

func (s *KanbanService) AddComment(ctx context.Context, userID int64, taskID uuid.UUID, req CreateCommentRequest) (*domain.TaskComment, error) {
	content, validationErr := validateTaskComment(req.Content, req.AttachmentURL)
	if validationErr != nil {
		return nil, validationErr
	}
	req.Content = content
	t, err := s.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return nil, apperrors.NotFound("Задача не найдена")
	}
	if err := s.checkAccessEditTask(ctx, t.BoardID, userID); err != nil {
		return nil, err
	}

	com := &domain.TaskComment{
		ID:            uuid.New(),
		TaskID:        taskID,
		UserID:        userID,
		Content:       req.Content,
		AttachmentURL: req.AttachmentURL,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	if err := s.taskRepo.CreateComment(ctx, com); err != nil {
		return nil, apperrors.Internal("Ошибка при сохранении комментария")
	}
	s.notifyTaskContent(t, userID, "kanban_comment_created", com)

	// Гамификация: комментарий к задаче
	if s.gamification != nil {
		go func() { _ = s.gamification.TrackActivity(context.Background(), userID, domain.ActionTaskCommentAdded) }()
	}

	return com, nil
}

func (s *KanbanService) ListComments(ctx context.Context, userID int64, taskID uuid.UUID) ([]*domain.TaskComment, error) {
	t, err := s.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return nil, apperrors.NotFound("Задача не найдена")
	}
	if err := s.checkAccess(ctx, t.BoardID, userID); err != nil {
		return nil, err
	}

	return s.taskRepo.GetComments(ctx, taskID)
}

func (s *KanbanService) UpdateComment(ctx context.Context, userID int64, commentID uuid.UUID, req UpdateCommentRequest) error {
	com, err := s.taskRepo.GetCommentByID(ctx, commentID)
	if err != nil {
		return apperrors.NotFound("Комментарий не найден")
	}

	if com.UserID != userID {
		return apperrors.Forbidden("Нет прав для редактирования этого комментария")
	}
	task, err := s.editableContentTask(ctx, userID, com.TaskID)
	if err != nil {
		return err
	}
	content, err := validateTaskComment(req.Content, com.AttachmentURL)
	if err != nil {
		return err
	}
	if err := s.taskRepo.UpdateComment(ctx, commentID, userID, content); err != nil {
		return err
	}
	com.Content = content
	com.UpdatedAt = time.Now()
	s.notifyTaskContent(task, userID, "kanban_comment_updated", com)
	return nil
}

func (s *KanbanService) DeleteComment(ctx context.Context, userID int64, commentID uuid.UUID) error {
	com, err := s.taskRepo.GetCommentByID(ctx, commentID)
	if err != nil {
		return apperrors.NotFound("Комментарий не найден")
	}

	if com.UserID != userID {
		return apperrors.Forbidden("Нет прав для удаления этого комментария")
	}
	task, err := s.editableContentTask(ctx, userID, com.TaskID)
	if err != nil {
		return err
	}
	if err := s.taskRepo.DeleteComment(ctx, commentID, userID); err != nil {
		return err
	}
	s.notifyTaskContent(task, userID, "kanban_comment_deleted", map[string]any{"comment_id": commentID})
	return nil
}

func (s *KanbanService) AddCommentWithPhoto(ctx context.Context, userID int64, taskID uuid.UUID, content string, filename string, size int64, contentType string, reader io.Reader) (*domain.TaskComment, error) {
	placeholder := "attachment"
	if _, err := validateTaskComment(content, &placeholder); err != nil {
		return nil, err
	}
	// 1. Используем существующий метод загрузки файлов задачи, чтобы получить полноценный download_url
	att, err := s.UploadTaskFile(ctx, taskID, userID, filename, size, contentType, reader)
	if err != nil {
		return nil, err
	}

	// 2. Создаем комментарий, используя полученный DownloadURL
	comment, err := s.AddComment(ctx, userID, taskID, CreateCommentRequest{
		Content:       content,
		AttachmentURL: &att.DownloadURL,
	})
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if cleanupErr := s.DeleteTaskFile(cleanupCtx, userID, taskID, att.ID); cleanupErr != nil {
			s.logger.Warn("Failed to clean up comment attachment", zap.Error(cleanupErr))
		}
	}
	return comment, err
}

// Labels

func (s *KanbanService) CreateLabel(ctx context.Context, userID int64, boardID uuid.UUID, req CreateLabelRequest) (*domain.KanbanLabel, error) {
	if err := s.checkAccessEditTask(ctx, boardID, userID); err != nil {
		return nil, err
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return nil, apperrors.BadRequest("Label name cannot be empty")
	}
	if len([]rune(req.Name)) > 50 {
		return nil, apperrors.BadRequest("Label name must not exceed 50 characters")
	}

	l := &domain.KanbanLabel{
		BoardID: boardID,
		Name:    req.Name,
		Color:   req.Color,
	}
	if err := s.boardRepo.CreateLabel(ctx, l); err != nil {
		if errors.Is(err, repository.ErrLabelExists) {
			return nil, apperrors.Conflict("Метка с таким названием уже существует")
		}
		return nil, apperrors.Internal("Failed to create label")
	}
	return l, nil
}

func (s *KanbanService) ListLabels(ctx context.Context, userID int64, boardID uuid.UUID) ([]*domain.KanbanLabel, error) {
	if err := s.checkAccess(ctx, boardID, userID); err != nil {
		return nil, err
	}
	return s.boardRepo.ListLabels(ctx, boardID)
}

func (s *KanbanService) DeleteLabel(ctx context.Context, userID int64, labelID uuid.UUID) error {
	l, err := s.boardRepo.GetLabel(ctx, labelID)
	if err != nil {
		return apperrors.NotFound("Label not found")
	}
	if err := s.checkAccessEditTask(ctx, l.BoardID, userID); err != nil {
		return err
	}
	return s.boardRepo.DeleteLabel(ctx, labelID, userID)

}

func (s *KanbanService) AddLabelToTask(ctx context.Context, userID int64, taskID uuid.UUID, req AddLabelToTaskRequest) error {
	t, err := s.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return apperrors.NotFound("Task not found")
	}
	if err := s.checkAccessEditTask(ctx, t.BoardID, userID); err != nil {
		return err
	}

	// Check if label belongs to the board
	l, err := s.boardRepo.GetLabel(ctx, req.LabelID)
	if err != nil || l.BoardID != t.BoardID {
		return apperrors.BadRequest("Label does not belong to this board")
	}

	return s.taskRepo.AddLabel(ctx, taskID, req.LabelID, userID)
}

func (s *KanbanService) RemoveLabelFromTask(ctx context.Context, userID int64, taskID, labelID uuid.UUID) error {
	t, err := s.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return apperrors.NotFound("Task not found")
	}
	if err := s.checkAccessEditTask(ctx, t.BoardID, userID); err != nil {
		return err
	}
	return s.taskRepo.RemoveLabel(ctx, taskID, labelID, userID)
}
func mapColumnKeyToStatus(key string) string {
	key = strings.ToUpper(strings.TrimSpace(key))
	switch key {
	case domain.ColTODO:
		return domain.StatusTodo
	case domain.ColInProgress:
		return domain.StatusWorking
	case domain.ColTest:
		return domain.StatusReview
	case domain.ColDone:
		return domain.StatusDone
	default:
		return key
	}
}
