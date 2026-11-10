package service

import (
	"context"
	"go.uber.org/zap"
	"time"
)

func (s *OrganizationService) StartIdentityDeletionWorker(ctx context.Context, remove func(context.Context, string) error) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			for i := 0; i < 10 && ctx.Err() == nil; i++ {
				callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				processed, err := s.repo.ProcessIdentityDeletion(callCtx, remove)
				cancel()
				if err != nil {
					s.logger.Warn("Retired identity cleanup deferred", zap.Error(err))
					break
				}
				if !processed {
					break
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
