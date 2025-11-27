-- Create idempotency_keys table for ensuring idempotent operations
CREATE TABLE IF NOT EXISTS idempotency_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    idempotency_key VARCHAR(255) NOT NULL UNIQUE,
    request_path VARCHAR(500) NOT NULL,
    request_method VARCHAR(10) NOT NULL,
    request_hash VARCHAR(64) NOT NULL,
    user_id UUID,
    response_status INT NOT NULL,
    response_body JSONB NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    CONSTRAINT fk_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL
);

-- Create index on idempotency_key for fast lookups
CREATE INDEX idx_idempotency_keys_key ON idempotency_keys(idempotency_key);

-- Create index on expires_at for cleanup queries
CREATE INDEX idx_idempotency_keys_expires_at ON idempotency_keys(expires_at);

-- Create index on user_id for user-specific queries
CREATE INDEX idx_idempotency_keys_user_id ON idempotency_keys(user_id) WHERE user_id IS NOT NULL;

-- Create index on created_at for analytics
CREATE INDEX idx_idempotency_keys_created_at ON idempotency_keys(created_at DESC);

-- Add comment explaining the table purpose
COMMENT ON TABLE idempotency_keys IS 'Stores idempotency keys to ensure operations are executed exactly once';
COMMENT ON COLUMN idempotency_keys.idempotency_key IS 'Client-provided unique key for the operation';
COMMENT ON COLUMN idempotency_keys.request_hash IS 'SHA-256 hash of request body to detect request changes';
COMMENT ON COLUMN idempotency_keys.response_status IS 'HTTP status code of the original response';
COMMENT ON COLUMN idempotency_keys.response_body IS 'Complete response body stored as JSON';
COMMENT ON COLUMN idempotency_keys.expires_at IS 'Expiration time after which the key can be cleaned up';
