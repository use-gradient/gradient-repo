#!/bin/sh
set -e

REPO="use-gradient/gradient-repo"
BINARY_NAME="gc"
INSTALL_DIR="/usr/local/bin"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  arm64)   ARCH="arm64" ;;
  *)
    echo "Unsupported architecture: $ARCH"
    exit 1
    ;;
esac

case "$OS" in
  darwin|linux) ;;
  *)
    echo "Unsupported OS: $OS"
    exit 1
    ;;
esac

ASSET_NAME="gc-${OS}-${ARCH}"

echo "Gradient CLI Installer"
echo "======================"
echo "OS:   $OS"
echo "Arch: $ARCH"
echo ""

# Get latest release tag
LATEST=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')

if [ -z "$LATEST" ]; then
  echo "Could not determine latest release. Falling back to 'latest'."
  DOWNLOAD_URL="https://github.com/${REPO}/releases/latest/download/${ASSET_NAME}"
else
  echo "Latest version: $LATEST"
  DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${LATEST}/${ASSET_NAME}"
fi

echo "Downloading $BINARY_NAME from $DOWNLOAD_URL ..."

TMP=$(mktemp)
if ! curl -fsSL -o "$TMP" "$DOWNLOAD_URL"; then
  echo ""
  echo "Download failed. Please check:"
  echo "  - https://github.com/${REPO}/releases"
  echo ""
  echo "Or build from source:"
  echo "  git clone https://github.com/${REPO}.git"
  echo "  cd gradient-repo && go build -o gc ./cmd/cli"
  rm -f "$TMP"
  exit 1
fi

chmod +x "$TMP"

if [ -w "$INSTALL_DIR" ]; then
  mv "$TMP" "${INSTALL_DIR}/${BINARY_NAME}"
else
  echo "Installing to ${INSTALL_DIR} (requires sudo)..."
  sudo mv "$TMP" "${INSTALL_DIR}/${BINARY_NAME}"
fi

echo ""
echo "✓ Gradient CLI installed to ${INSTALL_DIR}/${BINARY_NAME}"
echo ""
echo "Get started:"
echo "  gc auth login"
echo "  gc env create --name my-env"
echo ""
