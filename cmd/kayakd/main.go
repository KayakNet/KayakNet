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
	"math/rand"
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
	mux.HandleFunc("/api/chat", h.handleChat)
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

// populateSampleListings adds demo listings from various pseudonymous sellers
func populateSampleListings(m *market.Marketplace) {
	// Sample listings with images (using placeholder service or data URIs)
	samples := []struct {
		title, desc, category string
		price                 int64
		currency, image       string
		seller, sellerID      string
	}{
		{
			"Secure VPN Config Generator",
			"Generate custom WireGuard and OpenVPN configs optimized for privacy. Includes killswitch settings and DNS leak protection.",
			"software",
			50, "KNT",
			"https://picsum.photos/seed/vpn/400/300",
			"CipherPunk", "node_cipherpunk_001",
		},
		{
			"Privacy-Focused Linux ISO",
			"Custom hardened Linux distribution with pre-configured Tor, encrypted home, and security tools. Based on Debian.",
			"software",
			0, "KNT",
			"https://picsum.photos/seed/linux/400/300",
			"TuxMaster", "node_tuxmaster_002",
		},
		{
			"Encrypted Cloud Storage - 100GB",
			"Zero-knowledge encrypted storage. Your keys, your data. Accessible via KayakNet only. 1 year subscription.",
			"services",
			200, "KNT",
			"https://picsum.photos/seed/cloud/400/300",
			"VaultKeeper", "node_vaultkeeper_003",
		},
		{
			"Anonymous Email Hosting",
			"Private email server accessible through KayakNet. No logs, no tracking. Includes 5 aliases.",
			"services",
			150, "KNT",
			"https://picsum.photos/seed/email/400/300",
			"GhostMail", "node_ghostmail_004",
		},
		{
			"Crypto Privacy Consultation",
			"1 hour consultation on cryptocurrency privacy: mixing strategies, privacy coins, avoiding chain analysis.",
			"consulting",
			100, "KNT",
			"https://picsum.photos/seed/crypto/400/300",
			"SatoshiGhost", "node_satoshighost_005",
		},
		{
			"Custom Onion Router Setup",
			"Help setting up your own Tor relay or bridge. Includes server hardening and monitoring setup.",
			"consulting",
			75, "KNT",
			"https://picsum.photos/seed/onion/400/300",
			"RelayRunner", "node_relayrunner_006",
		},
		{
			"Secure Messaging Bot Development",
			"Custom bot development for Signal, Matrix, or XMPP. Privacy-focused, self-hosted solutions.",
			"development",
			300, "KNT",
			"https://picsum.photos/seed/bot/400/300",
			"ByteWeaver", "node_byteweaver_007",
		},
		{
			"Hardware Security Key Programming",
			"Custom firmware for YubiKey clones. FIDO2/U2F with custom attestation.",
			"hardware",
			250, "KNT",
			"https://picsum.photos/seed/hardware/400/300",
			"ChipWizard", "node_chipwizard_008",
		},
		{
			"Decentralized Website Hosting",
			"Host your .kyk website on KayakNet. Censorship-resistant, always online. 1 year hosting.",
			"services",
			120, "KNT",
			"https://picsum.photos/seed/web/400/300",
			"NetNomad", "node_netnomad_009",
		},
		{
			"OSINT Training Course",
			"10-part video course on open source intelligence gathering. Learn to investigate while staying anonymous.",
			"education",
			180, "KNT",
			"https://picsum.photos/seed/osint/400/300",
			"ShadowScout", "node_shadowscout_010",
		},
		{
			"Secure Code Audit Service",
			"Security review of your application code. Focus on cryptography, authentication, and data handling.",
			"consulting",
			500, "KNT",
			"https://picsum.photos/seed/audit/400/300",
			"BugHunter", "node_bughunter_011",
		},
		{
			"Private DNS Server Setup",
			"Set up your own encrypted DNS server. DoH/DoT support. No logging, full privacy.",
			"services",
			60, "KNT",
			"https://picsum.photos/seed/dns/400/300",
			"DNSNinja", "node_dnsninja_012",
		},
		{
			"Encrypted Backup Service - 500GB",
			"Distributed, encrypted backups across KayakNet. Data split across multiple nodes.",
			"services",
			350, "KNT",
			"https://picsum.photos/seed/backup/400/300",
			"DataVault", "node_datavault_013",
		},
		{
			"Anonymous Domain Registration Guide",
			"Complete guide to registering domains anonymously. Includes privacy-friendly registrars list.",
			"education",
			25, "KNT",
			"https://picsum.photos/seed/domain/400/300",
			"DomainGhost", "node_domainghost_014",
		},
		{
			"P2P File Sharing Node Setup",
			"Configure your own BitTorrent node with VPN, blocklists, and RSS automation.",
			"consulting",
			40, "KNT",
			"https://picsum.photos/seed/torrent/400/300",
			"SeedMaster", "node_seedmaster_015",
		},
		{
			"Steganography Toolkit",
			"Advanced tools for hiding data in images, audio, and video. Includes detection evasion techniques.",
			"software",
			80, "KNT",
			"https://picsum.photos/seed/stego/400/300",
			"HiddenByte", "node_hiddenbyte_016",
		},
	}

	for _, s := range samples {
		m.AddSampleListing(s.title, s.desc, s.category, s.price, s.currency, s.image, s.seller, s.sellerID)
		
		// Add seller profile
		m.AddSampleProfile(s.sellerID, s.seller, 
			"Trusted seller on KayakNet. Fast shipping, great communication.",
			int64(500+rand.Intn(5000)), 
			3.5+float64(rand.Intn(15))/10.0,
			int64(10+rand.Intn(100)))
	}
	
	// Add sample reviews
	sampleReviews := []struct {
		buyer   string
		rating  int
		comment string
	}{
		{"SilentBuyer", 5, "Excellent service! Fast delivery and exactly as described. Will buy again."},
		{"CryptoNomad", 5, "Perfect transaction. Seller was very responsive and helpful."},
		{"AnonUser42", 4, "Good product, shipping took a bit longer than expected but seller kept me updated."},
		{"DarknetVet", 5, "Top seller! Been buying from them for months. Always reliable."},
		{"PrivacyFirst", 4, "Solid product. Would recommend to others looking for privacy tools."},
		{"SecureSeeker", 5, "Amazing quality and the seller went above and beyond to help me set it up."},
		{"GhostRunner", 4, "Good experience overall. Product works as advertised."},
		{"ShadowBuyer", 5, "Fast shipping, great communication. A++ seller!"},
		{"VPNLover", 4, "Works great. Had a small issue but seller resolved it quickly."},
		{"TorUser99", 5, "Best purchase I've made on KayakNet. Highly recommended!"},
	}
	
	// Add reviews to random listings
	listings := m.Browse("")
	for i, listing := range listings {
		if i >= len(sampleReviews) {
			break
		}
		review := sampleReviews[i%len(sampleReviews)]
		m.AddSampleReview(listing.ID, listing.SellerID, review.buyer, review.rating, review.comment)
		// Add a couple more reviews per listing
		if i+3 < len(sampleReviews) {
			m.AddSampleReview(listing.ID, listing.SellerID, sampleReviews[(i+3)%len(sampleReviews)].buyer, 
				sampleReviews[(i+3)%len(sampleReviews)].rating, sampleReviews[(i+3)%len(sampleReviews)].comment)
		}
	}
	
	log.Printf("[MARKET] Added %d sample listings with reviews and profiles", len(samples))
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

	// Populate sample listings for demo
	populateSampleListings(n.marketplace)

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

	// Create homepage (served at home.kyk and kayaknet.kyk)
	n.homepage = NewHomepage(n)

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
		if h.node != nil && room != "" && message != "" {
			h.node.chatMgr.SendMessage(room, message)
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	room := r.URL.Query().Get("room")
	if room == "" {
		room = "general"
	}

	var messages []map[string]string
	if h.node != nil {
		for _, m := range h.node.chatMgr.GetMessages(room, 50) {
			messages = append(messages, map[string]string{
				"room":      m.Room,
				"nick":      m.Nick,
				"content":   m.Content,
				"timestamp": m.Timestamp.Format("15:04:05"),
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)
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
                    <div style="margin-bottom:15px"><label>PRICE (KNT):</label><br><input type="number" id="new-price" min="0" required style="width:100%"></div>
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
            const priceText = l.price === 0 ? 'FREE' : l.price + ' ' + (l.currency || 'KNT');
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
            const priceText = l.price === 0 ? 'FREE' : l.price + ' ' + (l.currency || 'KNT');
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
                '<div style="margin-bottom:15px"><label>QUANTITY:</label><br><input type="number" id="order-qty" value="1" min="1" style="width:100%"></div>' +
                '<div style="margin-bottom:15px"><label>MESSAGE TO SELLER:</label><br><textarea id="order-msg" placeholder="Any special requests..." style="width:100%"></textarea></div>' +
                '<div style="margin-bottom:15px"><label>DELIVERY ADDRESS (encrypted):</label><br><textarea id="order-addr" placeholder="Your delivery details..." style="width:100%"></textarea></div>' +
                '<div style="padding:15px;border:1px solid var(--amber);margin-bottom:15px;color:var(--amber)">' +
                'ESCROW: Funds will be held securely until you confirm receipt.' +
                '</div>' +
                '<button type="submit" class="btn">PLACE ORDER</button>' +
                '</form>';
            closeModal('listing-modal');
            showModal('order-modal');
        }
        
        async function placeOrder(e, listingId) {
            e.preventDefault();
            alert('Order placed! In a real implementation, this would create an order with escrow.');
            closeModal('order-modal');
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
            document.getElementById('orders-list').innerHTML = 
                '<div class="order">' +
                '<div class="order-header">' +
                '<span>No orders yet</span>' +
                '</div>' +
                '<p style="color:var(--green-dim)">Your ' + type + ' orders will appear here.</p>' +
                '</div>';
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
            document.getElementById('my-listings').innerHTML =
                '<p style="color:var(--green-dim)">Your listings will appear here. Use the CLI to create listings: sell [title] [price] [description]</p>';
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
            alert('Listing created! In production, this would broadcast to the network.');
            closeModal('create-modal');
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
        :root { --bg: #000; --green: #00ff00; --green-dim: #00aa00; --green-glow: #00ff0066; --border: #00ff0033; }
        body { font-family: 'VT323', monospace; background: var(--bg); color: var(--green); height: 100vh; display: flex; flex-direction: column; }
        body::before { content: ""; position: fixed; top: 0; left: 0; right: 0; bottom: 0; background: repeating-linear-gradient(0deg, rgba(0,0,0,0.15) 0px, rgba(0,0,0,0.15) 1px, transparent 1px, transparent 2px); pointer-events: none; z-index: 1000; }
        .container { max-width: 1000px; margin: 0 auto; padding: 20px; width: 100%; flex: 1; display: flex; flex-direction: column; }
        .terminal { border: 1px solid var(--green); background: #0a0a0a; box-shadow: 0 0 20px var(--green-glow); flex: 1; display: flex; flex-direction: column; }
        .term-header { background: var(--green); color: var(--bg); padding: 5px 15px; font-size: 14px; flex-shrink: 0; }
        .term-body { padding: 15px; flex: 1; display: flex; flex-direction: column; min-height: 0; }
        header { display: flex; justify-content: space-between; align-items: center; padding: 10px 0; border-bottom: 1px dashed var(--border); flex-shrink: 0; }
        .logo { font-size: 24px; color: var(--green); text-decoration: none; text-shadow: 0 0 10px var(--green-glow); }
        .logo::before { content: "["; } .logo::after { content: "]"; }
        nav { display: flex; gap: 15px; }
        nav a { color: var(--green-dim); text-decoration: none; padding: 5px 10px; border: 1px solid transparent; }
        nav a:hover, nav a.active { color: var(--green); border-color: var(--green); }
        .chat-container { display: flex; flex: 1; margin-top: 15px; gap: 15px; min-height: 0; }
        .rooms { width: 180px; border: 1px solid var(--border); padding: 10px; flex-shrink: 0; }
        .rooms h3 { margin-bottom: 10px; font-size: 14px; color: var(--green-dim); }
        .room { padding: 8px; cursor: pointer; margin-bottom: 5px; border: 1px solid transparent; }
        .room:hover, .room.active { border-color: var(--green); color: var(--green); text-shadow: 0 0 5px var(--green-glow); }
        .messages-container { flex: 1; display: flex; flex-direction: column; border: 1px solid var(--border); min-height: 0; }
        .messages { flex: 1; padding: 15px; overflow-y: auto; background: var(--bg); }
        .message { margin-bottom: 12px; font-size: 16px; }
        .message .nick { color: var(--green); }
        .message .nick::before { content: "<"; color: var(--green-dim); }
        .message .nick::after { content: ">"; color: var(--green-dim); }
        .message .time { color: var(--green-dim); font-size: 12px; margin-left: 10px; }
        .message .content { margin-top: 3px; color: var(--green-dim); padding-left: 20px; }
        .input-area { padding: 10px; border-top: 1px dashed var(--border); display: flex; gap: 10px; background: #0a0a0a; }
        .input-area input { flex: 1; padding: 10px; background: var(--bg); border: 1px solid var(--green); color: var(--green); font-family: inherit; font-size: 16px; }
        .input-area input:focus { outline: none; box-shadow: 0 0 10px var(--green-glow); }
        .input-area button { padding: 10px 20px; background: var(--green); color: var(--bg); border: none; cursor: pointer; font-family: inherit; font-size: 16px; }
    </style>
</head>
<body>
    <div class="container">
        <div class="terminal">
            <div class="term-header">KAYAKNET SECURE CHAT // ONION ROUTED</div>
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
                <div class="chat-container">
                    <div class="rooms">
                        <h3>// CHANNELS</h3>
                        <div class="room active" data-room="general">#general</div>
                        <div class="room" data-room="trading">#trading</div>
                        <div class="room" data-room="tech">#tech</div>
                        <div class="room" data-room="random">#random</div>
                    </div>
                    <div class="messages-container">
                        <div class="messages" id="messages">
                            <div class="message">
                                <span class="nick">SYSTEM</span>
                                <span class="time">00:00</span>
                                <div class="content">// Secure channel established. Messages are onion-routed.</div>
                            </div>
                        </div>
                        <div class="input-area">
                            <input type="text" id="message" placeholder="msg://..." />
                            <button onclick="sendMessage()">SEND</button>
                        </div>
                    </div>
                </div>
            </div>
        </div>
    </div>
    <script>
        let currentRoom = 'general';
        document.querySelectorAll('.room').forEach(el => {
            el.addEventListener('click', () => {
                document.querySelectorAll('.room').forEach(r => r.classList.remove('active'));
                el.classList.add('active');
                currentRoom = el.dataset.room;
                loadMessages();
            });
        });
        async function loadMessages() {
            const res = await fetch('/api/chat?room=' + currentRoom);
            const messages = await res.json();
            const container = document.getElementById('messages');
            if (!messages || messages.length === 0) {
                container.innerHTML = '<div class="message"><span class="nick">SYSTEM</span><div class="content">// No messages. Start transmission.</div></div>';
                return;
            }
            container.innerHTML = messages.map(m => '<div class="message"><span class="nick">' + m.nick.toUpperCase() + '</span><span class="time">' + m.timestamp + '</span><div class="content">' + m.content + '</div></div>').join('');
            container.scrollTop = container.scrollHeight;
        }
        async function sendMessage() {
            const input = document.getElementById('message');
            const message = input.value.trim();
            if (!message) return;
            await fetch('/api/chat', { method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, body: 'room=' + currentRoom + '&message=' + encodeURIComponent(message) });
            input.value = '';
            loadMessages();
        }
        document.getElementById('message').addEventListener('keypress', (e) => { if (e.key === 'Enter') sendMessage(); });
        loadMessages();
        setInterval(loadMessages, 3000);
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
