package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/events"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Producer implements messaging.Publisher for Kafka analytics topics,
// partitioning by aggregateId (orderId) with AllISRAcks durability.
type Producer struct {
	client *kgo.Client
	topic  string
}

// NewProducer creates a Kafka producer targeting brokers and topic.
func NewProducer(brokers []string, topic string) (*Producer, error) {
	if len(brokers) == 0 {
		return nil, errors.New("analytics: at least one Kafka broker is required")
	}
	if topic == "" {
		topic = DefaultTopic
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.DefaultProduceTopic(topic),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProduceRequestTimeout(5*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("analytics: create kafka producer: %w", err)
	}
	return &Producer{client: client, topic: topic}, nil
}

// Publish synchronously produces a versioned event envelope to Kafka, keyed by aggregateId.
func (p *Producer) Publish(ctx context.Context, routingKey string, messageID string, body []byte) error {
	if p.client == nil {
		return errors.New("analytics: kafka producer closed")
	}
	var env events.Envelope
	var key []byte
	if err := json.Unmarshal(body, &env); err == nil && env.AggregateID != "" {
		key = []byte(env.AggregateID)
	} else if messageID != "" {
		key = []byte(messageID)
	}

	rec := &kgo.Record{
		Topic: p.topic,
		Key:   key,
		Value: body,
		Headers: []kgo.RecordHeader{
			{Key: "event_id", Value: []byte(messageID)},
			{Key: "routing_key", Value: []byte(routingKey)},
		},
	}
	res := p.client.ProduceSync(ctx, rec)
	return res.FirstErr()
}

// Close flushes and closes the underlying Kafka client.
func (p *Producer) Close() error {
	if p.client != nil {
		p.client.Close()
	}
	return nil
}

// EnsureTopic creates the analytics topic if absent and enforces retention.ms policy.
func EnsureTopic(ctx context.Context, brokers []string, topic string, retentionMs int64) error {
	if len(brokers) == 0 {
		return errors.New("analytics: at least one Kafka broker is required")
	}
	if topic == "" {
		topic = DefaultTopic
	}
	if retentionMs <= 0 {
		retentionMs = DefaultRetentionMs
	}
	client, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		return err
	}
	defer client.Close()

	adm := kadm.NewClient(client)
	retStr := strconv.FormatInt(retentionMs, 10)
	cleanup := "delete"
	configs := map[string]*string{
		"retention.ms":   &retStr,
		"cleanup.policy": &cleanup,
	}

	details, err := adm.ListTopics(ctx, topic)
	if err != nil {
		return fmt.Errorf("analytics: list topics: %w", err)
	}
	if !details.Has(topic) {
		resp, err := adm.CreateTopics(ctx, 1, 1, configs, topic)
		if err != nil {
			return fmt.Errorf("analytics: create topic %s: %w", topic, err)
		}
		if err := resp.Error(); err != nil {
			return fmt.Errorf("analytics: create topic %s: %w", topic, err)
		}
		return nil
	}

	if _, err := adm.AlterTopicConfigs(ctx, []kadm.AlterConfig{
		{Op: kadm.SetConfig, Name: "retention.ms", Value: &retStr},
		{Op: kadm.SetConfig, Name: "cleanup.policy", Value: &cleanup},
	}, topic); err != nil {
		return fmt.Errorf("analytics: alter topic %s retention: %w", topic, err)
	}
	return nil
}

// Consumer streams analytics events from Kafka into PostgreSQL read models
// with durable deduplication and checkpointed offsets.
type Consumer struct {
	brokers []string
	topic   string
	group   string
	store   *Store
	logger  *slog.Logger
}

