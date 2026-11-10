package service

import (
	"context"
	"time"

	"go.uber.org/zap"
)

const eventPublishTimeout = 3 * time.Second

func publishEventAsync(logger *zap.Logger, publisher EventPublisher, routingKey string, payload any, fields ...zap.Field) {
	if publisher == nil {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), eventPublishTimeout)
		defer cancel()

		if err := publisher.Publish(ctx, routingKey, payload); err != nil {
			baseFields := []zap.Field{
				zap.String("routing_key", routingKey),
				zap.Error(err),
			}
			baseFields = append(baseFields, fields...)
			logger.Error("Failed to publish event", baseFields...)
		}
	}()
}
