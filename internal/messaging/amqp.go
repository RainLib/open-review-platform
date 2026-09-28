package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	amqp "github.com/rabbitmq/amqp091-go"
)

const deadLetterExchange = "openreview.dead-letter"

type AMQPPublisher struct {
	url        string
	connection *amqp.Connection
	channel    *amqp.Channel
	socket     net.Conn
	exchange   string
	returns    <-chan amqp.Return
	mu         sync.Mutex
}

type AMQPConsumer struct {
	connection *amqp.Connection
	channel    *amqp.Channel
}

type topologyBinding struct{ name, key string }

// topologyBindings is the complete durable event-to-queue contract. Keep it
// data-driven so terminal states can be asserted without needing a broker in
// every unit test; DeclareTopology remains the only place that applies it.
func topologyBindings() []topologyBinding {
	return []topologyBinding{
		{"openreview.review.ack.v1", "review.run.acknowledged"},
		{"openreview.review.execute.v1", "review.run.admitted"},
		{"openreview.agent-task.execute.v1", "agent.task.execute.requested"},
		{"openreview.agent-task.source.v1", "agent.task.source.resolve.requested"},
		// Cancellation is isolated from normal execution so an adapter stop is
		// not queued behind a backlog of new work.
		{"openreview.agent-task.cancel.v1", "agent.task.cancel.requested"},
		{"openreview.rule-test.execute.v1", "rule.test.requested"},
		{"openreview.review.terminal.v1", "review.run.cancelled"},
		{"openreview.review.terminal.v1", "review.run.superseded"},
		{"openreview.review.terminal.v1", "review.run.failed"},
		{"openreview.review.terminal.v1", "review.run.needs_attention"},
		{"openreview.interaction.response.v1", "review.interaction.response"},
		{"openreview.interaction.admission.v1", "review.interaction.admission"},
		{"openreview.external-issue.publish.v1", "external.issue.create"},
		{"openreview.external-issue.publish.v1", "external.issue.close"},
		{"openreview.provider-issue.triage.v1", "provider.issue.acknowledge"},
		{"openreview.provider-issue.triage.v1", "provider.issue.analyze"},
		{"openreview.notification.v1", "review.run.completed"},
		{"openreview.notification.v1", "review.run.failed"},
		{"openreview.notification.v1", "review.run.cancelled"},
		{"openreview.notification.v1", "review.run.superseded"},
		{"openreview.notification.v1", "review.run.needs_attention"},
		{"openreview.notification.v1", "notification.destination.test"},
	}
}

func OpenAMQPPublisher(url, exchange string) (*AMQPPublisher, error) {
	publisher := &AMQPPublisher{url: url, exchange: exchange}
	publisher.mu.Lock()
	err := publisher.connectLocked()
	publisher.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return publisher, nil
}

// connectLocked replaces a stale AMQP transport and restores all publisher
// guarantees before another outbox row can be acknowledged. The relay keeps
// failed rows durable, so reconnecting on the next attempt is safe even when a
// previous publish lost its confirmation after the broker accepted the bytes:
// downstream inbox deduplication still keys on the immutable message id.
func (p *AMQPPublisher) connectLocked() error {
	if p == nil || p.url == "" || p.exchange == "" {
		return fmt.Errorf("RabbitMQ publisher URL and exchange are required")
	}
	p.closeLocked()
	// Retain the underlying socket so a broker flow-control pause cannot block
	// the synchronous frame write past the caller's confirmation deadline.
	var socket net.Conn
	dial := amqp.DefaultDial(10 * time.Second)
	connection, err := amqp.DialConfig(p.url, amqp.Config{Dial: func(network, address string) (net.Conn, error) {
		connected, dialErr := dial(network, address)
		if dialErr == nil {
			socket = connected
		}
		return connected, dialErr
	}})
	if err != nil {
		return fmt.Errorf("connect RabbitMQ: %w", err)
	}
	channel, err := connection.Channel()
	if err != nil {
		_ = connection.Close()
		return fmt.Errorf("open RabbitMQ channel: %w", err)
	}
	p.connection = connection
	p.channel = channel
	p.socket = socket
	if err := p.DeclareTopology(); err != nil {
		p.closeLocked()
		return err
	}
	if err := p.enablePublisherConfirms(); err != nil {
		p.closeLocked()
		return err
	}
	return nil
}

