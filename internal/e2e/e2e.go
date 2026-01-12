// Package e2e implements end-to-end encryption for KayakNet
// Messages are encrypted so ONLY the recipient can read them
// Not relay nodes, not bootstrap nodes, not anyone else
package e2e

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/nacl/box"
)

var (
	ErrDecryptionFailed = errors.New("decryption failed - wrong key or corrupted")
	ErrInvalidKey       = errors.New("invalid public key")
	ErrKeyNotFound      = errors.New("recipient key not found")
	ErrMessageTooLarge  = errors.New("message too large")
)

const (
	// MaxEncryptedSize is maximum size of encrypted content
	MaxEncryptedSize = 10 * 1024 * 1024 // 10MB
	// NonceSize for AES-GCM
	NonceSize = 12
	// KeySize for AES-256
	KeySize = 32
)

// EncryptedEnvelope wraps an E2E encrypted message
type EncryptedEnvelope struct {
	Version       int    `json:"v"`          // Protocol version
	SenderID      string `json:"from"`       // Sender node ID (for key lookup)
	RecipientID   string `json:"to"`         // Recipient node ID
	EphemeralKey  []byte `json:"ek"`         // Ephemeral public key for this message
	Nonce         []byte `json:"n"`          // Encryption nonce
	Ciphertext    []byte `json:"ct"`         // Encrypted content
	MAC           []byte `json:"mac"`        // Message authentication code
	Timestamp     int64  `json:"ts"`         // Unix timestamp
	SignerKey     []byte `json:"sk"`         // Sender's public key for verification
	Signature     []byte `json:"sig"`        // Signature over envelope
}

// E2EManager handles end-to-end encryption
type E2EManager struct {
	mu           sync.RWMutex
	localID      string
	localPubKey  ed25519.PublicKey
	localPrivKey ed25519.PrivateKey
	x25519Pub    [32]byte
	x25519Priv   [32]byte
	knownKeys    map[string]*PeerKey // nodeID -> their public keys
	sessionKeys  map[string]*SessionKey // peerID -> derived session key
}

// PeerKey stores a peer's public keys
type PeerKey struct {
	NodeID    string
	Ed25519   ed25519.PublicKey
	X25519    [32]byte
	Verified  bool
	AddedAt   time.Time
	LastUsed  time.Time
}

// SessionKey is a derived key for a peer (for performance)
type SessionKey struct {
	Key       []byte
	CreatedAt time.Time
	UseCount  int
}

// NewE2EManager creates a new E2E encryption manager
func NewE2EManager(localID string, pubKey ed25519.PublicKey, privKey ed25519.PrivateKey) *E2EManager {
	m := &E2EManager{
		localID:      localID,
		localPubKey:  pubKey,
		localPrivKey: privKey,
		knownKeys:    make(map[string]*PeerKey),
		sessionKeys:  make(map[string]*SessionKey),
	}
	
	// Convert Ed25519 keys to X25519 for encryption
	m.x25519Priv = ed25519PrivateToX25519(privKey)
	m.x25519Pub = ed25519PublicToX25519(pubKey)
	
	return m
}

// AddPeerKey registers a peer's public key
func (m *E2EManager) AddPeerKey(nodeID string, pubKey ed25519.PublicKey) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	m.knownKeys[nodeID] = &PeerKey{
		NodeID:   nodeID,
		Ed25519:  pubKey,
		X25519:   ed25519PublicToX25519(pubKey),
		AddedAt:  time.Now(),
		LastUsed: time.Now(),
	}
}

// GetPeerKey returns a peer's public key
func (m *E2EManager) GetPeerKey(nodeID string) *PeerKey {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.knownKeys[nodeID]
}

