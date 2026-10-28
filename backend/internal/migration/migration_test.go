package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestArchivedBaseline(t *testing.T) {
	original, err := os.ReadFile("../../prisma/migrations/20261006120000_init/migration.sql")
	if err != nil {
		t.Fatal(err)
	}
	if normalized(string(original)) != steps()[0].sql {
		t.Fatal("baseline differs from immutable Prisma archive")
	}
	if checksum(steps()[0].sql) != "0bf265f9da0f9aaf07cb4a91e54efc03bad02aa33935806b1ba8d03d96226e49" {
		t.Fatal("historical baseline changed; restore the archive instead of rewriting history")
	}
	for _, bad := range []string{"down", "force", "reset", "", "UP"} {
		if ValidCommand(bad) {
			t.Fatal("unsafe command accepted")
		}
	}
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("GO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("run scripts/test-integration.ps1 for disposable migration checks")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Path != "/bliss_foundation_test" || u.User == nil || u.User.Username() != "bliss_test" || len(u.Query()) != 1 || u.Query().Get("sslmode") != "disable" {
		t.Fatal("refusing non-test database")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func execSQL(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, sql, args...); err != nil {
		t.Fatal(err)
	}
}
func reset(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	execSQL(t, pool, "DROP SCHEMA public CASCADE; CREATE SCHEMA public")
}
func run(t *testing.T, pool *pgxpool.Pool, command string) Result {
	t.Helper()
	result, err := Run(context.Background(), pool, command, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func fails(t *testing.T, pool *pgxpool.Pool, command, reason string) {
	t.Helper()
	_, err := Run(context.Background(), pool, command, time.Second)
	if err == nil || !strings.Contains(err.Error(), reason) {
		t.Fatalf("expected %q failure, got %v", reason, err)
	}
}
func legacy(t *testing.T, pool *pgxpool.Pool, populated bool) {
	t.Helper()
	execSQL(t, pool, read("sql/0001_legacy.sql"))
	execSQL(t, pool, read("prisma.sql"))
	execSQL(t, pool, `INSERT INTO public._prisma_migrations(id,checksum,migration_name,started_at,finished_at,applied_steps_count)
 VALUES ('11111111-1111-1111-1111-111111111111',$1,$2,'2026-10-06 12:00:00Z','2026-10-06 12:00:01Z',1)`, checksum(steps()[0].sql), legacyName)
	if populated {
		execSQL(t, pool, `INSERT INTO public."Order"(id,"createdAt","updatedAt","customerName","customerPhone","deliveryLocation","notes","paymentMethod","paymentStatus","totalKes",items)
  VALUES ('legacy-cuid-paid','2026-10-06 12:01:00.123','2026-10-06 12:02:00.456','Synthetic Customer','254700000000','Test pickup','synthetic fixture','pay_now','PAID',6000,'[{"id":"p1","qty":3,"priceKes":2000}]'),
  ('legacy-cuid-pending','2026-10-06 13:01:00.123','2026-10-06 13:02:00.456','Synthetic Pending','254700000001','Test pickup',NULL,'on_delivery','PENDING',2000,'[{"id":"p2","qty":1,"priceKes":2000}]');
  INSERT INTO public."MpesaTransaction"(id,"createdAt","updatedAt","orderId","merchantRequestId","checkoutRequestId","resultCode","resultDesc","mpesaReceiptNumber","transactionDate","phoneNumber",status)
  VALUES ('legacy-payment-cuid','2026-10-06 12:01:00.123','2026-10-06 12:02:00.456','legacy-cuid-paid','synthetic-merchant','synthetic-checkout','0','synthetic success','TESTRECEIPT','20261006120200','254700000000','SUCCESS')`)
	}
}

func snapshot(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var body string
	err := pool.QueryRow(context.Background(), `SELECT jsonb_build_array(
 (SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM public."Order" o),
 (SELECT jsonb_agg(to_jsonb(m) ORDER BY id) FROM public."MpesaTransaction" m),
 (SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM public._prisma_migrations p))::text`).Scan(&body)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func noLedger(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(context.Background(), "SELECT to_regclass('public.bliss_schema_migrations') IS NOT NULL").Scan(&exists); err != nil || exists {
		t.Fatalf("partial adoption: %v", err)
	}
}

func TestDisposableMigrations(t *testing.T) {
	pool := testPool(t)
	t.Run("empty requires explicit initialization", func(t *testing.T) {
		reset(t, pool)
		if got := run(t, pool, "status"); got.State != "empty" {
			t.Fatal(got)
		}
		noLedger(t, pool)
		fails(t, pool, "up", "first run init")
		fails(t, pool, "adopt-legacy", "requires verified")
		run(t, pool, "init")
		run(t, pool, "up")
		if got := run(t, pool, "status"); got.State != "current" || got.Orders != 0 || got.Payments != 0 {
			t.Fatal(got)
		}
		fails(t, pool, "init", "requires an empty")
		fails(t, pool, "adopt-legacy", "already adopted")
	})
	for _, populated := range []bool{false, true} {
		name := "empty legacy"
		if populated {
			name = "populated legacy"
		}
		t.Run(name, func(t *testing.T) {
			reset(t, pool)
			legacy(t, pool, populated)
			before := snapshot(t, pool)
			if got := run(t, pool, "status"); got.State != "legacy" {
				t.Fatal(got)
			}
			noLedger(t, pool)
			fails(t, pool, "init", "requires an empty")
			fails(t, pool, "up", "first run init")
			got := run(t, pool, "adopt-legacy")
			if populated && (got.Orders != 2 || got.Payments != 1) {
				t.Fatal(got)
			}
			run(t, pool, "up")
			run(t, pool, "status")
			if after := snapshot(t, pool); after != before {
				t.Fatal("legacy data/history changed")
			}
			var version int64
			var hash, origin string
			if err := pool.QueryRow(context.Background(), "SELECT version,checksum,origin FROM public.bliss_schema_migrations WHERE version=1").Scan(&version, &hash, &origin); err != nil || version != 1 || hash != checksum(steps()[0].sql) || origin != "prisma" {
				t.Fatalf("bad ledger: %v", err)
			}
		})
	}
	for name, sql := range map[string]string{
		"missing index":   `DROP INDEX public."MpesaTransaction_checkoutRequestId_key"`,
		"changed default": `ALTER TABLE public."Order" ALTER COLUMN "totalKes" SET DEFAULT 1`,
		"nullable column": `ALTER TABLE public."Order" ALTER COLUMN "customerName" DROP NOT NULL`,
		"changed type":    `ALTER TABLE public."Order" ALTER COLUMN "totalKes" TYPE bigint`,
		"extra table":     `CREATE TABLE public.unexpected(id int)`,
		"missing history": `DROP TABLE public._prisma_migrations`,
		"checksum":        `UPDATE public._prisma_migrations SET checksum=repeat('0',64)`,
		"unfinished":      `UPDATE public._prisma_migrations SET finished_at=NULL`,
		"rolled back":     `UPDATE public._prisma_migrations SET rolled_back_at=now()`,
		"unknown history": `UPDATE public._prisma_migrations SET migration_name='unknown'`,
		"missing steps":   `UPDATE public._prisma_migrations SET applied_steps_count=0`,
		"missing FK":      `ALTER TABLE public."MpesaTransaction" DROP CONSTRAINT "MpesaTransaction_orderId_fkey"`,
		"extra column":    `ALTER TABLE public."Order" ADD COLUMN surprise text`,
		"RLS":             `ALTER TABLE public."Order" ENABLE ROW LEVEL SECURITY`,
		"sequence":        `CREATE SEQUENCE public.surprise`,
		"history extra":   `INSERT INTO public._prisma_migrations SELECT '22222222-2222-2222-2222-222222222222',checksum,finished_at,'unknown',logs,rolled_back_at,started_at,applied_steps_count FROM public._prisma_migrations`,
	} {
		t.Run("reject "+name, func(t *testing.T) {
			reset(t, pool)
			legacy(t, pool, true)
			execSQL(t, pool, sql)
			fails(t, pool, "adopt-legacy", "")
			noLedger(t, pool)
		})
	}
	t.Run("CRLF historical checksum", func(t *testing.T) {
		reset(t, pool)
		legacy(t, pool, true)
		execSQL(t, pool, "UPDATE public._prisma_migrations SET checksum=$1", checksum(strings.ReplaceAll(steps()[0].sql, "\n", "\r\n")))
		run(t, pool, "adopt-legacy")
		run(t, pool, "status")
	})
	t.Run("Go ledger mismatch", func(t *testing.T) {
		reset(t, pool)
		run(t, pool, "init")
		execSQL(t, pool, "UPDATE public.bliss_schema_migrations SET checksum='tampered'")
		fails(t, pool, "up", "checksum mismatch")
		fails(t, pool, "status", "checksum mismatch")
	})
	for name, sql := range map[string]string{
		"unknown version":     "UPDATE public.bliss_schema_migrations SET version=version+9",
		"wrong origin":        "UPDATE public.bliss_schema_migrations SET origin='prisma'",
		"empty ledger":        "DELETE FROM public.bliss_schema_migrations",
		"ledger schema drift": "ALTER TABLE public.bliss_schema_migrations ADD COLUMN surprise text",
	} {
		t.Run(name, func(t *testing.T) {
			reset(t, pool)
			run(t, pool, "init")
			execSQL(t, pool, sql)
			fails(t, pool, "up", "")
		})
	}
	for _, lock := range []int64{LockID, PrismaLockID} {
		t.Run("concurrent runner refuses busy lock", func(t *testing.T) {
			reset(t, pool)
			legacy(t, pool, true)
			before := snapshot(t, pool)
			tx, err := pool.Begin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if _, err = tx.Exec(context.Background(), "SELECT pg_advisory_xact_lock($1)", lock); err != nil {
				t.Fatal(err)
			}
			fails(t, pool, "adopt-legacy", "lock busy")
			noLedger(t, pool)
			if snapshot(t, pool) != before {
				t.Fatal("busy lock changed data")
			}
			if err = tx.Rollback(context.Background()); err != nil {
				t.Fatal(err)
			}
			run(t, pool, "adopt-legacy")
		})
	}
	t.Run("failed SQL rolls back DDL and ledger", func(t *testing.T) {
		reset(t, pool)
		run(t, pool, "init")
		broken := append(steps(), step{3, `CREATE TABLE public.partial_change(id int); SELECT 1/0;`})
		if _, err := execute(context.Background(), pool, "up", broken); err == nil {
			t.Fatal("bad SQL accepted")
		}
		var clean bool
		if err := pool.QueryRow(context.Background(), "SELECT to_regclass('public.partial_change') IS NULL AND (SELECT count(*)=2 FROM public.bliss_schema_migrations)").Scan(&clean); err != nil || !clean {
			t.Fatalf("partial changes: %v", err)
		}
		run(t, pool, "up")
	})
	t.Run("compatible forward repair remains ordered and idempotent", func(t *testing.T) {
		reset(t, pool)
		legacy(t, pool, true)
		run(t, pool, "adopt-legacy")
		before := snapshot(t, pool)
		forward := append(steps(), step{3, `COMMENT ON TABLE public."Order" IS 'compatible upgrade';`}, step{4, `COMMENT ON TABLE public."Order" IS 'forward repair';`})
		for range 2 {
			if _, err := execute(context.Background(), pool, "up", forward); err != nil {
				t.Fatal(err)
			}
		}
		if snapshot(t, pool) != before {
			t.Fatal("forward repair changed legacy records/history")
		}
		var count int
		if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM public.bliss_schema_migrations").Scan(&count); err != nil || count != 4 {
			t.Fatalf("repair ledger: %v", err)
		}
		forward[1].sql += " -- changed after application"
		if _, err := execute(context.Background(), pool, "up", forward); err == nil {
			t.Fatal("edited applied SQL accepted")
		}
	})
	t.Run("cancellation releases lock and rolls back", func(t *testing.T) {
		reset(t, pool)
		run(t, pool, "init")
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		slow := append(steps(), step{3, `CREATE TABLE public.partial_change(id int); SELECT pg_sleep(10);`})
		if _, err := execute(ctx, pool, "up", slow); err == nil {
			t.Fatal("cancellation ignored")
		}
		// The server can observe socket closure after the client returns. Busy
		// retries must fail safely until its bounded transaction has rolled back.
		deadline := time.Now().Add(2 * time.Second)
		for {
			_, err := Run(context.Background(), pool, "up", time.Second)
			if err == nil {
				break
			}
			if time.Now().After(deadline) || !strings.Contains(err.Error(), "lock busy") {
				t.Fatal(err)
			}
			time.Sleep(10 * time.Millisecond)
		}
		var clean bool
		if err := pool.QueryRow(context.Background(), "SELECT to_regclass('public.partial_change') IS NULL AND (SELECT count(*)=2 FROM public.bliss_schema_migrations)").Scan(&clean); err != nil || !clean {
			t.Fatalf("cancel left partial changes: %v", err)
		}
	})
	t.Run("dedicated migration owner and runtime DML grants", func(t *testing.T) {
		testRuntimeGrants(t, pool)
	})
	t.Run("approved schema contract matches SQL", func(t *testing.T) {
		reset(t, pool)
		legacy(t, pool, false)
		execSQL(t, pool, read("ledger.sql"))
		tx, err := pool.BeginTx(context.Background(), pgx.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		if _, err = tx.Exec(context.Background(), "SET LOCAL search_path=pg_catalog,public"); err != nil {
			t.Fatal(err)
		}
		actual, err := inspect(context.Background(), tx)
		if err != nil {
			t.Fatal(err)
		}
		expected := map[string]json.RawMessage{}
		if err = json.Unmarshal([]byte(read("schema.json")), &expected); err != nil {
			t.Fatal(err)
		}
		if len(actual) != len(expected) {
			t.Fatal("contract tables differ")
		}
		for name, body := range actual {
			if !sameJSON(body, expected[name]) {
				t.Errorf("contract differs: %s", name)
			}
		}
	})
	reset(t, pool)
}

func testRuntimeGrants(t *testing.T, admin *pgxpool.Pool) {
	t.Helper()
	reset(t, admin)
	suffix := fmt.Sprint(time.Now().UnixNano())
	migrator, runtime := "r06_migrator_"+suffix, "r06_runtime_"+suffix
	identifier := func(role string) string { return pgx.Identifier{role}.Sanitize() }
	for _, role := range []string{migrator, runtime} {
		execSQL(t, admin, "CREATE ROLE "+identifier(role)+" NOLOGIN NOSUPERUSER NOINHERIT")
	}
	t.Cleanup(func() {
		reset(t, admin)
		for _, role := range []string{runtime, migrator} {
			execSQL(t, admin, "DROP OWNED BY "+identifier(role))
			execSQL(t, admin, "DROP ROLE "+identifier(role))
		}
	})
	execSQL(t, admin, "REVOKE CREATE ON SCHEMA public FROM PUBLIC; GRANT USAGE, CREATE ON SCHEMA public TO "+identifier(migrator))
	execSQL(t, admin, "GRANT CREATE ON DATABASE bliss_foundation_test TO "+identifier(migrator))
	rolePool := func(role string) *pgxpool.Pool {
		cfg := admin.Config().Copy()
		cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
			_, err := conn.Exec(ctx, "SET ROLE "+identifier(role))
			return err
		}
		pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		return pool
	}
	migrationPool, runtimePool := rolePool(migrator), rolePool(runtime)
	run(t, migrationPool, "init")
	denied := func(sql string) {
		t.Helper()
		_, err := runtimePool.Exec(context.Background(), sql)
		var pgError *pgconn.PgError
		if !errors.As(err, &pgError) || pgError.Code != "42501" {
			t.Fatalf("expected insufficient privilege, got %v", err)
		}
	}
	// Demonstrate the review finding without preconfigured default privileges.
	execSQL(t, admin, "GRANT USAGE ON SCHEMA public TO "+identifier(runtime))
	denied(`SELECT * FROM public."Order"`)
	denied(`SELECT * FROM public."MpesaTransaction"`)
	// Same explicit business-table grant as the operator runbook; no ledger grant.
	execSQL(t, admin, `GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public."Order", public."MpesaTransaction" TO `+identifier(runtime))
	execSQL(t, runtimePool, `INSERT INTO public."Order"(id,"updatedAt","customerName","customerPhone","deliveryLocation","totalKes",items)
 VALUES ('runtime-order',now(),'Synthetic','254700000000','Test',2000,'[]');
 INSERT INTO public."MpesaTransaction"(id,"updatedAt","orderId") VALUES ('runtime-payment',now(),'runtime-order');
 UPDATE public."Order" SET "paymentStatus"='PAID' WHERE id='runtime-order';
 UPDATE public."MpesaTransaction" SET status='SUCCESS' WHERE id='runtime-payment'`)
	var status, payment string
	if err := runtimePool.QueryRow(context.Background(), `SELECT o."paymentStatus",m.status FROM public."Order" o JOIN public."MpesaTransaction" m ON m."orderId"=o.id WHERE o.id='runtime-order'`).Scan(&status, &payment); err != nil || status != "PAID" || payment != "SUCCESS" {
		t.Fatalf("runtime read/write failed: %v", err)
	}
	execSQL(t, runtimePool, `DELETE FROM public."MpesaTransaction" WHERE id='runtime-payment'; DELETE FROM public."Order" WHERE id='runtime-order'`)
	execSQL(t, admin, `GRANT SELECT ON public.catalog_product,public.catalog_settings,public.stock_control TO `+identifier(runtime))
	execSQL(t, runtimePool, `SELECT id FROM public.catalog_product`)
	denied(`UPDATE public.catalog_product SET stock=99`)
	denied(`INSERT INTO public.stock_batch(id,digest,kind,actor,evidence) VALUES ('bad','bad','receipt','bad','bad')`)
	denied(`UPDATE public.stock_control SET authority='replacement'`)
	denied(`SELECT * FROM public.stock_approval`)
	denied(`CREATE TABLE public.runtime_ddl(id int)`)
	denied(`ALTER TABLE public."Order" ADD COLUMN surprise text`)
	denied(`SELECT * FROM public.bliss_schema_migrations`)
	denied(`UPDATE public.bliss_schema_migrations SET checksum='tampered'`)
	run(t, migrationPool, "status")
}

// Called only by the disposable PowerShell harness, around pg_dump/pg_restore.
func TestRecoveryFixture(t *testing.T) {
	phase := os.Getenv("GO_TEST_RECOVERY_PHASE")
	if phase == "" {
		t.Skip("recovery phase is controlled by the disposable harness")
	}
	pool := testPool(t)
	switch phase {
	case "seed":
		reset(t, pool)
		legacy(t, pool, true)
	case "adopt":
		before := snapshot(t, pool)
		run(t, pool, "adopt-legacy")
		run(t, pool, "up")
		if snapshot(t, pool) != before {
			t.Fatal("adoption changed restored records")
		}
	default:
		t.Fatal("unknown recovery phase")
	}
}
