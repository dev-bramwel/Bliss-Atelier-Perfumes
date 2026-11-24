-- 0013_employee_inventory_permission.sql: add owner-managed employee inventory permission
ALTER TABLE public.app_user
ADD COLUMN IF NOT EXISTS can_manage_inventory BOOLEAN NOT NULL DEFAULT FALSE;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'bliss_runtime') THEN
        GRANT UPDATE(reconciled_until) ON public.stock_control TO bliss_runtime;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'bliss_stock_operator') THEN
        GRANT SELECT(id, email, role, is_active, can_manage_inventory) ON public.app_user TO bliss_stock_operator;
    END IF;
END $$;
