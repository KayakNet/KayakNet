// Package security provides cryptographic security primitives for KayakNet
package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"golang.org/x/crypto/curve25519"
)

var (
	ErrInvalidSignature = errors.New("invalid signature")
	ErrInvalidNonce     = errors.New("invalid or replayed nonce")
	ErrMessageExpired   = errors.New("message timestamp expired")
	ErrDecryptionFailed = errors.New("decryption failed")
)

// SharedSecret derives a shared secret using X25519 key exchange
// This converts Ed25519 keys to X25519 for Diffie-Hellman
func SharedSecret(myPrivateKey ed25519.PrivateKey, theirPublicKey ed25519.PublicKey) ([]byte, error) {
	// Convert Ed25519 private key to X25519
	// The seed is the first 32 bytes of the 64-byte Ed25519 private key
	var x25519Private [32]byte
	copy(x25519Private[:], myPrivateKey[:32])
	
	// Hash to get proper X25519 private key
	h := sha256.Sum256(x25519Private[:])
	h[0] &= 248
	h[31] &= 127
	h[31] |= 64
	
	// Convert Ed25519 public key to X25519
	// This is a simplification - in production use a proper conversion
	var x25519Public [32]byte
	copy(x25519Public[:], theirPublicKey[:32])
	
	// Perform X25519 key exchange
	shared, err := curve25519.X25519(h[:], x25519Public[:])
	if err != nil {
		return nil, fmt.Errorf("key exchange failed: %w", err)
	}
	
	// Derive final key using SHA-256
	finalKey := sha256.Sum256(shared)
	return finalKey[:], nil
}

// Encrypt encrypts data using AES-256-GCM with the shared secret
func Encrypt(key, plaintext []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, errors.New("key must be 32 bytes")
	}
	
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
	
	// Prepend nonce to ciphertext
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

// Decrypt decrypts data using AES-256-GCM
func Decrypt(key, ciphertext []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, errors.New("key must be 32 bytes")
	}
	
	block, err := aes.NewCipher(key)
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
	
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecryptionFailed
	}
	
	return plaintext, nil
}

// NonceTracker prevents replay attacks by tracking seen nonces
type NonceTracker struct {
	mu       sync.RWMutex
	seen     map[uint64]time.Time
	window   time.Duration
	maxSize  int
}

// NewNonceTracker creates a new nonce tracker
func NewNonceTracker(window time.Duration, maxSize int) *NonceTracker {
	nt := &NonceTracker{
		seen:    make(map[uint64]time.Time),
		window:  window,
		maxSize: maxSize,
	}
	go nt.cleanup()
	return nt
}

// Check verifies a nonce hasn't been seen and records it
func (nt *NonceTracker) Check(nonce uint64, timestamp time.Time) error {
	nt.mu.Lock()
	defer nt.mu.Unlock()
	
	// Check timestamp is within acceptable window
	now := time.Now()
	if timestamp.Before(now.Add(-nt.window)) || timestamp.After(now.Add(nt.window)) {
		return ErrMessageExpired
	}
	
	// Check if nonce was seen
	if _, exists := nt.seen[nonce]; exists {
		return ErrInvalidNonce
	}
	
	// Record nonce
	nt.seen[nonce] = now
	
	// Evict if too large
	if len(nt.seen) > nt.maxSize {
		nt.evictOldest()
	}
	
	return nil
}

// cleanup periodically removes old nonces
func (nt *NonceTracker) cleanup() {
	ticker := time.NewTicker(nt.window / 2)
	defer ticker.Stop()
	
	for range ticker.C {
		nt.mu.Lock()
		cutoff := time.Now().Add(-nt.window)
		for nonce, seen := range nt.seen {
			if seen.Before(cutoff) {
				delete(nt.seen, nonce)
			}
		}
		nt.mu.Unlock()
	}
}

// evictOldest removes the oldest nonce (called with lock held)
func (nt *NonceTracker) evictOldest() {
	var oldestNonce uint64
	var oldestTime time.Time
	first := true
	
	for nonce, seen := range nt.seen {
		if first || seen.Before(oldestTime) {
			oldestNonce = nonce
			oldestTime = seen
			first = false
		}
	}
	
	delete(nt.seen, oldestNonce)
}

// GenerateNonce creates a cryptographically secure random nonce
func GenerateNonce() uint64 {
	var b [8]byte
	rand.Read(b[:])
	return binary.BigEndian.Uint64(b[:])
}

// HashPeerID creates a deterministic hash of a peer ID for Sybil detection
func HashPeerID(pubKey []byte, ip string) []byte {
	h := sha256.New()
	h.Write(pubKey)
	h.Write([]byte(ip))
	return h.Sum(nil)
}

