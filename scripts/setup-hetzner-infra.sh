#!/usr/bin/env bash
# setup-hetzner-infra.sh — Set up Hetzner Cloud infrastructure for Gradient
#
# This script creates all the Hetzner Cloud resources needed to run Gradient:
#   1. SSH key (for server access)
#   2. Firewall (allow SSH + agent health port)
#   3. Private network (for inter-server communication)
#   4. Container registry guidance (Hetzner doesn't have one — uses Docker Hub / GHCR)
#
# After running, it outputs the values you need for your .env file.
#
# Requirements:
#   - hcloud CLI installed (brew install hcloud / apt install hcloud-cli)
#   - HETZNER_API_TOKEN environment variable set
#
# Usage:
#   HETZNER_API_TOKEN=xxxx ./scripts/setup-hetzner-infra.sh

set -euo pipefail

# --- Configuration ---
HETZNER_LOCATION="${HETZNER_LOCATION:-fsn1}"
PROJECT_NAME="${PROJECT_NAME:-gradient}"
SSH_KEY_NAME="${SSH_KEY_NAME:-${PROJECT_NAME}-key}"
FIREWALL_NAME="${FIREWALL_NAME:-${PROJECT_NAME}-fw}"
NETWORK_NAME="${NETWORK_NAME:-${PROJECT_NAME}-net}"
NETWORK_RANGE="${NETWORK_RANGE:-10.0.0.0/16}"
SUBNET_RANGE="${SUBNET_RANGE:-10.0.1.0/24}"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

log()  { echo -e "${BLUE}[setup]${NC} $*"; }
ok()   { echo -e "${GREEN}[✓]${NC} $*"; }
warn() { echo -e "${YELLOW}[!]${NC} $*"; }
fail() { echo -e "${RED}[✗]${NC} $*" >&2; exit 1; }
info() { echo -e "${CYAN}[i]${NC} $*"; }

# --- Pre-flight checks ---
command -v hcloud >/dev/null 2>&1 || fail "hcloud CLI not found. Install: brew install hcloud"
[ -n "${HETZNER_API_TOKEN:-}" ] || fail "HETZNER_API_TOKEN not set"

export HCLOUD_TOKEN="$HETZNER_API_TOKEN"

echo ""
echo -e "${BLUE}╔══════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║        Gradient — Hetzner Infrastructure Setup          ║${NC}"
echo -e "${BLUE}╚══════════════════════════════════════════════════════════╝${NC}"
echo ""

# ─── 1. SSH Key ───────────────────────────────────────────────────────
log "Setting up SSH key..."

SSH_KEY_FILE="$HOME/.ssh/${PROJECT_NAME}_hetzner"
SSH_KEY_ID=""

if hcloud ssh-key describe "$SSH_KEY_NAME" >/dev/null 2>&1; then
    SSH_KEY_ID=$(hcloud ssh-key describe "$SSH_KEY_NAME" -o json | grep -o '"id":[0-9]*' | head -1 | cut -d: -f2)
    ok "SSH key already exists: $SSH_KEY_NAME (ID: $SSH_KEY_ID)"
else
    # Generate SSH key pair if it doesn't exist locally
    if [ ! -f "$SSH_KEY_FILE" ]; then
        ssh-keygen -t ed25519 -f "$SSH_KEY_FILE" -N "" -C "${PROJECT_NAME}@hetzner"
        ok "SSH key pair generated: $SSH_KEY_FILE"
    else
        ok "Using existing key: $SSH_KEY_FILE"
    fi

    # Upload public key to Hetzner
    hcloud ssh-key create \
        --name "$SSH_KEY_NAME" \
        --public-key-from-file "${SSH_KEY_FILE}.pub"

    SSH_KEY_ID=$(hcloud ssh-key describe "$SSH_KEY_NAME" -o json | grep -o '"id":[0-9]*' | head -1 | cut -d: -f2)
    ok "SSH key uploaded: $SSH_KEY_NAME (ID: $SSH_KEY_ID)"
fi

# ─── 2. Firewall ────────────────────────────────────────────────────
log "Setting up firewall..."

FIREWALL_ID=""

if hcloud firewall describe "$FIREWALL_NAME" >/dev/null 2>&1; then
    FIREWALL_ID=$(hcloud firewall describe "$FIREWALL_NAME" -o json | grep -o '"id":[0-9]*' | head -1 | cut -d: -f2)
    ok "Firewall already exists: $FIREWALL_NAME (ID: $FIREWALL_ID)"
else
    hcloud firewall create --name "$FIREWALL_NAME"
    FIREWALL_ID=$(hcloud firewall describe "$FIREWALL_NAME" -o json | grep -o '"id":[0-9]*' | head -1 | cut -d: -f2)

    # Allow SSH (port 22)
    hcloud firewall add-rule "$FIREWALL_NAME" \
        --direction in \
        --protocol tcp \
        --port 22 \
        --source-ips 0.0.0.0/0 \
        --source-ips ::/0 \
        --description "SSH access"

    # Allow agent health (port 8090) — from internal network only
    hcloud firewall add-rule "$FIREWALL_NAME" \
        --direction in \
        --protocol tcp \
        --port 8090 \
        --source-ips "$NETWORK_RANGE" \
        --description "Agent health endpoint (internal)"

    # Allow NATS (port 4222) — from internal network only
    hcloud firewall add-rule "$FIREWALL_NAME" \
        --direction in \
        --protocol tcp \
        --port 4222 \
        --source-ips "$NETWORK_RANGE" \
        --description "NATS Live Context Mesh (internal)"

    # Allow ICMP (ping)
    hcloud firewall add-rule "$FIREWALL_NAME" \
        --direction in \
        --protocol icmp \
        --source-ips 0.0.0.0/0 \
        --source-ips ::/0 \
        --description "ICMP (ping)"

    # Allow all outbound
    hcloud firewall add-rule "$FIREWALL_NAME" \
        --direction out \
        --protocol tcp \
        --port any \
        --destination-ips 0.0.0.0/0 \
        --destination-ips ::/0 \
        --description "All outbound TCP"

    hcloud firewall add-rule "$FIREWALL_NAME" \
        --direction out \
        --protocol udp \
        --port any \
        --destination-ips 0.0.0.0/0 \
        --destination-ips ::/0 \
        --description "All outbound UDP"

    ok "Firewall created: $FIREWALL_NAME (ID: $FIREWALL_ID)"
