// Package main implements the KayakNet node daemon
// Anonymous P2P network with built-in onion routing
package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kayaknet/kayaknet/internal/cap"
	"github.com/kayaknet/kayaknet/internal/chat"
	"github.com/kayaknet/kayaknet/internal/config"
	"github.com/kayaknet/kayaknet/internal/dht"
	"github.com/kayaknet/kayaknet/internal/identity"
	"github.com/kayaknet/kayaknet/internal/market"
	"github.com/kayaknet/kayaknet/internal/mix"
	"github.com/kayaknet/kayaknet/internal/names"
	"github.com/kayaknet/kayaknet/internal/onion"
	"github.com/kayaknet/kayaknet/internal/peer"
	"github.com/kayaknet/kayaknet/internal/proxy"
	"github.com/kayaknet/kayaknet/internal/pubsub"
	"github.com/kayaknet/kayaknet/internal/security"
)

var (
	configPath  = flag.String("config", "", "Path to configuration file")
	dataDir     = flag.String("data-dir", "./data", "Data directory")
	listenAddr  = flag.String("listen", "0.0.0.0:4242", "Listen address")
	bootstrap   = flag.String("bootstrap", "", "Bootstrap node addresses (comma-separated)")
	interactive = flag.Bool("i", false, "Interactive mode (CLI)")
	nodeName    = flag.String("name", "", "Node name")
	proxyEnable = flag.Bool("proxy", false, "Enable browser proxy (HTTP on 8118, SOCKS5 on 8119)")
	proxyHTTP   = flag.Int("proxy-http", 8118, "HTTP proxy port")
	proxySOCKS  = flag.Int("proxy-socks", 8119, "SOCKS5 proxy port")
)

// Node represents a KayakNet P2P node
type Node struct {
	mu          sync.RWMutex
	config      *config.Config
	identity    *identity.Identity
	peerStore   *peer.Store
	dht         *dht.DHT
	pubsub      *pubsub.PubSub
	capStore    *cap.Store
	onionRouter *onion.Router
	mixer       *mix.Mixer
	marketplace *market.Marketplace
	chatMgr     *chat.ChatManager
	nameService *names.NameService
	browserProxy *proxy.Proxy
	listener    net.PacketConn
	connections map[string]*PeerConn
	ctx         context.Context
	cancel      context.CancelFunc
	name        string

	// Security
	rateLimiter  *security.RateLimiter
	banList      *security.BanList
	peerScorer   *security.PeerScorer
	nonceTracker *security.NonceTracker
	validator    *security.MessageValidator
}

// PeerConn represents a connection to a peer
type PeerConn struct {
	NodeID    string
	PublicKey []byte
	Address   string
	LastSeen  time.Time
}

// Message types
const (
	MsgTypePing       = 0x01
	MsgTypePong       = 0x02
	MsgTypeFindNode   = 0x03
	MsgTypeNodes      = 0x04
	MsgTypeChat       = 0x08
	MsgTypeListing    = 0x10 // Marketplace listing
	MsgTypeOnion      = 0x20 // Onion-routed message
	MsgTypeNameReg    = 0x30 // .kyk domain registration
	MsgTypeNameLookup = 0x31 // .kyk domain lookup
	MsgTypeNameReply  = 0x32 // .kyk domain resolution response
)

// P2PMessage is the wire format
type P2PMessage struct {
	Type      byte            `json:"type"`
	From      string          `json:"from"`
	FromKey   []byte          `json:"from_key"`
	Timestamp int64           `json:"ts"`
	Nonce     uint64          `json:"nonce"`
	Payload   json.RawMessage `json:"payload"`
	Signature []byte          `json:"sig"`
}

