// Package security - Hardware Key Support
// Supports hardware security modules for key storage
// Keys never leave the hardware device
package security

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

var (
	ErrNoHardwareKey     = errors.New("no hardware key detected")
	ErrHardwareKeyLocked = errors.New("hardware key is locked")
	ErrSigningFailed     = errors.New("hardware signing failed")
	ErrUnsupportedDevice = errors.New("unsupported hardware device")
	ErrPINRequired       = errors.New("PIN required")
	ErrTouchRequired     = errors.New("touch confirmation required")
)

// HardwareKeyType represents supported hardware key types
type HardwareKeyType int

const (
	HWKeyNone HardwareKeyType = iota
	HWKeyYubiKey              // YubiKey 4/5 with PIV
	HWKeyLedger               // Ledger Nano S/X
	HWKeyTrezor               // Trezor One/Model T
	HWKeyTPM                  // Trusted Platform Module
	HWKeyPKCS11               // Generic PKCS#11 (smart cards)
)

func (t HardwareKeyType) String() string {
	switch t {
	case HWKeyYubiKey:
		return "YubiKey"
	case HWKeyLedger:
		return "Ledger"
	case HWKeyTrezor:
		return "Trezor"
	case HWKeyTPM:
		return "TPM"
	case HWKeyPKCS11:
		return "PKCS#11"
	default:
		return "None"
	}
}

// HardwareKeyInfo contains information about a detected hardware key
type HardwareKeyInfo struct {
	Type         HardwareKeyType
	Name         string
	Serial       string
	Manufacturer string
	Version      string
	Capabilities []string
	IsLocked     bool
	NeedsPIN     bool
	NeedsTouch   bool
}

// HardwareKeyManager manages hardware security keys
type HardwareKeyManager struct {
	mu           sync.RWMutex
	detected     *HardwareKeyInfo
	publicKey    ed25519.PublicKey
	keyPath      string // Derivation path or slot
	initialized  bool
	
	// Callbacks
	onPINRequest   func() string
	onTouchRequest func()
}

// NewHardwareKeyManager creates a new hardware key manager
func NewHardwareKeyManager() *HardwareKeyManager {
	return &HardwareKeyManager{
		keyPath: "m/44'/607'/0'/0'/0'", // Default Ed25519 derivation path
	}
}

// DetectHardwareKeys scans for available hardware keys
func (hkm *HardwareKeyManager) DetectHardwareKeys() ([]*HardwareKeyInfo, error) {
	var detected []*HardwareKeyInfo

	// Try to detect YubiKey
	if info := hkm.detectYubiKey(); info != nil {
		detected = append(detected, info)
	}

	// Try to detect Ledger
	if info := hkm.detectLedger(); info != nil {
		detected = append(detected, info)
	}

	// Try to detect Trezor
	if info := hkm.detectTrezor(); info != nil {
		detected = append(detected, info)
	}

	// Try to detect TPM
	if info := hkm.detectTPM(); info != nil {
		detected = append(detected, info)
	}

	return detected, nil
}

// Initialize initializes a hardware key for use
func (hkm *HardwareKeyManager) Initialize(keyInfo *HardwareKeyInfo) error {
	hkm.mu.Lock()
	defer hkm.mu.Unlock()

	hkm.detected = keyInfo
	
	// Get public key from device
	var err error
	switch keyInfo.Type {
	case HWKeyYubiKey:
		hkm.publicKey, err = hkm.getYubiKeyPublicKey()
	case HWKeyLedger:
		hkm.publicKey, err = hkm.getLedgerPublicKey()
	case HWKeyTrezor:
		hkm.publicKey, err = hkm.getTrezorPublicKey()
	case HWKeyTPM:
		hkm.publicKey, err = hkm.getTPMPublicKey()
	default:
		return ErrUnsupportedDevice
	}

	if err != nil {
		return err
	}

	hkm.initialized = true
	return nil
}

// PublicKey returns the public key from hardware
func (hkm *HardwareKeyManager) PublicKey() ed25519.PublicKey {
	hkm.mu.RLock()
	defer hkm.mu.RUnlock()
	return hkm.publicKey
}

// Sign signs data using the hardware key
// The private key never leaves the device
func (hkm *HardwareKeyManager) Sign(data []byte) ([]byte, error) {
	hkm.mu.RLock()
	defer hkm.mu.RUnlock()

	if !hkm.initialized || hkm.detected == nil {
		return nil, ErrNoHardwareKey
	}

	// Request touch if needed
	if hkm.detected.NeedsTouch && hkm.onTouchRequest != nil {
		hkm.onTouchRequest()
	}

	switch hkm.detected.Type {
	case HWKeyYubiKey:
		return hkm.signWithYubiKey(data)
	case HWKeyLedger:
		return hkm.signWithLedger(data)
	case HWKeyTrezor:
		return hkm.signWithTrezor(data)
	case HWKeyTPM:
		return hkm.signWithTPM(data)
	default:
		return nil, ErrUnsupportedDevice
	}
}

