package events

import "context"

type NoopPublisher struct{}

func NewNoopPublisher() Publisher {
	return NoopPublisher{}
}

func (NoopPublisher) Publish(context.Context, string, any) error {
	return nil
}

func (NoopPublisher) Close() error {
	return nil
}
