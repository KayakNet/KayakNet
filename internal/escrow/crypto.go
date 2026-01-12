// Package escrow implements cryptocurrency escrow for KayakNet marketplace
// Supports Monero (XMR) and Zcash (ZEC) for private transactions
package escrow

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	ErrWalletNotConfigured = errors.New("wallet not configured")
	ErrInsufficientFunds   = errors.New("insufficient funds")
	ErrInvalidAddress      = errors.New("invalid address")
	ErrTransactionFailed   = errors.New("transaction failed")
	ErrPaymentNotFound     = errors.New("payment not found")
	ErrPaymentPending      = errors.New("payment still pending confirmation")
)

// CryptoType represents supported cryptocurrencies
type CryptoType string

const (
	CryptoXMR CryptoType = "XMR" // Monero
	CryptoZEC CryptoType = "ZEC" // Zcash
)

// WalletConfig holds cryptocurrency wallet configuration
type WalletConfig struct {
	// Monero configuration
	MoneroRPCHost     string `json:"monero_rpc_host"`     // e.g., "127.0.0.1:18082"
	MoneroRPCUser     string `json:"monero_rpc_user"`
	MoneroRPCPassword string `json:"monero_rpc_password"`
	
	// Zcash configuration
	ZcashRPCHost     string `json:"zcash_rpc_host"`      // e.g., "127.0.0.1:8232"
	ZcashRPCUser     string `json:"zcash_rpc_user"`
	ZcashRPCPassword string `json:"zcash_rpc_password"`
}

// PaymentAddress represents an escrow payment address
type PaymentAddress struct {
	Currency    CryptoType `json:"currency"`
	Address     string     `json:"address"`
	PaymentID   string     `json:"payment_id,omitempty"`  // For Monero integrated addresses
	ViewKey     string     `json:"view_key,omitempty"`    // For payment verification
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
}

// Payment represents a tracked payment
type Payment struct {
	ID            string     `json:"id"`
	OrderID       string     `json:"order_id"`
	Currency      CryptoType `json:"currency"`
	Address       string     `json:"address"`
	Amount        float64    `json:"amount"`         // Amount in crypto
	AmountAtomic  uint64     `json:"amount_atomic"`  // Amount in smallest unit
	TxID          string     `json:"tx_id"`
	Confirmations int        `json:"confirmations"`
	Status        string     `json:"status"`         // pending, confirmed, released, refunded
	CreatedAt     time.Time  `json:"created_at"`
	ConfirmedAt   time.Time  `json:"confirmed_at"`
	ReleasedAt    time.Time  `json:"released_at"`
}

// CryptoWallet manages cryptocurrency operations
type CryptoWallet struct {
	mu           sync.RWMutex
	config       WalletConfig
	enabled      map[CryptoType]bool
	addresses    map[string]*PaymentAddress // orderID -> address
	payments     map[string]*Payment        // paymentID -> payment
	httpClient   *http.Client
}

// NewCryptoWallet creates a new cryptocurrency wallet manager
func NewCryptoWallet(config WalletConfig) *CryptoWallet {
	w := &CryptoWallet{
		config:     config,
		enabled:    make(map[CryptoType]bool),
		addresses:  make(map[string]*PaymentAddress),
		payments:   make(map[string]*Payment),
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
	
	// Only enable currencies when RPC is properly configured
	if config.MoneroRPCHost != "" {
		w.enabled[CryptoXMR] = true
	}
	if config.ZcashRPCHost != "" {
		w.enabled[CryptoZEC] = true
	}
	
	return w
}

// IsEnabled checks if a cryptocurrency is enabled
func (w *CryptoWallet) IsEnabled(crypto CryptoType) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.enabled[crypto]
}

// GetEnabledCurrencies returns list of enabled cryptocurrencies
func (w *CryptoWallet) GetEnabledCurrencies() []CryptoType {
	w.mu.RLock()
	defer w.mu.RUnlock()
	
	var currencies []CryptoType
	for c, enabled := range w.enabled {
		if enabled {
			currencies = append(currencies, c)
		}
	}
	return currencies
}

