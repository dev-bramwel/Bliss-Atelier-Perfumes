package messaging

import (
	"context"
	"sync"
)

// FakeDelivery represents a delivered message in the in-memory fake messaging system.
type FakeDelivery struct {
	msgID       string
	routingKey  string
	body        []byte
	redelivered bool
	acked       bool
	nacked      bool
	requeued    bool
	rejectedDLQ bool
}

func (f *FakeDelivery) MessageID() string       { return f.msgID }
func (f *FakeDelivery) RoutingKey() string      { return f.routingKey }
func (f *FakeDelivery) Body() []byte            { return f.body }
func (f *FakeDelivery) Redelivered() bool       { return f.redelivered }
func (f *FakeDelivery) Ack() error              { f.acked = true; return nil }
func (f *FakeDelivery) Nack(requeue bool) error { f.nacked = true; f.requeued = requeue; return nil }
func (f *FakeDelivery) RejectToDLQ() error      { f.rejectedDLQ = true; return nil }

func (f *FakeDelivery) IsAcked() bool       { return f.acked }
func (f *FakeDelivery) IsRejectedDLQ() bool { return f.rejectedDLQ }

// PublishedMessage records an event published to the fake broker.
type PublishedMessage struct {
	RoutingKey string
	MessageID  string
	Body       []byte
}

// FakeMessaging provides an in-memory messaging mock for unit testing.
type FakeMessaging struct {
	mu           sync.RWMutex
	brokerDown   bool
	failConfirms bool
	published    []PublishedMessage
	dlq          []PublishedMessage
	deliveries   chan *FakeDelivery
	closed       bool
}

// NewFakeMessaging initializes an in-memory mock messaging transport.
func NewFakeMessaging() *FakeMessaging {
	return &FakeMessaging{
		published:  make([]PublishedMessage, 0),
		dlq:        make([]PublishedMessage, 0),
		deliveries: make(chan *FakeDelivery, 100),
	}
}

// SetBrokerDown toggles whether the mock broker simulates a network outage.
func (f *FakeMessaging) SetBrokerDown(down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.brokerDown = down
}

// SetFailConfirms toggles whether publisher confirms fail (simulate unconfirmed nack).
func (f *FakeMessaging) SetFailConfirms(fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failConfirms = fail
}

// Publish stores the published message or returns an error if brokerDown or failConfirms.
func (f *FakeMessaging) Publish(ctx context.Context, routingKey string, messageID string, body []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed || f.brokerDown {
		return ErrNotConnected
	}
	if f.failConfirms {
		return ErrPublishFailed
	}

	msg := PublishedMessage{
		RoutingKey: routingKey,
		MessageID:  messageID,
		Body:       body,
	}
	f.published = append(f.published, msg)

	// Send to consumer channel if matching notifications routing key
	select {
	case f.deliveries <- &FakeDelivery{
		msgID:      messageID,
		routingKey: routingKey,
		body:       body,
	}:
	default:
	}

	return nil
}

// Consume reads deliveries from the mock channel and passes them to the handler.
func (f *FakeMessaging) Consume(ctx context.Context, handler func(ctx context.Context, d Delivery) error) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d, ok := <-f.deliveries:
			if !ok {
				return ErrConsumerClosed
			}
			_ = handler(ctx, d)
			if d.rejectedDLQ {
				f.mu.Lock()
				f.dlq = append(f.dlq, PublishedMessage{
					RoutingKey: d.routingKey,
					MessageID:  d.msgID,
					Body:       d.body,
				})
				f.mu.Unlock()
			}
		}
	}
}

// Deliver injects a synthetic delivery into the consumer queue (useful for testing redelivery and poison messages).
func (f *FakeMessaging) Deliver(msgID, routingKey string, body []byte, redelivered bool) *FakeDelivery {
	d := &FakeDelivery{
		msgID:       msgID,
		routingKey:  routingKey,
		body:        body,
		redelivered: redelivered,
	}
	f.deliveries <- d
	return d
}

// GetPublished returns a snapshot of all successfully published messages.
func (f *FakeMessaging) GetPublished() []PublishedMessage {
	f.mu.RLock()
	defer f.mu.RUnlock()
	copied := make([]PublishedMessage, len(f.published))
	copy(copied, f.published)
	return copied
}

// GetDLQ returns all messages routed to DLQ.
func (f *FakeMessaging) GetDLQ() []PublishedMessage {
	f.mu.RLock()
	defer f.mu.RUnlock()
	copied := make([]PublishedMessage, len(f.dlq))
	copy(copied, f.dlq)
	return copied
}

// Close closes the fake transport.
func (f *FakeMessaging) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.deliveries)
	}
	return nil
}

// Ensure FakeMessaging implements Publisher and Consumer.
var _ Publisher = (*FakeMessaging)(nil)
var _ Consumer = (*FakeMessaging)(nil)
var _ Delivery = (*FakeDelivery)(nil)
