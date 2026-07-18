-- Public-figure conductors: allow conductors that are not Rail users
-- (politicians, famous investors) whose trades come from public disclosures.
ALTER TABLE conductors ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE conductors ADD COLUMN IF NOT EXISTS source VARCHAR(16) NOT NULL DEFAULT 'rail';
ALTER TABLE conductors ADD COLUMN IF NOT EXISTS external_key VARCHAR(128);

-- One conductor row per public figure.
CREATE UNIQUE INDEX IF NOT EXISTS idx_conductors_external_key
    ON conductors(external_key) WHERE external_key IS NOT NULL;

-- Disclosure-based signals dedupe by (conductor, order_id) where order_id is
-- the disclosure reference. Non-unique: rail signals reuse order_id semantics.
CREATE INDEX IF NOT EXISTS idx_signals_conductor_order
    ON signals(conductor_id, order_id) WHERE order_id IS NOT NULL AND order_id <> '';
