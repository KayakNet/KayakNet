// Package onion implements onion routing for KayakNet
// All messages are routed through multiple hops - no IP addresses exposed
package onion

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"golang.org/x/crypto/curve25519"
)

const (
	// MinHops is minimum relay hops for anonymity
	MinHops = 3
	// MaxHops is maximum relay hops
	MaxHops = 5
	// CircuitTimeout is how long circuits live
	CircuitTimeout = 10 * time.Minute
)

var (
	ErrNoRoute       = errors.New("no route available")
	ErrCircuitFailed = errors.New("circuit creation failed")
	ErrDecryptFailed = errors.New("decryption failed")
)

// Router handles onion routing
type Router struct {
	mu          sync.RWMutex
	localID     string
	localPubKey ed25519.PublicKey
	localPrivKey ed25519.PrivateKey
	circuits    map[string]*Circuit
	relays      map[string]*Relay
	pendingMsgs map[string]chan []byte
}

// Relay represents a known relay node
type Relay struct {
	NodeID    string
	PubKey    ed25519.PublicKey
	Address   string
	LastSeen  time.Time
	Latency   time.Duration
	Available bool
}

// Circuit is an encrypted path through the network
type Circuit struct {
	ID        string
	Hops      []*CircuitHop
	CreatedAt time.Time
	LastUsed  time.Time
}

// CircuitHop is one hop in a circuit
type CircuitHop struct {
	NodeID     string
	PubKey     ed25519.PublicKey
	SharedKey  []byte // AES key for this hop
	Address    string
}

// OnionPacket is an encrypted layered packet
type OnionPacket struct {
	CircuitID string `json:"cid"`
	HopIndex  int    `json:"hop"`
	Payload   []byte `json:"data"` // Encrypted layer
	IsRelay   bool   `json:"relay"`
}

// OnionLayer is one decrypted layer
type OnionLayer struct {
	NextHop  string `json:"next"`      // Next node to forward to (empty if final)
	NextAddr string `json:"next_addr"` // Address of next hop
	Payload  []byte `json:"payload"`   // Either next layer or final message
	IsFinal  bool   `json:"final"`
}

// NewRouter creates a new onion router
func NewRouter(localID string, pubKey ed25519.PublicKey, privKey ed25519.PrivateKey) *Router {
	r := &Router{
		localID:      localID,
		localPubKey:  pubKey,
		localPrivKey: privKey,
		circuits:     make(map[string]*Circuit),
		relays:       make(map[string]*Relay),
		pendingMsgs:  make(map[string]chan []byte),
	}
	go r.maintenance()
	return r
}

// AddRelay adds a known relay to the router
func (r *Router) AddRelay(nodeID string, pubKey ed25519.PublicKey, address string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	r.relays[nodeID] = &Relay{
		NodeID:    nodeID,
		PubKey:    pubKey,
		Address:   address,
		LastSeen:  time.Now(),
		Available: true,
	}
}

// RemoveRelay removes a relay
func (r *Router) RemoveRelay(nodeID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.relays, nodeID)
}

// GetRelayCount returns number of known relays
func (r *Router) GetRelayCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.relays)
}

// CanRoute returns true if we have enough relays for anonymity
func (r *Router) CanRoute() bool {
	return r.GetRelayCount() >= MinHops
}

// BuildCircuit creates a new circuit to a destination
func (r *Router) BuildCircuit(destID string, destPubKey ed25519.PublicKey, destAddr string) (*Circuit, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	// Need at least MinHops relays (excluding destination)
	if len(r.relays) < MinHops-1 {
		return nil, ErrNoRoute
	}
	
	// Select random relays for hops
	relayList := make([]*Relay, 0, len(r.relays))
	for _, relay := range r.relays {
		if relay.NodeID != destID && relay.Available {
			relayList = append(relayList, relay)
		}
	}
	
	if len(relayList) < MinHops-1 {
		return nil, ErrNoRoute
	}
	
	// Shuffle and pick hops
	shuffleRelays(relayList)
	numHops := MinHops
	if len(relayList) >= MaxHops-1 {
		numHops = MinHops + randInt(MaxHops-MinHops)
	}
	if numHops > len(relayList)+1 {
		numHops = len(relayList) + 1
	}
	
	// Build circuit hops
	circuit := &Circuit{
		ID:        generateCircuitID(),
		Hops:      make([]*CircuitHop, numHops),
		CreatedAt: time.Now(),
		LastUsed:  time.Now(),
	}
	
	// Add relay hops
	for i := 0; i < numHops-1; i++ {
		relay := relayList[i]
		sharedKey, err := deriveSharedKey(r.localPrivKey, relay.PubKey)
		if err != nil {
			return nil, fmt.Errorf("key exchange failed: %w", err)
		}
		circuit.Hops[i] = &CircuitHop{
			NodeID:    relay.NodeID,
			PubKey:    relay.PubKey,
			SharedKey: sharedKey,
			Address:   relay.Address,
		}
	}
	
	// Add destination as final hop
	destSharedKey, err := deriveSharedKey(r.localPrivKey, destPubKey)
	if err != nil {
		return nil, fmt.Errorf("destination key exchange failed: %w", err)
	}
	circuit.Hops[numHops-1] = &CircuitHop{
		NodeID:    destID,
		PubKey:    destPubKey,
		SharedKey: destSharedKey,
		Address:   destAddr,
	}
	
	r.circuits[circuit.ID] = circuit
	return circuit, nil
}

