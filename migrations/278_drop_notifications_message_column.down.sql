-- Restore the legacy 'message' column (not used by any code).
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS message TEXT NOT NULL DEFAULT '';
