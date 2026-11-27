package analytics

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/events"
	"github.com/jackc/pgx/v5"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
)

// ClientConfig configures Kafka broker connectivity, topic retention, TLS, and SASL authentication.
type ClientConfig struct {
	Brokers       []string
	Topic         string
	ConsumerGroup string
	RetentionMs   int64
	TLS           bool
	SASLMechanism string // "", "plain", "scram-sha-256", "scram-sha-512"
	SASLUsername  string
	SASLPassword  string
}

func (c ClientConfig) normalized() ClientConfig {
	if c.Topic == "" {
		c.Topic = DefaultTopic
	}
	if c.ConsumerGroup == "" {
		c.ConsumerGroup = DefaultConsumerGroup
	}
	if c.RetentionMs <= 0 {
		c.RetentionMs = DefaultRetentionMs
	}
	c.SASLMechanism = strings.ToLower(strings.TrimSpace(c.SASLMechanism))
	return c
}

func kafkaDialOpts(cfg ClientConfig) ([]kgo.Opt, error) {
	if len(cfg.Brokers) == 0 {
		return nil, errors.New("analytics: at least one Kafka broker is required")
	}
	opts := []kgo.Opt{kgo.SeedBrokers(cfg.Brokers...)}
	if cfg.TLS {
		opts = append(opts, kgo.DialTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}))
	}
	if cfg.SASLMechanism != "" {
		if cfg.SASLUsername == "" || cfg.SASLPassword == "" {
			return nil, errors.New("analytics: SASL username and password are required")
		}
		switch cfg.SASLMechanism {
		case "plain":
			opts = append(opts, kgo.SASL(plain.Auth{
				User: cfg.SASLUsername,
				Pass: cfg.SASLPassword,
			}.AsMechanism()))
		case "scram-sha-256":
			opts = append(opts, kgo.SASL(scram.Auth{
				User: cfg.SASLUsername,
				Pass: cfg.SASLPassword,
			}.AsSha256Mechanism()))
		case "scram-sha-512":
			opts = append(opts, kgo.SASL(scram.Auth{
				User: cfg.SASLUsername,
				Pass: cfg.SASLPassword,
			}.AsSha512Mechanism()))
		default:
			return nil, fmt.Errorf("analytics: unsupported SASL mechanism %q", cfg.SASLMechanism)
		}
	}
	return opts, nil
}

// Producer implements messaging.Publisher for Kafka analytics topics,
// partitioning by aggregateId (orderId) with AllISRAcks durability.
type Producer struct {
	client *kgo.Client
	topic  string
}

// NewProducer creates a Kafka producer targeting brokers and topic without TLS/SASL.
func NewProducer(brokers []string, topic string) (*Producer, error) {
	return NewProducerWithConfig(ClientConfig{Brokers: brokers, Topic: topic})
}

// NewProducerWithConfig creates a Kafka producer using ClientConfig (including TLS/SASL).
func NewProducerWithConfig(cfg ClientConfig) (*Producer, error) {
	cfg = cfg.normalized()
	opts, err := kafkaDialOpts(cfg)
	if err != nil {
		return nil, err
	}
	opts = append(opts,
		kgo.DefaultProduceTopic(cfg.Topic),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProduceRequestTimeout(5*time.Second),
	)
	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("analytics: create kafka producer: %w", err)
	}
	return &Producer{client: client, topic: cfg.Topic}, nil
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
	return EnsureTopicWithConfig(ctx, ClientConfig{Brokers: brokers, Topic: topic, RetentionMs: retentionMs})
}

// EnsureTopicWithConfig creates/updates the analytics topic using ClientConfig (including TLS/SASL).
func EnsureTopicWithConfig(ctx context.Context, cfg ClientConfig) error {
	cfg = cfg.normalized()
	opts, err := kafkaDialOpts(cfg)
	if err != nil {
		return err
	}
	client, err := kgo.NewClient(opts...)
	if err != nil {
		return err
	}
	defer client.Close()

	adm := kadm.NewClient(client)
	retStr := strconv.FormatInt(cfg.RetentionMs, 10)
	cleanup := "delete"
	configs := map[string]*string{
		"retention.ms":   &retStr,
		"cleanup.policy": &cleanup,
	}

	details, err := adm.ListTopics(ctx, cfg.Topic)
	if err != nil {
		return fmt.Errorf("analytics: list topics: %w", err)
	}
	if !details.Has(cfg.Topic) {
		resp, err := adm.CreateTopics(ctx, 1, 1, configs, cfg.Topic)
		if err != nil {
			return fmt.Errorf("analytics: create topic %s: %w", cfg.Topic, err)
		}
		if err := resp.Error(); err != nil {
			return fmt.Errorf("analytics: create topic %s: %w", cfg.Topic, err)
		}
		return nil
	}

	if _, err := adm.AlterTopicConfigs(ctx, []kadm.AlterConfig{
		{Op: kadm.SetConfig, Name: "retention.ms", Value: &retStr},
		{Op: kadm.SetConfig, Name: "cleanup.policy", Value: &cleanup},
	}, cfg.Topic); err != nil {
		return fmt.Errorf("analytics: alter topic %s retention: %w", cfg.Topic, err)
	}
	return nil
}

// Consumer streams analytics events from Kafka into PostgreSQL read models
// with durable deduplication and checkpointed offsets.
type Consumer struct {
	cfg    ClientConfig
	store  *Store
	logger *slog.Logger
}

