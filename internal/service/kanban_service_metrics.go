package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

// ── 1. SLA Compliance ────────────────────────────────────────────────────────

func (s *KanbanService) GetSLACompliance(ctx context.Context, userID int64, boardID string, period string) (*domain.SLAComplianceResponse, error) {
	boardIDs, err := s.resolveMetricBoardIDs(ctx, userID, boardID)
	if err != nil {
		return nil, err
	}
	res, err := s.taskRepo.GetSLACompliance(ctx, boardIDs, boardID, period)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось получить SLA метрики", err)
	}
	return res, nil
}

// ── 2. Cycle Time ────────────────────────────────────────────────────────────

func (s *KanbanService) GetCycleTime(ctx context.Context, userID int64, boardID string, period string) (*domain.CycleTimeResponse, error) {
	boardIDs, err := s.resolveMetricBoardIDs(ctx, userID, boardID)
	if err != nil {
		return nil, err
	}
	res, err := s.taskRepo.GetCycleTime(ctx, boardIDs, boardID, period)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось получить Cycle Time", err)
	}
	return res, nil
}

// ── 3. Overdue Tasks ─────────────────────────────────────────────────────────

func (s *KanbanService) GetOverdueTasks(ctx context.Context, userID int64, boardID string) (*domain.OverdueTasksResponse, error) {
	boardIDs, err := s.resolveMetricBoardIDs(ctx, userID, boardID)
	if err != nil {
		return nil, err
	}
	res, err := s.taskRepo.GetOverdueTasks(ctx, boardIDs)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось получить просроченные задачи", err)
	}
	return res, nil
}

// ── 4. Reopen Rate ───────────────────────────────────────────────────────────

func (s *KanbanService) GetReopenRate(ctx context.Context, userID int64, boardID string, period string) (*domain.ReopenRateResponse, error) {
	boardIDs, err := s.resolveMetricBoardIDs(ctx, userID, boardID)
	if err != nil {
		return nil, err
	}
	res, err := s.taskRepo.GetReopenRate(ctx, boardIDs, boardID, period)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось получить Reopen Rate", err)
	}
	return res, nil
}

// ── 5. Time in Status ────────────────────────────────────────────────────────

func (s *KanbanService) GetTimeInStatus(ctx context.Context, userID int64, boardID string, period string) (*domain.TimeInStatusResponse, error) {
	boardIDs, err := s.resolveMetricBoardIDs(ctx, userID, boardID)
	if err != nil {
		return nil, err
	}
	res, err := s.taskRepo.GetTimeInStatus(ctx, boardIDs, boardID, period)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось получить Time-in-Status", err)
	}
	return res, nil
}

// ── 6. Workload Distribution ─────────────────────────────────────────────────

func (s *KanbanService) GetWorkloadDistribution(ctx context.Context, userID int64, boardID string, groupBy string) (*domain.WorkloadDistributionResponse, error) {
	boardIDs, err := s.resolveMetricBoardIDs(ctx, userID, boardID)
	if err != nil {
		return nil, err
	}
	res, err := s.taskRepo.GetWorkloadDistribution(ctx, boardIDs, boardID, groupBy)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось получить распределение нагрузки", err)
	}
	return res, nil
}

// ── 7. Capacity ──────────────────────────────────────────────────────────────

func (s *KanbanService) GetCapacity(ctx context.Context, userID int64, boardID string) (*domain.CapacityResponse, error) {
	start := time.Now()
	boardIDs, err := s.resolveMetricBoardIDs(ctx, userID, boardID)
	if err != nil {
		return nil, err
	}
	boardsResolvedAt := time.Now()
	res, err := s.taskRepo.GetCapacity(ctx, boardIDs, boardID)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось получить данные по capacity", err)
	}
	s.logger.Info("kanban_capacity_built",
		zap.Int64("user_id", userID),
		zap.String("board_id", boardID),
		zap.Int("boards_count", len(boardIDs)),
		zap.Int64("boards_resolve_ms", boardsResolvedAt.Sub(start).Milliseconds()),
		zap.Int64("total_ms", time.Since(start).Milliseconds()),
	)
	return res, nil
}