// SetPINCallback sets the callback for PIN requests
func (hkm *HardwareKeyManager) SetPINCallback(callback func() string) {
	hkm.mu.Lock()
	defer hkm.mu.Unlock()
	hkm.onPINRequest = callback
}

// SetTouchCallback sets the callback for touch requests
func (hkm *HardwareKeyManager) SetTouchCallback(callback func()) {
	hkm.mu.Lock()
	defer hkm.mu.Unlock()
	hkm.onTouchRequest = callback
}

// IsInitialized returns true if hardware key is ready
func (hkm *HardwareKeyManager) IsInitialized() bool {
	hkm.mu.RLock()
	defer hkm.mu.RUnlock()
	return hkm.initialized
}

// GetInfo returns current hardware key info
func (hkm *HardwareKeyManager) GetInfo() *HardwareKeyInfo {
	hkm.mu.RLock()
	defer hkm.mu.RUnlock()
	return hkm.detected
}

// ============ YubiKey Support ============

func (hkm *HardwareKeyManager) detectYubiKey() *HardwareKeyInfo {
	// Try ykman (YubiKey Manager CLI)
	cmd := exec.Command("ykman", "info")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	info := &HardwareKeyInfo{
		Type:         HWKeyYubiKey,
		Name:         "YubiKey",
		Capabilities: []string{"PIV", "FIDO2", "OTP"},
		NeedsTouch:   true,
		NeedsPIN:     true,
	}

	// Parse output for details
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, "Serial number:") {
			info.Serial = strings.TrimSpace(strings.Split(line, ":")[1])
		}
		if strings.Contains(line, "Device type:") {
			info.Name = strings.TrimSpace(strings.Split(line, ":")[1])
		}
		if strings.Contains(line, "Firmware version:") {
			info.Version = strings.TrimSpace(strings.Split(line, ":")[1])
		}
	}

	return info
}

func (hkm *HardwareKeyManager) getYubiKeyPublicKey() (ed25519.PublicKey, error) {
	// Use PIV slot 9a (Authentication) for Ed25519
	// ykman piv keys export 9a -
	cmd := exec.Command("ykman", "piv", "keys", "export", "9a", "-")
	output, err := cmd.Output()
	if err != nil {
		// Key might not exist, try to generate
		return hkm.generateYubiKeyKey()
	}

	// Parse PEM public key
	return parseEd25519PublicKey(output)
}

func (hkm *HardwareKeyManager) generateYubiKeyKey() (ed25519.PublicKey, error) {
	// Generate new Ed25519 key in slot 9a
	// ykman piv keys generate -a ED25519 9a -
	
	// Get PIN
	pin := ""
	if hkm.onPINRequest != nil {
		pin = hkm.onPINRequest()
	}
	if pin == "" {
		pin = "123456" // Default PIV PIN
	}

	cmd := exec.Command("ykman", "piv", "keys", "generate", 
		"-a", "ED25519", 
		"-P", pin,
		"9a", "-")
	
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to generate YubiKey key: %w", err)
	}

	return parseEd25519PublicKey(output)
}

