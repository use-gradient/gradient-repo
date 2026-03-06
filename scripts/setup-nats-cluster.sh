#!/usr/bin/env bash
#
# setup-nats-cluster.sh — Deploy a single NATS server for Gradient v0.1
#
# v0.1: Single node, single region. No cluster, no gateway.
# v0.2: Add multi-region gateway support.
#
# Usage:
#   ./setup-nats-cluster.sh --region fsn1 --token YOUR_NATS_AUTH_TOKEN [--hetzner-token HCLOUD_TOKEN]
#
set -euo pipefail

REGION="${1:-fsn1}"
NATS_AUTH_TOKEN="${NATS_AUTH_TOKEN:-}"
HCLOUD_TOKEN="${HCLOUD_TOKEN:-}"
NATS_SERVER_TYPE="cx22"  # 2 vCPU, 4 GB RAM — plenty for NATS

usage() {
    echo "Usage: $0 --region <region> --token <nats-auth-token> [--hetzner-token <hcloud-token>]"
    echo ""
    echo "Deploys a single NATS server (v0.1: single-node, no cluster/gateway)."
    echo ""
    echo "Options:"
    echo "  --region        Hetzner datacenter location (default: fsn1)"
    echo "  --token         NATS authentication token"
    echo "  --hetzner-token Hetzner Cloud API token (or set HCLOUD_TOKEN env var)"
    exit 1
}

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --region)    REGION="$2"; shift 2 ;;
        --token)     NATS_AUTH_TOKEN="$2"; shift 2 ;;
        --hetzner-token) HCLOUD_TOKEN="$2"; shift 2 ;;
        -h|--help)   usage ;;
        *)           shift ;;
    esac
done

if [ -z "$NATS_AUTH_TOKEN" ]; then
    echo "Error: --token (NATS auth token) is required"
    usage
fi

SERVER_NAME="gradient-nats-${REGION}"
echo "═══════════════════════════════════════════════════"
echo " Gradient NATS Setup — Region: ${REGION} (v0.1)"
echo "═══════════════════════════════════════════════════"
echo ""

# Generate cloud-init script
CLOUD_INIT=$(cat <<NATS_INIT
#!/bin/bash
set -euo pipefail

# Install NATS server
curl -L https://github.com/nats-io/nats-server/releases/download/v2.10.24/nats-server-v2.10.24-linux-amd64.tar.gz | tar xz
mv nats-server-v2.10.24-linux-amd64/nats-server /usr/local/bin/
chmod +x /usr/local/bin/nats-server

# Install NATS CLI
curl -L https://github.com/nats-io/natscli/releases/download/v0.1.5/nats-0.1.5-linux-amd64.tar.gz | tar xz
mv nats-0.1.5-linux-amd64/nats /usr/local/bin/
chmod +x /usr/local/bin/nats

# Create directories
mkdir -p /data/nats/jetstream /etc/nats /var/log/nats

# Create NATS config (single node, no gateway)
cat > /etc/nats/nats-server.conf <<'CONF'
server_name: ${SERVER_NAME}
listen: 0.0.0.0:4222

jetstream {
    store_dir: /data/nats/jetstream
    max_mem_store: 256MB
    max_file_store: 2GB
}

authorization {
    token: ${NATS_AUTH_TOKEN}
}

http_port: 8222
logtime: true
max_connections: 10000
max_payload: 1MB
write_deadline: 10s
CONF

# Substitute variables
sed -i "s/\\\${SERVER_NAME}/${SERVER_NAME}/g" /etc/nats/nats-server.conf
sed -i "s/\\\${NATS_AUTH_TOKEN}/${NATS_AUTH_TOKEN}/g" /etc/nats/nats-server.conf

# Create systemd service
cat > /etc/systemd/system/nats.service <<'SVC'
[Unit]
Description=NATS Server
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/nats-server -c /etc/nats/nats-server.conf
ExecReload=/bin/kill -HUP \$MAINPID
Restart=always
RestartSec=5
LimitNOFILE=65536
User=root

[Install]
WantedBy=multi-user.target
SVC

systemctl daemon-reload
systemctl enable nats
systemctl start nats

echo "NATS server started successfully"
NATS_INIT
)

echo "Creating NATS server: ${SERVER_NAME}"
echo ""

if command -v hcloud &>/dev/null && [ -n "$HCLOUD_TOKEN" ]; then
    export HCLOUD_TOKEN

    # Create the server
    hcloud server create \
        --name "${SERVER_NAME}" \
        --type "${NATS_SERVER_TYPE}" \
        --location "${REGION}" \
        --image ubuntu-24.04 \
        --user-data "${CLOUD_INIT}" \
        2>&1 || true

    IP=$(hcloud server ip "${SERVER_NAME}" 2>/dev/null || echo "unknown")
    echo ""
    echo "═══════════════════════════════════════════════════"
    echo " NATS Server Deployed!"
    echo "═══════════════════════════════════════════════════"
    echo ""
    echo "  Server:     ${SERVER_NAME}"
    echo "  Region:     ${REGION}"
    echo "  IP:         ${IP}"
    echo "  Client:     nats://${IP}:4222"
    echo "  Monitor:    http://${IP}:8222"
    echo ""
    echo "Add to your .env:"
    echo "  NATS_URL=nats://${IP}:4222"
    echo "  NATS_AUTH_TOKEN=${NATS_AUTH_TOKEN}"
else
    echo "Cloud-init script generated. To deploy manually:"
    echo ""
    echo "1. Create a server in region ${REGION}"
    echo "2. Run the following cloud-init script on it:"
    echo ""
    echo "${CLOUD_INIT}"
fi
