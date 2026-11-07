CREATE TABLE public.order_item (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id text NOT NULL REFERENCES public."Order"(id) ON DELETE CASCADE,
    product_id text NOT NULL REFERENCES public.catalog_product(id),
    product_name text NOT NULL,
    product_size text NOT NULL,
    unit_price_kes integer NOT NULL CHECK (unit_price_kes > 0),
    quantity integer NOT NULL CHECK (quantity > 0),
    subtotal_kes integer NOT NULL CHECK (subtotal_kes > 0),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE public.stock_reservation (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id text NOT NULL UNIQUE REFERENCES public."Order"(id) ON DELETE CASCADE,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'released', 'committed')),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    released_at timestamptz
);

CREATE TABLE public.stock_reservation_item (
    reservation_id uuid NOT NULL REFERENCES public.stock_reservation(id) ON DELETE CASCADE,
    product_id text NOT NULL REFERENCES public.catalog_product(id),
    quantity integer NOT NULL CHECK (quantity > 0),
    PRIMARY KEY (reservation_id, product_id)
);

CREATE TABLE public.order_fulfillment (
    order_id text PRIMARY KEY REFERENCES public."Order"(id) ON DELETE CASCADE,
    status text NOT NULL DEFAULT 'unfulfilled' CHECK (status IN ('unfulfilled', 'fulfilled', 'cancelled')),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX stock_reservation_status_expires_idx ON public.stock_reservation (status, expires_at);
CREATE INDEX order_item_order_id_idx ON public.order_item (order_id);
