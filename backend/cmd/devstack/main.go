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

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/backend/internal/config"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/backend/internal/database"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/backend/internal/migration"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/backend/internal/provider"
	"github.com/jackc/pgx/v5/pgxpool"
)

func secrets(directory string) error {
	names := []string{"admin_password", "migrator_password", "runtime_password", "migrator_url", "runtime_url"}
	existing := 0
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(directory, name)); err == nil {
			existing++
		}
	}
	if existing == len(names) {
		return nil
	}
	if existing != 0 {
		return errors.New("partial local credentials exist; restore the matching set without overwriting a database's credentials")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return errors.New("could not create private local credentials directory")
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return err
	}
	values := map[string]string{}
	for _, role := range []string{"admin", "migrator", "runtime"} {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		values[role+"_password"] = hex.EncodeToString(b)
	}
	for _, role := range []string{"migrator", "runtime"} {
		values[role+"_url"] = "postgresql://bliss_" + role + ":" + values[role+"_password"] + "@db:5432/bliss_dev?sslmode=disable"
	}
	for _, name := range names {
		// Directory mode 0700 protects host access; readable leaves allow Compose's
		// bind-mounted secret files to be consumed by distinct non-root container UIDs.
		file, err := os.OpenFile(filepath.Join(directory, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0444)
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
	} else if state.State != "current" {
		return errors.New("legacy database requires explicit reviewed adoption outside local bootstrap")
	}
	if _, err = migration.Run(ctx, pool, command, 15*time.Second); err != nil {
		return err
	}
	// This local-only capability exposes one boolean, not history rows, to runtime.
	predicate := migration.LedgerPredicate()
	sql := `GRANT USAGE ON SCHEMA public TO bliss_runtime;
 GRANT SELECT,INSERT,UPDATE,DELETE ON public."Order",public."MpesaTransaction" TO bliss_runtime;
 GRANT SELECT ON public.catalog_product,public.catalog_settings,public.stock_control TO bliss_runtime;
 CREATE SCHEMA IF NOT EXISTS bliss_ops;
 REVOKE ALL ON SCHEMA bliss_ops FROM PUBLIC;
 GRANT USAGE ON SCHEMA bliss_ops TO bliss_runtime;
 CREATE OR REPLACE FUNCTION bliss_ops.schema_ready() RETURNS boolean
 LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT ` + predicate + `
 FROM public.bliss_schema_migrations $$;
 REVOKE ALL ON FUNCTION bliss_ops.schema_ready() FROM PUBLIC;
 GRANT EXECUTE ON FUNCTION bliss_ops.schema_ready() TO bliss_runtime;`
	if _, err = pool.Exec(ctx, sql); err != nil {
		return errors.New("local schema grants failed")
	}
	fmt.Println("local migration and runtime grants complete")
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
