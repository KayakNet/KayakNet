#!/bin/bash
# KayakNet Monero Wallet Setup Script
# This script sets up monero-wallet-rpc for KayakNet escrow

set -e

echo "========================================"
echo "KayakNet Monero Wallet Setup"
echo "========================================"

# Configuration
MONERO_VERSION="0.18.3.4"
MONERO_DIR="/opt/monero"
WALLET_DIR="/var/lib/kayaknet/wallets/monero"
NETWORK="${1:-mainnet}"  # mainnet, stagenet, or testnet

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log() { echo -e "${GREEN}[+]${NC} $1"; }
warn() { echo -e "${YELLOW}[!]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1"; exit 1; }

# Check if running as root
if [ "$EUID" -ne 0 ]; then
    error "Please run as root"
fi

# Create user if not exists
if ! id "kayaknet" &>/dev/null; then
    log "Creating kayaknet user..."
    useradd -r -s /bin/false kayaknet
fi

# Create directories
log "Creating directories..."
mkdir -p "$MONERO_DIR"
mkdir -p "$WALLET_DIR"
mkdir -p /var/log/monero

# Download Monero
if [ ! -f "$MONERO_DIR/monero-wallet-rpc" ]; then
    log "Downloading Monero CLI tools v${MONERO_VERSION}..."
    cd /tmp
    wget -q "https://downloads.getmonero.org/cli/monero-linux-x64-v${MONERO_VERSION}.tar.bz2"
    tar -xjf "monero-linux-x64-v${MONERO_VERSION}.tar.bz2"
    mv monero-x86_64-linux-gnu-v${MONERO_VERSION}/* "$MONERO_DIR/"
    rm -rf "monero-linux-x64-v${MONERO_VERSION}.tar.bz2" monero-x86_64-linux-gnu-v${MONERO_VERSION}
    log "Monero downloaded successfully"
else
    log "Monero already installed"
fi

# Create wallet if not exists
if [ ! -f "$WALLET_DIR/kayaknet-wallet" ]; then
    log "Creating new Monero wallet..."
    
    # Generate wallet password
    WALLET_PASSWORD=$(openssl rand -base64 32)
    echo "$WALLET_PASSWORD" > "$WALLET_DIR/.password"
    chmod 600 "$WALLET_DIR/.password"
    
    # Create wallet
    echo "$WALLET_PASSWORD" | "$MONERO_DIR/monero-wallet-cli" \
        --generate-new-wallet "$WALLET_DIR/kayaknet-wallet" \
        --password-file "$WALLET_DIR/.password" \
        --mnemonic-language English \
        --command exit 2>/dev/null || true
    
    log "Wallet created. IMPORTANT: Save the seed phrase securely!"
else
    log "Wallet already exists"
fi

# Set permissions
chown -R kayaknet:kayaknet "$WALLET_DIR"
chmod -R 700 "$WALLET_DIR"

# Create systemd service for monero-wallet-rpc
NETWORK_FLAG=""
RPC_PORT="18082"
case "$NETWORK" in
    stagenet)
        NETWORK_FLAG="--stagenet"
        RPC_PORT="38082"
        ;;
    testnet)
        NETWORK_FLAG="--testnet"
        RPC_PORT="28082"
        ;;
esac

cat > /etc/systemd/system/monero-wallet-rpc.service << EOF
[Unit]
Description=Monero Wallet RPC for KayakNet
After=network.target

[Service]
Type=simple
User=kayaknet
Group=kayaknet
ExecStart=$MONERO_DIR/monero-wallet-rpc \
    $NETWORK_FLAG \
    --daemon-host node.moneroworld.com \
    --wallet-file $WALLET_DIR/kayaknet-wallet \
    --password-file $WALLET_DIR/.password \
    --rpc-bind-ip 127.0.0.1 \
    --rpc-bind-port $RPC_PORT \
    --disable-rpc-login \
    --confirm-external-bind
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal
SyslogIdentifier=monero-wallet-rpc

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable monero-wallet-rpc

log "Starting monero-wallet-rpc..."
systemctl start monero-wallet-rpc

echo ""
echo "========================================"
echo "Monero Setup Complete!"
echo "========================================"
echo ""
echo "Wallet RPC running on: 127.0.0.1:$RPC_PORT"
echo "Network: $NETWORK"
echo ""
echo "Add to your KayakNet config.json:"
echo "  \"monero\": {"
echo "    \"enabled\": true,"
echo "    \"rpc_host\": \"127.0.0.1\","
echo "    \"rpc_port\": $RPC_PORT,"
echo "    \"network\": \"$NETWORK\""
echo "  }"
echo ""
warn "IMPORTANT: Back up your wallet seed phrase from $WALLET_DIR"
echo ""



