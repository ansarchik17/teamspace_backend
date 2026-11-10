package service

import (
	"context"
	"go.uber.org/zap"
	"time"
)

func (s *PublicationService) RunPublicationObjectCleanup(ctx context.Context) {
	if s.fileService == nil {
		return
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		batchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := s.repo.ProcessPublicationObjectCleanup(batchCtx, s.fileService.DeleteFile)
		cancel()
		if err != nil && ctx.Err() == nil {
			s.logger.Warn("publication object cleanup failed", zap.Error(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
