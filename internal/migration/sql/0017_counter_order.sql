-- Add order channel for counter checkout.
ALTER TABLE public."Order"
ADD COLUMN channel TEXT NOT NULL DEFAULT 'online',
ADD CONSTRAINT order_channel_check CHECK (channel IN ('online', 'counter'));
