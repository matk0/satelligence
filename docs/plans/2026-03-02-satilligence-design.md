# Satilligence Design Document

**Date:** 2026-03-02
**Status:** Approved

## Overview

Satilligence is a Bitcoin-native AI API gateway that:
- Exposes an OpenAI-compatible API
- Routes requests to AI providers (starting with OpenAI)
- Charges users in satoshis via Lightning Network
- Uses L402 protocol for anonymous, seamless authentication
- Supports NWC (Nostr Wallet Connect) for auto-payments

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Satilligence                             │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│   Client ──▶ L402 Auth ──▶ Rate Limit ──▶ Moderation ──▶ Router │
│                                              │            │     │
│                                          [Reject]    [OpenAI]   │
│                                              │            │     │
│                                          Strike/Ban   Response  │
│                                                           │     │
│                                                     Billing ◀───┘
│         ┌──────────────────────────────────────────────────┘    │
│         ▼                                                       │
│   ┌───────────┐   ┌───────────┐   ┌────────────┐               │
│   │  Session  │   │  Ledger   │   │   Usage    │               │
│   │  Balance  │   │  Entries  │   │   Logs     │               │
│   └─────┬─────┘   └─────┬─────┘   └─────┬──────┘               │
│         └───────────────┴───────────────┘                       │
│                         │                                       │
│              DO Managed PostgreSQL                              │
│                                                                 │
│   Blink API: Invoices + Price Feed                             │
│   NWC: Auto-payment from user wallet                           │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

## Core Components

### 1. API Layer

OpenAI-compatible endpoint with Lightning extensions.

**Core endpoint:**
```
POST /v1/chat/completions
```

**Lightning extensions:**
```
GET  /v1/balance          # Current session balance
POST /v1/invoices         # Create invoice for top-up
GET  /v1/invoices/:id     # Check invoice status
GET  /v1/usage            # Usage history
```

### 2. L402 Authentication

No accounts, no API keys. Payment is authentication.

**First request flow:**
1. Client: `POST /v1/chat/completions` (no auth)
2. Server: `402 Payment Required` + invoice + macaroon
3. Client's NWC wallet auto-pays invoice
4. Blink webhook confirms payment
5. Client retries with `L402 macaroon:preimage` header
6. Server validates, processes request

**Subsequent requests:**
- Use same macaroon while balance sufficient
- New 402 challenge when balance depleted

### 3. Provider Router

Maps model names to providers. Provider-agnostic design.

**MVP models (OpenAI only):**
- gpt-4o
- gpt-4o-mini
- gpt-4-turbo

**Interface:**
```go
type ModelProvider interface {
    Chat(ctx context.Context, req ChatRequest) (ChatResponse, Usage, error)
}
```

### 4. Billing Engine

**Cost calculation:**
1. Get token usage from provider response
2. Look up model pricing (per 1M tokens)
3. Calculate USD cost
4. Apply 5% markup
5. Convert to sats using Blink price feed
6. Round up to nearest sat

**Example:**
```
Input:  150 tokens × ($5.00 / 1M) = $0.00075
Output: 80 tokens × ($15.00 / 1M) = $0.0012
Base:   $0.00195
+5%:    $0.002048
@ $60k: 3.41 sats → 4 sats (rounded up)
```

### 5. Blink Integration

**Used for:**
- Creating Lightning invoices
- Receiving payment webhooks
- BTC/USD price feed

**Key queries:**
```graphql
mutation LnInvoiceCreate($input: LnInvoiceCreateInput!) {
  lnInvoiceCreate(input: $input) {
    invoice { paymentRequest, paymentHash }
  }
}

query RealtimePrice {
  realtimePrice {
    btcSatPrice
    usdCentPrice
  }
}
```

### 6. Abuse Protection

**Layers:**
1. **OpenAI Moderation API** - Check every input before processing
2. **Strike system** - 3 violations = session banned
3. **Rate limiting** - 60 requests/minute per session
4. **Economic barrier** - 5,000 sats minimum initial deposit

## Database Schema

Using DigitalOcean Managed PostgreSQL.

### Sessions
```sql
CREATE TABLE sessions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    macaroon_id     TEXT UNIQUE NOT NULL,
    balance_sats    BIGINT NOT NULL DEFAULT 0,
    nwc_connection  TEXT,  -- encrypted
    strikes         INT NOT NULL DEFAULT 0,
    banned          BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMP NOT NULL DEFAULT NOW(),
    last_used_at    TIMESTAMP NOT NULL DEFAULT NOW()
);
```

### Ledger
```sql
CREATE TABLE ledger (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id      UUID NOT NULL REFERENCES sessions(id),
    type            TEXT NOT NULL,  -- 'deposit' | 'usage'
    amount_sats     BIGINT NOT NULL,
    invoice_id      TEXT,
    reference       TEXT,
    created_at      TIMESTAMP NOT NULL DEFAULT NOW()
);
```

### Usage Logs
```sql
CREATE TABLE usage_logs (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id          UUID NOT NULL REFERENCES sessions(id),
    model               TEXT NOT NULL,
    prompt_tokens       INT NOT NULL,
    completion_tokens   INT NOT NULL,
    cost_usd            DECIMAL(10,6) NOT NULL,
    cost_sats           BIGINT NOT NULL,
    created_at          TIMESTAMP NOT NULL DEFAULT NOW()
);
```

