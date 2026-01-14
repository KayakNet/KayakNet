// Package names implements the KayakNet Naming System (.kyk domains)
// These domains only resolve inside the KayakNet network
package names

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	// DomainSuffix is the KayakNet domain suffix
	DomainSuffix = ".kyk"
	
	// MaxNameLength is maximum domain name length (without suffix)
	MaxNameLength = 32
	
	// MinNameLength is minimum domain name length
	MinNameLength = 3
	
	// RegistrationTTL is how long a registration lasts
	RegistrationTTL = 365 * 24 * time.Hour // 1 year
	
	// RenewalWindow is when renewal becomes available
	RenewalWindow = 30 * 24 * time.Hour // 30 days before expiry
)

var (
	ErrInvalidName     = errors.New("invalid domain name")
	ErrNameTaken       = errors.New("domain name already registered")
	ErrNotOwner        = errors.New("not the domain owner")
	ErrNameNotFound    = errors.New("domain not found")
	ErrNameExpired     = errors.New("domain registration expired")
	
	// Valid name pattern: alphanumeric and hyphens, no leading/trailing hyphens
	validNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*[a-z0-9]$|^[a-z0-9]$`)
)

// Registration represents a .kyk domain registration
type Registration struct {
	Name        string    `json:"name"`        // e.g., "myservice"
	FullName    string    `json:"full_name"`   // e.g., "myservice.kyk"
	NodeID      string    `json:"node_id"`     // Owner's node ID
	OwnerKey    []byte    `json:"owner_key"`   // Owner's public key
	Address     string    `json:"address"`     // Current address (can be updated)
	Description string    `json:"description"` // Optional description
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Signature   []byte    `json:"signature"`   // Owner's signature
	
	// Optional service info
	ServiceType string            `json:"service_type,omitempty"` // "chat", "market", "file", etc.
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// NameService manages .kyk domain registrations
type NameService struct {
	mu          sync.RWMutex
	names       map[string]*Registration // name -> registration
	byNodeID    map[string][]string      // nodeID -> names owned
	localID     string
	localKey    ed25519.PublicKey
	signFunc    func([]byte) []byte
	dataDir     string                   // Directory for persistent storage
}

// NewNameService creates a new name service
func NewNameService(localID string, pubKey ed25519.PublicKey, signFunc func([]byte) []byte) *NameService {
	return &NameService{
		names:    make(map[string]*Registration),
		byNodeID: make(map[string][]string),
		localID:  localID,
		localKey: pubKey,
		signFunc: signFunc,
	}
}

// SetDataDir sets the data directory for persistence
func (ns *NameService) SetDataDir(dataDir string) error {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	
	ns.dataDir = filepath.Join(dataDir, "names")
	if err := os.MkdirAll(ns.dataDir, 0700); err != nil {
		return err
	}
	
	// Load existing data
	ns.loadData()
	return nil
}

// NamesData holds domain data for persistence
type NamesData struct {
	Names    map[string]*Registration `json:"names"`
	ByNodeID map[string][]string      `json:"by_node_id"`
}

// Save persists domain data to disk
func (ns *NameService) Save() error {
	if ns.dataDir == "" {
		return nil
	}
	
	ns.mu.RLock()
	data := NamesData{
		Names:    ns.names,
		ByNodeID: ns.byNodeID,
	}
	ns.mu.RUnlock()
	
	path := filepath.Join(ns.dataDir, "names.json")
	tmpPath := path + ".tmp"
	
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return err
	}
	
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	
	return os.Rename(tmpPath, path)
}

// loadData loads domain data from disk
func (ns *NameService) loadData() {
	if ns.dataDir == "" {
		return
	}
	
	path := filepath.Join(ns.dataDir, "names.json")
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	
	var data NamesData
	if err := json.NewDecoder(f).Decode(&data); err != nil {
		return
	}
	
	if data.Names != nil {
		ns.names = data.Names
	}
	if data.ByNodeID != nil {
		ns.byNodeID = data.ByNodeID
	}
}

// Register registers a new .kyk domain
func (ns *NameService) Register(name, description, serviceType string) (*Registration, error) {
	name = normalizeName(name)
	
	if err := validateName(name); err != nil {
		return nil, err
	}
	
	ns.mu.Lock()
	defer ns.mu.Unlock()
	
	// Check if name is taken
	if existing, ok := ns.names[name]; ok {
		if time.Now().Before(existing.ExpiresAt) {
			return nil, ErrNameTaken
		}
		// Expired - can be re-registered
	}
	
	reg := &Registration{
		Name:        name,
		FullName:    name + DomainSuffix,
		NodeID:      ns.localID,
		OwnerKey:    ns.localKey,
		Description: description,
		ServiceType: serviceType,
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(RegistrationTTL),
		UpdatedAt:   time.Now(),
		Metadata:    make(map[string]string),
	}
	
	// Sign the registration
	reg.Signature = ns.signRegistration(reg)
	
	ns.names[name] = reg
	ns.byNodeID[ns.localID] = append(ns.byNodeID[ns.localID], name)
	
	return reg, nil
}

// Resolve resolves a .kyk domain to its registration
func (ns *NameService) Resolve(domain string) (*Registration, error) {
	name := normalizeName(strings.TrimSuffix(domain, DomainSuffix))
	
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	
	reg, ok := ns.names[name]
	if !ok {
		return nil, ErrNameNotFound
	}
	
	if time.Now().After(reg.ExpiresAt) {
		return nil, ErrNameExpired
	}
	
	return reg, nil
}

// Update updates a domain's address or metadata
func (ns *NameService) Update(name, address, description string) error {
	name = normalizeName(name)
	
	ns.mu.Lock()
	defer ns.mu.Unlock()
	
	reg, ok := ns.names[name]
	if !ok {
		return ErrNameNotFound
	}
	
	if reg.NodeID != ns.localID {
		return ErrNotOwner
	}
	
	if address != "" {
		reg.Address = address
	}
	if description != "" {
		reg.Description = description
	}
	reg.UpdatedAt = time.Now()
	reg.Signature = ns.signRegistration(reg)
	
	return nil
}

// Renew renews a domain registration
func (ns *NameService) Renew(name string) error {
	name = normalizeName(name)
	
	ns.mu.Lock()
	defer ns.mu.Unlock()
	
	reg, ok := ns.names[name]
	if !ok {
		return ErrNameNotFound
	}
	
	if reg.NodeID != ns.localID {
		return ErrNotOwner
	}
	
	reg.ExpiresAt = time.Now().Add(RegistrationTTL)
	reg.UpdatedAt = time.Now()
	reg.Signature = ns.signRegistration(reg)
	
	return nil
}

// Transfer transfers ownership to another node
func (ns *NameService) Transfer(name, newNodeID string, newOwnerKey []byte) error {
	name = normalizeName(name)
	
	ns.mu.Lock()
	defer ns.mu.Unlock()
	
	reg, ok := ns.names[name]
	if !ok {
		return ErrNameNotFound
	}
	
	if reg.NodeID != ns.localID {
		return ErrNotOwner
	}
	
	// Remove from our ownership
	ns.removeFromOwnership(ns.localID, name)
	
	// Update registration
	reg.NodeID = newNodeID
	reg.OwnerKey = newOwnerKey
	reg.UpdatedAt = time.Now()
	// Note: new owner needs to sign
	
	// Add to new owner's list
	ns.byNodeID[newNodeID] = append(ns.byNodeID[newNodeID], name)
	
	return nil
}

// MyDomains returns domains owned by the local node
func (ns *NameService) MyDomains() []*Registration {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	
	var result []*Registration
	for _, name := range ns.byNodeID[ns.localID] {
		if reg, ok := ns.names[name]; ok {
			result = append(result, reg)
		}
	}
	return result
}

// Search searches for domains by prefix or keyword
func (ns *NameService) Search(query string) []*Registration {
	query = strings.ToLower(query)
	
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	
	var results []*Registration
	now := time.Now()
	
	for _, reg := range ns.names {
		if now.After(reg.ExpiresAt) {
			continue
		}
		if strings.Contains(reg.Name, query) || 
		   strings.Contains(reg.Description, query) {
			results = append(results, reg)
		}
	}
	
	return results
}

// ListAll returns all active registrations
func (ns *NameService) ListAll() []*Registration {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	
	var results []*Registration
	now := time.Now()
	
	for _, reg := range ns.names {
		if now.Before(reg.ExpiresAt) {
			results = append(results, reg)
		}
	}
	
	return results
}

// AddRegistration adds a registration received from the network
func (ns *NameService) AddRegistration(reg *Registration) error {
	if err := validateName(reg.Name); err != nil {
		return err
	}
	
	if !ns.verifyRegistration(reg) {
		return errors.New("invalid signature")
	}
	
	ns.mu.Lock()
	defer ns.mu.Unlock()
	
	// Check if we have a newer version
	if existing, ok := ns.names[reg.Name]; ok {
		if existing.UpdatedAt.After(reg.UpdatedAt) {
			return nil // Keep newer version
		}
	}
	
	ns.names[reg.Name] = reg
	ns.byNodeID[reg.NodeID] = append(ns.byNodeID[reg.NodeID], reg.Name)
	
	return nil
}

// Marshal serializes a registration
func (r *Registration) Marshal() ([]byte, error) {
	return json.Marshal(r)
}

// UnmarshalRegistration deserializes a registration
func UnmarshalRegistration(data []byte) (*Registration, error) {
	var r Registration
	err := json.Unmarshal(data, &r)
	return &r, err
}

// IsKykDomain checks if a string is a .kyk domain
func IsKykDomain(s string) bool {
	return strings.HasSuffix(strings.ToLower(s), DomainSuffix)
}

// Helper functions

func normalizeName(name string) string {
	name = strings.ToLower(name)
	name = strings.TrimSuffix(name, DomainSuffix)
	return strings.TrimSpace(name)
}

func validateName(name string) error {
	if len(name) < MinNameLength || len(name) > MaxNameLength {
		return ErrInvalidName
	}
	if !validNamePattern.MatchString(name) {
		return ErrInvalidName
	}
	// Reserved names
	reserved := []string{"kayaknet", "admin", "root", "system", "help", "support"}
	for _, r := range reserved {
		if name == r {
			return ErrInvalidName
		}
	}
	return nil
}

func (ns *NameService) signRegistration(r *Registration) []byte {
	data, _ := json.Marshal(struct {
		Name      string    `json:"name"`
		NodeID    string    `json:"node_id"`
		Address   string    `json:"address"`
		ExpiresAt time.Time `json:"expires_at"`
	}{r.Name, r.NodeID, r.Address, r.ExpiresAt})
	return ns.signFunc(data)
}

func (ns *NameService) verifyRegistration(r *Registration) bool {
	if len(r.OwnerKey) != ed25519.PublicKeySize {
		return false
	}
	data, _ := json.Marshal(struct {
		Name      string    `json:"name"`
		NodeID    string    `json:"node_id"`
		Address   string    `json:"address"`
		ExpiresAt time.Time `json:"expires_at"`
	}{r.Name, r.NodeID, r.Address, r.ExpiresAt})
	return ed25519.Verify(r.OwnerKey, data, r.Signature)
}

func (ns *NameService) removeFromOwnership(nodeID, name string) {
	names := ns.byNodeID[nodeID]
	for i, n := range names {
		if n == name {
			ns.byNodeID[nodeID] = append(names[:i], names[i+1:]...)
			return
		}
	}
}

// CleanExpired removes expired registrations
func (ns *NameService) CleanExpired() {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	
	now := time.Now()
	for name, reg := range ns.names {
		if now.After(reg.ExpiresAt) {
			delete(ns.names, name)
			ns.removeFromOwnership(reg.NodeID, name)
		}
	}
}

// Stats returns naming statistics
func (ns *NameService) Stats() (total, active int) {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	
	now := time.Now()
	total = len(ns.names)
	for _, reg := range ns.names {
		if now.Before(reg.ExpiresAt) {
			active++
		}
	}
	return
}

// GenerateRandomName generates a random available name
func (ns *NameService) GenerateRandomName() string {
	for {
		b := make([]byte, 4)
		rand.Read(b)
		name := hex.EncodeToString(b)
		
		ns.mu.RLock()
		_, taken := ns.names[name]
		ns.mu.RUnlock()
		
		if !taken {
			return name
		}
	}
}

