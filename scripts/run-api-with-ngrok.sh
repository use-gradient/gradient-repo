#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT_DIR"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

info()  { echo -e "${GREEN}▸${NC} $1"; }
warn()  { echo -e "${YELLOW}▸${NC} $1"; }
fail()  { echo -e "${RED}✗${NC} $1"; exit 1; }

# Check if ngrok is installed
if ! command -v ngrok >/dev/null 2>&1; then
  warn "ngrok is not installed. Installing via Homebrew..."
  if command -v brew >/dev/null 2>&1; then
    brew install ngrok/ngrok/ngrok
  else
    fail "ngrok is not installed and Homebrew is not available. Please install ngrok manually from https://ngrok.com/download"
  fi
fi

# Check if .env exists
if [ ! -f .env ]; then
  fail ".env file not found. Run 'make dev' first to create it."
fi

# Read PORT from .env (default to 6767)
PORT=$(grep "^PORT=" .env 2>/dev/null | cut -d= -f2- || echo "6767")
if [ -z "$PORT" ]; then
  PORT=6767
fi

# Read NGROK_DOMAIN from .env (optional, for static domains)
NGROK_DOMAIN=$(grep "^NGROK_DOMAIN=" .env 2>/dev/null | cut -d= -f2- || echo "")
if [ -z "$NGROK_DOMAIN" ]; then
  # Default to 6767-gradient if not specified
  NGROK_DOMAIN="6767-gradient"
fi

# Check if ngrok is already running
if curl -sf http://localhost:4040/api/tunnels >/dev/null 2>&1; then
  warn "ngrok appears to be already running on port 4040"
  warn "Using existing ngrok instance. If you need a fresh tunnel, stop it first."
  NGROK_PID=""
else
  # Start ngrok in background with custom domain
  # Try using --domain flag (works with ngrok free accounts if domain is configured)
  info "Starting ngrok tunnel on port $PORT with domain: $NGROK_DOMAIN..."
  ngrok http "$PORT" --domain="$NGROK_DOMAIN" > /tmp/ngrok.log 2>&1 &
  NGROK_PID=$!
  
  # Wait a moment to check if it started successfully
  sleep 2
  if ! kill -0 $NGROK_PID 2>/dev/null; then
    # Process died, likely domain doesn't exist - fallback to random domain
    warn "Failed to use domain '$NGROK_DOMAIN' (may require ngrok account setup)"
    warn "Falling back to random domain..."
    ngrok http "$PORT" > /tmp/ngrok.log 2>&1 &
    NGROK_PID=$!
  fi
  
  # Wait for ngrok to start
  info "Waiting for ngrok to start..."
  sleep 3
fi

# Try to get ngrok URL from API (ngrok web interface)
NGROK_URL=""
for i in {1..10}; do
  NGROK_URL=$(curl -s http://localhost:4040/api/tunnels 2>/dev/null | \
    python3 -c "import sys, json; data = json.load(sys.stdin); tunnels = data.get('tunnels', []); print(tunnels[0]['public_url'] if tunnels else '')" 2>/dev/null || echo "")
  
  if [ -n "$NGROK_URL" ]; then
    break
  fi
  sleep 1
done

if [ -z "$NGROK_URL" ]; then
  # Fallback: try to parse from ngrok log (macOS-compatible)
  NGROK_URL=$(grep -oE 'https://[a-z0-9]+\.ngrok(-free)?\.app' /tmp/ngrok.log 2>/dev/null | head -1 || echo "")
fi

if [ -z "$NGROK_URL" ]; then
  warn "Could not automatically detect ngrok URL. Please check http://localhost:4040 for the URL."
  warn "You can manually update LINEAR_REDIRECT_URI in .env with: https://your-ngrok-id.ngrok.io/api/v1/integrations/linear/callback"
  NGROK_URL="https://your-ngrok-id.ngrok.io"
else
  info "ngrok URL: $NGROK_URL"
  
  # Update LINEAR_REDIRECT_URI in .env
  LINEAR_REDIRECT_URI="${NGROK_URL}/api/v1/integrations/linear/callback"
  
  if grep -q "^LINEAR_REDIRECT_URI=" .env 2>/dev/null; then
    if [[ "$OSTYPE" == "darwin"* ]]; then
      sed -i '' "s|^LINEAR_REDIRECT_URI=.*|LINEAR_REDIRECT_URI=$LINEAR_REDIRECT_URI|" .env
    else
      sed -i "s|^LINEAR_REDIRECT_URI=.*|LINEAR_REDIRECT_URI=$LINEAR_REDIRECT_URI|" .env
    fi
  else
    # Add it if it doesn't exist
    echo "LINEAR_REDIRECT_URI=$LINEAR_REDIRECT_URI" >> .env
  fi
  
  info "Updated LINEAR_REDIRECT_URI in .env: $LINEAR_REDIRECT_URI"
  info "Update your Linear OAuth app callback URL to: $LINEAR_REDIRECT_URI"
fi

# Cleanup function
cleanup() {
  if [ -n "$NGROK_PID" ]; then
    info "Stopping ngrok..."
    kill $NGROK_PID 2>/dev/null || true
    wait $NGROK_PID 2>/dev/null || true
    info "ngrok stopped"
  fi
}

# Trap SIGINT and SIGTERM to cleanup
trap cleanup EXIT INT TERM