func main() {
	flag.Parse()

	var cfg *config.Config
	if *configPath != "" {
		var err error
		cfg, err = config.Load(*configPath)
		if err != nil {
			log.Fatalf("Failed to load config: %v", err)
		}
	} else {
		cfg = config.Default()
		cfg.Node.DataDir = *dataDir
		cfg.Node.IdentityFile = filepath.Join(*dataDir, "identity.json")
	}

	var bootstrapNodes []string
	if *bootstrap != "" {
		bootstrapNodes = strings.Split(*bootstrap, ",")
		for i := range bootstrapNodes {
			bootstrapNodes[i] = strings.TrimSpace(bootstrapNodes[i])
		}
	}
	cfg.DHT.BootstrapNodes = bootstrapNodes

	node, err := NewNode(cfg, *nodeName)
	if err != nil {
		log.Fatalf("Failed to create node: %v", err)
	}

	if err := node.Start(*listenAddr); err != nil {
		log.Fatalf("Failed to start node: %v", err)
	}

	// Print node info
	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║                  KayakNet Anonymous Network                 ║")
	fmt.Println("╠════════════════════════════════════════════════════════════╣")
	fmt.Printf("║  Node ID:  %s...  ║\n", node.identity.NodeID()[:32])
	fmt.Printf("║  Address:  %-47s ║\n", *listenAddr)
	fmt.Printf("║  Name:     %-47s ║\n", node.name)
	fmt.Println("╠════════════════════════════════════════════════════════════╣")
	fmt.Println("║  [+] Onion routing + traffic analysis resistance           ║")
	fmt.Println("║  [+] Padding, mixing, dummy traffic enabled                ║")
	fmt.Println("║  [+] .kyk domains - KayakNet naming system                 ║")

	// Start browser proxy if enabled
	if *proxyEnable {
		if err := node.browserProxy.Start(*proxyHTTP, *proxySOCKS); err != nil {
			log.Printf("[WARN] Failed to start browser proxy: %v", err)
		} else {
			fmt.Printf("║  [+] Browser proxy: HTTP %d, SOCKS5 %d                 ║\n", *proxyHTTP, *proxySOCKS)
		}
	}
	fmt.Println("╚════════════════════════════════════════════════════════════╝")

	if *interactive {
		go node.interactiveMode()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\nShutting down...")
	node.Stop()
}

// NewNode creates a new node
func NewNode(cfg *config.Config, name string) (*Node, error) {
	ctx, cancel := context.WithCancel(context.Background())

	if err := os.MkdirAll(cfg.Node.DataDir, 0700); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create data dir: %w", err)
	}

	id, err := identity.LoadOrCreate(cfg.Node.IdentityFile)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to load identity: %w", err)
	}

	peerStore, err := peer.NewStore(peer.StoreConfig{
		Path:     filepath.Join(cfg.Node.DataDir, "peers.json"),
		MaxPeers: 1000,
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create peer store: %w", err)
	}

	capStore := cap.NewStore()

	dhtCfg := dht.Config{
		LocalID:         id.PublicKey()[:20],
		LocalPubKey:     id.PublicKey(),
		Signer:          id.Sign,
		K:               cfg.DHT.K,
		Alpha:           cfg.DHT.Alpha,
		RecordTTL:       cfg.DHT.RecordTTL,
		RefreshInterval: cfg.DHT.RefreshInterval,
		BootstrapNodes:  cfg.DHT.BootstrapNodes,
	}
	dhtNode, err := dht.NewDHT(dhtCfg, peerStore)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create DHT: %w", err)
	}

	psCfg := pubsub.Config{
		LocalID:            id.NodeID(),
		LocalPubKey:        id.PublicKey(),
		Signer:             id.Sign,
		MaxTopics:          100,
		MaxPeersPerTopic:   50,
		MessageBufferSize:  1000,
		ScoreDecayInterval: time.Minute,
		RateLimitWindow:    time.Second,
		RateLimitMessages:  10,
	}
	ps := pubsub.NewPubSub(psCfg)

	// Create onion router
	onionRouter := onion.NewRouter(id.NodeID(), id.PublicKey(), ed25519.PrivateKey(id.PrivateKey()))

	// Security
	banList := security.NewBanList(10000)
	rateLimiter := security.NewRateLimiter(10, 50)
	peerScorer := security.NewPeerScorer(banList)
	nonceTracker := security.NewNonceTracker(5*time.Minute, 100000)
	validator := security.NewMessageValidator(security.MaxMessageSize)

	if name == "" {
		name = fmt.Sprintf("anon-%s", id.NodeID()[:8])
	}

	n := &Node{
		config:       cfg,
		identity:     id,
		peerStore:    peerStore,
		dht:          dhtNode,
		pubsub:       ps,
		capStore:     capStore,
		onionRouter:  onionRouter,
		connections:  make(map[string]*PeerConn),
		ctx:          ctx,
		cancel:       cancel,
		name:         name,
		rateLimiter:  rateLimiter,
		banList:      banList,
		peerScorer:   peerScorer,
		nonceTracker: nonceTracker,
		validator:    validator,
	}

	// Create mixer for traffic analysis resistance
	n.mixer = mix.NewMixer(func(dest string, data []byte) error {
		n.mu.RLock()
		peer, ok := n.connections[dest]
		n.mu.RUnlock()
		if !ok {
			return nil
		}
		addr, err := net.ResolveUDPAddr("udp", peer.Address)
		if err != nil {
			return err
		}
		_, err = n.listener.WriteTo(data, addr)
		return err
	})

	// Create marketplace (only accessible through network)
	n.marketplace = market.NewMarketplace(
		id.NodeID(),
		id.PublicKey(),
		ed25519.PrivateKey(id.PrivateKey()),
		id.Sign,
	)

	// Create chat manager (only accessible through network)
	n.chatMgr = chat.NewChatManager(
		id.NodeID(),
		id.PublicKey(),
		name,
		id.Sign,
	)

	// Create name service for .kyk domains
	n.nameService = names.NewNameService(
		id.NodeID(),
		id.PublicKey(),
		id.Sign,
	)

	// Create browser proxy (resolver wraps nameService)
	resolver := &nodeNameResolver{node: n}
	sender := &nodeMessageSender{node: n}
	n.browserProxy = proxy.NewProxy(resolver, sender)

	// Set up chat message handler
	n.chatMgr.OnMessage(func(msg *chat.Message) {
		fmt.Printf("\n[CHAT] [%s] %s: %s\n> ", msg.Room, msg.Nick, msg.Content)
	})

	return n, nil
}

// nodeNameResolver wraps the name service for proxy use
type nodeNameResolver struct {
	node *Node
}

func (r *nodeNameResolver) Resolve(domain string) (string, string, error) {
	reg, err := r.node.nameService.Resolve(domain)
	if err != nil {
		return "", "", err
	}
	return reg.NodeID, reg.Address, nil
}

// nodeMessageSender wraps the node for proxy use
type nodeMessageSender struct {
	node *Node
}

func (s *nodeMessageSender) SendToNode(nodeID string, data []byte) error {
	return s.node.sendAnonymous(nodeID, MsgTypeChat, data)
}

func (s *nodeMessageSender) SendHTTPRequest(nodeID string, req *http.Request) (*http.Response, error) {
	// For now, return a placeholder - full implementation would tunnel HTTP through onion routing
	return nil, fmt.Errorf("HTTP tunneling not yet implemented - use SOCKS5 proxy")
}

// Start starts the node
func (n *Node) Start(addr string) error {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return fmt.Errorf("invalid address: %w", err)
	}

	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	n.listener = conn

	// Start mixer for traffic analysis resistance
	n.mixer.Start()

	go n.handleMessages()
	go n.maintenance()

	if len(n.config.DHT.BootstrapNodes) > 0 {
		go n.bootstrap()
	}

	return nil
}

// Stop stops the node
func (n *Node) Stop() {
	n.cancel()
	n.mixer.Stop()
	if n.listener != nil {
		n.listener.Close()
	}
	n.peerStore.Save()
	n.pubsub.Close()
}

