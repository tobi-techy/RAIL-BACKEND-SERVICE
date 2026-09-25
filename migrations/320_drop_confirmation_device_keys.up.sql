-- Retire the Face ID device-key store after the passkey switch.
-- 315 created confirmation_device_keys; the confirmation service no longer
-- reads it (passkeys via WebAuthn replace device keys). Drop in a new
-- migration instead of deleting 315 so databases that already ran 315 stay
-- consistent. Users with enrolled device keys must re-enroll with passkeys.
DROP INDEX IF EXISTS idx_confirmation_device_keys_user;
DROP TABLE IF EXISTS confirmation_device_keys;
