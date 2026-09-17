package messaging

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/RainLib/open-review-platform/internal/domain"
	amqp "github.com/rabbitmq/amqp091-go"
)

const deadLetterExchange = "openreview.dead-letter"

type AMQPPublisher struct {
	connection *amqp.Connection
	channel    *amqp.Channel
	exchange   string
}

func OpenAMQPPublisher(url, exchange string) (*AMQPPublisher, error) {
	connection, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("connect RabbitMQ: %w", err)
	}
	channel, err := connection.Channel()
	if err != nil {
		connection.Close()
		return nil, fmt.Errorf("open RabbitMQ channel: %w", err)
	}
	publisher := &AMQPPublisher{connection: connection, channel: channel, exchange: exchange}
	if err := publisher.DeclareTopology(); err != nil {
		publisher.Close()
		return nil, err
	}
	return publisher, nil
}

func (p *AMQPPublisher) DeclareTopology() error {
	if err := p.channel.ExchangeDeclare(p.exchange, "topic", true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare review exchange: %w", err)
	}
	if err := p.channel.ExchangeDeclare(deadLetterExchange, "topic", true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare dead-letter exchange: %w", err)
	}
	queues := []struct{ name, key string }{
		{"openreview.review.ack.v1", "review.run.acknowledged"},
		{"openreview.review.execute.v1", "review.run.admitted"},
		{"openreview.review.publish.v1", "review.run.publishing"},
	}
	for _, queue := range queues {
		args := amqp.Table{"x-queue-type": "quorum", "x-dead-letter-exchange": deadLetterExchange}
		if _, err := p.channel.QueueDeclare(queue.name, true, false, false, false, args); err != nil {
			return fmt.Errorf("declare %s: %w", queue.name, err)
		}
		if err := p.channel.QueueBind(queue.name, queue.key, p.exchange, false, nil); err != nil {
			return fmt.Errorf("bind %s: %w", queue.name, err)
		}
	}
	if _, err := p.channel.QueueDeclare("openreview.review.dlq.v1", true, false, false, false, amqp.Table{"x-queue-type": "quorum"}); err != nil {
		return fmt.Errorf("declare review dead-letter queue: %w", err)
	}
	if err := p.channel.QueueBind("openreview.review.dlq.v1", "#", deadLetterExchange, false, nil); err != nil {
		return fmt.Errorf("bind review dead-letter queue: %w", err)
	}
	return nil
}

func (p *AMQPPublisher) Publish(ctx context.Context, message domain.OutboxMessage) error {
	body, err := json.Marshal(map[string]any{"message_id": message.ID.String(), "aggregate_id": message.AggregateID.String(), "topic": message.Topic, "payload": message.Payload})
	if err != nil {
		return fmt.Errorf("encode broker message: %w", err)
	}
	return p.channel.PublishWithContext(ctx, p.exchange, message.Topic, false, false, amqp.Publishing{
		ContentType: "application/json", DeliveryMode: amqp.Persistent, MessageId: message.ID.String(), Body: body,
	})
}

func (p *AMQPPublisher) Close() error {
	if p.channel != nil {
		_ = p.channel.Close()
	}
	if p.connection != nil {
		return p.connection.Close()
	}
	return nil
}
