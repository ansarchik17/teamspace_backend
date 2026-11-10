package service

import (
	"context"
	"sync"
	"time"

	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
	"go.uber.org/zap"
)

type DashboardService struct {
	repo   *repository.DashboardRepository
	logger *zap.Logger
}

func NewDashboardService(repo *repository.DashboardRepository, logger *zap.Logger) *DashboardService {
	return &DashboardService{
		repo:   repo,
		logger: logger,
	}
}

func (s *DashboardService) GetSummary(ctx context.Context, userID int64) (*domain.DashboardSummaryResponse, error) {
	start := time.Now()
	now := time.Now()

	var (
		activeCount int
		activeTrend int
		activeSpark []int

		mtCount int
		mtTrend int

		docCount int
		docTrend int

		wpCount  int
		wpTrend  int
		tpPoints int

		avgLoad     float64
		currentData []int

		totalTasks int
		taskStats  []domain.DashboardTaskStat

		compCompleted int
		compTotal     int
		compPerc      int
	)

	var wg sync.WaitGroup
	wg.Add(7)

	go func() {
		defer wg.Done()
		var err error
		activeCount, activeTrend, activeSpark, err = s.repo.GetActiveTasksStats(ctx, userID, now)
		if err != nil {
			s.logger.Error("failed to get active tasks stats", zap.Error(err))
		}
	}()

	go func() {
		defer wg.Done()
		var err error
		mtCount, mtTrend, err = s.repo.GetMeetingsTodayStats(ctx, userID, now)
		if err != nil {
			s.logger.Error("failed to get meetings stats", zap.Error(err))
		}
	}()

	go func() {
		defer wg.Done()
		var err error
		docCount, docTrend, err = s.repo.GetDocumentsWeeklyStats(ctx, userID, now)
		if err != nil {
			s.logger.Error("failed to get documents stats", zap.Error(err))
		}
	}()

	go func() {
		defer wg.Done()
		var err error
		wpCount, wpTrend, tpPoints, err = s.repo.GetPointsStats(ctx, userID, now)
		if err != nil {
			s.logger.Error("failed to get points stats", zap.Error(err))
		}
	}()

	go func() {
		defer wg.Done()
		var err error
		avgLoad, currentData, err = s.repo.GetEfficiencyDynamics(ctx, userID, now)
		if err != nil {
			s.logger.Error("failed to get efficiency dynamics", zap.Error(err))
		}
	}()

	go func() {
		defer wg.Done()
		var err error
		totalTasks, taskStats, err = s.repo.GetTaskStructure(ctx, userID)
		if err != nil {
			s.logger.Error("failed to get task structure", zap.Error(err))
		}
	}()

	go func() {
		defer wg.Done()
		var err error
		compCompleted, compTotal, compPerc, err = s.repo.GetCompletionProgress(ctx, userID)
		if err != nil {
			s.logger.Error("failed to get completion progress", zap.Error(err))
		}
	}()

	wg.Wait()

	resp := &domain.DashboardSummaryResponse{
		Summary: domain.DashboardSummary{
			ActiveTasks: domain.DashboardMetric{
				Count:           activeCount,
				TrendPercentage: activeTrend,
				Sparkline:       activeSpark,
			},
			MeetingsToday: domain.DashboardMetric{
				Count:           mtCount,
				TrendPercentage: mtTrend,
			},
			Documents: domain.DashboardMetric{
				Count:           docCount,
				TrendPercentage: docTrend,
			},
			WeeklyPoints: domain.DashboardPoints{
				Count:           wpCount,
				TrendPercentage: wpTrend,
				TodayPoints:     tpPoints,
			},
		},
		EfficiencyDynamics: domain.DashboardEfficiencyDynamics{
			AverageLoad: avgLoad,
			Labels:      []string{"Пн", "Вт", "Ср", "Чт", "Пт", "Сб", "Вс"},
			Datasets: []domain.DashboardDataset{
				{
					Name: "Текущая",
					Data: currentData,
				},
				{
					Name: "Ориентир",
					Data: []int{15, 15, 15, 15, 15, 15, 15}, // Standard benchmark
				},
			},
		},
		TaskStructure: domain.DashboardTaskStructure{
			TotalCount: totalTasks,
			Stats:      taskStats,
		},
		CompletionProgress: domain.DashboardCompletionProgress{
			CompletedCount: compCompleted,
			TotalCount:     compTotal,
			Percentage:     compPerc,
		},
	}

	s.logger.Info("dashboard_summary_built",
		zap.Int64("user_id", userID),
		zap.Int64("total_ms", time.Since(start).Milliseconds()),
	)

	return resp, nil
}
