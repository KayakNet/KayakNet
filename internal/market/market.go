// Package market implements a P2P marketplace accessible only through KayakNet
// No web interface, no clearnet - everything stays inside the network
package market

import (
	"crypto/ed25519"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrListingNotFound  = errors.New("listing not found")
	ErrUnauthorized     = errors.New("unauthorized")
	ErrInvalidListing   = errors.New("invalid listing")
	ErrOrderNotFound    = errors.New("order not found")
	ErrInvalidStatus    = errors.New("invalid order status")
	ErrDisputeExists    = errors.New("dispute already exists")
	ErrInsufficientFunds = errors.New("insufficient funds")
)

// Order status constants
const (
	OrderStatusPending    = "pending"     // Buyer placed order, waiting for seller
	OrderStatusAccepted   = "accepted"    // Seller accepted, waiting for payment
	OrderStatusPaid       = "paid"        // Payment in escrow
	OrderStatusShipped    = "shipped"     // Seller marked as shipped/delivered
	OrderStatusCompleted  = "completed"   // Buyer confirmed, funds released
	OrderStatusDisputed   = "disputed"    // Dispute opened
	OrderStatusRefunded   = "refunded"    // Refunded to buyer
	OrderStatusCancelled  = "cancelled"   // Cancelled
)

// Listing represents a service/product for sale
type Listing struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Category    string    `json:"category"`
	Price       float64   `json:"price"`       // Price in cryptocurrency
	Currency    string    `json:"currency"`    // "XMR" (Monero) or "ZEC" (Zcash)
	Image       string    `json:"image"`       // Image URL or data URI
	Images      []string  `json:"images"`      // Additional images
	SellerID    string    `json:"seller_id"`   // Node ID (anonymous)
	SellerName  string    `json:"seller_name"` // Pseudonym/handle
	SellerKey   []byte    `json:"seller_key"`  // Public key for verification
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Active      bool      `json:"active"`
	Signature   []byte    `json:"signature"`   // Seller's signature
	
	// Product details
	Stock       int       `json:"stock"`       // -1 for unlimited
	ShipsFrom   string    `json:"ships_from"`  // Location hint
	ShipsTo     []string  `json:"ships_to"`    // Regions shipped to
	DeliveryDays int      `json:"delivery_days"` // Estimated delivery
	
	// Stats
	Views       int64     `json:"views"`
	Purchases   int64     `json:"purchases"`
	Rating      float64   `json:"rating"`
	ReviewCount int64     `json:"review_count"`
	Favorites   int64     `json:"favorites"`
}

// Review is a buyer's review of a listing/seller
type Review struct {
	ID          string    `json:"id"`
	ListingID   string    `json:"listing_id"`
	OrderID     string    `json:"order_id"`
	SellerID    string    `json:"seller_id"`
	BuyerID     string    `json:"buyer_id"`
	BuyerName   string    `json:"buyer_name"`
	BuyerKey    []byte    `json:"buyer_key"`
	Rating      int       `json:"rating"`      // 1-5
	Comment     string    `json:"comment"`
	Quality     int       `json:"quality"`     // 1-5 product quality
	Shipping    int       `json:"shipping"`    // 1-5 shipping speed
	Communication int     `json:"communication"` // 1-5 seller communication
	CreatedAt   time.Time `json:"created_at"`
	Signature   []byte    `json:"signature"`
	Verified    bool      `json:"verified"`    // From verified purchase
	SellerReply string    `json:"seller_reply"` // Seller's response
	ReplyAt     time.Time `json:"reply_at"`
}

// Order represents a purchase transaction
type Order struct {
	ID          string    `json:"id"`
	ListingID   string    `json:"listing_id"`
	ListingTitle string   `json:"listing_title"`
	SellerID    string    `json:"seller_id"`
	SellerName  string    `json:"seller_name"`
	BuyerID     string    `json:"buyer_id"`
	BuyerName   string    `json:"buyer_name"`
	BuyerKey    []byte    `json:"buyer_key"`
	Quantity    int       `json:"quantity"`
	Price       float64   `json:"price"`
	Total       float64   `json:"total"`
	Currency    string    `json:"currency"`
	Status      string    `json:"status"`
	Message     string    `json:"message"`      // Buyer's message
	Address     string    `json:"address"`      // Encrypted delivery address
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	PaidAt      time.Time `json:"paid_at"`
	ShippedAt   time.Time `json:"shipped_at"`
	CompletedAt time.Time `json:"completed_at"`
	Signature   []byte    `json:"signature"`
	
	// Escrow
	EscrowID    string    `json:"escrow_id"`
	EscrowAmount float64  `json:"escrow_amount"`
	
	// Tracking
	TrackingInfo string   `json:"tracking_info"` // Encrypted tracking
}