# Start the API server
info "Starting API server on port $PORT..."
info "ngrok dashboard: http://localhost:4040"
info "Press Ctrl+C to stop both ngrok and the API server"
echo ""

# Export environment variables from .env (using grep+cut to avoid issues with SSH keys)
export PORT=$(grep "^PORT=" .env | cut -d= -f2-)
export ENV=$(grep "^ENV=" .env | cut -d= -f2-)
export DATABASE_URL=$(grep "^DATABASE_URL=" .env | cut -d= -f2-)
export CLERK_SECRET_KEY=$(grep "^CLERK_SECRET_KEY=" .env | cut -d= -f2-)
export CLERK_PUBLISHABLE_KEY=$(grep "^CLERK_PUBLISHABLE_KEY=" .env | cut -d= -f2-)
export CLERK_JWKS_URL=$(grep "^CLERK_JWKS_URL=" .env | cut -d= -f2-)
export CLERK_PEM_PUBLIC_KEY=$(grep "^CLERK_PEM_PUBLIC_KEY=" .env | cut -d= -f2-)
export STRIPE_SECRET_KEY=$(grep "^STRIPE_SECRET_KEY=" .env | cut -d= -f2-)
export STRIPE_WEBHOOK_SECRET=$(grep "^STRIPE_WEBHOOK_SECRET=" .env | cut -d= -f2-)
export STRIPE_PRICE_SMALL_ID=$(grep "^STRIPE_PRICE_SMALL_ID=" .env | cut -d= -f2-)
export STRIPE_PRICE_MEDIUM_ID=$(grep "^STRIPE_PRICE_MEDIUM_ID=" .env | cut -d= -f2-)
export STRIPE_PRICE_LARGE_ID=$(grep "^STRIPE_PRICE_LARGE_ID=" .env | cut -d= -f2-)
export STRIPE_PRICE_GPU_ID=$(grep "^STRIPE_PRICE_GPU_ID=" .env | cut -d= -f2-)
export HETZNER_API_TOKEN=$(grep "^HETZNER_API_TOKEN=" .env | cut -d= -f2-)
export HETZNER_LOCATION=$(grep "^HETZNER_LOCATION=" .env | cut -d= -f2-)
export HETZNER_SSH_KEY_IDS=$(grep "^HETZNER_SSH_KEY_IDS=" .env | cut -d= -f2-)
export HETZNER_SSH_PRIVATE_KEY=$(grep "^HETZNER_SSH_PRIVATE_KEY=" .env | cut -d= -f2-)
export HETZNER_FIREWALL_ID=$(grep "^HETZNER_FIREWALL_ID=" .env | cut -d= -f2-)
export HETZNER_NETWORK_ID=$(grep "^HETZNER_NETWORK_ID=" .env | cut -d= -f2-)
export HETZNER_IMAGE_ID=$(grep "^HETZNER_IMAGE_ID=" .env | cut -d= -f2-)
export REGISTRY_URL=$(grep "^REGISTRY_URL=" .env | cut -d= -f2-)
export REGISTRY_USER=$(grep "^REGISTRY_USER=" .env | cut -d= -f2-)
export REGISTRY_PASS=$(grep "^REGISTRY_PASS=" .env | cut -d= -f2-)
export VAULT_ADDR=$(grep "^VAULT_ADDR=" .env | cut -d= -f2-)
export VAULT_TOKEN=$(grep "^VAULT_TOKEN=" .env | cut -d= -f2-)
export JWT_SECRET=$(grep "^JWT_SECRET=" .env | cut -d= -f2-)
export API_URL=$(grep "^API_URL=" .env | cut -d= -f2-)
export AGENT_DOWNLOAD_URL=$(grep "^AGENT_DOWNLOAD_URL=" .env | cut -d= -f2-)
export NATS_URL=$(grep "^NATS_URL=" .env | cut -d= -f2-)
export NATS_AUTH_TOKEN=$(grep "^NATS_AUTH_TOKEN=" .env | cut -d= -f2-)
export NATS_MAX_AGE=$(grep "^NATS_MAX_AGE=" .env | cut -d= -f2-)
export WARM_POOL_DEFAULT_SIZE=$(grep "^WARM_POOL_DEFAULT_SIZE=" .env | cut -d= -f2-)
export WARM_POOL_MAX_SIZE=$(grep "^WARM_POOL_MAX_SIZE=" .env | cut -d= -f2-)
export WARM_POOL_IDLE_TIMEOUT=$(grep "^WARM_POOL_IDLE_TIMEOUT=" .env | cut -d= -f2-)
export GITHUB_APP_ID=$(grep "^GITHUB_APP_ID=" .env | cut -d= -f2-)
export GITHUB_APP_WEBHOOK_SECRET=$(grep "^GITHUB_APP_WEBHOOK_SECRET=" .env | cut -d= -f2-)
export LINEAR_CLIENT_ID=$(grep "^LINEAR_CLIENT_ID=" .env | cut -d= -f2-)
export LINEAR_CLIENT_SECRET=$(grep "^LINEAR_CLIENT_SECRET=" .env | cut -d= -f2-)
export LINEAR_REDIRECT_URI=$(grep "^LINEAR_REDIRECT_URI=" .env | cut -d= -f2-)
export MCP_SERVER_PORT=$(grep "^MCP_SERVER_PORT=" .env | cut -d= -f2-)
export LOG_LEVEL=$(grep "^LOG_LEVEL=" .env | cut -d= -f2-)

# Run the API server
go run cmd/api/main.go