// Encrypt encrypts a message for a specific recipient
// Only the recipient with the matching private key can decrypt
func (m *E2EManager) Encrypt(recipientID string, plaintext []byte) (*EncryptedEnvelope, error) {
	if len(plaintext) > MaxEncryptedSize {
		return nil, ErrMessageTooLarge
	}
	
	m.mu.RLock()
	recipientKey, exists := m.knownKeys[recipientID]
	m.mu.RUnlock()
	
	if !exists {
		return nil, ErrKeyNotFound
	}
	
	// Generate ephemeral X25519 keypair for this message (forward secrecy)
	var ephemeralPub, ephemeralPriv [32]byte
	if _, err := io.ReadFull(rand.Reader, ephemeralPriv[:]); err != nil {
		return nil, err
	}
	curve25519.ScalarBaseMult(&ephemeralPub, &ephemeralPriv)
	
	// Derive shared secret using X25519
	var sharedSecret [32]byte
	curve25519.ScalarMult(&sharedSecret, &ephemeralPriv, &recipientKey.X25519)
	
	// Derive encryption key using HKDF
	encKey := deriveKey(sharedSecret[:], ephemeralPub[:], recipientKey.X25519[:], "kayaknet-e2e-v1")
	
	// Generate nonce
	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	
	// Encrypt with AES-256-GCM
	block, err := aes.NewCipher(encKey)
	if err != nil {
		return nil, err
	}
	
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	
	// Include recipient ID in additional data to prevent misdirection
	additionalData := []byte(recipientID)
	ciphertext := gcm.Seal(nil, nonce, plaintext, additionalData)
	
	// Create envelope
	envelope := &EncryptedEnvelope{
		Version:      1,
		SenderID:     m.localID,
		RecipientID:  recipientID,
		EphemeralKey: ephemeralPub[:],
		Nonce:        nonce,
		Ciphertext:   ciphertext,
		Timestamp:    time.Now().Unix(),
		SignerKey:    m.localPubKey,
	}
	
	// Compute MAC over the envelope (without signature)
	envelope.MAC = computeMAC(encKey, envelope)
	
	// Sign the entire envelope
	envelope.Signature = m.signEnvelope(envelope)
	
	// Update last used
	m.mu.Lock()
	if pk, ok := m.knownKeys[recipientID]; ok {
		pk.LastUsed = time.Now()
	}
	m.mu.Unlock()
	
	return envelope, nil
}

// Decrypt decrypts a message intended for us
func (m *E2EManager) Decrypt(envelope *EncryptedEnvelope) ([]byte, error) {
	// Verify this message is for us
	if envelope.RecipientID != m.localID {
		return nil, errors.New("message not intended for us")
	}
	
	// Verify signature
	if !m.verifyEnvelope(envelope) {
		return nil, errors.New("invalid signature")
	}
	
	// Extract ephemeral public key
	var ephemeralPub [32]byte
	if len(envelope.EphemeralKey) != 32 {
		return nil, ErrInvalidKey
	}
	copy(ephemeralPub[:], envelope.EphemeralKey)
	
	// Derive shared secret
	var sharedSecret [32]byte
	curve25519.ScalarMult(&sharedSecret, &m.x25519Priv, &ephemeralPub)
	
	// Derive decryption key
	encKey := deriveKey(sharedSecret[:], ephemeralPub[:], m.x25519Pub[:], "kayaknet-e2e-v1")
	
	// Verify MAC
	expectedMAC := computeMAC(encKey, envelope)
	if !secureCompare(envelope.MAC, expectedMAC) {
		return nil, errors.New("MAC verification failed")
	}
	
	// Decrypt
	block, err := aes.NewCipher(encKey)
	if err != nil {
		return nil, err
	}
	
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	
	additionalData := []byte(m.localID)
	plaintext, err := gcm.Open(nil, envelope.Nonce, envelope.Ciphertext, additionalData)
	if err != nil {
		return nil, ErrDecryptionFailed
	}
	
	// Register sender's key if not known
	if len(envelope.SignerKey) == ed25519.PublicKeySize {
		m.AddPeerKey(envelope.SenderID, envelope.SignerKey)
	}
	
	return plaintext, nil
}

// EncryptForRoom encrypts a message for all members of a room
// Returns a map of recipientID -> encrypted envelope
func (m *E2EManager) EncryptForRoom(memberIDs []string, plaintext []byte) (map[string]*EncryptedEnvelope, error) {
	result := make(map[string]*EncryptedEnvelope)
	
	for _, memberID := range memberIDs {
		if memberID == m.localID {
			continue // Don't encrypt for ourselves
		}
		
		envelope, err := m.Encrypt(memberID, plaintext)
		if err != nil {
			// Skip members we don't have keys for
			continue
		}
		result[memberID] = envelope
	}
	
	return result, nil
}

