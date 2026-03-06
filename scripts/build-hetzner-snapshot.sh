#!/usr/bin/env bash
# build-hetzner-snapshot.sh — Build a pre-baked Hetzner server snapshot (golden image)
#
# This script:
# 1. Creates a temporary Hetzner server
# 2. Installs Docker, gradient-agent, and base dev packages
# 3. Takes a Hetzner snapshot of the server
# 4. Destroys the temporary server
#
# The resulting snapshot ID can be used as HETZNER_IMAGE_ID so new environments
# boot in seconds instead of minutes (no apt-get install on every boot).
#
# Requirements:
#   - hcloud CLI installed (brew install hcloud / apt install hcloud-cli)
#   - HETZNER_API_TOKEN environment variable set
#   - gradient-agent binary available at AGENT_DOWNLOAD_URL
#
# Usage:
#   HETZNER_API_TOKEN=xxxx ./scripts/build-hetzner-snapshot.sh
#   HETZNER_API_TOKEN=xxxx AGENT_URL=https://releases.example.com/gradient-agent ./scripts/build-hetzner-snapshot.sh

set -euo pipefail

# --- Configuration ---
HETZNER_LOCATION="${HETZNER_LOCATION:-fsn1}"
SERVER_TYPE="${SERVER_TYPE:-cx22}"
BASE_IMAGE="${BASE_IMAGE:-ubuntu-24.04}"
SNAPSHOT_NAME="${SNAPSHOT_NAME:-gradient-base-$(date +%Y%m%d-%H%M%S)}"
AGENT_URL="${AGENT_URL:-${AGENT_DOWNLOAD_URL:-}}"
SSH_KEY_NAME="${SSH_KEY_NAME:-}" # Optional: name of an SSH key already in Hetzner

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log()  { echo -e "${BLUE}[build]${NC} $*"; }
ok()   { echo -e "${GREEN}[✓]${NC} $*"; }
warn() { echo -e "${YELLOW}[!]${NC} $*"; }
fail() { echo -e "${RED}[✗]${NC} $*" >&2; exit 1; }

# --- Pre-flight checks ---
command -v hcloud >/dev/null 2>&1 || fail "hcloud CLI not found. Install: brew install hcloud"
[ -n "${HETZNER_API_TOKEN:-}" ] || fail "HETZNER_API_TOKEN not set"

export HCLOUD_TOKEN="$HETZNER_API_TOKEN"

TEMP_SERVER_NAME="gradient-snapshot-builder-$$"

cleanup() {
    log "Cleaning up..."
    if hcloud server describe "$TEMP_SERVER_NAME" >/dev/null 2>&1; then
        hcloud server delete "$TEMP_SERVER_NAME" --poll-interval 5s || true
        ok "Temporary server deleted"
    fi
}
trap cleanup EXIT

# --- Step 1: Create temporary server ---
log "Creating temporary server: $TEMP_SERVER_NAME"

SSH_KEY_OPT=""
if [ -n "$SSH_KEY_NAME" ]; then
    SSH_KEY_OPT="--ssh-key $SSH_KEY_NAME"
fi

hcloud server create \
    --name "$TEMP_SERVER_NAME" \
    --type "$SERVER_TYPE" \
    --image "$BASE_IMAGE" \
    --location "$HETZNER_LOCATION" \
    $SSH_KEY_OPT \
    --poll-interval 5s

SERVER_IP=$(hcloud server ip "$TEMP_SERVER_NAME")
ok "Server created: $SERVER_IP"

# --- Step 2: Wait for SSH ---
log "Waiting for SSH to become available..."
for i in $(seq 1 60); do
    if ssh -o StrictHostKeyChecking=no -o ConnectTimeout=5 root@"$SERVER_IP" "echo ok" >/dev/null 2>&1; then
        break
    fi
    sleep 3
done
ok "SSH is ready"

# --- Step 3: Install everything ---
log "Installing Docker, dev tools, and gradient-agent..."

ssh -o StrictHostKeyChecking=no root@"$SERVER_IP" bash <<'INSTALL_SCRIPT'
set -euo pipefail

export DEBIAN_FRONTEND=noninteractive

echo "=== Updating system packages ==="
apt-get update -qq
apt-get upgrade -y -qq

echo "=== Installing Docker ==="
apt-get install -y -qq \
    docker.io \
    containerd

systemctl enable docker
systemctl start docker

echo "=== Installing development tools ==="
apt-get install -y -qq \
    curl \
    wget \
    jq \
    git \
    build-essential \
    gcc \
    g++ \
    make \
    cmake \
    pkg-config \
    libssl-dev \
    libffi-dev \
    python3 \
    python3-pip \
    python3-venv \
    nodejs \
    npm \
    unzip \
    zip \
    htop \
    tmux \
    vim \
    nano \
    tree \
    ca-certificates \
    gnupg \
    lsb-release \
    software-properties-common \
    apt-transport-https \
    openssh-server \
    rsync \
    socat \
    net-tools \
    dnsutils \
    iputils-ping

echo "=== Installing Go (latest stable) ==="
GO_VERSION="1.23.5"
wget -q "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -O /tmp/go.tar.gz
rm -rf /usr/local/go
tar -C /usr/local -xzf /tmp/go.tar.gz
rm /tmp/go.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/profile.d/go.sh

