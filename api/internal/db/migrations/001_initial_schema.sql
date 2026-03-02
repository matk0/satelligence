-- Sessions table
CREATE TABLE IF NOT EXISTS sessions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    macaroon_id     TEXT UNIQUE NOT NULL,
    balance_sats    BIGINT NOT NULL DEFAULT 0,
    nwc_connection  TEXT,
    strikes         INT NOT NULL DEFAULT 0,
    banned          BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMP NOT NULL DEFAULT NOW(),
    last_used_at    TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_sessions_macaroon_id ON sessions(macaroon_id);
CREATE INDEX idx_sessions_created_at ON sessions(created_at);

-- Ledger table
CREATE TABLE IF NOT EXISTS ledger (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id      UUID NOT NULL REFERENCES sessions(id),
    type            TEXT NOT NULL CHECK (type IN ('deposit', 'usage')),
    amount_sats     BIGINT NOT NULL,
    invoice_id      TEXT,
    reference       TEXT,
    created_at      TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ledger_session_id ON ledger(session_id);
CREATE INDEX idx_ledger_created_at ON ledger(created_at);

-- Usage logs table
CREATE TABLE IF NOT EXISTS usage_logs (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id          UUID NOT NULL REFERENCES sessions(id),
    model               TEXT NOT NULL,
    prompt_tokens       INT NOT NULL,
    completion_tokens   INT NOT NULL,
    cost_usd            DECIMAL(10,6) NOT NULL,
    cost_sats           BIGINT NOT NULL,
    created_at          TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_usage_logs_session_id ON usage_logs(session_id);
CREATE INDEX idx_usage_logs_created_at ON usage_logs(created_at);

-- Invoices table
CREATE TABLE IF NOT EXISTS invoices (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id      UUID NOT NULL REFERENCES sessions(id),
    payment_request TEXT NOT NULL,
    payment_hash    TEXT UNIQUE NOT NULL,
    amount_sats     BIGINT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'paid', 'expired')),
    created_at      TIMESTAMP NOT NULL DEFAULT NOW(),
    paid_at         TIMESTAMP
);

CREATE INDEX idx_invoices_session_id ON invoices(session_id);
CREATE INDEX idx_invoices_payment_hash ON invoices(payment_hash);
CREATE INDEX idx_invoices_status ON invoices(status);
