// Package messaging provides the RabbitMQ client, topology declaration, publisher confirms,
// and in-memory mock transports for Bliss Atelier Perfumes.
package messaging

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Standard topology constants per docs/naming-and-code-rules.md and ADR 001.
const (
	ExchangeEvents     = "bliss.events.v1"
	QueueNotifications = "bliss.notifications.v1"
	QueueRetry         = "bliss.notifications.v1.retry"
	QueueDeadLetter    = "bliss.notifications.v1.dead"
	DefaultRetryTTLMs  = 5000 // 5 seconds delay before redelivery
)

// Common errors.
var (
	ErrNotConnected   = errors.New("messaging: broker not connected")
	ErrPublishFailed  = errors.New("messaging: publish failed or not confirmed")
	ErrConsumerClosed = errors.New("messaging: consumer closed")
)

// Delivery wraps an incoming message to allow transport-agnostic processing and acknowledgment.
type Delivery interface {
	MessageID() string
	RoutingKey() string
	Body() []byte
	Redelivered() bool
	Ack() error
	Nack(requeue bool) error
	RejectToDLQ() error
}

// Publisher is the interface for publishing events to the messaging topology.
type Publisher interface {
	Publish(ctx context.Context, routingKey string, messageID string, body []byte) error
	Close() error
}

// Consumer is the interface for consuming notifications from the queue.
type Consumer interface {
	Consume(ctx context.Context, handler func(ctx context.Context, d Delivery) error) error
	Close() error
}

// amqpDelivery implements Delivery wrapping amqp091-go.Delivery.
type amqpDelivery struct {
	d amqp.Delivery
}

func (a *amqpDelivery) MessageID() string       { return a.d.MessageId }
func (a *amqpDelivery) RoutingKey() string      { return a.d.RoutingKey }
func (a *amqpDelivery) Body() []byte            { return a.d.Body }
func (a *amqpDelivery) Redelivered() bool       { return a.d.Redelivered }
func (a *amqpDelivery) Ack() error              { return a.d.Ack(false) }
func (a *amqpDelivery) Nack(requeue bool) error { return a.d.Nack(false, requeue) }
func (a *amqpDelivery) RejectToDLQ() error      { return a.d.Reject(false) }

// RabbitMQClient manages an AMQP 0.9.1 connection, channel pool, topology declaration, and publisher confirms.
type RabbitMQClient struct {
	url      string
	mu       sync.RWMutex
	conn     *amqp.Connection
	pubChan  *amqp.Channel
	confirms chan amqp.Confirmation
	closed   bool
}