// WrapMessage wraps a message in onion layers for a circuit
func (r *Router) WrapMessage(circuit *Circuit, message []byte) (*OnionPacket, string, error) {
	if len(circuit.Hops) == 0 {
		return nil, "", ErrNoRoute
	}
	
	// Build layers from inside out (last hop first)
	payload := message
	
	for i := len(circuit.Hops) - 1; i >= 0; i-- {
		hop := circuit.Hops[i]
		
		layer := OnionLayer{
			Payload: payload,
			IsFinal: i == len(circuit.Hops)-1,
		}
		
		// If not final, include next hop info
		if i < len(circuit.Hops)-1 {
			layer.NextHop = circuit.Hops[i+1].NodeID
			layer.NextAddr = circuit.Hops[i+1].Address
		}
		
		// Serialize layer
		layerData, err := json.Marshal(layer)
		if err != nil {
			return nil, "", err
		}
		
		// Encrypt with this hop's shared key
		encrypted, err := encryptAES(hop.SharedKey, layerData)
		if err != nil {
			return nil, "", err
		}
		
		payload = encrypted
	}
	
	packet := &OnionPacket{
		CircuitID: circuit.ID,
		HopIndex:  0,
		Payload:   payload,
		IsRelay:   true,
	}
	
	// Return first hop address
	return packet, circuit.Hops[0].Address, nil
}

// UnwrapLayer decrypts one layer of an onion packet (for relay nodes)
func (r *Router) UnwrapLayer(packet *OnionPacket, senderPubKey ed25519.PublicKey) (*OnionLayer, error) {
	// Derive shared key with sender
	sharedKey, err := deriveSharedKey(r.localPrivKey, senderPubKey)
	if err != nil {
		return nil, err
	}
	
	// Decrypt this layer
	decrypted, err := decryptAES(sharedKey, packet.Payload)
	if err != nil {
		return nil, ErrDecryptFailed
	}
	
	var layer OnionLayer
	if err := json.Unmarshal(decrypted, &layer); err != nil {
		return nil, err
	}
	
	return &layer, nil
}

// GetCircuit returns a circuit by ID
func (r *Router) GetCircuit(circuitID string) *Circuit {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.circuits[circuitID]
}

// maintenance cleans up old circuits
func (r *Router) maintenance() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	
	for range ticker.C {
		r.mu.Lock()
		now := time.Now()
		for id, circuit := range r.circuits {
			if now.Sub(circuit.LastUsed) > CircuitTimeout {
				delete(r.circuits, id)
			}
		}
		r.mu.Unlock()
	}
}

// Helper functions

func deriveSharedKey(privKey ed25519.PrivateKey, pubKey ed25519.PublicKey) ([]byte, error) {
	// Convert Ed25519 to X25519 for key exchange
	var x25519Priv [32]byte
	var x25519Pub [32]byte
	
	// Use first 32 bytes of private key seed
	h := sha256.Sum256(privKey.Seed())
	copy(x25519Priv[:], h[:])
	x25519Priv[0] &= 248
	x25519Priv[31] &= 127
	x25519Priv[31] |= 64
	
	// Convert public key
	copy(x25519Pub[:], pubKey[:32])
	
	// X25519 key exchange
	shared, err := curve25519.X25519(x25519Priv[:], x25519Pub[:])
	if err != nil {
		return nil, err
	}
	
	// Derive final key
	final := sha256.Sum256(shared)
	return final[:], nil
}

func encryptAES(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func decryptAES(key, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	
	if len(ciphertext) < gcm.NonceSize() {
		return nil, ErrDecryptFailed
	}
	
	nonce := ciphertext[:gcm.NonceSize()]
	ciphertext = ciphertext[gcm.NonceSize():]
	
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func generateCircuitID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func shuffleRelays(relays []*Relay) {
	for i := len(relays) - 1; i > 0; i-- {
		j := randInt(i + 1)
		relays[i], relays[j] = relays[j], relays[i]
	}
}

func randInt(max int) int {
	b := make([]byte, 1)
	rand.Read(b)
	return int(b[0]) % max
}