func (p *AMQPPublisher) ensureReadyLocked() error {
	if p.connection != nil && !p.connection.IsClosed() && p.channel != nil && !p.channel.IsClosed() && p.returns != nil {
		return nil
	}
	if err := p.connectLocked(); err != nil {
		return fmt.Errorf("restore RabbitMQ publisher: %w", err)
	}
	return nil
}

// enablePublisherConfirms makes broker acceptance part of the durable handoff.
// Relay.Flush may mark an outbox row published only after Publish returns nil,
// so a successful socket write is not sufficient evidence of delivery.
func (p *AMQPPublisher) enablePublisherConfirms() error {
	if p == nil || p.channel == nil {
		return fmt.Errorf("AMQP publisher channel is required")
	}
	if err := p.channel.Confirm(false); err != nil {
		return fmt.Errorf("enable RabbitMQ publisher confirms: %w", err)
	}
	// Publish is serialized below, so one buffered return is enough and avoids
	// blocking the channel reader before the matching confirm can arrive.
	p.returns = p.channel.NotifyReturn(make(chan amqp.Return, 1))
	return nil
}

func (p *AMQPPublisher) DeclareTopology() error {
	if err := p.channel.ExchangeDeclare(p.exchange, "topic", true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare review exchange: %w", err)
	}
	if err := p.channel.ExchangeDeclare(deadLetterExchange, "topic", true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare dead-letter exchange: %w", err)
	}
	for _, queue := range topologyBindings() {
		args := amqp.Table{
			"x-queue-type":           "quorum",
			"x-dead-letter-exchange": deadLetterExchange,
			"x-delivery-limit":       int32(domain.QuorumQueueRedeliveryLimit),
		}
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
	if p == nil {
		return fmt.Errorf("AMQP publisher is not ready")
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
	}
	body, err := json.Marshal(map[string]any{"message_id": message.ID.String(), "aggregate_id": message.AggregateID.String(), "topic": message.Topic, "payload": message.Payload})
	if err != nil {
		return fmt.Errorf("encode broker message: %w", err)
	}

	// A channel is shared by each relay process. Waiting for one confirmation at
	// a time gives an unambiguous association between a mandatory return and its
	// outbox row; a failed row is retried through the durable outbox instead.
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ensureReadyLocked(); err != nil {
		return err
	}
	socket := p.socket
	if socket == nil {
		p.resetAmbiguousPublisherLocked()
		return fmt.Errorf("RabbitMQ publisher socket is unavailable")
	}
	deadline, _ := ctx.Deadline()
	if err := socket.SetWriteDeadline(deadline); err != nil {
		p.resetAmbiguousPublisherLocked()
		return fmt.Errorf("set RabbitMQ publish deadline: %w", err)
	}
	defer func() { _ = socket.SetWriteDeadline(time.Time{}) }()
	confirmation, err := p.channel.PublishWithDeferredConfirmWithContext(ctx, p.exchange, message.Topic, true, false, amqp.Publishing{
		ContentType: "application/json", DeliveryMode: amqp.Persistent, MessageId: message.ID.String(), Priority: outboxMessagePriority(message), Body: body,
	})
	if err != nil {
		p.resetAmbiguousPublisherLocked()
		return fmt.Errorf("publish broker message: %w", err)
	}
	accepted, err := confirmation.WaitContext(ctx)
	if err != nil {
		// The broker may still confirm this row after our deadline. Drop the
		// entire channel so neither its late confirm nor a mandatory return can
		// be mistaken for the next outbox row. Inbox idempotency covers a broker
		// acceptance whose confirmation was lost.
		p.resetAmbiguousPublisherLocked()
		return fmt.Errorf("wait for broker confirmation: %w", err)
	}
	if !accepted {
		return fmt.Errorf("broker negatively acknowledged message %s", message.ID)
	}
	// RabbitMQ sends a mandatory basic.return before its publisher confirm. A
	// returned message has not reached any consumer queue and must remain in
	// the outbox for retry/recovery rather than being recorded as published.
	select {
	case returned, ok := <-p.returns:
		if !ok {
			return fmt.Errorf("publisher return channel closed")
		}
		return fmt.Errorf("broker returned unroutable message %s for %s: %d %s", message.ID, returned.RoutingKey, returned.ReplyCode, returned.ReplyText)
	default:
		return nil
	}
}

func (p *AMQPPublisher) resetAmbiguousPublisherLocked() {
	p.returns = nil
	p.channel = nil
	p.socket = nil
	if p.connection != nil {
		_ = p.connection.CloseDeadline(time.Now().Add(2 * time.Second))
		p.connection = nil
	}
}

func (p *AMQPPublisher) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closeLocked()
}

