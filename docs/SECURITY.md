# KayakNet Security Guide

This document outlines the security architecture, threat model, and best practices for KayakNet.

## Security Architecture

### Layered Security Model

```
+------------------------------------------+
|            Application Layer             |
|  - Input validation                      |
|  - Rate limiting                         |
|  - Peer scoring                          |
+------------------------------------------+
|           Anonymity Layer                |
|  - 3-hop onion routing                   |
|  - Traffic analysis resistance           |
|  - Circuit isolation                     |
+------------------------------------------+
|           Transport Layer                |
|  - End-to-end encryption (AES-256-GCM)   |
|  - Perfect forward secrecy               |
|  - Message authentication                |
+------------------------------------------+
|            Identity Layer                |
|  - Ed25519 signatures                    |
|  - Signed messages                       |
|  - Key rotation support                  |
+------------------------------------------+
```

## Cryptographic Primitives

| Component | Algorithm | Key Size | Notes |
|-----------|-----------|----------|-------|
| Node Identity | Ed25519 | 256-bit | Signing and verification |
| Key Exchange | X25519 | 256-bit | ECDH for session keys |
| Encryption | AES-256-GCM | 256-bit | Authenticated encryption |
| Hashing | SHA-256 | 256-bit | Content addressing |
| Onion Layers | AES-256-GCM | 256-bit | Per-hop encryption |

## Threat Model

### What KayakNet Protects Against

1. **Passive Network Observers**
   - ISPs cannot see message contents
   - Traffic analysis is mitigated by padding and mixing
   - Timing correlation is reduced by random delays

2. **Active Network Attackers**
   - Message tampering detected via signatures
   - Replay attacks prevented by nonces
   - MITM attacks prevented by identity verification

3. **Malicious Peers**
   - Automatic rate limiting
   - Peer scoring and banning
   - No single point of trust

4. **Data Correlation**
   - 3-hop onion routing hides source/destination
   - Different circuits for different activities
   - No central logging

### Limitations

1. **Global Passive Adversary (GPA)**
   - Traffic analysis resistance helps but doesn't guarantee anonymity
   - Entry/exit correlation possible with enough resources
   - Use additional layers (Tor) for high-risk scenarios

2. **Endpoint Security**
   - KayakNet cannot protect compromised devices
   - Local malware can read decrypted data
   - Physical access = full compromise

3. **Metadata**
   - Timing of online presence may leak information
   - Transaction amounts visible to escrow participants
   - Seller activity patterns observable

## Automatic Security Features

### Rate Limiting

```
Messages per peer per second: 100 (configurable)
Window: 1 second (sliding)
Penalty: Temporary ban
```

### Peer Scoring

| Event | Score Change |
|-------|--------------|
| Valid message | +1 |
| Invalid signature | -50 |
| Rate limit violation | -20 |
| Invalid nonce | -30 |
| Malformed message | -10 |

### Automatic Banning

| Violation Count | Ban Duration |
|----------------|--------------|
| 1 | 5 minutes |
| 2 | 1 hour |
| 3 | 6 hours |
| 4 | 24 hours |
| 5+ | 7 days |

## Production Security Checklist

### Before Deployment

- [ ] Generate strong passwords for all credentials
- [ ] Configure firewall (allow 4242/udp only)
- [ ] Bind proxy to localhost only
- [ ] Set up log rotation
- [ ] Create non-root service user
- [ ] Verify file permissions (700 for data, 600 for keys)

### System Hardening

- [ ] Enable automatic security updates
- [ ] Disable root SSH login
- [ ] Use SSH keys instead of passwords
- [ ] Enable fail2ban
- [ ] Configure AppArmor/SELinux

### Network Security

- [ ] Use static IP for bootstrap nodes
- [ ] Configure DDoS protection (if available)
- [ ] Set up VPN for admin access
- [ ] Monitor unusual traffic patterns

### Cryptocurrency Security

- [ ] Store wallet seeds offline
- [ ] Use hardware wallet for large balances
- [ ] Enable wallet encryption
- [ ] Regular balance monitoring
- [ ] Separate hot/cold wallets

### Operational Security

- [ ] Don't link real identity to node
- [ ] Use dedicated hardware if possible
- [ ] Regularly rotate node identity (for vendors)
- [ ] Monitor for security announcements
- [ ] Have incident response plan

## Security Best Practices

### For Users

1. **Keep software updated**
   ```bash
   sudo systemctl stop kayaknet
   # Download new version
   sudo systemctl start kayaknet
   ```

2. **Use strong system security**
   - Full disk encryption
   - Strong login password
   - Screen lock when away

3. **Verify downloads**
   ```bash
   sha256sum kayakd-linux-amd64.tar.gz
   # Compare with published checksum
   ```

### For Vendors

1. **Separate identities**
   - Don't mix personal and vendor activities
   - Use dedicated hardware if possible

2. **Secure cryptocurrency**
   - Don't keep large balances online
   - Regular withdrawals to cold storage
   - Multiple wallet addresses

3. **Monitor for attacks**
   ```bash
   journalctl -u kayaknet | grep -E "(ban|attack|flood)"
   ```

### For Bootstrap Operators

1. **High availability**
   - Multiple geographic locations
   - Automatic failover
   - Regular health monitoring

2. **Capacity planning**
   - Monitor peer count
   - Scale resources as network grows
   - Rate limit appropriately

## Incident Response

### If Compromised

1. **Immediately**
   - Stop the service: `systemctl stop kayaknet`
   - Disconnect from network
   - Preserve logs: `cp -r /var/log/kayaknet /tmp/incident-logs`

2. **Assessment**
   - Check for unauthorized access
   - Review system logs
   - Identify attack vector

3. **Recovery**
   - Generate new identity
   - Rotate all credentials
   - Reinstall from clean source
   - Review and improve security

### If Wallet Compromised

1. **Immediately**
   - Stop all services
   - Transfer remaining funds to new wallet
   - Revoke any pending escrows

2. **Investigation**
   - Check transaction history
   - Identify unauthorized transactions
   - Preserve evidence

## Responsible Disclosure

Found a security issue? Please report responsibly:

1. **Do NOT** disclose publicly before fix is available
2. Email security concerns to the development team
3. Provide detailed reproduction steps
4. Allow reasonable time for fix (90 days)

## Version History

| Version | Date | Security Changes |
|---------|------|------------------|
| 0.1.0 | 2026-01-12 | Initial security implementation |

---

*This document is updated with each release. Always use the latest version.*

