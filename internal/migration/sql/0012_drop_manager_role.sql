-- Step 1: Migrate any existing manager accounts to employee
UPDATE public.app_user
SET role = 'employee', updated_at = now()
WHERE role = 'manager';

-- Step 2: Replace role check constraint to drop 'manager'
ALTER TABLE public.app_user DROP CONSTRAINT IF EXISTS app_user_role_check;
ALTER TABLE public.app_user ADD CONSTRAINT app_user_role_check CHECK (role IN ('owner', 'employee', 'customer'));
