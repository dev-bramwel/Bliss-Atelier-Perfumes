CREATE TABLE public.idempotency_key (
    key text NOT NULL,
    caller_scope text NOT NULL,
    operation text NOT NULL,
    request_hash text NOT NULL,
    status text NOT NULL CHECK (status IN ('in_progress', 'completed', 'failed')),
    status_code integer NOT NULL DEFAULT 0,
    response_headers jsonb NOT NULL DEFAULT '{}'::jsonb,
    response_body text NOT NULL DEFAULT '',
    resource_id text,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (key, caller_scope, operation)
);

CREATE INDEX idempotency_key_expires_idx ON public.idempotency_key (expires_at);

CREATE TABLE public.payment_attempt (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id text NOT NULL REFERENCES public."Order"(id) ON DELETE CASCADE,
    attempt_number integer NOT NULL CHECK (attempt_number > 0),
    amount_kes integer NOT NULL CHECK (amount_kes > 0),
    phone text NOT NULL,
    status text NOT NULL CHECK (status IN ('created', 'initiating', 'accepted', 'succeeded', 'failed', 'cancelled', 'unknown')),
    provider text NOT NULL DEFAULT 'mpesa_stk',
    provider_transaction_id text,
    provider_reference_id text,
    error_message text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (order_id, attempt_number)
);

CREATE INDEX payment_attempt_order_idx ON public.payment_attempt (order_id);
CREATE INDEX payment_attempt_status_idx ON public.payment_attempt (status);
