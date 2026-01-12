// Package escrow manages cryptocurrency escrow for marketplace transactions
package escrow

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

var (
	ErrEscrowNotFound     = errors.New("escrow not found")
	ErrInvalidState       = errors.New("invalid escrow state")
	ErrAlreadyFunded      = errors.New("escrow already funded")
	ErrNotFunded          = errors.New("escrow not funded")
	ErrDisputeActive      = errors.New("dispute is active")
)

// EscrowState represents the state of an escrow
type EscrowState string

const (
	StateCreated   EscrowState = "created"    // Escrow created, waiting for payment
	StateFunded    EscrowState = "funded"     // Payment received and confirmed
	StateShipped   EscrowState = "shipped"    // Seller marked as shipped
	StateCompleted EscrowState = "completed"  // Buyer confirmed, funds released
	StateDisputed  EscrowState = "disputed"   // Dispute opened
	StateRefunded  EscrowState = "refunded"   // Funds returned to buyer
	StateCancelled EscrowState = "cancelled"  // Escrow cancelled before funding
	StateExpired   EscrowState = "expired"    // Payment window expired
)

// Escrow represents an escrow transaction
type Escrow struct {
	ID              string      `json:"id"`
	OrderID         string      `json:"order_id"`
	ListingID       string      `json:"listing_id"`
	ListingTitle    string      `json:"listing_title"`
	
	// Participants
	BuyerID         string      `json:"buyer_id"`
	BuyerAddress    string      `json:"buyer_address"`    // Crypto refund address
	SellerID        string      `json:"seller_id"`
	SellerAddress   string      `json:"seller_address"`   // Crypto payout address
	
	// Payment details
	Currency        CryptoType  `json:"currency"`
	Amount          float64     `json:"amount"`           // Amount in crypto
	AmountAtomic    uint64      `json:"amount_atomic"`
	EscrowAddress   string      `json:"escrow_address"`   // Where buyer pays
	PaymentID       string      `json:"payment_id"`       // Tracked payment ID
	TxID            string      `json:"tx_id"`            // Blockchain transaction ID
	
	// Fee (percentage)
	FeePercent      float64     `json:"fee_percent"`
	FeeAmount       float64     `json:"fee_amount"`
	
	// State
	State           EscrowState `json:"state"`
	
	// Timestamps
	CreatedAt       time.Time   `json:"created_at"`
	FundedAt        time.Time   `json:"funded_at"`
	ShippedAt       time.Time   `json:"shipped_at"`
	CompletedAt     time.Time   `json:"completed_at"`
	ExpiresAt       time.Time   `json:"expires_at"`
	
	// Auto-release
	AutoReleaseAt   time.Time   `json:"auto_release_at"`  // Auto-release if buyer doesn't confirm
	AutoReleaseEnabled bool     `json:"auto_release_enabled"`
	
	// Dispute
	DisputeID       string      `json:"dispute_id,omitempty"`
	DisputeReason   string      `json:"dispute_reason,omitempty"`
	
	// Encrypted delivery info
	DeliveryInfo    string      `json:"delivery_info"`    // Encrypted
	TrackingInfo    string      `json:"tracking_info"`
	
	// Messages between parties
	Messages        []EscrowMessage `json:"messages"`
}

