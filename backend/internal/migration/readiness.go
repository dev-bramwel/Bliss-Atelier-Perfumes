package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	if json.Unmarshal([]byte(read("schema-v2.json")), &expected) != nil {
		return errors.New("schema unavailable")
	}
	for _, name := range []string{"Order", "MpesaTransaction", "bliss_schema_migrations", "catalog_settings", "catalog_product", "stock_control", "stock_batch", "stock_entry", "stock_approval"} {
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

// LedgerPredicate is used only by the local deployment attestation.
func LedgerPredicate() string {
	parts := []string{}
	for _, s := range steps() {
		parts = append(parts, fmt.Sprintf("(version=%d AND checksum='%s' AND origin='empty')", s.version, checksum(s.sql)))
	}
	return fmt.Sprintf("count(*)=%d AND coalesce(bool_and(%s),false)", len(parts), strings.Join(parts, " OR "))
}
