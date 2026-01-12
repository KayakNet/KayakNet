# KayakNet

A privacy-first peer-to-peer network with built-in anonymity. No central servers. No web services. Just nodes talking directly to each other through encrypted, anonymous channels.

```
    ┌─────────┐         ┌─────────┐
    │  Node   │◄───────►│  Node   │
    │  Alpha  │  relay  │  Beta   │
    └────┬────┘         └────┬────┘
         │                   │
         │    ┌─────────┐    │
         └───►│  Node   │◄───┘
              │  Gamma  │
              └─────────┘
```

## Features

- **Pure P2P**: Every node is equal. No servers, no central authority.
- **Onion Routing**: Multi-hop encrypted routing for anonymity (like Tor)
- **Traffic Analysis Resistance**: Padding, mixing, dummy traffic
- **.kyk Domains**: KayakNet naming system - register `yourname.kyk`
- **Browser Access**: Browse .kyk sites with any browser (no VPN needed)
- **Anonymous Chat**: End-to-end encrypted group messaging
- **P2P Marketplace**: Buy/sell within the network only
- **Ed25519 Identity**: Cryptographic node identity
- **Kademlia DHT**: Distributed discovery of peers and services
- **UDP Transport**: Lightweight, NAT-friendly communication

## Quick Start

### Build

```bash
# Build the node daemon
go build -o kayakd ./cmd/kayakd

# Build the CLI tool
go build -o kayakctl ./cmd/kayakctl
```

### Start Your First Node (Bootstrap)

```bash
# Start a bootstrap node with interactive mode
./kayakd -i --listen 0.0.0.0:4242 --name my-first-node
```

### Join the Network

```bash
# On another machine (or terminal), connect to your bootstrap node
./kayakd -i --listen 0.0.0.0:4243 --bootstrap <BOOTSTRAP_IP>:4242 --name node-2
```

## Interactive Commands

Once running with `-i` flag:

### Chat
```
chat <room> <message>   # Send anonymous message
rooms                   # List chat rooms
join <room>            # Join a room
history <room>         # Show room history
```

### Marketplace (Network-Only Access)
```
market                 # Marketplace overview
browse [category]      # Browse listings
search <query>         # Search listings
sell <title> <price> <desc>  # Create listing
buy <id>               # Request to purchase
mylistings             # Your listings
```

### Domains (.kyk)
```
register <name>        # Register yourname.kyk
resolve <name.kyk>     # Resolve a domain
domains                # List your domains
whois <name.kyk>       # Domain details
update-domain <name> <addr>  # Update address
search-domains <query> # Search domains
```

### Network
```
peers                  # List connected peers
connect <addr>         # Connect to peer
status                 # Anonymity status
info                   # Node information
help                   # All commands
```

### Browser Proxy
```
proxy                  # Proxy status
proxy-start            # Start browser proxy
proxy-stop             # Stop browser proxy
proxy-setup            # Browser configuration guide
```

## Browser Access (No VPN Required)

Access .kyk domains directly in your browser by configuring a local proxy.

### Start with Proxy Enabled

```bash
./kayakd -i --proxy --name my-node
```

### Or Enable at Runtime

```bash
> proxy-start
[OK] Browser proxy started
  HTTP Proxy:   127.0.0.1:8118
  SOCKS5 Proxy: 127.0.0.1:8119
```

### Configure Your Browser

**Firefox:**
1. Settings > Network Settings > Settings
2. Select "Manual proxy configuration"
3. HTTP Proxy: `127.0.0.1` Port: `8118`
4. Check "Also use this proxy for HTTPS"
5. Click OK

**Chrome (use Proxy SwitchyOmega extension):**
1. Install Proxy SwitchyOmega
2. Create new profile "KayakNet"
3. Set HTTP/HTTPS proxy to `127.0.0.1:8118`
4. Apply and select "KayakNet" profile

**System-wide (Linux):**
```bash
export http_proxy=http://127.0.0.1:8118
export https_proxy=http://127.0.0.1:8118
```

### Browse .kyk Sites

After setup, navigate to any .kyk domain:
- `http://example.kyk`
- `http://market.kyk`
- `http://mysite.kyk`

All traffic is routed through KayakNet's onion network.

## .kyk Domains

KayakNet has its own domain system - `.kyk` domains. These are only resolvable inside the network.

```bash
> register myservice
[OK] Registered: myservice.kyk
   Owner:   abc123def456...
   Expires: 2027-01-09

> resolve somesite.kyk
somesite.kyk
   Node:    789xyz...
   Desc:    My anonymous service
```

**Domain Rules:**
- 3-32 characters
- Alphanumeric and hyphens only
- Registrations last 1 year
- Only resolvable on KayakNet

## Privacy & Anonymity

### Onion Routing

KayakNet uses built-in onion routing when enough peers are available:

```
You -> Relay 1 -> Relay 2 -> Relay 3 -> Destination
         ^ Encrypted layers stripped at each hop
```

- **3-hop circuits** minimum
- Each relay only knows previous/next hop
- No single point can see both source and destination

### Traffic Analysis Resistance

KayakNet implements defenses against Global Passive Adversaries:

| Defense | Description |
|---------|-------------|
| Constant-rate padding | All messages are 4KB |
| Batch mixing | Messages held and shuffled every 100ms |
| Random delays | 0-500ms added per message |
| Dummy traffic | 0.1 msg/sec per peer |
| Loopback cover | Messages to self hide real traffic |

### Check Your Status

```bash
> status
Anonymity Status:
  [OK] ANONYMOUS - 3-hop onion routing active

Traffic Analysis Defenses:
  * Constant-rate padding: 4096 byte messages
  * Batch mixing: every 100ms
  * Random delays: up to 500ms
  * Dummy traffic: 0.1 msg/sec per peer
```

