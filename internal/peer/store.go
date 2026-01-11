// Package peer manages peer information and connections
package peer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Info stores information about a peer
type Info struct {
	NodeID       string    `json:"node_id"`
	PublicKey    []byte    `json:"public_key"`
	Addresses    []string  `json:"addresses"`
	LastSeen     time.Time `json:"last_seen"`
	Reputation   float64   `json:"reputation"`
	FailureCount int       `json:"failure_count"`
}

// StoreConfig configures the peer store
type StoreConfig struct {
	Path     string
	MaxPeers int
}

// Store manages peer information
type Store struct {
	mu     sync.RWMutex
	config StoreConfig
	peers  map[string]*Info
}

// NewStore creates a new peer store
func NewStore(config StoreConfig) (*Store, error) {
	store := &Store{
		config: config,
		peers:  make(map[string]*Info),
	}

	// Try to load existing peers
	if config.Path != "" {
		if err := store.load(); err != nil && !os.IsNotExist(err) {
			// Non-fatal, just start with empty store
		}
	}

	return store, nil
}

// Add adds or updates a peer
func (s *Store) Add(info *Info) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.peers[info.NodeID]; ok {
		// Update existing
		existing.Addresses = info.Addresses
		existing.LastSeen = info.LastSeen
		existing.PublicKey = info.PublicKey
	} else {
		// Add new
		if s.config.MaxPeers > 0 && len(s.peers) >= s.config.MaxPeers {
			// Evict oldest
			s.evictOldest()
		}
		s.peers[info.NodeID] = info
	}
}

// Get returns peer info by node ID
func (s *Store) Get(nodeID string) (*Info, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	info, ok := s.peers[nodeID]
	return info, ok
}

// Remove removes a peer
func (s *Store) Remove(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.peers, nodeID)
}

// List returns all peers
func (s *Store) List() []*Info {
	s.mu.RLock()
	defer s.mu.RUnlock()

	peers := make([]*Info, 0, len(s.peers))
	for _, info := range s.peers {
		peers = append(peers, info)
	}
	return peers
}

// Count returns the number of peers
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.peers)
}

// UpdateReputation updates a peer's reputation
func (s *Store) UpdateReputation(nodeID string, delta float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if info, ok := s.peers[nodeID]; ok {
		info.Reputation += delta
		if info.Reputation < -100 {
			info.Reputation = -100
		}
		if info.Reputation > 100 {
			info.Reputation = 100
		}
	}
}

// RecordFailure records a connection failure
func (s *Store) RecordFailure(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if info, ok := s.peers[nodeID]; ok {
		info.FailureCount++
	}
}

// ResetFailures resets failure count (on successful connection)
func (s *Store) ResetFailures(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if info, ok := s.peers[nodeID]; ok {
		info.FailureCount = 0
	}
}

// GetReliable returns peers with low failure count
func (s *Store) GetReliable(maxFailures int) []*Info {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var reliable []*Info
	for _, info := range s.peers {
		if info.FailureCount <= maxFailures {
			reliable = append(reliable, info)
		}
	}
	return reliable
}

// Save persists the peer store to disk
func (s *Store) Save() error {
	if s.config.Path == "" {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Ensure directory exists
	dir := filepath.Dir(s.config.Path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(s.peers, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(s.config.Path, data, 0600)
}

// load loads peers from disk
func (s *Store) load() error {
	data, err := os.ReadFile(s.config.Path)
	if err != nil {
		return err
	}

	var peers map[string]*Info
	if err := json.Unmarshal(data, &peers); err != nil {
		return err
	}

	s.peers = peers
	return nil
}

// evictOldest removes the oldest peer
func (s *Store) evictOldest() {
	var oldestID string
	var oldestTime time.Time

	first := true
	for id, info := range s.peers {
		if first || info.LastSeen.Before(oldestTime) {
			oldestID = id
			oldestTime = info.LastSeen
			first = false
		}
	}

	if oldestID != "" {
		delete(s.peers, oldestID)
	}
}

// Cleanup removes stale peers
func (s *Store) Cleanup(maxAge time.Duration, maxFailures int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	for id, info := range s.peers {
		if info.LastSeen.Before(cutoff) || info.FailureCount > maxFailures {
			delete(s.peers, id)
		}
	}
}