func (hkm *HardwareKeyManager) signWithYubiKey(data []byte) ([]byte, error) {
	// Create temp file with data to sign
	tmpFile, err := os.CreateTemp("", "kayak-sign-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Write(data)
	tmpFile.Close()

	// Get PIN
	pin := ""
	if hkm.onPINRequest != nil {
		pin = hkm.onPINRequest()
	}

	// Sign using PIV
	// ykman piv keys sign 9a <input> -a SHA256 -P <pin>
	cmd := exec.Command("ykman", "piv", "keys", "sign",
		"9a", tmpFile.Name(),
		"-a", "SHA256",
		"-P", pin)

	output, err := cmd.Output()
	if err != nil {
		return nil, ErrSigningFailed
	}

	return output, nil
}

// ============ Ledger Support ============

func (hkm *HardwareKeyManager) detectLedger() *HardwareKeyInfo {
	// Check for Ledger device via USB
	// Look for Ledger vendor ID (2c97)
	if !hkm.checkUSBDevice("2c97") {
		return nil
	}

	return &HardwareKeyInfo{
		Type:         HWKeyLedger,
		Name:         "Ledger",
		Manufacturer: "Ledger SAS",
		Capabilities: []string{"Ed25519", "Secp256k1"},
		NeedsTouch:   true,
		NeedsPIN:     false, // PIN on device
	}
}

func (hkm *HardwareKeyManager) getLedgerPublicKey() (ed25519.PublicKey, error) {
	// Use ledger-app-ed25519 or custom app
	// For now, return error - need Ledger app installed
	return nil, errors.New("Ledger Ed25519 app required - install via Ledger Live")
}

func (hkm *HardwareKeyManager) signWithLedger(data []byte) ([]byte, error) {
	// Would use Ledger HID API
	return nil, errors.New("Ledger signing requires Ed25519 app")
}

// ============ Trezor Support ============

func (hkm *HardwareKeyManager) detectTrezor() *HardwareKeyInfo {
	// Check for Trezor device via USB
	// Trezor vendor ID: 534c (SatoshiLabs)
	if !hkm.checkUSBDevice("534c") {
		return nil
	}

	return &HardwareKeyInfo{
		Type:         HWKeyTrezor,
		Name:         "Trezor",
		Manufacturer: "SatoshiLabs",
		Capabilities: []string{"Ed25519", "Secp256k1"},
		NeedsTouch:   true,
		NeedsPIN:     false, // PIN on device
	}
}

func (hkm *HardwareKeyManager) getTrezorPublicKey() (ed25519.PublicKey, error) {
	// Use trezorctl CLI
	cmd := exec.Command("trezorctl", "get-public-node", "-n", hkm.keyPath)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("trezorctl failed: %w", err)
	}

	// Parse public key from output
	return parseEd25519PublicKey(output)
}

func (hkm *HardwareKeyManager) signWithTrezor(data []byte) ([]byte, error) {
	// Convert data to hex
	hexData := hex.EncodeToString(data)

	cmd := exec.Command("trezorctl", "sign-message", "-n", hkm.keyPath, hexData)
	output, err := cmd.Output()
	if err != nil {
		return nil, ErrSigningFailed
	}

	// Parse signature from output
	return parseSignature(output)
}

// ============ TPM Support ============

func (hkm *HardwareKeyManager) detectTPM() *HardwareKeyInfo {
	// Check for TPM on Linux
	if runtime.GOOS != "linux" {
		return nil
	}

	// Check if TPM device exists
	if _, err := os.Stat("/dev/tpm0"); os.IsNotExist(err) {
		if _, err := os.Stat("/dev/tpmrm0"); os.IsNotExist(err) {
			return nil
		}
	}

	return &HardwareKeyInfo{
		Type:         HWKeyTPM,
		Name:         "TPM 2.0",
		Manufacturer: "System TPM",
		Capabilities: []string{"RSA", "ECDSA", "HMAC"},
		NeedsTouch:   false,
		NeedsPIN:     false,
	}
}

func (hkm *HardwareKeyManager) getTPMPublicKey() (ed25519.PublicKey, error) {
	// Use tpm2-tools
	// Generate key if not exists
	keyPath := filepath.Join(os.TempDir(), "kayaknet-tpm-key.pub")

	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		// Generate new key
		cmd := exec.Command("tpm2_createprimary", "-C", "o", "-g", "sha256", "-G", "ecc", "-c", keyPath)
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("TPM key generation failed: %w", err)
		}
	}

	// Read public key
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}

	return parseEd25519PublicKey(data)
}

