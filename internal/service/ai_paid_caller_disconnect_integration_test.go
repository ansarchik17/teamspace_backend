package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"github.com/west6ide/workspace-backend/internal/repository"
)

func TestAIPaidHTTPDisconnectDurableSlot(t *testing.T) {
	ctx, db, _, org := newAIOperationServiceDB(t)
	data, err := os.ReadFile("../../db/migrations/20261020100800_ai_reservation_reconciliation.sql")
	require.NoError(t, err)
	_, err = db.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0])
	require.NoError(t, err)
	started := make(chan struct{})
	finish := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-finish // A real provider can continue after the HTTP client disconnects.
		w.WriteHeader(http.StatusOK)
	}))
	defer provider.Close()
	defer close(finish)
	uid := int64(1)
	caller := domain.WithAIPaidCaller(ctx, domain.AIPaidCaller{OrganizationID: org, UserID: &uid})
	requestCtx, cancel := context.WithCancel(caller)
	defer cancel()
	newExecutor := func() AIPaidCallerService {
		return NewAIPaidCallerService(repository.NewAIReservationRepository(db, nil, repository.WithAIUserConcurrencyLimit(1)))
	}
	done := make(chan error, 1)
	go func() {
		_, err := newExecutor().ExecutePaidCall(requestCtx, validTestParams(), func(ctx context.Context, _ domain.AIReservation) (any, domain.AISettleRequest, error) {
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.URL, nil)
			if err != nil {
				return nil, domain.AISettleRequest{}, err
			}
			response, err := http.DefaultClient.Do(request)
			if response != nil {
				response.Body.Close()
			}
			return nil, domain.AISettleRequest{}, err
		})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider not reached")
	}
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not finish")
	}
	second := validTestParams()
	second.IdempotencyKey = "after-disconnect"
	dispatched := false
	_, err = newExecutor().ExecutePaidCall(caller, second, func(context.Context, domain.AIReservation) (any, domain.AISettleRequest, error) {
		dispatched = true
		return nil, domain.AISettleRequest{}, nil
	})
	var pending *domain.AIReservationDenied
	require.ErrorAs(t, err, &pending)
	require.Equal(t, "RESERVATION_PENDING", pending.Reason)
	require.Equal(t, "account", pending.Scope, "a lost response is not proof of currently running generation")
	require.False(t, dispatched)
	var status string
	require.NoError(t, db.QueryRow(ctx, `SELECT status FROM ai_usage_attempts LIMIT 1`).Scan(&status))
	require.Equal(t, "unknown", status)
}
