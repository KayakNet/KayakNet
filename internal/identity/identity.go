// Package identity manages node identity using Ed25519 keys
package identity

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kayaknet/kayaknet/pkg/crypto"
)

// Identity represents a node's cryptographic identity
type Identity struct {
	publicKey  ed25519.PublicKey
	privateKey ed25519.PrivateKey
	createdAt  time.Time
}

// StoredIdentity is the JSON format for persisting identity
type StoredIdentity struct {
	PublicKey  string    `json:"public_key"`
	PrivateKey string    `json:"private_key"`
	CreatedAt  time.Time `json:"created_at"`
}

// New creates a new random identity
func New() (*Identity, error) {
	pub, priv, err := crypto.GenerateKeyPair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate key pair: %w", err)
	}

	return &Identity{
		publicKey:  pub,
		privateKey: priv,
		createdAt:  time.Now(),
	}, nil
}

// Load loads an identity from a file
func Load(path string) (*Identity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read identity file: %w", err)
	}

	var stored StoredIdentity
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("failed to parse identity file: %w", err)
	}

	pubKey, err := hex.DecodeString(stored.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("invalid public key: %w", err)
	}

	privKey, err := hex.DecodeString(stored.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	return &Identity{
		publicKey:  ed25519.PublicKey(pubKey),
		privateKey: ed25519.PrivateKey(privKey),
		createdAt:  stored.CreatedAt,
	}, nil
}

// LoadOrCreate loads an existing identity or creates a new one
func LoadOrCreate(path string) (*Identity, error) {
	// Try to load existing
	if _, err := os.Stat(path); err == nil {
		return Load(path)
	}

	// Create new
	id, err := New()
	if err != nil {
		return nil, err
	}

	// Save it
	if err := id.Save(path); err != nil {
		return nil, fmt.Errorf("failed to save new identity: %w", err)
	}

	return id, nil
}

// Save persists the identity to a file
func (i *Identity) Save(path string) error {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	stored := StoredIdentity{
		PublicKey:  hex.EncodeToString(i.publicKey),
		PrivateKey: hex.EncodeToString(i.privateKey),
		CreatedAt:  i.createdAt,
	}

	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal identity: %w", err)
	}

	// Write with restrictive permissions
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write identity file: %w", err)
	}

	return nil
}

// NodeID returns a hex-encoded identifier derived from the public key
func (i *Identity) NodeID() string {
	return hex.EncodeToString(i.publicKey)
}

// PublicKey returns the raw public key bytes
func (i *Identity) PublicKey() []byte {
	return i.publicKey
}

// PublicKeyHex returns the hex-encoded public key
func (i *Identity) PublicKeyHex() string {
	return hex.EncodeToString(i.publicKey)
}

// PrivateKey returns the raw private key bytes
func (i *Identity) PrivateKey() []byte {
	return i.privateKey
}

// Sign signs a message using the identity's private key
func (i *Identity) Sign(message []byte) []byte {
	return crypto.Sign(i.privateKey, message)
}

// Verify verifies a signature using the identity's public key
func (i *Identity) Verify(message, signature []byte) bool {
	return crypto.Verify(i.publicKey, message, signature)
}

// CreatedAt returns when the identity was created
func (i *Identity) CreatedAt() time.Time {
	return i.createdAt
}
