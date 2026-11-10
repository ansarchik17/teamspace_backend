package repository

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	"testing"
)

type auditWriteTestTx struct {
	pgx.Tx
	args []any
	err  error
}

func (tx *auditWriteTestTx) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	tx.args = args
	return pgconn.NewCommandTag("INSERT 0 1"), tx.err
}

func TestAdministrationAuditWriterPreservesFailureAndNullSnapshots(t *testing.T) {
	tx := &auditWriteTestTx{}
	org := uuid.New()
	require.NoError(t, writeAdministrationAudit(context.Background(), tx, org, 7, "organization.create", "organization", org.String(), nil, json.RawMessage(`{"name":"Company"}`)))
	require.Equal(t, org, tx.args[0])
	require.Equal(t, int64(7), tx.args[1])
	require.JSONEq(t, "null", string(tx.args[5].(json.RawMessage)))
	tx.err = errors.New("audit insert failed")
	require.ErrorIs(t, writeAdministrationAudit(context.Background(), tx, org, 7, "organization.update", "organization", org.String(), nil, nil), tx.err)
}
