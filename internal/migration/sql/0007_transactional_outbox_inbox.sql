CREATE TABLE public.outbox_event (
    id text PRIMARY KEY,
    event_type text NOT NULL,
    aggregate_id text NOT NULL,
    aggregate_version integer NOT NULL DEFAULT 1,
    correlation_id text NOT NULL DEFAULT '',
    schema_version integer NOT NULL DEFAULT 1,
    payload jsonb NOT NULL,
    destination text NOT NULL DEFAULT 'rabbitmq',
    status text NOT NULL CHECK (status IN ('pending', 'published', 'failed')),
    retry_count integer NOT NULL DEFAULT 0,
    error_message text,
    published_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX outbox_event_pending_idx ON public.outbox_event (destination, status, created_at) WHERE status = 'pending';
CREATE INDEX outbox_event_aggregate_idx ON public.outbox_event (aggregate_id);

CREATE TABLE public.inbox_message (
    id text NOT NULL,
    consumer_name text NOT NULL,
    status text NOT NULL CHECK (status IN ('processing', 'processed', 'dead_lettered')),
    payload jsonb NOT NULL,
    error_message text,
    created_at timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz,
    PRIMARY KEY (id, consumer_name)
);

CREATE INDEX inbox_message_status_idx ON public.inbox_message (status);

DO $$
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'bliss_runtime') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON public.outbox_event TO bliss_runtime;
        GRANT SELECT, INSERT, UPDATE, DELETE ON public.inbox_message TO bliss_runtime;
    END IF;
END $$;
