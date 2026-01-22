package kayaker

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"

	"golang.org/x/crypto/curve25519"
)

var (
	ErrInvalidKey       = errors.New("invalid key")
	ErrDecryptionFailed = errors.New("decryption failed")
	ErrInvalidSignature = errors.New("invalid signature")
)

// KeyPair holds an identity key pair
type KeyPair struct {
	PublicKey  []byte `json:"public_key"`
	PrivateKey []byte `json:"private_key"`
}

// GenerateKeyPair creates a new Ed25519 key pair
func GenerateKeyPair() (*KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &KeyPair{
		PublicKey:  pub,
		PrivateKey: priv,
	}, nil
}

// GenerateSymmetricKey creates a random 256-bit key
func GenerateSymmetricKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	return key, nil
}

// GenerateFollowerKey creates a key to share with followers
func GenerateFollowerKey() ([]byte, error) {
	return GenerateSymmetricKey()
}

// EncryptContent encrypts content using AES-256-GCM
func EncryptContent(plaintext []byte, key []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, ErrInvalidKey
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

	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

// DecryptContent decrypts content using AES-256-GCM
func DecryptContent(ciphertext []byte, key []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, ErrInvalidKey
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

	nonce, ciphertext := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecryptionFailed
	}

	return plaintext, nil
}

// EncryptForRecipient encrypts a key for a specific recipient using X25519 key exchange
func EncryptForRecipient(data []byte, recipientPubKey []byte) ([]byte, error) {
	if len(recipientPubKey) != 32 {
		return nil, ErrInvalidKey
	}

	// Generate ephemeral key pair
	var ephemeralPriv, ephemeralPub [32]byte
	if _, err := io.ReadFull(rand.Reader, ephemeralPriv[:]); err != nil {
		return nil, err
	}
	curve25519.ScalarBaseMult(&ephemeralPub, &ephemeralPriv)

	// Compute shared secret
	var recipientKey [32]byte
	copy(recipientKey[:], recipientPubKey)
	var sharedSecret [32]byte
	curve25519.ScalarMult(&sharedSecret, &ephemeralPriv, &recipientKey)

	// Derive encryption key from shared secret
	encKey := sha256.Sum256(sharedSecret[:])

	// Encrypt data
	ciphertext, err := EncryptContent(data, encKey[:])
	if err != nil {
		return nil, err
	}

	// Prepend ephemeral public key
	result := make([]byte, 32+len(ciphertext))
	copy(result[:32], ephemeralPub[:])
	copy(result[32:], ciphertext)

	return result, nil
}

// DecryptFromSender decrypts data from a sender using X25519
func DecryptFromSender(encryptedData []byte, recipientPrivKey []byte) ([]byte, error) {
	if len(encryptedData) < 33 || len(recipientPrivKey) != 32 {
		return nil, ErrInvalidKey
	}

	// Extract ephemeral public key
	var ephemeralPub [32]byte
	copy(ephemeralPub[:], encryptedData[:32])
	ciphertext := encryptedData[32:]

	// Compute shared secret
	var privKey [32]byte
	copy(privKey[:], recipientPrivKey)
	var sharedSecret [32]byte
	curve25519.ScalarMult(&sharedSecret, &privKey, &ephemeralPub)

	// Derive decryption key
	decKey := sha256.Sum256(sharedSecret[:])

	// Decrypt
	return DecryptContent(ciphertext, decKey[:])
}

// Sign signs data with an Ed25519 private key
func Sign(data []byte, privateKey []byte) []byte {
	return ed25519.Sign(privateKey, data)
}

// Verify verifies a signature with an Ed25519 public key
func Verify(data []byte, signature []byte, publicKey []byte) bool {
	if len(publicKey) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(publicKey, data, signature)
}

// HashContent creates a SHA-256 hash of content
func HashContent(content []byte) string {
	hash := sha256.Sum256(content)
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

// DerivePublicViewKey derives a public view key from the identity
// This allows anyone to decrypt "public" visibility posts
func DerivePublicViewKey(identityPublicKey []byte) []byte {
	// For public posts, we use a deterministic key derived from identity
	// This is a simplified approach - real implementation might use a published key
	hash := sha256.Sum256(append([]byte("kayaker:public:"), identityPublicKey...))
	return hash[:]
}

// Ed25519ToX25519Public converts an Ed25519 public key to X25519
func Ed25519ToX25519Public(edPub []byte) []byte {
	if len(edPub) != 32 {
		return nil
	}
	// Simplified conversion - in production use a proper library
	// This is a placeholder that returns the key as-is for compatibility
	return edPub
}

// Ed25519ToX25519Private converts an Ed25519 private key to X25519
func Ed25519ToX25519Private(edPriv []byte) []byte {
	if len(edPriv) < 32 {
		return nil
	}
	// Use first 32 bytes of Ed25519 seed as X25519 private key
	result := make([]byte, 32)
	copy(result, edPriv[:32])
	return result
}