// GenerateEscrowAddress creates a new escrow address for an order
func (w *CryptoWallet) GenerateEscrowAddress(orderID string, currency CryptoType) (*PaymentAddress, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	
	if !w.enabled[currency] {
		return nil, ErrWalletNotConfigured
	}
	
	var addr *PaymentAddress
	var err error
	
	switch currency {
	case CryptoXMR:
		addr, err = w.generateMoneroAddress(orderID)
	case CryptoZEC:
		addr, err = w.generateZcashAddress(orderID)
	default:
		return nil, errors.New("unsupported currency")
	}
	
	if err != nil {
		return nil, err
	}
	
	w.addresses[orderID] = addr
	return addr, nil
}

// generateMoneroAddress creates a Monero subaddress for escrow
func (w *CryptoWallet) generateMoneroAddress(orderID string) (*PaymentAddress, error) {
	if w.config.MoneroRPCHost == "" {
		return nil, ErrWalletNotConfigured
	}
	
	// Call monero-wallet-rpc to create subaddress
	result, err := w.moneroRPC("create_address", map[string]interface{}{
		"account_index": 0,
		"label":         fmt.Sprintf("escrow_%s", orderID),
	})
	
	if err != nil {
		return nil, fmt.Errorf("monero RPC error: %w", err)
	}
	
	address, _ := result["address"].(string)
	addressIndex, _ := result["address_index"].(float64)
	
	return &PaymentAddress{
		Currency:  CryptoXMR,
		Address:   address,
		PaymentID: fmt.Sprintf("%d", int(addressIndex)),
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}, nil
}

// generateZcashAddress creates a Zcash shielded address for escrow
func (w *CryptoWallet) generateZcashAddress(orderID string) (*PaymentAddress, error) {
	if w.config.ZcashRPCHost == "" {
		return nil, ErrWalletNotConfigured
	}
	
	// Call zcash-cli z_getnewaddress
	result, err := w.zcashRPC("z_getnewaddress", []interface{}{"sapling"})
	
	if err != nil {
		return nil, fmt.Errorf("zcash RPC error: %w", err)
	}
	
	address, _ := result.(string)
	
	return &PaymentAddress{
		Currency:  CryptoZEC,
		Address:   address,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}, nil
}

// CheckPayment checks if a payment has been received
func (w *CryptoWallet) CheckPayment(orderID string, expectedAmount float64) (*Payment, error) {
	w.mu.RLock()
	addr, ok := w.addresses[orderID]
	w.mu.RUnlock()
	
	if !ok {
		return nil, ErrPaymentNotFound
	}
	
	switch addr.Currency {
	case CryptoXMR:
		return w.checkMoneroPayment(orderID, addr, expectedAmount)
	case CryptoZEC:
		return w.checkZcashPayment(orderID, addr, expectedAmount)
	}
	
	return nil, errors.New("unsupported currency")
}

// checkMoneroPayment checks for Monero payment
func (w *CryptoWallet) checkMoneroPayment(orderID string, addr *PaymentAddress, expectedAmount float64) (*Payment, error) {
	// Query incoming transfers
	result, err := w.moneroRPC("get_transfers", map[string]interface{}{
		"in":               true,
		"pending":          true,
		"filter_by_height": false,
	})
	
	if err != nil {
		return nil, fmt.Errorf("monero RPC error: %w", err)
	}
	
	// Parse transfers and find matching payment
	transfers, _ := result["in"].([]interface{})
	for _, t := range transfers {
		transfer, _ := t.(map[string]interface{})
		txAddr, _ := transfer["address"].(string)
		if txAddr == addr.Address {
			amount, _ := transfer["amount"].(float64)
			txid, _ := transfer["txid"].(string)
			confirmations, _ := transfer["confirmations"].(float64)
			
			payment := &Payment{
				ID:            generatePaymentID(),
				OrderID:       orderID,
				Currency:      CryptoXMR,
				Address:       addr.Address,
				Amount:        amount / 1e12, // Atomic units to XMR
				AmountAtomic:  uint64(amount),
				TxID:          txid,
				Confirmations: int(confirmations),
				Status:        "pending",
				CreatedAt:     time.Now(),
			}
			
			if confirmations >= 10 {
				payment.Status = "confirmed"
				payment.ConfirmedAt = time.Now()
			}
			
			w.mu.Lock()
			w.payments[payment.ID] = payment
			w.mu.Unlock()
			
			return payment, nil
		}
	}
	
	return nil, ErrPaymentNotFound
}

