// Command devstack bootstraps only the isolated local Compose database.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/config"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/database"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/migration"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/provider"
	"github.com/jackc/pgx/v5/pgxpool"
)

func secrets(directory string) error {
	names := []string{"admin_password", "migrator_password", "runtime_password", "catalog_password", "migrator_url", "runtime_url", "catalog_url"}
	existing := 0
	for _, name := range names {
		if info, err := os.Stat(filepath.Join(directory, name)); err == nil && !info.IsDir() {
			existing++
		}
	}
	if existing == len(names) {
		return nil
	}

	legacyNames := []string{"admin_password", "migrator_password", "runtime_password", "migrator_url", "runtime_url"}
	legacyCount := 0
	for _, name := range legacyNames {
		if info, err := os.Stat(filepath.Join(directory, name)); err == nil && !info.IsDir() {
			legacyCount++
		}
	}

	// Refuse overwriting partial credentials, unless upgrading a complete legacy credential directory.
	if existing != 0 && legacyCount != len(legacyNames) {
		return errors.New("partial local credentials exist; restore the matching set without overwriting a database's credentials")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return errors.New("could not create private local credentials directory")
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return err
	}
	values := map[string]string{}
	for _, name := range legacyNames {
		if content, err := os.ReadFile(filepath.Join(directory, name)); err == nil {
			values[name] = strings.TrimSpace(string(content))
		}
	}
	for _, role := range []string{"admin", "migrator", "runtime", "catalog"} {
		key := role + "_password"
		if _, ok := values[key]; !ok {
			b := make([]byte, 32)
			if _, err := rand.Read(b); err != nil {
				return err
			}
			values[key] = hex.EncodeToString(b)
		}
	}
	for _, role := range []string{"migrator", "runtime", "catalog"} {
		key := role + "_url"
		if _, ok := values[key]; !ok {
			values[key] = "postgresql://bliss_" + role + ":" + values[role+"_password"] + "@db:5432/bliss_dev?sslmode=disable"
		}
	}
	for _, name := range names {
		targetPath := filepath.Join(directory, name)
		if info, err := os.Stat(targetPath); err == nil {
			if !info.IsDir() {
				continue
			}
			if err := os.RemoveAll(targetPath); err != nil {
				return errors.New("could not clean up invalid directory for credential file")
			}
		}
		// Directory mode 0700 protects host access; readable leaves allow Compose's
		// bind-mounted secret files to be consumed by distinct non-root container UIDs.
		file, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0444)
		if err != nil {
			return errors.New("could not create local credential file")
		}
		_, err = file.WriteString(values[name])
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			return errors.New("could not write local credential file")
		}
	}
	return nil
}

func migrate() error {
	if os.Getenv("BLISS_LOCAL_DEV") != "1" {
		return errors.New("local bootstrap requires BLISS_LOCAL_DEV=1")
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	u, err := url.Parse(cfg.DatabaseURL)
	if err != nil || u.Host != "db:5432" || u.Path != "/bliss_dev" || u.User == nil || u.User.Username() != "bliss_migrator" {
		return errors.New("local bootstrap refuses non-Compose database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var pool *pgxpool.Pool
	for {
		pool, err = database.Open(ctx, cfg.DatabaseURL, 2, 5*time.Second)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(500 * time.Millisecond):
		}
	}
	defer pool.Close()
	state, err := migration.Run(ctx, pool, "status", 15*time.Second)
	if err != nil {
		return err
	}
	command := "up"
	if state.State == "empty" {
		command = "init"
	} else if state.State != "current" && state.State != "pending" {
		return errors.New("legacy database requires explicit reviewed adoption outside local bootstrap")
	}
	if _, err = migration.Run(ctx, pool, command, 15*time.Second); err != nil {
		return err
	}
	// This local-only capability exposes one boolean, not history rows, to runtime.
	predicate := migration.LedgerPredicate()
	sql := `GRANT USAGE ON SCHEMA public TO bliss_runtime;
 GRANT SELECT,INSERT,UPDATE,DELETE ON public."Order",public."MpesaTransaction" TO bliss_runtime;
 GRANT SELECT,INSERT,UPDATE,DELETE ON public.order_item,public.stock_reservation,public.stock_reservation_item,public.order_fulfillment TO bliss_runtime;
 GRANT SELECT,UPDATE(stock) ON public.catalog_product TO bliss_runtime;
 GRANT SELECT ON public.catalog_settings,public.stock_control TO bliss_runtime;
 CREATE SCHEMA IF NOT EXISTS bliss_ops;
 REVOKE ALL ON SCHEMA bliss_ops FROM PUBLIC;
 GRANT USAGE ON SCHEMA bliss_ops TO bliss_runtime;
 CREATE OR REPLACE FUNCTION bliss_ops.schema_ready() RETURNS boolean
 LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT ` + predicate + `
 FROM public.bliss_schema_migrations $$;
 REVOKE ALL ON FUNCTION bliss_ops.schema_ready() FROM PUBLIC;
 GRANT EXECUTE ON FUNCTION bliss_ops.schema_ready() TO bliss_runtime;
 DO $$
 BEGIN
  IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'bliss_catalog') THEN
   GRANT USAGE ON SCHEMA public TO bliss_catalog;
   GRANT SELECT ON public.catalog_product,public.catalog_settings,public.stock_control TO bliss_catalog;
   GRANT USAGE ON SCHEMA bliss_ops TO bliss_catalog;
   GRANT EXECUTE ON FUNCTION bliss_ops.schema_ready() TO bliss_catalog;
  END IF;
 END $$;`
	if _, err = pool.Exec(ctx, sql); err != nil {
		return errors.New("local schema grants failed")
	}
	fmt.Println("local migration, runtime and catalog grants complete")
	return nil
}

func main() {
	var err error
	if len(os.Args) == 3 && os.Args[1] == "secrets" && strings.TrimSpace(os.Args[2]) != "" {
		err = secrets(os.Args[2])
	} else if len(os.Args) == 4 && os.Args[1] == "audit-image" {
		err = auditImage(os.Args[2], os.Args[3])
	} else if len(os.Args) == 2 && os.Args[1] == "provider-check" {
		err = checkProvider()
	} else if len(os.Args) == 2 && os.Args[1] == "migrate" {
		err = migrate()
	} else {
		err = errors.New("usage: devstack secrets DIRECTORY | migrate | provider-check | audit-image ARCHIVE DIRECTORY")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func checkProvider() error {
	client, err := provider.New(3 * time.Second)
	if err != nil {
		return err
	}
	defer client.Close()
	for _, mode := range []string{"accept", "reject", "malformed"} {
		request, err := http.NewRequest(http.MethodPost, "http://fake-provider:8090/payments", bytes.NewBufferString(`{"amount":2500}`))
		if err != nil {
			return err
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Fixture-Mode", mode)
		var result struct {
			Status        string `json:"status"`
			TransactionID string `json:"transactionId"`
		}
		err = client.DoJSON(context.Background(), request, &result)
		if mode == "accept" && (err != nil || result.Status != "accepted" || result.TransactionID != "synthetic-fixture-only") {
			return errors.New("fake provider acceptance failed")
		}
		if mode != "accept" && err == nil {
			return errors.New("fake provider failure mode accepted")
		}
	}
	fmt.Println("fake provider acceptance, rejection and malformed-response checks passed")
	return nil
}
