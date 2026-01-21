#!/bin/bash
# KayakNet Zcash Wallet Setup Script
# This script sets up zcashd for KayakNet escrow

set -e

echo "========================================"
echo "KayakNet Zcash Wallet Setup"
echo "========================================"

# Configuration
ZCASH_DIR="/opt/zcash"
DATA_DIR="/var/lib/zcash"
NETWORK="${1:-mainnet}"  # mainnet or testnet

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

# Detect OS
if [ -f /etc/debian_version ]; then
    OS="debian"
elif [ -f /etc/redhat-release ]; then
    OS="redhat"
else
    error "Unsupported OS. Please install Zcash manually."
fi

# Install Zcash
if ! command -v zcashd &> /dev/null; then
    log "Installing Zcash..."
    
    if [ "$OS" = "debian" ]; then
        # Add Zcash repository
        apt-get update
        apt-get install -y apt-transport-https wget gnupg2
        
        wget -qO - https://apt.z.cash/zcash.asc | gpg --import
        gpg --export 3FE63B67F85EA808DE9B880E6DEF3BAF272766C0 | apt-key add -
        
        echo "deb [arch=amd64] https://apt.z.cash/ bookworm main" > /etc/apt/sources.list.d/zcash.list
        apt-get update
        apt-get install -y zcash
    else
        # For RHEL/CentOS, download binaries
        warn "Please install Zcash manually for this OS"
        exit 1
    fi
else
    log "Zcash already installed"
fi

# Create directories
log "Creating directories..."
mkdir -p "$DATA_DIR"
mkdir -p /var/log/zcash

# Generate RPC credentials
RPC_USER="kayaknet"
RPC_PASSWORD=$(openssl rand -base64 32)

# Create zcash.conf
NETWORK_PARAM=""
RPC_PORT="8232"
if [ "$NETWORK" = "testnet" ]; then
    NETWORK_PARAM="testnet=1"
    RPC_PORT="18232"
fi

cat > "$DATA_DIR/zcash.conf" << EOF
# KayakNet Zcash Configuration
$NETWORK_PARAM

# RPC Settings
rpcuser=$RPC_USER
rpcpassword=$RPC_PASSWORD
rpcbind=127.0.0.1
rpcport=$RPC_PORT
rpcallowip=127.0.0.1

# Network
listen=1
maxconnections=16

# Wallet
wallet=kayaknet-wallet

# Performance
dbcache=512
par=2

# Privacy
allowdeprecated=getnewaddress
allowdeprecated=z_getnewaddress
allowdeprecated=z_getbalance
allowdeprecated=z_gettotalbalance
allowdeprecated=z_listaddresses
allowdeprecated=z_listunspent
allowdeprecated=z_sendmany

# Logging
debug=rpc
EOF

# Save RPC password
echo "$RPC_PASSWORD" > "$DATA_DIR/.rpc_password"
chmod 600 "$DATA_DIR/.rpc_password"

# Set permissions
chown -R kayaknet:kayaknet "$DATA_DIR"
chmod -R 700 "$DATA_DIR"

# Fetch Zcash params if needed
if [ ! -d "/home/kayaknet/.zcash-params" ] && [ ! -d "$DATA_DIR/zcash-params" ]; then
    log "Fetching Zcash parameters (this may take a while)..."
    su -s /bin/bash kayaknet -c "zcash-fetch-params" || {
        # Alternative: use system-wide params
        mkdir -p /usr/share/zcash-params
        zcash-fetch-params || warn "Failed to fetch params, you may need to do this manually"
    }
fi

# Create systemd service
cat > /etc/systemd/system/zcashd.service << EOF
[Unit]
Description=Zcash Daemon for KayakNet
After=network.target

[Service]
Type=forking
User=kayaknet
Group=kayaknet
ExecStart=/usr/bin/zcashd -datadir=$DATA_DIR -daemon -pid=$DATA_DIR/zcashd.pid
ExecStop=/usr/bin/zcash-cli -datadir=$DATA_DIR stop
PIDFile=$DATA_DIR/zcashd.pid
Restart=on-failure
RestartSec=30
TimeoutStartSec=infinity
TimeoutStopSec=600
StandardOutput=journal
StandardError=journal
SyslogIdentifier=zcashd

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable zcashd

log "Starting zcashd..."
systemctl start zcashd

# Wait for RPC to be ready
log "Waiting for Zcash RPC to be ready..."
sleep 10

echo ""
echo "========================================"
echo "Zcash Setup Complete!"
echo "========================================"
echo ""
echo "Zcash RPC running on: 127.0.0.1:$RPC_PORT"
echo "Network: $NETWORK"
echo "RPC User: $RPC_USER"
echo "RPC Password: $RPC_PASSWORD"
echo ""
echo "Add to your KayakNet config.json:"
echo "  \"zcash\": {"
echo "    \"enabled\": true,"
echo "    \"rpc_host\": \"127.0.0.1\","
echo "    \"rpc_port\": $RPC_PORT,"
echo "    \"rpc_user\": \"$RPC_USER\","
echo "    \"rpc_password\": \"$RPC_PASSWORD\","
echo "    \"network\": \"$NETWORK\""
echo "  }"
echo ""
warn "IMPORTANT: The blockchain will take time to sync before transactions work"
warn "You can check sync status with: zcash-cli -datadir=$DATA_DIR getblockchaininfo"
echo ""