func (p *AMQPPublisher) closeLocked() error {
	p.returns = nil
	p.channel = nil
	p.socket = nil
	if p.connection != nil {
		err := p.connection.CloseDeadline(time.Now().Add(2 * time.Second))
		p.connection = nil
		return err
	}
	return nil
}

func OpenAMQPConsumer(url, exchange string) (*AMQPConsumer, error) {
	connection, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("connect RabbitMQ: %w", err)
	}
	channel, err := connection.Channel()
	if err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("open RabbitMQ channel: %w", err)
	}
	consumer := &AMQPConsumer{connection: connection, channel: channel}
	publisher := &AMQPPublisher{channel: channel, exchange: exchange}
	if err := publisher.DeclareTopology(); err != nil {
		_ = consumer.Close()
		return nil, err
	}
	// Apply before any Consume call. RabbitMQ interprets this as a per-consumer
	// prefetch default, including runner processes that register two dedicated
	// consumers on this channel.
	if err := channel.Qos(1, 0, false); err != nil {
		_ = consumer.Close()
		return nil, fmt.Errorf("set consumer prefetch: %w", err)
	}
	return consumer, nil
}

// Consume is intentionally a thin transport adapter. Durable deduplication
// and retry policy belong to HandleExactlyOnce and the database inbox.
func (c *AMQPConsumer) Consume(ctx context.Context, queue, consumer string, handler func(context.Context, []byte) error) error {
	if c == nil || c.channel == nil || queue == "" || consumer == "" || handler == nil {
		return fmt.Errorf("AMQP consumer, queue, consumer id, and handler are required")
	}
	deliveries, err := c.channel.Consume(queue, consumer, false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("consume %s: %w", queue, err)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case delivery, ok := <-deliveries:
			if !ok {
				return fmt.Errorf("consumer delivery channel closed")
			}
			if err := handler(ctx, delivery.Body); err != nil {
				if nackErr := delivery.Nack(false, true); nackErr != nil {
					return fmt.Errorf("handle message: %w (nack: %v)", err, nackErr)
				}
				continue
			}
			if err := delivery.Ack(false); err != nil {
				return fmt.Errorf("ack message: %w", err)
			}
		}
	}
}

// outboxMessagePriority targets the deployed RabbitMQ 4.1 quorum behaviour.
// That broker treats 0-4 as normal and values above 4 as high, favouring high
// work 2:1 while still giving normal work a fair share. The execution queue
// therefore uses 4 for standard/deep work and 8 for security work; arbitrary
// 0-31 values would not create extra execution classes on RabbitMQ 4.1.
// The topic remains the durable routing contract, so replaying an outbox row
// preserves business semantics even on older brokers that ignore priority.
func outboxMessagePriority(message domain.OutboxMessage) uint8 {
	switch message.Topic {
	case "review.interaction.response":
		return 31
	case "review.run.acknowledged":
		return 28
	case "review.interaction.admission":
		return 24
	case "review.run.admitted":
		if mode, ok := message.Payload["review_mode"].(string); ok && mode == "security" {
			return 8
		}
		return 4
	case "agent.task.execute.requested":
		// A human-approved task is important but must not starve interactive
		// acknowledgement and merge-gate traffic. The dedicated queue retains
		// its own bounded consumer concurrency.
		return 4
	case "agent.task.source.resolve.requested":
		// Capture the immutable source before planning; it is bounded provider
		// metadata work and must not overtake interactive acknowledgements.
		return 6
	case "agent.task.cancel.requested":
		// The local lease was already revoked transactionally. Prioritize the
		// external stop request so the remote sandbox is reclaimed promptly.
		return 16
	case "review.run.cancelled", "review.run.superseded", "review.run.failed", "review.run.needs_attention":
		return 16
	case "external.issue.create", "external.issue.close", "provider.issue.acknowledge":
		return 8
	case "provider.issue.analyze":
		return 4
	case "notification.destination.test":
		return 6
	default:
		return 4
	}
}

func (c *AMQPConsumer) Close() error {
	if c == nil {
		return nil
	}
	if c.channel != nil {
		_ = c.channel.Close()
	}
	if c.connection != nil {
		return c.connection.Close()
	}
	return nil
}
