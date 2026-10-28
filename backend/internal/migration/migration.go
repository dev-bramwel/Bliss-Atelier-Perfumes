// Package migration owns the transactional, checksum-verified SQL migration runner.
package migration

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed sql/*.sql schema.sql schema.json schema-v2.json ledger.sql prisma.sql
var assets embed.FS

const LockID int64 = 477418744910
const PrismaLockID int64 = 72707369
const legacyName = "20261006120000_init"

type Result struct {
	State    string `json:"state"`
	Orders   int64  `json:"orders"`
	Payments int64  `json:"payments"`
}

type step struct {
	version int64
	sql     string
}

func read(name string) string {
	b, err := assets.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return string(b)
}
func normalized(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }
func checksum(s string) string   { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func steps() []step {
	return []step{{1, normalized(read("sql/0001_legacy.sql"))}, {2, normalized(read("sql/0002_catalog.sql"))}}
}

// ValidCommand prevents misspelled/unsafe commands before database IO.
func ValidCommand(s string) bool {
	return s == "status" || s == "init" || s == "adopt-legacy" || s == "up"
}

// Run sanitizes all driver errors: PostgreSQL errors may contain customer values.
func Run(ctx context.Context, pool *pgxpool.Pool, command string, timeout time.Duration) (Result, error) {
	if !ValidCommand(command) {
		return Result{}, errors.New("usage: migrate status|init|adopt-legacy|up")
	}
	bounded, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := execute(bounded, pool, command, steps())
	if err != nil {
		var safe *failure
		if errors.As(err, &safe) {
			return Result{}, safe
		}
		return Result{}, errors.New("migration database operation failed; inspect status before retry; check availability, permissions and timeout")
	}
	return result, nil
}

type failure struct{ message string }

func (e *failure) Error() string  { return e.message }
func reject(message string) error { return &failure{message} }

func execute(ctx context.Context, pool *pgxpool.Pool, command string, migrations []step) (Result, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Result{}, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if _, err = tx.Exec(ctx, "SET LOCAL search_path = public"); err != nil {
		return Result{}, err
	}
	// Bound server work too: closing a canceled client socket does not imply
	// PostgreSQL has already stopped its statement or released transaction locks.
	if deadline, ok := ctx.Deadline(); ok {
		milliseconds := max(int64(1), time.Until(deadline).Milliseconds())
		if _, err = tx.Exec(ctx, "SELECT set_config('statement_timeout',$1,true),set_config('transaction_timeout',$1,true)", strconv.FormatInt(milliseconds, 10)+"ms"); err != nil {
			return Result{}, err
		}
	}
	for _, id := range []int64{LockID, PrismaLockID} {
		var locked bool
		if err = tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", id).Scan(&locked); err != nil {
			return Result{}, err
		}
		if !locked {
			return Result{}, reject("migration lock busy; another schema operation is active")
		}
	}
	tables, err := inspect(ctx, tx)
	if err != nil {
		return Result{}, err
	}
	// Stabilize existing relations against DDL and writes for consistent inventory/adoption.
	for name := range tables {
		if name != "Order" && name != "MpesaTransaction" && name != "_prisma_migrations" && name != "bliss_schema_migrations" && name != "catalog_settings" && name != "catalog_product" && name != "stock_control" && name != "stock_batch" && name != "stock_entry" && name != "stock_approval" {
			return Result{}, reject("unrecognized public schema; no changes applied")
		}
		if _, err = tx.Exec(ctx, "LOCK TABLE public."+pgx.Identifier{name}.Sanitize()+" IN SHARE ROW EXCLUSIVE MODE NOWAIT"); err != nil {
			return Result{}, err
		}
	}
	tables, err = inspect(ctx, tx)
	if err != nil {
		return Result{}, err
	}
	expected := map[string]json.RawMessage{}
	if err = json.Unmarshal([]byte(read("schema-v2.json")), &expected); err != nil {
		return Result{}, err
	}
	for name, actual := range tables {
		if !sameJSON(actual, expected[name]) {
			return Result{}, reject("schema mismatch; no changes applied")
		}
	}
	_, orders := tables["Order"]
	_, payments := tables["MpesaTransaction"]
	_, prisma := tables["_prisma_migrations"]
	_, ledger := tables["bliss_schema_migrations"]
	result := Result{State: "empty"}
	if len(tables) > 0 {
		if !orders || !payments {
			return Result{}, reject("incomplete baseline schema; no changes applied")
		}
		result.State = "legacy"
		if prisma {
			if err = verifyPrisma(ctx, tx); err != nil {
				return Result{}, err
			}
		}
		if !prisma && !ledger {
			return Result{}, reject("missing verified migration history; no changes applied")
		}
		if err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM public."Order"),(SELECT count(*) FROM public."MpesaTransaction")`).Scan(&result.Orders, &result.Payments); err != nil {
			return Result{}, err
		}
	}
	applied := 0
	if ledger {
		rows, e := tx.Query(ctx, "SELECT version,checksum,origin FROM public.bliss_schema_migrations ORDER BY version")
		if e != nil {
			return Result{}, e
		}
		for rows.Next() {
			var version int64
			var hash, origin string
			if e = rows.Scan(&version, &hash, &origin); e != nil {
				rows.Close()
				return Result{}, e
			}
			if applied >= len(migrations) || version != migrations[applied].version || hash != checksum(migrations[applied].sql) || (origin != "empty" && origin != "prisma") || (origin == "prisma") != prisma {
				rows.Close()
				return Result{}, reject("Go migration history/checksum mismatch; no changes applied")
			}
			applied++
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return Result{}, e
		}
		if applied == 0 {
			return Result{}, reject("empty Go migration ledger; no changes applied")
		}
		result.State = "current"
	}
	// Validate the exact schema for the applied version, including missing tables.
	contract := "schema.json"
	if applied >= 2 {
		contract = "schema-v2.json"
	}
	versionSchema := map[string]json.RawMessage{}
	if err = json.Unmarshal([]byte(read(contract)), &versionSchema); err != nil {
		return Result{}, err
	}
	if len(tables) > 0 {
		for name, body := range tables {
			if !sameJSON(body, versionSchema[name]) {
				return Result{}, reject("schema/version mismatch; no changes applied")
			}
		}
		for name := range versionSchema {
			if name == "_prisma_migrations" || name == "bliss_schema_migrations" {
				continue
			}
			if _, exists := tables[name]; !exists {
				return Result{}, reject("incomplete version schema; no changes applied")
			}
		}
	}
	if ledger && applied < len(migrations) {
		result.State = "pending"
	}
	switch command {
	case "status":
		return result, nil // rollback releases locks; no schema/history writes
	case "init":
		if len(tables) != 0 {
			return Result{}, reject("init requires an empty public schema; use explicit legacy adoption")
		}
	case "adopt-legacy":
		if ledger {
			return Result{}, reject("database already adopted; use status or up")
		}
		if !prisma || !orders || !payments {
			return Result{}, reject("adopt-legacy requires verified Prisma schema and history")
		}
	case "up":
		if !ledger {
			return Result{}, reject("up requires a Go ledger; first run init or adopt-legacy explicitly")
		}
	}
	if !ledger {
		if _, err = tx.Exec(ctx, read("ledger.sql")); err != nil {
			return Result{}, err
		}
	}
	for _, migration := range migrations[applied:] {
		origin := "empty"
		if prisma {
			origin = "prisma"
		}
		if command != "adopt-legacy" || migration.version != 1 {
			if _, err = tx.Exec(ctx, migration.sql); err != nil {
				return Result{}, err
			}
		}
		if _, err = tx.Exec(ctx, "INSERT INTO public.bliss_schema_migrations(version,checksum,origin) VALUES ($1,$2,$3)", migration.version, checksum(migration.sql), origin); err != nil {
			return Result{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Result{}, err
	}
	result.State = "current"
	return result, nil
}

func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ax, _ := json.Marshal(x)
	by, _ := json.Marshal(y)
	return string(ax) == string(by)
}

func inspect(ctx context.Context, tx pgx.Tx) (map[string]json.RawMessage, error) {
	// Reject routines, custom types and rules outside the supported contract.
	var extras bool
	err := tx.QueryRow(ctx, `SELECT
 EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='public') OR
 EXISTS(SELECT 1 FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='public' AND t.typrelid=0 AND t.typelem=0) OR
 EXISTS(SELECT 1 FROM pg_rewrite r JOIN pg_class c ON c.oid=r.ev_class JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public')`).Scan(&extras)
	if err != nil {
		return nil, err
	}
	if extras {
		return nil, reject("unsupported public schema objects; no changes applied")
	}
	rows, err := tx.Query(ctx, read("schema.sql"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]json.RawMessage{}
	for rows.Next() {
		var name, body string
		if err = rows.Scan(&name, &body); err != nil {
			return nil, err
		}
		result[name] = json.RawMessage(body)
	}
	return result, rows.Err()
}

func verifyPrisma(ctx context.Context, tx pgx.Tx) error {
	var count int
	var valid bool
	lf := normalized(read("sql/0001_legacy.sql"))
	err := tx.QueryRow(ctx, `SELECT count(*),coalesce(bool_and(migration_name=$1 AND checksum IN ($2,$3)
 AND finished_at IS NOT NULL AND rolled_back_at IS NULL AND applied_steps_count=1
 AND finished_at>=started_at AND (logs IS NULL OR logs='')),false) FROM public._prisma_migrations`, legacyName, checksum(lf), checksum(strings.ReplaceAll(lf, "\n", "\r\n"))).Scan(&count, &valid)
	if err != nil {
		return err
	}
	if count != 1 || !valid {
		return reject(fmt.Sprintf("Prisma history mismatch (%d records); no changes applied", count))
	}
	return nil
}
