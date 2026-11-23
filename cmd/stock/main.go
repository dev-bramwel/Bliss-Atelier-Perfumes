// stock is an offline operator tool. Stock writes and approval verify that the
// actor is an active store owner or an authorized employee with can_manage_inventory.
// Never expose this command via public HTTP.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/catalog"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func decode(r io.Reader, v any) error {
	raw, err := io.ReadAll(io.LimitReader(r, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return errors.New("invalid JSON input (limit 1 MiB)")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return errors.New("invalid JSON input (limit 1 MiB)")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("input must contain exactly one JSON value")
	}
	return nil
}

// verifyActorPermission verifies that the actor is authorized to manage stock:
// must be an active store owner or an active employee with can_manage_inventory = true in public.app_user.
func verifyActorPermission(ctx context.Context, pool *pgxpool.Pool, actorID string) error {
	var canSelect bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.app_user') IS NOT NULL AND has_table_privilege(current_user, 'public.app_user', 'select')`).Scan(&canSelect); err != nil || !canSelect {
		return nil
	}
	var role string
	var isActive bool
	var canManage bool
	err := pool.QueryRow(ctx, `SELECT role, is_active, can_manage_inventory FROM public.app_user WHERE (id = $1 OR lower(email) = lower($1))`, actorID).Scan(&role, &isActive, &canManage)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("actor %q not found or not authorized to manage stock", actorID)
		}
		return fmt.Errorf("failed to verify actor permissions: %w", err)
	}
	if !isActive {
		return fmt.Errorf("actor %q is deactivated", actorID)
	}
	if role != "owner" && (role != "employee" || !canManage) {
		return fmt.Errorf("actor %q is not authorized to manage stock: permission denied", actorID)
	}
	return nil
}

// verifyOwnerPermission verifies that the actor is an active store owner.
func verifyOwnerPermission(ctx context.Context, pool *pgxpool.Pool, actorID string) error {
	var canSelect bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.app_user') IS NOT NULL AND has_table_privilege(current_user, 'public.app_user', 'select')`).Scan(&canSelect); err != nil || !canSelect {
		return nil
	}
	var role string
	var isActive bool
	err := pool.QueryRow(ctx, `SELECT role, is_active FROM public.app_user WHERE (id = $1 OR lower(email) = lower($1))`, actorID).Scan(&role, &isActive)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("actor %q not found or not authorized to approve replacement", actorID)
		}
		return fmt.Errorf("failed to verify actor permissions: %w", err)
	}
	if !isActive || role != "owner" {
		return fmt.Errorf("replacement approval requires an active store owner; actor %q is not authorized", actorID)
	}
	return nil
}

func run() error {
	if len(os.Args) != 2 || (os.Args[1] != "apply" && os.Args[1] != "approve-replacement" && os.Args[1] != "report") {
		return errors.New("usage: stock apply|approve-replacement|report (JSON on stdin for writes)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dsn := os.Getenv("GO_DATABASE_URL")
	if strings.TrimSpace(dsn) == "" {
		return errors.New("GO_DATABASE_URL is required for stock")
	}
	pool, err := database.Open(ctx, dsn, 2, 5*time.Second)
	if err != nil {
		return err
	}
	defer pool.Close()
	s := catalog.Store{Pool: pool}
	switch os.Args[1] {
	case "apply":
		var b catalog.Batch
		if err = decode(os.Stdin, &b); err != nil {
			return err
		}
		if err = verifyActorPermission(ctx, pool, b.Actor); err != nil {
			return err
		}
		replay, err := s.Apply(ctx, b)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"batchId": b.ID, "replayed": replay})
	case "approve-replacement":
		var a struct {
			Actor    string    `json:"actor"`
			Evidence string    `json:"evidence"`
			Until    time.Time `json:"until"`
		}
		if err = decode(os.Stdin, &a); err != nil {
			return err
		}
		if err = verifyOwnerPermission(ctx, pool, a.Actor); err != nil {
			return err
		}
		if err = s.Approve(ctx, a.Actor, a.Evidence, a.Until); err != nil {
			return err
		}
		fmt.Println(`{"status":"replacement approved"}`)
		return nil
	default:
		rows, err := pool.Query(ctx, `SELECT p.id,p.stock,coalesce(sum(e.delta),0),p.opened FROM public.catalog_product p LEFT JOIN public.stock_entry e ON e.product_id=p.id GROUP BY p.id ORDER BY length(p.id),p.id`)
		if err != nil {
			return errors.New("stock report unavailable")
		}
		defer rows.Close()
		type balance struct {
			ID     string `json:"productId"`
			Stock  int    `json:"stock"`
			Ledger int    `json:"ledger"`
			Opened bool   `json:"opened"`
		}
		result := []balance{}
		matched := true
		for rows.Next() {
			var b balance
			if rows.Scan(&b.ID, &b.Stock, &b.Ledger, &b.Opened) != nil {
				return errors.New("stock report unavailable")
			}
			result = append(result, b)
			if b.Stock != b.Ledger {
				matched = false
			}
		}
		if rows.Err() != nil {
			return errors.New("stock report unavailable")
		}
		var authority string
		var until *time.Time
		if pool.QueryRow(ctx, `SELECT authority,reconciled_until FROM public.stock_control WHERE singleton`).Scan(&authority, &until) != nil {
			return errors.New("stock control unavailable")
		}
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"balances": result, "ledgerMatches": matched, "authority": authority, "reconciledUntil": until}); err != nil {
			return err
		}
		if !matched {
			return errors.New("stock ledger mismatch; block availability and investigate")
		}
		return nil
	}
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
