// Package kayaknet provides mobile bindings for KayakNet
// Build with: gomobile bind -target=android -o kayaknet.aar ./mobile/gomobile
package kayaknet

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kayaknet/kayaknet/internal/chat"
	"github.com/kayaknet/kayaknet/internal/config"
	"github.com/kayaknet/kayaknet/internal/e2e"
	"github.com/kayaknet/kayaknet/internal/escrow"
	"github.com/kayaknet/kayaknet/internal/identity"
	"github.com/kayaknet/kayaknet/internal/market"
	"github.com/kayaknet/kayaknet/internal/mix"
	"github.com/kayaknet/kayaknet/internal/names"
	"github.com/kayaknet/kayaknet/internal/onion"
	"github.com/kayaknet/kayaknet/internal/security"
)

// Version of the mobile library
const Version = "0.1.14"

// MobileNode wraps the KayakNet node for mobile use
type MobileNode struct {
	mu          sync.RWMutex
	config      *config.Config
	identity    *identity.Identity
	onionRouter *onion.Router
	mixer       *mix.Mixer
	marketplace *market.Marketplace
	chatMgr     *chat.ChatManager
	nameService *names.NameService
	escrowMgr   *escrow.EscrowManager
	e2e         *e2e.E2EManager
	listener    net.PacketConn
	connections map[string]*connection
	ctx         context.Context
	cancel      context.CancelFunc
	dataDir     string
	
	// Callbacks
	onChatMessage    func(room, sender, message string)
	onDM             func(senderID, senderName, message string)
	onPeerConnected  func(nodeID string)
	onPeerDisconnected func(nodeID string)
	onListingReceived func(listingJSON string)
}

type connection struct {
	NodeID   string
	Address  string
	LastSeen time.Time
}

// MessageCallback is called when a chat message is received
type MessageCallback interface {
	OnChatMessage(room, sender, message string)
	OnDM(senderID, senderName, message string)
}

// ConnectionCallback is called on peer connection events
type ConnectionCallback interface {
	OnPeerConnected(nodeID string)
	OnPeerDisconnected(nodeID string)
}

// ListingCallback is called when marketplace listings are received
type ListingCallback interface {
	OnListingReceived(listingJSON string)
}

// NewMobileNode creates a new KayakNet mobile node
func NewMobileNode(dataDir string) (*MobileNode, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("data directory required")
	}
	
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create data dir: %w", err)
	}
	
	ctx, cancel := context.WithCancel(context.Background())
	
	// Load or create identity
	idPath := filepath.Join(dataDir, "identity.json")
	id, err := identity.LoadOrCreate(idPath)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to load identity: %w", err)
	}
	
	node := &MobileNode{
		connections: make(map[string]*connection),
		ctx:         ctx,
		cancel:      cancel,
		dataDir:     dataDir,
		identity:    id,
	}
	
	// Initialize components
	node.onionRouter = onion.NewRouter(id.NodeID(), id.PublicKey(), ed25519.PrivateKey(id.PrivateKey()))
	
	node.marketplace = market.NewMarketplace(
		id.NodeID(),
		id.PublicKey(),
		ed25519.PrivateKey(id.PrivateKey()),
		id.Sign,
	)
	node.marketplace.SetDataDir(dataDir)
	
	node.chatMgr = chat.NewChatManager(
		id.NodeID(),
		id.PublicKey(),
		"mobile-user",
		id.Sign,
	)
	node.chatMgr.SetDataDir(dataDir)
	
	node.nameService = names.NewNameService(
		id.NodeID(),
		id.PublicKey(),
		id.Sign,
	)
	node.nameService.SetDataDir(dataDir)
	
	node.e2e = e2e.NewE2EManager(
		id.NodeID(),
		id.PublicKey(),
		ed25519.PrivateKey(id.PrivateKey()),
	)
	node.e2e.SetDataDir(dataDir)
	
	// Escrow with bootstrap wallet
	walletConfig := escrow.WalletConfig{}
	node.escrowMgr = escrow.NewEscrowManager(escrow.NewCryptoWallet(walletConfig), 2.0)
	node.escrowMgr.SetDataDir(dataDir)
	
	return node, nil
}

// Start connects to the network
func (n *MobileNode) Start(bootstrapAddr string, port int) error {
	if port == 0 {
		port = 4242
	}
	
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return fmt.Errorf("invalid address: %w", err)
	}
	
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	n.listener = conn
	
	// Start message handler
	go n.handleMessages()
	go n.maintenance()
	
	// Connect to bootstrap
	if bootstrapAddr != "" {
		go n.connectToBootstrap(bootstrapAddr)
	}
	
	log.Printf("[MOBILE] KayakNet started on port %d", port)
	return nil
}

// Stop shuts down the node
func (n *MobileNode) Stop() {
	n.cancel()
	if n.listener != nil {
		n.listener.Close()
	}
	
	// Save all data
	n.marketplace.Save()
	n.chatMgr.Save()
	n.nameService.Save()
	n.escrowMgr.Save()
	n.e2e.Save()
	
	log.Println("[MOBILE] KayakNet stopped")
}