### Invoices
```sql
CREATE TABLE invoices (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id      UUID NOT NULL REFERENCES sessions(id),
    payment_request TEXT NOT NULL,
    payment_hash    TEXT UNIQUE NOT NULL,
    amount_sats     BIGINT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending',  -- 'pending' | 'paid' | 'expired'
    created_at      TIMESTAMP NOT NULL DEFAULT NOW(),
    paid_at         TIMESTAMP
);
```

## Request Flow

```
1. Validate L402 header (or return 402 challenge)
2. Find session by macaroon
3. Check not banned
4. Rate limit check
5. Parse request, identify model
6. Estimate max cost
7. Check balance >= estimated cost (or return 402)
8. Call OpenAI Moderation API
9. If flagged: reject, add strike, maybe ban
10. Call OpenAI Chat API
11. Calculate actual cost
12. Deduct from session balance (atomic transaction)
13. Log usage
14. Return response
```

## Error Handling

| Scenario | Response |
|----------|----------|
| No L402 header | 402 + invoice + macaroon |
| Invalid macaroon | 401 Unauthorized |
| Session banned | 403 Forbidden |
| Insufficient balance | 402 + invoice for top-up |
| Rate limited | 429 Too Many Requests |
| Moderation flagged | 400 + violation notice |
| OpenAI 4xx | Pass through, no charge |
| OpenAI 5xx | 502, no charge |
| OpenAI timeout | 504, no charge |
| Blink down | 503 Service Unavailable |

## Project Structure

```
satilligence/
├── cmd/
│   └── server/
│       └── main.go
├── internal/
│   ├── api/
│   │   ├── handler.go
│   │   ├── middleware.go
│   │   └── routes.go
│   ├── billing/
│   │   ├── calculator.go
│   │   ├── pricing.go
│   │   └── converter.go
│   ├── blink/
│   │   ├── client.go
│   │   ├── invoice.go
│   │   ├── webhook.go
│   │   └── price.go
│   ├── l402/
│   │   ├── macaroon.go
│   │   └── challenge.go
│   ├── provider/
│   │   ├── interface.go
│   │   ├── router.go
│   │   └── openai/
│   │       └── client.go
│   ├── session/
│   │   ├── store.go
│   │   └── ledger.go
│   └── db/
│       ├── postgres.go
│       └── migrations/
├── config/
│   └── config.go
├── docker-compose.yml
├── Dockerfile
├── Makefile
└── go.mod
```

## Deployment

**Infrastructure:**
- DigitalOcean Droplet (Ubuntu, 2GB RAM)
- DigitalOcean Managed PostgreSQL
- Caddy for reverse proxy + auto-HTTPS
- Docker for application

**docker-compose.yml:**
```yaml
services:
  api:
    build: .
    ports:
      - "8080:8080"
    environment:
      - DATABASE_URL=${DO_POSTGRES_URL}
      - BLINK_API_KEY=${BLINK_API_KEY}
      - OPENAI_API_KEY=${OPENAI_API_KEY}
      - MACAROON_SECRET=${MACAROON_SECRET}
    restart: unless-stopped
```

**Caddyfile:**
```
api.satilligence.com {
    reverse_proxy localhost:8080
}
```

## Configuration

Environment variables:
```
DATABASE_URL        # DO Managed PostgreSQL connection string
BLINK_API_KEY       # Blink API authentication
OPENAI_API_KEY      # OpenAI API key
MACAROON_SECRET     # Secret for signing macaroons
MARKUP_PERCENT      # Default: 5
MIN_DEPOSIT_SATS    # Default: 5000
RATE_LIMIT_RPM      # Default: 60
MAX_STRIKES         # Default: 3
```

## MVP Scope

**Included:**
- L402 authentication (anonymous, seamless)
- NWC wallet connection for auto-payments
- `POST /v1/chat/completions` (OpenAI-compatible)
- OpenAI provider (gpt-4o, gpt-4o-mini, gpt-4-turbo)
- OpenAI Moderation API for abuse protection
- Session balance tracking
- Blink for invoices + price feed
- 5% markup on provider costs
- Rate limiting (60 req/min per session)
- 3-strike ban system
- DO Managed PostgreSQL
- Docker + Caddy on DO Droplet

**Not included (future):**
- Streaming responses
- Multiple providers (Anthropic, etc.)
- Traditional accounts / API keys
- Admin dashboard
- Usage analytics UI
- Volume discounts
- Nostr identity integration
- Auto-top-up via NWC

## Tech Stack

- **Language:** Go
- **HTTP Router:** chi
- **Database:** PostgreSQL (DO Managed)
- **GraphQL Client:** genqlient (for Blink)
- **Macaroons:** go-macaroon
- **Deployment:** Docker, Caddy, DigitalOcean

## Security Considerations

- Macaroon secret stored in environment, never logged
- NWC connection strings encrypted at rest
- Blink webhook signatures validated
- All traffic over HTTPS
- No PII collected (anonymous sessions)
- Strike system limits abuse blast radius
- Rate limiting prevents DoS

## Future Enhancements

1. **Streaming responses** - Per-token billing with estimation
2. **Multiple providers** - Anthropic, Mistral, etc.
3. **Nostr identity** - npub-based sessions for AI agents
4. **Auto-top-up** - NWC-initiated replenishment when low
5. **Admin dashboard** - Usage stats, revenue tracking
6. **Volume discounts** - Tiered pricing for heavy users
