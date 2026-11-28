CREATE TABLE IF NOT EXISTS public.analytics_consumer_dedup (
    event_id text NOT NULL,
    consumer_group text NOT NULL,
    event_type text NOT NULL,
    aggregate_id text NOT NULL,
    aggregate_version integer NOT NULL,
    topic text NOT NULL,
    partition integer NOT NULL,
    kafka_offset bigint NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, consumer_group)
);

CREATE TABLE IF NOT EXISTS public.analytics_consumer_checkpoint (
    consumer_group text NOT NULL,
    topic text NOT NULL,
    partition integer NOT NULL,
    last_offset bigint NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer_group, topic, partition)
);

CREATE TABLE IF NOT EXISTS public.analytics_order_fact (
    order_id text PRIMARY KEY,
    aggregate_version integer NOT NULL DEFAULT 0,
    payment_status text NOT NULL CHECK (payment_status IN ('PENDING', 'PAID')),
    product_revenue_kes integer NOT NULL DEFAULT 0,
    transport_merchant_kes integer NOT NULL DEFAULT 0,
    transport_courier_kes integer NOT NULL DEFAULT 0,
    occurred_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS analytics_order_fact_occurred_idx
    ON public.analytics_order_fact (payment_status, occurred_at);

CREATE TABLE IF NOT EXISTS public.analytics_exchange_fact (
    exchange_id text PRIMARY KEY,
    order_id text NOT NULL,
    price_difference_kes integer NOT NULL DEFAULT 0,
    completed_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS analytics_exchange_fact_completed_idx
    ON public.analytics_exchange_fact (completed_at);

CREATE TABLE IF NOT EXISTS public.analytics_payment_correction_fact (
    audit_id text PRIMARY KEY,
    order_id text NOT NULL,
    action text NOT NULL,
    amount_kes integer NOT NULL DEFAULT 0,
    recorded_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS analytics_payment_correction_fact_recorded_idx
    ON public.analytics_payment_correction_fact (recorded_at);

DO $$
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'bliss_runtime') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON
            public.analytics_consumer_dedup,
            public.analytics_consumer_checkpoint,
            public.analytics_order_fact,
            public.analytics_exchange_fact,
            public.analytics_payment_correction_fact
        TO bliss_runtime;
    END IF;
END $$;