// SetMessageCallback sets the callback for incoming messages
func (n *MobileNode) SetMessageCallback(cb MessageCallback) {
	n.onChatMessage = cb.OnChatMessage
	n.onDM = cb.OnDM
}

// SetConnectionCallback sets the callback for connection events
func (n *MobileNode) SetConnectionCallback(cb ConnectionCallback) {
	n.onPeerConnected = cb.OnPeerConnected
	n.onPeerDisconnected = cb.OnPeerDisconnected
}

// SetListingCallback sets the callback for marketplace listings
func (n *MobileNode) SetListingCallback(cb ListingCallback) {
	// Store callback for listing events
}

// GetNodeID returns this node's ID
func (n *MobileNode) GetNodeID() string {
	return n.identity.NodeID()
}

// GetVersion returns the library version
func (n *MobileNode) GetVersion() string {
	return Version
}

// === CHAT FUNCTIONS ===

// SendChatMessage sends a message to a room
func (n *MobileNode) SendChatMessage(room, message string) error {
	msg, err := n.chatMgr.SendMessage(room, message)
	if err != nil {
		return err
	}
	
	// Broadcast to network
	data, _ := msg.Marshal()
	n.broadcast(0x08, data) // MsgTypeChat
	
	return nil
}

// SendDM sends a direct message
func (n *MobileNode) SendDM(recipientID, message string) error {
	msg, err := n.chatMgr.SendDM(recipientID, message)
	if err != nil {
		return err
	}
	
	// Encrypt and send
	envelope, err := n.e2e.Encrypt(recipientID, msg.Content)
	if err != nil {
		// Send unencrypted if no key
		data, _ := msg.Marshal()
		return n.sendToNode(recipientID, 0x08, data)
	}
	
	data, _ := envelope.Marshal()
	return n.sendToNode(recipientID, 0x08, data)
}

// GetChatRooms returns list of joined rooms
func (n *MobileNode) GetChatRooms() string {
	rooms := n.chatMgr.ListRooms()
	data, _ := json.Marshal(rooms)
	return string(data)
}

// JoinRoom joins a chat room
func (n *MobileNode) JoinRoom(room string) {
	n.chatMgr.JoinRoom(room)
}

// GetChatHistory returns chat history for a room
func (n *MobileNode) GetChatHistory(room string, limit int) string {
	messages := n.chatMgr.GetHistory(room, limit)
	data, _ := json.Marshal(messages)
	return string(data)
}

// GetConversations returns DM conversations
func (n *MobileNode) GetConversations() string {
	convos := n.chatMgr.GetConversations()
	data, _ := json.Marshal(convos)
	return string(data)
}

// SetNickname sets the user's nickname
func (n *MobileNode) SetNickname(nick string) {
	n.chatMgr.SetNick(nick)
}

// === MARKETPLACE FUNCTIONS ===

// GetListings returns marketplace listings
func (n *MobileNode) GetListings(category string, limit int) string {
	listings := n.marketplace.Browse(category, limit)
	data, _ := json.Marshal(listings)
	return string(data)
}

// SearchListings searches the marketplace
func (n *MobileNode) SearchListings(query string) string {
	listings := n.marketplace.Search(query)
	data, _ := json.Marshal(listings)
	return string(data)
}

// GetListing returns a single listing
func (n *MobileNode) GetListing(listingID string) string {
	listing := n.marketplace.GetListing(listingID)
	if listing == nil {
		return ""
	}
	data, _ := json.Marshal(listing)
	return string(data)
}

// CreateListing creates a new marketplace listing
func (n *MobileNode) CreateListing(title, description, category string, price float64, currency string) (string, error) {
	listing, err := n.marketplace.CreateListingFull(title, description, category, price, currency, "", "")
	if err != nil {
		return "", err
	}
	
	// Broadcast to network
	data, _ := listing.Marshal()
	n.broadcast(0x10, data) // MsgTypeListing
	
	return listing.ID, nil
}

// GetMyListings returns user's own listings
func (n *MobileNode) GetMyListings() string {
	listings := n.marketplace.MyListings()
	data, _ := json.Marshal(listings)
	return string(data)
}

// === DOMAIN FUNCTIONS ===

// RegisterDomain registers a .kyk domain
func (n *MobileNode) RegisterDomain(name, description, serviceType string) error {
	reg, err := n.nameService.Register(name, description, serviceType)
	if err != nil {
		return err
	}
	
	// Broadcast to network
	data, _ := reg.Marshal()
	n.broadcast(0x30, data) // MsgTypeNameReg
	
	return nil
}

// ResolveDomain resolves a .kyk domain
func (n *MobileNode) ResolveDomain(domain string) string {
	reg, err := n.nameService.Resolve(domain)
	if err != nil {
		return ""
	}
	data, _ := json.Marshal(reg)
	return string(data)
}

