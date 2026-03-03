# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Trandor is a Bitcoin-native AI API gateway for autonomous AI agents. It exposes an OpenAI-compatible API and accepts payment via Lightning Network using NWC (Nostr Wallet Connect). No accounts, no API keys - payment serves as authentication.

**Target audience**: AI agents with Bitcoin wallets that need to consume AI services.

## Repository Structure

```
trandor/
├── api/          # Go backend (port 8080) - HTTP API, billing, NWC payments
├── web/          # Rails 8 frontend (port 3000) - UI, proxies to API
└── docs/         # Documentation
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

### Docker
```bash
make build        # docker compose build
make up           # docker compose up -d
make down         # docker compose down
make logs         # docker compose logs -f
```

## Architecture

### Payment Method: NWC (Nostr Wallet Connect)
- Agent provides NWC connection string in X-NWC header
- Server estimates cost, charges 2x via NWC
- Server calls AI provider, calculates actual cost
- Server refunds difference back to agent's wallet

### Go API Structure (api/internal/)
- `api/` - HTTP handlers and chi router
- `billing/` - Cost calculation, pricing per model, USD→sats conversion
- `blink/` - Lightning invoices, BTC/USD price feed via CoinGecko
- `nwc/` - Nostr Wallet Connect client
- `provider/` - AI provider interface (OpenAI)
- `models/` - Model feed and validation

### Rails Frontend Structure (web/app/)
- `controllers/api_controller.rb` - Proxies to Go backend
- `javascript/controllers/demo_controller.js` - Interactive demo
- `views/pages/` - Homepage and documentation

### Key API Routes
- `GET /v1/models` - List available models (no auth)
- `POST /v1/chat/completions` - Chat completion with NWC payment
- `POST /v1/chat/completions/stream` - Streaming chat with NWC payment

## Environment Setup

Required environment variables:
- `BLINK_API_KEY` - For Lightning invoices
- `OPENAI_API_KEY` - AI provider
- `MARKUP_PERCENT` - Our markup (default 5%)
- `API_PORT` - Server port (default 8080)

## Tech Stack

**API**: Go 1.24, chi router, go-nostr
**Web**: Rails 8.0, Stimulus.js, Tailwind CSS
**Infrastructure**: Docker, Caddy (production)
