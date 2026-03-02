.PHONY: dev dev-api dev-web build up down logs ps clean

# Development - run both services
dev:
	@echo "Starting development servers..."
	@echo "API: http://localhost:8080"
	@echo "Web: http://localhost:3000"
	@make -j2 dev-api dev-web

dev-api:
	cd api && go run ./cmd/server

dev-web:
	cd web && bin/dev

# Docker commands
build:
	docker compose build

up:
	docker compose up -d

down:
	docker compose down

logs:
	docker compose logs -f

ps:
	docker compose ps

# Clean up
clean:
	docker compose down -v
	cd api && rm -rf bin/
	cd web && rm -rf tmp/ log/

# Database
db-create:
	cd web && rails db:create

db-migrate:
	cd web && rails db:migrate

# Testing
test-api:
	cd api && go test -v ./...

test-web:
	cd web && rails test

test: test-api test-web

# Build for production
build-api:
	cd api && go build -o bin/satilligence ./cmd/server

build-web:
	cd web && rails assets:precompile