// Message represents a private message between buyer and seller
type Message struct {
	ID          string    `json:"id"`
	OrderID     string    `json:"order_id"`     // Associated order (optional)
	ListingID   string    `json:"listing_id"`   // Associated listing (optional)
	FromID      string    `json:"from_id"`
	FromName    string    `json:"from_name"`
	ToID        string    `json:"to_id"`
	ToName      string    `json:"to_name"`
	Content     string    `json:"content"`      // Encrypted content
	Read        bool      `json:"read"`
	CreatedAt   time.Time `json:"created_at"`
	Signature   []byte    `json:"signature"`
}

// Conversation groups messages between two parties
type Conversation struct {
	ID          string     `json:"id"`
	Participant1 string    `json:"participant1"`
	Participant2 string    `json:"participant2"`
	ListingID   string     `json:"listing_id"`
	OrderID     string     `json:"order_id"`
	Messages    []*Message `json:"messages"`
	UpdatedAt   time.Time  `json:"updated_at"`
	Unread      int        `json:"unread"`
}

// Dispute represents a conflict between buyer and seller
type Dispute struct {
	ID          string    `json:"id"`
	OrderID     string    `json:"order_id"`
	OpenedBy    string    `json:"opened_by"`    // buyer or seller ID
	Reason      string    `json:"reason"`
	Description string    `json:"description"`
	Evidence    []string  `json:"evidence"`     // Links to evidence
	Status      string    `json:"status"`       // open, resolved, escalated
	Resolution  string    `json:"resolution"`   // refund, release, split
	CreatedAt   time.Time `json:"created_at"`
	ResolvedAt  time.Time `json:"resolved_at"`
}

// SellerProfile represents a seller's public profile
type SellerProfile struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Bio           string    `json:"bio"`
	Avatar        string    `json:"avatar"`
	JoinedAt      time.Time `json:"joined_at"`
	LastSeen      time.Time `json:"last_seen"`
	Verified      bool      `json:"verified"`
	TrustedSeller bool      `json:"trusted_seller"`
	
	// Stats
	TotalSales    float64   `json:"total_sales"`
	TotalOrders   int64     `json:"total_orders"`
	Rating        float64   `json:"rating"`
	ReviewCount   int64     `json:"review_count"`
	ResponseTime  string    `json:"response_time"`  // avg response time
	ShipTime      string    `json:"ship_time"`      // avg ship time
	DisputeRate   float64   `json:"dispute_rate"`
	
	// Badges
	Badges        []string  `json:"badges"`
}

// Escrow holds funds during a transaction
type Escrow struct {
	ID          string    `json:"id"`
	OrderID     string    `json:"order_id"`
	Amount      float64   `json:"amount"`
	Currency    string    `json:"currency"`
	Status      string    `json:"status"`       // held, released, refunded
	CreatedAt   time.Time `json:"created_at"`
	ReleasedAt  time.Time `json:"released_at"`
}

// Favorite tracks user's saved listings
type Favorite struct {
	UserID    string    `json:"user_id"`
	ListingID string    `json:"listing_id"`
	AddedAt   time.Time `json:"added_at"`
}

// PurchaseRequest is a request to buy a listing (legacy, now use Order)
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
	mu            sync.RWMutex
	listings      map[string]*Listing
	reviews       map[string][]*Review       // listingID -> reviews
	sellerReviews map[string][]*Review       // sellerID -> reviews
	requests      map[string][]*PurchaseRequest // sellerID -> requests
	orders        map[string]*Order          // orderID -> order
	myOrders      map[string]bool            // orders where I'm buyer
	sellerOrders  map[string][]string        // sellerID -> orderIDs
	conversations map[string]*Conversation   // conversationID -> conversation
	myConversations []string                 // my conversation IDs
	disputes      map[string]*Dispute        // disputeID -> dispute
	escrows       map[string]*Escrow         // escrowID -> escrow
	favorites     map[string][]string        // userID -> listingIDs
	profiles      map[string]*SellerProfile  // sellerID -> profile
	myListings    map[string]bool            // listings I created
	localID       string
	localName     string
	localKey      ed25519.PublicKey
	localPriv     ed25519.PrivateKey
	signFunc      func([]byte) []byte
	dataDir       string                     // Directory for persistent storage
}

