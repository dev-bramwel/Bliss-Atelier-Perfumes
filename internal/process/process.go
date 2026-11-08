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
	"syscall"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/catalog"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/config"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/database"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/delivery"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/httpapi"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/migration"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/orders"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/provider"
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
	var ping httpapi.Ping
	var catalogStore httpapi.Catalog
	var orderStore httpapi.OrderService
	var deliveryStore *delivery.Store
	if cfg.DatabaseURL != "" {
		pool, e := database.Open(ctx, cfg.DatabaseURL, cfg.MaxConnections, cfg.IOTimeout)
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
		catalogStore = &catalog.Store{Pool: pool}
		orderStore = &orders.Store{Pool: pool}
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
		return httpapi.New(cfg.Address, cfg.IOTimeout, ping, catalogStore, orderStore, deliveryStore, secConfig, ncbaConfig, paymentProv).Serve(ctx, listener, cfg.ShutdownTimeout, logger)
	case "worker":
		if orderStore == nil {
			logger.Info("worker_idle", "mode", "no_database")
			<-ctx.Done()
			logger.Info("worker_stopped")
			return nil
		}
		logger.Info("worker_started", "role", "reservation_expiry_sweep")
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

		for {
			select {
			case <-ctx.Done():
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
				cancel()
			}
		}
	default:
		return errors.New("unknown command")
	}
}
