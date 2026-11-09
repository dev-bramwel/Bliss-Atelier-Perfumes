ALTER TABLE public.payment_attempt
    ADD COLUMN query_count integer NOT NULL DEFAULT 0,
    ADD COLUMN last_queried_at timestamptz,
    ADD COLUMN next_query_at timestamptz;

CREATE INDEX payment_attempt_reconcile_idx ON public.payment_attempt (status, next_query_at);

CREATE TABLE public.payment_exception (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id text REFERENCES public."Order"(id) ON DELETE SET NULL,
    attempt_id uuid REFERENCES public.payment_attempt(id) ON DELETE SET NULL,
    exception_type text NOT NULL CHECK (exception_type IN ('UNMAPPED_TRANSACTION', 'AMOUNT_MISMATCH', 'DUPLICATE_RECEIPT', 'STALE_ATTEMPT_UNRESOLVED', 'LATE_PAYMENT', 'MANUAL_RESOLUTION_REQUIRED')),
    provider text NOT NULL DEFAULT 'ncba',
    provider_transaction_id text,
    receipt_number text,
    received_amount_kes integer,
    expected_amount_kes integer,
    customer_phone text,
    status text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved', 'dismissed')),
    resolution_action text CHECK (resolution_action IS NULL OR resolution_action IN ('applied_to_order', 'refund_recorded', 'dismissed_duplicate', 'voided', 'manual_adjustment', 'duplicate_correction', 'amount_correction', 'refund_reconciliation')),
    resolution_notes text,
    resolved_by text,
    evidence text,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz
);

CREATE INDEX payment_exception_order_idx ON public.payment_exception (order_id);
CREATE INDEX payment_exception_receipt_idx ON public.payment_exception (receipt_number);
CREATE INDEX payment_exception_status_idx ON public.payment_exception (status, created_at);

CREATE TABLE public.payment_audit_ledger (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id text NOT NULL REFERENCES public."Order"(id) ON DELETE CASCADE,
    action text NOT NULL CHECK (action IN ('manual_settlement', 'duplicate_correction', 'amount_correction', 'refund_reconciliation')),
    amount_kes integer NOT NULL CHECK (amount_kes > 0),
    receipt_number text NOT NULL,
    provider text NOT NULL DEFAULT 'ncba',
    evidence text NOT NULL,
    notes text NOT NULL,
    authorized_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX payment_audit_ledger_order_idx ON public.payment_audit_ledger (order_id);
CREATE INDEX payment_audit_ledger_receipt_idx ON public.payment_audit_ledger (receipt_number);
CREATE INDEX payment_audit_ledger_auth_idx ON public.payment_audit_ledger (authorized_by);

DO $$
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'bliss_runtime') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON public.payment_exception TO bliss_runtime;
        GRANT SELECT, INSERT, UPDATE, DELETE ON public.payment_audit_ledger TO bliss_runtime;
    END IF;
END $$;