// NewMarketplace creates a new marketplace
func NewMarketplace(localID string, pubKey ed25519.PublicKey, privKey ed25519.PrivateKey, signFunc func([]byte) []byte) *Marketplace {
	m := &Marketplace{
		listings:        make(map[string]*Listing),
		reviews:         make(map[string][]*Review),
		sellerReviews:   make(map[string][]*Review),
		requests:        make(map[string][]*PurchaseRequest),
		orders:          make(map[string]*Order),
		myOrders:        make(map[string]bool),
		sellerOrders:    make(map[string][]string),
		conversations:   make(map[string]*Conversation),
		myConversations: []string{},
		disputes:        make(map[string]*Dispute),
		escrows:         make(map[string]*Escrow),
		favorites:       make(map[string][]string),
		profiles:        make(map[string]*SellerProfile),
		myListings:      make(map[string]bool),
		localID:         localID,
		localName:       "anonymous",
		localKey:        pubKey,
		localPriv:       privKey,
		signFunc:        signFunc,
	}
	
	// Create own profile
	m.profiles[localID] = &SellerProfile{
		ID:       localID,
		Name:     "anonymous",
		JoinedAt: time.Now(),
		LastSeen: time.Now(),
	}
	
	return m
}

// SetDataDir sets the data directory for persistence
func (m *Marketplace) SetDataDir(dataDir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	m.dataDir = filepath.Join(dataDir, "market")
	if err := os.MkdirAll(m.dataDir, 0700); err != nil {
		return err
	}
	
	// Load existing data
	m.loadData()
	return nil
}

// MarketData holds all marketplace data for persistence
type MarketData struct {
	Listings      map[string]*Listing           `json:"listings"`
	Reviews       map[string][]*Review          `json:"reviews"`
	SellerReviews map[string][]*Review          `json:"seller_reviews"`
	Orders        map[string]*Order             `json:"orders"`
	MyOrders      map[string]bool               `json:"my_orders"`
	SellerOrders  map[string][]string           `json:"seller_orders"`
	Conversations map[string]*Conversation      `json:"conversations"`
	MyConversations []string                    `json:"my_conversations"`
	Disputes      map[string]*Dispute           `json:"disputes"`
	Favorites     map[string][]string           `json:"favorites"`
	Profiles      map[string]*SellerProfile     `json:"profiles"`
	MyListings    map[string]bool               `json:"my_listings"`
}

// Save persists all marketplace data to disk
func (m *Marketplace) Save() error {
	if m.dataDir == "" {
		return nil // No persistence configured
	}
	
	m.mu.RLock()
	data := MarketData{
		Listings:        m.listings,
		Reviews:         m.reviews,
		SellerReviews:   m.sellerReviews,
		Orders:          m.orders,
		MyOrders:        m.myOrders,
		SellerOrders:    m.sellerOrders,
		Conversations:   m.conversations,
		MyConversations: m.myConversations,
		Disputes:        m.disputes,
		Favorites:       m.favorites,
		Profiles:        m.profiles,
		MyListings:      m.myListings,
	}
	m.mu.RUnlock()
	
	path := filepath.Join(m.dataDir, "market.json")
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

// loadData loads marketplace data from disk
func (m *Marketplace) loadData() {
	if m.dataDir == "" {
		return
	}
	
	path := filepath.Join(m.dataDir, "market.json")
	f, err := os.Open(path)
	if err != nil {
		return // File doesn't exist yet
	}
	defer f.Close()
	
	var data MarketData
	if err := json.NewDecoder(f).Decode(&data); err != nil {
		return
	}
	
	// Restore data
	if data.Listings != nil {
		m.listings = data.Listings
	}
	if data.Reviews != nil {
		m.reviews = data.Reviews
	}
	if data.SellerReviews != nil {
		m.sellerReviews = data.SellerReviews
	}
	if data.Orders != nil {
		m.orders = data.Orders
	}
	if data.MyOrders != nil {
		m.myOrders = data.MyOrders
	}
	if data.SellerOrders != nil {
		m.sellerOrders = data.SellerOrders
	}
	if data.Conversations != nil {
		m.conversations = data.Conversations
	}
	if data.MyConversations != nil {
		m.myConversations = data.MyConversations
	}
	if data.Disputes != nil {
		m.disputes = data.Disputes
	}
	if data.Favorites != nil {
		m.favorites = data.Favorites
	}
	if data.Profiles != nil {
		m.profiles = data.Profiles
	}
	if data.MyListings != nil {
		m.myListings = data.MyListings
	}
}

// SetLocalName sets the local user's display name
func (m *Marketplace) SetLocalName(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.localName = name
	if profile, ok := m.profiles[m.localID]; ok {
		profile.Name = name
	}
}

// GetLocalID returns the local user's ID
func (m *Marketplace) GetLocalID() string {
	return m.localID
}

// GetLocalName returns the local user's name
func (m *Marketplace) GetLocalName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.localName
}

