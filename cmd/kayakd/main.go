// Package main implements the KayakNet node daemon
// Anonymous P2P network with built-in onion routing
package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	crand "crypto/rand"
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
	"github.com/kayaknet/kayaknet/internal/e2e"
	"github.com/kayaknet/kayaknet/internal/escrow"
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
	mu           sync.RWMutex
	config       *config.Config
	identity     *identity.Identity
	peerStore    *peer.Store
	dht          *dht.DHT
	pubsub       *pubsub.PubSub
	capStore     *cap.Store
	onionRouter  *onion.Router
	mixer        *mix.Mixer
	marketplace  *market.Marketplace
	chatMgr      *chat.ChatManager
	nameService  *names.NameService
	browserProxy *proxy.Proxy
	homepage     *Homepage
	escrowMgr    *escrow.EscrowManager
	cryptoWallet *escrow.CryptoWallet
	e2e          *e2e.E2EManager
	listener     net.PacketConn
	connections  map[string]*PeerConn
	ctx          context.Context
	cancel       context.CancelFunc
	name         string

	// Security
	rateLimiter  *security.RateLimiter
	banList      *security.BanList
	peerScorer   *security.PeerScorer
	nonceTracker *security.NonceTracker
	validator    *security.MessageValidator
	powManager   *security.PoWManager         // Anti-Sybil proof-of-work
	hwKeyMgr     *security.HardwareKeyManager // Hardware key support
	localPoW     *security.ProofOfWork        // Our own PoW proof

	// Gossip routing
	seenMessages map[string]time.Time // MsgID -> first seen time (deduplication)
	seenMu       sync.RWMutex         // Lock for seenMessages
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
	MsgTypePing         = 0x01
	MsgTypePong         = 0x02
	MsgTypeFindNode     = 0x03
	MsgTypeNodes        = 0x04
	MsgTypeChat         = 0x08
	MsgTypeListing      = 0x10 // Marketplace listing
	MsgTypeOnion        = 0x20 // Onion-routed message
	MsgTypeNameReg      = 0x30 // .kyk domain registration
	MsgTypeNameLookup   = 0x31 // .kyk domain lookup
	MsgTypeNameReply    = 0x32 // .kyk domain resolution response
	MsgTypePoWChallenge = 0x40 // Proof-of-work challenge (anti-Sybil)
	MsgTypePoWResponse  = 0x41 // Proof-of-work response
	MsgTypePoWVerified  = 0x42 // PoW verification acknowledgment
)

// P2PMessage is the wire format
type P2PMessage struct {
	Type      byte            `json:"type"`
	From      string          `json:"from"`
	FromKey   []byte          `json:"from_key"`
	Timestamp int64           `json:"ts"`
	Nonce     uint64          `json:"nonce"`
	TTL       int             `json:"ttl"`       // Time-to-live for gossip (hops remaining)
	MsgID     string          `json:"msg_id"`    // Unique message ID for deduplication
	Payload   json.RawMessage `json:"payload"`
	Signature []byte          `json:"sig"`
}

// Homepage serves the main KayakNet portal at home.kyk
type Homepage struct {
	mu      sync.RWMutex
	port    int
	node    *Node
	server  *http.Server
	running bool
}

// NewHomepage creates a new homepage server
func NewHomepage(node *Node) *Homepage {
	return &Homepage{
		port: 8080,
		node: node,
	}
}

// Start starts the homepage server
func (h *Homepage) Start(port int) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.running {
		return nil
	}

	h.port = port

	mux := http.NewServeMux()
	mux.HandleFunc("/", h.handleHome)
	mux.HandleFunc("/api/search", h.handleSearch)
	mux.HandleFunc("/api/listings", h.handleListings)
	mux.HandleFunc("/api/reviews", h.handleReviews)
	mux.HandleFunc("/api/seller", h.handleSeller)
	mux.HandleFunc("/api/create-listing", h.handleCreateListing)
	mux.HandleFunc("/api/my-listings", h.handleMyListings)
	mux.HandleFunc("/api/escrow/create", h.handleEscrowCreate)
	mux.HandleFunc("/api/escrow/status", h.handleEscrowStatus)
	mux.HandleFunc("/api/escrow/pay", h.handleEscrowPay)
	mux.HandleFunc("/api/escrow/ship", h.handleEscrowShip)
	mux.HandleFunc("/api/escrow/release", h.handleEscrowRelease)
	mux.HandleFunc("/api/escrow/dispute", h.handleEscrowDispute)
	mux.HandleFunc("/api/escrow/my", h.handleMyEscrows)
	mux.HandleFunc("/api/chat", h.handleChat)
	mux.HandleFunc("/api/chat/rooms", h.handleChatRooms)
	mux.HandleFunc("/api/chat/profile", h.handleChatProfile)
	mux.HandleFunc("/api/chat/conversations", h.handleChatConversations)
	mux.HandleFunc("/api/chat/dm", h.handleChatDM)
	mux.HandleFunc("/api/chat/users", h.handleChatUsers)
	mux.HandleFunc("/api/chat/user", h.handleChatUser)
	mux.HandleFunc("/api/chat/search", h.handleChatSearch)
	mux.HandleFunc("/api/chat/reaction", h.handleChatReaction)
	mux.HandleFunc("/api/chat/block", h.handleChatBlock)
	mux.HandleFunc("/api/domains", h.handleDomains)
	mux.HandleFunc("/api/peers", h.handlePeers)
	mux.HandleFunc("/api/stats", h.handleStats)
	mux.HandleFunc("/marketplace", h.handleMarketplace)
	mux.HandleFunc("/chat", h.handleChatPage)
	mux.HandleFunc("/domains", h.handleDomainsPage)
	mux.HandleFunc("/network", h.handleNetworkPage)

	h.server = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", port),
		Handler: mux,
	}

	h.running = true

	go func() {
		if err := h.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[HOMEPAGE] Server error: %v", err)
		}
	}()

	return nil
}

// Stop stops the homepage server
func (h *Homepage) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.server != nil {
		h.server.Close()
	}
	h.running = false
}

// Port returns the homepage port
func (h *Homepage) Port() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.port
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
		// Start homepage server first (at home.kyk)
		homepagePort := 8080
		if err := node.homepage.Start(homepagePort); err != nil {
			log.Printf("[WARN] Failed to start homepage: %v", err)
		}
		// Tell proxy where homepage is
		node.browserProxy.SetHomepagePort(homepagePort)

		if err := node.browserProxy.Start(*proxyHTTP, *proxySOCKS); err != nil {
			log.Printf("[WARN] Failed to start browser proxy: %v", err)
		} else {
			fmt.Printf("║  [+] Browser proxy: HTTP %d, SOCKS5 %d                 ║\n", *proxyHTTP, *proxySOCKS)
			fmt.Println("║  [+] Homepage: http://home.kyk                         ║")
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

	// Anti-Sybil: Proof of Work manager
	// Difficulty 20 = ~1 second to solve on modern CPU
	powManager := security.NewPoWManager(20)

	// Hardware Key Support - try to detect hardware keys
	hwKeyMgr := security.NewHardwareKeyManager()
	devices, _ := hwKeyMgr.DetectHardwareKeys()
	if len(devices) > 0 {
		log.Printf("[SECURITY] Hardware key detected: %s", devices[0].Name)
		if err := hwKeyMgr.Initialize(devices[0]); err == nil {
			log.Printf("[SECURITY] Using hardware key for signing")
		}
	}

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
		powManager:   powManager,
		hwKeyMgr:     hwKeyMgr,
		seenMessages: make(map[string]time.Time),
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

	// Create E2E encryption manager for perfect privacy
	// Messages encrypted so ONLY recipient can read - no relay/bootstrap oversight
	n.e2e = e2e.NewE2EManager(
		id.NodeID(),
		id.PublicKey(),
		ed25519.PrivateKey(id.PrivateKey()),
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

	// Create homepage (served at home.kyk and kayaknet.kyk)
	n.homepage = NewHomepage(n)

	// Create cryptocurrency wallet and escrow manager
	// Configure wallet RPC endpoints in config.json for production
	moneroHost := ""
	if cfg.Crypto.Monero.Enabled && cfg.Crypto.Monero.RPCHost != "" {
		moneroHost = fmt.Sprintf("%s:%d", cfg.Crypto.Monero.RPCHost, cfg.Crypto.Monero.RPCPort)
	}
	zcashHost := ""
	if cfg.Crypto.Zcash.Enabled && cfg.Crypto.Zcash.RPCHost != "" {
		zcashHost = fmt.Sprintf("%s:%d", cfg.Crypto.Zcash.RPCHost, cfg.Crypto.Zcash.RPCPort)
	}
	walletConfig := escrow.WalletConfig{
		MoneroRPCHost:     moneroHost,
		MoneroRPCUser:     cfg.Crypto.Monero.RPCUser,
		MoneroRPCPassword: cfg.Crypto.Monero.RPCPassword,
		ZcashRPCHost:      zcashHost,
		ZcashRPCUser:      cfg.Crypto.Zcash.RPCUser,
		ZcashRPCPassword:  cfg.Crypto.Zcash.RPCPassword,
	}
	n.cryptoWallet = escrow.NewCryptoWallet(walletConfig)
	n.escrowMgr = escrow.NewEscrowManager(n.cryptoWallet, 2.0) // 2% platform fee

	// Start escrow maintenance goroutine
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-n.ctx.Done():
				return
			case <-ticker.C:
				n.escrowMgr.ProcessAutoReleases()
				n.escrowMgr.ProcessExpiredEscrows()
			}
		}
	}()

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
	go n.cleanSeenMessages() // Cleanup old gossip message IDs

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

	// Gossip routing: check if we've seen this message before
	shouldGossip := n.isGossipMessage(msg.Type)
	if shouldGossip && msg.MsgID != "" {
		n.seenMu.Lock()
		if _, seen := n.seenMessages[msg.MsgID]; seen {
			n.seenMu.Unlock()
			return // Already processed this message
		}
		n.seenMessages[msg.MsgID] = time.Now()
		n.seenMu.Unlock()

		// Forward to other peers (gossip) if TTL > 0
		if msg.TTL > 0 {
			go n.forwardMessage(msg, from.String())
		}
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
	case MsgTypePoWChallenge:
		n.handlePoWChallenge(from, msg)
	case MsgTypePoWResponse:
		n.handlePoWResponse(from, msg)
	case MsgTypePoWVerified:
		n.handlePoWVerified(msg)
	}
}

// isGossipMessage returns true for message types that should be flooded
func (n *Node) isGossipMessage(msgType byte) bool {
	switch msgType {
	case MsgTypeChat, MsgTypeListing, MsgTypeNameReg:
		return true
	default:
		return false
	}
}

// forwardMessage forwards a gossip message to all peers except the sender
func (n *Node) forwardMessage(msg *P2PMessage, senderAddr string) {
	// Decrement TTL
	forwardMsg := *msg
	forwardMsg.TTL--

	data, err := json.Marshal(forwardMsg)
	if err != nil {
		return
	}

	n.mu.RLock()
	peers := make([]*PeerConn, 0, len(n.connections))
	for _, peer := range n.connections {
		if peer.Address != senderAddr && peer.NodeID != msg.From {
			peers = append(peers, peer)
		}
	}
	n.mu.RUnlock()

	// Forward to all other peers
	for _, peer := range peers {
		addr, err := net.ResolveUDPAddr("udp", peer.Address)
		if err != nil {
			continue
		}
		n.listener.WriteTo(data, addr)
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

// handlePing responds to ping and challenges unverified nodes
func (n *Node) handlePing(from net.Addr, msg *P2PMessage) {
	// Always respond to ping
	n.sendDirect(from, MsgTypePong, msg.Payload)

	// If this node hasn't completed PoW, send them a challenge
	if n.requirePoW(msg.From) {
		n.sendPoWChallenge(from, msg.From)
	}
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

		// Add as potential relay and register E2E key
		if len(node.Key) == ed25519.PublicKeySize {
			n.onionRouter.AddRelay(node.ID, node.Key, node.Addr)
			n.e2e.AddPeerKey(node.ID, node.Key)
		}

		addr, err := net.ResolveUDPAddr("udp", node.Addr)
		if err != nil {
			continue
		}
		n.sendDirect(addr, MsgTypePing, []byte(`"ping"`))
	}
}

// handleChat processes chat (may be onion-routed, always E2E encrypted)
func (n *Node) handleChat(msg *P2PMessage) {
	// Register sender's key for future E2E encryption
	if len(msg.FromKey) == ed25519.PublicKeySize {
		n.e2e.AddPeerKey(msg.From, msg.FromKey)
	}

	// First try to decrypt E2E envelope
	envelope, err := e2e.UnmarshalEnvelope(msg.Payload)
	if err == nil {
		// This is E2E encrypted - decrypt it
		decrypted, err := n.e2e.Decrypt(envelope)
		if err != nil {
			// Not for us or decryption failed - ignore silently
			return
		}
		msg.Payload = decrypted
	}

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

// handlePoWChallenge processes PoW challenges from the network
// When we connect to a new node, they may challenge us
func (n *Node) handlePoWChallenge(from net.Addr, msg *P2PMessage) {
	var challenge security.Challenge
	if err := json.Unmarshal(msg.Payload, &challenge); err != nil {
		return
	}

	log.Printf("[POW] Received challenge from network (difficulty %d)", challenge.Difficulty)

	// Solve the challenge in background
	go func() {
		startTime := time.Now()
		proof, err := security.SolveChallenge(&challenge, n.identity.NodeID())
		if err != nil {
			log.Printf("[POW] Failed to solve challenge: %v", err)
			return
		}

		solveTime := time.Since(startTime)
		log.Printf("[POW] Solved challenge in %v (nonce: %d)", solveTime, proof.Nonce)

		// Sign the proof
		proofData, _ := json.Marshal(proof)
		proof.Signature = n.identity.Sign(proofData)

		// Store our proof
		n.mu.Lock()
		n.localPoW = proof
		n.mu.Unlock()

		// Send response
		payload, _ := json.Marshal(proof)
		n.sendDirect(from, MsgTypePoWResponse, payload)
	}()
}

// handlePoWResponse processes PoW responses from nodes
func (n *Node) handlePoWResponse(from net.Addr, msg *P2PMessage) {
	var proof security.ProofOfWork
	if err := json.Unmarshal(msg.Payload, &proof); err != nil {
		return
	}

	// Verify the proof
	if err := n.powManager.VerifyProof(&proof); err != nil {
		log.Printf("[POW] Invalid proof from %s: %v", msg.From, err)
		// Record bad behavior
		n.peerScorer.RecordBad(msg.From, from.String(), "invalid PoW")
		return
	}

	log.Printf("[POW] Verified proof from %s", msg.From[:16])

	// Send verification acknowledgment
	ack := map[string]interface{}{
		"verified": true,
		"node_id":  msg.From,
	}
	payload, _ := json.Marshal(ack)
	n.sendDirect(from, MsgTypePoWVerified, payload)

	// Record good behavior
	n.peerScorer.RecordGood(msg.From)
}

// handlePoWVerified processes PoW verification acknowledgments
func (n *Node) handlePoWVerified(msg *P2PMessage) {
	var ack struct {
		Verified bool   `json:"verified"`
		NodeID   string `json:"node_id"`
	}
	if err := json.Unmarshal(msg.Payload, &ack); err != nil {
		return
	}

	if ack.Verified && ack.NodeID == n.identity.NodeID() {
		log.Printf("[POW] Our proof verified by %s", msg.From[:16])
	}
}

// sendPoWChallenge sends a PoW challenge to a new node
func (n *Node) sendPoWChallenge(addr net.Addr, nodeID string) {
	challenge := n.powManager.GenerateChallenge(nodeID)
	payload, _ := json.Marshal(challenge)
	n.sendDirect(addr, MsgTypePoWChallenge, payload)
	log.Printf("[POW] Sent challenge to %s (difficulty %d)", nodeID[:16], challenge.Difficulty)
}

// requirePoW checks if a node needs to complete PoW before full access
func (n *Node) requirePoW(nodeID string) bool {
	return n.powManager.RequireProof(nodeID)
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

// broadcastGossip sends a message with gossip routing (TTL + MsgID for network-wide propagation)
// This ensures messages reach all nodes even if they're not directly connected
func (n *Node) broadcastGossip(msgType byte, payload []byte) int {
	// Generate unique message ID
	msgIDBytes := make([]byte, 16)
	crand.Read(msgIDBytes)
	msgID := hex.EncodeToString(msgIDBytes)

	// Mark as seen locally to prevent echo
	n.seenMu.Lock()
	n.seenMessages[msgID] = time.Now()
	n.seenMu.Unlock()

	// Create message with TTL (5 hops max) and MsgID
	msg := P2PMessage{
		Type:      msgType,
		From:      n.identity.NodeID(),
		FromKey:   n.identity.PublicKey(),
		Timestamp: time.Now().UnixNano(),
		Nonce:     security.GenerateNonce(),
		TTL:       5, // 5 hops should cover most networks
		MsgID:     msgID,
		Payload:   payload,
	}

	// Sign the message
	toSign, _ := json.Marshal(struct {
		Type      byte   `json:"type"`
		From      string `json:"from"`
		Timestamp int64  `json:"ts"`
		Payload   []byte `json:"payload"`
	}{msg.Type, msg.From, msg.Timestamp, msg.Payload})
	msg.Signature = n.identity.Sign(toSign)

	data, err := json.Marshal(msg)
	if err != nil {
		return 0
	}

	n.mu.RLock()
	peers := make([]*PeerConn, 0, len(n.connections))
	for _, peer := range n.connections {
		peers = append(peers, peer)
	}
	n.mu.RUnlock()

	count := 0
	for _, peer := range peers {
		addr, err := net.ResolveUDPAddr("udp", peer.Address)
		if err != nil {
			continue
		}
		if _, err := n.listener.WriteTo(data, addr); err == nil {
			count++
		}
	}
	return count
}

// cleanSeenMessages removes old entries from seenMessages (run periodically)
func (n *Node) cleanSeenMessages() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-5 * time.Minute)
			n.seenMu.Lock()
			for msgID, seenTime := range n.seenMessages {
				if seenTime.Before(cutoff) {
					delete(n.seenMessages, msgID)
				}
			}
			n.seenMu.Unlock()
		}
	}
}

