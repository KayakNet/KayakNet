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
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
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
	"github.com/kayaknet/kayaknet/internal/kayaker"
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
	"github.com/kayaknet/kayaknet/internal/updater"
)

// Version is set at build time
var Version = "v0.1.26"

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
	noUpdate    = flag.Bool("no-update", false, "Disable auto-updates")
	checkUpdate = flag.Bool("check-update", false, "Check for updates and exit")
	publicAPI   = flag.Bool("public-api", false, "Expose HTTP API on all interfaces (for bootstrap nodes)")
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
	kayakerMgr   *kayaker.Manager
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

	// Auto-updater
	updater *updater.Updater
	seenMu  sync.RWMutex // Lock for seenMessages
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
	MsgTypePoWChallenge  = 0x40 // Proof-of-work challenge (anti-Sybil)
	MsgTypePoWResponse   = 0x41 // Proof-of-work response
	MsgTypePoWVerified   = 0x42 // PoW verification acknowledgment
	MsgTypeRelay         = 0x50 // Relay message to another peer
	MsgTypeKayakerPost   = 0x60 // Kayaker microblog post
	MsgTypeKayakerProfile = 0x61 // Kayaker profile update
)

// P2PMessage is the wire format
type P2PMessage struct {
	Type      byte            `json:"type"`
	From      string          `json:"from"`
	FromKey   []byte          `json:"from_key"`
	Timestamp int64           `json:"ts"`
	Nonce     uint64          `json:"nonce"`
	TTL       int             `json:"ttl"`    // Time-to-live for gossip (hops remaining)
	MsgID     string          `json:"msg_id"` // Unique message ID for deduplication
	Payload   json.RawMessage `json:"payload"`
	Signature []byte          `json:"sig"`
}

// Homepage serves the main KayakNet portal at home.kyk
type Homepage struct {
	mu        sync.RWMutex
	port      int
	node      *Node
	server    *http.Server
	running   bool
	publicAPI bool // If true, listen on 0.0.0.0 instead of 127.0.0.1
}

