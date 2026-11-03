// Package catalog owns database prices and the serialized stock ledger.
package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Product struct {
	ID           string   `json:"id"`
	Slug         string   `json:"slug"`
	Name         string   `json:"name"`
	Category     string   `json:"category"`
	CategorySlug string   `json:"categorySlug"`
	Gender       string   `json:"gender"`
	Size         string   `json:"size"`
	Notes        string   `json:"notes"`
	Description  string   `json:"description"`
	Images       []string `json:"images"`
	PriceKes     int      `json:"priceKes"`
	Available    bool     `json:"available"`
}

type Store struct{ Pool *pgxpool.Pool }

func (s *Store) Products(ctx context.Context) ([]Product, error) {
	rows, err := s.Pool.Query(ctx, `SELECT p.id,p.slug,p.name,p.category,p.category_slug,p.gender,p.size,p.notes,p.description,p.images,
 coalesce(p.price_override_kes,c.common_price_kes),
 (i.authority='replacement' AND coalesce(i.reconciled_until>now(),false) AND p.opened AND p.stock>0)
 FROM public.catalog_product p CROSS JOIN public.catalog_settings c CROSS JOIN public.stock_control i ORDER BY length(p.id),p.id`)
	if err != nil {
		return nil, errors.New("catalog unavailable")
	}
	defer rows.Close()
	products := []Product{}
	for rows.Next() {
		var p Product
		if rows.Scan(&p.ID, &p.Slug, &p.Name, &p.Category, &p.CategorySlug, &p.Gender, &p.Size, &p.Notes, &p.Description, &p.Images, &p.PriceKes, &p.Available) != nil {
			return nil, errors.New("catalog unavailable")
		}
		products = append(products, p)
	}
	if rows.Err() != nil || len(products) == 0 {
		return nil, errors.New("catalog unavailable")
	}
	return products, nil
}

type Line struct {
	ProductID string `json:"productId"`
	// Opening/reconcile quantities are counted balances; receipt/sale quantities are positive units.
	// Adjustment quantity is a signed delta and requires the expected current balance.
	Quantity        int  `json:"quantity"`
	ExpectedBalance *int `json:"expectedBalance,omitempty"`
}