// GetMyDomains returns owned domains
func (n *MobileNode) GetMyDomains() string {
	domains := n.nameService.MyDomains()
	data, _ := json.Marshal(domains)
	return string(data)
}

// SearchDomains searches for domains
func (n *MobileNode) SearchDomains(query string) string {
	domains := n.nameService.Search(query)
	data, _ := json.Marshal(domains)
	return string(data)
}

// === ESCROW FUNCTIONS ===

// CreateEscrow creates a new escrow transaction
func (n *MobileNode) CreateEscrow(listingID, sellerID string, amount float64, currency string) (string, error) {
	escrowTx, err := n.escrowMgr.CreateEscrow(listingID, n.identity.NodeID(), sellerID, amount, currency)
	if err != nil {
		return "", err
	}
	return escrowTx.ID, nil
}

// GetEscrow returns escrow details
func (n *MobileNode) GetEscrow(escrowID string) string {
	escrowTx := n.escrowMgr.GetEscrow(escrowID)
	if escrowTx == nil {
		return ""
	}
	data, _ := json.Marshal(escrowTx)
	return string(data)
}

// GetMyEscrows returns user's escrows
func (n *MobileNode) GetMyEscrows() string {
	escrows := n.escrowMgr.GetUserEscrows(n.identity.NodeID())
	data, _ := json.Marshal(escrows)
	return string(data)
}

// === NETWORK FUNCTIONS ===

// GetPeerCount returns number of connected peers
func (n *MobileNode) GetPeerCount() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.connections)
}

// GetPeers returns connected peers as JSON
func (n *MobileNode) GetPeers() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	
	peers := make([]map[string]string, 0)
	for _, p := range n.connections {
		peers = append(peers, map[string]string{
			"node_id":   p.NodeID,
			"address":   p.Address,
			"last_seen": p.LastSeen.Format(time.RFC3339),
		})
	}
	
	data, _ := json.Marshal(peers)
	return string(data)
}

// IsConnected returns true if connected to the network
func (n *MobileNode) IsConnected() bool {
	return n.GetPeerCount() > 0
}

// IsAnonymous returns true if onion routing is active
func (n *MobileNode) IsAnonymous() bool {
	return n.onionRouter.CanRoute()
}

// GetStats returns network statistics as JSON
func (n *MobileNode) GetStats() string {
	stats := map[string]interface{}{
		"version":       Version,
		"node_id":       n.identity.NodeID()[:16] + "...",
		"peers":         n.GetPeerCount(),
		"anonymous":     n.IsAnonymous(),
		"relay_count":   n.onionRouter.GetRelayCount(),
		"listings":      n.marketplace.ListingCount(),
		"domains":       len(n.nameService.Search("")),
	}
	data, _ := json.Marshal(stats)
	return string(data)
}

// === INTERNAL FUNCTIONS ===

func (n *MobileNode) handleMessages() {
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
			continue
		}
		
		go n.processMessage(buf[:nBytes], addr)
	}
}

func (n *MobileNode) processMessage(data []byte, addr net.Addr) {
	// Simplified message processing
	// In production, this would decode and route messages properly
}

func (n *MobileNode) maintenance() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			// Ping peers, cleanup stale connections
			n.mu.Lock()
			for id, conn := range n.connections {
				if time.Since(conn.LastSeen) > 5*time.Minute {
					delete(n.connections, id)
					if n.onPeerDisconnected != nil {
						go n.onPeerDisconnected(id)
					}
				}
			}
			n.mu.Unlock()
			
			// Save data periodically
			n.marketplace.Save()
			n.chatMgr.Save()
			n.nameService.Save()
		}
	}
}

func (n *MobileNode) connectToBootstrap(addr string) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		log.Printf("[MOBILE] Invalid bootstrap address: %v", err)
		return
	}
	
	// Send ping
	n.listener.WriteTo([]byte{0x01}, udpAddr) // MsgTypePing
	log.Printf("[MOBILE] Connecting to bootstrap: %s", addr)
}

func (n *MobileNode) broadcast(msgType byte, data []byte) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	
	for _, conn := range n.connections {
		addr, err := net.ResolveUDPAddr("udp", conn.Address)
		if err != nil {
			continue
		}
		
		msg := append([]byte{msgType}, data...)
		n.listener.WriteTo(msg, addr)
	}
}

func (n *MobileNode) sendToNode(nodeID string, msgType byte, data []byte) error {
	n.mu.RLock()
	conn, ok := n.connections[nodeID]
	n.mu.RUnlock()
	
	if !ok {
		return fmt.Errorf("peer not connected")
	}
	
	addr, err := net.ResolveUDPAddr("udp", conn.Address)
	if err != nil {
		return err
	}
	
	msg := append([]byte{msgType}, data...)
	_, err = n.listener.WriteTo(msg, addr)
	return err
}