// NewHomepage creates a new homepage server
func NewHomepage(node *Node, publicAPI bool) *Homepage {
	return &Homepage{
		port:      8080,
		node:      node,
		publicAPI: publicAPI,
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
	mux.HandleFunc("/api/sync-listing", h.handleSyncListing)
	mux.HandleFunc("/api/my-listings", h.handleMyListings)
	mux.HandleFunc("/api/escrow/create", h.handleEscrowCreate)
	mux.HandleFunc("/api/escrow/status", h.handleEscrowStatus)
	mux.HandleFunc("/api/escrow/pay", h.handleEscrowPay)
	mux.HandleFunc("/api/escrow/manual-confirm", h.handleEscrowManualConfirm)
	mux.HandleFunc("/api/escrow/ship", h.handleEscrowShip)
	mux.HandleFunc("/api/escrow/release", h.handleEscrowRelease)
	mux.HandleFunc("/api/escrow/dispute", h.handleEscrowDispute)
	mux.HandleFunc("/api/escrow/my", h.handleMyEscrows)
	mux.HandleFunc("/api/escrow/all", h.handleAllEscrows)
	mux.HandleFunc("/api/chat", h.handleChat)
	mux.HandleFunc("/api/chat/rooms", h.handleChatRooms)
	mux.HandleFunc("/api/chat/profile", h.handleChatProfile)
	mux.HandleFunc("/api/chat/conversations", h.handleChatConversations)
	mux.HandleFunc("/api/chat/dm", h.handleChatDM)
	mux.HandleFunc("/api/chat/dm/read", h.handleDMRead)
	mux.HandleFunc("/api/chat/dm/typing", h.handleDMTyping)
	mux.HandleFunc("/api/chat/dm/pin", h.handleDMPin)
	mux.HandleFunc("/api/chat/dm/mute", h.handleDMMute)
	mux.HandleFunc("/api/chat/dm/archive", h.handleDMArchive)
	mux.HandleFunc("/api/chat/dm/delete", h.handleDMDelete)
	mux.HandleFunc("/api/chat/dm/search", h.handleDMSearch)
	mux.HandleFunc("/api/chat/dm/draft", h.handleDMDraft)
	mux.HandleFunc("/api/chat/dm/nickname", h.handleDMNickname)
	mux.HandleFunc("/api/chat/dm/clear", h.handleDMClear)
	mux.HandleFunc("/api/chat/dm/unread", h.handleDMUnread)
	mux.HandleFunc("/api/chat/users", h.handleChatUsers)
	mux.HandleFunc("/api/chat/user", h.handleChatUser)
	mux.HandleFunc("/api/chat/search", h.handleChatSearch)
	mux.HandleFunc("/api/chat/reaction", h.handleChatReaction)
	mux.HandleFunc("/api/chat/block", h.handleChatBlock)
	mux.HandleFunc("/api/chat/typing", h.handleChatTyping)
	mux.HandleFunc("/api/chat/edit", h.handleChatEdit)
	mux.HandleFunc("/api/chat/delete", h.handleChatDelete)
	mux.HandleFunc("/api/chat/pin", h.handleChatPin)
	mux.HandleFunc("/api/chat/pinned", h.handleChatPinned)
	mux.HandleFunc("/api/chat/search-users", h.handleChatSearchUsers)
	mux.HandleFunc("/api/domains", h.handleDomains)
	mux.HandleFunc("/api/domains/register", h.handleDomainRegister)
	mux.HandleFunc("/api/domains/my", h.handleMyDomains)
	mux.HandleFunc("/api/domains/update", h.handleDomainUpdate)
	mux.HandleFunc("/api/domains/renew", h.handleDomainRenew)
	mux.HandleFunc("/api/domains/resolve", h.handleDomainResolve)
	mux.HandleFunc("/api/peers", h.handlePeers)
	mux.HandleFunc("/api/stats", h.handleStats)
	mux.HandleFunc("/api/version", h.handleVersion)
	mux.HandleFunc("/api/update", h.handleUpdateCheck)
	mux.HandleFunc("/api/update/apply", h.handleUpdateApply)
	mux.HandleFunc("/marketplace", h.handleMarketplace)
	mux.HandleFunc("/chat", h.handleChatPage)
	mux.HandleFunc("/domains", h.handleDomainsPage)
	mux.HandleFunc("/network", h.handleNetworkPage)
	mux.HandleFunc("/kayaker", h.handleKayakerPage)
	mux.HandleFunc("/api/kayaker/profile", h.handleKayakerProfile)
	mux.HandleFunc("/api/kayaker/posts", h.handleKayakerPosts)
	mux.HandleFunc("/api/kayaker/post/", h.handleKayakerPost)
	mux.HandleFunc("/api/kayaker/feed", h.handleKayakerFeed)
	mux.HandleFunc("/api/kayaker/follow", h.handleKayakerFollow)
	mux.HandleFunc("/api/kayaker/like", h.handleKayakerLike)
	mux.HandleFunc("/api/kayaker/notifications", h.handleKayakerNotifications)
	mux.HandleFunc("/api/kayaker/search", h.handleKayakerSearch)
	mux.HandleFunc("/assets/logo.png", h.handleLogo)

	bindAddr := "127.0.0.1"
	if h.publicAPI {
		bindAddr = "0.0.0.0"
	}
	h.server = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", bindAddr, port),
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

	// Handle --check-update flag
	if *checkUpdate {
		u := updater.NewUpdater(Version)
		release, err := u.GetLatestVersion()
		if err != nil {
			fmt.Printf("Failed to check for updates: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Current version: %s\n", Version)
		fmt.Printf("Latest version:  %s\n", release.TagName)
		if release.TagName > Version {
			fmt.Println("Update available!")
		} else {
			fmt.Println("You are running the latest version.")
		}
		os.Exit(0)
	}

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

	// Start homepage server (for proxy or public API)
	homepagePort := 8080
	if *proxyEnable || *publicAPI {
		if err := node.homepage.Start(homepagePort); err != nil {
			log.Printf("[WARN] Failed to start homepage: %v", err)
		} else if *publicAPI {
			fmt.Printf("║  [+] Public API: http://0.0.0.0:%d                     ║\n", homepagePort)
		}
	}

	// Start browser proxy if enabled
	if *proxyEnable {
		// Tell proxy where homepage is
		node.browserProxy.SetHomepagePort(homepagePort)

		if err := node.browserProxy.Start(*proxyHTTP, *proxySOCKS); err != nil {
			log.Printf("[WARN] Failed to start browser proxy: %v", err)
		} else {
			fmt.Printf("║  [+] Browser proxy: HTTP %d, SOCKS5 %d                 ║\n", *proxyHTTP, *proxySOCKS)
			fmt.Println("║  [+] Homepage: http://home.kyk                         ║")
			fmt.Println("║  [+] Chat: http://chat.kyk  Kayaker: http://kayaker.kyk║")
			fmt.Println("║  [+] Market: http://market.kyk                         ║")
		}
	}
	fmt.Println("╚════════════════════════════════════════════════════════════╝")

	// Start auto-updater (unless disabled)
	if !*noUpdate {
		node.updater = updater.NewUpdater(Version)
		node.updater.SetUpdateCallback(func(oldVer, newVer string) {
			log.Printf("[UPDATE] Update available: %s -> %s (will apply on restart)", oldVer, newVer)
		})
		node.updater.Start()
		log.Printf("[UPDATE] Auto-updater enabled (current: %s)", Version)
	}

	if *interactive {
		go node.interactiveMode()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\nShutting down...")
	if node.updater != nil {
		node.updater.Stop()
	}
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

	// Create Kayaker microblogging manager
	n.kayakerMgr, err = kayaker.NewManager(*dataDir, id.NodeID(), id.PublicKey(), id.PrivateKey())
	if err != nil {
		log.Printf("Warning: failed to initialize Kayaker: %v", err)
	} else {
		// Set up P2P sync callbacks
		n.kayakerMgr.SetSyncCallbacks(
			func(post *kayaker.Post) {
				// Broadcast new post to network
				if data, err := kayaker.MarshalPost(post); err == nil {
					n.broadcast(MsgTypeKayakerPost, data)
				}
			},
			func(profile *kayaker.Profile) {
				// Broadcast profile update to network
				if data, err := kayaker.MarshalProfile(profile); err == nil {
					n.broadcast(MsgTypeKayakerProfile, data)
				}
			},
		)
	}

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
	n.homepage = NewHomepage(n, *publicAPI)

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

	// Enable persistence for all components
	dataDir := cfg.Node.DataDir

	// Chat persistence
	if err := n.chatMgr.SetDataDir(dataDir); err != nil {
		log.Printf("[CHAT] Warning: Could not enable persistence: %v", err)
	} else {
		log.Printf("[CHAT] Persistence enabled in %s/chat", dataDir)
	}

	// Marketplace persistence
	if err := n.marketplace.SetDataDir(dataDir); err != nil {
		log.Printf("[MARKET] Warning: Could not enable persistence: %v", err)
	}

	// Domain/Names persistence
	if err := n.nameService.SetDataDir(dataDir); err != nil {
		log.Printf("[NAMES] Warning: Could not enable persistence: %v", err)
	}

	// E2E key persistence
	n.e2e.SetDataDir(dataDir)

	// Escrow persistence
	if err := n.escrowMgr.SetDataDir(dataDir); err != nil {
		log.Printf("[ESCROW] Warning: Could not enable persistence: %v", err)
	}

	// Security persistence (bans, peer scores)
	n.banList.SetDataDir(dataDir)
	n.peerScorer.SetDataDir(dataDir)

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
	log.Println("[NODE] Shutting down and saving state...")
	n.cancel()
	n.mixer.Stop()
	if n.listener != nil {
		n.listener.Close()
	}

	// Save all persistent data
	n.peerStore.Save()
	n.pubsub.Close()

	if n.marketplace != nil {
		n.marketplace.Save()
	}
	if n.chatMgr != nil {
		n.chatMgr.Save()
	}
	if n.kayakerMgr != nil {
		n.kayakerMgr.Close()
	}
	if n.nameService != nil {
		n.nameService.Save()
	}
	if n.escrowMgr != nil {
		n.escrowMgr.Save()
	}
	if n.banList != nil {
		n.banList.Save()
	}
	if n.peerScorer != nil {
		n.peerScorer.Save()
	}
	if n.e2e != nil {
		n.e2e.Save()
	}

	log.Println("[NODE] State saved successfully")
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
	case MsgTypeRelay:
		n.handleRelay(from, msg)
	case MsgTypeKayakerPost:
		n.handleKayakerPost(msg)
	case MsgTypeKayakerProfile:
		n.handleKayakerProfile(msg)
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
	forwarded := 0
	for _, peer := range peers {
		addr, err := net.ResolveUDPAddr("udp", peer.Address)
		if err != nil {
			continue
		}
		n.listener.WriteTo(data, addr)
		forwarded++
	}

	if msg.Type == MsgTypeChat && forwarded > 0 {
		log.Printf("[GOSSIP] Forwarded chat from %s to %d peers (TTL: %d)", msg.From[:8], forwarded, forwardMsg.TTL)
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
	log.Printf("[CHAT] Received chat from %s (TTL: %d, MsgID: %s)", msg.From[:8], msg.TTL, msg.MsgID[:8])

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
			log.Printf("[CHAT] E2E decryption failed (not for us)")
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

// handleRelay processes relay messages - forward to the intended recipient
func (n *Node) handleRelay(from net.Addr, msg *P2PMessage) {
	var relay struct {
		FinalDest string `json:"dest"`
		MsgType   byte   `json:"type"`
		Payload   []byte `json:"payload"`
	}
	if err := json.Unmarshal(msg.Payload, &relay); err != nil {
		return
	}

	// Check if the final destination is us
	if relay.FinalDest == n.identity.NodeID() {
		// Process the inner message
		innerMsg := &P2PMessage{
			Type:      relay.MsgType,
			From:      msg.From,
			FromKey:   msg.FromKey,
			Timestamp: msg.Timestamp,
			Payload:   relay.Payload,
			Signature: msg.Signature,
		}
		// Handle based on inner type
		switch relay.MsgType {
		case MsgTypeChat:
			n.handleChat(innerMsg)
		}
		return
	}

	// Check if we're connected to the destination
	n.mu.RLock()
	dest, ok := n.connections[relay.FinalDest]
	n.mu.RUnlock()

	if ok {
		// Forward the inner message to the destination
		innerMsg := P2PMessage{
			Type:      relay.MsgType,
			From:      msg.From,
			FromKey:   msg.FromKey,
			Timestamp: msg.Timestamp,
			Nonce:     security.GenerateNonce(),
			Payload:   relay.Payload,
			Signature: msg.Signature,
		}
		data, _ := json.Marshal(innerMsg)
		addr, err := net.ResolveUDPAddr("udp", dest.Address)
		if err == nil {
			n.listener.WriteTo(data, addr)
			log.Printf("[RELAY] Forwarded message from %s to %s", msg.From[:8], relay.FinalDest[:8])
		}
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

// handleKayakerPost processes Kayaker posts from the network
func (n *Node) handleKayakerPost(msg *P2PMessage) {
	if n.kayakerMgr == nil {
		return
	}

	post, err := kayaker.UnmarshalPost(msg.Payload)
	if err != nil {
		return
	}

	if err := n.kayakerMgr.ImportPost(post); err != nil {
		return
	}

	log.Printf("[KAYAKER] Imported post %s from @%s", post.ID[:8], post.AuthorHandle)
}

// handleKayakerProfile processes Kayaker profiles from the network
func (n *Node) handleKayakerProfile(msg *P2PMessage) {
	if n.kayakerMgr == nil {
		return
	}

	profile, err := kayaker.UnmarshalProfile(msg.Payload)
	if err != nil {
		return
	}

	if err := n.kayakerMgr.ImportProfile(profile); err != nil {
		return
	}

	log.Printf("[KAYAKER] Imported profile @%s", profile.Handle)
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
	allPeers := make([]*PeerConn, 0, len(n.connections))
	for _, p := range n.connections {
		allPeers = append(allPeers, p)
	}
	n.mu.RUnlock()

	// If not directly connected, relay through a connected peer (like bootstrap)
	if !ok {
		// Try to relay through any connected peer
		if len(allPeers) > 0 {
			return n.relayToPeer(destID, msgType, payload, allPeers)
		}
		return fmt.Errorf("peer not found and no relay available")
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

// relayToPeer sends a message to a peer we're not directly connected to
// by relaying through connected peers (like bootstrap)
func (n *Node) relayToPeer(destID string, msgType byte, payload []byte, relays []*PeerConn) error {
	// Create a relay message that asks the relay to forward to destID
	relayMsg := struct {
		FinalDest string `json:"dest"`
		MsgType   byte   `json:"type"`
		Payload   []byte `json:"payload"`
	}{destID, msgType, payload}

	relayData, _ := json.Marshal(relayMsg)

	// Create the outer message
	msg := P2PMessage{
		Type:      MsgTypeRelay,
		From:      n.identity.NodeID(),
		FromKey:   n.identity.PublicKey(),
		Timestamp: time.Now().UnixNano(),
		Nonce:     security.GenerateNonce(),
		Payload:   relayData,
	}
	toSign, _ := json.Marshal(struct {
		Type      byte   `json:"type"`
		From      string `json:"from"`
		Timestamp int64  `json:"ts"`
		Payload   []byte `json:"payload"`
	}{msg.Type, msg.From, msg.Timestamp, msg.Payload})
	msg.Signature = n.identity.Sign(toSign)

	data, _ := json.Marshal(msg)

	// Send to first available relay
	for _, relay := range relays {
		addr, err := net.ResolveUDPAddr("udp", relay.Address)
		if err != nil {
			continue
		}
		_, err = n.listener.WriteTo(data, addr)
		if err == nil {
			log.Printf("[RELAY] Sent DM to %s via relay %s", destID[:8], relay.NodeID[:8])
			return nil
		}
	}
	return fmt.Errorf("failed to relay message")
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

		// Sync data from bootstrap HTTP API
		go n.syncFromBootstrap(addrStr)
	}
}

// syncFromBootstrap fetches and syncs data from bootstrap node
func (n *Node) syncFromBootstrap(addrStr string) {
	// Extract host from address (remove port, add HTTP port)
	host := strings.Split(addrStr, ":")[0]
	baseURL := fmt.Sprintf("http://%s:8080", host)

	log.Printf("[SYNC] Syncing data from bootstrap %s", baseURL)

	// Sync all data
	n.syncListingsFromURL(baseURL + "/api/listings")
	n.syncDomainsFromURL(baseURL + "/api/domains")
	n.syncChatFromURL(baseURL)

	// Start periodic sync
	go n.periodicSync(baseURL)
}

func (n *Node) syncListingsFromURL(url string) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		log.Printf("[SYNC] Failed to fetch listings: %v", err)
		return
	}
	defer resp.Body.Close()

	var listings []struct {
		ID          string  `json:"id"`
		Title       string  `json:"title"`
		Description string  `json:"description"`
		Price       float64 `json:"price"`
		Currency    string  `json:"currency"`
		Category    string  `json:"category"`
		Image       string  `json:"image"`
		SellerID    string  `json:"seller_id"`
		SellerName  string  `json:"seller_name"`
		CreatedAt   string  `json:"created_at"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&listings); err != nil {
		log.Printf("[SYNC] Failed to decode listings: %v", err)
		return
	}

	imported := 0
	for _, l := range listings {
		existing, _ := n.marketplace.GetListing(l.ID)
		if existing == nil {
			createdAt, _ := time.Parse(time.RFC3339, l.CreatedAt)
			if createdAt.IsZero() {
				createdAt = time.Now()
			}
			newListing := &market.Listing{
				ID:          l.ID,
				Title:       l.Title,
				Description: l.Description,
				Price:       l.Price,
				Currency:    l.Currency,
				Category:    l.Category,
				Image:       l.Image,
				SellerID:    l.SellerID,
				SellerName:  l.SellerName,
				CreatedAt:   createdAt,
				ExpiresAt:   createdAt.Add(30 * 24 * time.Hour),
				Active:      true,
			}
			n.marketplace.ImportListing(newListing)
			imported++
		}
	}
	if imported > 0 {
		log.Printf("[SYNC] Imported %d listings from bootstrap", imported)
	}
}

func (n *Node) syncDomainsFromURL(url string) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	var domains []struct {
		Name        string `json:"name"`
		FullName    string `json:"full_name"`
		Description string `json:"description"`
		Owner       string `json:"owner"`
		ServiceType string `json:"service_type"`
		CreatedAt   string `json:"created_at"`
		ExpiresAt   string `json:"expires_at"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&domains); err != nil {
		return
	}

	imported := 0
	for _, d := range domains {
		existing, _ := n.nameService.Resolve(d.Name)
		if existing == nil {
			createdAt, _ := time.Parse(time.RFC3339, d.CreatedAt)
			expiresAt, _ := time.Parse(time.RFC3339, d.ExpiresAt)
			if createdAt.IsZero() {
				createdAt = time.Now()
			}
			if expiresAt.IsZero() {
				expiresAt = createdAt.Add(365 * 24 * time.Hour)
			}
			n.nameService.ImportDomain(d.Name, d.Description, d.Owner, d.ServiceType, createdAt, expiresAt)
			imported++
		}
	}
	if imported > 0 {
		log.Printf("[SYNC] Imported %d domains from bootstrap", imported)
	}
}

func (n *Node) syncChatFromURL(baseURL string) {
	// First get all rooms from bootstrap
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(baseURL + "/api/chat/rooms")
	if err != nil {
		// Fallback to default rooms
		for _, room := range []string{"general", "market", "help", "random", "tech", "privacy"} {
			n.syncChatRoom(baseURL, room)
		}
		return
	}
	defer resp.Body.Close()

	var rooms []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rooms); err != nil {
		return
	}

	// Sync all rooms
	for _, room := range rooms {
		n.syncChatRoom(baseURL, room.Name)
	}
}

func (n *Node) syncChatRoom(baseURL, room string) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(fmt.Sprintf("%s/api/chat?room=%s", baseURL, room))
	if err != nil {
		return
	}
	defer resp.Body.Close()

	var messages []struct {
		ID        string `json:"id"`
		Room      string `json:"room"`
		SenderID  string `json:"sender_id"`
		Nick      string `json:"nick"`
		Content   string `json:"content"`
		Timestamp string `json:"timestamp"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&messages); err != nil {
		return
	}

	imported := 0
	for _, m := range messages {
		ts, _ := time.Parse(time.RFC3339, m.Timestamp)
		if ts.IsZero() {
			ts = time.Now()
		}
		if n.chatMgr.ImportMessage(m.ID, room, m.SenderID, m.Nick, m.Content, ts) {
			imported++
		}
	}
	if imported > 0 {
		log.Printf("[SYNC] Imported %d chat messages from bootstrap (room: %s)", imported, room)
	}
}

func (n *Node) periodicSync(baseURL string) {
	// Sync every 10 seconds for faster message propagation
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			n.syncListingsFromURL(baseURL + "/api/listings")
			n.syncDomainsFromURL(baseURL + "/api/domains")
			n.syncChatFromURL(baseURL)
		}
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

			// Periodic save of all persistent data
			n.peerStore.Save()
			n.marketplace.Save()
			n.chatMgr.Save()
			n.nameService.Save()
			n.escrowMgr.Save()
			n.banList.Save()
			n.peerScorer.Save()
			n.e2e.Save()
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
		case "update":
			n.cmdUpdate()
		case "version":
			fmt.Printf("KayakNet %s\n", Version)
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
| SYSTEM                                                      |
|   update             - Check for updates and apply          |
|   version            - Show current version                 |
|   quit/exit          - Exit KayakNet                        |
+-------------------------------------------------------------+

HOMEPAGE: Browse to http://home.kyk or http://kayaknet.kyk
`)
}

func (n *Node) cmdUpdate() {
	fmt.Printf("[UPDATE] Current version: %s\n", Version)
	fmt.Println("[UPDATE] Checking for updates...")

	if n.updater == nil {
		// Create temporary updater if disabled
		n.updater = updater.NewUpdater(Version)
	}

	release, err := n.updater.GetLatestVersion()
	if err != nil {
		fmt.Printf("[UPDATE] Failed to check: %v\n", err)
		return
	}

	fmt.Printf("[UPDATE] Latest version: %s\n", release.TagName)

	if release.TagName <= Version {
		fmt.Println("[UPDATE] You are running the latest version.")
		return
	}

	fmt.Println("[UPDATE] New version available! Downloading...")
	if err := n.updater.CheckAndUpdate(); err != nil {
		fmt.Printf("[UPDATE] Failed to update: %v\n", err)
		return
	}

	fmt.Println("[UPDATE] Update successful! Please restart KayakNet to use the new version.")
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
	sellerXMRAddr := r.FormValue("seller_xmr_address")
	sellerZECAddr := r.FormValue("seller_zec_address")

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
		sellerXMRAddr,
		sellerZECAddr,
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

func (h *Homepage) handleSyncListing(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.node == nil {
		http.Error(w, "Node not available", http.StatusServiceUnavailable)
		return
	}

	var listing struct {
		ID          string  `json:"id"`
		Title       string  `json:"title"`
		Description string  `json:"description"`
		Price       float64 `json:"price"`
		Currency    string  `json:"currency"`
		Category    string  `json:"category"`
		Image       string  `json:"image"`
		SellerID    string  `json:"seller_id"`
		SellerName  string  `json:"seller_name"`
		CreatedAt   string  `json:"created_at"`
	}

	if err := json.NewDecoder(r.Body).Decode(&listing); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if listing.ID == "" || listing.Title == "" {
		http.Error(w, "ID and title required", http.StatusBadRequest)
		return
	}

	// Check if listing already exists
	existing, _ := h.node.marketplace.GetListing(listing.ID)
	if existing != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"status": "exists"})
		return
	}

	// Parse created time
	createdAt, _ := time.Parse(time.RFC3339, listing.CreatedAt)
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	// Import the listing
	newListing := &market.Listing{
		ID:          listing.ID,
		Title:       listing.Title,
		Description: listing.Description,
		Price:       listing.Price,
		Currency:    listing.Currency,
		Category:    listing.Category,
		Image:       listing.Image,
		SellerID:    listing.SellerID,
		SellerName:  listing.SellerName,
		CreatedAt:   createdAt,
		ExpiresAt:   createdAt.Add(30 * 24 * time.Hour),
		Active:      true,
	}

	h.node.marketplace.ImportListing(newListing)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "imported", "id": listing.ID})
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

	// Check for forwarded params (from user nodes to bootstrap)
	forwardedSellerAddr := r.FormValue("seller_address")
	forwardedSellerID := r.FormValue("seller_id")
	forwardedBuyer := r.FormValue("buyer")
	forwardedAmount := r.FormValue("amount")
	forwardedTitle := r.FormValue("listing_title")

	if listingID == "" || currency == "" {
		http.Error(w, "Missing required fields", http.StatusBadRequest)
		return
	}

	// Validate currency
	cryptoType := escrow.CryptoType(currency)
	if cryptoType != escrow.CryptoXMR && cryptoType != escrow.CryptoZEC {
		http.Error(w, "Invalid currency. Use XMR or ZEC", http.StatusBadRequest)
		return
	}

	var listingTitle string
	var sellerID string
	var sellerAddress string
	var cryptoAmount float64
	var buyerID string

	// If this is a forwarded request from another node (bootstrap receiving from user node)
	if forwardedSellerAddr != "" && forwardedSellerID != "" {
		// Use forwarded params
		sellerAddress = forwardedSellerAddr
		sellerID = forwardedSellerID
		listingTitle = forwardedTitle
		if forwardedBuyer != "" {
			buyerID = forwardedBuyer
		} else {
			buyerID = h.node.identity.NodeID()
		}
		if forwardedAmount != "" {
			cryptoAmount, _ = strconv.ParseFloat(forwardedAmount, 64)
		}
	} else {
		// Get listing details locally
		listing, err := h.node.marketplace.GetListing(listingID)
		if err != nil {
			http.Error(w, "Listing not found", http.StatusNotFound)
			return
		}

		listingTitle = listing.Title
		sellerID = listing.SellerID
		buyerID = h.node.identity.NodeID()

		// Convert price to crypto amount
		rate, _ := escrow.GetExchangeRate(cryptoType)
		if rate > 0 {
			cryptoAmount = float64(listing.Price) / rate
		} else {
			cryptoAmount = float64(listing.Price) / 100
		}

		// Get seller's crypto address from listing
		if currency == "XMR" {
			sellerAddress = listing.SellerXMRAddress
		} else if currency == "ZEC" {
			sellerAddress = listing.SellerZECAddress
		}
	}

	// Validate seller has an address for this currency
	if sellerAddress == "" {
		http.Error(w, fmt.Sprintf("Seller has not provided a %s address", currency), http.StatusBadRequest)
		return
	}

	// Check if local node has crypto configured, if not forward to bootstrap
	if !h.node.cryptoWallet.IsConfigured() {
		// Forward to bootstrap node with all listing details
		bootstrapURL := "http://203.161.33.237:8080/api/escrow/create"

		formData := url.Values{}
		formData.Set("listing_id", listingID)
		formData.Set("listing_title", listingTitle)
		formData.Set("currency", currency)
		formData.Set("buyer_address", buyerAddress)
		formData.Set("delivery_info", deliveryInfo)
		formData.Set("buyer", buyerID)
		formData.Set("seller_id", sellerID)
		formData.Set("seller_address", sellerAddress)
		formData.Set("amount", fmt.Sprintf("%.8f", cryptoAmount))

		resp, err := http.PostForm(bootstrapURL, formData)
		if err != nil {
			http.Error(w, "Failed to contact escrow service: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer resp.Body.Close()

		// Forward response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}

	// Create escrow locally (for bootstrap node)
	params := escrow.EscrowParams{
		OrderID:       generateOrderID(),
		ListingID:     listingID,
		ListingTitle:  listingTitle,
		BuyerID:       buyerID,
		BuyerAddress:  buyerAddress,
		SellerID:      sellerID,
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

	// If local node doesn't have crypto configured, forward to bootstrap
	if !h.node.cryptoWallet.IsConfigured() {
		bootstrapURL := "http://203.161.33.237:8080/api/escrow/status"
		if escrowID != "" {
			bootstrapURL += "?id=" + url.QueryEscape(escrowID) + "&node_id=" + url.QueryEscape(h.node.identity.NodeID())
		} else if orderID != "" {
			bootstrapURL += "?order_id=" + url.QueryEscape(orderID) + "&node_id=" + url.QueryEscape(h.node.identity.NodeID())
		}

		resp, err := http.Get(bootstrapURL)
		if err != nil {
			http.Error(w, "Failed to contact escrow service: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer resp.Body.Close()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
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

	// Determine if current user is buyer or seller
	nodeID := r.URL.Query().Get("node_id")
	if nodeID == "" {
		nodeID = h.node.identity.NodeID()
	}
	isBuyer := esc.BuyerID == nodeID
	isSeller := esc.SellerID == nodeID

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"escrow_id":       esc.ID,
		"order_id":        esc.OrderID,
		"listing_id":      esc.ListingID,
		"listing_title":   esc.ListingTitle,
		"buyer_id":        esc.BuyerID,
		"seller_id":       esc.SellerID,
		"is_buyer":        isBuyer,
		"is_seller":       isSeller,
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

// handleEscrowManualConfirm allows manual payment confirmation (for testing or when auto-detect fails)
func (h *Homepage) handleEscrowManualConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	escrowID := r.FormValue("escrow_id")
	txID := r.FormValue("tx_id") // Optional transaction ID for record

	if h.node == nil || escrowID == "" {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// Forward to bootstrap if not configured locally
	if !h.node.cryptoWallet.IsConfigured() {
		formData := url.Values{}
		formData.Set("escrow_id", escrowID)
		formData.Set("tx_id", txID)
		resp, err := http.PostForm("http://203.161.33.237:8080/api/escrow/manual-confirm", formData)
		if err != nil {
			http.Error(w, "Failed to contact escrow service: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}

	// Manually mark escrow as funded
	err := h.node.escrowMgr.ManualConfirmPayment(escrowID, txID)
	if err != nil {
		http.Error(w, "Failed to confirm payment: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Payment manually confirmed. Order is now funded.",
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

	// Forward to bootstrap if not configured locally
	if !h.node.cryptoWallet.IsConfigured() {
		formData := url.Values{}
		formData.Set("escrow_id", escrowID)
		formData.Set("tracking_info", trackingInfo)
		resp, err := http.PostForm("http://203.161.33.237:8080/api/escrow/ship", formData)
		if err != nil {
			http.Error(w, "Failed to contact escrow service: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
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

	// Forward to bootstrap if not configured locally
	if !h.node.cryptoWallet.IsConfigured() {
		formData := url.Values{}
		formData.Set("escrow_id", escrowID)
		resp, err := http.PostForm("http://203.161.33.237:8080/api/escrow/release", formData)
		if err != nil {
			http.Error(w, "Failed to contact escrow service: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
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

	// Forward to bootstrap if not configured locally
	if !h.node.cryptoWallet.IsConfigured() {
		formData := url.Values{}
		formData.Set("escrow_id", escrowID)
		formData.Set("reason", reason)
		resp, err := http.PostForm("http://203.161.33.237:8080/api/escrow/dispute", formData)
		if err != nil {
			http.Error(w, "Failed to contact escrow service: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
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

	// Forward to bootstrap if not configured locally
	if !h.node.cryptoWallet.IsConfigured() {
		bootstrapURL := "http://203.161.33.237:8080/api/escrow/my?role=" + url.QueryEscape(role) + "&node_id=" + url.QueryEscape(h.node.identity.NodeID())
		resp, err := http.Get(bootstrapURL)
		if err != nil {
			http.Error(w, "Failed to contact escrow service: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}

	// For bootstrap, use node_id from query if provided (forwarded request)
	nodeID := r.URL.Query().Get("node_id")
	if nodeID == "" {
		nodeID = h.node.identity.NodeID()
	}

	var escrows []*escrow.Escrow
	if role == "seller" {
		escrows = h.node.escrowMgr.GetSellerEscrows(nodeID)
	} else {
		escrows = h.node.escrowMgr.GetBuyerEscrows(nodeID)
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

func (h *Homepage) handleAllEscrows(w http.ResponseWriter, r *http.Request) {
	if h.node == nil {
		http.Error(w, "Node not available", http.StatusServiceUnavailable)
		return
	}

	// Forward to bootstrap if not configured locally
	if !h.node.cryptoWallet.IsConfigured() {
		bootstrapURL := "http://203.161.33.237:8080/api/escrow/all"
		resp, err := http.Get(bootstrapURL)
		if err != nil {
			http.Error(w, "Failed to contact escrow service: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}

	// Get all escrows (for admin/testing)
	escrows := h.node.escrowMgr.GetAllEscrows()

	var results []map[string]interface{}
	for _, e := range escrows {
		results = append(results, map[string]interface{}{
			"escrow_id":     e.ID,
			"order_id":      e.OrderID,
			"listing_id":    e.ListingID,
			"listing_title": e.ListingTitle,
			"buyer_id":      e.BuyerID,
			"seller_id":     e.SellerID,
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

// handleDMRead marks a DM conversation as read
func (h *Homepage) handleDMRead(w http.ResponseWriter, r *http.Request) {
	if h.node == nil {
		http.Error(w, "Node not available", http.StatusServiceUnavailable)
		return
	}
	userID := r.URL.Query().Get("user")
	if userID == "" {
		userID = r.FormValue("user")
	}
	if userID != "" {
		h.node.chatMgr.MarkDMAsRead(userID)
	}
	w.WriteHeader(http.StatusOK)
}

// handleDMTyping handles typing indicators for DMs
func (h *Homepage) handleDMTyping(w http.ResponseWriter, r *http.Request) {
	if h.node == nil {
		http.Error(w, "Node not available", http.StatusServiceUnavailable)
		return
	}

	userID := r.URL.Query().Get("user")
	if userID == "" {
		userID = r.FormValue("user")
	}

	if r.Method == "POST" {
		typing := r.FormValue("typing") == "true"
		h.node.chatMgr.SetDMTyping(userID, typing)

		// Broadcast typing indicator to the other user
		if typing {
			payload, _ := json.Marshal(map[string]interface{}{
				"type":   "dm_typing",
				"from":   h.node.identity.NodeID(),
				"to":     userID,
				"typing": true,
			})
			h.node.broadcastGossip(MsgTypeChat, payload)
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	// GET - return who's typing
	typing := h.node.chatMgr.GetDMTypingUsers(userID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"typing": typing,
	})
}

// handleDMPin pins/unpins a DM conversation
func (h *Homepage) handleDMPin(w http.ResponseWriter, r *http.Request) {
	if h.node == nil {
		http.Error(w, "Node not available", http.StatusServiceUnavailable)
		return
	}
	userID := r.FormValue("user")
	pinned := r.FormValue("pinned") == "true"
	if userID != "" {
		h.node.chatMgr.PinConversation(userID, pinned)
	}
	w.WriteHeader(http.StatusOK)
}

// handleDMMute mutes/unmutes a DM conversation
func (h *Homepage) handleDMMute(w http.ResponseWriter, r *http.Request) {
	if h.node == nil {
		http.Error(w, "Node not available", http.StatusServiceUnavailable)
		return
	}
	userID := r.FormValue("user")
	muted := r.FormValue("muted") == "true"
	if userID != "" {
		h.node.chatMgr.MuteConversation(userID, muted)
	}
	w.WriteHeader(http.StatusOK)
}

// handleDMArchive archives/unarchives a DM conversation
func (h *Homepage) handleDMArchive(w http.ResponseWriter, r *http.Request) {
	if h.node == nil {
		http.Error(w, "Node not available", http.StatusServiceUnavailable)
		return
	}

	if r.Method == "GET" {
		// Return archived conversations
		archived := h.node.chatMgr.GetArchivedConversations()
		var convs []map[string]interface{}
		for _, c := range archived {
			otherUser := c.Participants[0]
			if otherUser == h.node.identity.NodeID() && len(c.Participants) > 1 {
				otherUser = c.Participants[1]
			}
			convs = append(convs, map[string]interface{}{
				"id":           c.ID,
				"user":         otherUser,
				"last_message": c.LastMessage.Format(time.RFC3339),
				"unread":       c.Unread,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(convs)
		return
	}

	userID := r.FormValue("user")
	archived := r.FormValue("archived") == "true"
	if userID != "" {
		h.node.chatMgr.ArchiveConversation(userID, archived)
	}
	w.WriteHeader(http.StatusOK)
}

// handleDMDelete deletes a message or entire conversation
func (h *Homepage) handleDMDelete(w http.ResponseWriter, r *http.Request) {
	if h.node == nil {
		http.Error(w, "Node not available", http.StatusServiceUnavailable)
		return
	}
	userID := r.FormValue("user")
	messageID := r.FormValue("message_id")
	deleteAll := r.FormValue("all") == "true"

	if userID == "" {
		http.Error(w, "user required", http.StatusBadRequest)
		return
	}

	if deleteAll {
		h.node.chatMgr.DeleteConversation(userID)
	} else if messageID != "" {
		err := h.node.chatMgr.DeleteDMMessage(userID, messageID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

// handleDMSearch searches messages in a DM conversation
func (h *Homepage) handleDMSearch(w http.ResponseWriter, r *http.Request) {
	if h.node == nil {
		http.Error(w, "Node not available", http.StatusServiceUnavailable)
		return
	}
	userID := r.URL.Query().Get("user")
	query := r.URL.Query().Get("q")

	if userID == "" || query == "" {
		http.Error(w, "user and q required", http.StatusBadRequest)
		return
	}

	results := h.node.chatMgr.SearchDMMessages(userID, query)
	var messages []map[string]interface{}
	for _, m := range results {
		messages = append(messages, map[string]interface{}{
			"id":        m.ID,
			"sender_id": m.SenderID,
			"nick":      m.Nick,
			"content":   m.Content,
			"timestamp": m.Timestamp.Format(time.RFC3339),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)
}

// handleDMDraft saves/retrieves draft messages
func (h *Homepage) handleDMDraft(w http.ResponseWriter, r *http.Request) {
	if h.node == nil {
		http.Error(w, "Node not available", http.StatusServiceUnavailable)
		return
	}

	userID := r.URL.Query().Get("user")
	if userID == "" {
		userID = r.FormValue("user")
	}

	if r.Method == "POST" {
		draft := r.FormValue("draft")
		h.node.chatMgr.SetDMDraft(userID, draft)
		w.WriteHeader(http.StatusOK)
		return
	}

	draft := h.node.chatMgr.GetDMDraft(userID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"draft": draft})
}

// handleDMNickname sets/gets custom nicknames
func (h *Homepage) handleDMNickname(w http.ResponseWriter, r *http.Request) {
	if h.node == nil {
		http.Error(w, "Node not available", http.StatusServiceUnavailable)
		return
	}

	userID := r.URL.Query().Get("user")
	if userID == "" {
		userID = r.FormValue("user")
	}

	if r.Method == "POST" {
		nickname := r.FormValue("nickname")
		h.node.chatMgr.SetDMNickname(userID, nickname)
		w.WriteHeader(http.StatusOK)
		return
	}

	nickname := h.node.chatMgr.GetDMNickname(userID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"nickname": nickname})
}

// handleDMClear clears all messages in a conversation
func (h *Homepage) handleDMClear(w http.ResponseWriter, r *http.Request) {
	if h.node == nil {
		http.Error(w, "Node not available", http.StatusServiceUnavailable)
		return
	}
	userID := r.FormValue("user")
	if userID != "" {
		h.node.chatMgr.ClearDMHistory(userID)
	}
	w.WriteHeader(http.StatusOK)
}

// handleDMUnread returns total unread DM count
func (h *Homepage) handleDMUnread(w http.ResponseWriter, r *http.Request) {
	if h.node == nil {
		http.Error(w, "Node not available", http.StatusServiceUnavailable)
		return
	}
	count := h.node.chatMgr.GetTotalUnreadDMs()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int{"unread": count})
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

func (h *Homepage) handleChatTyping(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == "POST" {
		room := r.FormValue("room")
		if h.node != nil && room != "" {
			h.node.chatMgr.SetTyping(room, h.node.identity.NodeID())
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	room := r.URL.Query().Get("room")
	typing := []string{}
	if h.node != nil && room != "" {
		typing = h.node.chatMgr.GetTyping(room)
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"typing": typing})
}

func (h *Homepage) handleChatEdit(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	msgID := r.FormValue("message_id")
	content := r.FormValue("content")
	if h.node != nil && msgID != "" {
		if err := h.node.chatMgr.EditMessage(msgID, content, h.node.identity.NodeID()); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Homepage) handleChatDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	msgID := r.FormValue("message_id")
	if h.node != nil && msgID != "" {
		if err := h.node.chatMgr.DeleteMessage(msgID, h.node.identity.NodeID()); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Homepage) handleChatPin(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	room := r.FormValue("room")
	msgID := r.FormValue("message_id")
	if h.node != nil && room != "" && msgID != "" {
		h.node.chatMgr.PinMessage(room, msgID)
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Homepage) handleChatPinned(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	room := r.URL.Query().Get("room")
	var pinned []*chat.Message
	if h.node != nil && room != "" {
		pinned = h.node.chatMgr.GetPinnedMessages(room)
	}
	json.NewEncoder(w).Encode(pinned)
}

func (h *Homepage) handleChatSearchUsers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	query := r.URL.Query().Get("q")
	var users []*chat.User
	if h.node != nil && query != "" {
		users = h.node.chatMgr.SearchUsers(query)
	}
	json.NewEncoder(w).Encode(users)
}

func (h *Homepage) handleDomains(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	var domains []map[string]interface{}

	if h.node != nil {
		var results []*names.Registration
		if query != "" {
			results = h.node.nameService.Search(query)
		} else {
			results = h.node.nameService.Search("")
		}

		for _, d := range results {
			isOwner := d.NodeID == h.node.identity.NodeID()
			domains = append(domains, map[string]interface{}{
				"name":         d.Name,
				"full_name":    d.Name + ".kyk",
				"description":  d.Description,
				"owner":        d.NodeID,
				"address":      d.Address,
				"service_type": d.ServiceType,
				"created_at":   d.CreatedAt.Format("2006-01-02"),
				"expires_at":   d.ExpiresAt.Format("2006-01-02"),
				"is_owner":     isOwner,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(domains)
}

func (h *Homepage) handleDomainRegister(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "POST" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "POST required",
		})
		return
	}

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		ServiceType string `json:"service_type"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Invalid request body",
		})
		return
	}

	if req.Name == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Domain name is required",
		})
		return
	}

	reg, err := h.node.nameService.Register(req.Name, req.Description, req.ServiceType)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	// Broadcast the registration to the network
	h.broadcastDomainRegistration(reg)

	// Save to disk
	go h.node.nameService.Save()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"domain":  reg.FullName,
		"expires": reg.ExpiresAt.Format("2006-01-02"),
		"message": "Domain registered successfully!",
	})
}

func (h *Homepage) broadcastDomainRegistration(reg *names.Registration) {
	if h.node == nil {
		return
	}

	data, err := reg.Marshal()
	if err != nil {
		return
	}

	// Broadcast via gossip
	h.node.broadcastGossip(MsgTypeNameReg, data)
	log.Printf("[DOMAIN] Broadcast registration: %s", reg.FullName)
}

func (h *Homepage) handleMyDomains(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var domains []map[string]interface{}

	if h.node != nil {
		results := h.node.nameService.MyDomains()

		for _, d := range results {
			domains = append(domains, map[string]interface{}{
				"name":         d.Name,
				"full_name":    d.Name + ".kyk",
				"description":  d.Description,
				"address":      d.Address,
				"service_type": d.ServiceType,
				"created_at":   d.CreatedAt.Format("2006-01-02"),
				"expires_at":   d.ExpiresAt.Format("2006-01-02"),
				"updated_at":   d.UpdatedAt.Format("2006-01-02 15:04"),
			})
		}
	}

	json.NewEncoder(w).Encode(domains)
}

func (h *Homepage) handleDomainUpdate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "POST" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "POST required",
		})
		return
	}

	var req struct {
		Name        string `json:"name"`
		Address     string `json:"address"`
		Description string `json:"description"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Invalid request body",
		})
		return
	}

	if err := h.node.nameService.Update(req.Name, req.Address, req.Description); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	// Save to disk
	go h.node.nameService.Save()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Domain updated successfully!",
	})
}

func (h *Homepage) handleDomainRenew(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "POST" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "POST required",
		})
		return
	}

	var req struct {
		Name string `json:"name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Invalid request body",
		})
		return
	}

	if err := h.node.nameService.Renew(req.Name); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	// Save to disk
	go h.node.nameService.Save()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Domain renewed for another year!",
	})
}

func (h *Homepage) handleDomainResolve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	domain := r.URL.Query().Get("domain")
	if domain == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Domain parameter required",
		})
		return
	}

	reg, err := h.node.nameService.Resolve(domain)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":      true,
		"name":         reg.Name,
		"full_name":    reg.FullName,
		"node_id":      reg.NodeID,
		"address":      reg.Address,
		"description":  reg.Description,
		"service_type": reg.ServiceType,
		"created_at":   reg.CreatedAt.Format("2006-01-02"),
		"expires_at":   reg.ExpiresAt.Format("2006-01-02"),
	})
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
		"version":     Version,
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

func (h *Homepage) handleVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"version": Version,
	})
}

func (h *Homepage) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	result := map[string]interface{}{
		"current_version":  Version,
		"latest_version":   Version,
		"update_available": false,
		"error":            "",
	}

	if h.node != nil && h.node.updater != nil {
		release, err := h.node.updater.GetLatestVersion()
		if err != nil {
			result["error"] = err.Error()
		} else {
			result["latest_version"] = release.TagName
			result["update_available"] = release.TagName > Version
		}
	}

	json.NewEncoder(w).Encode(result)
}

func (h *Homepage) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "POST" {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	if h.node == nil || h.node.updater == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Updater not initialized",
		})
		return
	}

	// Check and apply update
	err := h.node.updater.CheckAndUpdate()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":          true,
		"message":          "Update applied successfully. Please restart KayakNet.",
		"restart_required": true,
	})
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
// Kayaker Handlers - Twitter/Threads-like Microblogging
// ============================================================================

func (h *Homepage) handleKayakerPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(kayakerPageHTML))
}

func (h *Homepage) handleKayakerProfile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	
	if h.node.kayakerMgr == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "Kayaker not initialized"})
		return
	}

	switch r.Method {
	case "GET":
		// Get profile by ID/handle or own profile
		idOrHandle := r.URL.Query().Get("id")
		var profile *kayaker.Profile
		var err error
		if idOrHandle == "" {
			profile, err = h.node.kayakerMgr.GetMyProfile()
		} else {
			profile, err = h.node.kayakerMgr.GetProfile(idOrHandle)
		}
		if err != nil || profile == nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "Profile not found"})
			return
		}
		json.NewEncoder(w).Encode(kayaker.ProfileToJSON(profile))

	case "POST":
		// Create or update profile
		var req struct {
			Handle      string `json:"handle"`
			DisplayName string `json:"display_name"`
			Bio         string `json:"bio"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "Invalid request"})
			return
		}
		profile, err := h.node.kayakerMgr.CreateProfile(req.Handle, req.DisplayName, req.Bio)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(kayaker.ProfileToJSON(profile))

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *Homepage) handleKayakerPosts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	
	if h.node.kayakerMgr == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "Kayaker not initialized"})
		return
	}

	switch r.Method {
	case "POST":
		// Create new post
		var req kayaker.CreatePostRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "Invalid request"})
			return
		}
		if req.Visibility == "" {
			req.Visibility = kayaker.VisibilityPublic
		}
		post, err := h.node.kayakerMgr.CreatePost(req)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
			return
		}
		// Include decrypted content in response
		content, _ := h.node.kayakerMgr.DecryptPostContent(post)
		response := kayaker.PostToJSON(post)
		response["decrypted_content"] = content
		json.NewEncoder(w).Encode(response)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *Homepage) handleKayakerPost(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	
	if h.node.kayakerMgr == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "Kayaker not initialized"})
		return
	}

	// Extract post ID from path /api/kayaker/post/{id}
	postID := strings.TrimPrefix(r.URL.Path, "/api/kayaker/post/")
	if postID == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "Post ID required"})
		return
	}

	switch r.Method {
	case "GET":
		post, err := h.node.kayakerMgr.GetPost(postID)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
			return
		}
		content, _ := h.node.kayakerMgr.DecryptPostContent(post)
		response := kayaker.PostToJSON(post)
		response["decrypted_content"] = content
		
		// Get replies
		repliesResp, _ := h.node.kayakerMgr.GetFeed(kayaker.FeedRequest{
			Type:   kayaker.FeedReplies,
			PostID: postID,
			Limit:  20,
		})
		if repliesResp != nil {
			var replies []map[string]interface{}
			for _, reply := range repliesResp.Posts {
				replyJSON := kayaker.PostToJSON(&reply)
				c, _ := h.node.kayakerMgr.DecryptPostContent(&reply)
				replyJSON["decrypted_content"] = c
				replies = append(replies, replyJSON)
			}
			response["replies"] = replies
		}
		json.NewEncoder(w).Encode(response)

	case "DELETE":
		if err := h.node.kayakerMgr.DeletePost(postID); err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *Homepage) handleKayakerFeed(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	
	if h.node.kayakerMgr == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "Kayaker not initialized"})
		return
	}

	feedType := r.URL.Query().Get("type")
	if feedType == "" {
		feedType = "explore"
	}

	var ft kayaker.FeedType
	switch feedType {
	case "home":
		ft = kayaker.FeedHome
	case "explore":
		ft = kayaker.FeedExplore
	case "profile":
		ft = kayaker.FeedProfile
	default:
		ft = kayaker.FeedExplore
	}

	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 50 {
			limit = parsed
		}
	}

	resp, err := h.node.kayakerMgr.GetFeed(kayaker.FeedRequest{
		Type:   ft,
		UserID: r.URL.Query().Get("user_id"),
		Cursor: r.URL.Query().Get("cursor"),
		Limit:  limit,
	})
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
		return
	}

	// Decrypt post contents
	var posts []map[string]interface{}
	for _, post := range resp.Posts {
		postJSON := kayaker.PostToJSON(&post)
		content, _ := h.node.kayakerMgr.DecryptPostContent(&post)
		postJSON["decrypted_content"] = content
		// Check if current user liked this post
		liked, _ := h.node.kayakerMgr.HasLiked(post.ID)
		postJSON["liked"] = liked
		posts = append(posts, postJSON)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"posts":       posts,
		"next_cursor": resp.NextCursor,
		"has_more":    resp.HasMore,
	})
}

func (h *Homepage) handleKayakerFollow(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	
	if h.node.kayakerMgr == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "Kayaker not initialized"})
		return
	}

	switch r.Method {
	case "GET":
		// Get followers or following
		userID := r.URL.Query().Get("user_id")
		listType := r.URL.Query().Get("type") // "followers" or "following"
		
		limit := 20
		offset := 0
		if l := r.URL.Query().Get("limit"); l != "" {
			limit, _ = strconv.Atoi(l)
		}
		if o := r.URL.Query().Get("offset"); o != "" {
			offset, _ = strconv.Atoi(o)
		}

		var profiles []kayaker.Profile
		var err error
		if listType == "following" {
			profiles, err = h.node.kayakerMgr.GetFollowing(userID, limit, offset)
		} else {
			profiles, err = h.node.kayakerMgr.GetFollowers(userID, limit, offset)
		}
		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
			return
		}

		var result []map[string]interface{}
		for _, p := range profiles {
			result = append(result, kayaker.ProfileToJSON(&p))
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"profiles": result})

	case "POST":
		// Follow a user
		var req struct {
			UserID string `json:"user_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "Invalid request"})
			return
		}
		if err := h.node.kayakerMgr.Follow(req.UserID); err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})

	case "DELETE":
		// Unfollow a user
		userID := r.URL.Query().Get("user_id")
		if userID == "" {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "user_id required"})
			return
		}
		if err := h.node.kayakerMgr.Unfollow(userID); err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *Homepage) handleKayakerLike(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	
	if h.node.kayakerMgr == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "Kayaker not initialized"})
		return
	}

	switch r.Method {
	case "POST":
		var req struct {
			PostID string `json:"post_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "Invalid request"})
			return
		}
		if err := h.node.kayakerMgr.LikePost(req.PostID); err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})

	case "DELETE":
		postID := r.URL.Query().Get("post_id")
		if postID == "" {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "post_id required"})
			return
		}
		if err := h.node.kayakerMgr.UnlikePost(postID); err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *Homepage) handleKayakerNotifications(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	
	if h.node.kayakerMgr == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "Kayaker not initialized"})
		return
	}

	switch r.Method {
	case "GET":
		limit := 50
		unreadOnly := r.URL.Query().Get("unread") == "true"
		notifications, err := h.node.kayakerMgr.GetNotifications(limit, unreadOnly)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"notifications": notifications})

	case "POST":
		// Mark all as read
		if err := h.node.kayakerMgr.MarkNotificationsRead(); err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *Homepage) handleKayakerSearch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	
	if h.node.kayakerMgr == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "Kayaker not initialized"})
		return
	}

	query := r.URL.Query().Get("q")
	searchType := r.URL.Query().Get("type") // "user", "hashtag", or empty for both
	
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		limit, _ = strconv.Atoi(l)
	}

	results, err := h.node.kayakerMgr.Search(query, searchType, limit)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"results": results})
}

