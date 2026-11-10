package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

type GetBoardFilters struct {
	SearchTasksRequest
	UserID *int64     `form:"user_id"`
	Status *string    `form:"status"`
	Date   *time.Time `form:"date"`
	Name   *string    `form:"name"`
}

func (s *KanbanService) GetBoardWithFilters(ctx context.Context, requestingUserID int64, filters *GetBoardFilters) (*domain.BoardResponse, error) {
	if filters == nil {
		filters = &GetBoardFilters{}
	}
	req := filters.SearchTasksRequest
	if filters.Status != nil {
		req.Status = *filters.Status
	}
	if filters.Name != nil && req.Query == "" {
		req.Query = *filters.Name
	}
	if filters.Date != nil {
		start := *filters.Date
		end := start.AddDate(0, 0, 1).Add(-time.Microsecond)
		req.DateFrom = &start
		req.DateTo = &end
	}
	taskFilters, err := parseTaskFilters(req)
	if err != nil {
		return nil, err
	}
	var board *domain.Board
	if taskFilters.BoardID != nil {
		board, err = s.boardRepo.GetBoard(ctx, *taskFilters.BoardID)
	} else {
		owner := requestingUserID
		if filters.UserID != nil {
			owner = *filters.UserID
			if owner <= 0 {
				return nil, apperrors.BadRequest("Некорректный user_id")
			}
		}
		if owner == requestingUserID {
			if err = s.boardRepo.EnsureDefaultBoard(ctx, owner); err != nil {
				return nil, apperrors.InternalWithErr("Не удалось инициализировать доску", err)
			}
		}
		board, err = s.boardRepo.GetBoardByOwnerID(ctx, owner)
	}
	if err != nil {
		return nil, apperrors.NotFound("Доска не найдена")
	}
	if err = s.checkAccess(ctx, board.ID, requestingUserID); err != nil {
		return nil, err
	}
	cols, err := s.boardRepo.ListColumns(ctx, board.ID)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось получить колонки", err)
	}
	taskFilters.BoardID = &board.ID
	tasks, err := s.taskRepo.Search(ctx, requestingUserID, taskFilters)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось получить задачи", err)
	}
	result, err := s.buildBoardResponse(cols, tasks)
	if err != nil {
		return nil, err
	}
	result.Board = board
	return result, nil
}

func (s *KanbanService) buildBoardResponse(cols []*domain.KanbanColumn, tasks []*domain.Task) (*domain.BoardResponse, error) {
	byCol := make(map[uuid.UUID][]*domain.Task)

	for _, t := range tasks {
		byCol[t.ColumnID] = append(byCol[t.ColumnID], t)
	}

	columnsWithTasks := make([]*domain.ColumnWithTasks, 0, len(cols))
	for _, c := range cols {
		tasksForCol := byCol[c.ID]
		if tasksForCol == nil {
			tasksForCol = make([]*domain.Task, 0)
		}

		columnsWithTasks = append(columnsWithTasks, &domain.ColumnWithTasks{
			Column:     c,
			Tasks:      tasksForCol,
			TotalTasks: len(tasksForCol),
		})
	}

	return &domain.BoardResponse{Columns: columnsWithTasks}, nil
}