// CreateListing creates a new listing
func (m *Marketplace) CreateListing(title, description, category string, price float64, currency string, ttl time.Duration) (*Listing, error) {
	return m.CreateListingFull(title, description, category, price, currency, "", "", ttl)
}

// CreateListingFull creates a new listing with all options
func (m *Marketplace) CreateListingFull(title, description, category string, price float64, currency, image, sellerName string, ttl time.Duration) (*Listing, error) {
	if title == "" || price < 0 {
		return nil, ErrInvalidListing
	}

	if sellerName == "" {
		sellerName = "anonymous"
	}

	listing := &Listing{
		ID:          generateID(),
		Title:       title,
		Description: description,
		Category:    category,
		Price:       price,
		Currency:    currency,
		Image:       image,
		SellerID:    m.localID,
		SellerName:  sellerName,
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

	// Persist to disk
	go m.Save()

	return listing, nil
}

// ListingCount returns the number of listings
func (m *Marketplace) ListingCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.listings)
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

	// Persist to disk
	go m.Save()

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
	
	// Persist to disk
	go m.Save()
	
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
		Title       string  `json:"title"`
		Description string  `json:"description"`
		Price       float64 `json:"price"`
		SellerID    string  `json:"seller_id"`
	}{l.ID, l.Title, l.Description, l.Price, l.SellerID})
	return m.signFunc(data)
}

func (m *Marketplace) verifyListing(l *Listing) bool {
	if len(l.SellerKey) != ed25519.PublicKeySize {
		return false
	}
	data, _ := json.Marshal(struct {
		ID          string  `json:"id"`
		Title       string  `json:"title"`
		Description string  `json:"description"`
		Price       float64 `json:"price"`
		SellerID    string  `json:"seller_id"`
	}{l.ID, l.Title, l.Description, l.Price, l.SellerID})
	return ed25519.Verify(l.SellerKey, data, l.Signature)
}