// handleLogo serves the KayakNet logo
func (h *Homepage) handleLogo(w http.ResponseWriter, r *http.Request) {
	// Try to read logo from assets directory
	logoPath := "assets/logo/kayaknet-logo.png"
	data, err := os.ReadFile(logoPath)
	if err != nil {
		// Fallback: serve a simple SVG logo
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 100">
			<ellipse cx="100" cy="60" rx="80" ry="25" fill="#228B22" stroke="#006400" stroke-width="3"/>
			<ellipse cx="100" cy="55" rx="70" ry="15" fill="#8B4513"/>
			<line x1="140" y1="20" x2="80" y2="90" stroke="#CD5C5C" stroke-width="8" stroke-linecap="round"/>
			<ellipse cx="75" cy="95" rx="15" ry="8" fill="#CD5C5C"/>
			<text x="100" y="65" text-anchor="middle" font-family="monospace" font-size="14" fill="#004400" font-weight="bold">KayakNet</text>
		</svg>`))
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "max-age=86400")
	w.Write(data)
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
            display: flex;
            align-items: center;
            gap: 10px;
        }
        .logo img {
            height: 40px;
            width: auto;
        }
        .logo-text::before { content: "["; color: var(--green-dim); }
        .logo-text::after { content: "]"; color: var(--green-dim); }
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
                    <a href="/" class="logo"><img src="/assets/logo.png" alt="KayakNet"><span class="logo-text">KAYAKNET</span></a>
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
        .logo { font-size: 24px; color: var(--green); text-decoration: none; text-shadow: 0 0 10px var(--green-glow); display: flex; align-items: center; gap: 8px; }
        .logo img { height: 32px; width: auto; }
        .logo-text::before { content: "["; color: var(--green-dim); } .logo-text::after { content: "]"; color: var(--green-dim); }
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
                    <a href="/" class="logo"><img src="/assets/logo.png" alt="KayakNet"><span class="logo-text">KAYAKNET</span></a>
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
                        <button class="tab" onclick="loadAllOrders()">ALL ORDERS</button>
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
                    <div style="margin-bottom:15px;padding:10px;border:1px solid var(--amber);background:rgba(255,191,0,0.05)">
                        <strong style="color:var(--amber)">PAYMENT ADDRESSES (to receive funds):</strong><br><br>
                        <div style="margin-bottom:10px"><label>YOUR MONERO (XMR) ADDRESS:</label><br><input type="text" id="new-xmr-addr" placeholder="4..." style="width:100%"></div>
                        <div><label>YOUR ZCASH (ZEC) ADDRESS:</label><br><input type="text" id="new-zec-addr" placeholder="zs1..." style="width:100%"></div>
                        <small style="color:var(--amber)">Enter addresses for the currencies you accept</small>
                    </div>
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
                '<div style="margin-top:20px;display:flex;gap:10px;flex-wrap:wrap;justify-content:center">' +
                '<button class="btn" onclick="checkPaymentStatus(\'' + escrow.escrow_id + '\')">CHECK PAYMENT STATUS</button>' +
                '<button class="btn" style="background:var(--amber);color:#000" onclick="manualConfirmPayment(\'' + escrow.escrow_id + '\')">MANUAL CONFIRM</button>' +
                '</div>' +
                '<div style="margin-top:10px;font-size:12px;color:var(--text-muted)">' +
                'Use Manual Confirm if you verified payment in your wallet but auto-detect failed.' +
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
        
        async function manualConfirmPayment(escrowId) {
            const txId = prompt('Enter the transaction ID (optional, for records):');
            if (txId === null) return; // User cancelled
            
            const formData = new FormData();
            formData.append('escrow_id', escrowId);
            formData.append('tx_id', txId || '');
            
            try {
                const res = await fetch('/api/escrow/manual-confirm', {
                    method: 'POST',
                    body: formData
                });
                const result = await res.json();
                
                if (result.success) {
                    alert('Payment manually confirmed! The order is now funded.');
                    closeModal('order-modal');
                    loadOrders('buying');
                } else {
                    alert('Failed to confirm: ' + (result.message || 'Unknown error'));
                }
            } catch (err) {
                alert('Error: ' + err.message);
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
        
        async function loadAllOrders() {
            const res = await fetch('/api/escrow/all');
            const orders = await res.json();
            const container = document.getElementById('orders-list');
            
            if (!orders || orders.length === 0) {
                container.innerHTML = 
                    '<div class="order">' +
                    '<div class="order-header"><span>No orders yet</span></div>' +
                    '<p style="color:var(--green-dim)">All network orders will appear here.</p>' +
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
                    '<span>' + (o.listing_title || 'Order') + '</span>' +
                    '<span class="order-status" style="background:' + stateColor + ';color:#000">' + o.state.toUpperCase() + '</span>' +
                    '</div>' +
                    '<p>' + (o.amount ? o.amount.toFixed(6) : '0') + ' ' + (o.currency || 'XMR') + '</p>' +
                    '<p style="color:var(--green-dim);font-size:12px">' + new Date(o.created_at).toLocaleDateString() + '</p>' +
                    '</div>';
            }).join('');
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
            
            // Determine if current user is buyer or seller
            const isSeller = e.is_seller || false;
            const isBuyer = e.is_buyer || false;
            
            let actions = '';
            let roleLabel = isSeller ? '[YOU ARE SELLER]' : (isBuyer ? '[YOU ARE BUYER]' : '');
            
            if (e.state === 'created') {
                if (isBuyer) {
                    actions = '<p style="color:var(--amber)">Waiting for payment. Send crypto to escrow address.</p>';
                } else if (isSeller) {
                    actions = '<p style="color:var(--amber)">Waiting for buyer to pay.</p>';
                }
            } else if (e.state === 'funded') {
                if (isSeller) {
                    actions = '<button class="btn" onclick="markShipped(\'' + escrowId + '\')">MARK AS SHIPPED</button>' +
                        '<p style="margin-top:10px;color:var(--green-dim)">Ship the item, then mark as shipped.</p>';
                } else if (isBuyer) {
                    actions = '<p style="color:var(--amber)">Payment received. Waiting for seller to ship.</p>';
                } else {
                    // For "ALL ORDERS" view - show both options
                    actions = '<button class="btn" onclick="markShipped(\'' + escrowId + '\')">MARK AS SHIPPED (Seller)</button>';
                }
            } else if (e.state === 'shipped') {
                if (isBuyer) {
                    actions = '<button class="btn" onclick="releaseEscrow(\'' + escrowId + '\')">CONFIRM RECEIVED</button>' +
                        '<button class="btn btn-danger" style="margin-left:10px" onclick="openDispute(\'' + escrowId + '\')">OPEN DISPUTE</button>' +
                        '<p style="margin-top:10px;color:var(--green-dim)">Click Confirm when you receive the item to release funds.</p>';
                } else if (isSeller) {
                    actions = '<p style="color:var(--amber)">Item shipped. Waiting for buyer to confirm receipt.</p>' +
                        '<p style="color:var(--green-dim)">Auto-release in 14 days if buyer doesn\'t respond.</p>';
                } else {
                    // For "ALL ORDERS" view
                    actions = '<button class="btn" onclick="releaseEscrow(\'' + escrowId + '\')">CONFIRM RECEIVED (Buyer)</button>';
                }
            } else if (e.state === 'completed') {
                actions = '<p style="color:var(--green)">Transaction complete! Funds released to seller.</p>';
            } else if (e.state === 'disputed') {
                actions = '<p style="color:var(--red)">Dispute in progress. Funds frozen.</p>';
            }
            
            document.getElementById('order-form').innerHTML =
                '<h2>' + e.listing_title + '</h2>' +
                '<div style="padding:10px;background:var(--bg-darker);color:var(--amber);margin-bottom:15px">' + roleLabel + '</div>' +
                '<div style="padding:20px;border:1px solid var(--border);margin:15px 0">' +
                '<p><strong>Status:</strong> <span style="color:var(--amber)">' + e.state.toUpperCase() + '</span></p>' +
                '<p><strong>Amount:</strong> ' + e.amount.toFixed(8) + ' ' + e.currency + '</p>' +
                '<p><strong>Order ID:</strong> ' + e.order_id + '</p>' +
                (e.tx_id ? '<p><strong>TX ID:</strong> <span style="font-size:12px">' + e.tx_id + '</span></p>' : '') +
                (e.tracking_info ? '<p><strong>Tracking:</strong> ' + e.tracking_info + '</p>' : '') +
                '<p><strong>Created:</strong> ' + new Date(e.created_at).toLocaleString() + '</p>' +
                (e.auto_release_at && e.state === 'shipped' ? '<p><strong>Auto-release:</strong> ' + new Date(e.auto_release_at).toLocaleString() + '</p>' : '') +
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
            const xmrAddr = document.getElementById('new-xmr-addr').value;
            const zecAddr = document.getElementById('new-zec-addr').value;
            
            // Validate that seller has address for the currency they accept
            if (currency === 'XMR' && !xmrAddr) {
                alert('You must provide your Monero address to accept XMR payments');
                return;
            }
            if (currency === 'ZEC' && !zecAddr) {
                alert('You must provide your Zcash address to accept ZEC payments');
                return;
            }
            
            const formData = new FormData();
            formData.append('title', title);
            formData.append('category', category);
            formData.append('price', price);
            formData.append('currency', currency);
            formData.append('description', desc);
            formData.append('image', image);
            formData.append('seller_xmr_address', xmrAddr);
            formData.append('seller_zec_address', zecAddr);
            
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
        .logo { font-size: 20px; color: var(--green); text-decoration: none; text-shadow: 0 0 10px var(--green-glow); display: flex; align-items: center; gap: 8px; }
        .logo img { height: 28px; width: auto; }
        .logo-text::before { content: "["; color: var(--green-dim); } .logo-text::after { content: "]"; color: var(--green-dim); }
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
                    <a href="/" class="logo"><img src="/assets/logo.png" alt="KayakNet"><span class="logo-text">KAYAKNET</span></a>
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
                            <div class="panel-header"><span>DIRECT MESSAGES</span><span id="dm-header-badge"></span></div>
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
                                <button class="input-btn" id="voice-btn" onclick="toggleVoice()" title="Voice message">MIC</button>
                                <input type="text" id="message-input" placeholder="Type *bold* _italic_ @mention... (Shift+Enter for newline)" onkeydown="handleKeyDown(event)" oninput="handleTyping()">
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
        let replyingTo = null;
        let isRecording = false;
        let mediaRecorder = null;
        let audioChunks = [];
        
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
                
                // Also get total unread count
                const unreadRes = await fetch('/api/chat/dm/unread');
                const unreadData = await unreadRes.json();
                const dmHeader = document.getElementById('dm-header-badge');
                if (dmHeader && unreadData.unread > 0) {
                    dmHeader.innerHTML = ' <span class="unread">' + unreadData.unread + '</span>';
                } else if (dmHeader) {
                    dmHeader.innerHTML = '';
                }
                
                if (!convs || convs.length === 0) {
                    container.innerHTML = '<div style="padding:5px;color:var(--green-dim);font-size:12px;">No conversations</div>';
                    return;
                }
                container.innerHTML = convs.map(c => {
                    const otherUser = c.participants.find(p => p !== myProfile?.id) || c.participants[0];
                    const isPinned = c.pinned ? '<span style="color:var(--green);margin-right:3px;">*</span>' : '';
                    const isMuted = c.muted ? '<span style="color:var(--green-dim);margin-right:3px;">M</span>' : '';
                    const draft = c.draft ? '<span style="color:var(--red);font-size:10px;"> [draft]</span>' : '';
                    return '<div class="dm-item' + (currentDM === otherUser ? ' active' : '') + (c.pinned ? ' pinned' : '') + '" ' +
                        'onclick="selectDM(\'' + otherUser + '\')" ' +
                        'oncontextmenu="showDMContextMenu(event, \'' + otherUser + '\', ' + (c.pinned||false) + ', ' + (c.muted||false) + ')">' +
                        isPinned + isMuted +
                        '<div class="status-dot status-online"></div>' +
                        '<span>' + escapeHtml(otherUser.substring(0,12)) + '</span>' +
                        draft +
                        (c.unread > 0 ? '<span class="unread">' + c.unread + '</span>' : '') +
                        '</div>';
                }).join('');
            } catch(e) { console.error('Failed to load DMs:', e); }
        }
        
        // DM context menu
        function showDMContextMenu(e, userId, isPinned, isMuted) {
            e.preventDefault();
            const menu = document.getElementById('dm-context-menu');
            menu.innerHTML = 
                '<div class="ctx-item" onclick="togglePinDM(\'' + userId + '\', ' + !isPinned + ')">' + (isPinned ? 'Unpin' : 'Pin') + ' Conversation</div>' +
                '<div class="ctx-item" onclick="toggleMuteDM(\'' + userId + '\', ' + !isMuted + ')">' + (isMuted ? 'Unmute' : 'Mute') + ' Notifications</div>' +
                '<div class="ctx-item" onclick="searchDM(\'' + userId + '\')">Search Messages</div>' +
                '<div class="ctx-item" onclick="archiveDM(\'' + userId + '\')">Archive</div>' +
                '<div class="ctx-item" onclick="setNickname(\'' + userId + '\')">Set Nickname</div>' +
                '<div class="ctx-item" style="color:var(--red);" onclick="clearDM(\'' + userId + '\')">Clear History</div>' +
                '<div class="ctx-item" style="color:var(--red);" onclick="deleteDM(\'' + userId + '\')">Delete Conversation</div>';
            menu.style.display = 'block';
            menu.style.left = e.pageX + 'px';
            menu.style.top = e.pageY + 'px';
        }
        
        async function togglePinDM(userId, pin) {
            await fetch('/api/chat/dm/pin', {method: 'POST', body: new URLSearchParams({user: userId, pinned: pin})});
            hideDMContextMenu();
            loadDMs();
        }
        
        async function toggleMuteDM(userId, mute) {
            await fetch('/api/chat/dm/mute', {method: 'POST', body: new URLSearchParams({user: userId, muted: mute})});
            hideDMContextMenu();
            loadDMs();
        }
        
        async function searchDM(userId) {
            hideDMContextMenu();
            const query = prompt('Search messages:');
            if (!query) return;
            const res = await fetch('/api/chat/dm/search?user=' + userId + '&q=' + encodeURIComponent(query));
            const results = await res.json();
            if (!results || results.length === 0) {
                alert('No messages found');
                return;
            }
            selectDM(userId);
            setTimeout(() => {
                const msgId = results[0].id;
                const el = document.getElementById('msg-' + msgId);
                if (el) {
                    el.scrollIntoView({behavior: 'smooth', block: 'center'});
                    el.style.background = 'rgba(0,255,0,0.1)';
                    setTimeout(() => el.style.background = '', 2000);
                }
            }, 500);
        }
        
        async function archiveDM(userId) {
            if (!confirm('Archive this conversation?')) return;
            await fetch('/api/chat/dm/archive', {method: 'POST', body: new URLSearchParams({user: userId, archived: 'true'})});
            hideDMContextMenu();
            loadDMs();
        }
        
        async function setNickname(userId) {
            const nickname = prompt('Set nickname for this user:');
            if (nickname === null) return;
            await fetch('/api/chat/dm/nickname', {method: 'POST', body: new URLSearchParams({user: userId, nickname: nickname})});
            hideDMContextMenu();
            loadDMs();
        }
        
        async function clearDM(userId) {
            if (!confirm('Clear all messages in this conversation?')) return;
            await fetch('/api/chat/dm/clear', {method: 'POST', body: new URLSearchParams({user: userId})});
            hideDMContextMenu();
            loadMessages();
        }
        
        async function deleteDM(userId) {
            if (!confirm('Delete this entire conversation?')) return;
            await fetch('/api/chat/dm/delete', {method: 'POST', body: new URLSearchParams({user: userId, all: 'true'})});
            hideDMContextMenu();
            if (currentDM === userId) {
                currentDM = null;
                document.getElementById('messages').innerHTML = '';
            }
            loadDMs();
        }
        
        function hideDMContextMenu() {
            document.getElementById('dm-context-menu').style.display = 'none';
        }
        
        // Typing indicator for DMs
        function handleDMTyping() {
            if (!currentDM) return;
            fetch('/api/chat/dm/typing', {method: 'POST', body: new URLSearchParams({user: currentDM, typing: 'true'})});
            if (typingTimeout) clearTimeout(typingTimeout);
            typingTimeout = setTimeout(() => {
                fetch('/api/chat/dm/typing', {method: 'POST', body: new URLSearchParams({user: currentDM, typing: 'false'})});
            }, 3000);
        }
        
        // Check typing status
        async function checkDMTyping() {
            if (!currentDM) return;
            const res = await fetch('/api/chat/dm/typing?user=' + currentDM);
            const data = await res.json();
            const indicator = document.getElementById('typing-indicator');
            if (data.typing && data.typing.length > 0) {
                indicator.textContent = data.typing[0].substring(0,8) + '... is typing';
                indicator.style.display = 'block';
            } else {
                indicator.style.display = 'none';
            }
        }
        
        // Save draft when leaving DM
        async function saveDMDraft() {
            if (!currentDM) return;
            const input = document.getElementById('msg-input');
            if (input.value.trim()) {
                await fetch('/api/chat/dm/draft', {method: 'POST', body: new URLSearchParams({user: currentDM, draft: input.value})});
            }
        }
        
        // Load draft when entering DM
        async function loadDMDraft() {
            if (!currentDM) return;
            const res = await fetch('/api/chat/dm/draft?user=' + currentDM);
            const data = await res.json();
            if (data.draft) {
                document.getElementById('msg-input').value = data.draft;
            }
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
            const isMe = m.sender_id === myProfile?.id;
            const isMention = m.content && myProfile && (m.content.includes('@' + myProfile.nick) || m.content.includes('@' + myProfile.id?.substring(0,8)));
            
            let mediaHtml = '';
            if (m.media) {
                if (m.media.type && m.media.type.startsWith('image/')) {
                    mediaHtml = '<div class="media"><img src="data:' + m.media.type + ';base64,' + m.media.data + '" onclick="viewImage(this.src)" alt="' + escapeHtml(m.media.name) + '"></div>';
                } else if (m.media.type && m.media.type.startsWith('audio/')) {
                    mediaHtml = '<div class="media"><audio controls src="data:' + m.media.type + ';base64,' + m.media.data + '"></audio></div>';
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
            
            let replyHtml = '';
            if (m.reply_to) {
                replyHtml = '<div style="padding:5px 10px;margin-bottom:5px;background:rgba(0,255,0,0.03);border-left:2px solid var(--green-dim);font-size:12px;color:var(--green-dim);cursor:pointer;" onclick="scrollToMsg(\'' + m.reply_to + '\')">Reply to message</div>';
            }
            
            let actionsHtml = '<div class="msg-actions" style="display:none;position:absolute;right:5px;top:5px;background:var(--bg);border:1px solid var(--border);padding:2px;">' +
                '<span style="cursor:pointer;padding:3px;font-size:11px;" onclick="replyTo(\'' + m.id + '\', \'' + escapeHtml(m.nick || 'Anon') + '\', \'' + escapeHtml((m.content || '').substring(0,30)) + '\')" title="Reply">REPLY</span>' +
                '<span style="cursor:pointer;padding:3px;font-size:11px;" onclick="addReaction(\'' + m.id + '\', \'👍\')" title="React">+</span>' +
                (isMe ? '<span style="cursor:pointer;padding:3px;font-size:11px;" onclick="editMsg(\'' + m.id + '\')" title="Edit">EDIT</span>' : '') +
                (isMe ? '<span style="cursor:pointer;padding:3px;font-size:11px;color:var(--red);" onclick="deleteMsg(\'' + m.id + '\')" title="Delete">DEL</span>' : '') +
                '<span style="cursor:pointer;padding:3px;font-size:11px;" onclick="pinMsg(\'' + m.id + '\')" title="Pin">PIN</span>' +
                '</div>';
            
            const formattedContent = formatContent(m.content || '');
            
            return '<div class="message' + (m.type === 5 ? ' system' : '') + (m.receiver_id ? ' dm' : '') + (isMention ? '" style="background:rgba(0,255,255,0.03);border-left-color:var(--cyan);' : '') + (m.edited ? ' edited' : '') + '" id="msg-' + m.id + '" onmouseenter="this.querySelector(\'.msg-actions\').style.display=\'flex\'" onmouseleave="this.querySelector(\'.msg-actions\').style.display=\'none\'" style="position:relative;">' +
                actionsHtml +
                replyHtml +
                '<div class="message-header">' +
                '<div class="avatar">' + avatar + '</div>' +
                '<span class="nick" onclick="showProfile(\'' + m.sender_id + '\')">' + escapeHtml(m.nick || 'Anonymous') + '</span>' +
                '<span class="user-id">' + shortId + '</span>' +
                '<span class="time">' + time + (m.edited ? ' (edited)' : '') + '</span>' +
                '</div>' +
                '<div class="content">' + formattedContent + '</div>' +
                mediaHtml +
                reactionsHtml +
                '</div>';
        }
        
        function formatContent(content) {
            if (!content) return '';
            let html = escapeHtml(content);
            html = html.replace(/\*([^*]+)\*/g, '<strong style="color:var(--green);">$1</strong>');
            html = html.replace(/_([^_]+)_/g, '<em style="color:var(--cyan);">$1</em>');
            html = html.replace(/\x60([^\x60]+)\x60/g, '<code style="background:rgba(0,255,0,0.1);padding:2px 5px;">$1</code>');
            html = html.replace(/@(\w+)/g, '<span style="color:var(--amber);background:rgba(255,170,0,0.1);padding:0 3px;cursor:pointer;" onclick="findUser(\'$1\')">@$1</span>');
            html = html.replace(/(https?:\/\/[^\s]+)/g, '<a href="$1" target="_blank" style="color:var(--cyan);">$1</a>');
            return html;
        }
        
        function replyTo(msgId, nick, content) {
            replyingTo = msgId;
            const preview = document.getElementById('attachment-preview');
            preview.style.display = 'block';
            preview.innerHTML = '<div style="border-left:2px solid var(--green);padding-left:10px;">Replying to <strong>' + nick + '</strong>: ' + content + '... <span onclick="cancelReply()" style="cursor:pointer;color:var(--red);">[X]</span></div>';
            document.getElementById('message-input').focus();
        }
        
        function cancelReply() {
            replyingTo = null;
            if (!pendingAttachment) {
                document.getElementById('attachment-preview').style.display = 'none';
            }
        }
        
        function scrollToMsg(msgId) {
            const el = document.getElementById('msg-' + msgId);
            if (el) {
                el.scrollIntoView({ behavior: 'smooth', block: 'center' });
                el.style.background = 'rgba(0,255,255,0.1)';
                setTimeout(() => el.style.background = '', 2000);
            }
        }
        
        async function editMsg(msgId) {
            const newContent = prompt('Edit message:');
            if (newContent !== null) {
                await fetch('/api/chat/edit', { method: 'POST', body: new URLSearchParams({ message_id: msgId, content: newContent }) });
                loadMessages();
            }
        }
        
        async function deleteMsg(msgId) {
            if (confirm('Delete this message?')) {
                await fetch('/api/chat/delete', { method: 'POST', body: new URLSearchParams({ message_id: msgId }) });
                loadMessages();
            }
        }
        
        async function pinMsg(msgId) {
            await fetch('/api/chat/pin', { method: 'POST', body: new URLSearchParams({ room: currentRoom, message_id: msgId }) });
            alert('Message pinned!');
        }
        
        function findUser(name) {
            document.getElementById('user-search').value = name;
            searchUsers();
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
        
        async function selectDM(userId) {
            // Save draft from previous DM
            await saveDMDraft();
            
            currentDM = userId;
            currentRoom = null;
            document.querySelectorAll('.room-item, .dm-item').forEach(el => el.classList.remove('active'));
            document.querySelector('.dm-item[onclick*="' + userId + '"]')?.classList.add('active');
            document.getElementById('chat-title').textContent = 'DM: ' + userId.substring(0,12);
            document.getElementById('chat-title').innerHTML += ' <span style="font-size:10px;cursor:pointer;" onclick="showDMOptions(\'' + userId + '\')">[...]</span>';
            
            // Mark as read
            fetch('/api/chat/dm/read?user=' + userId);
            
            // Load draft
            await loadDMDraft();
            
            loadMessages();
            loadDMs(); // Refresh to update unread counts
            
            // Start typing indicator polling
            if (window.dmTypingInterval) clearInterval(window.dmTypingInterval);
            window.dmTypingInterval = setInterval(checkDMTyping, 2000);
        }
        
        function showDMOptions(userId) {
            const menu = document.getElementById('dm-context-menu');
            menu.innerHTML = 
                '<div class="ctx-item" onclick="searchDM(\'' + userId + '\')">Search Messages</div>' +
                '<div class="ctx-item" onclick="viewProfile(\'' + userId + '\')">View Profile</div>' +
                '<div class="ctx-item" onclick="setNickname(\'' + userId + '\')">Set Nickname</div>' +
                '<div class="ctx-item" style="color:var(--red);" onclick="blockUserFromDM(\'' + userId + '\')">Block User</div>';
            menu.style.display = 'block';
            menu.style.left = '50%';
            menu.style.top = '60px';
        }
        
        async function viewProfile(userId) {
            hideDMContextMenu();
            showProfile(userId);
        }
        
        async function blockUserFromDM(userId) {
            if (!confirm('Block this user? They will not be able to message you.')) return;
            await fetch('/api/chat/block', {method: 'POST', body: new URLSearchParams({user: userId})});
            hideDMContextMenu();
            alert('User blocked');
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
            
            if (replyingTo) {
                formData.append('reply_to', replyingTo);
            }
            
            if (pendingAttachment) {
                formData.append('media_type', pendingAttachment.type);
                formData.append('media_name', pendingAttachment.name);
                formData.append('media_data', pendingAttachment.data);
            }
            
            try {
                const endpoint = currentDM ? '/api/chat/dm' : '/api/chat';
                await fetch(endpoint, { method: 'POST', body: formData });
                input.value = '';
                cancelReply();
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
            // Send typing indicator for DMs
            if (currentDM) {
                handleDMTyping();
            }
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
            const emojis = ['👍', '❤️', '😂', '🔥', '💯', '🚀', '✅', '⚡', '🤔', '😎', '🎉', '💪', '🙏', '👀'];
            const emoji = emojis[Math.floor(Math.random() * emojis.length)];
            document.getElementById('message-input').value += emoji;
        }
        
        async function toggleVoice() {
            const btn = document.getElementById('voice-btn');
            if (!isRecording) {
                try {
                    const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
                    mediaRecorder = new MediaRecorder(stream);
                    audioChunks = [];
                    mediaRecorder.ondataavailable = e => audioChunks.push(e.data);
                    mediaRecorder.onstop = async () => {
                        const blob = new Blob(audioChunks, { type: 'audio/webm' });
                        const reader = new FileReader();
                        reader.onload = () => {
                            pendingAttachment = { type: 'audio/webm', name: 'voice_' + Date.now() + '.webm', size: blob.size, data: reader.result.split(',')[1] };
                            sendMessage();
                        };
                        reader.readAsDataURL(blob);
                        stream.getTracks().forEach(t => t.stop());
                    };
                    mediaRecorder.start();
                    isRecording = true;
                    btn.textContent = 'STOP';
                    btn.style.background = 'var(--red)';
                    btn.style.borderColor = 'var(--red)';
                } catch(e) { alert('Could not access microphone'); }
            } else {
                mediaRecorder.stop();
                isRecording = false;
                btn.textContent = 'MIC';
                btn.style.background = '';
                btn.style.borderColor = '';
            }
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
            if (confirm("Block this user? They will not be able to DM you.")) {
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
        
        // Close context menu on click outside
        document.addEventListener('click', function(e) {
            if (!e.target.closest('#dm-context-menu') && !e.target.closest('.dm-item')) {
                hideDMContextMenu();
            }
        });
    </script>
    
    <!-- DM Context Menu -->
    <div id="dm-context-menu" style="display:none;position:fixed;background:var(--bg);border:1px solid var(--border);z-index:1000;min-width:150px;box-shadow:0 2px 10px rgba(0,0,0,0.5);">
    </div>
    
    <style>
        .ctx-item { padding: 8px 12px; cursor: pointer; font-size: 12px; color: var(--green); }
        .ctx-item:hover { background: var(--border); }
        .dm-item.pinned { border-left: 2px solid var(--green); }
        .dm-item .unread { background: var(--red); color: var(--bg); padding: 1px 4px; border-radius: 8px; font-size: 10px; margin-left: auto; }
    </style>
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
        :root { --bg: #000; --green: #00ff00; --green-dim: #00aa00; --green-glow: #00ff0066; --border: #00ff0033; --amber: #ffaa00; --red: #ff4444; }
        body { font-family: 'VT323', monospace; background: var(--bg); color: var(--green); min-height: 100vh; }
        body::before { content: ""; position: fixed; top: 0; left: 0; right: 0; bottom: 0; background: repeating-linear-gradient(0deg, rgba(0,0,0,0.15) 0px, rgba(0,0,0,0.15) 1px, transparent 1px, transparent 2px); pointer-events: none; z-index: 1000; }
        .container { max-width: 1000px; margin: 0 auto; padding: 20px; }
        .terminal { border: 1px solid var(--green); background: #0a0a0a; box-shadow: 0 0 20px var(--green-glow); }
        .term-header { background: var(--green); color: var(--bg); padding: 5px 15px; font-size: 14px; }
        .term-body { padding: 20px; }
        header { display: flex; justify-content: space-between; align-items: center; padding: 15px 0; border-bottom: 1px dashed var(--border); margin-bottom: 20px; }
        .logo { font-size: 24px; color: var(--green); text-decoration: none; text-shadow: 0 0 10px var(--green-glow); display: flex; align-items: center; gap: 8px; }
        .logo img { height: 32px; width: auto; }
        .logo-text::before { content: "["; color: var(--green-dim); } .logo-text::after { content: "]"; color: var(--green-dim); }
        nav { display: flex; gap: 15px; }
        nav a { color: var(--green-dim); text-decoration: none; padding: 5px 10px; border: 1px solid transparent; }
        nav a:hover, nav a.active { color: var(--green); border-color: var(--green); }
        h1, h2 { font-size: 24px; margin-bottom: 20px; }
        h1::before, h2::before { content: "> "; color: var(--green-dim); }
        h2 { font-size: 18px; margin-top: 30px; color: var(--green-dim); }
        .tabs { display: flex; gap: 10px; margin-bottom: 20px; }
        .tab { padding: 10px 20px; background: transparent; border: 1px solid var(--border); color: var(--green-dim); cursor: pointer; font-family: inherit; font-size: 16px; }
        .tab:hover, .tab.active { color: var(--green); border-color: var(--green); background: rgba(0,255,0,0.05); }
        .tab-content { display: none; }
        .tab-content.active { display: block; }
        .search-bar { display: flex; gap: 10px; margin-bottom: 20px; flex-wrap: wrap; }
        input, select, textarea { flex: 1; min-width: 200px; padding: 10px; background: var(--bg); border: 1px solid var(--green); color: var(--green); font-family: inherit; font-size: 16px; }
        input:focus, select:focus, textarea:focus { outline: none; box-shadow: 0 0 10px var(--green-glow); }
        textarea { resize: vertical; min-height: 60px; }
        .btn { padding: 10px 20px; background: var(--green); color: var(--bg); border: none; cursor: pointer; font-family: inherit; font-size: 16px; }
        .btn:hover { box-shadow: 0 0 15px var(--green-glow); }
        .btn-small { padding: 5px 10px; font-size: 14px; }
        .btn-amber { background: var(--amber); }
        .btn-red { background: var(--red); }
        .domains { display: grid; gap: 10px; }
        .domain { border: 1px solid var(--border); padding: 15px; background: var(--bg); }
        .domain:hover { border-color: var(--green); box-shadow: 0 0 10px var(--green-glow); }
        .domain-header { display: flex; justify-content: space-between; align-items: flex-start; flex-wrap: wrap; gap: 10px; }
        .domain-name { font-size: 22px; color: var(--green); text-shadow: 0 0 5px var(--green-glow); }
        .domain-name.owned { color: var(--amber); text-shadow: 0 0 5px rgba(255,170,0,0.5); }
        .domain-desc { color: var(--green-dim); font-size: 14px; margin-top: 8px; }
        .domain-meta { display: flex; gap: 15px; flex-wrap: wrap; margin-top: 10px; font-size: 12px; color: var(--green-dim); opacity: 0.8; }
        .domain-actions { display: flex; gap: 5px; margin-top: 10px; }
        .form-group { margin-bottom: 15px; }
        .form-group label { display: block; margin-bottom: 5px; color: var(--green-dim); font-size: 14px; }
        .form-row { display: flex; gap: 15px; flex-wrap: wrap; }
        .form-row .form-group { flex: 1; min-width: 200px; }
        .message { padding: 10px 15px; margin-bottom: 15px; border: 1px solid; }
        .message.success { border-color: var(--green); color: var(--green); background: rgba(0,255,0,0.1); }
        .message.error { border-color: var(--red); color: var(--red); background: rgba(255,0,0,0.1); }
        .stats { display: grid; grid-template-columns: repeat(3, 1fr); gap: 15px; margin-bottom: 20px; }
        .stat { border: 1px solid var(--border); padding: 15px; text-align: center; }
        .stat-value { font-size: 28px; color: var(--green); text-shadow: 0 0 10px var(--green-glow); }
        .stat-label { color: var(--green-dim); font-size: 12px; margin-top: 5px; text-transform: uppercase; }
        .badge { display: inline-block; padding: 2px 8px; font-size: 12px; border: 1px solid; margin-left: 10px; }
        .badge.owned { border-color: var(--amber); color: var(--amber); }
        .badge.service { border-color: var(--green-dim); color: var(--green-dim); }
    </style>
</head>
<body>
    <div class="container">
        <div class="terminal">
            <div class="term-header">KAYAKNET DOMAIN REGISTRY // .KYK NAMING SYSTEM</div>
            <div class="term-body">
                <header>
                    <a href="/" class="logo"><img src="/assets/logo.png" alt="KayakNet"><span class="logo-text">KAYAKNET</span></a>
                    <nav>
                        <a href="/marketplace">/market</a>
                        <a href="/chat">/chat</a>
                        <a href="/domains" class="active">/domains</a>
                        <a href="/network">/network</a>
                    </nav>
                </header>
                
                <div class="stats">
                    <div class="stat"><div class="stat-value" id="total-domains">0</div><div class="stat-label">Total Domains</div></div>
                    <div class="stat"><div class="stat-value" id="my-domains">0</div><div class="stat-label">My Domains</div></div>
                    <div class="stat"><div class="stat-value">1 YR</div><div class="stat-label">Registration Period</div></div>
                </div>
                
                <div class="tabs">
                    <button class="tab active" onclick="showTab('browse')">BROWSE</button>
                    <button class="tab" onclick="showTab('register')">+REGISTER</button>
                    <button class="tab" onclick="showTab('mydomains')">MY DOMAINS</button>
                    <button class="tab" onclick="showTab('lookup')">WHOIS</button>
                </div>
                
                <div id="message"></div>
                
                <!-- Browse Tab -->
                <div id="tab-browse" class="tab-content active">
                    <h2>BROWSE_DOMAINS</h2>
                    <div class="search-bar">
                        <input type="text" id="search" placeholder="dns://search domains..." oninput="loadDomains()" />
                    </div>
                    <div class="domains" id="domains">
                        <div class="domain"><div class="domain-name">LOADING...</div></div>
                    </div>
                </div>
                
                <!-- Register Tab -->
                <div id="tab-register" class="tab-content">
                    <h2>REGISTER_DOMAIN</h2>
                    <form onsubmit="registerDomain(event)">
                        <div class="form-row">
                            <div class="form-group">
                                <label>DOMAIN NAME</label>
                                <input type="text" id="reg-name" placeholder="mydomain" pattern="[a-z0-9][a-z0-9-]*[a-z0-9]|[a-z0-9]" required />
                            </div>
                            <div class="form-group">
                                <label>SERVICE TYPE (optional)</label>
                                <select id="reg-type">
                                    <option value="">-- None --</option>
                                    <option value="website">Website</option>
                                    <option value="chat">Chat Service</option>
                                    <option value="market">Marketplace</option>
                                    <option value="file">File Hosting</option>
                                    <option value="api">API Service</option>
                                    <option value="other">Other</option>
                                </select>
                            </div>
                        </div>
                        <div class="form-group">
                            <label>DESCRIPTION (optional)</label>
                            <textarea id="reg-desc" placeholder="What is this domain for?"></textarea>
                        </div>
                        <div style="display: flex; justify-content: space-between; align-items: center; flex-wrap: wrap; gap: 10px;">
                            <div style="color: var(--green-dim); font-size: 14px;">
                                Your domain: <span id="preview-domain" style="color: var(--green);">----.kyk</span>
                            </div>
                            <button type="submit" class="btn">REGISTER DOMAIN</button>
                        </div>
                    </form>
                </div>
                
                <!-- My Domains Tab -->
                <div id="tab-mydomains" class="tab-content">
                    <h2>MY_DOMAINS</h2>
                    <div class="domains" id="my-domains-list">
                        <div class="domain"><div class="domain-name">LOADING...</div></div>
                    </div>
                </div>
                
                <!-- Lookup/WHOIS Tab -->
                <div id="tab-lookup" class="tab-content">
                    <h2>WHOIS_LOOKUP</h2>
                    <div class="search-bar">
                        <input type="text" id="lookup-domain" placeholder="domain.kyk" />
                        <button class="btn" onclick="lookupDomain()">RESOLVE</button>
                    </div>
                    <div id="lookup-result" style="margin-top: 20px;"></div>
                </div>
            </div>
        </div>
    </div>
    <script>
        function showTab(tab) {
            document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
            document.querySelectorAll('.tab-content').forEach(t => t.classList.remove('active'));
            document.querySelector('[onclick="showTab(\'' + tab + '\')"]').classList.add('active');
            document.getElementById('tab-' + tab).classList.add('active');
            if (tab === 'mydomains') loadMyDomains();
        }
        
        function showMessage(msg, type) {
            const el = document.getElementById('message');
            el.className = 'message ' + type;
            el.textContent = msg;
            el.style.display = 'block';
            setTimeout(() => el.style.display = 'none', 5000);
        }
        
        async function loadDomains() {
            const search = document.getElementById('search').value;
            const res = await fetch('/api/domains?q=' + encodeURIComponent(search));
            const domains = await res.json();
            const container = document.getElementById('domains');
            
            document.getElementById('total-domains').textContent = domains ? domains.length : 0;
            
            if (!domains || domains.length === 0) {
                container.innerHTML = '<div class="domain"><div class="domain-name">NO_RECORDS_FOUND</div><div class="domain-desc">// Register the first .kyk domain!</div></div>';
                return;
            }
            
            container.innerHTML = domains.map(d => {
                let badges = '';
                if (d.is_owner) badges += '<span class="badge owned">OWNED</span>';
                if (d.service_type) badges += '<span class="badge service">' + d.service_type.toUpperCase() + '</span>';
                return '<div class="domain">' +
                    '<div class="domain-header">' +
                        '<div><div class="domain-name' + (d.is_owner ? ' owned' : '') + '">' + d.full_name.toUpperCase() + badges + '</div></div>' +
                    '</div>' +
                    '<div class="domain-desc">' + (d.description || '// No description') + '</div>' +
                    '<div class="domain-meta">' +
                        '<span>OWNER: ' + d.owner.substring(0, 16) + '...</span>' +
                        '<span>EXPIRES: ' + d.expires_at + '</span>' +
                        (d.address ? '<span>ADDR: ' + d.address + '</span>' : '') +
                    '</div>' +
                '</div>';
            }).join('');
        }
        
        async function loadMyDomains() {
            const res = await fetch('/api/domains/my');
            const domains = await res.json();
            const container = document.getElementById('my-domains-list');
            
            document.getElementById('my-domains').textContent = domains ? domains.length : 0;
            
            if (!domains || domains.length === 0) {
                container.innerHTML = '<div class="domain"><div class="domain-name">NO_DOMAINS_OWNED</div><div class="domain-desc">// Register your first .kyk domain!</div></div>';
                return;
            }
            
            container.innerHTML = domains.map(d => {
                return '<div class="domain">' +
                    '<div class="domain-header">' +
                        '<div><div class="domain-name owned">' + d.full_name.toUpperCase() + '</div></div>' +
                    '</div>' +
                    '<div class="domain-desc">' + (d.description || '// No description') + '</div>' +
                    '<div class="domain-meta">' +
                        '<span>CREATED: ' + d.created_at + '</span>' +
                        '<span>EXPIRES: ' + d.expires_at + '</span>' +
                        '<span>UPDATED: ' + d.updated_at + '</span>' +
                    '</div>' +
                    '<div class="domain-actions">' +
                        '<button class="btn btn-small" onclick="editDomain(\'' + d.name + '\', \'' + (d.address || '') + '\', \'' + (d.description || '').replace(/'/g, "\\'") + '\')">EDIT</button>' +
                        '<button class="btn btn-small btn-amber" onclick="renewDomain(\'' + d.name + '\')">RENEW</button>' +
                    '</div>' +
                '</div>';
            }).join('');
        }
        
        document.getElementById('reg-name').addEventListener('input', function(e) {
            const name = e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '');
            e.target.value = name;
            document.getElementById('preview-domain').textContent = name ? name + '.kyk' : '----.kyk';
        });
        
        async function registerDomain(e) {
            e.preventDefault();
            const name = document.getElementById('reg-name').value;
            const desc = document.getElementById('reg-desc').value;
            const type = document.getElementById('reg-type').value;
            
            const res = await fetch('/api/domains/register', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ name, description: desc, service_type: type })
            });
            const data = await res.json();
            
            if (data.success) {
                showMessage('Domain ' + data.domain + ' registered successfully! Expires: ' + data.expires, 'success');
                document.getElementById('reg-name').value = '';
                document.getElementById('reg-desc').value = '';
                document.getElementById('reg-type').value = '';
                document.getElementById('preview-domain').textContent = '----.kyk';
                loadDomains();
                loadMyDomains();
            } else {
                showMessage('Error: ' + data.error, 'error');
            }
        }
        
        async function editDomain(name, address, description) {
            const newAddr = prompt('Enter new address (or leave empty):', address);
            const newDesc = prompt('Enter new description (or leave empty):', description);
            
            if (newAddr === null && newDesc === null) return;
            
            const res = await fetch('/api/domains/update', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ name, address: newAddr || '', description: newDesc || '' })
            });
            const data = await res.json();
            
            if (data.success) {
                showMessage('Domain updated successfully!', 'success');
                loadMyDomains();
            } else {
                showMessage('Error: ' + data.error, 'error');
            }
        }
        
        async function renewDomain(name) {
            if (!confirm('Renew ' + name + '.kyk for another year?')) return;
            
            const res = await fetch('/api/domains/renew', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ name })
            });
            const data = await res.json();
            
            if (data.success) {
                showMessage(data.message, 'success');
                loadMyDomains();
            } else {
                showMessage('Error: ' + data.error, 'error');
            }
        }
        
        async function lookupDomain() {
            const domain = document.getElementById('lookup-domain').value;
            if (!domain) {
                showMessage('Enter a domain to lookup', 'error');
                return;
            }
            
            const res = await fetch('/api/domains/resolve?domain=' + encodeURIComponent(domain));
            const data = await res.json();
            const container = document.getElementById('lookup-result');
            
            if (!data.success) {
                container.innerHTML = '<div class="domain"><div class="domain-name" style="color: var(--red);">NOT_FOUND</div><div class="domain-desc">' + data.error + '</div></div>';
                return;
            }
            
            container.innerHTML = '<div class="domain">' +
                '<div class="domain-name">' + data.full_name.toUpperCase() + '</div>' +
                '<div class="domain-desc">' + (data.description || '// No description') + '</div>' +
                '<div class="domain-meta" style="flex-direction: column; align-items: flex-start; gap: 5px;">' +
                    '<span>NODE_ID: ' + data.node_id + '</span>' +
                    (data.address ? '<span>ADDRESS: ' + data.address + '</span>' : '') +
                    (data.service_type ? '<span>SERVICE: ' + data.service_type.toUpperCase() + '</span>' : '') +
                    '<span>CREATED: ' + data.created_at + '</span>' +
                    '<span>EXPIRES: ' + data.expires_at + '</span>' +
                '</div>' +
            '</div>';
        }
        
        // Initialize
        loadDomains();
        loadMyDomains();
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
        .logo { font-size: 24px; color: var(--green); text-decoration: none; text-shadow: 0 0 10px var(--green-glow); display: flex; align-items: center; gap: 8px; }
        .logo img { height: 32px; width: auto; }
        .logo-text::before { content: "["; color: var(--green-dim); } .logo-text::after { content: "]"; color: var(--green-dim); }
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
                    <a href="/" class="logo"><img src="/assets/logo.png" alt="KayakNet"><span class="logo-text">KAYAKNET</span></a>
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
                        <h3>VERSION</h3>
                        <div class="status-value" id="version" style="font-size:18px">--</div>
                    </div>
                </div>
                <div id="update-section" style="display:none; margin-bottom: 20px; padding: 15px; border: 1px solid var(--amber); background: rgba(255,170,0,0.1);">
                    <span style="color: var(--amber);">[UPDATE]</span> New version available: <span id="new-version"></span>
                    <button onclick="applyUpdate()" style="margin-left: 15px; padding: 5px 15px; background: var(--amber); color: #000; border: none; cursor: pointer; font-family: inherit;">DOWNLOAD & INSTALL</button>
                    <span id="update-status" style="margin-left: 10px;"></span>
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
        async function checkUpdates() {
            try {
                const res = await fetch('/api/update');
                const data = await res.json();
                document.getElementById('version').textContent = data.current_version || '--';
                if (data.update_available) {
                    document.getElementById('update-section').style.display = 'block';
                    document.getElementById('new-version').textContent = data.latest_version;
                }
            } catch(e) {
                console.error('Update check failed:', e);
            }
        }
        async function applyUpdate() {
            const status = document.getElementById('update-status');
            status.textContent = 'DOWNLOADING...';
            status.style.color = 'var(--amber)';
            try {
                const res = await fetch('/api/update/apply', { method: 'POST' });
                const data = await res.json();
                if (data.success) {
                    status.textContent = 'SUCCESS! RESTART REQUIRED';
                    status.style.color = 'var(--green)';
                    alert('Update installed successfully! Please restart KayakNet to use the new version.');
                } else {
                    status.textContent = 'FAILED: ' + (data.error || 'Unknown error');
                    status.style.color = '#ff0000';
                }
            } catch(e) {
                status.textContent = 'ERROR: ' + e.message;
                status.style.color = '#ff0000';
            }
        }
        loadNetwork();
        checkUpdates();
        setInterval(loadNetwork, 5000);
        setInterval(checkUpdates, 60000);
    </script>
</body>
</html>`

const kayakerPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>KAYAKER // KAYAKNET</title>
    <style>
        @import url('https://fonts.googleapis.com/css2?family=VT323&family=Space+Grotesk:wght@400;600;700&display=swap');
        * { margin: 0; padding: 0; box-sizing: border-box; }
        :root {
            --bg: #000000;
            --bg-card: #0a0a0a;
            --bg-hover: #111111;
            --green: #00ff00;
            --green-dim: #00aa00;
            --green-glow: #00ff0066;
            --amber: #ffaa00;
            --red: #ff4444;
            --border: #00ff0033;
            --border-light: #00ff0022;
        }
        body {
            font-family: 'VT323', monospace;
            background: var(--bg);
            color: var(--green);
            min-height: 100vh;
        }
        body::before {
            content: "";
            position: fixed;
            top: 0; left: 0; right: 0; bottom: 0;
            background: repeating-linear-gradient(0deg, rgba(0,0,0,0.15) 0px, rgba(0,0,0,0.15) 1px, transparent 1px, transparent 2px);
            pointer-events: none;
            z-index: 1000;
        }
        .container {
            max-width: 700px;
            margin: 0 auto;
            min-height: 100vh;
            border-left: 1px solid var(--border);
            border-right: 1px solid var(--border);
        }
        header {
            position: sticky;
            top: 0;
            background: rgba(0,0,0,0.95);
            backdrop-filter: blur(10px);
            border-bottom: 1px solid var(--border);
            padding: 15px 20px;
            z-index: 100;
            display: flex;
            justify-content: space-between;
            align-items: center;
        }
        .logo {
            font-size: 28px;
            color: var(--green);
            text-shadow: 0 0 10px var(--green-glow);
        }
        nav {
            display: flex;
            gap: 20px;
        }
        nav a {
            color: var(--green-dim);
            text-decoration: none;
            font-size: 18px;
            padding: 8px 16px;
            border: 1px solid transparent;
            transition: all 0.2s;
        }
        nav a:hover, nav a.active {
            color: var(--green);
            border-color: var(--green);
            text-shadow: 0 0 10px var(--green-glow);
        }
        .tabs {
            display: flex;
            border-bottom: 1px solid var(--border);
        }
        .tab {
            flex: 1;
            padding: 15px;
            text-align: center;
            color: var(--green-dim);
            cursor: pointer;
            border-bottom: 2px solid transparent;
            transition: all 0.2s;
        }
        .tab:hover {
            background: var(--bg-hover);
        }
        .tab.active {
            color: var(--green);
            border-bottom-color: var(--green);
        }
        .compose-area {
            padding: 20px;
            border-bottom: 1px solid var(--border);
        }
        .compose-box {
            display: flex;
            gap: 15px;
        }
        .avatar {
            width: 48px;
            height: 48px;
            border-radius: 50%;
            background: var(--green-dim);
            border: 2px solid var(--green);
            display: flex;
            align-items: center;
            justify-content: center;
            font-size: 20px;
            flex-shrink: 0;
        }
        .avatar.small {
            width: 36px;
            height: 36px;
            font-size: 14px;
        }
        .compose-input {
            flex: 1;
        }
        .compose-input textarea {
            width: 100%;
            background: transparent;
            border: none;
            color: var(--green);
            font-family: inherit;
            font-size: 20px;
            resize: none;
            outline: none;
            min-height: 80px;
        }
        .compose-input textarea::placeholder {
            color: var(--green-dim);
        }
        .compose-actions {
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin-top: 15px;
            padding-top: 15px;
            border-top: 1px solid var(--border-light);
        }
        .char-count {
            color: var(--green-dim);
            font-size: 16px;
        }
        .char-count.warning { color: var(--amber); }
        .char-count.error { color: var(--red); }
        .btn {
            padding: 10px 24px;
            background: var(--green);
            color: var(--bg);
            border: none;
            font-family: inherit;
            font-size: 18px;
            font-weight: bold;
            cursor: pointer;
            transition: all 0.2s;
        }
        .btn:hover {
            box-shadow: 0 0 20px var(--green-glow);
        }
        .btn:disabled {
            opacity: 0.5;
            cursor: not-allowed;
        }
        .btn.outline {
            background: transparent;
            color: var(--green);
            border: 1px solid var(--green);
        }
        .feed {
            display: flex;
            flex-direction: column;
        }
        .post {
            padding: 15px 20px;
            border-bottom: 1px solid var(--border);
            display: flex;
            gap: 15px;
            cursor: pointer;
            transition: background 0.2s;
        }
        .post:hover {
            background: var(--bg-hover);
        }
        .post-content {
            flex: 1;
            min-width: 0;
        }
        .post-header {
            display: flex;
            align-items: center;
            gap: 8px;
            margin-bottom: 5px;
        }
        .post-name {
            font-weight: bold;
            color: var(--green);
        }
        .post-handle {
            color: var(--green-dim);
        }
        .post-time {
            color: var(--green-dim);
        }
        .post-text {
            font-size: 18px;
            line-height: 1.4;
            word-wrap: break-word;
            white-space: pre-wrap;
        }
        .post-actions {
            display: flex;
            gap: 40px;
            margin-top: 15px;
        }
        .action {
            display: flex;
            align-items: center;
            gap: 8px;
            color: var(--green-dim);
            cursor: pointer;
            transition: color 0.2s;
            font-size: 16px;
        }
        .action:hover {
            color: var(--green);
        }
        .action.liked {
            color: var(--red);
        }
        .action svg {
            width: 18px;
            height: 18px;
        }
        .profile-setup {
            padding: 40px 20px;
            text-align: center;
        }
        .profile-setup h2 {
            font-size: 28px;
            margin-bottom: 10px;
        }
        .profile-setup p {
            color: var(--green-dim);
            margin-bottom: 30px;
        }
        .form-group {
            margin-bottom: 20px;
            text-align: left;
        }
        .form-group label {
            display: block;
            margin-bottom: 8px;
            color: var(--green-dim);
        }
        .form-group input {
            width: 100%;
            padding: 12px;
            background: var(--bg-card);
            border: 1px solid var(--border);
            color: var(--green);
            font-family: inherit;
            font-size: 18px;
        }
        .form-group input:focus {
            outline: none;
            border-color: var(--green);
            box-shadow: 0 0 10px var(--green-glow);
        }
        .empty-state {
            text-align: center;
            padding: 60px 20px;
            color: var(--green-dim);
        }
        .empty-state h3 {
            font-size: 24px;
            margin-bottom: 10px;
            color: var(--green);
        }
        .notification {
            position: fixed;
            bottom: 20px;
            right: 20px;
            padding: 15px 25px;
            background: var(--green);
            color: var(--bg);
            font-size: 18px;
            z-index: 10000;
            animation: slideIn 0.3s ease;
        }
        .notification.error {
            background: var(--red);
        }
        @keyframes slideIn {
            from { transform: translateX(100%); opacity: 0; }
            to { transform: translateX(0); opacity: 1; }
        }
        .loading {
            text-align: center;
            padding: 40px;
            color: var(--green-dim);
        }
        .profile-header {
            padding: 20px;
            border-bottom: 1px solid var(--border);
        }
        .profile-info {
            display: flex;
            gap: 20px;
            align-items: flex-start;
        }
        .profile-avatar {
            width: 80px;
            height: 80px;
            border-radius: 50%;
            background: var(--green-dim);
            border: 3px solid var(--green);
            display: flex;
            align-items: center;
            justify-content: center;
            font-size: 32px;
        }
        .profile-details h2 {
            font-size: 24px;
            margin-bottom: 5px;
        }
        .profile-stats {
            display: flex;
            gap: 20px;
            margin-top: 15px;
        }
        .stat {
            color: var(--green-dim);
        }
        .stat strong {
            color: var(--green);
            margin-right: 5px;
        }
    </style>
</head>
<body>
    <div class="container">
        <header>
            <div class="logo">> KAYAKER_</div>
            <nav>
                <a href="/">HOME</a>
                <a href="/chat">CHAT</a>
                <a href="/marketplace">MARKET</a>
                <a href="/kayaker" class="active">KAYAKER</a>
            </nav>
        </header>

        <div id="profile-setup" style="display:none;">
            <div class="profile-setup">
                <h2>> CREATE YOUR PROFILE</h2>
                <p>Set up your Kayaker identity to start posting</p>
                <div class="form-group">
                    <label>@HANDLE (3-20 characters, letters/numbers/underscore)</label>
                    <input type="text" id="setup-handle" placeholder="your_handle" maxlength="20">
                </div>
                <div class="form-group">
                    <label>DISPLAY NAME</label>
                    <input type="text" id="setup-name" placeholder="Anonymous User" maxlength="50">
                </div>
                <div class="form-group">
                    <label>BIO</label>
                    <input type="text" id="setup-bio" placeholder="Tell the world about yourself..." maxlength="160">
                </div>
                <button class="btn" onclick="createProfile()">CREATE PROFILE</button>
            </div>
        </div>

        <div id="main-content" style="display:none;">
            <div class="tabs">
                <div class="tab active" data-feed="explore">EXPLORE</div>
                <div class="tab" data-feed="home">FOLLOWING</div>
                <div class="tab" data-feed="profile">MY POSTS</div>
            </div>

            <div class="compose-area">
                <div class="compose-box">
                    <div class="avatar" id="my-avatar">?</div>
                    <div class="compose-input">
                        <textarea id="compose-text" placeholder="What's happening?" maxlength="500"></textarea>
                        <div class="compose-actions">
                            <span class="char-count"><span id="char-count">0</span>/500</span>
                            <button class="btn" id="post-btn" onclick="createPost()" disabled>POST</button>
                        </div>
                    </div>
                </div>
            </div>

            <div class="feed" id="feed">
                <div class="loading">Loading posts...</div>
            </div>
        </div>
    </div>

    <script>
        let currentFeed = 'explore';
        let myProfile = null;
        let cursor = '';
        let loading = false;

        // Initialize
        async function init() {
            try {
                const res = await fetch('/api/kayaker/profile');
                const data = await res.json();
                if (data.error || !data.handle) {
                    document.getElementById('profile-setup').style.display = 'block';
                    document.getElementById('main-content').style.display = 'none';
                } else {
                    myProfile = data;
                    document.getElementById('profile-setup').style.display = 'none';
                    document.getElementById('main-content').style.display = 'block';
                    document.getElementById('my-avatar').textContent = data.handle[0].toUpperCase();
                    loadFeed();
                }
            } catch (e) {
                showNotification('Failed to load profile', true);
            }
        }

        // Create profile
        async function createProfile() {
            const handle = document.getElementById('setup-handle').value.trim();
            const name = document.getElementById('setup-name').value.trim() || 'Anonymous';
            const bio = document.getElementById('setup-bio').value.trim();

            if (!/^[a-zA-Z0-9_]{3,20}$/.test(handle)) {
                showNotification('Invalid handle format', true);
                return;
            }

            try {
                const res = await fetch('/api/kayaker/profile', {
                    method: 'POST',
                    headers: {'Content-Type': 'application/json'},
                    body: JSON.stringify({ handle, display_name: name, bio })
                });
                const data = await res.json();
                if (data.error) {
                    showNotification(data.error, true);
                    return;
                }
                myProfile = data;
                document.getElementById('profile-setup').style.display = 'none';
                document.getElementById('main-content').style.display = 'block';
                document.getElementById('my-avatar').textContent = handle[0].toUpperCase();
                showNotification('Profile created!');
                loadFeed();
            } catch (e) {
                showNotification('Failed to create profile', true);
            }
        }

        // Load feed
        async function loadFeed(append = false) {
            if (loading) return;
            loading = true;

            const feedEl = document.getElementById('feed');
            if (!append) {
                feedEl.innerHTML = '<div class="loading">Loading posts...</div>';
                cursor = '';
            }

            try {
                const params = new URLSearchParams({
                    type: currentFeed,
                    limit: '20'
                });
                if (cursor) params.set('cursor', cursor);
                if (currentFeed === 'profile' && myProfile) {
                    params.set('user_id', myProfile.id);
                }

                const res = await fetch('/api/kayaker/feed?' + params);
                const data = await res.json();

                if (data.error) {
                    feedEl.innerHTML = '<div class="empty-state"><h3>Error</h3><p>' + data.error + '</p></div>';
                    return;
                }

                if (!append) feedEl.innerHTML = '';

                if (!data.posts || data.posts.length === 0) {
                    if (!append) {
                        feedEl.innerHTML = '<div class="empty-state"><h3>No posts yet</h3><p>Be the first to post something!</p></div>';
                    }
                    return;
                }

                data.posts.forEach(post => {
                    feedEl.appendChild(createPostElement(post));
                });

                cursor = data.next_cursor || '';
            } catch (e) {
                feedEl.innerHTML = '<div class="empty-state"><h3>Error</h3><p>Failed to load posts</p></div>';
            } finally {
                loading = false;
            }
        }

        // Create post element
        function createPostElement(post) {
            const div = document.createElement('div');
            div.className = 'post';
            div.onclick = () => viewPost(post.id);

            const content = post.decrypted_content || '[Encrypted]';
            const handle = post.author_handle || 'anonymous';
            const time = formatTime(post.created_at);
            const initial = handle[0].toUpperCase();

            div.innerHTML = ` + "`" + `
                <div class="avatar small">${initial}</div>
                <div class="post-content">
                    <div class="post-header">
                        <span class="post-name">${handle}</span>
                        <span class="post-handle">@${handle}</span>
                        <span class="post-time">· ${time}</span>
                    </div>
                    <div class="post-text">${escapeHtml(content)}</div>
                    <div class="post-actions">
                        <div class="action" onclick="event.stopPropagation(); replyTo('${post.id}')">
                            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                                <path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z"/>
                            </svg>
                            <span>${post.reply_count || 0}</span>
                        </div>
                        <div class="action" onclick="event.stopPropagation(); repost('${post.id}')">
                            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                                <path d="M17 1l4 4-4 4M3 11V9a4 4 0 0 1 4-4h14M7 23l-4-4 4-4M21 13v2a4 4 0 0 1-4 4H3"/>
                            </svg>
                            <span>${post.repost_count || 0}</span>
                        </div>
                        <div class="action ${post.liked ? 'liked' : ''}" onclick="event.stopPropagation(); toggleLike('${post.id}', this)">
                            <svg viewBox="0 0 24 24" fill="${post.liked ? 'currentColor' : 'none'}" stroke="currentColor" stroke-width="2">
                                <path d="M20.84 4.61a5.5 5.5 0 0 0-7.78 0L12 5.67l-1.06-1.06a5.5 5.5 0 0 0-7.78 7.78l1.06 1.06L12 21.23l7.78-7.78 1.06-1.06a5.5 5.5 0 0 0 0-7.78z"/>
                            </svg>
                            <span>${post.like_count || 0}</span>
                        </div>
                    </div>
                </div>
            ` + "`" + `;

            return div;
        }

        // Create a new post
        async function createPost() {
            const textarea = document.getElementById('compose-text');
            const content = textarea.value.trim();

            if (!content) return;

            const btn = document.getElementById('post-btn');
            btn.disabled = true;
            btn.textContent = 'POSTING...';

            try {
                const res = await fetch('/api/kayaker/posts', {
                    method: 'POST',
                    headers: {'Content-Type': 'application/json'},
                    body: JSON.stringify({ content, visibility: 'public' })
                });
                const data = await res.json();

                if (data.error) {
                    showNotification(data.error, true);
                    return;
                }

                textarea.value = '';
                document.getElementById('char-count').textContent = '0';
                showNotification('Posted!');
                loadFeed();
            } catch (e) {
                showNotification('Failed to post', true);
            } finally {
                btn.disabled = false;
                btn.textContent = 'POST';
            }
        }

        // Toggle like
        async function toggleLike(postId, el) {
            const isLiked = el.classList.contains('liked');
            const method = isLiked ? 'DELETE' : 'POST';
            const url = isLiked ? '/api/kayaker/like?post_id=' + postId : '/api/kayaker/like';

            try {
                const opts = { method };
                if (!isLiked) {
                    opts.headers = {'Content-Type': 'application/json'};
                    opts.body = JSON.stringify({ post_id: postId });
                }
                await fetch(url, opts);

                el.classList.toggle('liked');
                const countEl = el.querySelector('span');
                const count = parseInt(countEl.textContent) || 0;
                countEl.textContent = isLiked ? Math.max(0, count - 1) : count + 1;

                const svg = el.querySelector('svg');
                svg.setAttribute('fill', isLiked ? 'none' : 'currentColor');
            } catch (e) {
                showNotification('Failed to like post', true);
            }
        }

        // View single post
        function viewPost(postId) {
            // For now, just reload - could implement modal view
            console.log('View post:', postId);
        }

        // Reply to post
        function replyTo(postId) {
            const textarea = document.getElementById('compose-text');
            textarea.focus();
            textarea.dataset.replyTo = postId;
            textarea.placeholder = 'Write your reply...';
        }

        // Repost
        async function repost(postId) {
            showNotification('Reposting coming soon!');
        }

        // Format time
        function formatTime(dateStr) {
            const date = new Date(dateStr);
            const now = new Date();
            const diff = (now - date) / 1000;

            if (diff < 60) return 'now';
            if (diff < 3600) return Math.floor(diff / 60) + 'm';
            if (diff < 86400) return Math.floor(diff / 3600) + 'h';
            return date.toLocaleDateString();
        }

        // Escape HTML
        function escapeHtml(text) {
            const div = document.createElement('div');
            div.textContent = text;
            return div.innerHTML;
        }

        // Show notification
        function showNotification(msg, isError = false) {
            const notif = document.createElement('div');
            notif.className = 'notification' + (isError ? ' error' : '');
            notif.textContent = msg;
            document.body.appendChild(notif);
            setTimeout(() => notif.remove(), 3000);
        }

        // Tab switching
        document.querySelectorAll('.tab').forEach(tab => {
            tab.addEventListener('click', () => {
                document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
                tab.classList.add('active');
                currentFeed = tab.dataset.feed;
                loadFeed();
            });
        });

        // Character count
        document.getElementById('compose-text').addEventListener('input', (e) => {
            const len = e.target.value.length;
            const countEl = document.getElementById('char-count');
            countEl.textContent = len;
            countEl.parentElement.className = 'char-count' + 
                (len > 450 ? ' warning' : '') + 
                (len >= 500 ? ' error' : '');
            document.getElementById('post-btn').disabled = len === 0 || len > 500;
        });

        // Infinite scroll
        window.addEventListener('scroll', () => {
            if (window.innerHeight + window.scrollY >= document.body.offsetHeight - 500) {
                if (cursor && !loading) loadFeed(true);
            }
        });

        // Initialize
        init();
    </script>
</body>
</html>`