fi

# ─── 3. Private Network ──────────────────────────────────────────────
log "Setting up private network..."

NETWORK_ID=""

if hcloud network describe "$NETWORK_NAME" >/dev/null 2>&1; then
    NETWORK_ID=$(hcloud network describe "$NETWORK_NAME" -o json | grep -o '"id":[0-9]*' | head -1 | cut -d: -f2)
    ok "Network already exists: $NETWORK_NAME (ID: $NETWORK_ID)"
else
    hcloud network create \
        --name "$NETWORK_NAME" \
        --ip-range "$NETWORK_RANGE"

    NETWORK_ID=$(hcloud network describe "$NETWORK_NAME" -o json | grep -o '"id":[0-9]*' | head -1 | cut -d: -f2)

    # Add subnet
    hcloud network add-subnet "$NETWORK_NAME" \
        --type server \
        --network-zone eu-central \
        --ip-range "$SUBNET_RANGE"

    ok "Network created: $NETWORK_NAME (ID: $NETWORK_ID)"
fi

# ─── 4. Container Registry guidance ────────────────────────────────
echo ""
info "Hetzner does not provide a managed container registry."
info "For snapshot storage, use one of these options:"
echo ""
echo "  Option A: Docker Hub (simplest)"
echo "    REGISTRY_URL=docker.io/yourorg/gradient-envs"
echo "    REGISTRY_USER=yourdockerhubuser"
echo "    REGISTRY_PASS=yourdockerhubtoken"
echo ""
echo "  Option B: GitHub Container Registry (GHCR)"
echo "    REGISTRY_URL=ghcr.io/yourorg/gradient-envs"
echo "    REGISTRY_USER=yourgithubuser"
echo "    REGISTRY_PASS=ghp_yourgithubtoken"
echo ""
echo "  Option C: Self-hosted registry on Hetzner"
echo "    docker run -d -p 5000:5000 --name registry --restart always registry:2"
echo "    REGISTRY_URL=your-hetzner-ip:5000/gradient-envs"
echo ""

# ─── Output ────────────────────────────────────────────────────────
SSH_PRIV_KEY=""
if [ -f "$SSH_KEY_FILE" ]; then
    SSH_PRIV_KEY=$(cat "$SSH_KEY_FILE" | base64 | tr -d '\n')
fi

echo ""
echo -e "${GREEN}═══════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN} Hetzner Infrastructure Ready${NC}"
echo -e "${GREEN}═══════════════════════════════════════════════════════════${NC}"
echo ""
echo -e "  SSH Key:      ${BLUE}$SSH_KEY_NAME${NC} (ID: ${BLUE}$SSH_KEY_ID${NC})"
echo -e "  Firewall:     ${BLUE}$FIREWALL_NAME${NC} (ID: ${BLUE}$FIREWALL_ID${NC})"
echo -e "  Network:      ${BLUE}$NETWORK_NAME${NC} (ID: ${BLUE}$NETWORK_ID${NC})"
echo -e "  Location:     ${BLUE}$HETZNER_LOCATION${NC}"
echo ""
echo -e "  Add these to your ${YELLOW}.env${NC} file:"
echo ""
echo -e "${YELLOW}# Hetzner Cloud${NC}"
echo "HETZNER_API_TOKEN=${HETZNER_API_TOKEN}"
echo "HETZNER_LOCATION=${HETZNER_LOCATION}"
echo "HETZNER_SSH_KEY_IDS=${SSH_KEY_ID}"
echo "HETZNER_FIREWALL_ID=${FIREWALL_ID}"
echo "HETZNER_NETWORK_ID=${NETWORK_ID}"
if [ -n "$SSH_PRIV_KEY" ]; then
    echo ""
    echo "# SSH private key (base64-encoded) — set from file:"
    echo "# HETZNER_SSH_PRIVATE_KEY=\$(cat ${SSH_KEY_FILE} | base64)"
    echo "# Or copy this value directly:"
    echo "HETZNER_SSH_PRIVATE_KEY=${SSH_PRIV_KEY}"
fi
echo ""
echo -e "${GREEN}═══════════════════════════════════════════════════════════${NC}"
echo ""
echo -e "  ${CYAN}Next steps:${NC}"
echo "  1. Set the container registry variables (see options above)"
echo "  2. Optionally build a pre-baked snapshot:"
echo "     ./scripts/build-hetzner-snapshot.sh"
echo "  3. Set up NATS for Live Context Mesh:"
echo "     docker compose up -d nats"
echo "  4. Start the Gradient API server:"
echo "     make run"
echo ""
