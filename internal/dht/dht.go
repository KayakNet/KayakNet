// Package dht implements a Kademlia-like distributed hash table for KayakNet
package dht

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/kayaknet/kayaknet/internal/peer"
)

const (
	// IDLength is the length of node IDs in bytes
	IDLength = 20
	// K is the bucket size (replication factor)
	DefaultK = 20
	// Alpha is the concurrency factor for queries
	DefaultAlpha = 3
)

// Config configures the DHT
type Config struct {
	LocalID         []byte
	LocalPubKey     []byte
	Signer          func([]byte) []byte
	K               int
	Alpha           int
	RecordTTL       time.Duration
	RefreshInterval time.Duration
	BootstrapNodes  []string
}

// NodeInfo represents information about a DHT node
type NodeInfo struct {
	ID       []byte
	PubKey   []byte
	LastSeen time.Time
}

// Record represents a stored DHT record
type Record struct {
	Key       []byte    `json:"key"`
	Value     []byte    `json:"value"`
	Type      string    `json:"type"`
	Owner     []byte    `json:"owner"`
	Signature []byte    `json:"sig"`
	Timestamp time.Time `json:"ts"`
	ExpiresAt time.Time `json:"expires"`
}

// ServiceManifest describes a service in the DHT
type ServiceManifest struct {
	ServiceID   string            `json:"service_id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Category    string            `json:"category"`
	Endpoints   []ServiceEndpoint `json:"endpoints"`
	Owner       []byte            `json:"owner"`
	Signature   []byte            `json:"signature"`
	Timestamp   time.Time         `json:"timestamp"`
}

// ServiceEndpoint describes how to connect to a service
type ServiceEndpoint struct {
	Transport string `json:"transport"` // "udp", "tcp", "tor", "i2p"
	Address   string `json:"address"`
}

// PeerInfo stores information about a peer in the DHT
type PeerInfo struct {
	NodeID    []byte    `json:"node_id"`
	PubKey    []byte    `json:"pub_key"`
	Addresses []string  `json:"addresses"`
	Signature []byte    `json:"signature"`
	Timestamp time.Time `json:"timestamp"`
}

// RevocationEntry in the revocation list
type RevocationEntry struct {
	CapID     string    `json:"cap_id"`
	Reason    string    `json:"reason"`
	RevokedAt time.Time `json:"revoked_at"`
	Signature []byte    `json:"signature"`
}

// Bucket is a k-bucket in the routing table
type Bucket struct {
	mu    sync.RWMutex
	nodes []NodeInfo
	k     int
}

// RoutingTable is the Kademlia routing table
type RoutingTable struct {
	mu       sync.RWMutex
	localID  []byte
	buckets  [IDLength * 8]*Bucket
	k        int
}

// DHT is the distributed hash table
type DHT struct {
	mu           sync.RWMutex
	config       Config
	routingTable *RoutingTable
	records      map[string]*Record
	peerStore    *peer.Store
}

// NewDHT creates a new DHT instance
func NewDHT(config Config, peerStore *peer.Store) (*DHT, error) {
	if config.K == 0 {
		config.K = DefaultK
	}
	if config.Alpha == 0 {
		config.Alpha = DefaultAlpha
	}
	if config.RecordTTL == 0 {
		config.RecordTTL = 24 * time.Hour
	}

	rt := &RoutingTable{
		localID: config.LocalID,
		k:       config.K,
	}
	for i := range rt.buckets {
		rt.buckets[i] = &Bucket{
			nodes: make([]NodeInfo, 0, config.K),
			k:     config.K,
		}
	}

	return &DHT{
		config:       config,
		routingTable: rt,
		records:      make(map[string]*Record),
		peerStore:    peerStore,
	}, nil
}

// XOR computes the XOR distance between two IDs
func XOR(a, b []byte) []byte {
	result := make([]byte, len(a))
	for i := range a {
		if i < len(b) {
			result[i] = a[i] ^ b[i]
		} else {
			result[i] = a[i]
		}
	}
	return result
}

// BucketIndex returns the bucket index for a given node ID
func (rt *RoutingTable) BucketIndex(nodeID []byte) int {
	dist := XOR(rt.localID, nodeID)
	for i, b := range dist {
		for j := 7; j >= 0; j-- {
			if (b>>j)&1 != 0 {
				return i*8 + (7 - j)
			}
		}
	}
	return IDLength*8 - 1
}

// AddNode adds a node to the routing table
func (d *DHT) AddNode(nodeID, pubKey []byte) {
	if bytes.Equal(nodeID, d.config.LocalID) {
		return
	}

	idx := d.routingTable.BucketIndex(nodeID)
	bucket := d.routingTable.buckets[idx]

	bucket.mu.Lock()
	defer bucket.mu.Unlock()

	// Check if node already exists
	for i, n := range bucket.nodes {
		if bytes.Equal(n.ID, nodeID) {
			bucket.nodes[i].LastSeen = time.Now()
			bucket.nodes[i].PubKey = pubKey
			return
		}
	}

	// Add new node
	if len(bucket.nodes) < bucket.k {
		bucket.nodes = append(bucket.nodes, NodeInfo{
			ID:       nodeID,
			PubKey:   pubKey,
			LastSeen: time.Now(),
		})
	}
	// TODO: eviction policy for full buckets
}

// FindClosestNodes finds the k closest nodes to a target ID
func (d *DHT) FindClosestNodes(targetID []byte, k int) []NodeInfo {
	var allNodes []NodeInfo

	for _, bucket := range d.routingTable.buckets {
		bucket.mu.RLock()
		allNodes = append(allNodes, bucket.nodes...)
		bucket.mu.RUnlock()
	}

	// Sort by XOR distance
	sort.Slice(allNodes, func(i, j int) bool {
		distI := XOR(allNodes[i].ID, targetID)
		distJ := XOR(allNodes[j].ID, targetID)
		return bytes.Compare(distI, distJ) < 0
	})

	if len(allNodes) > k {
		allNodes = allNodes[:k]
	}

	return allNodes
}

// NodeCount returns the number of nodes in the routing table
func (d *DHT) NodeCount() int {
	count := 0
	for _, bucket := range d.routingTable.buckets {
		bucket.mu.RLock()
		count += len(bucket.nodes)
		bucket.mu.RUnlock()
	}
	return count
}

// Store stores a record in the DHT
func (d *DHT) Store(key []byte, value []byte, recordType string) error {
	record := &Record{
		Key:       key,
		Value:     value,
		Type:      recordType,
		Owner:     d.config.LocalPubKey,
		Timestamp: time.Now(),
		ExpiresAt: time.Now().Add(d.config.RecordTTL),
	}

	// Sign the record
	toSign := append(key, value...)
	record.Signature = d.config.Signer(toSign)

	d.mu.Lock()
	d.records[hex.EncodeToString(key)] = record
	d.mu.Unlock()

	return nil
}

// Get retrieves a record from the local store
func (d *DHT) Get(key []byte) (*Record, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	record, ok := d.records[hex.EncodeToString(key)]
	if !ok {
		return nil, false
	}

	if time.Now().After(record.ExpiresAt) {
		return nil, false
	}

	return record, true
}

// StoreServiceManifest stores a service manifest in the DHT
func (d *DHT) StoreServiceManifest(ctx context.Context, manifest *ServiceManifest) error {
	data, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}

	key := []byte("service:" + manifest.ServiceID)
	return d.Store(key, data, "service")
}

// GetServiceManifest retrieves a service manifest from the DHT
func (d *DHT) GetServiceManifest(ctx context.Context, serviceID string) (*ServiceManifest, error) {
	key := []byte("service:" + serviceID)
	record, ok := d.Get(key)
	if !ok {
		return nil, fmt.Errorf("service not found: %s", serviceID)
	}

	var manifest ServiceManifest
	if err := json.Unmarshal(record.Value, &manifest); err != nil {
		return nil, fmt.Errorf("failed to unmarshal manifest: %w", err)
	}

	return &manifest, nil
}

// StorePeerInfo stores peer information in the DHT
func (d *DHT) StorePeerInfo(ctx context.Context, info *PeerInfo) error {
	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("failed to marshal peer info: %w", err)
	}

	key := append([]byte("peer:"), info.NodeID...)
	return d.Store(key, data, "peer")
}

// GetPeerInfo retrieves peer information from the DHT
func (d *DHT) GetPeerInfo(ctx context.Context, nodeID []byte) (*PeerInfo, error) {
	key := append([]byte("peer:"), nodeID...)
	record, ok := d.Get(key)
	if !ok {
		return nil, fmt.Errorf("peer not found")
	}

	var info PeerInfo
	if err := json.Unmarshal(record.Value, &info); err != nil {
		return nil, fmt.Errorf("failed to unmarshal peer info: %w", err)
	}

	return &info, nil
}

// VerifyRecord verifies a record's signature
func VerifyRecord(record *Record) bool {
	if len(record.Owner) != ed25519.PublicKeySize {
		return false
	}
	toSign := append(record.Key, record.Value...)
	return ed25519.Verify(record.Owner, toSign, record.Signature)
}

// ListServices returns all locally stored services
func (d *DHT) ListServices() []*ServiceManifest {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var services []*ServiceManifest
	for key, record := range d.records {
		if record.Type != "service" {
			continue
		}
		if time.Now().After(record.ExpiresAt) {
			continue
		}
		_ = key // Unused but required for iteration

		var manifest ServiceManifest
		if err := json.Unmarshal(record.Value, &manifest); err != nil {
			continue
		}
		services = append(services, &manifest)
	}

	return services
}

// CleanExpired removes expired records
func (d *DHT) CleanExpired() {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	for key, record := range d.records {
		if now.After(record.ExpiresAt) {
			delete(d.records, key)
		}
	}
}
