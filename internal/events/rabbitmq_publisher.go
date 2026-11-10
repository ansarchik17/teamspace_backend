package events

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rabbitmq/amqp091-go"
	"github.com/west6ide/workspace-backend/internal/config"
)

type RabbitMQPublisher struct {
	channel  *amqp091.Channel
	conn     *amqp091.Connection
	exchange string
	mu       sync.Mutex
	producer string
}

func NewRabbitMQPublisher(cfg config.RabbitMQConfig) (Publisher, error) {
	conn, err := amqp091.Dial(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("dial rabbitmq: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open rabbitmq channel: %w", err)
	}

	if err := ch.ExchangeDeclare(
		cfg.Exchange,
		"topic",
		true,
		false,
		false,
		false,
		nil,
	); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("declare rabbitmq exchange: %w", err)
	}

	return &RabbitMQPublisher{
		channel:  ch,
		conn:     conn,
		exchange: cfg.Exchange,
		producer: cfg.Producer,
	}, nil
}

func (p *RabbitMQPublisher) Publish(ctx context.Context, routingKey string, data any) error {
	payload, err := json.Marshal(Event{
		Meta: EventMeta{
			EventID:   uuid.NewString(),
			EventType: routingKey,
			Timestamp: time.Now().UTC(),
			Producer:  p.producer,
		},
		Data: data,
	})
	if err != nil {
		return fmt.Errorf("marshal event payload: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.channel.PublishWithContext(
		ctx,
		p.exchange,
		routingKey,
		false,
		false,
		amqp091.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp091.Persistent,
			Timestamp:    time.Now().UTC(),
			Body:         payload,
		},
	); err != nil {
		return fmt.Errorf("publish rabbitmq event: %w", err)
	}

	return nil
}

func (p *RabbitMQPublisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	var closeErr error
	if p.channel != nil {
		if err := p.channel.Close(); err != nil {
			closeErr = err
		}
		p.channel = nil
	}
	if p.conn != nil {
		if err := p.conn.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
		p.conn = nil
	}

	return closeErr
}
