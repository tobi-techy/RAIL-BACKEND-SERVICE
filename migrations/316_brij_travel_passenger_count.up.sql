-- Track how many passengers a BRIJ flight intent is priced for. BRIJ sizes the
-- offer per passenger (intent.passenger_count), and /air/book must submit that
-- many. Rail supports a single-adult booking today: the count is persisted with
-- the intent so BookFlight can reject multi-passenger intents before any user
-- funds or escrow money move, instead of discovering the mismatch after payment.
ALTER TABLE travel_orders ADD COLUMN IF NOT EXISTS passenger_count INTEGER NOT NULL DEFAULT 1;