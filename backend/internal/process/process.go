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

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/backend/internal/config"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/backend/internal/database"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/backend/internal/httpapi"
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
		// Deliberately read-only until the verified Prisma baseline is delivered.
		if len(args) != 1 || args[0] != "status" {
			return errors.New("usage: migrate status; schema writes are disabled until Prisma baseline adoption")
		}
		if cfg.DatabaseURL == "" {
			return errors.New("GO_DATABASE_URL is required for migrate status")
		}
	} else if len(args) != 0 {
		return errors.New("unexpected command arguments")
	}
	var ping httpapi.Ping
	if cfg.DatabaseURL != "" {
		pool, e := database.Open(ctx, cfg.DatabaseURL, cfg.MaxConnections, cfg.IOTimeout)
		if e != nil {
			return e
		}
		defer pool.Close()
		ping = pool.Ping
	}
	switch command {
	case "api":
		listener, e := net.Listen("tcp", cfg.Address)
		if e != nil {
			return errors.New("HTTP listener unavailable")
		}
		defer listener.Close()
		return httpapi.New(cfg.Address, cfg.IOTimeout, ping).Serve(ctx, listener, cfg.ShutdownTimeout, logger)
	case "worker":
		logger.Info("worker_idle", "mode", "foundation_no_jobs")
		<-ctx.Done()
		logger.Info("worker_stopped")
		return nil
	case "migrate":
		logger.Info("migration_status", "schema_owner", "prisma", "go_migrations", "disabled")
		return nil
	default:
		return errors.New("unknown command")
	}
}
