ALTER TABLE public.guest_order_access ADD COLUMN user_id text REFERENCES public.app_user(id);
CREATE INDEX guest_order_access_user_idx ON public.guest_order_access(user_id) WHERE user_id IS NOT NULL;
