// Package market implements a P2P marketplace accessible only through KayakNet
// No web interface, no clearnet - everything stays inside the network
package market

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

var (
	ErrListingNotFound = errors.New("listing not found")
	ErrUnauthorized    = errors.New("unauthorized")
	ErrInvalidListing  = errors.New("invalid listing")
)

// Listing represents a service/product for sale
type Listing struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Category    string    `json:"category"`
	Price       int64     `json:"price"`       // In network credits/tokens
	Currency    string    `json:"currency"`    // "credits", "btc", "xmr", etc.
	SellerID    string    `json:"seller_id"`   // Node ID (anonymous)
	SellerKey   []byte    `json:"seller_key"`  // Public key for verification
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Active      bool      `json:"active"`
	Signature   []byte    `json:"signature"`   // Seller's signature
	
	// Stats
	Views       int64     `json:"views"`
	Purchases   int64     `json:"purchases"`
	Rating      float64   `json:"rating"`
	ReviewCount int64     `json:"review_count"`
}

// Review is a buyer's review of a listing
type Review struct {
	ID         string    `json:"id"`
	ListingID  string    `json:"listing_id"`
	BuyerID    string    `json:"buyer_id"`
	BuyerKey   []byte    `json:"buyer_key"`
	Rating     int       `json:"rating"`     // 1-5
	Comment    string    `json:"comment"`
	CreatedAt  time.Time `json:"created_at"`
	Signature  []byte    `json:"signature"`
}

// PurchaseRequest is a request to buy a listing
type PurchaseRequest struct {
	ID         string    `json:"id"`
	ListingID  string    `json:"listing_id"`
	BuyerID    string    `json:"buyer_id"`
	BuyerKey   []byte    `json:"buyer_key"`
	Message    string    `json:"message"`    // Optional message to seller
	CreatedAt  time.Time `json:"created_at"`
	Signature  []byte    `json:"signature"`
}

// Marketplace manages listings and transactions
type Marketplace struct {
	mu          sync.RWMutex
	listings    map[string]*Listing
	reviews     map[string][]*Review  // listingID -> reviews
	requests    map[string][]*PurchaseRequest // sellerID -> requests
	myListings  map[string]bool       // listings I created
	localID     string
	localKey    ed25519.PublicKey
	localPriv   ed25519.PrivateKey
	signFunc    func([]byte) []byte
}

// NewMarketplace creates a new marketplace
func NewMarketplace(localID string, pubKey ed25519.PublicKey, privKey ed25519.PrivateKey, signFunc func([]byte) []byte) *Marketplace {
	return &Marketplace{
		listings:   make(map[string]*Listing),
		reviews:    make(map[string][]*Review),
		requests:   make(map[string][]*PurchaseRequest),
		myListings: make(map[string]bool),
		localID:    localID,
		localKey:   pubKey,
		localPriv:  privKey,
		signFunc:   signFunc,
	}
}

// CreateListing creates a new listing
func (m *Marketplace) CreateListing(title, description, category string, price int64, currency string, ttl time.Duration) (*Listing, error) {
	if title == "" || price < 0 {
		return nil, ErrInvalidListing
	}

	listing := &Listing{
		ID:          generateID(),
		Title:       title,
		Description: description,
		Category:    category,
		Price:       price,
		Currency:    currency,
		SellerID:    m.localID,
		SellerKey:   m.localKey,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(ttl),
		Active:      true,
	}

	// Sign the listing
	listing.Signature = m.signListing(listing)

	m.mu.Lock()
	m.listings[listing.ID] = listing
	m.myListings[listing.ID] = true
	m.mu.Unlock()

	return listing, nil
}

// GetListing returns a listing by ID
func (m *Marketplace) GetListing(id string) (*Listing, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	listing, ok := m.listings[id]
	if !ok {
		return nil, ErrListingNotFound
	}
	return listing, nil
}

// Browse returns all active listings, optionally filtered by category
func (m *Marketplace) Browse(category string) []*Listing {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []*Listing
	now := time.Now()

	for _, listing := range m.listings {
		if !listing.Active || now.After(listing.ExpiresAt) {
			continue
		}
		if category != "" && listing.Category != category {
			continue
		}
		results = append(results, listing)
	}

	return results
}

// Search searches listings by keyword
func (m *Marketplace) Search(query string) []*Listing {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []*Listing
	now := time.Now()

	for _, listing := range m.listings {
		if !listing.Active || now.After(listing.ExpiresAt) {
			continue
		}
		// Simple search - check title and description
		if contains(listing.Title, query) || contains(listing.Description, query) {
			results = append(results, listing)
		}
	}

	return results
}

// AddListing adds a listing received from the network
func (m *Marketplace) AddListing(listing *Listing) error {
	// Verify signature
	if !m.verifyListing(listing) {
		return ErrUnauthorized
	}

	m.mu.Lock()
	m.listings[listing.ID] = listing
	m.mu.Unlock()

	return nil
}

