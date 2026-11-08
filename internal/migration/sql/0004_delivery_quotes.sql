CREATE TABLE public.delivery_quote (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id text NOT NULL REFERENCES public."Order"(id) ON DELETE CASCADE,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    amount_kes integer NOT NULL CHECK (amount_kes >= 0),
    fee_recipient text NOT NULL CHECK (fee_recipient IN ('merchant', 'courier')),
    arrangement text NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'rejected', 'expired')),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    accepted_at timestamptz,
    rejected_at timestamptz,
    created_by text NOT NULL,
    customer_notes text,
    UNIQUE (order_id, version)
);

CREATE INDEX delivery_quote_order_id_status_idx ON public.delivery_quote (order_id, status);
CREATE INDEX delivery_quote_status_expires_idx ON public.delivery_quote (status, expires_at);