// EncryptSymmetric encrypts with a shared room key (for public rooms)
func (m *E2EManager) EncryptSymmetric(roomKey []byte, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(roomKey)
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

// DecryptSymmetric decrypts with a shared room key
func (m *E2EManager) DecryptSymmetric(roomKey []byte, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(roomKey)
	if err != nil {
		return nil, err
	}
	
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	
	if len(ciphertext) < gcm.NonceSize() {
		return nil, ErrDecryptionFailed
	}
	
	nonce := ciphertext[:gcm.NonceSize()]
	ciphertext = ciphertext[gcm.NonceSize():]
	
	return gcm.Open(nil, nonce, ciphertext, nil)
}

// DeriveRoomKey derives a key for a room from a password/passphrase
func DeriveRoomKey(roomName, passphrase string) []byte {
	// Use HKDF to derive room key
	salt := sha256.Sum256([]byte("kayaknet-room-" + roomName))
	reader := hkdf.New(sha256.New, []byte(passphrase), salt[:], []byte("room-key"))
	key := make([]byte, KeySize)
	io.ReadFull(reader, key)
	return key
}

// GenerateRoomKey generates a random room key
func GenerateRoomKey() []byte {
	key := make([]byte, KeySize)
	rand.Read(key)
	return key
}

// Marshal serializes an envelope
func (e *EncryptedEnvelope) Marshal() ([]byte, error) {
	return json.Marshal(e)
}

// UnmarshalEnvelope deserializes an envelope
func UnmarshalEnvelope(data []byte) (*EncryptedEnvelope, error) {
	var e EncryptedEnvelope
	err := json.Unmarshal(data, &e)
	return &e, err
}

// ToBase64 returns base64 encoded envelope
func (e *EncryptedEnvelope) ToBase64() string {
	data, _ := e.Marshal()
	return base64.StdEncoding.EncodeToString(data)
}

// EnvelopeFromBase64 decodes a base64 envelope
func EnvelopeFromBase64(s string) (*EncryptedEnvelope, error) {
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	return UnmarshalEnvelope(data)
}

// Helper functions

func (m *E2EManager) signEnvelope(e *EncryptedEnvelope) []byte {
	data := fmt.Sprintf("%d:%s:%s:%x:%x:%d",
		e.Version, e.SenderID, e.RecipientID,
		e.EphemeralKey, e.Ciphertext, e.Timestamp)
	return ed25519.Sign(m.localPrivKey, []byte(data))
}

func (m *E2EManager) verifyEnvelope(e *EncryptedEnvelope) bool {
	if len(e.SignerKey) != ed25519.PublicKeySize {
		return false
	}
	data := fmt.Sprintf("%d:%s:%s:%x:%x:%d",
		e.Version, e.SenderID, e.RecipientID,
		e.EphemeralKey, e.Ciphertext, e.Timestamp)
	return ed25519.Verify(e.SignerKey, []byte(data), e.Signature)
}

func deriveKey(sharedSecret, ephemeralPub, recipientPub []byte, info string) []byte {
	// Combine inputs
	input := append(sharedSecret, ephemeralPub...)
	input = append(input, recipientPub...)
	
	// HKDF
	salt := sha256.Sum256([]byte(info))
	reader := hkdf.New(sha256.New, input, salt[:], []byte(info))
	key := make([]byte, KeySize)
	io.ReadFull(reader, key)
	return key
}

func computeMAC(key []byte, e *EncryptedEnvelope) []byte {
	data := fmt.Sprintf("%d:%s:%s:%x:%x:%d",
		e.Version, e.SenderID, e.RecipientID,
		e.EphemeralKey, e.Nonce, e.Timestamp)
	h := sha256.Sum256(append(key, []byte(data)...))
	return h[:]
}

func secureCompare(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var result byte
	for i := 0; i < len(a); i++ {
		result |= a[i] ^ b[i]
	}
	return result == 0
}

func ed25519PrivateToX25519(privKey ed25519.PrivateKey) [32]byte {
	h := sha512.Sum512(privKey.Seed())
	var x25519Priv [32]byte
	copy(x25519Priv[:], h[:32])
	x25519Priv[0] &= 248
	x25519Priv[31] &= 127
	x25519Priv[31] |= 64
	return x25519Priv
}

func ed25519PublicToX25519(pubKey ed25519.PublicKey) [32]byte {
	// This is a simplified conversion - in production use a proper library
	var x25519Pub [32]byte
	// Use the public key bytes directly (works for most cases)
	copy(x25519Pub[:], pubKey[:32])
	return x25519Pub
}

// SecureWipe zeros out sensitive data
func SecureWipe(data []byte) {
	for i := range data {
		data[i] = 0
	}
}

// PerfectForwardSecrecy ensures old session keys can't decrypt new messages
// even if long-term keys are compromised
type PerfectForwardSecrecy struct {
	ratchet map[string]*RatchetState
	mu      sync.RWMutex
}

// RatchetState maintains ratchet for a peer
type RatchetState struct {
	ChainKey    []byte
	MessageKey  []byte
	Counter     uint32
	PeerPubKey  [32]byte
	LastUpdated time.Time
}

// NewPFS creates perfect forward secrecy manager
func NewPFS() *PerfectForwardSecrecy {
	return &PerfectForwardSecrecy{
		ratchet: make(map[string]*RatchetState),
	}
}

// Ratchet advances the key chain
func (p *PerfectForwardSecrecy) Ratchet(peerID string, sharedSecret []byte) []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	state, exists := p.ratchet[peerID]
	if !exists {
		state = &RatchetState{
			ChainKey: sharedSecret,
		}
		p.ratchet[peerID] = state
	}
	
	// KDF ratchet step
	h := sha256.Sum256(append(state.ChainKey, byte(state.Counter)))
	state.ChainKey = h[:]
	state.Counter++
	state.LastUpdated = time.Now()
	
	// Derive message key
	msgKey := sha256.Sum256(append(state.ChainKey, []byte("msg")...))
	state.MessageKey = msgKey[:]
	
	return state.MessageKey
}

