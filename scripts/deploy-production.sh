#!/bin/bash
# KayakNet Production Deployment Script
# Usage: ./deploy-production.sh [bootstrap|vendor|user]

set -e

NODE_TYPE="${1:-user}"
VERSION="0.1.0"
INSTALL_DIR="/opt/kayaknet"
CONFIG_DIR="/etc/kayaknet"
DATA_DIR="/var/lib/kayaknet"
LOG_DIR="/var/log/kayaknet"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log() { echo -e "${GREEN}[+]${NC} $1"; }
warn() { echo -e "${YELLOW}[!]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1"; exit 1; }
info() { echo -e "${CYAN}[*]${NC} $1"; }

banner() {
    echo ""
    echo -e "${CYAN}"
    echo "  _  __                 _     _   _      _   "
    echo " | |/ /__ _ _   _  __ _| | __| \ | | ___| |_ "
    echo " | ' // _\` | | | |/ _\` | |/ /|  \| |/ _ \ __|"
    echo " | . \ (_| | |_| | (_| |   < | |\  |  __/ |_ "
    echo " |_|\_\__,_|\__, |\__,_|_|\_\|_| \_|\___|\__|"
    echo "            |___/                            "
    echo -e "${NC}"
    echo " Production Deployment Script v${VERSION}"
    echo " Node Type: ${NODE_TYPE}"
    echo ""
}

check_root() {
    if [ "$EUID" -ne 0 ]; then
        error "Please run as root"
    fi
}

detect_arch() {
    ARCH=$(uname -m)
    case "$ARCH" in
        x86_64) ARCH="amd64" ;;
        aarch64) ARCH="arm64" ;;
        *) error "Unsupported architecture: $ARCH" ;;
    esac
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    BINARY="kayakd-v${VERSION}-${OS}-${ARCH}"
    log "Detected: ${OS}/${ARCH}"
}

create_user() {
    if ! id "kayaknet" &>/dev/null; then
        log "Creating kayaknet system user..."
        useradd -r -s /bin/false -d "$DATA_DIR" kayaknet
    else
        log "User kayaknet already exists"
    fi
}

create_directories() {
    log "Creating directories..."
    mkdir -p "$INSTALL_DIR"
    mkdir -p "$CONFIG_DIR"
    mkdir -p "$DATA_DIR"
    mkdir -p "$LOG_DIR"
    mkdir -p "$DATA_DIR/wallets"
}

download_binary() {
    if [ -f "$INSTALL_DIR/kayakd" ]; then
        warn "Binary already exists, skipping download"
        return
    fi
    
    log "Downloading KayakNet binary..."
    DOWNLOAD_URL="https://github.com/kayaknet/kayaknet/releases/download/v${VERSION}/${BINARY}.tar.gz"
    
    cd /tmp
    wget -q "$DOWNLOAD_URL" -O kayakd.tar.gz || {
        warn "Download failed, trying to build from source..."
        build_from_source
        return
    }
    
    tar -xzf kayakd.tar.gz
    mv kayakd "$INSTALL_DIR/"
    chmod +x "$INSTALL_DIR/kayakd"
    rm -f kayakd.tar.gz
    
    log "Binary installed to $INSTALL_DIR/kayakd"
}

build_from_source() {
    log "Building from source..."
    
    # Check for Go
    if ! command -v go &> /dev/null; then
        error "Go is not installed. Please install Go 1.21+ first"
    fi
    
    cd /tmp
    git clone https://github.com/kayaknet/kayaknet.git kayaknet-src
    cd kayaknet-src
    go build -ldflags="-s -w" -o "$INSTALL_DIR/kayakd" ./cmd/kayakd
    cd /
    rm -rf /tmp/kayaknet-src
    
    log "Built and installed kayakd"
}

