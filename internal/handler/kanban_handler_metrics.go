package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/middleware"
)

// GetSLACompliance godoc
// @Summary Получить SLA compliance по задачам
// @Description Возвращает процент задач, закрытых вовремя, и количество закрытых с просрочкой за период. Если board_id не передан, считает по доступным пользователю доскам.
// @Tags metrics
// @Security BearerAuth
// @Produce json
// @Param board_id query string false "ID доски (UUID)"
// @Param period query string false "Период" Enums(day, week, month) default(week)
// @Success 200 {object} domain.SLAComplianceResponse
// @Failure 400 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /metrics/sla-compliance [get]
func (h *KanbanHandler) GetSLACompliance(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	period, ok := metricsPeriod(c)
	if !ok {
		return
	}
	res, err := h.svc.GetSLACompliance(c.Request.Context(), userID, c.Query("board_id"), period)
	writeKanbanMetricsResponse(c, res, err)
}

// GetCycleTime godoc
// @Summary Получить cycle time по задачам
// @Description Возвращает среднее и медианное время выполнения задач до перехода в финальный статус за период. Если board_id не передан, считает по доступным пользователю доскам.
// @Tags metrics
// @Security BearerAuth
// @Produce json
// @Param board_id query string false "ID доски (UUID)"
// @Param period query string false "Период" Enums(day, week, month) default(week)
// @Success 200 {object} domain.CycleTimeResponse
// @Failure 400 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /metrics/cycle-time [get]
func (h *KanbanHandler) GetCycleTime(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	period, ok := metricsPeriod(c)
	if !ok {
		return
	}
	res, err := h.svc.GetCycleTime(c.Request.Context(), userID, c.Query("board_id"), period)
	writeKanbanMetricsResponse(c, res, err)
}

// GetOverdueTasks godoc
// @Summary Получить просроченные задачи
// @Description Возвращает список задач, у которых due_at меньше текущего времени и задача не находится в финальном статусе. Если board_id не передан, ищет по доступным пользователю доскам.
// @Tags metrics
// @Security BearerAuth
// @Produce json
// @Param board_id query string false "ID доски (UUID)"
// @Success 200 {object} domain.OverdueTasksResponse
// @Failure 400 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /tasks/overdue [get]
func (h *KanbanHandler) GetOverdueTasks(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	res, err := h.svc.GetOverdueTasks(c.Request.Context(), userID, c.Query("board_id"))
	writeKanbanMetricsResponse(c, res, err)
}

// GetReopenRate godoc
// @Summary Получить reopen rate по задачам
// @Description Возвращает количество и процент задач, которые возвращались с проверки обратно в работу за период. Если board_id не передан, считает по доступным пользователю доскам.
// @Tags metrics
// @Security BearerAuth
// @Produce json
// @Param board_id query string false "ID доски (UUID)"
// @Param period query string false "Период" Enums(day, week, month) default(week)
// @Success 200 {object} domain.ReopenRateResponse
// @Failure 400 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /metrics/reopen-rate [get]
func (h *KanbanHandler) GetReopenRate(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	period, ok := metricsPeriod(c)
	if !ok {
		return
	}
	res, err := h.svc.GetReopenRate(c.Request.Context(), userID, c.Query("board_id"), period)
	writeKanbanMetricsResponse(c, res, err)
}

// GetTimeInStatus godoc
// @Summary Получить распределение времени по статусам
// @Description Возвращает среднее время пребывания задач в статусах за период для построения воронки. Если board_id не передан, считает по доступным пользователю доскам.
// @Tags metrics
// @Security BearerAuth
// @Produce json
// @Param board_id query string false "ID доски (UUID)"
// @Param period query string false "Период" Enums(day, week, month) default(week)
// @Success 200 {object} domain.TimeInStatusResponse
// @Failure 400 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /metrics/time-in-status [get]
func (h *KanbanHandler) GetTimeInStatus(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	period, ok := metricsPeriod(c)
	if !ok {
		return
	}
	res, err := h.svc.GetTimeInStatus(c.Request.Context(), userID, c.Query("board_id"), period)
	writeKanbanMetricsResponse(c, res, err)
}

// GetWorkloadDistribution godoc
// @Summary Получить распределение текущей нагрузки
// @Description Возвращает агрегированные данные для pie chart по текущим незавершённым задачам. group_by поддерживает priority, status и type. Если board_id не передан, считает по доступным пользователю доскам.
// @Tags metrics
// @Security BearerAuth
// @Produce json
// @Param board_id query string false "ID доски (UUID)"
// @Param group_by query string false "Группировка" Enums(priority, status, type) default(priority)
// @Success 200 {object} domain.WorkloadDistributionResponse
// @Failure 400 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /metrics/workload-distribution [get]
func (h *KanbanHandler) GetWorkloadDistribution(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	groupBy := c.DefaultQuery("group_by", "priority")
	res, err := h.svc.GetWorkloadDistribution(c.Request.Context(), userID, c.Query("board_id"), groupBy)
	writeKanbanMetricsResponse(c, res, err)
}

