-- Store the display bank name on RampHub offramp orders so transaction
-- history can show "GTBank" instead of a provider-scoped bank code.
ALTER TABLE ramphub_orders ADD COLUMN IF NOT EXISTS bank_name TEXT;