// RemoveListing removes a listing (only owner can do this)
func (m *Marketplace) RemoveListing(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.myListings[id] {
		return ErrUnauthorized
	}

	delete(m.listings, id)
	delete(m.myListings, id)
	return nil
}

// CreatePurchaseRequest creates a request to buy
func (m *Marketplace) CreatePurchaseRequest(listingID, message string) (*PurchaseRequest, error) {
	m.mu.RLock()
	listing, ok := m.listings[listingID]
	m.mu.RUnlock()

	if !ok {
		return nil, ErrListingNotFound
	}

	req := &PurchaseRequest{
		ID:        generateID(),
		ListingID: listingID,
		BuyerID:   m.localID,
		BuyerKey:  m.localKey,
		Message:   message,
		CreatedAt: time.Now(),
	}

	// Sign the request
	data, _ := json.Marshal(struct {
		ID        string `json:"id"`
		ListingID string `json:"listing_id"`
		BuyerID   string `json:"buyer_id"`
		Message   string `json:"message"`
	}{req.ID, req.ListingID, req.BuyerID, req.Message})
	req.Signature = m.signFunc(data)

	// Store under seller's ID
	m.mu.Lock()
	m.requests[listing.SellerID] = append(m.requests[listing.SellerID], req)
	m.mu.Unlock()

	return req, nil
}

// GetMyRequests returns purchase requests for my listings
func (m *Marketplace) GetMyRequests() []*PurchaseRequest {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.requests[m.localID]
}

// AddReview adds a review
func (m *Marketplace) AddReview(listingID string, rating int, comment string) (*Review, error) {
	if rating < 1 || rating > 5 {
		return nil, errors.New("rating must be 1-5")
	}

	review := &Review{
		ID:        generateID(),
		ListingID: listingID,
		BuyerID:   m.localID,
		BuyerKey:  m.localKey,
		Rating:    rating,
		Comment:   comment,
		CreatedAt: time.Now(),
	}

	// Sign
	data, _ := json.Marshal(struct {
		ListingID string `json:"listing_id"`
		Rating    int    `json:"rating"`
		Comment   string `json:"comment"`
	}{listingID, rating, comment})
	review.Signature = m.signFunc(data)

	m.mu.Lock()
	m.reviews[listingID] = append(m.reviews[listingID], review)
	
	// Update listing rating
	if listing, ok := m.listings[listingID]; ok {
		total := listing.Rating * float64(listing.ReviewCount)
		listing.ReviewCount++
		listing.Rating = (total + float64(rating)) / float64(listing.ReviewCount)
	}
	m.mu.Unlock()

	return review, nil
}

// GetReviews returns reviews for a listing
func (m *Marketplace) GetReviews(listingID string) []*Review {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.reviews[listingID]
}

// GetMyListings returns listings I created
func (m *Marketplace) GetMyListings() []*Listing {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []*Listing
	for id := range m.myListings {
		if listing, ok := m.listings[id]; ok {
			results = append(results, listing)
		}
	}
	return results
}

// GetCategories returns all categories
func (m *Marketplace) GetCategories() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cats := make(map[string]bool)
	for _, listing := range m.listings {
		if listing.Category != "" {
			cats[listing.Category] = true
		}
	}

	var result []string
	for c := range cats {
		result = append(result, c)
	}
	return result
}

// Marshal serializes a listing for network transmission
func (l *Listing) Marshal() ([]byte, error) {
	return json.Marshal(l)
}

// UnmarshalListing deserializes a listing
func UnmarshalListing(data []byte) (*Listing, error) {
	var l Listing
	err := json.Unmarshal(data, &l)
	return &l, err
}

// Helper functions

func (m *Marketplace) signListing(l *Listing) []byte {
	data, _ := json.Marshal(struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Price       int64  `json:"price"`
		SellerID    string `json:"seller_id"`
	}{l.ID, l.Title, l.Description, l.Price, l.SellerID})
	return m.signFunc(data)
}

func (m *Marketplace) verifyListing(l *Listing) bool {
	if len(l.SellerKey) != ed25519.PublicKeySize {
		return false
	}
	data, _ := json.Marshal(struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Price       int64  `json:"price"`
		SellerID    string `json:"seller_id"`
	}{l.ID, l.Title, l.Description, l.Price, l.SellerID})
	return ed25519.Verify(l.SellerKey, data, l.Signature)
}

func generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && searchString(s, substr)))
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// CleanExpired removes expired listings
func (m *Marketplace) CleanExpired() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for id, listing := range m.listings {
		if now.After(listing.ExpiresAt) {
			delete(m.listings, id)
			delete(m.myListings, id)
		}
	}
}

// Stats returns marketplace statistics
func (m *Marketplace) Stats() (listings, categories int) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cats := make(map[string]bool)
	for _, l := range m.listings {
		if l.Active {
			listings++
			cats[l.Category] = true
		}
	}
	return listings, len(cats)
}

