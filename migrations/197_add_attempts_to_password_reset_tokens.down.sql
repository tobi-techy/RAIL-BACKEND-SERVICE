ALTER TABLE password_reset_tokens
    DROP COLUMN IF EXISTS attempts,
    ALTER COLUMN selector TYPE VARCHAR(32) USING LEFT(selector, 32);
