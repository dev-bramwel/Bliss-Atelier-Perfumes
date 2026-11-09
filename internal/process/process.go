// Package process wires command lifecycles and emits only safe operational events.
package process

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/catalog"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/config"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/database"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/delivery"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/httpapi"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/messaging"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/migration"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/orders"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/outbox"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/provider"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/reconciliation"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/rediscache"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/worker"
)

func Main(command string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := Run(ctx, command, os.Args[1:], os.Getenv, os.Stderr)
	stop()
	os.Exit(code)
}

func Run(ctx context.Context, command string, args []string, get func(string) string, output io.Writer) int {
	logger := slog.New(slog.NewJSONHandler(output, nil))
	if err := run(ctx, command, args, get, logger); err != nil {
		logger.Error("process_failed", "reason", err.Error())
		return 1
	}
	return 0
}

func run(ctx context.Context, command string, args []string, get func(string) string, logger *slog.Logger) error {
	cfg, err := config.Load(get)
	if err != nil {
		return err
	}
	if command == "migrate" {
		if len(args) != 1 || !migration.ValidCommand(args[0]) {
			return errors.New("usage: migrate status|init|adopt-legacy|up")
		}
		if cfg.DatabaseURL == "" {
			return errors.New("GO_DATABASE_URL is required for migrate")
		}
	} else if len(args) != 0 {
		return errors.New("unexpected command arguments")
	}
	var pool *pgxpool.Pool
	var ping httpapi.Ping
	var catalogStore httpapi.Catalog
	var orderStore httpapi.OrderService
	var deliveryStore *delivery.Store
	var redisClient *rediscache.Client
	var catalogCache *rediscache.CatalogCache

	if cfg.RedisURL != "" {
		var rErr error
		redisClient, rErr = rediscache.NewClient(cfg.RedisURL)
		if rErr != nil {
			logger.Warn("redis_client_init_failed", "error", rErr.Error())
		} else {
			defer redisClient.Close()
			catalogCache = rediscache.NewCatalogCache(redisClient, "", 5*time.Minute)
			logger.Info("redis_connected", "url_configured", true)
		}
	}

	if cfg.DatabaseURL != "" {
		var e error
		pool, e = database.Open(ctx, cfg.DatabaseURL, cfg.MaxConnections, cfg.IOTimeout)
		if e != nil {
			return e
		}
		defer pool.Close()
		if command == "migrate" {
			result, e := migration.Run(ctx, pool, args[0], cfg.IOTimeout)
			if e != nil {
				return e
			}
			logger.Info("migration_status", "command", args[0], "state", result.State, "orders", result.Orders, "payments", result.Payments)
			return nil
		}
		catalogStore = &catalog.Store{Pool: pool, Invalidator: catalogCache}
		orderStore = &orders.Store{Pool: pool, Invalidator: catalogCache}
		deliveryStore = delivery.NewStore(pool)
		ping = pool.Ping
		if cfg.RequireSchema {
			ping = func(ctx context.Context) error {
				return migration.RuntimeReady(ctx, pool)
			}
		}
	}
	switch command {
	case "api":
		listener, e := net.Listen("tcp", cfg.Address)
		if e != nil {
			return errors.New("HTTP listener unavailable")
		}
		defer listener.Close()
		secConfig := httpapi.SecurityConfig{
			AllowedOrigins:      cfg.AllowedOrigins,
			TrustedProxies:      cfg.TrustedProxies,
			ManagerSecret:       cfg.ManagerSecret,
			MpesaCallbackSecret: cfg.MpesaCallbackSecret,
		}
		ncbaConfig := provider.NCBAConfig{
			BaseURL:      cfg.NCBABaseURL,
			Username:     cfg.NCBAUsername,
			Password:     cfg.NCBAPassword,
			PayBillNo:    cfg.NCBAPayBillNo,
			AccountNo:    cfg.NCBAAccountNo,
			IPNUsername:  cfg.NCBAIPNUsername,
			IPNPassword:  cfg.NCBAIPNPassword,
			IPNSecretKey: cfg.NCBAIPNSecretKey,
		}
		var paymentProv provider.PaymentProvider
		if cfg.NCBAUsername != "" && cfg.NCBAPassword != "" {
			ncbaClient, _ := provider.New(cfg.IOTimeout)
			paymentProv, _ = provider.NewNCBA(ncbaConfig, ncbaClient)
		}
		return httpapi.New(cfg.Address, cfg.IOTimeout, ping, catalogStore, orderStore, deliveryStore, secConfig, ncbaConfig, paymentProv, redisClient, catalogCache).Serve(ctx, listener, cfg.ShutdownTimeout, logger)
	case "worker":
		if pool == nil || orderStore == nil {
			logger.Info("worker_idle", "mode", "no_database")
			<-ctx.Done()
			logger.Info("worker_stopped")
			return nil
		}
		logger.Info("worker_started", "role", "background_reconciliation_and_expiry_sweep")

		var paymentProv provider.PaymentProvider
		if cfg.NCBAUsername != "" && cfg.NCBAPassword != "" {
			ncbaConfig := provider.NCBAConfig{
				BaseURL:      cfg.NCBABaseURL,
				Username:     cfg.NCBAUsername,
				Password:     cfg.NCBAPassword,
				PayBillNo:    cfg.NCBAPayBillNo,
				AccountNo:    cfg.NCBAAccountNo,
				IPNUsername:  cfg.NCBAIPNUsername,
				IPNPassword:  cfg.NCBAIPNPassword,
				IPNSecretKey: cfg.NCBAIPNSecretKey,
			}
			ncbaClient, _ := provider.New(cfg.IOTimeout)
			paymentProv, _ = provider.NewNCBA(ncbaConfig, ncbaClient)
			defer paymentProv.Close()
		}
		reconciler := reconciliation.New(orderStore, paymentProv, reconciliation.DefaultConfig(), logger)

		workerCtx, cancelWorker := context.WithCancel(ctx)
		defer cancelWorker()

		var msgWg sync.WaitGroup
		if cfg.RabbitMQURL != "" {
			msgWg.Add(1)
			go func() {
				defer msgWg.Done()
				runMessagingWorker(workerCtx, pool, cfg.RabbitMQURL, logger)
			}()
		} else {
			logger.Info("worker_messaging_disabled", "reason", "no_rabbitmq_url")
		}

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		// Run immediate sweep on startup
		if released, err := orderStore.ReleaseExpired(ctx, time.Now().UTC()); err != nil {
			logger.Error("reservation_sweep_failed", "error", err.Error())
		} else if released > 0 {
			logger.Info("reservations_released", "count", released)
		}
		if deliveryStore != nil {
			if expiredQuotes, err := deliveryStore.ExpireQuotes(ctx, time.Now().UTC()); err != nil {
				logger.Error("quote_sweep_failed", "error", err.Error())
			} else if expiredQuotes > 0 {
				logger.Info("quotes_expired", "count", expiredQuotes)
			}
		}
		if summary, err := reconciler.ReconcileStaleAttempts(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("reconciliation_sweep_failed", "error", err.Error())
		} else if summary.Processed > 0 {
			logger.Info("reconciliation_sweep_completed",
				"processed", summary.Processed,
				"succeeded", summary.Succeeded,
				"failed", summary.Failed,
				"pending", summary.Pending,
				"exceptions_raised", summary.ExceptionsRaised,
			)
		}

		for {
			select {
			case <-ctx.Done():
				cancelWorker()
				msgWg.Wait()
				logger.Info("worker_stopped")
				return nil
			case <-ticker.C:
				sweepCtx, cancel := context.WithTimeout(ctx, cfg.IOTimeout)
				released, err := orderStore.ReleaseExpired(sweepCtx, time.Now().UTC())
				if err != nil && !errors.Is(err, context.Canceled) {
					logger.Error("reservation_sweep_failed", "error", err.Error())
				} else if released > 0 {
					logger.Info("reservations_released", "count", released)
				}
				if deliveryStore != nil {
					expiredQuotes, err := deliveryStore.ExpireQuotes(sweepCtx, time.Now().UTC())
					if err != nil && !errors.Is(err, context.Canceled) {
						logger.Error("quote_sweep_failed", "error", err.Error())
					} else if expiredQuotes > 0 {
						logger.Info("quotes_expired", "count", expiredQuotes)
					}
				}
				if summary, err := reconciler.ReconcileStaleAttempts(sweepCtx); err != nil && !errors.Is(err, context.Canceled) {
					logger.Error("reconciliation_sweep_failed", "error", err.Error())
				} else if summary.Processed > 0 {
					logger.Info("reconciliation_sweep_completed",
						"processed", summary.Processed,
						"succeeded", summary.Succeeded,
						"failed", summary.Failed,
						"pending", summary.Pending,
						"exceptions_raised", summary.ExceptionsRaised,
					)
				}
				cancel()
			}
		}
	default:
		return errors.New("unknown command")
	}
}

