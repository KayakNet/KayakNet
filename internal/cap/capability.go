// Package cap implements the capability-based access control system for KayakNet
package cap

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Capability represents a signed access token
type Capability struct {
	CapID       string    `json:"cap_id"`
	ServiceID   string    `json:"service_id"`
	Grantee     []byte    `json:"grantee"`      // Public key of the holder
	Permissions []string  `json:"permissions"`  // e.g., ["read", "write", "admin"]
	Quota       *Quota    `json:"quota,omitempty"`
	ExpiresAt   time.Time `json:"expires_at"`
	IssuedAt    time.Time `json:"issued_at"`
	IssuerKey   []byte    `json:"issuer_key"`
	Signature   []byte    `json:"signature"`
}

// Quota limits resource usage
type Quota struct {
	MaxRequests   int64 `json:"max_requests,omitempty"`
	MaxBandwidth  int64 `json:"max_bandwidth,omitempty"`  // bytes
	MaxStorage    int64 `json:"max_storage,omitempty"`    // bytes
	UsedRequests  int64 `json:"used_requests,omitempty"`
	UsedBandwidth int64 `json:"used_bandwidth,omitempty"`
	UsedStorage   int64 `json:"used_storage,omitempty"`
}

// Store manages capabilities
type Store struct {
	mu           sync.RWMutex
	capabilities map[string]*Capability
	revoked      map[string]time.Time
}

// NewStore creates a new capability store
func NewStore() *Store {
	return &Store{
		capabilities: make(map[string]*Capability),
		revoked:      make(map[string]time.Time),
	}
}

// NewCapability creates a new unsigned capability
func NewCapability(serviceID string, grantee []byte, permissions []string, ttl time.Duration) *Capability {
	capID := generateCapID()
	return &Capability{
		CapID:       capID,
		ServiceID:   serviceID,
		Grantee:     grantee,
		Permissions: permissions,
		IssuedAt:    time.Now(),
		ExpiresAt:   time.Now().Add(ttl),
	}
}

// generateCapID creates a random capability ID
func generateCapID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Sign signs the capability with the issuer's private key
func (c *Capability) Sign(privateKey ed25519.PrivateKey, publicKey []byte) error {
	c.IssuerKey = publicKey
	
	// Create the data to sign (everything except signature)
	toSign, err := c.dataToSign()
	if err != nil {
		return fmt.Errorf("failed to create signing data: %w", err)
	}
	
	c.Signature = ed25519.Sign(privateKey, toSign)
	return nil
}

// dataToSign creates the canonical data for signing
func (c *Capability) dataToSign() ([]byte, error) {
	data := struct {
		CapID       string    `json:"cap_id"`
		ServiceID   string    `json:"service_id"`
		Grantee     []byte    `json:"grantee"`
		Permissions []string  `json:"permissions"`
		Quota       *Quota    `json:"quota,omitempty"`
		ExpiresAt   time.Time `json:"expires_at"`
		IssuedAt    time.Time `json:"issued_at"`
		IssuerKey   []byte    `json:"issuer_key"`
	}{
		CapID:       c.CapID,
		ServiceID:   c.ServiceID,
		Grantee:     c.Grantee,
		Permissions: c.Permissions,
		Quota:       c.Quota,
		ExpiresAt:   c.ExpiresAt,
		IssuedAt:    c.IssuedAt,
		IssuerKey:   c.IssuerKey,
	}
	return json.Marshal(data)
}

// Verify verifies the capability's signature
func (c *Capability) Verify() error {
	if len(c.IssuerKey) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid issuer key size")
	}
	
	toSign, err := c.dataToSign()
	if err != nil {
		return fmt.Errorf("failed to create verification data: %w", err)
	}
	
	if !ed25519.Verify(c.IssuerKey, toSign, c.Signature) {
		return fmt.Errorf("invalid signature")
	}
	
	return nil
}

