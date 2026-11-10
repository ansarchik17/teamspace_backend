package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type aiBudgetObservation struct {
	scope             string
	configured        bool
	limit, used, held int64
}

func recordAIBudgetObservation(ctx context.Context, tx pgx.Tx, attempt uuid.UUID, requested int64, observation aiBudgetObservation) error {
	var limit *int64
	if observation.configured {
		limit = &observation.limit
	}
	_, err := tx.Exec(ctx, `INSERT INTO ai_usage_budget_observations(attempt_id,scope,limit_microcredits,used_microcredits,held_microcredits,requested_microcredits) VALUES($1,$2,$3,$4,$5,$6)`, attempt, observation.scope, limit, observation.used, observation.held, requested)
	return err
}
