-- Staff visibility is persisted separately from customer and payment records.
CREATE TABLE public.order_assignment (
    order_id text PRIMARY KEY REFERENCES public."Order"(id) ON DELETE CASCADE,
    user_id text NOT NULL REFERENCES public.app_user(id),
    assigned_by text NOT NULL REFERENCES public.app_user(id),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX order_assignment_user_idx ON public.order_assignment (user_id, order_id);
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'bliss_runtime') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON public.order_assignment TO bliss_runtime;
    END IF;
END $$;
