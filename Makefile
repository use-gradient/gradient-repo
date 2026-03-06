.PHONY: dev build build-api build-cli build-mcp build-agent run-api test clean install-cli db-up db-down db-reset deps fmt vet lint setup-hetzner build-snapshot setup-nats dev-ui dev-all build-ui

# ─── One-command setup ────────────────────────────────────────────────
dev:
	@./scripts/dev-setup.sh

# ─── Database ────────────────────────────────────────────────────────
db-up:
	docker compose up -d postgres nats

db-down:
	docker compose down

db-reset:
	docker compose down -v
	docker compose up -d postgres
	@echo "Waiting for Postgres..."
	@sleep 3
	@echo "✓ Database reset"

# ─── Build ───────────────────────────────────────────────────────────
build: build-api build-cli build-mcp build-agent

build-api:
	go build -o bin/gradient-api cmd/api/main.go

build-cli:
	go build -o bin/gc cmd/cli/main.go

build-mcp:
	go build -o bin/gradient-mcp cmd/mcp/main.go

build-agent:
	go build -o bin/gradient-agent cmd/agent/main.go

# Build agent for Linux (for Hetzner servers)
build-agent-linux:
	GOOS=linux GOARCH=amd64 go build -o bin/gradient-agent-linux cmd/agent/main.go

# ─── Run ─────────────────────────────────────────────────────────────
run-api:
	go run cmd/api/main.go

run-mcp:
	go run cmd/mcp/main.go

# ─── Test ────────────────────────────────────────────────────────────
test:
	go test ./... -count=1

test-v:
	go test ./... -count=1 -v

test-race:
	go test ./... -count=1 -race

test-cover:
	go test ./... -count=1 -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html
	@echo "✓ Coverage report: coverage.html"

# ─── Code quality ───────────────────────────────────────────────────
fmt:
	go fmt ./...

vet:
	go vet ./...

lint: fmt vet
	@echo "✓ All checks passed"

# ─── Install ────────────────────────────────────────────────────────
install-cli: build-cli
	sudo cp bin/gc /usr/local/bin/gc
	@echo "✓ gc CLI installed to /usr/local/bin/gc"

# ─── Clean ──────────────────────────────────────────────────────────
clean:
	rm -rf bin/ coverage.out coverage.html

# ─── Hetzner Infrastructure ──────────────────────────────────────────
setup-hetzner:
	@./scripts/setup-hetzner-infra.sh

build-snapshot: build-agent-linux
	@./scripts/build-hetzner-snapshot.sh

# ─── NATS Cluster ────────────────────────────────────────────────────
setup-nats:
	@./scripts/setup-nats-cluster.sh

# ─── Full Stack (dev) ────────────────────────────────────────────────
stack-up:
	docker compose up -d postgres nats
	@echo "Waiting for services..."
	@sleep 3
	@echo "✓ PostgreSQL + NATS running"

stack-up-full:
	docker compose --profile secrets up -d
	@echo "Waiting for services..."
	@sleep 3
	@echo "✓ PostgreSQL + NATS + Vault running"

# ─── Dashboard UI ───────────────────────────────────────────────────
dev-ui: ## Start the dashboard UI dev server (Vite on :5173)
	cd web && npm run dev

dev-all: ## Start everything: API + services + UI
	@echo "Starting API + services..."
	@$(MAKE) dev &
	@echo "Waiting for API to start..."
	@sleep 5
	@echo "Starting dashboard UI..."
	@cd web && npm run dev

build-ui: ## Build UI for production (output to web/dist)
	cd web && npm run build

install-ui: ## Install UI dependencies
	cd web && npm install

# ─── Dependencies ───────────────────────────────────────────────────
deps:
	go mod tidy
	go mod download
