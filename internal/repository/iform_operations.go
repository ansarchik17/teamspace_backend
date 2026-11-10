package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/west6ide/workspace-backend/internal/apperrors"
	"github.com/west6ide/workspace-backend/internal/domain"
)

// Only the original HR actor can initialize the employee created by this request.
func (r *OrganizationRepository) IFormOnboardingEmployee(ctx context.Context, company, requestID uuid.UUID, actor int64) (int64, error) {
	var response []byte
	err := r.db.QueryRow(ctx, `SELECT response FROM iform_operation_receipts WHERE organization_id=$1 AND request_id=$2 AND actor_id=$3 AND operation='workspace.employee.create' AND response->>'state'='SUCCEEDED' ORDER BY created_at DESC LIMIT 1`, company, requestID, actor).Scan(&response)
	if err != nil {
		return 0, apperrors.NotFound("Профиль этой заявки не найден")
	}
	var result struct {
		EmployeeID int64 `json:"employee_id"`
	}
	if json.Unmarshal(response, &result) != nil || result.EmployeeID <= 0 {
		return 0, apperrors.Internal("Invalid onboarding receipt")
	}
	return result.EmployeeID, nil
}

// Target systems deduplicate by the same stable key. This receipt avoids a new
// external call after success, while the transaction serializes concurrent calls.
func (r *OrganizationRepository) IFormOperationReceipt(ctx context.Context, actor int64, op domain.IFormOperation, hash string, run func() (json.RawMessage, error)) (json.RawMessage, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "iform-operation:"+op.Key()); err != nil {
		return nil, err
	}
	var savedHash, kind string
	var savedActor int64
	var response json.RawMessage
	err = tx.QueryRow(ctx, `SELECT actor_id,operation,request_hash,response FROM iform_operation_receipts WHERE organization_id=$1 AND request_id=$2 AND step_id=$3 AND occurrence=$4`, op.OrganizationID, op.RequestID, op.StepID, op.Occurrence).Scan(&savedActor, &kind, &savedHash, &response)
	if err == nil {
		if savedActor != actor || kind != op.Operation || savedHash != hash {
			return nil, apperrors.Conflict("Ключ операции iForm уже использован с другими параметрами")
		}
		return response, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	response, err = run()
	if err != nil {
		return nil, err
	}
	var state struct {
		State string `json:"state"`
	}
	if json.Unmarshal(response, &state) != nil {
		return nil, apperrors.Internal("Invalid iForm operation result")
	}
	// Polls are not cached while the target is still running.
	if state.State == "WAITING" {
		return response, nil
	}
	_, err = tx.Exec(ctx, `INSERT INTO iform_operation_receipts(organization_id,request_id,step_id,occurrence,actor_id,operation,request_hash,response) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, op.OrganizationID, op.RequestID, op.StepID, op.Occurrence, actor, op.Operation, hash, response)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return response, nil
}
