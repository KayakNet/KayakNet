// Package crypto provides cryptographic primitives for KayakNet
package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
)

// KeyPair represents an Ed25519 key pair for node identity
type KeyPair struct {
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
}

// GenerateKeyPair creates a new Ed25519 key pair and returns pub, priv
func GenerateKeyPair() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate key pair: %w", err)
	}
	return pub, priv, nil
}

// NewKeyPair creates a new KeyPair struct
func NewKeyPair() (*KeyPair, error) {
	pub, priv, err := GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	return &KeyPair{
		PublicKey:  pub,
		PrivateKey: priv,
	}, nil
}

// Sign signs a message with a private key
func Sign(privateKey ed25519.PrivateKey, message []byte) []byte {
	return ed25519.Sign(privateKey, message)
}

// Verify verifies a signature with a public key
func Verify(publicKey ed25519.PublicKey, message, signature []byte) bool {
	return ed25519.Verify(publicKey, message, signature)
}

// KeyPair methods

// Sign signs a message with the private key
func (kp *KeyPair) Sign(message []byte) []byte {
	return ed25519.Sign(kp.PrivateKey, message)
}

// Verify verifies a signature against a message using the public key
func (kp *KeyPair) Verify(message, signature []byte) bool {
	return ed25519.Verify(kp.PublicKey, message, signature)
}

// VerifyWithKey verifies a signature with a given public key
func VerifyWithKey(pubKey ed25519.PublicKey, message, signature []byte) bool {
	return ed25519.Verify(pubKey, message, signature)
}

// PublicKeyHex returns the hex-encoded public key
func (kp *KeyPair) PublicKeyHex() string {
	return hex.EncodeToString(kp.PublicKey)
}

// NodeID returns the node ID derived from public key (first 20 bytes of pubkey)
func (kp *KeyPair) NodeID() []byte {
	return kp.PublicKey[:20]
}

// NodeIDHex returns the hex-encoded node ID
func (kp *KeyPair) NodeIDHex() string {
	return hex.EncodeToString(kp.NodeID())
}

// MarshalPrivateKey returns the raw private key bytes
func (kp *KeyPair) MarshalPrivateKey() []byte {
	return kp.PrivateKey
}

// MarshalPublicKey returns the raw public key bytes
func (kp *KeyPair) MarshalPublicKey() []byte {
	return kp.PublicKey
}

// UnmarshalKeyPair reconstructs a key pair from private key bytes
func UnmarshalKeyPair(privKeyBytes []byte) (*KeyPair, error) {
	if len(privKeyBytes) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid private key size")
	}
	priv := ed25519.PrivateKey(privKeyBytes)
	pub := priv.Public().(ed25519.PublicKey)
	return &KeyPair{
		PublicKey:  pub,
		PrivateKey: priv,
	}, nil
}

// PublicKeyFromBytes creates a public key from raw bytes
func PublicKeyFromBytes(b []byte) (ed25519.PublicKey, error) {
	if len(b) != ed25519.PublicKeySize {
		return nil, errors.New("invalid public key size")
	}
	return ed25519.PublicKey(b), nil
}

// PublicKeyFromHex creates a public key from hex string
func PublicKeyFromHex(s string) (ed25519.PublicKey, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid hex: %w", err)
	}
	return PublicKeyFromBytes(b)
}
