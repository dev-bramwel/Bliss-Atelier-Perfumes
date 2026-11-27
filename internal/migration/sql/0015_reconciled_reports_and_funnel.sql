-- 0015_reconciled_reports_and_funnel.sql: privacy-aware, deduplicated funnel events for R22 analytics.

CREATE TABLE IF NOT EXISTS public.analytics_funnel_event (
    event_id text PRIMARY KEY,
    session_id text NOT NULL,
    event_type text NOT NULL CHECK (event_type IN ('catalog_view', 'cart_add', 'checkout_start', 'payment_initiated')),
    product_id text REFERENCES public.catalog_product(id) ON DELETE SET NULL,
    occurred_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS analytics_funnel_event_time_type_idx
    ON public.analytics_funnel_event (occurred_at, event_type);

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'bliss_runtime') THEN
        GRANT SELECT, INSERT, DELETE ON public.analytics_funnel_event TO bliss_runtime;
    END IF;
END $$;
