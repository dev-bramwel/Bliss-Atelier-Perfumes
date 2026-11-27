-- 0014_unused_perfume_exchanges.sql: unused-perfume exchange workflow, quarantined stock, and paid timestamp anchor.

ALTER TABLE public."Order"
    ADD COLUMN IF NOT EXISTS paid_at timestamptz;

UPDATE public."Order"
SET paid_at = "updatedAt"
WHERE "paymentStatus" = 'PAID' AND paid_at IS NULL;

ALTER TABLE public.catalog_product
    ADD COLUMN IF NOT EXISTS quarantined_stock integer NOT NULL DEFAULT 0 CHECK (quarantined_stock >= 0);

ALTER TABLE public.stock_batch
    DROP CONSTRAINT IF EXISTS stock_batch_kind_check;

ALTER TABLE public.stock_batch
    ADD CONSTRAINT stock_batch_kind_check
    CHECK (kind IN ('opening', 'receipt', 'local_sale', 'adjustment', 'reconcile', 'exchange_return', 'exchange_issue'));

CREATE TABLE IF NOT EXISTS public.exchange_request (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id text NOT NULL REFERENCES public."Order"(id) ON DELETE RESTRICT,
    status text NOT NULL DEFAULT 'requested' CHECK (status IN ('requested', 'inspected', 'approved', 'rejected', 'completed')),
    reason text NOT NULL,
    requested_by text NOT NULL,
    requested_by_role text NOT NULL,
    inspected_by text,
    inspected_by_role text,
    inspection_passed boolean,
    inspection_notes text,
    inspected_at timestamptz,
    decided_by text,
    decided_by_role text,
    decision_reason text,
    decided_at timestamptz,
    completed_by text,
    completed_by_role text,
    completed_at timestamptz,
    price_difference_kes integer NOT NULL DEFAULT 0 CHECK (price_difference_kes >= 0),
    topup_receipt text,
    topup_evidence text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS exchange_request_order_idx ON public.exchange_request (order_id);
CREATE INDEX IF NOT EXISTS exchange_request_status_idx ON public.exchange_request (status, created_at DESC);

CREATE TABLE IF NOT EXISTS public.exchange_item (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    exchange_id uuid NOT NULL REFERENCES public.exchange_request(id) ON DELETE CASCADE,
    returned_product_id text NOT NULL REFERENCES public.catalog_product(id),
    returned_quantity integer NOT NULL CHECK (returned_quantity > 0),
    returned_unit_price_kes integer NOT NULL CHECK (returned_unit_price_kes > 0),
    replacement_product_id text NOT NULL REFERENCES public.catalog_product(id),
    replacement_quantity integer NOT NULL CHECK (replacement_quantity > 0),
    replacement_unit_price_kes integer NOT NULL CHECK (replacement_unit_price_kes > 0),
    quarantine_status text NOT NULL DEFAULT 'quarantined' CHECK (quarantine_status IN ('quarantined', 'released_to_stock', 'rejected'))
);

CREATE INDEX IF NOT EXISTS exchange_item_exchange_idx ON public.exchange_item (exchange_id);
CREATE INDEX IF NOT EXISTS exchange_item_returned_product_idx ON public.exchange_item (returned_product_id);

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'bliss_runtime') THEN
        GRANT SELECT, INSERT, UPDATE ON public.exchange_request TO bliss_runtime;
        GRANT SELECT, INSERT, UPDATE ON public.exchange_item TO bliss_runtime;
    END IF;
END $$;
