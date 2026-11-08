CREATE TABLE public.guest_order_access (
    order_id TEXT NOT NULL,
    token_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,

    CONSTRAINT guest_order_access_pkey PRIMARY KEY (order_id, token_hash),
    CONSTRAINT guest_order_access_order_fkey FOREIGN KEY (order_id)
        REFERENCES public."Order"(id) ON DELETE CASCADE
);

CREATE INDEX guest_order_access_token_hash_idx ON public.guest_order_access (token_hash);
CREATE INDEX guest_order_access_order_expires_idx ON public.guest_order_access (order_id, expires_at);
