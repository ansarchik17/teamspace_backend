package service

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestParseTaskFiltersRejectsInvalidInput(t *testing.T) {
	bad := "not-a-uuid"
	now := time.Now()
	past := now.Add(-time.Hour)
	for _, req := range []SearchTasksRequest{
		{BoardID: &bad}, {ColumnIDs: []string{bad}}, {LabelIDs: []string{bad}}, {SprintIDs: []string{bad}}, {SprintID: &bad},
		{AuthorIDs: []int64{0}}, {AssigneeIDs: []int64{-1}}, {Statuses: []string{" "}}, {Limit: 201}, {Limit: -1}, {Offset: -1}, {DateFrom: &now, DateTo: &past},
	} {
		_, err := parseTaskFilters(req)
		require.Error(t, err, "%+v", req)
	}
}
