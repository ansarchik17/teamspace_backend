package repository

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
	"sync"
	"testing"
)

type testStorageCapacity struct{}

func (testStorageCapacity) Capacity(context.Context) (domain.StorageCapacity, error) {
	return domain.StorageCapacity{TotalBytes: 1 << 40, FreeBytes: 1 << 40}, nil
}

type limitedStorageCapacity struct{ fail bool }

func (p limitedStorageCapacity) Capacity(context.Context) (domain.StorageCapacity, error) {
	if p.fail {
		return domain.StorageCapacity{}, errors.New("private MinIO error")
	}
	return domain.StorageCapacity{TotalBytes: 1000, FreeBytes: 1000, ReserveBytes: 100}, nil
}
func TestAdministrationMinIOCapacity(t *testing.T) {
	ctx, db, a, b := newAdministrationStorageTestDB(t)
	r := NewOrganizationRepository(db, zap.NewNop())
	r.SetStorageCapacityProvider(limitedStorageCapacity{})
	req := func(q, rev int64) domain.AdministrationStorageRequest {
		return domain.AdministrationStorageRequest{QuotaBytes: &q, Revision: &rev}
	}
	_, err := r.AdministrationStorageCapacity(ctx, 2, a)
	require.Error(t, err, "director cannot read platform capacity")
	cap, err := r.AdministrationStorageCapacity(ctx, 1, a)
	require.NoError(t, err)
	require.Equal(t, int64(700), cap.MaxQuotaBytes, "unconfigured companies reserve existing allocations")
	require.Error(t, r.SetAdministrationStorage(ctx, 1, &a, req(701, 0)))
	// Both grants fit separately, but must be serialized across companies.
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, org := range []struct{ target bool }{{true}, {false}} {
		wg.Add(1)
		go func(first bool) {
			defer wg.Done()
			<-start
			id := a
			if !first {
				id = b
			}
			results <- r.SetAdministrationStorage(ctx, 1, &id, req(600, 0))
		}(org.target)
	}
	close(start)
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	require.Equal(t, 1, success)
	var audits int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM organization_admin_audit WHERE action='storage.quota.update'`).Scan(&audits))
	require.Equal(t, 1, audits)
	r.SetStorageCapacityProvider(limitedStorageCapacity{fail: true})
	_, err = r.AdministrationStorageCapacity(ctx, 1, a)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private MinIO error")
	state, err := r.AdministrationStorage(ctx, 1, &a)
	require.NoError(t, err)
	err = r.SetAdministrationStorage(ctx, 1, &a, req(650, state.Revision))
	require.NoError(t, err, "unavailable metrics must not block logical budget updates")
	r.SetStorageCapacityProvider(nil)
	_, err = r.AdministrationStorageCapacity(ctx, 1, a)
	require.Error(t, err)
}