// handleMessages processes incoming messages
func (n *Node) handleMessages() {
	buf := make([]byte, security.MaxMessageSize)
	for {
		select {
		case <-n.ctx.Done():
			return
		default:
		}

		n.listener.SetReadDeadline(time.Now().Add(time.Second))
		nBytes, addr, err := n.listener.ReadFrom(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			continue
		}

		if err := security.ValidateMessageSize(buf[:nBytes]); err != nil {
			continue
		}

		var msg P2PMessage
		if err := json.Unmarshal(buf[:nBytes], &msg); err != nil {
			continue
		}

		go n.handleMessage(addr, &msg)
	}
}

// handleMessage processes a single message
func (n *Node) handleMessage(from net.Addr, msg *P2PMessage) {
	// Security checks
	if n.banList.IsBanned(msg.From) {
		return
	}

	if !n.rateLimiter.Allow(msg.From) {
		n.peerScorer.RecordRateLimitHit(msg.From, from.String())
		return
	}

	if err := n.validator.Validate(msg.Type, msg.From, msg.FromKey, msg.Payload, msg.Signature); err != nil {
		n.peerScorer.RecordBad(msg.From, from.String(), "invalid")
		return
	}

	if !n.verifySignature(msg) {
		n.peerScorer.RecordInvalidSignature(msg.From, from.String())
		return
	}

	msgTime := time.Unix(0, msg.Timestamp)
	if err := n.nonceTracker.Check(msg.Nonce, msgTime); err != nil {
		return
	}

	n.peerScorer.RecordGood(msg.From)

	// Update peer info and onion router
	n.mu.Lock()
	n.connections[msg.From] = &PeerConn{
		NodeID:    msg.From,
		PublicKey: msg.FromKey,
		Address:   from.String(),
		LastSeen:  time.Now(),
	}
	n.mu.Unlock()

	// Add as relay for onion routing and mixer
	if len(msg.FromKey) == 32 {
		n.dht.AddNode(msg.FromKey[:20], msg.FromKey)
		n.onionRouter.AddRelay(msg.From, msg.FromKey, from.String())
		n.mixer.AddPeer(msg.From)
	}

	// Handle message type
	switch msg.Type {
	case MsgTypePing:
		n.handlePing(from, msg)
	case MsgTypePong:
		n.handlePong(msg)
	case MsgTypeFindNode:
		n.handleFindNode(from, msg)
	case MsgTypeNodes:
		n.handleNodes(msg)
	case MsgTypeChat:
		n.handleChat(msg)
	case MsgTypeListing:
		n.handleListing(msg)
	case MsgTypeOnion:
		n.handleOnion(from, msg)
	case MsgTypeNameReg:
		n.handleNameReg(msg)
	case MsgTypeNameLookup:
		n.handleNameLookup(from, msg)
	case MsgTypeNameReply:
		n.handleNameReply(msg)
	}
}

func (n *Node) verifySignature(msg *P2PMessage) bool {
	if len(msg.FromKey) != ed25519.PublicKeySize || len(msg.Signature) != ed25519.SignatureSize {
		return false
	}
	toSign, _ := json.Marshal(struct {
		Type      byte   `json:"type"`
		From      string `json:"from"`
		Timestamp int64  `json:"ts"`
		Payload   []byte `json:"payload"`
	}{msg.Type, msg.From, msg.Timestamp, msg.Payload})
	return ed25519.Verify(msg.FromKey, toSign, msg.Signature)
}

// handlePing responds to ping
func (n *Node) handlePing(from net.Addr, msg *P2PMessage) {
	n.sendDirect(from, MsgTypePong, msg.Payload)
}

// handlePong processes pong
func (n *Node) handlePong(msg *P2PMessage) {
	n.mu.Lock()
	if conn, ok := n.connections[msg.From]; ok {
		conn.LastSeen = time.Now()
	}
	n.mu.Unlock()
}

// handleFindNode responds with closest nodes
func (n *Node) handleFindNode(from net.Addr, msg *P2PMessage) {
	var targetID []byte
	json.Unmarshal(msg.Payload, &targetID)

	closest := n.dht.FindClosestNodes(targetID, 20)

	type nodeInfo struct {
		ID   string `json:"id"`
		Addr string `json:"addr"`
		Key  []byte `json:"key"`
	}

	var nodes []nodeInfo
	n.mu.RLock()
	for _, node := range closest {
		nodeID := hex.EncodeToString(node.ID)
		if conn, ok := n.connections[nodeID]; ok {
			nodes = append(nodes, nodeInfo{
				ID:   nodeID,
				Addr: conn.Address,
				Key:  node.PubKey,
			})
		}
		if len(nodes) >= 20 {
			break
		}
	}
	n.mu.RUnlock()

	payload, _ := json.Marshal(nodes)
	n.sendDirect(from, MsgTypeNodes, payload)
}

// handleNodes processes received nodes
func (n *Node) handleNodes(msg *P2PMessage) {
	type nodeInfo struct {
		ID   string `json:"id"`
		Addr string `json:"addr"`
		Key  []byte `json:"key"`
	}

	var nodes []nodeInfo
	if err := json.Unmarshal(msg.Payload, &nodes); err != nil {
		return
	}

	for _, node := range nodes {
		if node.ID == n.identity.NodeID() {
			continue
		}
		if err := security.ValidateAddress(node.Addr); err != nil {
			continue
		}

		// Add as potential relay
		if len(node.Key) == 32 {
			n.onionRouter.AddRelay(node.ID, node.Key, node.Addr)
		}

		addr, err := net.ResolveUDPAddr("udp", node.Addr)
		if err != nil {
			continue
		}
		n.sendDirect(addr, MsgTypePing, []byte(`"ping"`))
	}
}