## Using Docker

### Run a Local Network

```bash
# Start 4 nodes (1 bootstrap + 3 regular nodes)
docker-compose up --build

# Attach to a node to interact
docker attach kayak-node1
```

### Run a Single Node

```bash
# Build
docker build -t kayaknet -f cmd/kayakd/Dockerfile .

# Run bootstrap node
docker run -it --rm -p 4242:4242/udp kayaknet kayakd -i --name bootstrap

# Run connecting node
docker run -it --rm -p 4243:4242/udp kayaknet kayakd -i \
  --bootstrap <HOST_IP>:4242 --name node-2
```

## Architecture

```
+---------------------------------------------------------------+
|                      KayakNet Node                            |
+---------------------------------------------------------------+
|  +----------+ +----------+ +----------+ +-------------------+ |
|  | Chat     | | Market   | | Names    | | Capabilities      | |
|  | (Rooms)  | | (P2P)    | | (.kyk)   | | (Access Ctrl)     | |
|  +----------+ +----------+ +----------+ +-------------------+ |
+---------------------------------------------------------------+
|  +---------------------------------------------------------+  |
|  |              Onion Router                               |  |
|  |    - Multi-hop circuits                                 |  |
|  |    - Layered encryption                                 |  |
|  |    - Relay management                                   |  |
|  +---------------------------------------------------------+  |
+---------------------------------------------------------------+
|  +---------------------------------------------------------+  |
|  |              Traffic Analysis Resistance                |  |
|  |    - Padding, mixing, delays                            |  |
|  |    - Dummy traffic, loopback cover                      |  |
|  +---------------------------------------------------------+  |
+---------------------------------------------------------------+
|  +---------------------------------------------------------+  |
|  |              DHT (Kademlia-like)                        |  |
|  |    - Peer Discovery                                     |  |
|  |    - Service Announcements                              |  |
|  |    - Record Storage                                     |  |
|  +---------------------------------------------------------+  |
+---------------------------------------------------------------+
|  +---------------------------------------------------------+  |
|  |              UDP Transport (Direct)                     |  |
|  +---------------------------------------------------------+  |
+---------------------------------------------------------------+
```

## Message Protocol

Messages are JSON-encoded UDP packets:

```json
{
  "type": 1,
  "from": "abc123...",
  "from_key": "<32 bytes>",
  "ts": 1704067200000000000,
  "nonce": 123456789,
  "payload": "...",
  "sig": "<64 bytes>"
}
```

### Message Types

| Type | Name        | Description                    |
|------|-------------|--------------------------------|
| 0x01 | PING        | Keepalive / discovery          |
| 0x02 | PONG        | Response to PING               |
| 0x03 | FIND_NODE   | Request closest nodes          |
| 0x04 | NODES       | Response with node list        |
| 0x08 | CHAT        | Group chat message             |
| 0x10 | LISTING     | Marketplace listing            |
| 0x20 | ONION       | Onion-routed packet            |
| 0x30 | NAME_REG    | .kyk domain registration       |
| 0x31 | NAME_LOOKUP | .kyk domain lookup             |
| 0x32 | NAME_REPLY  | .kyk resolution response       |

## Configuration

Create a `config.json`:

```json
{
  "node": {
    "name": "my-node",
    "data_dir": "./data"
  },
  "dht": {
    "k": 20,
    "alpha": 3,
    "bootstrap_nodes": [
      "192.168.1.100:4242",
      "192.168.1.101:4242"
    ]
  }
}
```

Run with config:

```bash
./kayakd --config config.json -i
```

## Building a Network

### 1. Start Bootstrap Nodes

Run 2-3 bootstrap nodes on reliable machines with public IPs:

```bash
# On server 1 (e.g., 203.0.113.10)
./kayakd --listen 0.0.0.0:4242 --name bootstrap-1

# On server 2 (e.g., 203.0.113.20)
./kayakd --listen 0.0.0.0:4242 --name bootstrap-2 --bootstrap 203.0.113.10:4242
```

### 2. Share Bootstrap Addresses

Share the bootstrap node addresses with network participants:
- `203.0.113.10:4242`
- `203.0.113.20:4242`

### 3. Nodes Join

Participants start their nodes:

```bash
./kayakd -i --bootstrap 203.0.113.10:4242,203.0.113.20:4242
```

### 4. Network Grows

As more nodes join:
- They discover each other through the DHT
- Onion routing becomes available (3+ relays needed)
- Traffic analysis defenses activate
- .kyk domains propagate across the network

## Security Model

### What We Protect Against

| Threat | Protection |
|--------|------------|
| Eavesdropping | End-to-end encryption |
| Traffic analysis | Padding, mixing, dummy traffic |
| Peer identification | Onion routing (3+ hops) |
| Replay attacks | Nonce tracking |
| DoS/Flooding | Rate limiting, peer scoring, auto-ban |

### What We Don't Protect Against

- Endpoint compromise (if your machine is hacked)
- Timing attacks with very few nodes
- Active attacks during bootstrap (before anonymity)

### Anonymity Requirements

- **Minimum 3 peers** for onion routing
- More peers = better anonymity
- Traffic analysis resistance always active

## Development

```bash
# Run tests
go test ./...

# Run with verbose logging
./kayakd -i --listen 0.0.0.0:4242 2>&1 | tee node.log
```

## Comparison with Tor

| Feature | KayakNet | Tor |
|---------|----------|-----|
| Architecture | Pure P2P | Directory authorities |
| Hop count | 3 | 3 |
| Traffic padding | Yes | Limited |
| Dummy traffic | Yes | No |
| Built-in services | Chat, Market, Domains | None (exit nodes) |
| Setup | Single binary | Multiple components |

## License

MIT