func runMessagingWorker(ctx context.Context, pool *pgxpool.Pool, rabbitURL string, logger *slog.Logger) {
	outboxStore := outbox.NewStore(pool)
	backoff := 2 * time.Second

	for {
		if ctx.Err() != nil {
			return
		}

		client, err := messaging.Dial(rabbitURL)
		if err != nil {
			logger.Warn("messaging_connect_failed", "error", err.Error(), "retry_in", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
				if backoff < 30*time.Second {
					backoff *= 2
				}
				continue
			}
		}

		logger.Info("messaging_connected", "topology", messaging.ExchangeEvents)
		backoff = 2 * time.Second

		msgCtx, cancelMsg := context.WithCancel(ctx)
		var wg sync.WaitGroup

		pub := outbox.NewBackgroundPublisher(outboxStore, client, outbox.DefaultPublisherConfig(), logger)
		cons := worker.NewNotificationConsumer(pool, client, logger)

		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := pub.Start(msgCtx); err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("outbox_publisher_error", "error", err.Error())
				cancelMsg()
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := cons.Start(msgCtx); err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("notification_consumer_error", "error", err.Error())
				cancelMsg()
			}
		}()

		<-msgCtx.Done()
		cancelMsg()
		_ = client.Close()
		wg.Wait()

		if ctx.Err() != nil {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
	}
}