// handleChat processes chat (may be onion-routed)
func (n *Node) handleChat(msg *P2PMessage) {
	// Parse the chat message
	chatMsg, err := chat.UnmarshalMessage(msg.Payload)
	if err != nil {
		// Fallback to old format
		var oldFormat struct {
			Room    string `json:"room"`
			Message string `json:"message"`
			Nick    string `json:"nick"`
		}
		if err := json.Unmarshal(msg.Payload, &oldFormat); err != nil {
			return
		}
		chatMsg = &chat.Message{
			Room:      oldFormat.Room,
			Content:   oldFormat.Message,
			Nick:      oldFormat.Nick,
			SenderID:  msg.From,
			SenderKey: msg.FromKey,
			Timestamp: time.Now(),
		}
	}

	// Store in chat manager (handles display via callback)
	n.chatMgr.ReceiveMessage(chatMsg)
}

// handleNameReg processes .kyk domain registrations from the network
func (n *Node) handleNameReg(msg *P2PMessage) {
	reg, err := names.UnmarshalRegistration(msg.Payload)
	if err != nil {
		return
	}

	if err := n.nameService.AddRegistration(reg); err != nil {
		return
	}

	// Optionally forward to other peers (gossip)
}

// handleNameLookup processes .kyk domain lookups from the network
func (n *Node) handleNameLookup(from net.Addr, msg *P2PMessage) {
	var domain string
	if err := json.Unmarshal(msg.Payload, &domain); err != nil {
		return
	}

	reg, err := n.nameService.Resolve(domain)
	if err != nil {
		return
	}

	// Send back the registration
	payload, _ := reg.Marshal()
	n.sendDirect(from, MsgTypeNameReply, payload)
}

// handleNameReply processes .kyk domain resolution responses
func (n *Node) handleNameReply(msg *P2PMessage) {
	reg, err := names.UnmarshalRegistration(msg.Payload)
	if err != nil {
		return
	}

	// Store the received registration
	if err := n.nameService.AddRegistration(reg); err != nil {
		return
	}

	// Display result
	fmt.Printf("\n[NET] Found: %s\n", reg.FullName)
	fmt.Printf("   Owner: %s...\n", reg.NodeID[:16])
	if reg.Description != "" {
		fmt.Printf("   %s\n", reg.Description)
	}
	fmt.Print("> ")
}

// handleListing processes marketplace listings from the network
func (n *Node) handleListing(msg *P2PMessage) {
	listing, err := market.UnmarshalListing(msg.Payload)
	if err != nil {
		return
	}

	if err := n.marketplace.AddListing(listing); err != nil {
		return
	}

	log.Printf("[PKG] New listing: %s", listing.Title)
}

// handleOnion processes onion-routed message (relay or final destination)
func (n *Node) handleOnion(from net.Addr, msg *P2PMessage) {
	var packet onion.OnionPacket
	if err := json.Unmarshal(msg.Payload, &packet); err != nil {
		return
	}

	// Try to unwrap this layer
	layer, err := n.onionRouter.UnwrapLayer(&packet, msg.FromKey)
	if err != nil {
		return
	}

	if layer.IsFinal {
		// This message is for us - process inner payload
		var innerMsg P2PMessage
		if err := json.Unmarshal(layer.Payload, &innerMsg); err != nil {
			return
		}
		// Handle the inner message (e.g., chat)
		n.handleMessage(from, &innerMsg)
	} else {
		// Forward to next hop
		nextPacket := onion.OnionPacket{
			CircuitID: packet.CircuitID,
			HopIndex:  packet.HopIndex + 1,
			Payload:   layer.Payload,
			IsRelay:   true,
		}

		nextPayload, _ := json.Marshal(nextPacket)

		addr, err := net.ResolveUDPAddr("udp", layer.NextAddr)
		if err != nil {
			return
		}
		n.sendDirect(addr, MsgTypeOnion, nextPayload)
	}
}

// sendDirect sends a message directly (for bootstrapping and routing)
func (n *Node) sendDirect(to net.Addr, msgType byte, payload []byte) error {
	msg := P2PMessage{
		Type:      msgType,
		From:      n.identity.NodeID(),
		FromKey:   n.identity.PublicKey(),
		Timestamp: time.Now().UnixNano(),
		Nonce:     security.GenerateNonce(),
		Payload:   payload,
	}

	toSign, _ := json.Marshal(struct {
		Type      byte   `json:"type"`
		From      string `json:"from"`
		Timestamp int64  `json:"ts"`
		Payload   []byte `json:"payload"`
	}{msg.Type, msg.From, msg.Timestamp, msg.Payload})
	msg.Signature = n.identity.Sign(toSign)

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	_, err = n.listener.WriteTo(data, to)
	return err
}

// sendAnonymous sends a message through onion routing (if available)
func (n *Node) sendAnonymous(destID string, msgType byte, payload []byte) error {
	n.mu.RLock()
	dest, ok := n.connections[destID]
	n.mu.RUnlock()

	if !ok {
		return fmt.Errorf("peer not found")
	}

	// Check if we can use onion routing
	if n.onionRouter.CanRoute() {
		return n.sendViaOnion(dest, msgType, payload)
	}

	// Fallback to direct (network too small for anonymity)
	addr, err := net.ResolveUDPAddr("udp", dest.Address)
	if err != nil {
		return err
	}
	return n.sendDirect(addr, msgType, payload)
}

