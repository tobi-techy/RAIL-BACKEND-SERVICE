-- Per-user Umbra privacy wallets.
-- Private keys are AES-256-GCM encrypted at rest using ENCRYPTION_KEY.
-- The key_ciphertext column NEVER contains plaintext key material.
CREATE TABLE IF NOT EXISTS umbra_wallets (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    solana_address  VARCHAR(64)  NOT NULL,
    key_ciphertext  TEXT         NOT NULL,  -- AES-256-GCM encrypted private key (hex)
    key_version     INTEGER      NOT NULL DEFAULT 1,  -- for key rotation
    network         VARCHAR(10)  NOT NULL DEFAULT 'mainnet' CHECK (network IN ('mainnet', 'devnet')),
    registered      BOOLEAN      NOT NULL DEFAULT FALSE,  -- Umbra on-chain registration done
    registered_at   TIMESTAMP,
    created_at      TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(user_id)
);

CREATE INDEX idx_umbra_wallets_user_id ON umbra_wallets(user_id);
CREATE INDEX idx_umbra_wallets_solana_address ON umbra_wallets(solana_address);
