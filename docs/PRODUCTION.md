# KayakNet Production Deployment Guide

This guide covers deploying KayakNet nodes in a production environment.

## Table of Contents

1. [System Requirements](#system-requirements)
2. [Quick Start](#quick-start)
3. [Deployment Types](#deployment-types)
4. [Configuration](#configuration)
5. [Security Hardening](#security-hardening)
6. [Cryptocurrency Setup](#cryptocurrency-setup)
7. [Monitoring](#monitoring)
8. [Troubleshooting](#troubleshooting)

---

## System Requirements

### Bootstrap Node
- CPU: 2+ cores
- RAM: 2GB minimum, 4GB recommended
- Storage: 20GB SSD
- Network: Static IP, 100Mbps+ with low latency
- OS: Ubuntu 22.04 LTS / Debian 12 / RHEL 9

### Vendor Node
- CPU: 4+ cores
- RAM: 8GB minimum (for crypto wallets)
- Storage: 100GB+ SSD (blockchain data)
- Network: 50Mbps+ stable connection
- OS: Ubuntu 22.04 LTS / Debian 12

### User Node
- CPU: 1+ cores
- RAM: 512MB minimum
- Storage: 1GB
- Network: Any stable connection
- OS: Linux, macOS, Windows

---

## Quick Start

### One-Line Install (Linux)

```bash
curl -sSL https://raw.githubusercontent.com/kayaknet/kayaknet/main/scripts/deploy-production.sh | sudo bash -s user
```

### Manual Installation

```bash
# Download
wget https://github.com/KayakNet/downloads/releases/download/v0.1.0/kayakd-v0.1.0-linux-amd64.tar.gz
tar -xzf kayakd-linux-amd64.tar.gz

# Install
sudo mkdir -p /opt/kayaknet
sudo mv kayakd /opt/kayaknet/
sudo chmod +x /opt/kayaknet/kayakd

# Configure
sudo mkdir -p /etc/kayaknet
sudo cp config/templates/user.json /etc/kayaknet/config.json

# Start
/opt/kayaknet/kayakd --config /etc/kayaknet/config.json
```

---

## Deployment Types

### Bootstrap Node

Bootstrap nodes are the backbone of the network. They:
- Help new nodes discover peers
- Maintain the DHT network
- Don't store sensitive data
- Should be run on reliable servers with static IPs

```bash
sudo ./scripts/deploy-production.sh bootstrap
```

Configuration priorities:
- High rate limits (1000 msg/sec)
- No proxy needed
- Extensive logging
- High availability (Restart=always)

### Vendor Node

Vendor nodes run marketplace sellers. They:
- List products/services
- Handle escrow transactions
- Require cryptocurrency wallets
- Should be online 24/7 for sales

```bash
sudo ./scripts/deploy-production.sh vendor

# Setup crypto wallets
sudo ./scripts/wallet-setup/setup-monero.sh
sudo ./scripts/wallet-setup/setup-zcash.sh
```

### User Node

Regular users running the network. They:
- Browse the marketplace
- Use chat
- Access .kyk domains
- Can run casually or as a service

```bash
# As a service
sudo ./scripts/deploy-production.sh user

# Or directly
./kayakd --bootstrap 203.161.33.237:4242 --proxy
```

---

## Configuration

### Configuration File Locations

| Location | Purpose |
|----------|---------|
| `/etc/kayaknet/config.json` | System-wide config |
| `~/.kayaknet/config.json` | User config |
| `./config.json` | Local config |

### Key Configuration Options

```json
{
  "node": {
    "name": "my-node",           // Node identifier
    "mode": "vendor",            // user, vendor, or bootstrap
    "data_dir": "/var/lib/kayaknet"
  },
  "dht": {
    "bootstrap_nodes": [         // Initial peers
      "203.161.33.237:4242"
    ]
  },
  "security": {
    "onion_hops": 3,            // Anonymity level (3 recommended)
    "rate_limit_messages": 100,  // Messages per second
    "ban_threshold": 10          // Violations before ban
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
      "rpc_port": 8232,
      "rpc_user": "kayaknet",
      "rpc_password": "your-secure-password"
    }
  }
}
```

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `KAYAKNET_CONFIG` | Config file path | `/etc/kayaknet/config.json` |
| `KAYAKNET_DATA` | Data directory | `/var/lib/kayaknet` |
| `KAYAKNET_LOG_LEVEL` | Log level | `info` |

---

## Security Hardening

### Network Security

1. **Firewall Configuration**
```bash
# UFW
sudo ufw allow 4242/udp comment 'KayakNet P2P'
sudo ufw deny 8118  # Block external proxy access
sudo ufw deny 8119

# iptables
iptables -A INPUT -p udp --dport 4242 -j ACCEPT
iptables -A INPUT -p tcp --dport 8118 -s 127.0.0.1 -j ACCEPT
iptables -A INPUT -p tcp --dport 8118 -j DROP
```

2. **Proxy Binding**
Always bind proxies to localhost:
```json
{
  "proxy": {
    "bind_address": "127.0.0.1",
    "allow_external": false
  }
}
```

### System Security

1. **Run as non-root**
```bash
sudo useradd -r -s /bin/false kayaknet
sudo chown -R kayaknet:kayaknet /var/lib/kayaknet
```

2. **systemd hardening** (already in service files)
- `NoNewPrivileges=true`
- `PrivateTmp=true`
- `ProtectSystem=strict`
- `ProtectHome=true`

3. **File permissions**
```bash
chmod 700 /var/lib/kayaknet
chmod 600 /etc/kayaknet/config.json
chmod 600 /var/lib/kayaknet/identity.json
```

### Operational Security

1. **Use Tor for bootstrap connections** (optional)
```json
{
  "transport": {
    "tor": {
      "enabled": true,
      "proxy_addr": "127.0.0.1:9050"
    }
  }
}
```

2. **Rotate node identity periodically** (for vendors)
```bash
# Backup old identity
mv /var/lib/kayaknet/identity.json /var/lib/kayaknet/identity.json.bak

# Restart to generate new identity
systemctl restart kayaknet
```

3. **Monitor for anomalies**
```bash
journalctl -u kayaknet -f | grep -E "(ban|attack|flood)"
```

---

## Cryptocurrency Setup

### Monero Wallet

1. **Install Monero**
```bash
sudo ./scripts/wallet-setup/setup-monero.sh mainnet
```

2. **Verify wallet**
```bash
# Check balance
curl -X POST http://127.0.0.1:18082/json_rpc \
  -d '{"jsonrpc":"2.0","id":"0","method":"get_balance"}' \
  -H 'Content-Type: application/json'
```

3. **Generate address for escrow**
```bash
curl -X POST http://127.0.0.1:18082/json_rpc \
  -d '{"jsonrpc":"2.0","id":"0","method":"create_address","params":{"account_index":0}}' \
  -H 'Content-Type: application/json'
```

### Zcash Wallet

1. **Install Zcash**
```bash
sudo ./scripts/wallet-setup/setup-zcash.sh mainnet
```

2. **Wait for sync**
```bash
zcash-cli getblockchaininfo | jq '.blocks, .headers'
```

3. **Generate shielded address**
```bash
zcash-cli z_getnewaddress
```

### Security Notes

- **Never expose wallet RPC to the internet**
- Store wallet passwords in `/var/lib/kayaknet/wallets/.password`
- Back up wallet files and seed phrases offline
- Use separate wallets for escrow and personal funds

---

## Monitoring

### Health Checks

```bash
# Check service status
systemctl status kayaknet

# Check peer count
curl -s http://127.0.0.1:8080/api/stats | jq '.peers'

# Check network connectivity
curl -s http://127.0.0.1:8080/api/network
```

### Logging

```bash
# Follow logs
journalctl -u kayaknet -f

# Check for errors
journalctl -u kayaknet -p err --since "1 hour ago"

# Log rotation (in /etc/logrotate.d/kayaknet)
/var/log/kayaknet/*.log {
    daily
    rotate 7
    compress
    missingok
    notifempty
}
```

### Metrics (Prometheus)

Add to config:
```json
{
  "metrics": {
    "enabled": true,
    "port": 9090
  }
}
```

### Alerting

Set up alerts for:
- Service down (`systemctl is-active kayaknet`)
- Low peer count (< 3 peers)
- High ban rate (> 10/hour)
- Wallet balance low (for vendors)

---

## Troubleshooting

### Node Won't Start

```bash
# Check logs
journalctl -u kayaknet -n 100

# Verify config
cat /etc/kayaknet/config.json | jq .

# Check permissions
ls -la /var/lib/kayaknet/

# Check port availability
ss -ulnp | grep 4242
```

### No Peers Connecting

```bash
# Check firewall
sudo ufw status
iptables -L -n

# Test UDP connectivity
nc -u -v 203.161.33.237 4242

# Check bootstrap node
curl -s http://203.161.33.237:8080/health
```

### Proxy Not Working

```bash
# Check proxy is listening
ss -tlnp | grep -E "8118|8119"

# Test proxy
curl --proxy http://127.0.0.1:8118 http://home.kyk

# Check config
jq '.proxy' /etc/kayaknet/config.json
```

### Wallet Issues

```bash
# Monero - check RPC
curl http://127.0.0.1:18082/json_rpc -d '{"jsonrpc":"2.0","method":"get_version"}'

# Zcash - check sync
zcash-cli getblockchaininfo

# Check wallet service
systemctl status monero-wallet-rpc
systemctl status zcashd
```

### Reset Node

```bash
# Stop service
sudo systemctl stop kayaknet

# Backup identity (optional)
sudo cp /var/lib/kayaknet/identity.json ~/identity.backup.json

# Clear data
sudo rm -rf /var/lib/kayaknet/*

# Restart
sudo systemctl start kayaknet
```

---

## Support

- GitHub Issues: https://github.com/kayaknet/kayaknet/issues
- Documentation: https://docs.kayaknet.io
- Chat: Join #support on KayakNet chat

---

## Version History

| Version | Date | Changes |
|---------|------|---------|
| 0.1.0 | 2026-01-12 | Initial production release |