// EscrowMessage is a message within an escrow
type EscrowMessage struct {
	ID        string    `json:"id"`
	From      string    `json:"from"`      // buyer or seller
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// EscrowManager manages all escrows
type EscrowManager struct {
	mu           sync.RWMutex
	escrows      map[string]*Escrow       // escrowID -> escrow
	orderEscrows map[string]string        // orderID -> escrowID
	wallet       *CryptoWallet
	feePercent   float64                  // Platform fee percentage
	
	// Callbacks
	onStateChange func(*Escrow, EscrowState)
}

// NewEscrowManager creates a new escrow manager
func NewEscrowManager(wallet *CryptoWallet, feePercent float64) *EscrowManager {
	return &EscrowManager{
		escrows:      make(map[string]*Escrow),
		orderEscrows: make(map[string]string),
		wallet:       wallet,
		feePercent:   feePercent,
	}
}

// SetStateChangeCallback sets callback for state changes
func (m *EscrowManager) SetStateChangeCallback(cb func(*Escrow, EscrowState)) {
	m.onStateChange = cb
}

// CreateEscrow creates a new escrow for an order
func (m *EscrowManager) CreateEscrow(params EscrowParams) (*Escrow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	// Check if escrow already exists for this order
	if _, ok := m.orderEscrows[params.OrderID]; ok {
		return nil, errors.New("escrow already exists for this order")
	}
	
	// Generate escrow address
	addr, err := m.wallet.GenerateEscrowAddress(params.OrderID, params.Currency)
	if err != nil {
		return nil, err
	}
	
	// Calculate fee
	feeAmount := params.Amount * (m.feePercent / 100)
	
	escrowID := generateEscrowID()
	
	escrow := &Escrow{
		ID:            escrowID,
		OrderID:       params.OrderID,
		ListingID:     params.ListingID,
		ListingTitle:  params.ListingTitle,
		BuyerID:       params.BuyerID,
		BuyerAddress:  params.BuyerAddress,
		SellerID:      params.SellerID,
		SellerAddress: params.SellerAddress,
		Currency:      params.Currency,
		Amount:        params.Amount,
		AmountAtomic:  ConvertToAtomic(params.Currency, params.Amount),
		EscrowAddress: addr.Address,
		FeePercent:    m.feePercent,
		FeeAmount:     feeAmount,
		State:         StateCreated,
		CreatedAt:     time.Now(),
		ExpiresAt:     time.Now().Add(24 * time.Hour), // 24 hour payment window
		DeliveryInfo:  params.DeliveryInfo,
		Messages:      []EscrowMessage{},
	}
	
	m.escrows[escrowID] = escrow
	m.orderEscrows[params.OrderID] = escrowID
	
	return escrow, nil
}

// EscrowParams are parameters for creating an escrow
type EscrowParams struct {
	OrderID       string
	ListingID     string
	ListingTitle  string
	BuyerID       string
	BuyerAddress  string     // Buyer's refund address
	SellerID      string
	SellerAddress string     // Seller's payout address
	Currency      CryptoType
	Amount        float64
	DeliveryInfo  string     // Encrypted delivery information
}

// GetEscrow returns an escrow by ID
func (m *EscrowManager) GetEscrow(escrowID string) (*Escrow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	escrow, ok := m.escrows[escrowID]
	if !ok {
		return nil, ErrEscrowNotFound
	}
	return escrow, nil
}

// GetEscrowByOrder returns escrow for an order
func (m *EscrowManager) GetEscrowByOrder(orderID string) (*Escrow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	escrowID, ok := m.orderEscrows[orderID]
	if !ok {
		return nil, ErrEscrowNotFound
	}
	
	escrow, ok := m.escrows[escrowID]
	if !ok {
		return nil, ErrEscrowNotFound
	}
	return escrow, nil
}

// CheckPayment checks if payment has been received for an escrow
func (m *EscrowManager) CheckPayment(escrowID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	escrow, ok := m.escrows[escrowID]
	if !ok {
		return false, ErrEscrowNotFound
	}
	
	if escrow.State != StateCreated {
		return escrow.State == StateFunded || escrow.State == StateShipped, nil
	}
	
	// Check for payment
	payment, err := m.wallet.CheckPayment(escrow.OrderID, escrow.Amount)
	if err != nil {
		return false, nil // No payment yet
	}
	
	if payment.Confirmations >= getRequiredConfirmations(escrow.Currency) {
		escrow.State = StateFunded
		escrow.FundedAt = time.Now()
		escrow.PaymentID = payment.ID
		escrow.TxID = payment.TxID
		
		// Set auto-release timer (14 days after funding)
		escrow.AutoReleaseAt = time.Now().Add(14 * 24 * time.Hour)
		escrow.AutoReleaseEnabled = true
		
		if m.onStateChange != nil {
			m.onStateChange(escrow, StateFunded)
		}
		
		return true, nil
	}
	
	return false, nil
}

// MarkShipped seller marks order as shipped
func (m *EscrowManager) MarkShipped(escrowID, trackingInfo string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	escrow, ok := m.escrows[escrowID]
	if !ok {
		return ErrEscrowNotFound
	}
	
	if escrow.State != StateFunded {
		return ErrInvalidState
	}
	
	escrow.State = StateShipped
	escrow.ShippedAt = time.Now()
	escrow.TrackingInfo = trackingInfo
	
	if m.onStateChange != nil {
		m.onStateChange(escrow, StateShipped)
	}
	
	return nil
}

// Release buyer confirms receipt, releases funds to seller
func (m *EscrowManager) Release(escrowID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	escrow, ok := m.escrows[escrowID]
	if !ok {
		return ErrEscrowNotFound
	}
	
	if escrow.State == StateDisputed {
		return ErrDisputeActive
	}
	
	if escrow.State != StateFunded && escrow.State != StateShipped {
		return ErrInvalidState
	}
	
	// Release payment to seller
	if escrow.PaymentID != "" {
		if err := m.wallet.ReleasePayment(escrow.PaymentID, escrow.SellerAddress); err != nil {
			return err
		}
	}
	
	escrow.State = StateCompleted
	escrow.CompletedAt = time.Now()
	escrow.AutoReleaseEnabled = false
	
	if m.onStateChange != nil {
		m.onStateChange(escrow, StateCompleted)
	}
	
	return nil
}

// Refund returns funds to buyer
func (m *EscrowManager) Refund(escrowID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	escrow, ok := m.escrows[escrowID]
	if !ok {
		return ErrEscrowNotFound
	}
	
	if escrow.State != StateFunded && escrow.State != StateShipped && escrow.State != StateDisputed {
		return ErrInvalidState
	}
	
	// Refund payment to buyer
	if escrow.PaymentID != "" {
		if err := m.wallet.RefundPayment(escrow.PaymentID, escrow.BuyerAddress); err != nil {
			return err
		}
	}
	
	escrow.State = StateRefunded
	escrow.CompletedAt = time.Now()
	escrow.AutoReleaseEnabled = false
	
	if m.onStateChange != nil {
		m.onStateChange(escrow, StateRefunded)
	}
	
	return nil
}

// OpenDispute opens a dispute on an escrow
func (m *EscrowManager) OpenDispute(escrowID, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	escrow, ok := m.escrows[escrowID]
	if !ok {
		return ErrEscrowNotFound
	}
	
	if escrow.State != StateFunded && escrow.State != StateShipped {
		return ErrInvalidState
	}
	
	escrow.State = StateDisputed
	escrow.DisputeID = generateEscrowID()
	escrow.DisputeReason = reason
	escrow.AutoReleaseEnabled = false
	
	if m.onStateChange != nil {
		m.onStateChange(escrow, StateDisputed)
	}
	
	return nil
}

// ResolveDispute resolves a dispute
func (m *EscrowManager) ResolveDispute(escrowID string, releaseToSeller bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	escrow, ok := m.escrows[escrowID]
	if !ok {
		return ErrEscrowNotFound
	}
	
	if escrow.State != StateDisputed {
		return ErrInvalidState
	}
	
	if releaseToSeller {
		escrow.State = StateCompleted
		if escrow.PaymentID != "" {
			m.wallet.ReleasePayment(escrow.PaymentID, escrow.SellerAddress)
		}
	} else {
		escrow.State = StateRefunded
		if escrow.PaymentID != "" {
			m.wallet.RefundPayment(escrow.PaymentID, escrow.BuyerAddress)
		}
	}
	
	escrow.CompletedAt = time.Now()
	
	if m.onStateChange != nil {
		m.onStateChange(escrow, escrow.State)
	}
	
	return nil
}

// Cancel cancels an unfunded escrow
func (m *EscrowManager) Cancel(escrowID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	escrow, ok := m.escrows[escrowID]
	if !ok {
		return ErrEscrowNotFound
	}
	
	if escrow.State != StateCreated {
		return ErrInvalidState
	}
	
	escrow.State = StateCancelled
	
	if m.onStateChange != nil {
		m.onStateChange(escrow, StateCancelled)
	}
	
	return nil
}

// AddMessage adds a message to the escrow
func (m *EscrowManager) AddMessage(escrowID, from, content string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	escrow, ok := m.escrows[escrowID]
	if !ok {
		return ErrEscrowNotFound
	}
	
	msg := EscrowMessage{
		ID:        generateEscrowID()[:16],
		From:      from,
		Content:   content,
		CreatedAt: time.Now(),
	}
	
	escrow.Messages = append(escrow.Messages, msg)
	return nil
}

// GetBuyerEscrows returns all escrows for a buyer
func (m *EscrowManager) GetBuyerEscrows(buyerID string) []*Escrow {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	var escrows []*Escrow
	for _, e := range m.escrows {
		if e.BuyerID == buyerID {
			escrows = append(escrows, e)
		}
	}
	return escrows
}

// GetSellerEscrows returns all escrows for a seller
func (m *EscrowManager) GetSellerEscrows(sellerID string) []*Escrow {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	var escrows []*Escrow
	for _, e := range m.escrows {
		if e.SellerID == sellerID {
			escrows = append(escrows, e)
		}
	}
	return escrows
}

// ProcessAutoReleases checks and processes auto-releases
func (m *EscrowManager) ProcessAutoReleases() {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	now := time.Now()
	for _, escrow := range m.escrows {
		if escrow.AutoReleaseEnabled && now.After(escrow.AutoReleaseAt) {
			if escrow.State == StateFunded || escrow.State == StateShipped {
				// Auto-release to seller
				if escrow.PaymentID != "" {
					m.wallet.ReleasePayment(escrow.PaymentID, escrow.SellerAddress)
				}
				escrow.State = StateCompleted
				escrow.CompletedAt = now
				escrow.AutoReleaseEnabled = false
				
				if m.onStateChange != nil {
					m.onStateChange(escrow, StateCompleted)
				}
			}
		}
	}
}

// ProcessExpiredEscrows cancels expired unfunded escrows
func (m *EscrowManager) ProcessExpiredEscrows() {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	now := time.Now()
	for _, escrow := range m.escrows {
		if escrow.State == StateCreated && now.After(escrow.ExpiresAt) {
			escrow.State = StateExpired
			if m.onStateChange != nil {
				m.onStateChange(escrow, StateExpired)
			}
		}
	}
}

// Stats returns escrow statistics
func (m *EscrowManager) Stats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	stats := map[string]interface{}{
		"total":     len(m.escrows),
		"created":   0,
		"funded":    0,
		"shipped":   0,
		"completed": 0,
		"disputed":  0,
		"refunded":  0,
	}
	
	var totalValue float64
	for _, e := range m.escrows {
		switch e.State {
		case StateCreated:
			stats["created"] = stats["created"].(int) + 1
		case StateFunded:
			stats["funded"] = stats["funded"].(int) + 1
			totalValue += e.Amount
		case StateShipped:
			stats["shipped"] = stats["shipped"].(int) + 1
			totalValue += e.Amount
		case StateCompleted:
			stats["completed"] = stats["completed"].(int) + 1
		case StateDisputed:
			stats["disputed"] = stats["disputed"].(int) + 1
			totalValue += e.Amount
		case StateRefunded:
			stats["refunded"] = stats["refunded"].(int) + 1
		}
	}
	stats["held_value"] = totalValue
	
	return stats
}

// Marshal serializes an escrow
func (e *Escrow) Marshal() ([]byte, error) {
	return json.Marshal(e)
}

// UnmarshalEscrow deserializes an escrow
func UnmarshalEscrow(data []byte) (*Escrow, error) {
	var e Escrow
	err := json.Unmarshal(data, &e)
	return &e, err
}

func generateEscrowID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func getRequiredConfirmations(currency CryptoType) int {
	switch currency {
	case CryptoXMR:
		return 10 // Monero recommended
	case CryptoZEC:
		return 6  // Zcash standard
	}
	return 6
}

