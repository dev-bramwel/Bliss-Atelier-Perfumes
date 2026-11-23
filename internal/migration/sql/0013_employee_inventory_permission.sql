-- 0013_employee_inventory_permission.sql: add owner-managed employee inventory permission
ALTER TABLE public.app_user
ADD COLUMN IF NOT EXISTS can_manage_inventory BOOLEAN NOT NULL DEFAULT FALSE;
