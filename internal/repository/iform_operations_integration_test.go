package repository

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
)

func TestIFormOperationReceiptsSerializeRetriesAndDoNotCachePendingPolls(t *testing.T) {
	dsn := os.Getenv("IFORM_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set IFORM_TEST_DATABASE_URL to disposable PostgreSQL")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "iform_operations_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(ctx, `CREATE TABLE organizations(id uuid PRIMARY KEY);CREATE TABLE users(id bigint PRIMARY KEY);INSERT INTO users VALUES(1),(2)`); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../db/migrations/20261019000100_iform_operation_receipts.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	company := uuid.New()
	if _, err = db.Exec(ctx, `INSERT INTO organizations VALUES($1)`, company); err != nil {
		t.Fatal(err)
	}
	repo := NewOrganizationRepository(db, zap.NewNop())
	op := domain.IFormOperation{OrganizationID: company, RequestID: uuid.New(), StepID: "createDocument", Occurrence: 1, Operation: "workspace.odo.create"}
	var calls atomic.Int32
	var wg sync.WaitGroup
	run := func() (json.RawMessage, error) {
		calls.Add(1)
		return json.RawMessage(`{"state":"SUCCEEDED","document_id":"stable"}`), nil
	}
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, e := repo.IFormOperationReceipt(ctx, 1, op, "same-hash", run)
			if e != nil || !strings.Contains(string(got), "stable") {
				t.Errorf("receipt %s %v", got, e)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("external calls=%d", calls.Load())
	}
	if _, err = repo.IFormOperationReceipt(ctx, 1, op, "changed-hash", run); err == nil {
		t.Fatal("changed payload reused receipt")
	}
	if _, err = repo.IFormOperationReceipt(ctx, 2, op, "same-hash", run); err == nil {
		t.Fatal("different actor reused receipt")
	}
	op.StepID = "waitDocument"
	op.Operation = "workspace.odo.wait"
	polls := 0
	poll := func() (json.RawMessage, error) {
		polls++
		if polls == 2 {
			return nil, errors.New("synthetic temporary failure")
		}
		if polls == 3 {
			return json.RawMessage(`{"state":"WAITING","outcome":"RETURNED","document_id":"stable"}`), nil
		}
		if polls < 4 {
			return json.RawMessage(`{"state":"WAITING"}`), nil
		}
		return json.RawMessage(`{"state":"SUCCEEDED"}`), nil
	}
	for i := 0; i < 5; i++ {
		if _, err = repo.IFormOperationReceipt(ctx, 1, op, "poll-hash", poll); err != nil {
			if i == 1 {
				continue
			}
			t.Fatal(err)
		}
	}
	if polls != 4 {
		t.Fatalf("polls=%d, expected pending polls to run and final result to be cached", polls)
	}
	op.StepID = "createEmployee"
	op.Operation = "workspace.employee.create"
	if _, err = repo.IFormOperationReceipt(ctx, 1, op, "employee-hash", func() (json.RawMessage, error) { return json.RawMessage(`{"state":"SUCCEEDED","employee_id":24}`), nil }); err != nil {
		t.Fatal(err)
	}
	if id, err := repo.IFormOnboardingEmployee(ctx, company, op.RequestID, 1); err != nil || id != 24 {
		t.Fatalf("employee receipt: %d %v", id, err)
	}
	if _, err := repo.IFormOnboardingEmployee(ctx, company, op.RequestID, 2); err == nil {
		t.Fatal("different HR claimed employee receipt")
	}
	if _, err := repo.IFormOnboardingEmployee(ctx, uuid.New(), op.RequestID, 1); err == nil {
		t.Fatal("foreign company claimed employee receipt")
	}
}