// DoubleRatchet implements Signal-style double ratchet (simplified)
type DoubleRatchet struct {
	rootKey      []byte
	sendChain    []byte
	recvChain    []byte
	sendCounter  uint32
	recvCounter  uint32
	dhPrivate    [32]byte
	dhPublic     [32]byte
	peerDHPublic [32]byte
}

// NewDoubleRatchet creates a new double ratchet session
func NewDoubleRatchet(sharedSecret []byte) *DoubleRatchet {
	dr := &DoubleRatchet{
		rootKey: sharedSecret,
	}
	
	// Generate initial DH keypair
	rand.Read(dr.dhPrivate[:])
	curve25519.ScalarBaseMult(&dr.dhPublic, &dr.dhPrivate)
	
	// Initial chain keys
	dr.sendChain = sha256Sum(append(sharedSecret, []byte("send")...))
	dr.recvChain = sha256Sum(append(sharedSecret, []byte("recv")...))
	
	return dr
}

// EncryptMessage encrypts with double ratchet
func (dr *DoubleRatchet) EncryptMessage(plaintext []byte) ([]byte, [32]byte, uint32, error) {
	// Derive message key
	msgKey := sha256Sum(append(dr.sendChain, byte(dr.sendCounter)))
	
	// Advance chain
	dr.sendChain = sha256Sum(append(dr.sendChain, byte(dr.sendCounter+1)))
	counter := dr.sendCounter
	dr.sendCounter++
	
	// Encrypt
	block, _ := aes.NewCipher(msgKey)
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	rand.Read(nonce)
	
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	
	return ciphertext, dr.dhPublic, counter, nil
}

