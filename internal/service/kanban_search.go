package service

import (
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// parseTaskFilters is shared by board and list representations.
func parseTaskFilters(req SearchTasksRequest) (domain.TaskFilters, error) {
	f := domain.TaskFilters{Query: strings.TrimSpace(req.Query), AuthorIDs: req.AuthorIDs, AssigneeIDs: req.AssigneeIDs, ColumnKeys: req.ColumnKeys, DateFrom: req.DateFrom, DateTo: req.DateTo, Limit: req.Limit, Offset: req.Offset}
	invalid := func() (domain.TaskFilters, error) {
		return f, apperrors.BadRequest("Некорректные фильтры задач")
	}
	if f.Limit < 0 || f.Limit > 200 || f.Offset < 0 || utf8.RuneCountInString(f.Query) > 256 {
		return invalid()
	}
	if f.Limit == 0 {
		f.Limit = 100
	}
	if f.DateFrom != nil && f.DateTo != nil && f.DateFrom.After(*f.DateTo) {
		return invalid()
	}
	for _, ids := range [][]int64{f.AuthorIDs, f.AssigneeIDs} {
		for _, id := range ids {
			if id <= 0 {
				return invalid()
			}
		}
	}
	for _, p := range req.Priorities {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			return invalid()
		}
		f.Priorities = append(f.Priorities, p)
	}
	statuses := append([]string{}, req.Statuses...)
	if req.Status != "" {
		statuses = append(statuses, req.Status)
	}
	for _, status := range statuses {
		status = strings.ToUpper(strings.TrimSpace(status))
		if status == "" {
			return invalid()
		}
		f.Statuses = append(f.Statuses, status)
		switch normalizeKanbanTaskStatus(status) {
		case domain.ColTODO:
			f.Statuses = append(f.Statuses, domain.ColTODO, domain.StatusTodo)
		case domain.ColInProgress:
			f.Statuses = append(f.Statuses, domain.ColInProgress, domain.StatusWorking)
		case domain.ColTest:
			f.Statuses = append(f.Statuses, domain.ColTest, domain.StatusReview)
		case domain.ColDone:
			f.Statuses = append(f.Statuses, domain.ColDone, domain.StatusDone)
		}
	}
	for _, pair := range []struct {
		src []string
		dst *[]uuid.UUID
	}{{req.ColumnIDs, &f.ColumnIDs}, {req.LabelIDs, &f.LabelIDs}, {req.SprintIDs, &f.SprintIDs}} {
		for _, raw := range pair.src {
			id, err := uuid.Parse(raw)
			if err != nil || id == uuid.Nil {
				return invalid()
			}
			*pair.dst = append(*pair.dst, id)
		}
	}
	if req.BoardID != nil {
		id, err := uuid.Parse(*req.BoardID)
		if err != nil || id == uuid.Nil {
			return invalid()
		}
		f.BoardID = &id
	}
	if req.SprintID != nil {
		id, err := uuid.Parse(*req.SprintID)
		if err != nil || id == uuid.Nil {
			return invalid()
		}
		f.SprintIDs = append(f.SprintIDs, id)
	}
	return f, nil
}