// NewConsumer constructs a Kafka analytics Consumer without TLS/SASL.
func NewConsumer(brokers []string, topic, group string, store *Store, logger *slog.Logger) *Consumer {
	return NewConsumerWithConfig(ClientConfig{
		Brokers:       brokers,
		Topic:         topic,
		ConsumerGroup: group,
	}, store, logger)
}

// NewConsumerWithConfig constructs a Kafka analytics Consumer using ClientConfig (including TLS/SASL).
func NewConsumerWithConfig(cfg ClientConfig, store *Store, logger *slog.Logger) *Consumer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Consumer{
		cfg:    cfg.normalized(),
		store:  store,
		logger: logger,
	}
}

// Start runs the consumer loop until ctx is canceled.
func (c *Consumer) Start(ctx context.Context) error {
	opts, err := kafkaDialOpts(c.cfg)
	if err != nil {
		return err
	}
	opts = append(opts,
		kgo.ConsumerGroup(c.cfg.ConsumerGroup),
		kgo.ConsumeTopics(c.cfg.Topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.DisableAutoCommit(),
	)
	client, err := kgo.NewClient(opts...)
	if err != nil {
		return fmt.Errorf("analytics: create kafka consumer: %w", err)
	}
	defer client.Close()

	c.logger.InfoContext(ctx, "kafka_analytics_consumer_started", "topic", c.cfg.Topic, "group", c.cfg.ConsumerGroup)

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

// ReplayAll preflights Kafka topic offsets, fetches all retained records before mutating
// PostgreSQL, and then atomically resets projections, seeds any pre-retention baseline from
// authoritative tables, and applies the replayed Kafka stream in a single transaction.
func (c *Consumer) ReplayAll(ctx context.Context) (int, error) {
	if c.store == nil || c.store.Pool == nil {
		return 0, errors.New("analytics: database pool not initialized")
	}

	opts, err := kafkaDialOpts(c.cfg)
	if err != nil {
		return 0, err
	}
	admClient, err := kgo.NewClient(opts...)
	if err != nil {
		return 0, fmt.Errorf("analytics: create admin client: %w", err)
	}
	adm := kadm.NewClient(admClient)
	endOffsets, endErr := adm.ListEndOffsets(ctx, c.cfg.Topic)
	startOffsets, startErr := adm.ListStartOffsets(ctx, c.cfg.Topic)
	admClient.Close()
	if endErr != nil {
		return 0, fmt.Errorf("analytics: list end offsets: %w", endErr)
	}
	if startErr != nil {
		return 0, fmt.Errorf("analytics: list start offsets: %w", startErr)
	}

	targets := make(map[int32]int64)
	consumeParts := make(map[string]map[int32]kgo.Offset)
	consumeParts[c.cfg.Topic] = make(map[int32]kgo.Offset)
	var offsetLookupErr error
	partCount := 0

	endOffsets.Each(func(o kadm.ListedOffset) {
		if offsetLookupErr != nil {
			return
		}
		if o.Topic != c.cfg.Topic {
			return
		}
		partCount++
		if o.Err != nil {
			offsetLookupErr = fmt.Errorf("analytics: end offset error on partition %d: %w", o.Partition, o.Err)
			return
		}
		so, ok := startOffsets.Lookup(c.cfg.Topic, o.Partition)
		if !ok {
			offsetLookupErr = fmt.Errorf("analytics: missing start offset for partition %d", o.Partition)
			return
		}
		if so.Err != nil {
			offsetLookupErr = fmt.Errorf("analytics: start offset error on partition %d: %w", o.Partition, so.Err)
			return
		}
		if o.Offset > so.Offset {
			targets[o.Partition] = o.Offset - 1
			consumeParts[c.cfg.Topic][o.Partition] = kgo.NewOffset().At(so.Offset)
		}
	})
	if offsetLookupErr != nil {
		return 0, offsetLookupErr
	}
	if partCount == 0 {
		return 0, fmt.Errorf("analytics: topic %s has no partitions", c.cfg.Topic)
	}

	var records []*kgo.Record
	if len(targets) > 0 {
		replayOpts := append(opts, kgo.ConsumePartitions(consumeParts))
		client, err := kgo.NewClient(replayOpts...)
		if err != nil {
			return 0, fmt.Errorf("analytics: create replay client: %w", err)
		}
		defer client.Close()

		for len(targets) > 0 {
			fetches := client.PollFetches(ctx)
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			if errs := fetches.Errors(); len(errs) > 0 {
				return 0, errs[0].Err
			}
			fetches.EachRecord(func(rec *kgo.Record) {
				records = append(records, rec)
				if target, ok := targets[rec.Partition]; ok && rec.Offset >= target {
					delete(targets, rec.Partition)
				}
			})
		}
	}

	tx, err := c.store.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("analytics: begin replay tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	group := c.store.group()
	if err := resetForReplayTx(ctx, tx, group); err != nil {
		return 0, err
	}
	if err := seedBaselineTx(ctx, tx); err != nil {
		return 0, err
	}

	replayed := 0
	for _, rec := range records {
		applied, err := applyEventTx(ctx, tx, group, rec.Topic, rec.Partition, rec.Offset, rec.Value)
		if err != nil {
			return 0, err
		}
		if applied {
			replayed++
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("analytics: commit replay tx: %w", err)
	}
	return replayed, nil
}