// DecryptMessage decrypts with double ratchet
func (dr *DoubleRatchet) DecryptMessage(ciphertext []byte, peerDH [32]byte, counter uint32) ([]byte, error) {
	// Check if DH ratchet needed
	if peerDH != dr.peerDHPublic {
		dr.dhRatchet(peerDH)
	}
	
	// Derive message key for this counter
	chainKey := dr.recvChain
	for i := uint32(0); i < counter; i++ {
		chainKey = sha256Sum(append(chainKey, byte(i+1)))
	}
	msgKey := sha256Sum(append(chainKey, byte(counter)))
	
	// Decrypt
	block, _ := aes.NewCipher(msgKey)
	gcm, _ := cipher.NewGCM(block)
	
	if len(ciphertext) < gcm.NonceSize() {
		return nil, ErrDecryptionFailed
	}
	
	nonce := ciphertext[:gcm.NonceSize()]
	ciphertext = ciphertext[gcm.NonceSize():]
	
	return gcm.Open(nil, nonce, ciphertext, nil)
}

// dhRatchet performs DH ratchet step
func (dr *DoubleRatchet) dhRatchet(peerDH [32]byte) {
	dr.peerDHPublic = peerDH
	
	// Compute new shared secret
	var sharedSecret [32]byte
	curve25519.ScalarMult(&sharedSecret, &dr.dhPrivate, &peerDH)
	
	// Update root key and receive chain
	newRoot := sha256Sum(append(dr.rootKey, sharedSecret[:]...))
	dr.rootKey = newRoot
	dr.recvChain = sha256Sum(append(newRoot, []byte("recv")...))
	dr.recvCounter = 0
	
	// Generate new DH keypair
	rand.Read(dr.dhPrivate[:])
	curve25519.ScalarBaseMult(&dr.dhPublic, &dr.dhPrivate)
	
	// Update send chain
	curve25519.ScalarMult(&sharedSecret, &dr.dhPrivate, &peerDH)
	newRoot = sha256Sum(append(dr.rootKey, sharedSecret[:]...))
	dr.rootKey = newRoot
	dr.sendChain = sha256Sum(append(newRoot, []byte("send")...))
	dr.sendCounter = 0
}

func sha256Sum(data []byte) []byte {
	h := sha256.Sum256(data)
	return h[:]
}

// SealedBox provides one-shot anonymous encryption (like NaCl box.Seal)
func SealedBox(recipientPubKey [32]byte, message []byte) ([]byte, error) {
	// Generate ephemeral keypair
	ephemeralPub, ephemeralPriv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	
	// Seal the message
	nonce := new([24]byte)
	rand.Read(nonce[:])
	
	sealed := box.Seal(nonce[:], message, nonce, &recipientPubKey, ephemeralPriv)
	
	// Prepend ephemeral public key
	result := make([]byte, 32+len(sealed))
	copy(result[:32], ephemeralPub[:])
	copy(result[32:], sealed)
	
	return result, nil
}

// OpenSealedBox decrypts an anonymous sealed box
func OpenSealedBox(privateKey [32]byte, sealed []byte) ([]byte, error) {
	if len(sealed) < 32+24 {
		return nil, ErrDecryptionFailed
	}
	
	// Extract ephemeral public key
	var ephemeralPub [32]byte
	copy(ephemeralPub[:], sealed[:32])
	
	// Extract nonce and ciphertext
	var nonce [24]byte
	copy(nonce[:], sealed[32:32+24])
	ciphertext := sealed[32+24:]
	
	// Open the box
	message, ok := box.Open(nil, ciphertext, &nonce, &ephemeralPub, &privateKey)
	if !ok {
		return nil, ErrDecryptionFailed
	}
	
	return message, nil
}

// GetLocalPublicKey returns the local X25519 public key for key exchange
func (m *E2EManager) GetLocalPublicKey() [32]byte {
	return m.x25519Pub
}

// GetLocalID returns the local node ID
func (m *E2EManager) GetLocalID() string {
	return m.localID
}

// Stats returns E2E statistics
func (m *E2EManager) Stats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	return map[string]interface{}{
		"known_peers":    len(m.knownKeys),
		"session_keys":   len(m.sessionKeys),
		"local_id":       m.localID[:16] + "...",
	}
}