func generateID() string {
	b := make([]byte, 16)
	crand.Read(b)
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

// =====================
// ORDER MANAGEMENT
// =====================

// CreateOrder creates a new order for a listing
func (m *Marketplace) CreateOrder(listingID string, quantity int, message, address string) (*Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	listing, ok := m.listings[listingID]
	if !ok {
		return nil, ErrListingNotFound
	}

	if listing.Stock != -1 && listing.Stock < quantity {
		return nil, errors.New("insufficient stock")
	}

	order := &Order{
		ID:           generateID(),
		ListingID:    listingID,
		ListingTitle: listing.Title,
		SellerID:     listing.SellerID,
		SellerName:   listing.SellerName,
		BuyerID:      m.localID,
		BuyerName:    m.localName,
		BuyerKey:     m.localKey,
		Quantity:     quantity,
		Price:        listing.Price,
		Total:        listing.Price * float64(quantity),
		Currency:     listing.Currency,
		Status:       OrderStatusPending,
		Message:      message,
		Address:      address,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	// Sign the order
	data, _ := json.Marshal(struct {
		ID        string  `json:"id"`
		ListingID string  `json:"listing_id"`
		BuyerID   string  `json:"buyer_id"`
		Total     float64 `json:"total"`
	}{order.ID, order.ListingID, order.BuyerID, order.Total})
	order.Signature = m.signFunc(data)

	m.orders[order.ID] = order
	m.myOrders[order.ID] = true
	m.sellerOrders[listing.SellerID] = append(m.sellerOrders[listing.SellerID], order.ID)

	// Create conversation for this order
	m.createOrderConversation(order)

	return order, nil
}

// GetOrder returns an order by ID
func (m *Marketplace) GetOrder(orderID string) (*Order, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	order, ok := m.orders[orderID]
	if !ok {
		return nil, ErrOrderNotFound
	}
	return order, nil
}

// GetMyOrders returns orders where I'm the buyer
func (m *Marketplace) GetMyOrders() []*Order {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var orders []*Order
	for id := range m.myOrders {
		if order, ok := m.orders[id]; ok {
			orders = append(orders, order)
		}
	}
	
	// Sort by created time, newest first
	sort.Slice(orders, func(i, j int) bool {
		return orders[i].CreatedAt.After(orders[j].CreatedAt)
	})
	
	return orders
}

// GetSellerOrders returns orders for my listings
func (m *Marketplace) GetSellerOrders() []*Order {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var orders []*Order
	for _, orderID := range m.sellerOrders[m.localID] {
		if order, ok := m.orders[orderID]; ok {
			orders = append(orders, order)
		}
	}
	
	sort.Slice(orders, func(i, j int) bool {
		return orders[i].CreatedAt.After(orders[j].CreatedAt)
	})
	
	return orders
}

// UpdateOrderStatus updates an order's status
func (m *Marketplace) UpdateOrderStatus(orderID, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	order, ok := m.orders[orderID]
	if !ok {
		return ErrOrderNotFound
	}

	// Validate status transition
	validTransitions := map[string][]string{
		OrderStatusPending:   {OrderStatusAccepted, OrderStatusCancelled},
		OrderStatusAccepted:  {OrderStatusPaid, OrderStatusCancelled},
		OrderStatusPaid:      {OrderStatusShipped, OrderStatusDisputed, OrderStatusRefunded},
		OrderStatusShipped:   {OrderStatusCompleted, OrderStatusDisputed},
		OrderStatusDisputed:  {OrderStatusRefunded, OrderStatusCompleted},
	}

	valid := false
	for _, s := range validTransitions[order.Status] {
		if s == status {
			valid = true
			break
		}
	}
	if !valid {
		return ErrInvalidStatus
	}

	order.Status = status
	order.UpdatedAt = time.Now()

	switch status {
	case OrderStatusPaid:
		order.PaidAt = time.Now()
	case OrderStatusShipped:
		order.ShippedAt = time.Now()
	case OrderStatusCompleted:
		order.CompletedAt = time.Now()
		// Update listing stats
		if listing, ok := m.listings[order.ListingID]; ok {
			listing.Purchases++
		}
	}

	return nil
}

// AcceptOrder seller accepts an order
func (m *Marketplace) AcceptOrder(orderID string) error {
	return m.UpdateOrderStatus(orderID, OrderStatusAccepted)
}

// MarkPaid marks order as paid (funds in escrow)
func (m *Marketplace) MarkPaid(orderID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	order, ok := m.orders[orderID]
	if !ok {
		return ErrOrderNotFound
	}

	// Create escrow
	escrow := &Escrow{
		ID:        generateID(),
		OrderID:   orderID,
		Amount:    order.Total,
		Currency:  order.Currency,
		Status:    "held",
		CreatedAt: time.Now(),
	}
	m.escrows[escrow.ID] = escrow
	order.EscrowID = escrow.ID
	order.EscrowAmount = escrow.Amount
	order.Status = OrderStatusPaid
	order.PaidAt = time.Now()
	order.UpdatedAt = time.Now()

	return nil
}

// MarkShipped seller marks order as shipped
func (m *Marketplace) MarkShipped(orderID, trackingInfo string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	order, ok := m.orders[orderID]
	if !ok {
		return ErrOrderNotFound
	}

	order.Status = OrderStatusShipped
	order.TrackingInfo = trackingInfo
	order.ShippedAt = time.Now()
	order.UpdatedAt = time.Now()

	return nil
}

// CompleteOrder buyer confirms receipt, releases escrow
func (m *Marketplace) CompleteOrder(orderID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	order, ok := m.orders[orderID]
	if !ok {
		return ErrOrderNotFound
	}

	// Release escrow
	if escrow, ok := m.escrows[order.EscrowID]; ok {
		escrow.Status = "released"
		escrow.ReleasedAt = time.Now()
	}

	order.Status = OrderStatusCompleted
	order.CompletedAt = time.Now()
	order.UpdatedAt = time.Now()

	// Update listing and seller stats
	if listing, ok := m.listings[order.ListingID]; ok {
		listing.Purchases++
	}
	if profile, ok := m.profiles[order.SellerID]; ok {
		profile.TotalOrders++
		profile.TotalSales += order.Total
	}

	return nil
}

// CancelOrder cancels an order
func (m *Marketplace) CancelOrder(orderID string) error {
	return m.UpdateOrderStatus(orderID, OrderStatusCancelled)
}

// =====================
// MESSAGING SYSTEM
// =====================

func (m *Marketplace) createOrderConversation(order *Order) {
	convID := generateID()
	conv := &Conversation{
		ID:           convID,
		Participant1: order.BuyerID,
		Participant2: order.SellerID,
		OrderID:      order.ID,
		ListingID:    order.ListingID,
		Messages:     []*Message{},
		UpdatedAt:    time.Now(),
	}
	m.conversations[convID] = conv
	m.myConversations = append(m.myConversations, convID)
}

// SendMessage sends a private message
func (m *Marketplace) SendMessage(toID, content string, orderID, listingID string) (*Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Find or create conversation
	var conv *Conversation
	for _, c := range m.conversations {
		if (c.Participant1 == m.localID && c.Participant2 == toID) ||
			(c.Participant1 == toID && c.Participant2 == m.localID) {
			if orderID != "" && c.OrderID == orderID {
				conv = c
				break
			} else if listingID != "" && c.ListingID == listingID {
				conv = c
				break
			}
		}
	}

	if conv == nil {
		convID := generateID()
		conv = &Conversation{
			ID:           convID,
			Participant1: m.localID,
			Participant2: toID,
			OrderID:      orderID,
			ListingID:    listingID,
			Messages:     []*Message{},
			UpdatedAt:    time.Now(),
		}
		m.conversations[convID] = conv
		m.myConversations = append(m.myConversations, convID)
	}

	// Get recipient name
	toName := "anonymous"
	if profile, ok := m.profiles[toID]; ok {
		toName = profile.Name
	}

	msg := &Message{
		ID:        generateID(),
		OrderID:   orderID,
		ListingID: listingID,
		FromID:    m.localID,
		FromName:  m.localName,
		ToID:      toID,
		ToName:    toName,
		Content:   content,
		Read:      false,
		CreatedAt: time.Now(),
	}

	// Sign message
	data, _ := json.Marshal(struct {
		ID      string `json:"id"`
		FromID  string `json:"from_id"`
		ToID    string `json:"to_id"`
		Content string `json:"content"`
	}{msg.ID, msg.FromID, msg.ToID, msg.Content})
	msg.Signature = m.signFunc(data)

	conv.Messages = append(conv.Messages, msg)
	conv.UpdatedAt = time.Now()
	conv.Unread++

	return msg, nil
}

// GetConversations returns all conversations
func (m *Marketplace) GetConversations() []*Conversation {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var convs []*Conversation
	for _, id := range m.myConversations {
		if conv, ok := m.conversations[id]; ok {
			convs = append(convs, conv)
		}
	}

	// Also check conversations where we're a participant
	for _, conv := range m.conversations {
		if conv.Participant1 == m.localID || conv.Participant2 == m.localID {
			found := false
			for _, c := range convs {
				if c.ID == conv.ID {
					found = true
					break
				}
			}
			if !found {
				convs = append(convs, conv)
			}
		}
	}

	sort.Slice(convs, func(i, j int) bool {
		return convs[i].UpdatedAt.After(convs[j].UpdatedAt)
	})

	return convs
}

// GetConversation returns a specific conversation
func (m *Marketplace) GetConversation(convID string) (*Conversation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	conv, ok := m.conversations[convID]
	if !ok {
		return nil, errors.New("conversation not found")
	}
	return conv, nil
}

// GetMessages returns messages for a conversation
func (m *Marketplace) GetMessages(convID string) []*Message {
	m.mu.RLock()
	defer m.mu.RUnlock()

	conv, ok := m.conversations[convID]
	if !ok {
		return nil
	}
	return conv.Messages
}

// MarkMessagesRead marks all messages in a conversation as read
func (m *Marketplace) MarkMessagesRead(convID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	conv, ok := m.conversations[convID]
	if !ok {
		return
	}

	for _, msg := range conv.Messages {
		if msg.ToID == m.localID {
			msg.Read = true
		}
	}
	conv.Unread = 0
}

// GetUnreadCount returns total unread messages
func (m *Marketplace) GetUnreadCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, conv := range m.conversations {
		if conv.Participant1 == m.localID || conv.Participant2 == m.localID {
			for _, msg := range conv.Messages {
				if msg.ToID == m.localID && !msg.Read {
					count++
				}
			}
		}
	}
	return count
}

// =====================
// DISPUTE SYSTEM
// =====================

// OpenDispute opens a dispute for an order
func (m *Marketplace) OpenDispute(orderID, reason, description string) (*Dispute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	order, ok := m.orders[orderID]
	if !ok {
		return nil, ErrOrderNotFound
	}

	// Check if dispute already exists
	for _, d := range m.disputes {
		if d.OrderID == orderID && d.Status == "open" {
			return nil, ErrDisputeExists
		}
	}

	dispute := &Dispute{
		ID:          generateID(),
		OrderID:     orderID,
		OpenedBy:    m.localID,
		Reason:      reason,
		Description: description,
		Evidence:    []string{},
		Status:      "open",
		CreatedAt:   time.Now(),
	}

	m.disputes[dispute.ID] = dispute
	order.Status = OrderStatusDisputed
	order.UpdatedAt = time.Now()

	return dispute, nil
}

// GetDispute returns a dispute by ID
func (m *Marketplace) GetDispute(disputeID string) (*Dispute, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	dispute, ok := m.disputes[disputeID]
	if !ok {
		return nil, errors.New("dispute not found")
	}
	return dispute, nil
}

// AddEvidence adds evidence to a dispute
func (m *Marketplace) AddEvidence(disputeID, evidence string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	dispute, ok := m.disputes[disputeID]
	if !ok {
		return errors.New("dispute not found")
	}

	dispute.Evidence = append(dispute.Evidence, evidence)
	return nil
}

// ResolveDispute resolves a dispute
func (m *Marketplace) ResolveDispute(disputeID, resolution string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	dispute, ok := m.disputes[disputeID]
	if !ok {
		return errors.New("dispute not found")
	}

	dispute.Status = "resolved"
	dispute.Resolution = resolution
	dispute.ResolvedAt = time.Now()

	// Handle resolution
	order, ok := m.orders[dispute.OrderID]
	if ok {
		switch resolution {
		case "refund":
			order.Status = OrderStatusRefunded
			if escrow, ok := m.escrows[order.EscrowID]; ok {
				escrow.Status = "refunded"
			}
		case "release":
			order.Status = OrderStatusCompleted
			if escrow, ok := m.escrows[order.EscrowID]; ok {
				escrow.Status = "released"
			}
		}
		order.UpdatedAt = time.Now()
	}

	return nil
}

// =====================
// REVIEWS (ENHANCED)
// =====================

// AddDetailedReview adds a detailed review with multiple ratings
func (m *Marketplace) AddDetailedReview(orderID string, rating, quality, shipping, communication int, comment string) (*Review, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if rating < 1 || rating > 5 {
		return nil, errors.New("rating must be 1-5")
	}

	order, ok := m.orders[orderID]
	if !ok {
		return nil, ErrOrderNotFound
	}

	// Check if order is completed
	if order.Status != OrderStatusCompleted {
		return nil, errors.New("can only review completed orders")
	}

	review := &Review{
		ID:            generateID(),
		ListingID:     order.ListingID,
		OrderID:       orderID,
		SellerID:      order.SellerID,
		BuyerID:       m.localID,
		BuyerName:     m.localName,
		BuyerKey:      m.localKey,
		Rating:        rating,
		Comment:       comment,
		Quality:       quality,
		Shipping:      shipping,
		Communication: communication,
		CreatedAt:     time.Now(),
		Verified:      true,
	}

	// Sign
	data, _ := json.Marshal(struct {
		OrderID string `json:"order_id"`
		Rating  int    `json:"rating"`
		Comment string `json:"comment"`
	}{orderID, rating, comment})
	review.Signature = m.signFunc(data)

	m.reviews[order.ListingID] = append(m.reviews[order.ListingID], review)
	m.sellerReviews[order.SellerID] = append(m.sellerReviews[order.SellerID], review)

	// Update listing rating
	if listing, ok := m.listings[order.ListingID]; ok {
		total := listing.Rating * float64(listing.ReviewCount)
		listing.ReviewCount++
		listing.Rating = (total + float64(rating)) / float64(listing.ReviewCount)
	}

	// Update seller rating
	if profile, ok := m.profiles[order.SellerID]; ok {
		total := profile.Rating * float64(profile.ReviewCount)
		profile.ReviewCount++
		profile.Rating = (total + float64(rating)) / float64(profile.ReviewCount)
	}

	return review, nil
}

// ReplyToReview seller replies to a review
func (m *Marketplace) ReplyToReview(reviewID, reply string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, reviews := range m.reviews {
		for _, r := range reviews {
			if r.ID == reviewID {
				r.SellerReply = reply
				r.ReplyAt = time.Now()
				return nil
			}
		}
	}
	return errors.New("review not found")
}

// GetSellerReviews returns all reviews for a seller
func (m *Marketplace) GetSellerReviews(sellerID string) []*Review {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sellerReviews[sellerID]
}

// =====================
// SELLER PROFILES
// =====================

// GetProfile returns a seller's profile
func (m *Marketplace) GetProfile(sellerID string) (*SellerProfile, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	profile, ok := m.profiles[sellerID]
	if !ok {
		return nil, errors.New("profile not found")
	}
	return profile, nil
}

// UpdateProfile updates the local user's profile
func (m *Marketplace) UpdateProfile(name, bio, avatar string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	profile, ok := m.profiles[m.localID]
	if !ok {
		profile = &SellerProfile{
			ID:       m.localID,
			JoinedAt: time.Now(),
		}
		m.profiles[m.localID] = profile
	}

	if name != "" {
		profile.Name = name
		m.localName = name
	}
	if bio != "" {
		profile.Bio = bio
	}
	if avatar != "" {
		profile.Avatar = avatar
	}
	profile.LastSeen = time.Now()

	return nil
}

// GetSellerListings returns all listings by a seller
func (m *Marketplace) GetSellerListings(sellerID string) []*Listing {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var listings []*Listing
	for _, l := range m.listings {
		if l.SellerID == sellerID && l.Active {
			listings = append(listings, l)
		}
	}
	return listings
}

// =====================
// FAVORITES
// =====================

// AddFavorite adds a listing to favorites
func (m *Marketplace) AddFavorite(listingID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	listing, ok := m.listings[listingID]
	if !ok {
		return ErrListingNotFound
	}

	// Check if already favorited
	for _, id := range m.favorites[m.localID] {
		if id == listingID {
			return nil
		}
	}

	m.favorites[m.localID] = append(m.favorites[m.localID], listingID)
	listing.Favorites++
	return nil
}

// RemoveFavorite removes a listing from favorites
func (m *Marketplace) RemoveFavorite(listingID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	favs := m.favorites[m.localID]
	for i, id := range favs {
		if id == listingID {
			m.favorites[m.localID] = append(favs[:i], favs[i+1:]...)
			if listing, ok := m.listings[listingID]; ok {
				listing.Favorites--
			}
			return nil
		}
	}
	return nil
}

// GetFavorites returns user's favorite listings
func (m *Marketplace) GetFavorites() []*Listing {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var listings []*Listing
	for _, id := range m.favorites[m.localID] {
		if listing, ok := m.listings[id]; ok {
			listings = append(listings, listing)
		}
	}
	return listings
}

// IsFavorite checks if a listing is favorited
func (m *Marketplace) IsFavorite(listingID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, id := range m.favorites[m.localID] {
		if id == listingID {
			return true
		}
	}
	return false
}

// =====================
// ENHANCED SEARCH
// =====================

// SearchAdvanced performs advanced search with filters
func (m *Marketplace) SearchAdvanced(query, category string, minPrice, maxPrice float64, sortBy string) []*Listing {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []*Listing
	now := time.Now()
	queryLower := strings.ToLower(query)

	for _, listing := range m.listings {
		if !listing.Active || now.After(listing.ExpiresAt) {
			continue
		}

		// Category filter
		if category != "" && listing.Category != category {
			continue
		}

		// Price filter
		if minPrice > 0 && listing.Price < minPrice {
			continue
		}
		if maxPrice > 0 && listing.Price > maxPrice {
			continue
		}

		// Search query
		if query != "" {
			titleMatch := strings.Contains(strings.ToLower(listing.Title), queryLower)
			descMatch := strings.Contains(strings.ToLower(listing.Description), queryLower)
			if !titleMatch && !descMatch {
				continue
			}
		}

		results = append(results, listing)
	}

	// Sort results
	switch sortBy {
	case "price_asc":
		sort.Slice(results, func(i, j int) bool {
			return results[i].Price < results[j].Price
		})
	case "price_desc":
		sort.Slice(results, func(i, j int) bool {
			return results[i].Price > results[j].Price
		})
	case "rating":
		sort.Slice(results, func(i, j int) bool {
			return results[i].Rating > results[j].Rating
		})
	case "newest":
		sort.Slice(results, func(i, j int) bool {
			return results[i].CreatedAt.After(results[j].CreatedAt)
		})
	case "popular":
		sort.Slice(results, func(i, j int) bool {
			return results[i].Purchases > results[j].Purchases
		})
	}

	return results
}

// IncrementViews increments view count for a listing
func (m *Marketplace) IncrementViews(listingID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	if listing, ok := m.listings[listingID]; ok {
		listing.Views++
	}
}

// =====================