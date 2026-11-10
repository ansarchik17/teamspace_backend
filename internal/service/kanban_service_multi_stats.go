package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func (s *KanbanService) GetMultiBoardStats(ctx context.Context, userID int64, boardIDs []string, dateFrom, dateTo *time.Time) (*domain.BoardStatsResponse, error) {
	var validBoardIDs []uuid.UUID

	if len(boardIDs) > 0 {
		for _, bid := range boardIDs {
			uid, err := uuid.Parse(bid)
			if err != nil {
				continue
			}
			if err := s.checkAccess(ctx, uid, userID); err == nil {
				validBoardIDs = append(validBoardIDs, uid)
			}
		}
	} else {
		boards, err := s.ListUserBoards(ctx, userID)
		if err != nil {
			return nil, apperrors.InternalWithErr("Не удалось получить список досок", err)
		}
		for _, b := range boards {
			validBoardIDs = append(validBoardIDs, b.ID)
		}
	}

	if len(validBoardIDs) == 0 {
		return &domain.BoardStatsResponse{
			BoardName: "Общая статистика",
			Period: domain.BoardStatsPeriod{
				DateFrom: dateFrom,
				DateTo:   dateTo,
			},
		}, nil
	}

	stats, err := s.taskRepo.GetMultiBoardStats(ctx, validBoardIDs, dateFrom, dateTo)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось получить статистику досок", err)
	}
	return stats, nil
}
