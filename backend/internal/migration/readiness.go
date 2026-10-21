package migration

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RuntimeReady checks the catalog and a restricted local deployment attestation.
// The runtime cannot read or modify migration history directly.
func RuntimeReady(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return errors.New("schema unavailable")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if _, err = tx.Exec(ctx, "SET LOCAL search_path=public"); err != nil {
		return errors.New("schema unavailable")
	}
	actual, err := inspect(ctx, tx)
	if err != nil {
		return errors.New("schema unavailable")
	}
	expected := map[string]json.RawMessage{}
	if json.Unmarshal([]byte(read("schema.json")), &expected) != nil {
		return errors.New("schema unavailable")
	}
	for _, name := range []string{"Order", "MpesaTransaction", "bliss_schema_migrations"} {
		if !sameJSON(actual[name], expected[name]) {
			return errors.New("schema unavailable")
		}
	}
	for name, body := range actual {
		if !sameJSON(body, expected[name]) {
			return errors.New("schema unavailable")
		}
	}
	var ready bool
	if tx.QueryRow(ctx, `SELECT bliss_ops.schema_ready() AND bool_and(
 has_table_privilege(name,'SELECT') AND has_table_privilege(name,'INSERT') AND
 has_table_privilege(name,'UPDATE') AND has_table_privilege(name,'DELETE'))
 FROM unnest(ARRAY['public."Order"','public."MpesaTransaction"']) AS tables(name)`).Scan(&ready) != nil || !ready {
		return errors.New("schema unavailable")
	}
	return nil
}

// BaselineChecksum supplies the local deployment's exact ledger expectation.
func BaselineChecksum() string { return checksum(steps()[0].sql) }