echo "=== Configuring Docker ==="
# Configure Docker for container-first workloads
cat > /etc/docker/daemon.json <<'DOCKERCFG'
{
    "log-driver": "json-file",
    "log-opts": {
        "max-size": "50m",
        "max-file": "3"
    },
    "storage-driver": "overlay2",
    "live-restore": true,
    "default-ulimits": {
        "nofile": {
            "Name": "nofile",
            "Hard": 65536,
            "Soft": 65536
        }
    }
}
DOCKERCFG
systemctl restart docker

echo "=== Setting up gradient directories ==="
mkdir -p /home/gradient/workspace
mkdir -p /gradient/context
mkdir -p /gradient/snapshots
mkdir -p /etc/profile.d

echo "=== Setting up SSH for environment access ==="
# Allow password-less root login (key-based only)
sed -i 's/#PermitRootLogin.*/PermitRootLogin prohibit-password/' /etc/ssh/sshd_config
sed -i 's/PermitRootLogin.*/PermitRootLogin prohibit-password/' /etc/ssh/sshd_config
systemctl restart sshd

echo "=== Pre-pulling base image ==="
docker pull ubuntu:24.04

echo "=== Cleaning up ==="
apt-get clean
rm -rf /var/lib/apt/lists/*
rm -rf /tmp/*

echo "=== Pre-bake complete ==="
INSTALL_SCRIPT

ok "Base packages installed"

# --- Step 3b: Install gradient-agent (if URL provided) ---
if [ -n "$AGENT_URL" ]; then
    log "Installing gradient-agent from $AGENT_URL..."
    ssh -o StrictHostKeyChecking=no root@"$SERVER_IP" bash <<AGENT_INSTALL
set -euo pipefail
curl -fsSL -o /usr/local/bin/gradient-agent "$AGENT_URL"
chmod +x /usr/local/bin/gradient-agent

# Create systemd service template (env vars will be set by cloud-init on actual boot)
cat > /etc/systemd/system/gradient-agent.service <<'AGENTSVC'
[Unit]
Description=Gradient Agent — periodic snapshots, health reporting, and Live Context Mesh
After=docker.service
Requires=docker.service

[Service]
Type=simple
ExecStart=/usr/local/bin/gradient-agent
Restart=always
RestartSec=10
EnvironmentFile=-/etc/gradient/agent.env

[Install]
WantedBy=multi-user.target
AGENTSVC

mkdir -p /etc/gradient
# Don't enable yet — cloud-init will write the env file and start the service
systemctl daemon-reload
echo "gradient-agent installed"
AGENT_INSTALL
    ok "gradient-agent installed"
else
    warn "AGENT_URL not set — gradient-agent will be downloaded at boot time via cloud-init"
fi

# --- Step 4: Power off and snapshot ---
log "Powering off server for snapshot..."
hcloud server poweroff "$TEMP_SERVER_NAME" --poll-interval 5s
ok "Server powered off"

log "Creating snapshot: $SNAPSHOT_NAME..."
SNAPSHOT_RESULT=$(hcloud server create-image \
    --type snapshot \
    --description "Gradient pre-baked base image (Ubuntu 24.04 + Docker + dev tools + gradient-agent)" \
    "$TEMP_SERVER_NAME" \
    2>&1)

# Extract snapshot ID
SNAPSHOT_ID=$(echo "$SNAPSHOT_RESULT" | grep -oP 'Image \K[0-9]+' || echo "$SNAPSHOT_RESULT" | grep -oP 'ID:\s+\K[0-9]+' || echo "")

if [ -z "$SNAPSHOT_ID" ]; then
    # Try parsing from the output differently
    SNAPSHOT_ID=$(hcloud image list --type snapshot --sort created:desc -o noheader -o columns=id | head -1)
fi

ok "Snapshot created!"
echo ""
echo -e "${GREEN}═══════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN} Hetzner Snapshot Ready${NC}"
echo -e "${GREEN}═══════════════════════════════════════════════════════════${NC}"
echo ""
echo -e "  Snapshot Name: ${BLUE}$SNAPSHOT_NAME${NC}"
echo -e "  Snapshot ID:   ${BLUE}$SNAPSHOT_ID${NC}"
echo ""
echo -e "  Add to your ${YELLOW}.env${NC}:"
echo -e "    ${YELLOW}HETZNER_IMAGE_ID=$SNAPSHOT_ID${NC}"
echo ""
echo -e "  This snapshot includes:"
echo "    ✓ Ubuntu 24.04"
echo "    ✓ Docker (with overlay2, log rotation)"
echo "    ✓ Build tools (gcc, g++, make, cmake)"
echo "    ✓ Python 3 + pip + venv"
echo "    ✓ Node.js + npm"
echo "    ✓ Go 1.23.5"
echo "    ✓ Git, curl, jq, htop, tmux, vim"
echo "    ✓ gradient-agent (if AGENT_URL was provided)"
echo "    ✓ Pre-pulled ubuntu:24.04 Docker image"
echo ""
echo -e "${GREEN}═══════════════════════════════════════════════════════════${NC}"