// sendViaOnion sends through onion routing
func (n *Node) sendViaOnion(dest *PeerConn, msgType byte, payload []byte) error {
	// Build circuit to destination
	circuit, err := n.onionRouter.BuildCircuit(dest.NodeID, dest.PublicKey, dest.Address)
	if err != nil {
		// Fallback to direct
		addr, _ := net.ResolveUDPAddr("udp", dest.Address)
		return n.sendDirect(addr, msgType, payload)
	}

	// Create inner message
	innerMsg := P2PMessage{
		Type:      msgType,
		From:      n.identity.NodeID(),
		FromKey:   n.identity.PublicKey(),
		Timestamp: time.Now().UnixNano(),
		Nonce:     security.GenerateNonce(),
		Payload:   payload,
	}
	toSign, _ := json.Marshal(struct {
		Type      byte   `json:"type"`
		From      string `json:"from"`
		Timestamp int64  `json:"ts"`
		Payload   []byte `json:"payload"`
	}{innerMsg.Type, innerMsg.From, innerMsg.Timestamp, innerMsg.Payload})
	innerMsg.Signature = n.identity.Sign(toSign)

	innerData, _ := json.Marshal(innerMsg)

	// Wrap in onion layers
	packet, firstHopAddr, err := n.onionRouter.WrapMessage(circuit, innerData)
	if err != nil {
		return err
	}

	packetData, _ := json.Marshal(packet)

	addr, err := net.ResolveUDPAddr("udp", firstHopAddr)
	if err != nil {
		return err
	}

	return n.sendDirect(addr, MsgTypeOnion, packetData)
}

// broadcast sends to all peers (via onion when possible)
func (n *Node) broadcast(msgType byte, payload []byte) int {
	n.mu.RLock()
	peers := make([]string, 0, len(n.connections))
	for id := range n.connections {
		peers = append(peers, id)
	}
	n.mu.RUnlock()

	count := 0
	for _, id := range peers {
		if err := n.sendAnonymous(id, msgType, payload); err == nil {
			count++
		}
	}
	return count
}

// bootstrap connects to bootstrap nodes
func (n *Node) bootstrap() {
	time.Sleep(time.Second)

	for _, addrStr := range n.config.DHT.BootstrapNodes {
		if err := security.ValidateAddress(addrStr); err != nil {
			continue
		}

		addr, err := net.ResolveUDPAddr("udp", addrStr)
		if err != nil {
			continue
		}

		log.Printf("[LINK] Connecting to bootstrap: %s", addrStr)
		n.sendDirect(addr, MsgTypePing, []byte(`"ping"`))

		time.Sleep(500 * time.Millisecond)
		target, _ := json.Marshal(n.identity.PublicKey()[:20])
		n.sendDirect(addr, MsgTypeFindNode, target)
	}
}

// maintenance runs periodic tasks
func (n *Node) maintenance() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			// Ping peers
			n.mu.RLock()
			for _, conn := range n.connections {
				if time.Since(conn.LastSeen) > time.Minute {
					if addr, err := net.ResolveUDPAddr("udp", conn.Address); err == nil {
						n.sendDirect(addr, MsgTypePing, []byte(`"ping"`))
					}
				}
			}
			n.mu.RUnlock()

			// Clean stale connections
			n.mu.Lock()
			for id, conn := range n.connections {
				if time.Since(conn.LastSeen) > 5*time.Minute {
					delete(n.connections, id)
					n.onionRouter.RemoveRelay(id)
				}
			}
			n.mu.Unlock()

			n.capStore.CleanExpired()
			n.nameService.CleanExpired()
			n.peerStore.Save()
		}
	}
}

// interactiveMode provides CLI interface
func (n *Node) interactiveMode() {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("\n┌───────────────────────────────────────────────┐")
	fmt.Println("│           KayakNet Internal Network            │")
	fmt.Println("├───────────────────────────────────────────────┤")
	fmt.Println("│  Commands: chat, market, domains, peers, help │")
	fmt.Println("└───────────────────────────────────────────────┘")
	fmt.Print("> ")

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		parts := strings.Fields(line)
		if len(parts) == 0 {
			fmt.Print("> ")
			continue
		}

		switch parts[0] {
		// === CHAT COMMANDS ===
		case "chat", "c":
			if len(parts) < 3 {
				fmt.Println("Usage: chat <room> <message>")
			} else {
				n.cmdChat(parts[1], strings.Join(parts[2:], " "))
			}
		case "rooms":
			n.cmdRooms()
		case "join":
			if len(parts) < 2 {
				fmt.Println("Usage: join <room>")
			} else {
				n.chatMgr.JoinRoom(parts[1])
				fmt.Printf("Joined #%s\n", parts[1])
			}
		case "history":
			if len(parts) < 2 {
				fmt.Println("Usage: history <room> [count]")
			} else {
				count := 20
				if len(parts) > 2 {
					count, _ = strconv.Atoi(parts[2])
				}
				n.cmdHistory(parts[1], count)
			}

		// === MARKETPLACE COMMANDS ===
		case "market":
			n.cmdMarket()
		case "browse":
			category := ""
			if len(parts) > 1 {
				category = parts[1]
			}
			n.cmdBrowse(category)
		case "sell":
			if len(parts) < 4 {
				fmt.Println("Usage: sell <title> <price> <description...>")
			} else {
				price, _ := strconv.ParseInt(parts[2], 10, 64)
				n.cmdSell(parts[1], price, strings.Join(parts[3:], " "))
			}
		case "buy":
			if len(parts) < 2 {
				fmt.Println("Usage: buy <listing-id>")
			} else {
				n.cmdBuy(parts[1])
			}
		case "mylistings":
			n.cmdMyListings()
		case "search":
			if len(parts) < 2 {
				fmt.Println("Usage: search <query>")
			} else {
				n.cmdSearch(strings.Join(parts[1:], " "))
			}

		// === DOMAIN COMMANDS (.kyk) ===
		case "register", "reg":
			if len(parts) < 2 {
				fmt.Println("Usage: register <name> [description]")
			} else {
				desc := ""
				if len(parts) > 2 {
					desc = strings.Join(parts[2:], " ")
				}
				n.cmdRegister(parts[1], desc)
			}
		case "resolve", "lookup":
			if len(parts) < 2 {
				fmt.Println("Usage: resolve <name.kyk>")
			} else {
				n.cmdResolve(parts[1])
			}
		case "domains", "mydomains":
			n.cmdDomains()
		case "whois":
			if len(parts) < 2 {
				fmt.Println("Usage: whois <name.kyk>")
			} else {
				n.cmdWhois(parts[1])
			}
		case "update-domain":
			if len(parts) < 3 {
				fmt.Println("Usage: update-domain <name> <address>")
			} else {
				n.cmdUpdateDomain(parts[1], parts[2])
			}
		case "search-domains":
			if len(parts) < 2 {
				fmt.Println("Usage: search-domains <query>")
			} else {
				n.cmdSearchDomains(strings.Join(parts[1:], " "))
			}

		// === NETWORK COMMANDS ===
		case "peers", "p":
			n.cmdPeers()
		case "connect":
			if len(parts) < 2 {
				fmt.Println("Usage: connect <address:port>")
			} else {
				n.cmdConnect(parts[1])
			}
		case "status", "s":
			n.cmdStatus()
		case "info", "i":
			n.cmdInfo()

		// === BROWSER PROXY COMMANDS ===
		case "proxy":
			n.cmdProxy()
		case "proxy-start":
			httpPort := 8118
			socksPort := 8119
			if len(parts) > 1 {
				httpPort, _ = strconv.Atoi(parts[1])
			}
			if len(parts) > 2 {
				socksPort, _ = strconv.Atoi(parts[2])
			}
			n.cmdProxyStart(httpPort, socksPort)
		case "proxy-stop":
			n.cmdProxyStop()
		case "proxy-setup":
			n.cmdProxySetup()

		// === SYSTEM ===
		case "quit", "q", "exit":
			fmt.Println("Goodbye!")
			n.cancel()
			return
		case "help", "h":
			n.cmdHelp()
		default:
			fmt.Printf("Unknown: %s (type 'help')\n", parts[0])
		}
		fmt.Print("> ")
	}
}