// checkZcashPayment checks for Zcash payment
func (w *CryptoWallet) checkZcashPayment(orderID string, addr *PaymentAddress, expectedAmount float64) (*Payment, error) {
	// Query z_listreceivedbyaddress
	result, err := w.zcashRPC("z_listreceivedbyaddress", []interface{}{addr.Address, 0})
	
	if err != nil {
		return nil, fmt.Errorf("zcash RPC error: %w", err)
	}
	
	// Parse received transactions
	received, _ := result.([]interface{})
	for _, r := range received {
		rx, _ := r.(map[string]interface{})
		amount, _ := rx["amount"].(float64)
		txid, _ := rx["txid"].(string)
		
		payment := &Payment{
			ID:            generatePaymentID(),
			OrderID:       orderID,
			Currency:      CryptoZEC,
			Address:       addr.Address,
			Amount:        amount,
			AmountAtomic:  uint64(amount * 1e8),
			TxID:          txid,
			Confirmations: 6, // Zcash default
			Status:        "confirmed",
			CreatedAt:     time.Now(),
			ConfirmedAt:   time.Now(),
		}
		
		w.mu.Lock()
		w.payments[payment.ID] = payment
		w.mu.Unlock()
		
		return payment, nil
	}
	
	return nil, ErrPaymentNotFound
}

// ReleasePayment releases funds to seller
func (w *CryptoWallet) ReleasePayment(paymentID, sellerAddress string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	
	payment, ok := w.payments[paymentID]
	if !ok {
		return ErrPaymentNotFound
	}
	
	if payment.Status != "confirmed" {
		return ErrPaymentPending
	}
	
	// In production, this would send the transaction
	// For now, just update status
	switch payment.Currency {
	case CryptoXMR:
		// w.moneroRPC("transfer", ...)
	case CryptoZEC:
		// w.zcashRPC("z_sendmany", ...)
	}
	
	payment.Status = "released"
	payment.ReleasedAt = time.Now()
	
	return nil
}

// RefundPayment refunds funds to buyer
func (w *CryptoWallet) RefundPayment(paymentID, buyerAddress string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	
	payment, ok := w.payments[paymentID]
	if !ok {
		return ErrPaymentNotFound
	}
	
	if payment.Status != "confirmed" {
		return ErrPaymentPending
	}
	
	// In production, this would send the refund transaction
	payment.Status = "refunded"
	payment.ReleasedAt = time.Now()
	
	return nil
}

// GetPayment returns a payment by ID
func (w *CryptoWallet) GetPayment(paymentID string) (*Payment, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	
	payment, ok := w.payments[paymentID]
	if !ok {
		return nil, ErrPaymentNotFound
	}
	return payment, nil
}

// GetPaymentByOrder returns payment for an order
func (w *CryptoWallet) GetPaymentByOrder(orderID string) (*Payment, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	
	for _, p := range w.payments {
		if p.OrderID == orderID {
			return p, nil
		}
	}
	return nil, ErrPaymentNotFound
}

// GetAddress returns the escrow address for an order
func (w *CryptoWallet) GetAddress(orderID string) (*PaymentAddress, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	
	addr, ok := w.addresses[orderID]
	if !ok {
		return nil, ErrPaymentNotFound
	}
	return addr, nil
}