// Dial establishes a connection to RabbitMQ, declares the required topology, and enables publisher confirms.
func Dial(url string) (*RabbitMQClient, error) {
	if url == "" {
		return nil, errors.New("messaging: rabbitmq url cannot be empty")
	}

	conn, err := amqp.DialConfig(url, amqp.Config{
		Dial: amqp.DefaultDial(5 * time.Second),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to dial rabbitmq: %w", err)
	}

	client := &RabbitMQClient{
		url:  url,
		conn: conn,
	}

	// Declare durable topology
	if err := client.declareTopology(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to declare rabbitmq topology: %w", err)
	}

	// Initialize dedicated publisher channel with confirms
	if err := client.initPublisherChannel(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to init publisher channel: %w", err)
	}

	return client, nil
}

// declareTopology sets up the durable exchange, notification queue, retry queue, and DLQ.
func (c *RabbitMQClient) declareTopology() error {
	ch, err := c.conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	// 1. Declare Topic Exchange: bliss.events.v1
	if err := ch.ExchangeDeclare(
		ExchangeEvents,
		"topic",
		true,  // durable
		false, // auto-delete
		false, // internal
		false, // no-wait
		nil,
	); err != nil {
		return fmt.Errorf("declare exchange %s failed: %w", ExchangeEvents, err)
	}

	// 2. Declare Dead Letter Queue: bliss.notifications.v1.dead
	if _, err := ch.QueueDeclare(
		QueueDeadLetter,
		true,  // durable
		false, // auto-delete
		false, // exclusive
		false, // no-wait
		nil,
	); err != nil {
		return fmt.Errorf("declare dlq %s failed: %w", QueueDeadLetter, err)
	}

	// Bind DLQ to events exchange with dead-letter routing key
	if err := ch.QueueBind(
		QueueDeadLetter,
		"dead.*",
		ExchangeEvents,
		false,
		nil,
	); err != nil {
		return fmt.Errorf("bind dlq failed: %w", err)
	}

	// 3. Declare Main Notifications Queue with Dead-Letter Exchange pointing to bliss.events.v1
	queueArgs := amqp.Table{
		"x-dead-letter-exchange":    ExchangeEvents,
		"x-dead-letter-routing-key": "dead.notifications",
	}
	if _, err := ch.QueueDeclare(
		QueueNotifications,
		true,  // durable
		false, // auto-delete
		false, // exclusive
		false, // no-wait
		queueArgs,
	); err != nil {
		return fmt.Errorf("declare queue %s failed: %w", QueueNotifications, err)
	}

	// Bind main queue to order, payment, and delivery event patterns
	routingKeys := []string{"order.*", "payment.*", "delivery.*"}
	for _, key := range routingKeys {
		if err := ch.QueueBind(
			QueueNotifications,
			key,
			ExchangeEvents,
			false,
			nil,
		); err != nil {
			return fmt.Errorf("bind queue %s to %s failed: %w", QueueNotifications, key, err)
		}
	}

	// 4. Declare Retry Queue with Message TTL and DLX returning to ExchangeEvents
	retryArgs := amqp.Table{
		"x-message-ttl":             int32(DefaultRetryTTLMs),
		"x-dead-letter-exchange":    ExchangeEvents,
		"x-dead-letter-routing-key": "order.retry.requeued",
	}
	if _, err := ch.QueueDeclare(
		QueueRetry,
		true,
		false,
		false,
		false,
		retryArgs,
	); err != nil {
		return fmt.Errorf("declare retry queue %s failed: %w", QueueRetry, err)
	}

	return nil
}

func (c *RabbitMQClient) initPublisherChannel() error {
	if c.pubChan != nil {
		_ = c.pubChan.Close()
		c.pubChan = nil
	}
	ch, err := c.conn.Channel()
	if err != nil {
		return err
	}

	// Put channel into confirm mode
	if err := ch.Confirm(false); err != nil {
		_ = ch.Close()
		return fmt.Errorf("channel confirm mode failed: %w", err)
	}

	c.pubChan = ch
	c.confirms = ch.NotifyPublish(make(chan amqp.Confirmation, 100))
	return nil
}

// Publish sends a persistent message to the bliss.events.v1 exchange with publisher confirms.
func (c *RabbitMQClient) Publish(ctx context.Context, routingKey string, messageID string, body []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed || c.conn == nil || c.conn.IsClosed() {
		return ErrNotConnected
	}

	msg := amqp.Publishing{
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now().UTC(),
		ContentType:  "application/json",
		MessageId:    messageID,
		Body:         body,
	}

	// Publish to bliss.events.v1
	err := c.pubChan.PublishWithContext(
		ctx,
		ExchangeEvents,
		routingKey,
		false, // mandatory
		false, // immediate
		msg,
	)
	if err != nil {
		return fmt.Errorf("publish failed: %w", err)
	}

	// Wait for publisher confirm
	select {
	case confirmation, ok := <-c.confirms:
		if !ok || !confirmation.Ack {
			return ErrPublishFailed
		}
		return nil
	case <-ctx.Done():
		// Reinitialize channel to purge any pending confirmation tags for this abandoned message
		_ = c.initPublisherChannel()
		return ctx.Err()
	}
}

// Consume starts consuming messages from bliss.notifications.v1 with bounded prefetch.
func (c *RabbitMQClient) Consume(ctx context.Context, handler func(ctx context.Context, d Delivery) error) error {
	ch, err := c.conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	// Bounded prefetch to prevent buffer exhaustion
	if err := ch.Qos(10, 0, false); err != nil {
		return fmt.Errorf("set qos failed: %w", err)
	}

	deliveries, err := ch.Consume(
		QueueNotifications,
		"bliss-notification-worker",
		false, // autoAck = false for manual durable ACK
		false, // exclusive
		false, // noLocal
		false, // noWait
		nil,
	)
	if err != nil {
		return fmt.Errorf("consume failed: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d, ok := <-deliveries:
			if !ok {
				return ErrConsumerClosed
			}
			wrapped := &amqpDelivery{d: d}
			_ = handler(ctx, wrapped)
		}
	}
}

// Close gracefully closes the publisher channel and AMQP connection.
func (c *RabbitMQClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true

	var errs []error
	if c.pubChan != nil {
		if err := c.pubChan.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}