func (n *Node) cmdPeers() {
	n.mu.RLock()
	defer n.mu.RUnlock()

	if len(n.connections) == 0 {
		fmt.Println("No connected peers")
		return
	}

	fmt.Printf("\n[PEERS] Peers (%d):\n", len(n.connections))
	for _, conn := range n.connections {
		age := time.Since(conn.LastSeen).Round(time.Second)
		fmt.Printf("  • %s... (seen %s ago)\n", conn.NodeID[:16], age)
	}
}

func (n *Node) cmdChat(room, message string) {
	if err := security.ValidateName(room); err != nil {
		fmt.Println("Invalid room name (use alphanumeric)")
		return
	}

	// Create and store message locally
	msg, err := n.chatMgr.SendMessage(room, message)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	// Broadcast to network
	payload, _ := msg.Marshal()
	count := n.broadcast(MsgTypeChat, payload)

	if n.onionRouter.CanRoute() {
		fmt.Printf("[ONION] Sent anonymously to %d peers\n", count)
	} else {
		fmt.Printf("[SEND] Sent to %d peers (need %d+ for anonymity)\n", count, onion.MinHops)
	}
}

func (n *Node) cmdConnect(addrStr string) {
	if err := security.ValidateAddress(addrStr); err != nil {
		fmt.Printf("Invalid address: %v\n", err)
		return
	}

	addr, err := net.ResolveUDPAddr("udp", addrStr)
	if err != nil {
		fmt.Printf("Invalid address: %v\n", err)
		return
	}

	n.sendDirect(addr, MsgTypePing, []byte(`"ping"`))
	fmt.Printf("[LINK] Connecting to %s\n", addrStr)
}

func (n *Node) cmdStatus() {
	relays := n.onionRouter.GetRelayCount()

	fmt.Println("\n[SECURE] Anonymity Status:")
	if relays >= onion.MinHops {
		fmt.Printf("  [OK] ANONYMOUS - %d-hop onion routing active\n", onion.MinHops)
	} else {
		fmt.Printf("  [WARN]  Building network - need %d peers, have %d\n", onion.MinHops, relays)
	}

	fmt.Println("\n[SHIELD]  Traffic Analysis Defenses:")
	fmt.Printf("  • Constant-rate padding: %d byte messages\n", mix.PaddingSize)
	fmt.Printf("  • Batch mixing: every %s\n", mix.BatchInterval)
	fmt.Printf("  • Random delays: up to %s\n", mix.MaxDelay)
	fmt.Printf("  • Dummy traffic: %.1f msg/sec per peer\n", mix.DummyRate)
}

func (n *Node) cmdInfo() {
	n.mu.RLock()
	peerCount := len(n.connections)
	n.mu.RUnlock()

	listings, categories := n.marketplace.Stats()
	totalDomains, activeDomains := n.nameService.Stats()
	myDomains := len(n.nameService.MyDomains())

	fmt.Println("\n[INFO] Node Info:")
	fmt.Printf("  ID:      %s\n", n.identity.NodeID())
	fmt.Printf("  Name:    %s\n", n.name)
	fmt.Printf("  Peers:   %d\n", peerCount)
	fmt.Printf("  Relays:  %d\n", n.onionRouter.GetRelayCount())
	fmt.Printf("  Market:  %d listings in %d categories\n", listings, categories)
	fmt.Printf("  Domains: %d known, %d active, %d owned\n", totalDomains, activeDomains, myDomains)
}

// === CHAT COMMANDS ===

func (n *Node) cmdRooms() {
	rooms := n.chatMgr.ListRooms()
	fmt.Println("\n[CHAT] Chat Rooms:")
	for _, room := range rooms {
		fmt.Printf("  #%-15s %s\n", room.Name, room.Description)
	}
	fmt.Println("\nUse: chat <room> <message>")
}

func (n *Node) cmdHistory(room string, count int) {
	msgs := n.chatMgr.GetMessages(room, count)
	if len(msgs) == 0 {
		fmt.Printf("No messages in #%s\n", room)
		return
	}

	fmt.Printf("\n[CHAT] #%s (last %d messages):\n", room, len(msgs))
	for _, msg := range msgs {
		ts := msg.Timestamp.Format("15:04")
		fmt.Printf("  [%s] %s: %s\n", ts, msg.Nick, msg.Content)
	}
}

// === MARKETPLACE COMMANDS ===

