package messaging

import (
	"context"
	"testing"
	"time"
)

func TestFakeMessaging_PublishAndConsume(t *testing.T) {
	fake := NewFakeMessaging()
	defer fake.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	received := make(chan string, 1)
	go func() {
		_ = fake.Consume(ctx, func(ctx context.Context, d Delivery) error {
			received <- string(d.Body())
			return d.Ack()
		})
	}()

	err := fake.Publish(ctx, "order.created", "msg-1", []byte("hello-order"))
	if err != nil {
		t.Fatalf("failed to publish: %v", err)
	}

	select {
	case msg := <-received:
		if msg != "hello-order" {
			t.Errorf("expected 'hello-order', got %s", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message")
	}

	// Verify published history
	published := fake.GetPublished()
	if len(published) != 1 || published[0].MessageID != "msg-1" {
		t.Fatalf("unexpected published history: %+v", published)
	}
}

func TestFakeMessaging_BrokerOutage(t *testing.T) {
	fake := NewFakeMessaging()
	defer fake.Close()

	ctx := context.Background()

	// Simulate broker outage
	fake.SetBrokerDown(true)

	err := fake.Publish(ctx, "order.created", "msg-2", []byte("should-fail"))
	if err == nil {
		t.Fatal("expected error during broker outage, got nil")
	}

	// Broker recovers
	fake.SetBrokerDown(false)

	err = fake.Publish(ctx, "order.created", "msg-2", []byte("should-succeed"))
	if err != nil {
		t.Fatalf("expected publish to succeed after broker recovery, got %v", err)
	}
}

func TestFakeMessaging_FailConfirms(t *testing.T) {
	fake := NewFakeMessaging()
	defer fake.Close()

	ctx := context.Background()
	fake.SetFailConfirms(true)

	err := fake.Publish(ctx, "order.created", "msg-3", []byte("unconfirmed"))
	if err != ErrPublishFailed {
		t.Fatalf("expected ErrPublishFailed, got %v", err)
	}
}

func TestFakeMessaging_DLQ(t *testing.T) {
	fake := NewFakeMessaging()
	defer fake.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		_ = fake.Consume(ctx, func(ctx context.Context, d Delivery) error {
			_ = d.RejectToDLQ()
			return nil
		})
	}()

	_ = fake.Publish(ctx, "order.poison", "poison-1", []byte("bad-payload"))

	// Wait briefly for delivery and DLQ rejection
	time.Sleep(100 * time.Millisecond)

	dlq := fake.GetDLQ()
	if len(dlq) != 1 || dlq[0].MessageID != "poison-1" {
		t.Fatalf("expected poison-1 in DLQ, got %+v", dlq)
	}
}