install_config() {
    log "Installing configuration..."
    
    case "$NODE_TYPE" in
        bootstrap)
            CONFIG_TEMPLATE="bootstrap.json"
            SYSTEMD_SERVICE="kayaknet-bootstrap.service"
            ;;
        vendor)
            CONFIG_TEMPLATE="vendor.json"
            SYSTEMD_SERVICE="kayaknet-vendor.service"
            ;;
        user)
            CONFIG_TEMPLATE="user.json"
            SYSTEMD_SERVICE="kayaknet-user.service"
            ;;
        *)
            error "Unknown node type: $NODE_TYPE"
            ;;
    esac
    
    # Download config template if not building from source
    if [ ! -f "$CONFIG_DIR/config.json" ]; then
        TEMPLATE_URL="https://raw.githubusercontent.com/kayaknet/kayaknet/main/config/templates/${CONFIG_TEMPLATE}"
        wget -q "$TEMPLATE_URL" -O "$CONFIG_DIR/config.json" || {
            # Fallback: create basic config
            create_basic_config
        }
    else
        warn "Config already exists, not overwriting"
    fi
}

create_basic_config() {
    log "Creating basic configuration..."
    
    case "$NODE_TYPE" in
        bootstrap)
            cat > "$CONFIG_DIR/config.json" << 'EOF'
{
  "node": {
    "name": "kayaknet-bootstrap",
    "data_dir": "/var/lib/kayaknet",
    "identity_file": "/var/lib/kayaknet/identity.json",
    "mode": "bootstrap"
  },
  "dht": {
    "k": 20,
    "alpha": 3,
    "record_ttl": "24h",
    "refresh_interval": "1h",
    "bootstrap_nodes": []
  },
  "transport": {
    "udp": {
      "enabled": true,
      "listen_addr": "0.0.0.0:4242"
    }
  },
  "security": {
    "rate_limit_messages": 1000,
    "ban_threshold": 20
  },
  "proxy": {
    "enabled": false
  },
  "logging": {
    "level": "info",
    "file": "/var/log/kayaknet/kayakd.log"
  }
}
EOF
            ;;
        vendor)
            cat > "$CONFIG_DIR/config.json" << 'EOF'
{
  "node": {
    "name": "kayaknet-vendor",
    "data_dir": "/var/lib/kayaknet",
    "identity_file": "/var/lib/kayaknet/identity.json",
    "mode": "vendor"
  },
  "dht": {
    "k": 20,
    "bootstrap_nodes": ["203.161.33.237:4242"]
  },
  "transport": {
    "udp": {
      "enabled": true,
      "listen_addr": "0.0.0.0:4242"
    }
  },
  "proxy": {
    "enabled": true,
    "http_port": 8118,
    "socks5_port": 8119
  },
  "crypto": {
    "monero": {
      "enabled": true,
      "rpc_host": "127.0.0.1",
      "rpc_port": 18082
    },
    "zcash": {
      "enabled": true,
      "rpc_host": "127.0.0.1",
      "rpc_port": 8232
    }
  },
  "logging": {
    "level": "info",
    "file": "/var/log/kayaknet/kayakd.log"
  }
}
EOF
            ;;
        *)
            cat > "$CONFIG_DIR/config.json" << 'EOF'
{
  "node": {
    "name": "kayaknet-user",
    "data_dir": "/var/lib/kayaknet",
    "identity_file": "/var/lib/kayaknet/identity.json",
    "mode": "user"
  },
  "dht": {
    "k": 20,
    "bootstrap_nodes": ["203.161.33.237:4242"]
  },
  "transport": {
    "udp": {
      "enabled": true,
      "listen_addr": "0.0.0.0:4242"
    }
  },
  "proxy": {
    "enabled": true,
    "http_port": 8118,
    "socks5_port": 8119
  },
  "logging": {
    "level": "warn"
  }
}
EOF
            ;;
    esac
}

install_systemd() {
    log "Installing systemd service..."
    
    case "$NODE_TYPE" in
        bootstrap)
            cat > /etc/systemd/system/kayaknet.service << EOF
[Unit]
Description=KayakNet Bootstrap Node
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=kayaknet
Group=kayaknet
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/kayakd --config $CONFIG_DIR/config.json
Restart=always
RestartSec=5
StartLimitIntervalSec=0

NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ReadWritePaths=$DATA_DIR $LOG_DIR

LimitNOFILE=65535
MemoryMax=2G

StandardOutput=journal
StandardError=journal
SyslogIdentifier=kayaknet

[Install]
WantedBy=multi-user.target
EOF
            ;;
        vendor)
            cat > /etc/systemd/system/kayaknet.service << EOF