// IsExpired checks if the capability has expired
func (c *Capability) IsExpired() bool {
	return time.Now().After(c.ExpiresAt)
}

// HasPermission checks if the capability grants a specific permission
func (c *Capability) HasPermission(perm string) bool {
	for _, p := range c.Permissions {
		if p == perm || p == "*" || p == "admin" {
			return true
		}
	}
	return false
}

// Marshal serializes the capability to JSON
func (c *Capability) Marshal() ([]byte, error) {
	return json.MarshalIndent(c, "", "  ")
}

// Unmarshal deserializes a capability from JSON
func Unmarshal(data []byte) (*Capability, error) {
	var c Capability
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Store methods

// Store adds a capability to the store
func (s *Store) Store(c *Capability) error {
	if err := c.Verify(); err != nil {
		return fmt.Errorf("invalid capability: %w", err)
	}
	
	s.mu.Lock()
	defer s.mu.Unlock()
	
	if _, revoked := s.revoked[c.CapID]; revoked {
		return fmt.Errorf("capability is revoked")
	}
	
	s.capabilities[c.CapID] = c
	return nil
}

// Get retrieves a capability by ID
func (s *Store) Get(capID string) (*Capability, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	c, ok := s.capabilities[capID]
	if !ok {
		return nil, false
	}
	
	if c.IsExpired() {
		return nil, false
	}
	
	return c, true
}

// GetByService returns all capabilities for a service
func (s *Store) GetByService(serviceID string) []*Capability {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	var caps []*Capability
	for _, c := range s.capabilities {
		if c.ServiceID == serviceID && !c.IsExpired() {
			caps = append(caps, c)
		}
	}
	return caps
}

// Revoke revokes a capability
func (s *Store) Revoke(capID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	s.revoked[capID] = time.Now()
	delete(s.capabilities, capID)
}

// IsRevoked checks if a capability is revoked
func (s *Store) IsRevoked(capID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revoked[capID] != (time.Time{})
}

// CleanExpired removes expired capabilities
func (s *Store) CleanExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	for id, c := range s.capabilities {
		if c.IsExpired() {
			delete(s.capabilities, id)
		}
	}
}

// List returns all valid capabilities
func (s *Store) List() []*Capability {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	var caps []*Capability
	for _, c := range s.capabilities {
		if !c.IsExpired() {
			caps = append(caps, c)
		}
	}
	return caps
}

// CheckAccess verifies if a grantee has permission for a service
func (s *Store) CheckAccess(serviceID string, grantee []byte, permission string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	for _, c := range s.capabilities {
		if c.ServiceID != serviceID {
			continue
		}
		if c.IsExpired() {
			continue
		}
		if string(c.Grantee) != string(grantee) {
			continue
		}
		if c.HasPermission(permission) {
			return true
		}
	}
	return false
}

// UpdateQuota updates the usage for a capability
func (s *Store) UpdateQuota(capID string, requests, bandwidth, storage int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	c, ok := s.capabilities[capID]
	if !ok {
		return fmt.Errorf("capability not found")
	}
	
	if c.Quota == nil {
		return nil // No quota restrictions
	}
	
	c.Quota.UsedRequests += requests
	c.Quota.UsedBandwidth += bandwidth
	c.Quota.UsedStorage += storage
	
	return nil
}

// CheckQuota verifies if the capability is within quota limits
func (c *Capability) CheckQuota() bool {
	if c.Quota == nil {
		return true
	}
	
	if c.Quota.MaxRequests > 0 && c.Quota.UsedRequests >= c.Quota.MaxRequests {
		return false
	}
	if c.Quota.MaxBandwidth > 0 && c.Quota.UsedBandwidth >= c.Quota.MaxBandwidth {
		return false
	}
	if c.Quota.MaxStorage > 0 && c.Quota.UsedStorage >= c.Quota.MaxStorage {
		return false
	}
	
	return true
}
