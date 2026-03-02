-- NWC credits table
-- Tracks overpayment credits per wallet pubkey for future request discounts
CREATE TABLE IF NOT EXISTS nwc_credits (
    wallet_pubkey   TEXT PRIMARY KEY,
    balance_sats    BIGINT NOT NULL DEFAULT 0,
    total_paid      BIGINT NOT NULL DEFAULT 0,
    total_cost      BIGINT NOT NULL DEFAULT 0,
    request_count   INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_nwc_credits_updated_at ON nwc_credits(updated_at);