func (n *Node) cmdMarket() {
	listings, categories := n.marketplace.Stats()
	fmt.Println("\n[PKG] KayakNet Marketplace")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("  Listings:   %d\n", listings)
	fmt.Printf("  Categories: %d\n", categories)
	fmt.Println("\nCommands:")
	fmt.Println("  browse [category]  - Browse listings")
	fmt.Println("  search <query>     - Search listings")
	fmt.Println("  sell <title> <price> <desc> - Create listing")
	fmt.Println("  buy <id>           - Purchase/request access")
	fmt.Println("  mylistings         - Your listings")
}

func (n *Node) cmdBrowse(category string) {
	listings := n.marketplace.Browse(category)
	if len(listings) == 0 {
		fmt.Println("No listings found")
		return
	}

	fmt.Println("\n[PKG] Marketplace Listings:")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	for _, l := range listings {
		rating := "*"
		if l.Rating > 0 {
			rating = fmt.Sprintf("%.1f*", l.Rating)
		}
		fmt.Printf("  [%s] %s\n", l.ID[:8], l.Title)
		fmt.Printf("       %d %s  |  %s  |  by %s...\n", l.Price, l.Currency, rating, l.SellerID[:8])
		if l.Description != "" {
			desc := l.Description
			if len(desc) > 60 {
				desc = desc[:60] + "..."
			}
			fmt.Printf("      %s\n", desc)
		}
		fmt.Println()
	}
}

func (n *Node) cmdSell(title string, price int64, description string) {
	listing, err := n.marketplace.CreateListing(
		title,
		description,
		"general",
		price,
		"credits",
		7*24*time.Hour, // 7 day TTL
	)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	// Broadcast to network
	data, _ := listing.Marshal()
	n.broadcast(MsgTypeListing, data)

	fmt.Printf("[OK] Listed: %s\n", listing.Title)
	fmt.Printf("   ID: %s\n", listing.ID)
	fmt.Printf("   Price: %d credits\n", listing.Price)
}

func (n *Node) cmdBuy(listingID string) {
	// Find listing (partial ID match)
	var found *market.Listing
	for _, l := range n.marketplace.Browse("") {
		if strings.HasPrefix(l.ID, listingID) {
			found = l
			break
		}
	}

	if found == nil {
		fmt.Println("Listing not found")
		return
	}

	req, err := n.marketplace.CreatePurchaseRequest(found.ID, "Interested in purchasing")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("[MSG] Request sent to seller\n")
	fmt.Printf("   Listing: %s\n", found.Title)
	fmt.Printf("   Request ID: %s\n", req.ID)
	fmt.Println("   The seller will receive your anonymous request.")
}

func (n *Node) cmdMyListings() {
	listings := n.marketplace.GetMyListings()
	if len(listings) == 0 {
		fmt.Println("You have no listings")
		return
	}

	fmt.Println("\n[PKG] Your Listings:")
	for _, l := range listings {
		status := "[OK] Active"
		if !l.Active {
			status = "[ERR] Inactive"
		}
		fmt.Printf("  [%s] %s - %d %s %s\n", l.ID[:8], l.Title, l.Price, l.Currency, status)
	}
}

func (n *Node) cmdSearch(query string) {
	listings := n.marketplace.Search(query)
	if len(listings) == 0 {
		fmt.Printf("No listings matching '%s'\n", query)
		return
	}

	fmt.Printf("\n[SEARCH] Search results for '%s':\n", query)
	for _, l := range listings {
		fmt.Printf("  [%s] %s - %d %s\n", l.ID[:8], l.Title, l.Price, l.Currency)
	}
}

// === DOMAIN COMMANDS (.kyk) ===

func (n *Node) cmdRegister(name, description string) {
	reg, err := n.nameService.Register(name, description, "")
	if err != nil {
		fmt.Printf("[ERR] Failed: %v\n", err)
		return
	}

	// Broadcast registration to network
	data, _ := reg.Marshal()
	n.broadcast(MsgTypeNameReg, data)

	fmt.Printf("[OK] Registered: %s\n", reg.FullName)
	fmt.Printf("   Owner:   %s...\n", reg.NodeID[:16])
	fmt.Printf("   Expires: %s\n", reg.ExpiresAt.Format("2006-01-02"))
	fmt.Println("\n   Your domain is now resolvable on KayakNet!")
}

func (n *Node) cmdResolve(domain string) {
	if !names.IsKykDomain(domain) {
		domain = domain + names.DomainSuffix
	}

	reg, err := n.nameService.Resolve(domain)
	if err != nil {
		// Try asking network
		payload, _ := json.Marshal(domain)
		n.broadcast(MsgTypeNameLookup, payload)
		fmt.Printf("[SEARCH] Looking up %s on network...\n", domain)
		return
	}

	fmt.Printf("\n[NET] %s\n", reg.FullName)
	fmt.Printf("   Node:    %s...\n", reg.NodeID[:16])
	if reg.Address != "" {
		fmt.Printf("   Address: %s\n", reg.Address)
	}
	if reg.ServiceType != "" {
		fmt.Printf("   Type:    %s\n", reg.ServiceType)
	}
	if reg.Description != "" {
		fmt.Printf("   Desc:    %s\n", reg.Description)
	}
}

func (n *Node) cmdDomains() {
	domains := n.nameService.MyDomains()
	if len(domains) == 0 {
		fmt.Println("You have no .kyk domains")
		fmt.Println("Use: register <name> [description]")
		return
	}

	fmt.Println("\n[NET] Your .kyk Domains:")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	for _, reg := range domains {
		expires := reg.ExpiresAt.Format("2006-01-02")
		fmt.Printf("  %-20s (expires %s)\n", reg.FullName, expires)
		if reg.Description != "" {
			fmt.Printf("      %s\n", reg.Description)
		}
	}
}