// GetCapacity godoc
// @Summary Получить capacity по исполнителям
// @Description Возвращает текущую загрузку исполнителей по незавершённым задачам относительно WIP-лимита. Если board_id не передан, считает по доступным пользователю доскам.
// @Tags metrics
// @Security BearerAuth
// @Produce json
// @Param board_id query string false "ID доски (UUID)"
// @Success 200 {object} domain.CapacityResponse
// @Failure 400 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /metrics/capacity [get]
func (h *KanbanHandler) GetCapacity(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	res, err := h.svc.GetCapacity(c.Request.Context(), userID, c.Query("board_id"))
	writeKanbanMetricsResponse(c, res, err)
}

// GetStaleTasks godoc
// @Summary Получить зависшие задачи
// @Description Возвращает список незавершённых задач без активности дольше threshold_days дней. Если board_id не передан, ищет по доступным пользователю доскам.
// @Tags metrics
// @Security BearerAuth
// @Produce json
// @Param board_id query string false "ID доски (UUID)"
// @Param threshold_days query int false "Порог отсутствия активности в днях" default(7)
// @Success 200 {object} domain.StaleTasksResponse
// @Failure 400 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /tasks/stale [get]
func (h *KanbanHandler) GetStaleTasks(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	thresholdDays := 7
	if raw := c.Query("threshold_days"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			respondJSON(c, http.StatusBadRequest, gin.H{"error": "threshold_days должен быть положительным числом"})
			return
		}
		thresholdDays = value
	}
	res, err := h.svc.GetStaleTasks(c.Request.Context(), userID, c.Query("board_id"), thresholdDays)
	writeKanbanMetricsResponse(c, res, err)
}

// GetBurnDown godoc
// @Summary Получить burn-down график
// @Description Возвращает точки графика с количеством оставшихся задач по дням для выбранной доски. board_id обязателен.
// @Tags metrics
// @Security BearerAuth
// @Produce json
// @Param board_id query string true "ID доски (UUID)"
// @Param date_from query string false "Начальная дата (YYYY-MM-DD или RFC3339)"
// @Param date_to query string false "Конечная дата (YYYY-MM-DD или RFC3339)"
// @Success 200 {object} domain.BurnDownResponse
// @Failure 400 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /metrics/burn-down [get]
func (h *KanbanHandler) GetBurnDown(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	boardID := c.Query("board_id")
	if boardID == "" {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "board_id обязателен"})
		return
	}

	dateTo, err := parseOptionalKanbanStatsTime(c.Query("date_to"), true)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат date_to"})
		return
	}
	to := time.Now().UTC()
	if dateTo != nil {
		to = *dateTo
	}

	dateFrom, err := parseOptionalKanbanStatsTime(c.Query("date_from"), false)
	if err != nil {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "Неверный формат date_from"})
		return
	}
	from := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -6)
	if dateFrom != nil {
		from = *dateFrom
	}
	if from.After(to) {
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "date_from не может быть позже date_to"})
		return
	}

	res, err := h.svc.GetBurnDown(c.Request.Context(), userID, boardID, from, to)
	writeKanbanMetricsResponse(c, res, err)
}

// GetThroughput godoc
// @Summary Получить throughput по задачам
// @Description Возвращает количество созданных и закрытых задач по дням за период. Если board_id не передан, считает по доступным пользователю доскам.
// @Tags metrics
// @Security BearerAuth
// @Produce json
// @Param board_id query string false "ID доски (UUID)"
// @Param period query string false "Период" Enums(day, week, month) default(week)
// @Success 200 {object} domain.ThroughputResponse
// @Failure 400 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /metrics/throughput [get]
func (h *KanbanHandler) GetThroughput(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	period, ok := metricsPeriod(c)
	if !ok {
		return
	}
	res, err := h.svc.GetThroughput(c.Request.Context(), userID, c.Query("board_id"), period)
	writeKanbanMetricsResponse(c, res, err)
}

// GetEfficiencyTrend godoc
// @Summary Получить тренд эффективности
// @Description Возвращает точки линейного графика эффективности за период: завершённые задачи, активные задачи и процент эффективности. Если board_id не передан, считает по доступным пользователю доскам.
// @Tags metrics
// @Security BearerAuth
// @Produce json
// @Param board_id query string false "ID доски (UUID)"
// @Param period query string false "Период" Enums(day, week, month) default(week)
// @Success 200 {object} domain.EfficiencyTrendResponse
// @Failure 400 {object} map[string]any
// @Failure 403 {object} map[string]any
// @Failure 500 {object} map[string]any
// @Router /metrics/efficiency-trend [get]
func (h *KanbanHandler) GetEfficiencyTrend(c *gin.Context) {
	userID := middleware.MustGetUserID(c)
	period, ok := metricsPeriod(c)
	if !ok {
		return
	}
	res, err := h.svc.GetEfficiencyTrend(c.Request.Context(), userID, c.Query("board_id"), period)
	writeKanbanMetricsResponse(c, res, err)
}

func metricsPeriod(c *gin.Context) (string, bool) {
	period := c.DefaultQuery("period", "week")
	switch period {
	case "day", "week", "month":
		return period, true
	default:
		respondJSON(c, http.StatusBadRequest, gin.H{"error": "period должен быть одним из: day, week, month"})
		return "", false
	}
}

func writeKanbanMetricsResponse(c *gin.Context, res any, err error) {
	if err != nil {
		app := apperrors.ToAppError(err)
		respondJSON(c, app.StatusCode, app)
		return
	}
	respondJSON(c, http.StatusOK, res)
}
