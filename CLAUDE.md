# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Trandor is a Bitcoin-native AI API gateway monorepo. It exposes an OpenAI-compatible API and charges users in satoshis via Lightning Network using L402 protocol for anonymous, seamless authentication.

## Repository Structure

```
trandor/
├── api/          # Go backend (port 8080) - HTTP API, billing, L402 auth
├── web/          # Rails 8 frontend (port 3000) - UI, proxies to API
└── docs/plans/   # Design documentation
```

## Common Commands

### Development (from root)
```bash
make dev          # Start both API and Web servers
make dev-api      # Go API only (localhost:8080)
make dev-web      # Rails + CSS watcher (localhost:3000)
```

### Testing
```bash
make test         # Run all tests
make test-api     # Go tests only: cd api && go test -v ./...
make test-web     # Rails tests only: cd web && rails test
```

### API-specific (from api/)
```bash
make dev          # go run ./cmd/server
make build        # go build -o bin/trandor ./cmd/server
make test         # go test -v ./...
make lint         # golangci-lint run
make fmt          # go fmt ./...
```

### Database Migrations
```bash
# Go API migrations (from api/)
make migrate-up   # migrate -path internal/db/migrations -database "$DATABASE_URL" up
make migrate-down

# Rails migrations (from web/)
rails db:migrate
```

### Docker
```bash
make build        # docker compose build
make up           # docker compose up -d
make down         # docker compose down
make logs         # docker compose logs -f
```

## Architecture

### Payment Methods (3 models)
1. **L402**: Prepaid balance with macaroon-based auth
2. **NWC**: Nostr Wallet Connect auto-payments (estimates 2x, refunds difference)
3. **WebLN**: Browser-based payments via Alby extension

### Go API Structure (api/internal/)
- `api/` - HTTP handlers and chi router (`routes.go` for all endpoints)
- `billing/` - Cost calculation, pricing per model, USD→sats conversion
- `blink/` - Lightning invoices, BTC/USD price feed via GraphQL
- `l402/` - Macaroon-based stateless authentication
- `nwc/` - Nostr Wallet Connect client
- `provider/` - AI provider interface (currently OpenAI)
- `session/` - PostgreSQL session store, balance tracking, strike system
- `db/` - Database connection, migrations

### Rails Frontend Structure (web/app/)
- `controllers/api_controller.rb` - Proxies to Go backend
- `javascript/controllers/` - Stimulus.js (chat_controller.js, demo_controller.js)

### Key API Routes
- `POST /v1/chat/completions` - L402-authenticated chat
- `POST /v1/nwc/chat/completions` - NWC auto-payment chat
- `POST /v1/webln/quote` → `POST /v1/webln/chat/completions` - WebLN flow
- `GET /v1/balance`, `POST /v1/invoices`, `GET /v1/usage` - Session management

## Environment Setup

Copy `.env.example` to `.env` and configure:
- `DATABASE_URL` - PostgreSQL connection
- `BLINK_API_KEY` - Lightning invoices
- `OPENAI_API_KEY` - AI provider
- `MACAROON_SECRET` - Generate with `openssl rand -hex 32`
- `SECRET_KEY_BASE` - Rails secret (generate with `rails secret`)

## Tech Stack

**API**: Go 1.24, chi router, pgx, go-nostr, macaroon.v2
**Web**: Rails 8.0, Stimulus.js, Tailwind CSS, Hotwire
**Infrastructure**: Docker, Caddy (production), PostgreSQL 16
