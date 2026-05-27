-- Add unique constraint on (user_id, image_hash) to prevent duplicate receipt inserts
-- This fixes a TOCTOU race condition in receipt scanning
ALTER TABLE receipt_scans ADD CONSTRAINT uq_receipt_scans_user_hash UNIQUE (user_id, image_hash);
