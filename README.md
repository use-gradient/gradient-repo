# Gradient

The Infrastructure Platform That AI Agents Can Actually Use

## Quick Start — One Command

```bash
make dev
```

That's it. This will:
1. Check prerequisites (Go 1.21+, Docker)
2. Create `.env` with local dev defaults
3. Start PostgreSQL via Docker
4. Install Go dependencies
5. Build all binaries (`gradient-api`, `gc` CLI, `gradient-mcp`)
6. Run the test suite
7. Apply database migrations

## Prerequisites

- **Go 1.21+** — [install](https://go.dev/dl/)
- **Docker** — [install](https://docker.com)

That's all you need for local dev. No Clerk, Stripe, or AWS accounts required — the API runs in dev mode with auth disabled and billing mocked.

## After Setup

```bash
# Start the API server
make run-api

# In another terminal — use the CLI
./bin/gc env list
./bin/gc env create --name my-env --region us-east-1

# API health check
curl http://localhost:6767/api/v1/health

# Start MCP server (for AI agents)
make run-mcp
```

## Running the UI

The dashboard is a React + Vite app in the `web/` directory.

```bash
cd web

# Install dependencies
npm install

# Start the dev server
npm run dev
```

The UI will be available at **http://localhost:6969**. It proxies `/api` requests to the backend at `localhost:6767`, so make sure the API server is running (`make run-api`) in another terminal.

| Command | What it does |
|---------|-------------|
| `npm run dev` | Start Vite dev server (port 6969) |
| `npm run build` | Type-check and build for production |
| `npm run preview` | Preview the production build |

## Makefile Targets

| Command | What it does |
|---------|-------------|
| `make dev` | **One-command local setup** |
| `make run-api` | Start API server |
| `make run-mcp` | Start MCP server |
| `make build` | Build all binaries |
| `make test` | Run test suite |
| `make test-v` | Run tests with verbose output |
| `make test-race` | Run tests with race detector |
| `make test-cover` | Generate HTML coverage report |
| `make lint` | Run `fmt` + `vet` |
| `make db-up` | Start PostgreSQL |
| `make db-down` | Stop PostgreSQL |
| `make db-reset` | Wipe and restart PostgreSQL |
| `make install-cli` | Install `gc` to `/usr/local/bin` |
| `make clean` | Remove build artifacts |

## Project Structure

```
gradient/
├── cmd/
│   ├── api/              # REST API server entry point
│   ├── cli/              # CLI tool (gc)
│   └── mcp/              # MCP server entry point
├── internal/
│   ├── api/              # HTTP handlers + auth middleware
│   ├── config/           # Configuration (env vars)
│   ├── db/               # Database connection + migrations
│   ├── mcp/              # MCP JSON-RPC handler
│   ├── models/           # Data models
│   └── services/         # Business logic (env, billing, repo, context)
├── pkg/
│   ├── env/              # Cloud providers (AWS EC2 + Docker)
│   ├── context/          # Context store (branch state tracking)
│   ├── secrets/          # Secrets orchestrator
│   └── scaling/          # Auto-scaling
├── scripts/
│   └── dev-setup.sh      # Local dev bootstrap
├── docker-compose.yml    # PostgreSQL for local dev
├── Makefile              # All commands
└── .env.example          # Environment variables template
```

## Configuration

Copy `.env.example` to `.env` (done automatically by `make dev`). Key settings:

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `DATABASE_URL` | Yes | `postgres://gradient:gradient@localhost:5432/gradient?sslmode=disable` | PostgreSQL connection |
| `PORT` | No | `6767` | API server port |
| `ENV` | No | `development` | `development` = dev mode (no auth) |
| `CLERK_SECRET_KEY` | Prod only | — | Clerk JWT verification |
| `CLERK_PEM_PUBLIC_KEY` | Prod only | — | Clerk RSA public key (PEM) |
| `STRIPE_SECRET_KEY` | Prod only | — | Stripe billing |
| `AWS_AMI_ID` | Prod only | — | Pre-baked AMI for environments |
| `AWS_SECURITY_GROUP_ID` | Prod only | — | EC2 security group |
| `AWS_SUBNET_ID` | Prod only | — | EC2 subnet |

## Documentation

- [MVP Spec](./MVP_SPEC.md) — What we're building
- [Engineering Spec](./ENGINEERING_SPEC.md) — Full technical spec