// Monero RPC helper
func (w *CryptoWallet) moneroRPC(method string, params interface{}) (map[string]interface{}, error) {
	if w.config.MoneroRPCHost == "" {
		return nil, ErrWalletNotConfigured
	}
	
	body := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "0",
		"method":  method,
		"params":  params,
	}
	
	jsonBody, _ := json.Marshal(body)
	
	req, _ := http.NewRequest("POST", "http://"+w.config.MoneroRPCHost+"/json_rpc", strings.NewReader(string(jsonBody)))
	req.Header.Set("Content-Type", "application/json")
	
	if w.config.MoneroRPCUser != "" {
		req.SetBasicAuth(w.config.MoneroRPCUser, w.config.MoneroRPCPassword)
	}
	
	resp, err := w.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	respBody, _ := io.ReadAll(resp.Body)
	
	var result map[string]interface{}
	json.Unmarshal(respBody, &result)
	
	if errObj, ok := result["error"]; ok {
		errMap, _ := errObj.(map[string]interface{})
		errMsg, _ := errMap["message"].(string)
		return nil, errors.New(errMsg)
	}
	
	resultData, _ := result["result"].(map[string]interface{})
	return resultData, nil
}

// Zcash RPC helper
func (w *CryptoWallet) zcashRPC(method string, params interface{}) (interface{}, error) {
	if w.config.ZcashRPCHost == "" {
		return nil, ErrWalletNotConfigured
	}
	
	body := map[string]interface{}{
		"jsonrpc": "1.0",
		"id":      "kayaknet",
		"method":  method,
		"params":  params,
	}
	
	jsonBody, _ := json.Marshal(body)
	
	req, _ := http.NewRequest("POST", "http://"+w.config.ZcashRPCHost, strings.NewReader(string(jsonBody)))
	req.Header.Set("Content-Type", "application/json")
	
	if w.config.ZcashRPCUser != "" {
		req.SetBasicAuth(w.config.ZcashRPCUser, w.config.ZcashRPCPassword)
	}
	
	resp, err := w.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	respBody, _ := io.ReadAll(resp.Body)
	
	var result map[string]interface{}
	json.Unmarshal(respBody, &result)
	
	if errObj, ok := result["error"]; ok && errObj != nil {
		errMap, _ := errObj.(map[string]interface{})
		errMsg, _ := errMap["message"].(string)
		return nil, errors.New(errMsg)
	}
	
	return result["result"], nil
}

// ValidateAddress validates a cryptocurrency address
func ValidateAddress(currency CryptoType, address string) bool {
	switch currency {
	case CryptoXMR:
		// Monero mainnet addresses: 95 chars starting with 4
		// Monero subaddresses: 95 chars starting with 8
		if len(address) != 95 {
			return false
		}
		return address[0] == '4' || address[0] == '8'
	case CryptoZEC:
		// Zcash transparent: starts with t1 or t3 (34 chars)
		// Zcash shielded (sprout): starts with zc (95 chars)
		// Zcash shielded (sapling): starts with zs (78 chars)
		if strings.HasPrefix(address, "zs") && len(address) == 78 {
			return true
		}
		if strings.HasPrefix(address, "t1") || strings.HasPrefix(address, "t3") {
			return len(address) == 35
		}
		return false
	}
	return false
}

// GetExchangeRate returns current exchange rate (USD)
func GetExchangeRate(currency CryptoType) (float64, error) {
	// In production, query a price API
	// For now, return approximate rates
	switch currency {
	case CryptoXMR:
		return 150.0, nil // ~$150 per XMR
	case CryptoZEC:
		return 30.0, nil  // ~$30 per ZEC
	}
	return 0, errors.New("unsupported currency")
}

// ConvertToAtomic converts from display units to atomic units
func ConvertToAtomic(currency CryptoType, amount float64) uint64 {
	switch currency {
	case CryptoXMR:
		return uint64(amount * 1e12) // 12 decimal places
	case CryptoZEC:
		return uint64(amount * 1e8)  // 8 decimal places
	}
	return 0
}

// ConvertFromAtomic converts from atomic units to display units
func ConvertFromAtomic(currency CryptoType, atomic uint64) float64 {
	switch currency {
	case CryptoXMR:
		return float64(atomic) / 1e12
	case CryptoZEC:
		return float64(atomic) / 1e8
	}
	return 0
}

func generatePaymentID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