// Missing/null quantities must never silently become a zero opening count.
func (l *Line) UnmarshalJSON(raw []byte) error {
	var input struct {
		ProductID       string `json:"productId"`
		Quantity        *int   `json:"quantity"`
		ExpectedBalance *int   `json:"expectedBalance,omitempty"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&input) != nil || input.Quantity == nil {
		return errors.New("product line requires an integer quantity and valid fields")
	}
	l.ProductID, l.Quantity, l.ExpectedBalance = input.ProductID, *input.Quantity, input.ExpectedBalance
	return nil
}

type Batch struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Actor    string `json:"actor"`
	Evidence string `json:"evidence"`
	Lines    []Line `json:"lines"`
}

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$`)

func (b *Batch) Validate() error {
	if !identifier.MatchString(b.ID) || len(strings.TrimSpace(b.Actor)) < 1 || len(b.Actor) > 128 || len(strings.TrimSpace(b.Evidence)) < 1 || len(b.Evidence) > 512 || len(b.Lines) == 0 || len(b.Lines) > 1000 {
		return errors.New("invalid batch identity, actor, evidence or line count")
	}
	switch b.Kind {
	case "opening", "receipt", "local_sale", "adjustment", "reconcile":
	default:
		return errors.New("invalid batch kind")
	}
	seen := map[string]bool{}
	for _, l := range b.Lines {
		if !identifier.MatchString(l.ProductID) || seen[l.ProductID] || l.Quantity < -1000000 || l.Quantity > 1000000 {
			return errors.New("invalid or duplicate product mapping")
		}
		seen[l.ProductID] = true
		if (b.Kind == "opening" || b.Kind == "reconcile") && l.Quantity < 0 {
			return errors.New("negative counted stock")
		}
		if (b.Kind == "receipt" || b.Kind == "local_sale") && l.Quantity <= 0 {
			return errors.New("quantity must be positive")
		}
		if (b.Kind == "adjustment" || b.Kind == "reconcile") && (l.ExpectedBalance == nil || *l.ExpectedBalance < 0) {
			return errors.New("expectedBalance required for corrections and reconciliation")
		}
	}
	sort.Slice(b.Lines, func(i, j int) bool { return b.Lines[i].ProductID < b.Lines[j].ProductID })
	return nil
}

// Apply is atomic. A global control-row lock serializes operator sales, reconciliation,
// and configuration changes; this is intentional at the shop's current scale.
func (s *Store) Apply(ctx context.Context, b Batch) (replayed bool, err error) {
	if err = b.Validate(); err != nil {
		return false, err
	}
	raw, _ := json.Marshal(b)
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, errors.New("stock database unavailable")
	}
	defer rollback(tx)
	var authority string
	if err = tx.QueryRow(ctx, `SELECT authority FROM public.stock_control WHERE singleton FOR UPDATE`).Scan(&authority); err != nil {
		return false, errors.New("stock control unavailable")
	}
	var existing string
	err = tx.QueryRow(ctx, `SELECT digest FROM public.stock_batch WHERE id=$1`, b.ID).Scan(&existing)
	if err == nil {
		if existing != digest {
			return false, errors.New("batch ID already used with different content")
		}
		return true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, errors.New("stock history unavailable")
	}
	if authority != "replacement" {
		return false, errors.New("stock authority is unconfirmed; replacement approval required")
	}
	if b.Kind == "opening" || b.Kind == "reconcile" {
		var count int
		if tx.QueryRow(ctx, `SELECT count(*) FROM public.catalog_product`).Scan(&count) != nil || count != len(b.Lines) {
			return false, errors.New("counted import must map every catalog product exactly once")
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO public.stock_batch(id,digest,kind,actor,evidence) VALUES ($1,$2,$3,$4,$5)`, b.ID, digest, b.Kind, b.Actor, b.Evidence); err != nil {
		return false, errors.New("stock batch write failed")
	}
	for _, l := range b.Lines {
		var stock int
		var opened bool
		if tx.QueryRow(ctx, `SELECT stock,opened FROM public.catalog_product WHERE id=$1 FOR UPDATE`, l.ProductID).Scan(&stock, &opened) != nil {
			return false, fmt.Errorf("unknown product mapping: %s", l.ProductID)
		}
		if b.Kind == "opening" && opened {
			return false, errors.New("opening balance already recorded")
		}
		if b.Kind != "opening" && !opened {
			return false, errors.New("opening balance required")
		}
		if l.ExpectedBalance != nil && *l.ExpectedBalance != stock {
			return false, errors.New("balance changed; recount before retry")
		}
		delta := l.Quantity
		if b.Kind == "local_sale" {
			delta = -l.Quantity
		}
		if b.Kind == "opening" || b.Kind == "reconcile" {
			delta = l.Quantity - stock
		}
		balance := stock + delta
		if balance < 0 || balance > 1000000 {
			return false, errors.New("insufficient stock or balance limit exceeded")
		}
		if _, err = tx.Exec(ctx, `UPDATE public.catalog_product SET stock=$2,opened=true WHERE id=$1`, l.ProductID, balance); err != nil {
			return false, errors.New("stock update failed")
		}
		if _, err = tx.Exec(ctx, `INSERT INTO public.stock_entry(batch_id,product_id,delta,balance) VALUES ($1,$2,$3,$4)`, b.ID, l.ProductID, delta, balance); err != nil {
			return false, errors.New("stock ledger write failed")
		}
	}
	// Imports and corrections invalidate availability; verified local sales deduct
	// immediately without extending the approved reconciliation deadline.
	if _, err = tx.Exec(ctx, `UPDATE public.stock_control SET reconciled_until=NULL WHERE $1 <> 'local_sale'`, b.Kind); err != nil {
		return false, errors.New("stock approval update failed")
	}
	if tx.Commit(ctx) != nil {
		return false, errors.New("stock commit uncertain; retry identical batch ID and content")
	}
	return false, nil
}

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

// Approve is an owner/operator assertion that the DB is the sole stock authority,
// all local sales are recorded, and the counted balances are signed off.
// Its expiry is bounded to 24 hours; the owner must explicitly approve that policy.
func (s *Store) Approve(ctx context.Context, actor, evidence string, until time.Time) error {
	if len(strings.TrimSpace(actor)) < 1 || len(actor) > 128 || len(strings.TrimSpace(evidence)) < 1 || len(evidence) > 512 || !until.After(time.Now()) || until.After(time.Now().Add(24*time.Hour)) {
		return errors.New("actor, evidence and future expiry within 24 hours required")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return errors.New("stock database unavailable")
	}
	defer rollback(tx)
	var authority string
	if tx.QueryRow(ctx, `SELECT authority FROM public.stock_control WHERE singleton FOR UPDATE`).Scan(&authority) != nil {
		return errors.New("stock control unavailable")
	}
	var matches bool
	if tx.QueryRow(ctx, `SELECT NOT EXISTS (
 SELECT 1 FROM public.catalog_product p WHERE p.stock <>
 coalesce((SELECT sum(e.delta) FROM public.stock_entry e WHERE e.product_id=p.id),0))`).Scan(&matches) != nil || !matches {
		return errors.New("stock ledger mismatch; approval rejected")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO public.stock_approval(actor,evidence,valid_until) VALUES ($1,$2,$3)`, actor, evidence, until); err != nil {
		return errors.New("stock approval audit failed")
	}
	// First approval permits opening import but cannot expose unopened products.
	if _, err = tx.Exec(ctx, `UPDATE public.stock_control SET authority='replacement',approved_by=$1,evidence=$2,reconciled_until=$3`, actor, evidence, until); err != nil {
		return errors.New("stock approval failed")
	}
	if tx.Commit(ctx) != nil {
		return errors.New("stock approval commit uncertain")
	}
	return nil
}