[Unit]
Description=KayakNet Vendor Node
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=kayaknet
Group=kayaknet
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/kayakd --config $CONFIG_DIR/config.json
Restart=always
RestartSec=5
StartLimitIntervalSec=0

NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ReadWritePaths=$DATA_DIR $LOG_DIR

LimitNOFILE=65535
MemoryMax=4G

StandardOutput=journal
StandardError=journal
SyslogIdentifier=kayaknet

[Install]
WantedBy=multi-user.target
EOF
            ;;
        *)
            cat > /etc/systemd/system/kayaknet.service << EOF
[Unit]
Description=KayakNet User Node
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=kayaknet
Group=kayaknet
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/kayakd --config $CONFIG_DIR/config.json
Restart=on-failure
RestartSec=10

NoNewPrivileges=true
ProtectHome=true

LimitNOFILE=4096
MemoryMax=512M

StandardOutput=journal
StandardError=journal
SyslogIdentifier=kayaknet

[Install]
WantedBy=multi-user.target
EOF
            ;;
    esac
    
    systemctl daemon-reload
    systemctl enable kayaknet
}

set_permissions() {
    log "Setting permissions..."
    chown -R kayaknet:kayaknet "$INSTALL_DIR"
    chown -R kayaknet:kayaknet "$CONFIG_DIR"
    chown -R kayaknet:kayaknet "$DATA_DIR"
    chown -R kayaknet:kayaknet "$LOG_DIR"
    chmod 755 "$INSTALL_DIR"
    chmod 700 "$CONFIG_DIR"
    chmod 700 "$DATA_DIR"
    chmod 755 "$LOG_DIR"
    chmod 600 "$CONFIG_DIR/config.json"
}

configure_firewall() {
    log "Configuring firewall..."
    
    if command -v ufw &> /dev/null; then
        ufw allow 4242/udp comment 'KayakNet P2P'
        log "UFW: Opened UDP port 4242"
    elif command -v firewall-cmd &> /dev/null; then
        firewall-cmd --permanent --add-port=4242/udp
        firewall-cmd --reload
        log "firewalld: Opened UDP port 4242"
    else
        warn "No firewall detected. Please manually open UDP port 4242"
    fi
}

start_service() {
    log "Starting KayakNet..."
    systemctl start kayaknet
    sleep 2
    
    if systemctl is-active --quiet kayaknet; then
        log "KayakNet started successfully!"
    else
        error "Failed to start KayakNet. Check logs: journalctl -u kayaknet"
    fi
}

print_summary() {
    echo ""
    echo "========================================"
    echo -e "${GREEN}KayakNet Production Deployment Complete${NC}"
    echo "========================================"
    echo ""
    echo "Node Type:    $NODE_TYPE"
    echo "Binary:       $INSTALL_DIR/kayakd"
    echo "Config:       $CONFIG_DIR/config.json"
    echo "Data:         $DATA_DIR"
    echo "Logs:         $LOG_DIR"
    echo ""
    echo "Commands:"
    echo "  Start:      systemctl start kayaknet"
    echo "  Stop:       systemctl stop kayaknet"
    echo "  Status:     systemctl status kayaknet"
    echo "  Logs:       journalctl -u kayaknet -f"
    echo ""
    
    if [ "$NODE_TYPE" = "vendor" ]; then
        echo "Vendor Setup:"
        echo "  1. Configure Monero wallet (see scripts/wallet-setup/setup-monero.sh)"
        echo "  2. Configure Zcash wallet (see scripts/wallet-setup/setup-zcash.sh)"
        echo "  3. Update $CONFIG_DIR/config.json with wallet credentials"
        echo "  4. Restart: systemctl restart kayaknet"
        echo ""
    fi
    
    if [ "$NODE_TYPE" != "bootstrap" ]; then
        echo "Browser Access:"
        echo "  HTTP Proxy:   127.0.0.1:8118"
        echo "  SOCKS5 Proxy: 127.0.0.1:8119"
        echo "  Homepage:     http://home.kyk"
        echo ""
    fi
}

# Main
banner
check_root
detect_arch
create_user
create_directories
download_binary
install_config
install_systemd
set_permissions
configure_firewall
start_service
print_summary

