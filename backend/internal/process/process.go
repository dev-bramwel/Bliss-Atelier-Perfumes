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

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/backend/internal/catalog"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/backend/internal/config"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/backend/internal/database"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/backend/internal/httpapi"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/backend/internal/migration"
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
		return httpapi.New(cfg.Address, cfg.IOTimeout, ping, catalogStore).Serve(ctx, listener, cfg.ShutdownTimeout, logger)
	case "worker":
		logger.Info("worker_idle", "mode", "foundation_no_jobs")
		<-ctx.Done()
		logger.Info("worker_stopped")
		return nil
	default:
		return errors.New("unknown command")
	}
}