func (n *Node) cmdWhois(domain string) {
	if !names.IsKykDomain(domain) {
		domain = domain + names.DomainSuffix
	}

	reg, err := n.nameService.Resolve(domain)
	if err != nil {
		fmt.Printf("[ERR] %s not found\n", domain)
		return
	}

	fmt.Printf("\n[INFO] WHOIS: %s\n", reg.FullName)
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("  Domain:      %s\n", reg.FullName)
	fmt.Printf("  Owner Node:  %s\n", reg.NodeID)
	fmt.Printf("  Registered:  %s\n", reg.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("  Updated:     %s\n", reg.UpdatedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("  Expires:     %s\n", reg.ExpiresAt.Format("2006-01-02 15:04:05"))
	if reg.Address != "" {
		fmt.Printf("  Address:     %s\n", reg.Address)
	}
	if reg.ServiceType != "" {
		fmt.Printf("  Type:        %s\n", reg.ServiceType)
	}
	if reg.Description != "" {
		fmt.Printf("  Description: %s\n", reg.Description)
	}
}

func (n *Node) cmdUpdateDomain(name, address string) {
	if names.IsKykDomain(name) {
		name = strings.TrimSuffix(name, names.DomainSuffix)
	}

	if err := n.nameService.Update(name, address, ""); err != nil {
		fmt.Printf("[ERR] Failed: %v\n", err)
		return
	}

	// Broadcast update
	reg, _ := n.nameService.Resolve(name + names.DomainSuffix)
	if reg != nil {
		data, _ := reg.Marshal()
		n.broadcast(MsgTypeNameReg, data)
	}

	fmt.Printf("[OK] Updated %s%s\n", name, names.DomainSuffix)
	fmt.Printf("   Address: %s\n", address)
}

func (n *Node) cmdSearchDomains(query string) {
	results := n.nameService.Search(query)
	if len(results) == 0 {
		fmt.Printf("No domains matching '%s'\n", query)
		return
	}

	fmt.Printf("\n[SEARCH] Domains matching '%s':\n", query)
	for _, reg := range results {
		fmt.Printf("  %-20s - %s\n", reg.FullName, reg.Description)
	}
}

// === BROWSER PROXY COMMANDS ===

func (n *Node) cmdProxy() {
	if n.browserProxy.IsRunning() {
		requests, bytesIn, bytesOut := n.browserProxy.Stats()
		fmt.Println("\n[PROXY] Browser Proxy Status: RUNNING")
		fmt.Printf("  HTTP Port:   127.0.0.1:%d\n", n.browserProxy.Port())
		fmt.Printf("  SOCKS5 Port: 127.0.0.1:%d\n", n.browserProxy.Port()+1)
		fmt.Printf("  Requests:    %d\n", requests)
		fmt.Printf("  Bytes In:    %d\n", bytesIn)
		fmt.Printf("  Bytes Out:   %d\n", bytesOut)
		fmt.Println("\nBrowser can access .kyk domains when configured to use proxy")
	} else {
		fmt.Println("\n[PROXY] Browser Proxy Status: STOPPED")
		fmt.Println("  Use 'proxy-start' to enable browser access")
		fmt.Println("  Use 'proxy-setup' to see browser configuration instructions")
	}
}

func (n *Node) cmdProxyStart(httpPort, socksPort int) {
	if n.browserProxy.IsRunning() {
		fmt.Println("[WARN] Proxy already running")
		return
	}

	if err := n.browserProxy.Start(httpPort, socksPort); err != nil {
		fmt.Printf("[ERR] Failed to start proxy: %v\n", err)
		return
	}

	fmt.Println("[OK] Browser proxy started")
	fmt.Printf("  HTTP Proxy:   127.0.0.1:%d\n", httpPort)
	fmt.Printf("  SOCKS5 Proxy: 127.0.0.1:%d\n", socksPort)
	fmt.Println("\nConfigure your browser to use these proxy settings")
	fmt.Println("Then browse to any .kyk domain (e.g., http://example.kyk)")
}

func (n *Node) cmdProxyStop() {
	if !n.browserProxy.IsRunning() {
		fmt.Println("[WARN] Proxy not running")
		return
	}

	n.browserProxy.Stop()
	fmt.Println("[OK] Browser proxy stopped")
}

func (n *Node) cmdProxySetup() {
	fmt.Println(proxy.ProxyInfo(8118, 8119))
}

func (n *Node) cmdHelp() {
	fmt.Println(`
+-------------------------------------------------------------+
|                    KayakNet Commands                        |
+-------------------------------------------------------------+
| CHAT                                                        |
|   chat <room> <msg>  - Send message to room                 |
|   rooms              - List chat rooms                      |
|   join <room>        - Join a room                          |
|   history <room>     - Show room history                    |
|                                                             |
| MARKETPLACE (network-only access)                           |
|   market             - Marketplace overview                 |
|   browse [category]  - Browse all listings                  |
|   search <query>     - Search listings                      |
|   sell <t> <p> <d>   - Create listing                       |
|   buy <id>           - Request to purchase                  |
|   mylistings         - Your listings                        |
|                                                             |
| DOMAINS (.kyk)                                              |
|   register <name>    - Register a .kyk domain               |
|   resolve <name>     - Resolve .kyk domain                  |
|   domains            - List your domains                    |
|   whois <name>       - Domain details                       |
|   update-domain      - Update domain address                |
|   search-domains     - Search domains                       |
|                                                             |
| BROWSER PROXY (access .kyk in browser)                      |
|   proxy              - Proxy status                         |
|   proxy-start        - Start browser proxy                  |
|   proxy-stop         - Stop browser proxy                   |
|   proxy-setup        - Browser configuration guide          |
|                                                             |
| NETWORK                                                     |
|   peers              - List connected peers                 |
|   connect <addr>     - Connect to peer                      |
|   status             - Anonymity status                     |
|   info               - Node information                     |
|                                                             |
| quit/exit            - Exit KayakNet                        |
+-------------------------------------------------------------+
`)
}
