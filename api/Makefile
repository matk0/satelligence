.PHONY: build run test clean dev docker-build docker-up docker-down migrate

# Build the application
build:
	go build -o bin/trandor ./cmd/server

# Run the application
run: build
	./bin/trandor

# Run in development mode with auto-reload
dev:
	go run ./cmd/server

# Run tests
test:
	go test -v ./...

# Run tests with coverage
test-coverage:
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Clean build artifacts
clean:
	rm -rf bin/
	rm -f coverage.out coverage.html

# Format code
fmt:
	go fmt ./...

# Lint code
lint:
	golangci-lint run

# Docker commands
docker-build:
	docker build -t trandor .

docker-up:
	docker-compose up -d

docker-down:
	docker-compose down

docker-logs:
	docker-compose logs -f

# Database migrations (using golang-migrate)
migrate-up:
	migrate -path internal/db/migrations -database "$$DATABASE_URL" up

migrate-down:
	migrate -path internal/db/migrations -database "$$DATABASE_URL" down

migrate-create:
	@read -p "Migration name: " name; \
	migrate create -ext sql -dir internal/db/migrations -seq $$name
