package events

import "context"

type Publisher interface {
	Publish(ctx context.Context, routingKey string, data any) error
	Close() error
}