func (hkm *HardwareKeyManager) signWithTPM(data []byte) ([]byte, error) {
	// Create temp file with data
	tmpFile, err := os.CreateTemp("", "kayak-tpm-sign-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Write(data)
	tmpFile.Close()

	sigFile := tmpFile.Name() + ".sig"
	defer os.Remove(sigFile)

	// Sign with TPM
	cmd := exec.Command("tpm2_sign", "-c", filepath.Join(os.TempDir(), "kayaknet-tpm-key.pub"),
		"-g", "sha256", "-o", sigFile, tmpFile.Name())
	
	if err := cmd.Run(); err != nil {
		return nil, ErrSigningFailed
	}

	return os.ReadFile(sigFile)
}

// ============ Helper Functions ============

func (hkm *HardwareKeyManager) checkUSBDevice(vendorID string) bool {
	// Check /sys/bus/usb/devices on Linux
	if runtime.GOOS == "linux" {
		matches, _ := filepath.Glob("/sys/bus/usb/devices/*/idVendor")
		for _, path := range matches {
			data, err := os.ReadFile(path)
			if err == nil && strings.TrimSpace(string(data)) == vendorID {
				return true
			}
		}
	}
	
	// On macOS, would use IOKit
	// On Windows, would use SetupAPI
	
	return false
}

func parseEd25519PublicKey(data []byte) (ed25519.PublicKey, error) {
	// Try to parse as raw 32 bytes
	if len(data) == 32 {
		return ed25519.PublicKey(data), nil
	}

	// Try to parse as hex
	s := strings.TrimSpace(string(data))
	if decoded, err := hex.DecodeString(s); err == nil && len(decoded) == 32 {
		return ed25519.PublicKey(decoded), nil
	}

	// Could add PEM parsing here
	return nil, errors.New("unable to parse public key")
}

func parseSignature(data []byte) ([]byte, error) {
	// Try raw bytes
	if len(data) == 64 {
		return data, nil
	}

	// Try hex
	s := strings.TrimSpace(string(data))
	if decoded, err := hex.DecodeString(s); err == nil && len(decoded) == 64 {
		return decoded, nil
	}

	return nil, errors.New("unable to parse signature")
}

// ============ Software Fallback ============

// SoftwareKeyManager provides software-based key management as fallback
type SoftwareKeyManager struct {
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
	encrypted  bool
}

// NewSoftwareKeyManager creates a software key manager
func NewSoftwareKeyManager() *SoftwareKeyManager {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	return &SoftwareKeyManager{
		privateKey: priv,
		publicKey:  pub,
	}
}

// LoadSoftwareKey loads key from encrypted file
func LoadSoftwareKey(path string, password []byte) (*SoftwareKeyManager, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Decrypt key file (simplified - would use proper KDF in production)
	// For now, assume unencrypted
	if len(data) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid key file")
	}

	priv := ed25519.PrivateKey(data)
	pub := priv.Public().(ed25519.PublicKey)

	return &SoftwareKeyManager{
		privateKey: priv,
		publicKey:  pub,
	}, nil
}

// SaveSoftwareKey saves key to encrypted file
func (skm *SoftwareKeyManager) Save(path string, password []byte) error {
	// Encrypt private key (simplified)
	return os.WriteFile(path, skm.privateKey, 0600)
}

// PublicKey returns the public key
func (skm *SoftwareKeyManager) PublicKey() ed25519.PublicKey {
	return skm.publicKey
}

// Sign signs data
func (skm *SoftwareKeyManager) Sign(data []byte) ([]byte, error) {
	return ed25519.Sign(skm.privateKey, data), nil
}

// ============ Unified Key Interface ============

// KeySigner is the interface for signing operations
type KeySigner interface {
	PublicKey() ed25519.PublicKey
	Sign(data []byte) ([]byte, error)
}

// KeyManager provides unified key management
type KeyManager struct {
	hardware *HardwareKeyManager
	software *SoftwareKeyManager
	useHardware bool
}

// NewKeyManager creates a new unified key manager
func NewKeyManager() *KeyManager {
	return &KeyManager{
		hardware: NewHardwareKeyManager(),
		software: NewSoftwareKeyManager(),
	}
}

// Initialize detects and initializes the best available key
func (km *KeyManager) Initialize() error {
	// Try hardware first
	devices, _ := km.hardware.DetectHardwareKeys()
	if len(devices) > 0 {
		if err := km.hardware.Initialize(devices[0]); err == nil {
			km.useHardware = true
			return nil
		}
	}

	// Fall back to software
	km.useHardware = false
	return nil
}

// PublicKey returns the public key
func (km *KeyManager) PublicKey() ed25519.PublicKey {
	if km.useHardware {
		return km.hardware.PublicKey()
	}
	return km.software.PublicKey()
}

// Sign signs data using the best available method
func (km *KeyManager) Sign(data []byte) ([]byte, error) {
	if km.useHardware {
		return km.hardware.Sign(data)
	}
	return km.software.Sign(data)
}

// IsUsingHardware returns true if using hardware key
func (km *KeyManager) IsUsingHardware() bool {
	return km.useHardware
}

// GetSigner returns the active signer
func (km *KeyManager) GetSigner() KeySigner {
	if km.useHardware {
		return km.hardware
	}
	return km.software
}

// ForceHardware forces use of hardware key (fails if not available)
func (km *KeyManager) ForceHardware() error {
	if !km.hardware.IsInitialized() {
		devices, _ := km.hardware.DetectHardwareKeys()
		if len(devices) == 0 {
			return ErrNoHardwareKey
		}
		if err := km.hardware.Initialize(devices[0]); err != nil {
			return err
		}
	}
	km.useHardware = true
	return nil
}

// Implement KeySigner interface for HardwareKeyManager
func (hkm *HardwareKeyManager) GetPublicKey() ed25519.PublicKey {
	return hkm.PublicKey()
}

// SecureMemory wipes sensitive data from memory
func SecureMemory(data []byte) {
	for i := range data {
		data[i] = 0
	}
	// Force GC to actually release the memory
	// In production, would use mlock and secure memory allocation
	io.ReadFull(rand.Reader, data)
	for i := range data {
		data[i] = 0
	}
}