// NewConsumer constructs a Kafka analytics Consumer.
func NewConsumer(brokers []string, topic, group string, store *Store, logger *slog.Logger) *Consumer {
	if topic == "" {
		topic = DefaultTopic
	}
	if group == "" {
		group = DefaultConsumerGroup
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Consumer{
		brokers: brokers,
		topic:   topic,
		group:   group,
		store:   store,
		logger:  logger,
	}
}

// Start runs the consumer loop until ctx is canceled.
func (c *Consumer) Start(ctx context.Context) error {
	if len(c.brokers) == 0 {
		return errors.New("analytics: at least one Kafka broker is required")
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(c.brokers...),
		kgo.ConsumerGroup(c.group),
		kgo.ConsumeTopics(c.topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.DisableAutoCommit(),
	)
	if err != nil {
		return fmt.Errorf("analytics: create kafka consumer: %w", err)
	}
	defer client.Close()

	c.logger.InfoContext(ctx, "kafka_analytics_consumer_started", "topic", c.topic, "group", c.group)

	for {
		fetches := client.PollFetches(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			for _, fe := range errs {
				c.logger.WarnContext(ctx, "kafka_fetch_error", "topic", fe.Topic, "partition", fe.Partition, "error", fe.Err.Error())
			}
			continue
		}

		var processed []*kgo.Record
		var applyErr error
		fetches.EachRecord(func(rec *kgo.Record) {
			if applyErr != nil {
				return
			}
			if _, err := c.store.ApplyEvent(ctx, rec.Topic, rec.Partition, rec.Offset, rec.Value); err != nil {
				applyErr = err
				return
			}
			processed = append(processed, rec)
		})
		if applyErr != nil {
			return applyErr
		}
		if len(processed) > 0 {
			if err := client.CommitRecords(ctx, processed...); err != nil && ctx.Err() == nil {
				c.logger.WarnContext(ctx, "kafka_commit_offsets_failed", "error", err.Error())
			}
		}
	}
}

// ReplayAll resets the analytics read models and checkpoints for this consumer group,
// then replays every available message from offset 0 up to each partition's high watermark.
func (c *Consumer) ReplayAll(ctx context.Context) (int, error) {
	if len(c.brokers) == 0 {
		return 0, errors.New("analytics: at least one Kafka broker is required")
	}
	if err := c.store.ResetForReplay(ctx); err != nil {
		return 0, err
	}

	admClient, err := kgo.NewClient(kgo.SeedBrokers(c.brokers...))
	if err != nil {
		return 0, err
	}
	adm := kadm.NewClient(admClient)
	endOffsets, err := adm.ListEndOffsets(ctx, c.topic)
	startOffsets, startErr := adm.ListStartOffsets(ctx, c.topic)
	admClient.Close()
	if err != nil {
		return 0, fmt.Errorf("analytics: list end offsets: %w", err)
	}
	if startErr != nil {
		return 0, fmt.Errorf("analytics: list start offsets: %w", startErr)
	}

	targets := make(map[int32]int64)
	consumeParts := make(map[string]map[int32]kgo.Offset)
	consumeParts[c.topic] = make(map[int32]kgo.Offset)

	endOffsets.Each(func(o kadm.ListedOffset) {
		if o.Err != nil {
			return
		}
		startOff := int64(0)
		if so, ok := startOffsets.Lookup(c.topic, o.Partition); ok && so.Err == nil {
			startOff = so.Offset
		}
		if o.Offset > startOff {
			targets[o.Partition] = o.Offset - 1
			consumeParts[c.topic][o.Partition] = kgo.NewOffset().At(startOff)
		}
	})

	if len(targets) == 0 {
		return 0, nil
	}

	client, err := kgo.NewClient(
		kgo.SeedBrokers(c.brokers...),
		kgo.ConsumePartitions(consumeParts),
	)
	if err != nil {
		return 0, fmt.Errorf("analytics: create replay client: %w", err)
	}
	defer client.Close()

	replayed := 0
	for len(targets) > 0 {
		fetches := client.PollFetches(ctx)
		if ctx.Err() != nil {
			return replayed, ctx.Err()
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			return replayed, errs[0].Err
		}
		var applyErr error
		fetches.EachRecord(func(rec *kgo.Record) {
			if applyErr != nil {
				return
			}
			applied, err := c.store.ApplyEvent(ctx, rec.Topic, rec.Partition, rec.Offset, rec.Value)
			if err != nil {
				applyErr = err
				return
			}
			if applied {
				replayed++
			}
			if target, ok := targets[rec.Partition]; ok && rec.Offset >= target {
				delete(targets, rec.Partition)
			}
		})
		if applyErr != nil {
			return replayed, applyErr
		}
	}
	return replayed, nil
}