// sendE2EEncryptedDM sends a DM with end-to-end encryption
// Only the recipient can decrypt - not relays, not bootstrap, nobody else
func (n *Node) sendE2EEncryptedDM(recipientID string, msg *chat.Message) error {
	// Serialize the message
	plaintext, err := msg.Marshal()
	if err != nil {
		return err
	}

	// E2E encrypt for recipient only
	envelope, err := n.e2e.Encrypt(recipientID, plaintext)
	if err != nil {
		// Key not known - broadcast unencrypted (will be encrypted by transport)
		log.Printf("[E2E] Recipient key not found, sending via transport encryption only")
		return n.sendAnonymous(recipientID, MsgTypeChat, plaintext)
	}

	// Serialize envelope
	envelopeData, err := envelope.Marshal()
	if err != nil {
		return err
	}

	// Send via onion routing (double protection: E2E + onion)
	return n.sendAnonymous(recipientID, MsgTypeChat, envelopeData)
}

// sendE2EBroadcast encrypts a message for all known recipients
// Each recipient gets their own E2E encrypted copy
func (n *Node) sendE2EBroadcast(msgType byte, plaintext []byte) int {
	n.mu.RLock()
	peers := make([]string, 0, len(n.connections))
	for id := range n.connections {
		peers = append(peers, id)
	}
	n.mu.RUnlock()

	count := 0
	for _, id := range peers {
		// Try E2E encryption for each peer
		envelope, err := n.e2e.Encrypt(id, plaintext)
		if err != nil {
			// No key for this peer, send transport-encrypted only
			if err := n.sendAnonymous(id, msgType, plaintext); err == nil {
				count++
			}
			continue
		}

		envelopeData, _ := envelope.Marshal()
		if err := n.sendAnonymous(id, msgType, envelopeData); err == nil {
			count++
		}
	}
	return count
}

