package service

import (
	"context"
	"github.com/google/uuid"
	"github.com/west6ide/workspace-backend/internal/domain"
	"go.uber.org/zap"
	"time"
)

func (s *PublicationService) RunScheduledPublications(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		batchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := s.repo.PublishDueWithIDs(batchCtx)
		cancel()
		if err != nil && ctx.Err() == nil {
			s.logger.Warn("scheduled publication batch failed", zap.Error(err))
		}
		archiveCtx, cancelArchive := context.WithTimeout(ctx, 10*time.Second)
		archivedIDs, err := s.repo.ArchiveDueWithIDs(archiveCtx)
		cancelArchive()
		if err != nil && ctx.Err() == nil {
			s.logger.Warn("auto-archive publication batch failed", zap.Error(err))
		} else if len(archivedIDs) > 0 {
			s.logger.Info("auto-archived obsolete publications", zap.Int("count", len(archivedIDs)))
		}
		pending, err := s.repo.PendingBroadcasts(ctx)
		if err != nil && ctx.Err() == nil {
			s.logger.Warn("load pending publication broadcasts failed", zap.Error(err))
		}
		for _, job := range pending {
			s.sendQueuedBroadcast(ctx, job.PublicationID, job.TriggeredBy)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *PublicationService) sendQueuedBroadcast(ctx context.Context, id uuid.UUID, triggeredBy int64) {
	batchCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	_, err := s.BroadcastPublication(batchCtx, id, triggeredBy, domain.BroadcastPublicationRequest{})
	if err != nil {
		s.logger.Warn("publication broadcast failed; queued for retry", zap.String("pub_id", id.String()), zap.Error(err))
		if retryErr := s.repo.RetryQueuedBroadcast(ctx, id, err); retryErr != nil {
			s.logger.Warn("schedule broadcast retry failed", zap.Error(retryErr))
		}
		return
	}
	if err := s.repo.CompleteQueuedBroadcast(ctx, id); err != nil {
		s.logger.Warn("complete publication broadcast queue failed", zap.Error(err))
	}
}