// ── 8. Stale Tasks ───────────────────────────────────────────────────────────

func (s *KanbanService) GetStaleTasks(ctx context.Context, userID int64, boardID string, thresholdDays int) (*domain.StaleTasksResponse, error) {
	start := time.Now()
	boardIDs, err := s.resolveMetricBoardIDs(ctx, userID, boardID)
	if err != nil {
		return nil, err
	}
	boardsResolvedAt := time.Now()
	res, err := s.taskRepo.GetStaleTasks(ctx, boardIDs, thresholdDays)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось получить зависшие задачи", err)
	}
	s.logger.Info("kanban_stale_tasks_built",
		zap.Int64("user_id", userID),
		zap.String("board_id", boardID),
		zap.Int("boards_count", len(boardIDs)),
		zap.Int("threshold_days", thresholdDays),
		zap.Int("tasks_count", res.Count),
		zap.Int64("boards_resolve_ms", boardsResolvedAt.Sub(start).Milliseconds()),
		zap.Int64("total_ms", time.Since(start).Milliseconds()),
	)
	return res, nil
}

// ── 9. Burn-Down ─────────────────────────────────────────────────────────────

func (s *KanbanService) GetBurnDown(ctx context.Context, userID int64, boardID string, from, to time.Time) (*domain.BurnDownResponse, error) {
	if err := s.checkMetricsAccess(ctx, userID, boardID); err != nil {
		return nil, err
	}
	if boardID == "" {
		return nil, apperrors.BadRequest("board_id обязателен для Burn-down chart")
	}
	res, err := s.taskRepo.GetBurnDown(ctx, boardID, from, to)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось получить Burn-down данные", err)
	}
	return res, nil
}

// ── 10. Throughput ───────────────────────────────────────────────────────────

func (s *KanbanService) GetThroughput(ctx context.Context, userID int64, boardID string, period string) (*domain.ThroughputResponse, error) {
	boardIDs, err := s.resolveMetricBoardIDs(ctx, userID, boardID)
	if err != nil {
		return nil, err
	}
	res, err := s.taskRepo.GetThroughput(ctx, boardIDs, boardID, period)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось получить Throughput данные", err)
	}
	return res, nil
}

// ── 11. Efficiency Trend ─────────────────────────────────────────────────────

func (s *KanbanService) GetEfficiencyTrend(ctx context.Context, userID int64, boardID string, period string) (*domain.EfficiencyTrendResponse, error) {
	boardIDs, err := s.resolveMetricBoardIDs(ctx, userID, boardID)
	if err != nil {
		return nil, err
	}
	res, err := s.taskRepo.GetEfficiencyTrend(ctx, boardIDs, boardID, period)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось получить Efficiency Trend", err)
	}
	return res, nil
}

// ── access helper ─────────────────────────────────────────────────────────────

// checkMetricsAccess разрешает доступ к метрикам:
//   - boardID не задан → пользователь видит агрегат по своим доскам
//   - boardID задан    → проверяем членство на доске
func (s *KanbanService) checkMetricsAccess(ctx context.Context, userID int64, boardID string) error {
	if boardID == "" {
		return nil
	}
	bid, err := uuid.Parse(boardID)
	if err != nil {
		return apperrors.BadRequest("Неверный формат board_id")
	}
	return s.checkAccess(ctx, bid, userID)
}

func (s *KanbanService) resolveMetricBoardIDs(ctx context.Context, userID int64, boardID string) ([]uuid.UUID, error) {
	if boardID != "" {
		bid, err := uuid.Parse(boardID)
		if err != nil {
			return nil, apperrors.BadRequest("Неверный формат board_id")
		}
		if err := s.checkAccess(ctx, bid, userID); err != nil {
			return nil, err
		}
		return []uuid.UUID{bid}, nil
	}

	boards, err := s.boardRepo.ListBoards(ctx, userID)
	if err != nil {
		return nil, apperrors.InternalWithErr("Не удалось получить доступные доски", err)
	}
	boardIDs := make([]uuid.UUID, 0, len(boards))
	for _, board := range boards {
		if board != nil {
			boardIDs = append(boardIDs, board.ID)
		}
	}
	return boardIDs, nil
}