// broadcastListing broadcasts a marketplace listing to all peers via gossip
func (n *Node) broadcastListing(listing *market.Listing) {
	data, err := listing.Marshal()
	if err != nil {
		log.Printf("[MARKET] Failed to marshal listing: %v", err)
		return
	}

	count := n.broadcastGossip(MsgTypeListing, data)
	log.Printf("[MARKET] Broadcast listing '%s' to %d peers (gossip)", listing.Title, count)
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
				price, _ := strconv.ParseFloat(parts[2], 64)
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

	// Broadcast with gossip routing (propagates to entire network)
	payload, _ := msg.Marshal()
	count := n.broadcastGossip(MsgTypeChat, payload)

	fmt.Printf("[GOSSIP] Chat sent to %d peers (will propagate network-wide)\n", count)
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

func (n *Node) cmdSell(title string, price float64, description string) {
	listing, err := n.marketplace.CreateListing(
		title,
		description,
		"general",
		price,
		"XMR",
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

HOMEPAGE: Browse to http://home.kyk or http://kayaknet.kyk
`)
}

// ============================================================================
// Homepage Handlers
// ============================================================================

func (h *Homepage) handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(homepageHTML))
}

func (h *Homepage) handleSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	var results []map[string]string

	if query != "" && h.node != nil {
		// Search listings
		listings := h.node.marketplace.Search(query)
		for _, l := range listings {
			results = append(results, map[string]string{
				"type":        "listing",
				"title":       l.Title,
				"description": l.Description,
				"url":         "/marketplace?id=" + l.ID,
			})
		}

		// Search domains
		domains := h.node.nameService.Search(query)
		for _, d := range domains {
			results = append(results, map[string]string{
				"type":        "domain",
				"title":       d.Name + ".kyk",
				"description": d.Description,
				"url":         "/domains?name=" + d.Name,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func (h *Homepage) handleListings(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")
	query := r.URL.Query().Get("q")
	var listings []map[string]interface{}

	if h.node != nil {
		var results []*market.Listing
		if query != "" {
			results = h.node.marketplace.Search(query)
		} else {
			results = h.node.marketplace.Browse(category)
		}

		for _, l := range results {
			listings = append(listings, map[string]interface{}{
				"id":           l.ID,
				"title":        l.Title,
				"description":  l.Description,
				"price":        l.Price,
				"currency":     l.Currency,
				"image":        l.Image,
				"seller_id":    l.SellerID,
				"seller_name":  l.SellerName,
				"category":     l.Category,
				"rating":       l.Rating,
				"review_count": l.ReviewCount,
				"views":        l.Views,
				"created_at":   l.CreatedAt.Format(time.RFC3339),
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(listings)
}

func (h *Homepage) handleReviews(w http.ResponseWriter, r *http.Request) {
	listingID := r.URL.Query().Get("listing")
	sellerID := r.URL.Query().Get("seller")
	var reviews []map[string]interface{}

	if h.node != nil {
		var results []*market.Review
		if listingID != "" {
			results = h.node.marketplace.GetReviews(listingID)
		} else if sellerID != "" {
			results = h.node.marketplace.GetSellerReviews(sellerID)
		}

		for _, r := range results {
			reviews = append(reviews, map[string]interface{}{
				"id":            r.ID,
				"listing_id":    r.ListingID,
				"buyer_id":      r.BuyerID,
				"buyer_name":    r.BuyerName,
				"rating":        r.Rating,
				"comment":       r.Comment,
				"quality":       r.Quality,
				"shipping":      r.Shipping,
				"communication": r.Communication,
				"created_at":    r.CreatedAt.Format(time.RFC3339),
				"verified":      r.Verified,
				"seller_reply":  r.SellerReply,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reviews)
}

func (h *Homepage) handleCreateListing(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.node == nil {
		http.Error(w, "Node not available", http.StatusServiceUnavailable)
		return
	}

	// Parse form data
	title := r.FormValue("title")
	category := r.FormValue("category")
	priceStr := r.FormValue("price")
	currency := r.FormValue("currency")
	description := r.FormValue("description")
	image := r.FormValue("image")

	if title == "" {
		http.Error(w, "Title is required", http.StatusBadRequest)
		return
	}

	price, _ := strconv.ParseFloat(priceStr, 64)
	if price < 0 {
		price = 0
	}

	// Default to XMR if no currency specified
	if currency == "" {
		currency = "XMR"
	}
	// Validate currency
	if currency != "XMR" && currency != "ZEC" {
		currency = "XMR"
	}

	// Get seller name from node config or use default
	sellerName := h.node.marketplace.GetLocalName()
	if sellerName == "" || sellerName == "anonymous" {
		if h.node.name != "" {
			sellerName = h.node.name
		} else {
			sellerName = "anonymous"
		}
	}

	// Create the listing
	listing, err := h.node.marketplace.CreateListingFull(
		title,
		description,
		category,
		price,
		currency,
		image,
		sellerName,
		30*24*time.Hour, // 30 days TTL
	)

	if err != nil {
		http.Error(w, "Failed to create listing: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Broadcast to network
	go h.node.broadcastListing(listing)

	// Return success
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"id":      listing.ID,
		"title":   listing.Title,
		"message": "Listing created and broadcast to network",
	})
}

func (h *Homepage) handleMyListings(w http.ResponseWriter, r *http.Request) {
	var listings []map[string]interface{}

	if h.node != nil {
		for _, l := range h.node.marketplace.GetMyListings() {
			listings = append(listings, map[string]interface{}{
				"id":           l.ID,
				"title":        l.Title,
				"description":  l.Description,
				"price":        l.Price,
				"currency":     l.Currency,
				"image":        l.Image,
				"seller_id":    l.SellerID,
				"seller_name":  l.SellerName,
				"category":     l.Category,
				"rating":       l.Rating,
				"review_count": l.ReviewCount,
				"views":        l.Views,
				"created_at":   l.CreatedAt.Format(time.RFC3339),
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(listings)
}

// =====================
// ESCROW API HANDLERS
// =====================

func (h *Homepage) handleEscrowCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.node == nil {
		http.Error(w, "Node not available", http.StatusServiceUnavailable)
		return
	}

	// Parse parameters
	listingID := r.FormValue("listing_id")
	currency := r.FormValue("currency") // XMR or ZEC
	buyerAddress := r.FormValue("buyer_address")
	deliveryInfo := r.FormValue("delivery_info")

	if listingID == "" || currency == "" {
		http.Error(w, "Missing required fields", http.StatusBadRequest)
		return
	}

	// Get listing details
	listing, err := h.node.marketplace.GetListing(listingID)
	if err != nil {
		http.Error(w, "Listing not found", http.StatusNotFound)
		return
	}

	// Validate currency
	cryptoType := escrow.CryptoType(currency)
	if cryptoType != escrow.CryptoXMR && cryptoType != escrow.CryptoZEC {
		http.Error(w, "Invalid currency. Use XMR or ZEC", http.StatusBadRequest)
		return
	}

	// Convert price to crypto amount (simplified - in production, use real exchange rates)
	var cryptoAmount float64
	rate, _ := escrow.GetExchangeRate(cryptoType)
	if rate > 0 {
		cryptoAmount = float64(listing.Price) / rate // Assuming price is in USD-equivalent
	} else {
		cryptoAmount = float64(listing.Price) / 100 // Fallback
	}

	// Get seller's address (in production, this would be stored with the listing)
	sellerAddress := ""

	// Create escrow
	params := escrow.EscrowParams{
		OrderID:       generateOrderID(),
		ListingID:     listingID,
		ListingTitle:  listing.Title,
		BuyerID:       h.node.identity.NodeID(),
		BuyerAddress:  buyerAddress,
		SellerID:      listing.SellerID,
		SellerAddress: sellerAddress,
		Currency:      cryptoType,
		Amount:        cryptoAmount,
		DeliveryInfo:  deliveryInfo,
	}

	esc, err := h.node.escrowMgr.CreateEscrow(params)
	if err != nil {
		http.Error(w, "Failed to create escrow: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Return escrow details with payment address
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":         true,
		"escrow_id":       esc.ID,
		"order_id":        esc.OrderID,
		"payment_address": esc.EscrowAddress,
		"amount":          esc.Amount,
		"currency":        esc.Currency,
		"fee_percent":     esc.FeePercent,
		"fee_amount":      esc.FeeAmount,
		"expires_at":      esc.ExpiresAt.Format(time.RFC3339),
		"message":         fmt.Sprintf("Send %.8f %s to the payment address", esc.Amount, esc.Currency),
	})
}

func (h *Homepage) handleEscrowStatus(w http.ResponseWriter, r *http.Request) {
	escrowID := r.URL.Query().Get("id")
	orderID := r.URL.Query().Get("order_id")

	if h.node == nil {
		http.Error(w, "Node not available", http.StatusServiceUnavailable)
		return
	}

	var esc *escrow.Escrow
	var err error

	if escrowID != "" {
		esc, err = h.node.escrowMgr.GetEscrow(escrowID)
	} else if orderID != "" {
		esc, err = h.node.escrowMgr.GetEscrowByOrder(orderID)
	} else {
		http.Error(w, "Escrow ID or Order ID required", http.StatusBadRequest)
		return
	}

	if err != nil {
		http.Error(w, "Escrow not found", http.StatusNotFound)
		return
	}

	// Check for payment if still in created state
	if esc.State == escrow.StateCreated {
		h.node.escrowMgr.CheckPayment(esc.ID)
		// Refresh escrow data
		esc, _ = h.node.escrowMgr.GetEscrow(esc.ID)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"escrow_id":       esc.ID,
		"order_id":        esc.OrderID,
		"listing_id":      esc.ListingID,
		"listing_title":   esc.ListingTitle,
		"buyer_id":        esc.BuyerID,
		"seller_id":       esc.SellerID,
		"currency":        esc.Currency,
		"amount":          esc.Amount,
		"payment_address": esc.EscrowAddress,
		"tx_id":           esc.TxID,
		"state":           esc.State,
		"created_at":      esc.CreatedAt.Format(time.RFC3339),
		"funded_at":       esc.FundedAt.Format(time.RFC3339),
		"shipped_at":      esc.ShippedAt.Format(time.RFC3339),
		"expires_at":      esc.ExpiresAt.Format(time.RFC3339),
		"auto_release_at": esc.AutoReleaseAt.Format(time.RFC3339),
		"tracking_info":   esc.TrackingInfo,
		"messages":        esc.Messages,
	})
}

func (h *Homepage) handleEscrowPay(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	escrowID := r.FormValue("escrow_id")

	if h.node == nil || escrowID == "" {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// Check if payment was received on the blockchain
	received, err := h.node.escrowMgr.CheckPayment(escrowID)
	if err != nil {
		http.Error(w, "Failed to check payment: "+err.Error(), http.StatusBadRequest)
		return
	}

	if !received {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": "Payment not yet received. Please send the exact amount to the escrow address and wait for confirmations.",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Payment received and confirmed. Funds are now in escrow.",
	})
}

func (h *Homepage) handleEscrowShip(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	escrowID := r.FormValue("escrow_id")
	trackingInfo := r.FormValue("tracking_info")

	if h.node == nil || escrowID == "" {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	err := h.node.escrowMgr.MarkShipped(escrowID, trackingInfo)
	if err != nil {
		http.Error(w, "Failed to mark shipped: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Order marked as shipped. Buyer can now confirm receipt.",
	})
}

func (h *Homepage) handleEscrowRelease(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	escrowID := r.FormValue("escrow_id")

	if h.node == nil || escrowID == "" {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	err := h.node.escrowMgr.Release(escrowID)
	if err != nil {
		http.Error(w, "Failed to release: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Funds released to seller. Transaction complete!",
	})
}

func (h *Homepage) handleEscrowDispute(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	escrowID := r.FormValue("escrow_id")
	reason := r.FormValue("reason")

	if h.node == nil || escrowID == "" || reason == "" {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	err := h.node.escrowMgr.OpenDispute(escrowID, reason)
	if err != nil {
		http.Error(w, "Failed to open dispute: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Dispute opened. Funds are frozen until resolution.",
	})
}

func (h *Homepage) handleMyEscrows(w http.ResponseWriter, r *http.Request) {
	if h.node == nil {
		http.Error(w, "Node not available", http.StatusServiceUnavailable)
		return
	}

	role := r.URL.Query().Get("role") // "buyer" or "seller"

	var escrows []*escrow.Escrow
	if role == "seller" {
		escrows = h.node.escrowMgr.GetSellerEscrows(h.node.identity.NodeID())
	} else {
		escrows = h.node.escrowMgr.GetBuyerEscrows(h.node.identity.NodeID())
	}

	var results []map[string]interface{}
	for _, e := range escrows {
		results = append(results, map[string]interface{}{
			"escrow_id":     e.ID,
			"order_id":      e.OrderID,
			"listing_id":    e.ListingID,
			"listing_title": e.ListingTitle,
			"currency":      e.Currency,
			"amount":        e.Amount,
			"state":         e.State,
			"created_at":    e.CreatedAt.Format(time.RFC3339),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func generateOrderID() string {
	b := make([]byte, 16)
	crand.Read(b)
	return hex.EncodeToString(b)
}

func (h *Homepage) handleSeller(w http.ResponseWriter, r *http.Request) {
	sellerID := r.URL.Query().Get("id")

	if h.node == nil || sellerID == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{})
		return
	}

	profile, err := h.node.marketplace.GetProfile(sellerID)
	if err != nil {
		// Create a basic profile from listings if not found
		listings := h.node.marketplace.GetSellerListings(sellerID)
		if len(listings) > 0 {
			profile = &market.SellerProfile{
				ID:       sellerID,
				Name:     listings[0].SellerName,
				JoinedAt: listings[0].CreatedAt,
			}
		} else {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{})
			return
		}
	}

	result := map[string]interface{}{
		"id":             profile.ID,
		"name":           profile.Name,
		"bio":            profile.Bio,
		"avatar":         profile.Avatar,
		"joined_at":      profile.JoinedAt.Format(time.RFC3339),
		"last_seen":      profile.LastSeen.Format(time.RFC3339),
		"verified":       profile.Verified,
		"trusted_seller": profile.TrustedSeller,
		"total_sales":    profile.TotalSales,
		"total_orders":   profile.TotalOrders,
		"rating":         profile.Rating,
		"review_count":   profile.ReviewCount,
		"response_time":  profile.ResponseTime,
		"ship_time":      profile.ShipTime,
		"dispute_rate":   profile.DisputeRate,
		"badges":         profile.Badges,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (h *Homepage) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		room := r.FormValue("room")
		message := r.FormValue("message")
		mediaType := r.FormValue("media_type")
		mediaName := r.FormValue("media_name")
		mediaData := r.FormValue("media_data")

		if h.node != nil && room != "" && (message != "" || mediaData != "") {
			var media *chat.MediaAttachment
			if mediaData != "" {
				media = &chat.MediaAttachment{
					Type: mediaType,
					Name: mediaName,
					Data: mediaData,
				}
			}
			// Send locally
			chatMsg, _ := h.node.chatMgr.SendMessageWithMedia(room, message, media)

			// Broadcast with gossip routing (propagates network-wide)
			if chatMsg != nil {
				payload, _ := chatMsg.Marshal()
				h.node.broadcastGossip(MsgTypeChat, payload)
			}
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	room := r.URL.Query().Get("room")
	if room == "" {
		room = "general"
	}

	var messages []map[string]interface{}
	if h.node != nil {
		for _, m := range h.node.chatMgr.GetMessages(room, 100) {
			msg := map[string]interface{}{
				"id":        m.ID,
				"type":      m.Type,
				"room":      m.Room,
				"sender_id": m.SenderID,
				"nick":      m.Nick,
				"content":   m.Content,
				"timestamp": m.Timestamp.Format(time.RFC3339),
				"reactions": m.Reactions,
			}
			if m.Media != nil {
				msg["media"] = m.Media
			}
			messages = append(messages, msg)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)
}

func (h *Homepage) handleChatRooms(w http.ResponseWriter, r *http.Request) {
	var rooms []map[string]interface{}
	if h.node != nil {
		for _, room := range h.node.chatMgr.ListRooms() {
			rooms = append(rooms, map[string]interface{}{
				"name":        room.Name,
				"description": room.Description,
				"private":     room.Private,
				"members":     len(room.Members),
			})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rooms)
}

func (h *Homepage) handleChatProfile(w http.ResponseWriter, r *http.Request) {
	if h.node == nil {
		http.Error(w, "Node not available", http.StatusServiceUnavailable)
		return
	}

	if r.Method == "POST" {
		nick := r.FormValue("nick")
		status := r.FormValue("status")
		statusMsg := r.FormValue("status_msg")
		bio := r.FormValue("bio")
		avatar := r.FormValue("avatar")
		h.node.chatMgr.UpdateProfile(nick, status, statusMsg, bio, avatar)
		w.WriteHeader(http.StatusOK)
		return
	}

	user := h.node.chatMgr.GetLocalUser()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}

func (h *Homepage) handleChatConversations(w http.ResponseWriter, r *http.Request) {
	var convs []map[string]interface{}
	if h.node != nil {
		for _, conv := range h.node.chatMgr.GetConversations() {
			convs = append(convs, map[string]interface{}{
				"id":           conv.ID,
				"participants": conv.Participants,
				"last_message": conv.LastMessage.Format(time.RFC3339),
				"unread":       conv.Unread,
			})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(convs)
}

func (h *Homepage) handleChatDM(w http.ResponseWriter, r *http.Request) {
	if h.node == nil {
		http.Error(w, "Node not available", http.StatusServiceUnavailable)
		return
	}

	if r.Method == "POST" {
		userID := r.FormValue("user")
		message := r.FormValue("message")
		mediaType := r.FormValue("media_type")
		mediaName := r.FormValue("media_name")
		mediaData := r.FormValue("media_data")

		if userID != "" && (message != "" || mediaData != "") {
			var media *chat.MediaAttachment
			if mediaData != "" {
				media = &chat.MediaAttachment{
					Type: mediaType,
					Name: mediaName,
					Data: mediaData,
				}
			}
			chatMsg, err := h.node.chatMgr.SendDM(userID, message, media)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			// Send E2E encrypted DM over the network
			h.node.sendE2EEncryptedDM(userID, chatMsg)
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	userID := r.URL.Query().Get("user")
	var messages []map[string]interface{}
	if userID != "" {
		for _, m := range h.node.chatMgr.GetDMMessages(userID, 100) {
			msg := map[string]interface{}{
				"id":          m.ID,
				"type":        m.Type,
				"sender_id":   m.SenderID,
				"receiver_id": m.ReceiverID,
				"nick":        m.Nick,
				"content":     m.Content,
				"timestamp":   m.Timestamp.Format(time.RFC3339),
			}
			if m.Media != nil {
				msg["media"] = m.Media
			}
			messages = append(messages, msg)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)
}

func (h *Homepage) handleChatUsers(w http.ResponseWriter, r *http.Request) {
	room := r.URL.Query().Get("room")
	var users []map[string]interface{}

	if h.node != nil {
		var userList []*chat.User
		if room != "" {
			userList = h.node.chatMgr.GetOnlineUsers(room)
		} else {
			userList = h.node.chatMgr.GetUsers()
		}
		for _, u := range userList {
			users = append(users, map[string]interface{}{
				"id":            u.ID,
				"nick":          u.Nick,
				"status":        u.Status,
				"status_msg":    u.StatusMsg,
				"last_seen":     u.LastSeen.Format(time.RFC3339),
				"messages_sent": u.MessagesSent,
			})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(users)
}

func (h *Homepage) handleChatUser(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("id")
	if userID == "" || h.node == nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	user := h.node.chatMgr.GetUser(userID)
	if user == nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}

func (h *Homepage) handleChatSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	var users []map[string]interface{}

	if h.node != nil && query != "" {
		for _, u := range h.node.chatMgr.SearchUsers(query) {
			users = append(users, map[string]interface{}{
				"id":     u.ID,
				"nick":   u.Nick,
				"status": u.Status,
			})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(users)
}

func (h *Homepage) handleChatReaction(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	room := r.FormValue("room")
	messageID := r.FormValue("message_id")
	emoji := r.FormValue("emoji")

	if h.node != nil && room != "" && messageID != "" && emoji != "" {
		err := h.node.chatMgr.AddReaction(room, messageID, emoji)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Homepage) handleChatBlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID := r.FormValue("user")
	if h.node != nil && userID != "" {
		h.node.chatMgr.BlockUser(userID)
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Homepage) handleDomains(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	var domains []map[string]string

	if h.node != nil {
		var results []*names.Registration
		if query != "" {
			results = h.node.nameService.Search(query)
		} else {
			results = h.node.nameService.Search("")
		}

		for _, d := range results {
			domains = append(domains, map[string]string{
				"name":        d.Name,
				"full_name":   d.Name + ".kyk",
				"description": d.Description,
				"owner":       d.NodeID,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(domains)
}

func (h *Homepage) handlePeers(w http.ResponseWriter, r *http.Request) {
	var peers []map[string]string

	if h.node != nil {
		h.node.mu.RLock()
		for id, p := range h.node.connections {
			peers = append(peers, map[string]string{
				"id":        id,
				"name":      p.NodeID[:16] + "...",
				"last_seen": p.LastSeen.Format("15:04:05"),
			})
		}
		h.node.mu.RUnlock()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(peers)
}

func (h *Homepage) handleStats(w http.ResponseWriter, r *http.Request) {
	stats := map[string]interface{}{
		"peers":       0,
		"listings":    0,
		"domains":     0,
		"messages":    0,
		"anonymous":   false,
		"relay_count": 0,
	}

	if h.node != nil {
		h.node.mu.RLock()
		stats["peers"] = len(h.node.connections)
		h.node.mu.RUnlock()
		stats["listings"] = h.node.marketplace.ListingCount()
		stats["marketplace_listings"], stats["marketplace_categories"] = h.node.marketplace.Stats()
		stats["domains"] = len(h.node.nameService.Search(""))
		stats["relay_count"] = h.node.onionRouter.GetRelayCount()
		stats["anonymous"] = h.node.onionRouter.CanRoute()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func (h *Homepage) handleMarketplace(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(marketplaceHTML))
}

func (h *Homepage) handleChatPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(chatPageHTML))
}

func (h *Homepage) handleDomainsPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(domainsPageHTML))
}

func (h *Homepage) handleNetworkPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(networkPageHTML))
}

// ============================================================================
// Homepage HTML Templates
// ============================================================================

const homepageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>KayakNet // ANONYMOUS NETWORK</title>
    <style>
        @import url('https://fonts.googleapis.com/css2?family=VT323&family=Share+Tech+Mono&display=swap');
        * { margin: 0; padding: 0; box-sizing: border-box; }
        :root {
            --bg: #000000;
            --bg-term: #0a0a0a;
            --green: #00ff00;
            --green-dim: #00aa00;
            --green-glow: #00ff0066;
            --amber: #ffaa00;
            --red: #ff0000;
            --border: #00ff0033;
        }
        body {
            font-family: 'VT323', 'Share Tech Mono', monospace;
            background: var(--bg);
            color: var(--green);
            min-height: 100vh;
            position: relative;
            overflow-x: hidden;
        }
        body::before {
            content: "";
            position: fixed;
            top: 0; left: 0; right: 0; bottom: 0;
            background: repeating-linear-gradient(0deg, rgba(0,0,0,0.15) 0px, rgba(0,0,0,0.15) 1px, transparent 1px, transparent 2px);
            pointer-events: none;
            z-index: 1000;
        }
        body::after {
            content: "";
            position: fixed;
            top: 0; left: 0; right: 0; bottom: 0;
            background: radial-gradient(ellipse at center, transparent 0%, rgba(0,0,0,0.4) 100%);
            pointer-events: none;
            z-index: 999;
        }
        .container { max-width: 1000px; margin: 0 auto; padding: 20px; position: relative; z-index: 1; }
        .terminal-window {
            border: 1px solid var(--green);
            background: var(--bg-term);
            box-shadow: 0 0 20px var(--green-glow), inset 0 0 20px rgba(0,255,0,0.03);
        }
        .terminal-header {
            background: var(--green);
            color: var(--bg);
            padding: 5px 15px;
            font-size: 14px;
            display: flex;
            justify-content: space-between;
        }
        .terminal-body { padding: 20px; }
        header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            padding: 15px 0;
            border-bottom: 1px dashed var(--border);
            margin-bottom: 30px;
        }
        .logo {
            font-size: 28px;
            color: var(--green);
            text-decoration: none;
            text-shadow: 0 0 10px var(--green-glow);
            letter-spacing: 2px;
        }
        .logo::before { content: "["; color: var(--green-dim); }
        .logo::after { content: "]"; color: var(--green-dim); }
        nav { display: flex; gap: 20px; }
        nav a {
            color: var(--green-dim);
            text-decoration: none;
            font-size: 16px;
            padding: 5px 10px;
            border: 1px solid transparent;
            transition: all 0.2s;
        }
        nav a:hover, nav a.active {
            color: var(--green);
            border-color: var(--green);
            text-shadow: 0 0 5px var(--green-glow);
        }
        .status {
            color: var(--green);
            animation: blink 1s infinite;
        }
        @keyframes blink { 0%, 100% { opacity: 1; } 50% { opacity: 0.5; } }
        .ascii-art {
            text-align: center;
            padding: 30px 0;
            font-size: 12px;
            line-height: 1.2;
            color: var(--green-dim);
            white-space: pre;
            text-shadow: 0 0 5px var(--green-glow);
        }
        .hero { text-align: center; padding: 20px 0; }
        .hero h1 {
            font-size: 36px;
            color: var(--green);
            text-shadow: 0 0 20px var(--green-glow);
            letter-spacing: 3px;
            margin-bottom: 10px;
        }
        .hero p { color: var(--green-dim); font-size: 18px; margin-bottom: 30px; }
        .search-box {
            max-width: 500px;
            margin: 0 auto;
            display: flex;
            gap: 10px;
        }
        .search-box input {
            flex: 1;
            padding: 12px 15px;
            font-size: 16px;
            background: var(--bg);
            border: 1px solid var(--green);
            color: var(--green);
            font-family: inherit;
        }
        .search-box input:focus {
            outline: none;
            box-shadow: 0 0 10px var(--green-glow);
        }
        .search-box input::placeholder { color: var(--green-dim); }
        .search-box button, .btn {
            padding: 12px 20px;
            background: var(--green);
            color: var(--bg);
            border: none;
            cursor: pointer;
            font-family: inherit;
            font-size: 16px;
            text-transform: uppercase;
        }
        .search-box button:hover, .btn:hover {
            box-shadow: 0 0 15px var(--green-glow);
        }
        .stats-bar {
            display: grid;
            grid-template-columns: repeat(4, 1fr);
            gap: 15px;
            margin: 30px 0;
            padding: 15px;
            border: 1px dashed var(--border);
        }
        .stat { text-align: center; padding: 10px; }
        .stat-value {
            font-size: 32px;
            color: var(--green);
            text-shadow: 0 0 10px var(--green-glow);
        }
        .stat-label { font-size: 12px; color: var(--green-dim); margin-top: 5px; text-transform: uppercase; }
        .features {
            display: grid;
            grid-template-columns: repeat(2, 1fr);
            gap: 15px;
            margin: 30px 0;
        }
        .feature-card {
            border: 1px solid var(--border);
            padding: 20px;
            text-decoration: none;
            color: inherit;
            transition: all 0.2s;
            background: var(--bg);
        }
        .feature-card:hover {
            border-color: var(--green);
            box-shadow: 0 0 15px var(--green-glow);
        }
        .feature-card h3 {
            color: var(--green);
            font-size: 18px;
            margin-bottom: 10px;
        }
        .feature-card h3::before { content: "> "; color: var(--green-dim); }
        .feature-card p { color: var(--green-dim); font-size: 14px; line-height: 1.5; }
        footer {
            text-align: center;
            padding: 30px 0;
            border-top: 1px dashed var(--border);
            margin-top: 30px;
            color: var(--green-dim);
            font-size: 14px;
        }
        .prompt::before { content: "root@kayaknet:~$ "; color: var(--green-dim); }
        .cursor { animation: cursor-blink 1s infinite; }
        @keyframes cursor-blink { 0%, 100% { opacity: 1; } 50% { opacity: 0; } }
    </style>
</head>
<body>
    <div class="container">
        <div class="terminal-window">
            <div class="terminal-header">
                <span>KAYAKNET TERMINAL v1.0</span>
                <span class="status">[CONNECTED]</span>
            </div>
            <div class="terminal-body">
                <header>
                    <a href="/" class="logo">KAYAKNET</a>
                    <nav>
                        <a href="/marketplace">/market</a>
                        <a href="/chat">/chat</a>
                        <a href="/domains">/domains</a>
                        <a href="/network">/network</a>
                    </nav>
                </header>
                
                <div class="ascii-art">
 _  __    _    _   _    _    _  ___   _ _____ _____
| |/ /   / \  | \ | |  / \  | |/ / \ | | ____|_   _|
| ' /   / _ \ |  \| | / _ \ | ' /|  \| |  _|   | |
| . \  / ___ \| |\  |/ ___ \| . \| |\  | |___  | |
|_|\_\/_/   \_\_| \_/_/   \_\_|\_\_| \_|_____| |_|
        ANONYMOUS P2P NETWORK // SECURE // FREE
                </div>
                
                <section class="hero">
                    <h1>> WELCOME_</h1>
                    <p>[ Anonymous peer-to-peer network. No servers. No tracking. No censorship. ]</p>
                    <div class="search-box">
                        <input type="text" id="search" placeholder="search://query..." />
                        <button onclick="search()">EXEC</button>
                    </div>
                </section>
                
                <div class="stats-bar">
                    <div class="stat">
                        <div class="stat-value" id="stat-peers">0</div>
                        <div class="stat-label">PEERS</div>
                    </div>
                    <div class="stat">
                        <div class="stat-value" id="stat-listings">0</div>
                        <div class="stat-label">LISTINGS</div>
                    </div>
                    <div class="stat">
                        <div class="stat-value" id="stat-domains">0</div>
                        <div class="stat-label">DOMAINS</div>
                    </div>
                    <div class="stat">
                        <div class="stat-value" id="stat-anonymous">--</div>
                        <div class="stat-label">ANON_STATUS</div>
                    </div>
                </div>
                
                <section class="features">
                    <a href="/marketplace" class="feature-card">
                        <h3>MARKETPLACE</h3>
                        <p>Anonymous commerce. Buy and sell without identity. Peer-to-peer transactions only.</p>
                    </a>
                    <a href="/chat" class="feature-card">
                        <h3>SECURE_CHAT</h3>
                        <p>Onion-routed messaging. End-to-end encrypted. No logs. No traces.</p>
                    </a>
                    <a href="/domains" class="feature-card">
                        <h3>.KYK_DOMAINS</h3>
                        <p>Register anonymous domains. Only resolvable inside KayakNet. Host hidden services.</p>
                    </a>
                    <a href="/network" class="feature-card">
                        <h3>NET_STATUS</h3>
                        <p>Monitor peers, relays, anonymity metrics. Real-time network health.</p>
                    </a>
                </section>
                
                <footer>
                    <p class="prompt">system ready<span class="cursor">_</span></p>
                    <p style="margin-top: 10px;">[ KAYAKNET // ANONYMOUS // DECENTRALIZED // FREE ]</p>
                </footer>
            </div>
        </div>
    </div>
    
    <script>
        async function loadStats() {
            try {
                const res = await fetch('/api/stats');
                const stats = await res.json();
                document.getElementById('stat-peers').textContent = stats.peers || 0;
                document.getElementById('stat-listings').textContent = stats.listings || 0;
                document.getElementById('stat-domains').textContent = stats.domains || 0;
                document.getElementById('stat-anonymous').textContent = stats.anonymous ? 'ACTIVE' : 'BUILD';
            } catch(e) {}
        }
        function search() {
            const query = document.getElementById('search').value;
            if (query) window.location.href = '/marketplace?q=' + encodeURIComponent(query);
        }
        document.getElementById('search').addEventListener('keypress', (e) => { if (e.key === 'Enter') search(); });
        loadStats();
        setInterval(loadStats, 10000);
    </script>
</body>
</html>`

const marketplaceHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>MARKET // KAYAKNET</title>
    <style>
        @import url('https://fonts.googleapis.com/css2?family=VT323&display=swap');
        * { margin: 0; padding: 0; box-sizing: border-box; }
        :root { --bg: #000; --bg2: #0a0a0a; --green: #00ff00; --green-dim: #00aa00; --green-glow: #00ff0066; --border: #00ff0033; --amber: #ffaa00; --red: #ff3333; --blue: #33aaff; }
        body { font-family: 'VT323', monospace; background: var(--bg); color: var(--green); min-height: 100vh; }
        body::before { content: ""; position: fixed; top: 0; left: 0; right: 0; bottom: 0; background: repeating-linear-gradient(0deg, rgba(0,0,0,0.15) 0px, rgba(0,0,0,0.15) 1px, transparent 1px, transparent 2px); pointer-events: none; z-index: 1000; }
        .container { max-width: 1400px; margin: 0 auto; padding: 20px; }
        .terminal { border: 1px solid var(--green); background: var(--bg2); box-shadow: 0 0 20px var(--green-glow); }
        .term-header { background: var(--green); color: var(--bg); padding: 5px 15px; font-size: 14px; display: flex; justify-content: space-between; }
        .term-body { padding: 20px; }
        header { display: flex; justify-content: space-between; align-items: center; padding: 15px 0; border-bottom: 1px dashed var(--border); margin-bottom: 20px; }
        .logo { font-size: 24px; color: var(--green); text-decoration: none; text-shadow: 0 0 10px var(--green-glow); }
        .logo::before { content: "["; } .logo::after { content: "]"; }
        nav { display: flex; gap: 15px; align-items: center; }
        nav a { color: var(--green-dim); text-decoration: none; padding: 5px 10px; border: 1px solid transparent; }
        nav a:hover, nav a.active { color: var(--green); border-color: var(--green); }
        .nav-badge { background: var(--red); color: #fff; padding: 2px 6px; font-size: 12px; margin-left: 5px; }
        h1, h2, h3 { margin-bottom: 15px; }
        h1::before, h2::before { content: "> "; color: var(--green-dim); }
        .tabs { display: flex; gap: 5px; margin-bottom: 20px; border-bottom: 1px solid var(--border); padding-bottom: 10px; }
        .tab { padding: 10px 20px; background: transparent; border: 1px solid var(--border); color: var(--green-dim); cursor: pointer; font-family: inherit; font-size: 16px; }
        .tab:hover, .tab.active { background: var(--border); color: var(--green); border-color: var(--green); }
        .toolbar { display: flex; gap: 10px; margin-bottom: 20px; flex-wrap: wrap; align-items: center; }
        input, select, textarea { padding: 10px; background: var(--bg); border: 1px solid var(--green); color: var(--green); font-family: inherit; font-size: 16px; }
        input:focus, select:focus, textarea:focus { outline: none; box-shadow: 0 0 10px var(--green-glow); }
        input::placeholder, textarea::placeholder { color: var(--green-dim); }
        textarea { resize: vertical; min-height: 80px; }
        .btn { padding: 10px 20px; background: var(--green); color: var(--bg); border: none; cursor: pointer; font-family: inherit; font-size: 16px; transition: all 0.2s; }
        .btn:hover { box-shadow: 0 0 15px var(--green-glow); }
        .btn-outline { background: transparent; border: 1px solid var(--green); color: var(--green); }
        .btn-danger { background: var(--red); }
        .btn-sm { padding: 5px 10px; font-size: 14px; }
        .stats { display: flex; gap: 20px; margin-bottom: 20px; font-size: 14px; color: var(--green-dim); flex-wrap: wrap; }
        .stat-box { border: 1px solid var(--border); padding: 10px 15px; }
        .stat-box .value { font-size: 24px; color: var(--green); }
        .listings { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 20px; }
        .listing { border: 1px solid var(--border); background: var(--bg); transition: all 0.3s; overflow: hidden; cursor: pointer; position: relative; }
        .listing:hover { border-color: var(--green); box-shadow: 0 0 20px var(--green-glow); transform: translateY(-2px); }
        .listing-img { width: 100%; height: 180px; object-fit: cover; border-bottom: 1px solid var(--border); filter: grayscale(30%) brightness(0.9); }
        .listing:hover .listing-img { filter: grayscale(0%) brightness(1); }
        .listing-content { padding: 15px; }
        .listing h3 { color: var(--green); margin-bottom: 8px; font-size: 18px; }
        .listing .price { color: var(--amber); font-size: 24px; margin: 10px 0; text-shadow: 0 0 10px rgba(255,170,0,0.5); }
        .listing .price.free { color: var(--green); }
        .listing .desc { color: var(--green-dim); font-size: 14px; margin-bottom: 12px; line-height: 1.4; max-height: 60px; overflow: hidden; }
        .listing .meta { display: flex; justify-content: space-between; align-items: center; border-top: 1px dashed var(--border); padding-top: 10px; margin-top: 10px; font-size: 14px; }
        .listing .seller { color: var(--green); cursor: pointer; }
        .listing .seller:hover { text-decoration: underline; }
        .listing .seller::before { content: "@"; color: var(--green-dim); }
        .listing .rating { color: var(--amber); }
        .listing .category { background: var(--border); color: var(--green); padding: 2px 8px; font-size: 12px; display: inline-block; margin-bottom: 8px; }
        .listing .fav-btn { position: absolute; top: 10px; right: 10px; background: rgba(0,0,0,0.7); border: 1px solid var(--green); color: var(--green); width: 30px; height: 30px; cursor: pointer; font-size: 16px; }
        .listing .fav-btn:hover, .listing .fav-btn.active { background: var(--green); color: var(--bg); }
        .badge { display: inline-block; padding: 2px 8px; font-size: 12px; margin-right: 5px; }
        .badge-trusted { background: var(--amber); color: var(--bg); }
        .badge-verified { background: var(--green); color: var(--bg); }
        .badge-new { background: var(--blue); color: var(--bg); }
        /* Modal */
        .modal { display: none; position: fixed; top: 0; left: 0; right: 0; bottom: 0; background: rgba(0,0,0,0.9); z-index: 2000; overflow-y: auto; padding: 20px; }
        .modal.active { display: flex; justify-content: center; align-items: flex-start; }
        .modal-content { background: var(--bg2); border: 1px solid var(--green); max-width: 900px; width: 100%; margin: 20px auto; box-shadow: 0 0 30px var(--green-glow); }
        .modal-header { background: var(--green); color: var(--bg); padding: 10px 20px; display: flex; justify-content: space-between; align-items: center; }
        .modal-close { background: none; border: none; color: var(--bg); font-size: 24px; cursor: pointer; }
        .modal-body { padding: 20px; }
        .detail-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 20px; }
        .detail-img { width: 100%; height: 300px; object-fit: cover; border: 1px solid var(--border); }
        .detail-info h2 { font-size: 28px; margin-bottom: 10px; }
        .detail-price { font-size: 36px; color: var(--amber); margin: 15px 0; }
        .detail-seller { padding: 15px; border: 1px solid var(--border); margin: 15px 0; }
        .detail-seller-name { font-size: 20px; color: var(--green); cursor: pointer; }
        .detail-seller-stats { display: flex; gap: 20px; margin-top: 10px; font-size: 14px; color: var(--green-dim); }
        .detail-actions { display: flex; gap: 10px; margin-top: 20px; }
        .reviews-section { margin-top: 30px; border-top: 1px solid var(--border); padding-top: 20px; }
        .review { border: 1px solid var(--border); padding: 15px; margin-bottom: 10px; }
        .review-header { display: flex; justify-content: space-between; margin-bottom: 10px; }
        .review-user { color: var(--green); }
        .review-rating { color: var(--amber); }
        .review-date { color: var(--green-dim); font-size: 12px; }
        .review-text { color: var(--green-dim); line-height: 1.5; }
        .review-reply { background: var(--border); padding: 10px; margin-top: 10px; border-left: 2px solid var(--green); }
        .stars { color: var(--amber); letter-spacing: 2px; }
        /* Orders */
        .order { border: 1px solid var(--border); padding: 15px; margin-bottom: 15px; }
        .order-header { display: flex; justify-content: space-between; margin-bottom: 10px; }
        .order-status { padding: 3px 10px; font-size: 14px; }
        .order-status.pending { background: var(--amber); color: var(--bg); }
        .order-status.completed { background: var(--green); color: var(--bg); }
        .order-status.disputed { background: var(--red); color: #fff; }
        /* Messages */
        .conversation { border: 1px solid var(--border); padding: 15px; margin-bottom: 10px; cursor: pointer; }
        .conversation:hover { border-color: var(--green); }
        .conversation-header { display: flex; justify-content: space-between; }
        .conversation .unread { background: var(--red); color: #fff; padding: 2px 8px; font-size: 12px; }
        .message { padding: 10px; margin: 5px 0; max-width: 70%; }
        .message.sent { background: var(--green); color: var(--bg); margin-left: auto; }
        .message.received { background: var(--border); }
        .message-time { font-size: 10px; opacity: 0.7; margin-top: 5px; }
        @media (max-width: 768px) {
            .detail-grid { grid-template-columns: 1fr; }
            .listings { grid-template-columns: 1fr; }
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="terminal">
            <div class="term-header">
                <span>KAYAKNET MARKETPLACE // ANONYMOUS P2P COMMERCE</span>
                <span id="unread-badge"></span>
            </div>
            <div class="term-body">
                <header>
                    <a href="/" class="logo">KAYAKNET</a>
                    <nav>
                        <a href="/marketplace" class="active">/market</a>
                        <a href="#" onclick="showTab('messages')">/messages<span id="msg-count"></span></a>
                        <a href="#" onclick="showTab('orders')">/orders</a>
                        <a href="#" onclick="showTab('favorites')">/saved</a>
                        <a href="/chat">/chat</a>
                        <a href="/network">/network</a>
                    </nav>
                </header>
                
                <div class="tabs">
                    <button class="tab active" onclick="showTab('browse')">BROWSE</button>
                    <button class="tab" onclick="showTab('my-listings')">MY_LISTINGS</button>
                    <button class="tab" onclick="showTab('orders')">ORDERS</button>
                    <button class="tab" onclick="showTab('messages')">MESSAGES</button>
                    <button class="tab" onclick="showTab('favorites')">FAVORITES</button>
                </div>
                
                <!-- Browse Tab -->
                <div id="tab-browse" class="tab-content">
                    <div class="stats" id="stats">LOADING...</div>
                    <div class="toolbar">
                        <input type="text" id="search" placeholder="SEARCH://..." style="flex: 1; min-width: 200px;" />
                        <select id="category">
                            <option value="">ALL_CATEGORIES</option>
                            <option value="software">SOFTWARE</option>
                            <option value="services">SERVICES</option>
                            <option value="consulting">CONSULTING</option>
                            <option value="development">DEVELOPMENT</option>
                            <option value="education">EDUCATION</option>
                            <option value="hardware">HARDWARE</option>
                        </select>
                        <select id="sort">
                            <option value="newest">NEWEST</option>
                            <option value="price_asc">PRICE: LOW-HIGH</option>
                            <option value="price_desc">PRICE: HIGH-LOW</option>
                            <option value="rating">TOP RATED</option>
                            <option value="popular">MOST POPULAR</option>
                        </select>
                        <button class="btn" onclick="showCreateListing()">+ NEW LISTING</button>
                    </div>
                    <div class="listings" id="listings"></div>
                </div>
                
                <!-- My Listings Tab -->
                <div id="tab-my-listings" class="tab-content" style="display:none;">
                    <h2>MY_LISTINGS</h2>
                    <div id="my-listings"></div>
                </div>
                
                <!-- Orders Tab -->
                <div id="tab-orders" class="tab-content" style="display:none;">
                    <h2>ORDERS</h2>
                    <div class="tabs">
                        <button class="tab active" onclick="loadOrders('buying')">PURCHASES</button>
                        <button class="tab" onclick="loadOrders('selling')">SALES</button>
                    </div>
                    <div id="orders-list"></div>
                </div>
                
                <!-- Messages Tab -->
                <div id="tab-messages" class="tab-content" style="display:none;">
                    <h2>MESSAGES</h2>
                    <div id="conversations-list"></div>
                </div>
                
                <!-- Favorites Tab -->
                <div id="tab-favorites" class="tab-content" style="display:none;">
                    <h2>SAVED_LISTINGS</h2>
                    <div class="listings" id="favorites-list"></div>
                </div>
            </div>
        </div>
    </div>
    
    <!-- Listing Detail Modal -->
    <div id="listing-modal" class="modal">
        <div class="modal-content">
            <div class="modal-header">
                <span>LISTING_DETAILS</span>
                <button class="modal-close" onclick="closeModal('listing-modal')">&times;</button>
            </div>
            <div class="modal-body" id="listing-detail"></div>
        </div>
    </div>
    
    <!-- Seller Profile Modal -->
    <div id="seller-modal" class="modal">
        <div class="modal-content">
            <div class="modal-header">
                <span>SELLER_PROFILE</span>
                <button class="modal-close" onclick="closeModal('seller-modal')">&times;</button>
            </div>
            <div class="modal-body" id="seller-detail"></div>
        </div>
    </div>
    
    <!-- Order Modal -->
    <div id="order-modal" class="modal">
        <div class="modal-content">
            <div class="modal-header">
                <span>PLACE_ORDER</span>
                <button class="modal-close" onclick="closeModal('order-modal')">&times;</button>
            </div>
            <div class="modal-body" id="order-form"></div>
        </div>
    </div>
    
    <!-- Message Modal -->
    <div id="message-modal" class="modal">
        <div class="modal-content">
            <div class="modal-header">
                <span>CONVERSATION</span>
                <button class="modal-close" onclick="closeModal('message-modal')">&times;</button>
            </div>
            <div class="modal-body" id="message-thread"></div>
        </div>
    </div>
    
    <!-- Create Listing Modal -->
    <div id="create-modal" class="modal">
        <div class="modal-content">
            <div class="modal-header">
                <span>CREATE_LISTING</span>
                <button class="modal-close" onclick="closeModal('create-modal')">&times;</button>
            </div>
            <div class="modal-body">
                <form onsubmit="createListing(event)">
                    <div style="margin-bottom:15px"><label>TITLE:</label><br><input type="text" id="new-title" required style="width:100%"></div>
                    <div style="margin-bottom:15px"><label>CATEGORY:</label><br><select id="new-category" style="width:100%">
                        <option value="software">SOFTWARE</option>
                        <option value="services">SERVICES</option>
                        <option value="consulting">CONSULTING</option>
                        <option value="development">DEVELOPMENT</option>
                        <option value="education">EDUCATION</option>
                        <option value="hardware">HARDWARE</option>
                    </select></div>
                    <div style="margin-bottom:15px;display:flex;gap:10px;">
                        <div style="flex:1"><label>PRICE:</label><br><input type="number" id="new-price" min="0" step="0.0001" required style="width:100%"></div>
                        <div style="width:120px"><label>CURRENCY:</label><br><select id="new-currency" style="width:100%">
                            <option value="XMR">XMR (Monero)</option>
                            <option value="ZEC">ZEC (Zcash)</option>
                        </select></div>
                    </div>
                    <div style="margin-bottom:15px"><label>DESCRIPTION:</label><br><textarea id="new-desc" required style="width:100%"></textarea></div>
                    <div style="margin-bottom:15px"><label>IMAGE URL:</label><br><input type="text" id="new-image" placeholder="https://..." style="width:100%"></div>
                    <button type="submit" class="btn">CREATE LISTING</button>
                </form>
            </div>
        </div>
    </div>
    
    <!-- Review Modal -->
    <div id="review-modal" class="modal">
        <div class="modal-content">
            <div class="modal-header">
                <span>LEAVE_REVIEW</span>
                <button class="modal-close" onclick="closeModal('review-modal')">&times;</button>
            </div>
            <div class="modal-body" id="review-form"></div>
        </div>
    </div>
    
    <script>
        let currentListing = null;
        let favorites = new Set();
        
        function showTab(tab) {
            document.querySelectorAll('.tab-content').forEach(t => t.style.display = 'none');
            document.querySelectorAll('.tabs .tab').forEach(t => t.classList.remove('active'));
            document.getElementById('tab-' + tab).style.display = 'block';
            event?.target?.classList?.add('active');
            
            if (tab === 'orders') loadOrders('buying');
            if (tab === 'messages') loadConversations();
            if (tab === 'favorites') loadFavorites();
            if (tab === 'my-listings') loadMyListings();
        }
        
        function showModal(id) { document.getElementById(id).classList.add('active'); }
        function closeModal(id) { document.getElementById(id).classList.remove('active'); }
        
        async function loadListings() {
            const category = document.getElementById('category').value;
            const search = document.getElementById('search').value;
            const sort = document.getElementById('sort').value;
            const res = await fetch('/api/listings?category=' + category + '&q=' + search + '&sort=' + sort);
            const listings = await res.json();
            const container = document.getElementById('listings');
            
            const statsRes = await fetch('/api/stats');
            const stats = await statsRes.json();
            document.getElementById('stats').innerHTML = 
                '<div class="stat-box"><div class="value">' + (stats.marketplace_listings || 0) + '</div>LISTINGS</div>' +
                '<div class="stat-box"><div class="value">' + (stats.marketplace_categories || 0) + '</div>CATEGORIES</div>' +
                '<div class="stat-box"><div class="value">SECURE</div>ESCROW</div>';
            
            if (!listings || listings.length === 0) {
                container.innerHTML = '<div class="listing"><div class="listing-content"><h3>NO_LISTINGS</h3><p class="desc">No items found.</p></div></div>';
                return;
            }
            container.innerHTML = listings.map(l => renderListing(l)).join('');
        }
        
        function renderListing(l) {
            const priceClass = l.price === 0 ? 'price free' : 'price';
            const priceText = l.price === 0 ? 'FREE' : l.price + ' ' + (l.currency || 'XMR');
            const imgUrl = l.image || 'https://picsum.photos/seed/' + l.id + '/400/300';
            const sellerName = l.seller_name || 'anonymous';
            const rating = l.rating ? l.rating.toFixed(1) : '-';
            const stars = l.rating ? '★'.repeat(Math.round(l.rating)) + '☆'.repeat(5-Math.round(l.rating)) : '';
            const isFav = favorites.has(l.id) ? 'active' : '';
            return '<div class="listing" onclick="showListing(\'' + l.id + '\')">' +
                '<button class="fav-btn ' + isFav + '" onclick="event.stopPropagation();toggleFav(\'' + l.id + '\')">♥</button>' +
                '<img class="listing-img" src="' + imgUrl + '" onerror="this.style.display=\'none\'">' +
                '<div class="listing-content">' +
                '<span class="category">' + (l.category || 'misc').toUpperCase() + '</span>' +
                '<h3>' + l.title.toUpperCase() + '</h3>' +
                '<div class="' + priceClass + '">' + priceText + '</div>' +
                '<p class="desc">' + (l.description || '') + '</p>' +
                '<div class="meta">' +
                '<span class="seller" onclick="event.stopPropagation();showSeller(\'' + l.seller_id + '\')">@' + sellerName + '</span>' +
                '<span class="rating"><span class="stars">' + stars + '</span> ' + rating + '</span>' +
                '</div></div></div>';
        }
        
        async function showListing(id) {
            const res = await fetch('/api/listings');
            const listings = await res.json();
            const l = listings.find(x => x.id === id);
            if (!l) return;
            currentListing = l;
            
            const imgUrl = l.image || 'https://picsum.photos/seed/' + l.id + '/400/300';
            const priceText = l.price === 0 ? 'FREE' : l.price + ' ' + (l.currency || 'XMR');
            const stars = l.rating ? '★'.repeat(Math.round(l.rating)) + '☆'.repeat(5-Math.round(l.rating)) : '☆☆☆☆☆';
            
            document.getElementById('listing-detail').innerHTML = 
                '<div class="detail-grid">' +
                '<div><img class="detail-img" src="' + imgUrl + '"></div>' +
                '<div class="detail-info">' +
                '<span class="category">' + (l.category || 'misc').toUpperCase() + '</span>' +
                '<h2>' + l.title + '</h2>' +
                '<div class="detail-price">' + priceText + '</div>' +
                '<p style="color:var(--green-dim);line-height:1.6">' + (l.description || '') + '</p>' +
                '<div class="detail-seller">' +
                '<div class="detail-seller-name" onclick="showSeller(\'' + l.seller_id + '\')">@' + (l.seller_name || 'anonymous') + '</div>' +
                '<div class="detail-seller-stats">' +
                '<span><span class="stars">' + stars + '</span> ' + (l.rating?.toFixed(1) || '-') + '</span>' +
                '<span>' + (l.review_count || 0) + ' reviews</span>' +
                '<span>' + (l.purchases || 0) + ' sales</span>' +
                '</div></div>' +
                '<div class="detail-actions">' +
                '<button class="btn" onclick="showOrderForm(\'' + l.id + '\')">BUY NOW</button>' +
                '<button class="btn btn-outline" onclick="showMessageForm(\'' + l.seller_id + '\', \'' + l.id + '\')">MESSAGE SELLER</button>' +
                '<button class="btn btn-outline" onclick="toggleFav(\'' + l.id + '\')">♥ SAVE</button>' +
                '</div></div></div>' +
                '<div class="reviews-section"><h3>REVIEWS (' + (l.review_count || 0) + ')</h3><div id="reviews-list">Loading...</div></div>';
            
            showModal('listing-modal');
            loadReviews(l.id);
        }
        
        async function loadReviews(listingId) {
            const res = await fetch('/api/reviews?listing=' + listingId);
            const reviews = await res.json();
            const container = document.getElementById('reviews-list');
            
            if (!reviews || reviews.length === 0) {
                container.innerHTML = '<p style="color:var(--green-dim)">No reviews yet.</p>';
                return;
            }
            
            container.innerHTML = reviews.map(r => 
                '<div class="review">' +
                '<div class="review-header">' +
                '<span class="review-user">@' + (r.buyer_name || 'anonymous') + (r.verified ? ' <span class="badge badge-verified">VERIFIED</span>' : '') + '</span>' +
                '<span class="review-rating"><span class="stars">' + '★'.repeat(r.rating) + '☆'.repeat(5-r.rating) + '</span></span>' +
                '</div>' +
                '<p class="review-text">' + r.comment + '</p>' +
                '<div class="review-date">' + new Date(r.created_at).toLocaleDateString() + '</div>' +
                (r.seller_reply ? '<div class="review-reply"><strong>SELLER:</strong> ' + r.seller_reply + '</div>' : '') +
                '</div>'
            ).join('');
        }
        
        async function showSeller(sellerId) {
            const res = await fetch('/api/seller?id=' + sellerId);
            const seller = await res.json();
            
            const stars = seller.rating ? '★'.repeat(Math.round(seller.rating)) + '☆'.repeat(5-Math.round(seller.rating)) : '☆☆☆☆☆';
            const badges = [];
            if (seller.trusted_seller) badges.push('<span class="badge badge-trusted">TRUSTED</span>');
            if (seller.verified) badges.push('<span class="badge badge-verified">VERIFIED</span>');
            
            document.getElementById('seller-detail').innerHTML =
                '<div style="text-align:center;padding:20px;border:1px solid var(--border);margin-bottom:20px">' +
                '<div style="font-size:48px;color:var(--green)">@</div>' +
                '<h2>' + (seller.name || 'anonymous') + '</h2>' +
                '<div>' + badges.join(' ') + '</div>' +
                '<p style="color:var(--green-dim);margin:10px 0">' + (seller.bio || 'No bio') + '</p>' +
                '</div>' +
                '<div class="stats">' +
                '<div class="stat-box"><div class="value">' + stars + '</div>RATING</div>' +
                '<div class="stat-box"><div class="value">' + (seller.total_orders || 0) + '</div>SALES</div>' +
                '<div class="stat-box"><div class="value">' + (seller.review_count || 0) + '</div>REVIEWS</div>' +
                '<div class="stat-box"><div class="value">' + (seller.response_time || 'N/A') + '</div>RESPONSE</div>' +
                '</div>' +
                '<h3>LISTINGS BY THIS SELLER</h3>' +
                '<div class="listings" id="seller-listings">Loading...</div>';
            
            showModal('seller-modal');
            loadSellerListings(sellerId);
        }
        
        async function loadSellerListings(sellerId) {
            const res = await fetch('/api/listings?seller=' + sellerId);
            const listings = await res.json();
            document.getElementById('seller-listings').innerHTML = 
                (listings && listings.length > 0) ? listings.map(l => renderListing(l)).join('') : '<p>No listings</p>';
        }
        
        function showOrderForm(listingId) {
            document.getElementById('order-form').innerHTML =
                '<form onsubmit="placeOrder(event, \'' + listingId + '\')">' +
                '<div style="margin-bottom:15px"><label>PAYMENT METHOD:</label><br>' +
                '<select id="order-currency" style="width:100%">' +
                '<option value="XMR">MONERO (XMR) - Maximum Privacy</option>' +
                '<option value="ZEC">ZCASH (ZEC) - Shielded Transactions</option>' +
                '</select></div>' +
                '<div style="margin-bottom:15px"><label>YOUR REFUND ADDRESS (optional):</label><br>' +
                '<input type="text" id="order-refund" placeholder="Your XMR/ZEC address for refunds..." style="width:100%"></div>' +
                '<div style="margin-bottom:15px"><label>DELIVERY INFO (encrypted):</label><br>' +
                '<textarea id="order-addr" placeholder="Shipping address, email, or delivery instructions..." style="width:100%"></textarea></div>' +
                '<div style="padding:15px;border:1px solid var(--amber);margin-bottom:15px;color:var(--amber)">' +
                '<strong>ESCROW PROTECTION</strong><br>' +
                '- Funds held securely until you confirm receipt<br>' +
                '- 14-day auto-release after shipping<br>' +
                '- Dispute resolution available<br>' +
                '- 2% platform fee' +
                '</div>' +
                '<button type="submit" class="btn">CREATE ESCROW ORDER</button>' +
                '</form>';
            closeModal('listing-modal');
            showModal('order-modal');
        }
        
        async function placeOrder(e, listingId) {
            e.preventDefault();
            const currency = document.getElementById('order-currency').value;
            const refundAddr = document.getElementById('order-refund').value;
            const deliveryInfo = document.getElementById('order-addr').value;
            
            if (!deliveryInfo) {
                alert('Please enter delivery information');
                return;
            }
            
            try {
                const formData = new FormData();
                formData.append('listing_id', listingId);
                formData.append('currency', currency);
                formData.append('buyer_address', refundAddr);
                formData.append('delivery_info', deliveryInfo);
                
                const res = await fetch('/api/escrow/create', {
                    method: 'POST',
                    body: formData
                });
                
                if (res.ok) {
                    const result = await res.json();
                    showPaymentModal(result);
                } else {
                    const err = await res.text();
                    alert('Failed to create order: ' + err);
                }
            } catch (err) {
                alert('Error: ' + err.message);
            }
        }
        
        function showPaymentModal(escrow) {
            closeModal('order-modal');
            document.getElementById('order-form').innerHTML =
                '<div style="text-align:center;padding:20px">' +
                '<h2 style="color:var(--amber)">PAYMENT REQUIRED</h2>' +
                '<p style="margin:20px 0">Send exactly:</p>' +
                '<div style="font-size:32px;color:var(--green);padding:20px;border:2px solid var(--green);margin:20px 0">' +
                escrow.amount.toFixed(8) + ' ' + escrow.currency +
                '</div>' +
                '<p style="margin:10px 0">To address:</p>' +
                '<div style="background:var(--bg);padding:15px;border:1px solid var(--border);word-break:break-all;font-size:12px">' +
                escrow.payment_address +
                '</div>' +
                '<button class="btn btn-outline" style="margin-top:10px" onclick="navigator.clipboard.writeText(\'' + escrow.payment_address + '\');this.textContent=\'COPIED!\'">' +
                'COPY ADDRESS</button>' +
                '<p style="margin:20px 0;color:var(--green-dim)">' +
                'Order ID: ' + escrow.order_id + '<br>' +
                'Fee: ' + escrow.fee_percent + '% (' + escrow.fee_amount.toFixed(8) + ' ' + escrow.currency + ')<br>' +
                'Expires: ' + new Date(escrow.expires_at).toLocaleString() +
                '</p>' +
                '<div style="padding:15px;border:1px solid var(--amber);color:var(--amber);text-align:left">' +
                'After sending payment, click below to check status.<br>' +
                'Requires ' + (escrow.currency === 'XMR' ? '10' : '6') + ' confirmations.' +
                '</div>' +
                '<div style="margin-top:20px">' +
                '<button class="btn" onclick="checkPaymentStatus(\'' + escrow.escrow_id + '\')">CHECK PAYMENT STATUS</button>' +
                '</div>' +
                '</div>';
            showModal('order-modal');
        }
        
        async function checkPaymentStatus(escrowId) {
            const res = await fetch('/api/escrow/status?id=' + escrowId);
            const status = await res.json();
            
            if (status.state === 'funded' || status.state === 'shipped' || status.state === 'completed') {
                alert('Payment confirmed! State: ' + status.state.toUpperCase());
                closeModal('order-modal');
                loadOrders('buying');
            } else if (status.state === 'created') {
                alert('Payment not yet received. Please send ' + status.amount + ' ' + status.currency + ' to the address shown.');
            } else {
                alert('Order status: ' + status.state.toUpperCase());
            }
        }
        
        function showMessageForm(sellerId, listingId) {
            document.getElementById('message-thread').innerHTML =
                '<div id="msg-history" style="max-height:300px;overflow-y:auto;margin-bottom:15px;padding:10px;border:1px solid var(--border)"></div>' +
                '<form onsubmit="sendMessage(event, \'' + sellerId + '\', \'' + listingId + '\')">' +
                '<textarea id="msg-content" placeholder="Type your message..." style="width:100%;margin-bottom:10px"></textarea>' +
                '<button type="submit" class="btn">SEND MESSAGE</button>' +
                '</form>';
            showModal('message-modal');
        }
        
        async function sendMessage(e, sellerId, listingId) {
            e.preventDefault();
            const content = document.getElementById('msg-content').value;
            if (!content) return;
            alert('Message sent! In production, this would send via encrypted P2P messaging.');
            document.getElementById('msg-content').value = '';
        }
        
        async function loadOrders(type) {
            const role = type === 'buying' ? 'buyer' : 'seller';
            const res = await fetch('/api/escrow/my?role=' + role);
            const orders = await res.json();
            const container = document.getElementById('orders-list');
            
            if (!orders || orders.length === 0) {
                container.innerHTML = 
                    '<div class="order">' +
                    '<div class="order-header"><span>No ' + type + ' orders yet</span></div>' +
                    '<p style="color:var(--green-dim)">Your ' + type + ' orders will appear here.</p>' +
                    '</div>';
                return;
            }
            
            container.innerHTML = orders.map(o => {
                const stateColors = {
                    'created': 'var(--green-dim)',
                    'funded': 'var(--amber)',
                    'shipped': 'var(--blue)',
                    'completed': 'var(--green)',
                    'disputed': 'var(--red)',
                    'refunded': 'var(--amber)',
                    'cancelled': 'var(--red)',
                    'expired': 'var(--red)'
                };
                const stateColor = stateColors[o.state] || 'var(--green-dim)';
                
                return '<div class="order" onclick="showEscrowDetails(\'' + o.escrow_id + '\')" style="cursor:pointer">' +
                    '<div class="order-header">' +
                    '<span>' + o.listing_title + '</span>' +
                    '<span class="order-status" style="background:' + stateColor + ';color:#000">' + o.state.toUpperCase() + '</span>' +
                    '</div>' +
                    '<p>' + o.amount.toFixed(6) + ' ' + o.currency + '</p>' +
                    '<p style="color:var(--green-dim);font-size:12px">' + new Date(o.created_at).toLocaleDateString() + '</p>' +
                    '</div>';
            }).join('');
        }
        
        async function showEscrowDetails(escrowId) {
            const res = await fetch('/api/escrow/status?id=' + escrowId);
            const e = await res.json();
            
            let actions = '';
            if (e.state === 'funded') {
                actions = '<button class="btn" onclick="markShipped(\'' + escrowId + '\')">MARK SHIPPED</button>';
            } else if (e.state === 'shipped') {
                actions = '<button class="btn" onclick="releaseEscrow(\'' + escrowId + '\')">CONFIRM RECEIVED</button>' +
                    '<button class="btn btn-danger" style="margin-left:10px" onclick="openDispute(\'' + escrowId + '\')">OPEN DISPUTE</button>';
            }
            
            document.getElementById('order-form').innerHTML =
                '<h2>' + e.listing_title + '</h2>' +
                '<div style="padding:20px;border:1px solid var(--border);margin:15px 0">' +
                '<p><strong>Status:</strong> <span style="color:var(--amber)">' + e.state.toUpperCase() + '</span></p>' +
                '<p><strong>Amount:</strong> ' + e.amount.toFixed(8) + ' ' + e.currency + '</p>' +
                '<p><strong>Order ID:</strong> ' + e.order_id + '</p>' +
                (e.tx_id ? '<p><strong>TX ID:</strong> <span style="font-size:12px">' + e.tx_id + '</span></p>' : '') +
                (e.tracking_info ? '<p><strong>Tracking:</strong> ' + e.tracking_info + '</p>' : '') +
                '<p><strong>Created:</strong> ' + new Date(e.created_at).toLocaleString() + '</p>' +
                (e.state === 'shipped' ? '<p><strong>Auto-release:</strong> ' + new Date(e.auto_release_at).toLocaleString() + '</p>' : '') +
                '</div>' +
                '<div style="margin-top:20px">' + actions + '</div>';
            showModal('order-modal');
        }
        
        async function markShipped(escrowId) {
            const tracking = prompt('Enter tracking info (optional):');
            const res = await fetch('/api/escrow/ship', {
                method: 'POST',
                body: new URLSearchParams({ escrow_id: escrowId, tracking_info: tracking || '' })
            });
            if (res.ok) {
                alert('Order marked as shipped!');
                closeModal('order-modal');
                loadOrders('selling');
            }
        }
        
        async function releaseEscrow(escrowId) {
            if (!confirm('Confirm you have received the item? This will release funds to the seller.')) return;
            const res = await fetch('/api/escrow/release', {
                method: 'POST',
                body: new URLSearchParams({ escrow_id: escrowId })
            });
            if (res.ok) {
                alert('Funds released to seller. Transaction complete!');
                closeModal('order-modal');
                loadOrders('buying');
            }
        }
        
        async function openDispute(escrowId) {
            const reason = prompt('Reason for dispute:');
            if (!reason) return;
            const res = await fetch('/api/escrow/dispute', {
                method: 'POST',
                body: new URLSearchParams({ escrow_id: escrowId, reason: reason })
            });
            if (res.ok) {
                alert('Dispute opened. Funds are frozen.');
                closeModal('order-modal');
                loadOrders('buying');
            }
        }
        
        async function loadConversations() {
            document.getElementById('conversations-list').innerHTML =
                '<div class="conversation">' +
                '<div class="conversation-header">' +
                '<span>No messages yet</span>' +
                '</div>' +
                '<p style="color:var(--green-dim)">Start a conversation by messaging a seller.</p>' +
                '</div>';
        }
        
        async function loadFavorites() {
            const container = document.getElementById('favorites-list');
            if (favorites.size === 0) {
                container.innerHTML = '<p style="color:var(--green-dim)">No saved listings. Click ♥ on any listing to save it.</p>';
                return;
            }
            const res = await fetch('/api/listings');
            const listings = await res.json();
            const favListings = listings.filter(l => favorites.has(l.id));
            container.innerHTML = favListings.length > 0 ? favListings.map(l => renderListing(l)).join('') : '<p>No saved listings</p>';
        }
        
        async function loadMyListings() {
            const res = await fetch('/api/my-listings');
            const listings = await res.json();
            const container = document.getElementById('my-listings');
            
            if (!listings || listings.length === 0) {
                container.innerHTML = '<p style="color:var(--green-dim)">You have no listings yet. Click "+ NEW LISTING" to create one.</p>';
                return;
            }
            container.innerHTML = '<div class="listings">' + listings.map(l => renderListing(l)).join('') + '</div>';
        }
        
        function toggleFav(id) {
            if (favorites.has(id)) {
                favorites.delete(id);
            } else {
                favorites.add(id);
            }
            loadListings();
        }
        
        function showCreateListing() {
            showModal('create-modal');
        }
        
        async function createListing(e) {
            e.preventDefault();
            const title = document.getElementById('new-title').value;
            const category = document.getElementById('new-category').value;
            const price = document.getElementById('new-price').value;
            const currency = document.getElementById('new-currency').value;
            const desc = document.getElementById('new-desc').value;
            const image = document.getElementById('new-image').value;
            
            const formData = new FormData();
            formData.append('title', title);
            formData.append('category', category);
            formData.append('price', price);
            formData.append('currency', currency);
            formData.append('description', desc);
            formData.append('image', image);
            
            try {
                const res = await fetch('/api/create-listing', {
                    method: 'POST',
                    body: formData
                });
                
                if (res.ok) {
                    const result = await res.json();
                    alert('Listing created and broadcast to ' + (result.message || 'network') + '!');
                    closeModal('create-modal');
                    // Clear form
                    document.getElementById('new-title').value = '';
                    document.getElementById('new-price').value = '';
                    document.getElementById('new-desc').value = '';
                    document.getElementById('new-image').value = '';
                    // Reload listings
                    loadListings();
                    loadMyListings();
                } else {
                    const err = await res.text();
                    alert('Failed to create listing: ' + err);
                }
            } catch (err) {
                alert('Error: ' + err.message);
            }
        }
        
        document.getElementById('search').addEventListener('input', loadListings);
        document.getElementById('category').addEventListener('change', loadListings);
        document.getElementById('sort').addEventListener('change', loadListings);
        loadListings();
    </script>
</body>
</html>`

const chatPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>CHAT // KAYAKNET</title>
    <style>
        @import url('https://fonts.googleapis.com/css2?family=VT323&display=swap');
        * { margin: 0; padding: 0; box-sizing: border-box; }
        :root { --bg: #000; --green: #00ff00; --green-dim: #00aa00; --green-glow: #00ff0066; --border: #00ff0033; --amber: #ffaa00; --red: #ff4444; --cyan: #00ffff; }
        body { font-family: 'VT323', monospace; background: var(--bg); color: var(--green); height: 100vh; display: flex; flex-direction: column; }
        body::before { content: ""; position: fixed; top: 0; left: 0; right: 0; bottom: 0; background: repeating-linear-gradient(0deg, rgba(0,0,0,0.15) 0px, rgba(0,0,0,0.15) 1px, transparent 1px, transparent 2px); pointer-events: none; z-index: 1000; }
        .container { max-width: 1400px; margin: 0 auto; padding: 10px; width: 100%; flex: 1; display: flex; flex-direction: column; }
        .terminal { border: 1px solid var(--green); background: #0a0a0a; box-shadow: 0 0 20px var(--green-glow); flex: 1; display: flex; flex-direction: column; }
        .term-header { background: var(--green); color: var(--bg); padding: 5px 15px; font-size: 14px; flex-shrink: 0; display: flex; justify-content: space-between; align-items: center; }
        .term-body { padding: 10px; flex: 1; display: flex; flex-direction: column; min-height: 0; }
        header { display: flex; justify-content: space-between; align-items: center; padding: 8px 0; border-bottom: 1px dashed var(--border); flex-shrink: 0; }
        .logo { font-size: 20px; color: var(--green); text-decoration: none; text-shadow: 0 0 10px var(--green-glow); }
        .logo::before { content: "["; } .logo::after { content: "]"; }
        nav { display: flex; gap: 10px; }
        nav a { color: var(--green-dim); text-decoration: none; padding: 3px 8px; border: 1px solid transparent; font-size: 14px; }
        nav a:hover, nav a.active { color: var(--green); border-color: var(--green); }
        .chat-layout { display: flex; flex: 1; margin-top: 10px; gap: 10px; min-height: 0; }
        
        /* Left sidebar - Rooms & DMs */
        .sidebar-left { width: 200px; display: flex; flex-direction: column; gap: 10px; flex-shrink: 0; }
        .panel { border: 1px solid var(--border); background: rgba(0,20,0,0.5); }
        .panel-header { padding: 8px 10px; border-bottom: 1px solid var(--border); font-size: 12px; color: var(--green-dim); display: flex; justify-content: space-between; align-items: center; }
        .panel-body { padding: 5px; max-height: 200px; overflow-y: auto; }
        .room-item, .dm-item, .user-item { padding: 6px 8px; cursor: pointer; border: 1px solid transparent; margin: 2px 0; font-size: 14px; display: flex; align-items: center; gap: 8px; }
        .room-item:hover, .room-item.active, .dm-item:hover, .dm-item.active { border-color: var(--green); background: rgba(0,255,0,0.05); }
        .room-item .unread, .dm-item .unread { background: var(--amber); color: var(--bg); padding: 0 5px; font-size: 10px; margin-left: auto; }
        .status-dot { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; }
        .status-online { background: var(--green); box-shadow: 0 0 5px var(--green); }
        .status-away { background: var(--amber); }
        .status-offline { background: var(--green-dim); opacity: 0.5; }
        
        /* Main chat area */
        .chat-main { flex: 1; display: flex; flex-direction: column; border: 1px solid var(--border); min-width: 0; }
        .chat-header { padding: 10px; border-bottom: 1px solid var(--border); display: flex; justify-content: space-between; align-items: center; background: rgba(0,20,0,0.5); }
        .chat-title { font-size: 18px; }
        .chat-title::before { content: "#"; color: var(--green-dim); }
        .chat-topic { font-size: 12px; color: var(--green-dim); margin-top: 3px; }
        .typing-indicator { font-size: 12px; color: var(--cyan); font-style: italic; }
        .messages { flex: 1; padding: 10px; overflow-y: auto; background: var(--bg); }
        .message { margin-bottom: 10px; padding: 8px; border-left: 2px solid transparent; }
        .message:hover { background: rgba(0,255,0,0.02); border-left-color: var(--green-dim); }
        .message-header { display: flex; align-items: center; gap: 10px; margin-bottom: 4px; }
        .message .avatar { width: 28px; height: 28px; border: 1px solid var(--green); display: flex; align-items: center; justify-content: center; font-size: 12px; background: rgba(0,255,0,0.1); flex-shrink: 0; }
        .message .nick { color: var(--green); cursor: pointer; font-size: 15px; }
        .message .nick:hover { text-decoration: underline; }
        .message .user-id { color: var(--green-dim); font-size: 10px; opacity: 0.6; }
        .message .time { color: var(--green-dim); font-size: 11px; margin-left: auto; }
        .message .content { color: #aaffaa; padding-left: 38px; font-size: 15px; line-height: 1.4; word-break: break-word; }
        .message .media { margin-top: 8px; padding-left: 38px; }
        .message .media img { max-width: 300px; max-height: 200px; border: 1px solid var(--border); cursor: pointer; }
        .message .reactions { display: flex; gap: 5px; margin-top: 5px; padding-left: 38px; }
        .message .reaction { padding: 2px 6px; border: 1px solid var(--border); font-size: 12px; cursor: pointer; }
        .message .reaction:hover { border-color: var(--green); }
        .message.system { border-left-color: var(--cyan); }
        .message.system .nick { color: var(--cyan); }
        .message.dm { border-left-color: var(--amber); background: rgba(255,170,0,0.03); }
        
        /* Input area */
        .input-area { padding: 10px; border-top: 1px solid var(--border); background: rgba(0,20,0,0.5); }
        .input-row { display: flex; gap: 8px; align-items: center; }
        .input-area input[type="text"] { flex: 1; padding: 10px; background: var(--bg); border: 1px solid var(--green); color: var(--green); font-family: inherit; font-size: 15px; }
        .input-area input:focus { outline: none; box-shadow: 0 0 10px var(--green-glow); }
        .input-btn { padding: 10px 15px; background: transparent; border: 1px solid var(--green); color: var(--green); cursor: pointer; font-family: inherit; font-size: 14px; }
        .input-btn:hover { background: var(--green); color: var(--bg); }
        .input-btn.primary { background: var(--green); color: var(--bg); }
        .file-input { display: none; }
        
        /* Right sidebar - Users */
        .sidebar-right { width: 180px; display: flex; flex-direction: column; gap: 10px; flex-shrink: 0; }
        .user-item .user-nick { flex: 1; overflow: hidden; text-overflow: ellipsis; }
        .user-item .user-status { font-size: 10px; color: var(--green-dim); }
        
        /* Profile modal */
        .modal { display: none; position: fixed; top: 0; left: 0; right: 0; bottom: 0; background: rgba(0,0,0,0.9); z-index: 2000; align-items: center; justify-content: center; }
        .modal.active { display: flex; }
        .modal-content { background: #0a0a0a; border: 1px solid var(--green); padding: 20px; max-width: 500px; width: 90%; max-height: 80vh; overflow-y: auto; }
        .modal-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 15px; padding-bottom: 10px; border-bottom: 1px solid var(--border); }
        .modal-close { cursor: pointer; font-size: 20px; }
        .profile-avatar { width: 80px; height: 80px; border: 2px solid var(--green); display: flex; align-items: center; justify-content: center; font-size: 32px; margin: 0 auto 15px; }
        .profile-nick { text-align: center; font-size: 24px; margin-bottom: 5px; }
        .profile-id { text-align: center; font-size: 12px; color: var(--green-dim); margin-bottom: 15px; word-break: break-all; }
        .profile-status { text-align: center; margin-bottom: 15px; }
        .profile-bio { padding: 10px; border: 1px solid var(--border); margin-bottom: 15px; color: var(--green-dim); }
        .profile-stats { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; margin-bottom: 15px; }
        .profile-stat { text-align: center; padding: 10px; border: 1px solid var(--border); }
        .profile-stat-value { font-size: 20px; color: var(--green); }
        .profile-stat-label { font-size: 11px; color: var(--green-dim); }
        .profile-actions { display: flex; gap: 10px; }
        .profile-actions button { flex: 1; }
        
        /* Image viewer */
        .image-viewer { display: none; position: fixed; top: 0; left: 0; right: 0; bottom: 0; background: rgba(0,0,0,0.95); z-index: 3000; align-items: center; justify-content: center; cursor: pointer; }
        .image-viewer.active { display: flex; }
        .image-viewer img { max-width: 90%; max-height: 90%; border: 2px solid var(--green); }
        
        /* Tabs */
        .tabs { display: flex; border-bottom: 1px solid var(--border); margin-bottom: 10px; }
        .tab { padding: 8px 15px; cursor: pointer; border-bottom: 2px solid transparent; color: var(--green-dim); }
        .tab:hover { color: var(--green); }
        .tab.active { color: var(--green); border-bottom-color: var(--green); }
        
        @media (max-width: 900px) {
            .sidebar-right { display: none; }
            .sidebar-left { width: 150px; }
        }
        @media (max-width: 600px) {
            .sidebar-left { width: 50px; }
            .room-item span, .dm-item span { display: none; }
            .panel-header span { display: none; }
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="terminal">
            <div class="term-header">
                <span>KAYAKNET SECURE CHAT // ONION ROUTED // E2E ENCRYPTED</span>
                <span id="connection-status">CONNECTED</span>
            </div>
            <div class="term-body">
                <header>
                    <a href="/" class="logo">KAYAKNET</a>
                    <nav>
                        <a href="/marketplace">/market</a>
                        <a href="/chat" class="active">/chat</a>
                        <a href="/domains">/domains</a>
                        <a href="/network">/network</a>
                    </nav>
                </header>
                <div class="chat-layout">
                    <!-- Left Sidebar -->
                    <div class="sidebar-left">
                        <div class="panel">
                            <div class="panel-header"><span>CHANNELS</span><span style="cursor:pointer" onclick="showCreateRoom()">+</span></div>
                            <div class="panel-body" id="rooms-list"></div>
                        </div>
                        <div class="panel">
                            <div class="panel-header"><span>DIRECT MESSAGES</span></div>
                            <div class="panel-body" id="dm-list"></div>
                        </div>
                        <div class="panel">
                            <div class="panel-header"><span>MY PROFILE</span></div>
                            <div class="panel-body">
                                <div class="user-item" onclick="showMyProfile()" id="my-profile">
                                    <div class="status-dot status-online"></div>
                                    <span class="user-nick">Loading...</span>
                                </div>
                            </div>
                        </div>
                    </div>
                    
                    <!-- Main Chat -->
                    <div class="chat-main">
                        <div class="chat-header">
                            <div>
                                <div class="chat-title" id="chat-title">general</div>
                                <div class="chat-topic" id="chat-topic">General discussion - welcome to KayakNet!</div>
                            </div>
                            <div class="typing-indicator" id="typing-indicator"></div>
                        </div>
                        <div class="messages" id="messages"></div>
                        <div class="input-area">
                            <div class="input-row">
                                <input type="file" id="file-input" class="file-input" accept="image/*,.pdf,.txt,.zip" onchange="handleFileSelect(event)">
                                <button class="input-btn" onclick="document.getElementById('file-input').click()" title="Attach file">+FILE</button>
                                <input type="text" id="message-input" placeholder="Type a message... (Shift+Enter for newline)" onkeydown="handleKeyDown(event)" oninput="handleTyping()">
                                <button class="input-btn" onclick="insertEmoji()">:)</button>
                                <button class="input-btn primary" onclick="sendMessage()">SEND</button>
                            </div>
                            <div id="attachment-preview" style="margin-top:8px;display:none;"></div>
                        </div>
                    </div>
                    
                    <!-- Right Sidebar - Users -->
                    <div class="sidebar-right">
                        <div class="panel" style="flex:1;display:flex;flex-direction:column;">
                            <div class="panel-header"><span>ONLINE</span> <span id="online-count">0</span></div>
                            <div class="panel-body" id="users-list" style="flex:1;max-height:none;"></div>
                        </div>
                        <div class="panel">
                            <div class="panel-header"><span>FIND USERS</span></div>
                            <div class="panel-body">
                                <input type="text" id="user-search" placeholder="Search..." style="width:100%;padding:5px;background:var(--bg);border:1px solid var(--border);color:var(--green);font-family:inherit;" oninput="searchUsers()">
                                <div id="search-results" style="margin-top:5px;"></div>
                            </div>
                        </div>
                    </div>
                </div>
            </div>
        </div>
    </div>
    
    <!-- Profile Modal -->
    <div class="modal" id="profile-modal">
        <div class="modal-content">
            <div class="modal-header">
                <span>USER PROFILE</span>
                <span class="modal-close" onclick="closeModal('profile-modal')">X</span>
            </div>
            <div id="profile-content"></div>
        </div>
    </div>
    
    <!-- Image Viewer -->
    <div class="image-viewer" id="image-viewer" onclick="this.classList.remove('active')">
        <img id="viewer-image" src="">
    </div>

    <script>
        let currentRoom = 'general';
        let currentDM = null;
        let myProfile = null;
        let pendingAttachment = null;
        let typingTimeout = null;
        
        // Initialize
        async function init() {
            await loadMyProfile();
            await loadRooms();
            await loadDMs();
            await loadMessages();
            await loadOnlineUsers();
            
            // Poll for updates
            setInterval(loadMessages, 2000);
            setInterval(loadOnlineUsers, 5000);
            setInterval(loadDMs, 10000);
        }
        
        async function loadMyProfile() {
            try {
                const res = await fetch('/api/chat/profile');
                myProfile = await res.json();
                document.getElementById('my-profile').innerHTML = 
                    '<div class="status-dot status-' + myProfile.status + '"></div>' +
                    '<span class="user-nick">' + escapeHtml(myProfile.nick || myProfile.id.substring(0,8)) + '</span>';
            } catch(e) { console.error('Failed to load profile:', e); }
        }
        
        async function loadRooms() {
            try {
                const res = await fetch('/api/chat/rooms');
                const rooms = await res.json();
                const container = document.getElementById('rooms-list');
                container.innerHTML = rooms.map(r => 
                    '<div class="room-item' + (r.name === currentRoom && !currentDM ? ' active' : '') + '" onclick="selectRoom(\'' + r.name + '\')">' +
                    '<span>#' + escapeHtml(r.name) + '</span>' +
                    '</div>'
                ).join('');
            } catch(e) { console.error('Failed to load rooms:', e); }
        }
        
        async function loadDMs() {
            try {
                const res = await fetch('/api/chat/conversations');
                const convs = await res.json();
                const container = document.getElementById('dm-list');
                if (!convs || convs.length === 0) {
                    container.innerHTML = '<div style="padding:5px;color:var(--green-dim);font-size:12px;">No conversations</div>';
                    return;
                }
                container.innerHTML = convs.map(c => {
                    const otherUser = c.participants.find(p => p !== myProfile?.id) || c.participants[0];
                    return '<div class="dm-item' + (currentDM === otherUser ? ' active' : '') + '" onclick="selectDM(\'' + otherUser + '\')">' +
                        '<div class="status-dot status-online"></div>' +
                        '<span>' + escapeHtml(otherUser.substring(0,12)) + '</span>' +
                        (c.unread > 0 ? '<span class="unread">' + c.unread + '</span>' : '') +
                        '</div>';
                }).join('');
            } catch(e) { console.error('Failed to load DMs:', e); }
        }
        
        async function loadMessages() {
            try {
                let url = currentDM ? '/api/chat/dm?user=' + currentDM : '/api/chat?room=' + currentRoom;
                const res = await fetch(url);
                const messages = await res.json();
                const container = document.getElementById('messages');
                
                if (!messages || messages.length === 0) {
                    container.innerHTML = '<div class="message system"><div class="message-header"><span class="nick">SYSTEM</span></div><div class="content">// No messages yet. Start the conversation!</div></div>';
                    return;
                }
                
                container.innerHTML = messages.map(m => renderMessage(m)).join('');
                container.scrollTop = container.scrollHeight;
            } catch(e) { console.error('Failed to load messages:', e); }
        }
        
        function renderMessage(m) {
            const avatar = (m.nick || 'A')[0].toUpperCase();
            const time = new Date(m.timestamp).toLocaleTimeString();
            const shortId = m.sender_id ? m.sender_id.substring(0,8) : '';
            
            let mediaHtml = '';
            if (m.media) {
                if (m.media.type && m.media.type.startsWith('image/')) {
                    mediaHtml = '<div class="media"><img src="data:' + m.media.type + ';base64,' + m.media.data + '" onclick="viewImage(this.src)" alt="' + escapeHtml(m.media.name) + '"></div>';
                } else if (m.media.name) {
                    mediaHtml = '<div class="media">[FILE: ' + escapeHtml(m.media.name) + ' (' + formatSize(m.media.size) + ')]</div>';
                }
            }
            
            let reactionsHtml = '';
            if (m.reactions && Object.keys(m.reactions).length > 0) {
                reactionsHtml = '<div class="reactions">' + 
                    Object.entries(m.reactions).map(([emoji, users]) => 
                        '<span class="reaction" onclick="addReaction(\'' + m.id + '\', \'' + emoji + '\')">' + emoji + ' ' + users.length + '</span>'
                    ).join('') + '</div>';
            }
            
            return '<div class="message' + (m.type === 5 ? ' system' : '') + (m.receiver_id ? ' dm' : '') + '">' +
                '<div class="message-header">' +
                '<div class="avatar">' + avatar + '</div>' +
                '<span class="nick" onclick="showProfile(\'' + m.sender_id + '\')">' + escapeHtml(m.nick || 'Anonymous') + '</span>' +
                '<span class="user-id">' + shortId + '</span>' +
                '<span class="time">' + time + '</span>' +
                '</div>' +
                '<div class="content">' + escapeHtml(m.content) + '</div>' +
                mediaHtml +
                reactionsHtml +
                '</div>';
        }
        
        async function loadOnlineUsers() {
            try {
                const res = await fetch('/api/chat/users?room=' + currentRoom);
                const users = await res.json();
                const container = document.getElementById('users-list');
                document.getElementById('online-count').textContent = users?.length || 0;
                
                if (!users || users.length === 0) {
                    container.innerHTML = '<div style="padding:5px;color:var(--green-dim);font-size:12px;">No users online</div>';
                    return;
                }
                
                container.innerHTML = users.map(u => 
                    '<div class="user-item" onclick="showProfile(\'' + u.id + '\')">' +
                    '<div class="status-dot status-' + (u.status || 'online') + '"></div>' +
                    '<div class="user-nick">' + escapeHtml(u.nick || u.id.substring(0,8)) + '</div>' +
                    '</div>'
                ).join('');
            } catch(e) { console.error('Failed to load users:', e); }
        }
        
        function selectRoom(room) {
            currentRoom = room;
            currentDM = null;
            document.querySelectorAll('.room-item, .dm-item').forEach(el => el.classList.remove('active'));
            document.querySelector('.room-item[onclick*="' + room + '"]')?.classList.add('active');
            document.getElementById('chat-title').textContent = room;
            document.getElementById('chat-title').style.setProperty('--before', '"#"');
            loadMessages();
            loadOnlineUsers();
        }
        
        function selectDM(userId) {
            currentDM = userId;
            currentRoom = null;
            document.querySelectorAll('.room-item, .dm-item').forEach(el => el.classList.remove('active'));
            document.querySelector('.dm-item[onclick*="' + userId + '"]')?.classList.add('active');
            document.getElementById('chat-title').textContent = 'DM: ' + userId.substring(0,12);
            loadMessages();
        }
        
        async function sendMessage() {
            const input = document.getElementById('message-input');
            const content = input.value.trim();
            if (!content && !pendingAttachment) return;
            
            const formData = new FormData();
            if (currentDM) {
                formData.append('user', currentDM);
            } else {
                formData.append('room', currentRoom);
            }
            formData.append('message', content);
            
            if (pendingAttachment) {
                formData.append('media_type', pendingAttachment.type);
                formData.append('media_name', pendingAttachment.name);
                formData.append('media_data', pendingAttachment.data);
            }
            
            try {
                const endpoint = currentDM ? '/api/chat/dm' : '/api/chat';
                await fetch(endpoint, { method: 'POST', body: formData });
                input.value = '';
                clearAttachment();
                loadMessages();
            } catch(e) { console.error('Failed to send:', e); }
        }
        
        function handleKeyDown(e) {
            if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault();
                sendMessage();
            }
        }
        
        function handleTyping() {
            // Could send typing indicator to server
            clearTimeout(typingTimeout);
            typingTimeout = setTimeout(() => {}, 3000);
        }
        
        function handleFileSelect(e) {
            const file = e.target.files[0];
            if (!file) return;
            if (file.size > 1024 * 1024) {
                alert('File too large. Max 1MB.');
                return;
            }
            
            const reader = new FileReader();
            reader.onload = function(ev) {
                const base64 = ev.target.result.split(',')[1];
                pendingAttachment = {
                    type: file.type,
                    name: file.name,
                    size: file.size,
                    data: base64
                };
                
                const preview = document.getElementById('attachment-preview');
                preview.style.display = 'block';
                if (file.type.startsWith('image/')) {
                    preview.innerHTML = '<img src="' + ev.target.result + '" style="max-height:100px;border:1px solid var(--green);"> <span onclick="clearAttachment()" style="cursor:pointer;color:var(--red);">[X]</span>';
                } else {
                    preview.innerHTML = '<span style="color:var(--amber);">[' + escapeHtml(file.name) + ']</span> <span onclick="clearAttachment()" style="cursor:pointer;color:var(--red);">[X]</span>';
                }
            };
            reader.readAsDataURL(file);
        }
        
        function clearAttachment() {
            pendingAttachment = null;
            document.getElementById('attachment-preview').style.display = 'none';
            document.getElementById('file-input').value = '';
        }
        
        function insertEmoji() {
            const emojis = ['👍', '❤️', '😂', '🔥', '💯', '🚀', '✅', '⚡'];
            const emoji = emojis[Math.floor(Math.random() * emojis.length)];
            document.getElementById('message-input').value += emoji;
        }
        
        async function addReaction(msgId, emoji) {
            try {
                await fetch('/api/chat/reaction', {
                    method: 'POST',
                    body: new URLSearchParams({ room: currentRoom, message_id: msgId, emoji: emoji })
                });
                loadMessages();
            } catch(e) { console.error('Failed to add reaction:', e); }
        }
        
        async function showProfile(userId) {
            try {
                const res = await fetch('/api/chat/user?id=' + userId);
                const user = await res.json();
                
                const content = document.getElementById('profile-content');
                content.innerHTML = 
                    '<div class="profile-avatar">' + (user.nick || 'A')[0].toUpperCase() + '</div>' +
                    '<div class="profile-nick">' + escapeHtml(user.nick || 'Anonymous') + '</div>' +
                    '<div class="profile-id">' + user.id + '</div>' +
                    '<div class="profile-status"><span class="status-dot status-' + (user.status || 'offline') + '" style="display:inline-block;margin-right:5px;"></span>' + (user.status_msg || user.status || 'offline') + '</div>' +
                    '<div class="profile-bio">' + escapeHtml(user.bio || 'No bio set.') + '</div>' +
                    '<div class="profile-stats">' +
                    '<div class="profile-stat"><div class="profile-stat-value">' + (user.messages_sent || 0) + '</div><div class="profile-stat-label">MESSAGES</div></div>' +
                    '<div class="profile-stat"><div class="profile-stat-value">' + formatDate(user.joined_at) + '</div><div class="profile-stat-label">JOINED</div></div>' +
                    '</div>' +
                    '<div class="profile-actions">' +
                    '<button class="input-btn" onclick="startDM(\'' + user.id + '\')">SEND DM</button>' +
                    '<button class="input-btn" onclick="blockUser(\'' + user.id + '\')">BLOCK</button>' +
                    '</div>';
                
                document.getElementById('profile-modal').classList.add('active');
            } catch(e) { console.error('Failed to load profile:', e); }
        }
        
        function showMyProfile() {
            if (myProfile) {
                showProfile(myProfile.id);
            }
        }
        
        function startDM(userId) {
            closeModal('profile-modal');
            selectDM(userId);
        }
        
        async function blockUser(userId) {
            if (confirm('Block this user? They won\\'t be able to DM you.')) {
                await fetch('/api/chat/block', { method: 'POST', body: new URLSearchParams({ user: userId }) });
                closeModal('profile-modal');
            }
        }
        
        async function searchUsers() {
            const query = document.getElementById('user-search').value;
            if (query.length < 2) {
                document.getElementById('search-results').innerHTML = '';
                return;
            }
            
            try {
                const res = await fetch('/api/chat/search?q=' + encodeURIComponent(query));
                const users = await res.json();
                document.getElementById('search-results').innerHTML = users.slice(0,5).map(u =>
                    '<div class="user-item" onclick="showProfile(\'' + u.id + '\')">' +
                    '<span>' + escapeHtml(u.nick || u.id.substring(0,12)) + '</span></div>'
                ).join('');
            } catch(e) { console.error('Search failed:', e); }
        }
        
        function viewImage(src) {
            document.getElementById('viewer-image').src = src;
            document.getElementById('image-viewer').classList.add('active');
        }
        
        function closeModal(id) {
            document.getElementById(id).classList.remove('active');
        }
        
        function escapeHtml(text) {
            if (!text) return '';
            const div = document.createElement('div');
            div.textContent = text;
            return div.innerHTML;
        }
        
        function formatSize(bytes) {
            if (bytes < 1024) return bytes + ' B';
            if (bytes < 1024*1024) return (bytes/1024).toFixed(1) + ' KB';
            return (bytes/1024/1024).toFixed(1) + ' MB';
        }
        
        function formatDate(dateStr) {
            if (!dateStr) return 'N/A';
            const d = new Date(dateStr);
            return d.toLocaleDateString();
        }
        
        // Start
        init();
    </script>
</body>
</html>`

const domainsPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>DOMAINS // KAYAKNET</title>
    <style>
        @import url('https://fonts.googleapis.com/css2?family=VT323&display=swap');
        * { margin: 0; padding: 0; box-sizing: border-box; }
        :root { --bg: #000; --green: #00ff00; --green-dim: #00aa00; --green-glow: #00ff0066; --border: #00ff0033; }
        body { font-family: 'VT323', monospace; background: var(--bg); color: var(--green); min-height: 100vh; }
        body::before { content: ""; position: fixed; top: 0; left: 0; right: 0; bottom: 0; background: repeating-linear-gradient(0deg, rgba(0,0,0,0.15) 0px, rgba(0,0,0,0.15) 1px, transparent 1px, transparent 2px); pointer-events: none; z-index: 1000; }
        .container { max-width: 1000px; margin: 0 auto; padding: 20px; }
        .terminal { border: 1px solid var(--green); background: #0a0a0a; box-shadow: 0 0 20px var(--green-glow); }
        .term-header { background: var(--green); color: var(--bg); padding: 5px 15px; font-size: 14px; }
        .term-body { padding: 20px; }
        header { display: flex; justify-content: space-between; align-items: center; padding: 15px 0; border-bottom: 1px dashed var(--border); margin-bottom: 20px; }
        .logo { font-size: 24px; color: var(--green); text-decoration: none; text-shadow: 0 0 10px var(--green-glow); }
        .logo::before { content: "["; } .logo::after { content: "]"; }
        nav { display: flex; gap: 15px; }
        nav a { color: var(--green-dim); text-decoration: none; padding: 5px 10px; border: 1px solid transparent; }
        nav a:hover, nav a.active { color: var(--green); border-color: var(--green); }
        h1 { font-size: 24px; margin-bottom: 20px; }
        h1::before { content: "> "; color: var(--green-dim); }
        .search-bar { display: flex; gap: 10px; margin-bottom: 20px; flex-wrap: wrap; }
        input { flex: 1; min-width: 200px; padding: 10px; background: var(--bg); border: 1px solid var(--green); color: var(--green); font-family: inherit; font-size: 16px; }
        input:focus { outline: none; box-shadow: 0 0 10px var(--green-glow); }
        .btn { padding: 10px 20px; background: var(--green); color: var(--bg); border: none; cursor: pointer; font-family: inherit; font-size: 16px; }
        .btn:hover { box-shadow: 0 0 15px var(--green-glow); }
        .domains { display: grid; gap: 10px; }
        .domain { border: 1px solid var(--border); padding: 15px; display: flex; justify-content: space-between; align-items: center; flex-wrap: wrap; gap: 10px; background: var(--bg); }
        .domain:hover { border-color: var(--green); box-shadow: 0 0 10px var(--green-glow); }
        .domain-name { font-size: 20px; color: var(--green); text-shadow: 0 0 5px var(--green-glow); }
        .domain-desc { color: var(--green-dim); font-size: 14px; margin-top: 5px; }
        .domain-owner { color: var(--green-dim); font-size: 12px; opacity: 0.7; }
    </style>
</head>
<body>
    <div class="container">
        <div class="terminal">
            <div class="term-header">KAYAKNET DOMAIN REGISTRY // .KYK</div>
            <div class="term-body">
                <header>
                    <a href="/" class="logo">KAYAKNET</a>
                    <nav>
                        <a href="/marketplace">/market</a>
                        <a href="/chat">/chat</a>
                        <a href="/domains" class="active">/domains</a>
                        <a href="/network">/network</a>
                    </nav>
                </header>
                <h1>.KYK_DOMAINS</h1>
                <div class="search-bar">
                    <input type="text" id="search" placeholder="dns://search..." />
                    <button class="btn" onclick="alert('CMD: register [name]')">+REGISTER</button>
                </div>
                <div class="domains" id="domains">
                    <div class="domain"><div><div class="domain-name">LOADING...</div><div class="domain-desc">Querying DNS...</div></div></div>
                </div>
            </div>
        </div>
    </div>
    <script>
        async function loadDomains() {
            const search = document.getElementById('search').value;
            const res = await fetch('/api/domains?q=' + search);
            const domains = await res.json();
            const container = document.getElementById('domains');
            if (!domains || domains.length === 0) {
                container.innerHTML = '<div class="domain"><div><div class="domain-name">NO_RECORDS</div><div class="domain-desc">// Register the first .kyk domain</div></div></div>';
                return;
            }
            container.innerHTML = domains.map(d => '<div class="domain"><div><div class="domain-name">' + d.full_name.toUpperCase() + '</div><div class="domain-desc">' + (d.description || '// No description') + '</div></div><div class="domain-owner">OWNER: ' + d.owner.substring(0, 16) + '...</div></div>').join('');
        }
        document.getElementById('search').addEventListener('input', loadDomains);
        loadDomains();
    </script>
</body>
</html>`

const networkPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>NETWORK // KAYAKNET</title>
    <style>
        @import url('https://fonts.googleapis.com/css2?family=VT323&display=swap');
        * { margin: 0; padding: 0; box-sizing: border-box; }
        :root { --bg: #000; --green: #00ff00; --green-dim: #00aa00; --green-glow: #00ff0066; --amber: #ffaa00; --amber-glow: #ffaa0066; --border: #00ff0033; }
        body { font-family: 'VT323', monospace; background: var(--bg); color: var(--green); min-height: 100vh; }
        body::before { content: ""; position: fixed; top: 0; left: 0; right: 0; bottom: 0; background: repeating-linear-gradient(0deg, rgba(0,0,0,0.15) 0px, rgba(0,0,0,0.15) 1px, transparent 1px, transparent 2px); pointer-events: none; z-index: 1000; }
        .container { max-width: 1000px; margin: 0 auto; padding: 20px; }
        .terminal { border: 1px solid var(--green); background: #0a0a0a; box-shadow: 0 0 20px var(--green-glow); }
        .term-header { background: var(--green); color: var(--bg); padding: 5px 15px; font-size: 14px; }
        .term-body { padding: 20px; }
        header { display: flex; justify-content: space-between; align-items: center; padding: 15px 0; border-bottom: 1px dashed var(--border); margin-bottom: 20px; }
        .logo { font-size: 24px; color: var(--green); text-decoration: none; text-shadow: 0 0 10px var(--green-glow); }
        .logo::before { content: "["; } .logo::after { content: "]"; }
        nav { display: flex; gap: 15px; }
        nav a { color: var(--green-dim); text-decoration: none; padding: 5px 10px; border: 1px solid transparent; }
        nav a:hover, nav a.active { color: var(--green); border-color: var(--green); }
        h1 { font-size: 24px; margin-bottom: 20px; }
        h1::before { content: "> "; color: var(--green-dim); }
        h2 { font-size: 18px; margin: 25px 0 15px; color: var(--green-dim); }
        h2::before { content: "// "; }
        .status-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 15px; margin-bottom: 30px; }
        .status-card { border: 1px solid var(--border); padding: 20px; text-align: center; background: var(--bg); }
        .status-card h3 { color: var(--green-dim); font-size: 12px; text-transform: uppercase; margin-bottom: 10px; }
        .status-value { font-size: 36px; color: var(--green); text-shadow: 0 0 10px var(--green-glow); }
        .status-value.success { color: var(--green); text-shadow: 0 0 15px var(--green-glow); }
        .status-value.warning { color: var(--amber); text-shadow: 0 0 15px var(--amber-glow); }
        .peers { display: grid; gap: 8px; }
        .peer { border: 1px solid var(--border); padding: 12px 15px; display: flex; justify-content: space-between; align-items: center; flex-wrap: wrap; gap: 10px; background: var(--bg); font-size: 14px; }
        .peer:hover { border-color: var(--green); }
        .peer-id { color: var(--green); word-break: break-all; }
        .peer-status { color: var(--green-dim); font-size: 12px; }
    </style>
</head>
<body>
    <div class="container">
        <div class="terminal">
            <div class="term-header">KAYAKNET NETWORK STATUS // REAL-TIME</div>
            <div class="term-body">
                <header>
                    <a href="/" class="logo">KAYAKNET</a>
                    <nav>
                        <a href="/marketplace">/market</a>
                        <a href="/chat">/chat</a>
                        <a href="/domains">/domains</a>
                        <a href="/network" class="active">/network</a>
                    </nav>
                </header>
                <h1>NETWORK_STATUS</h1>
                <div class="status-grid">
                    <div class="status-card">
                        <h3>ANONYMITY</h3>
                        <div class="status-value" id="anon-status">--</div>
                    </div>
                    <div class="status-card">
                        <h3>PEERS</h3>
                        <div class="status-value" id="peer-count">0</div>
                    </div>
                    <div class="status-card">
                        <h3>RELAYS</h3>
                        <div class="status-value" id="relay-count">0</div>
                    </div>
                    <div class="status-card">
                        <h3>HOPS</h3>
                        <div class="status-value">3</div>
                    </div>
                </div>
                <h2>CONNECTED_PEERS</h2>
                <div class="peers" id="peers">
                    <div class="peer"><span class="peer-id">SCANNING...</span></div>
                </div>
            </div>
        </div>
    </div>
    <script>
        async function loadNetwork() {
            const statsRes = await fetch('/api/stats');
            const stats = await statsRes.json();
            const anonEl = document.getElementById('anon-status');
            if (stats.anonymous) {
                anonEl.textContent = 'ACTIVE';
                anonEl.className = 'status-value success';
            } else {
                anonEl.textContent = 'BUILD';
                anonEl.className = 'status-value warning';
            }
            document.getElementById('peer-count').textContent = stats.peers || 0;
            document.getElementById('relay-count').textContent = stats.relay_count || 0;
            const peersRes = await fetch('/api/peers');
            const peers = await peersRes.json();
            const container = document.getElementById('peers');
            if (!peers || peers.length === 0) {
                container.innerHTML = '<div class="peer"><span class="peer-id">NO_PEERS_CONNECTED</span></div>';
                return;
            }
            container.innerHTML = peers.map(p => '<div class="peer"><span class="peer-id">' + p.id + '</span><span class="peer-status">PING: ' + p.last_seen + '</span></div>').join('');
        }
        loadNetwork();
        setInterval(loadNetwork, 5000);
    </script>
</body>
</html>`
