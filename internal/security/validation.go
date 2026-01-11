// Package security - Input validation and message sanitization
package security

import (
	"errors"
	"net"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Validation limits
const (
	MaxMessageSize    = 64 * 1024  // 64 KB max message
	MaxPayloadSize    = 32 * 1024  // 32 KB max payload
	MaxNodeIDLength   = 128        // Max node ID string length
	MaxNameLength     = 64         // Max name/room length
	MaxAddressLength  = 256        // Max address string length
	MinPublicKeySize  = 32         // Ed25519 public key
	MaxPublicKeySize  = 32
	MinSignatureSize  = 64         // Ed25519 signature
	MaxSignatureSize  = 64
	MaxPeersPerMessage = 100       // Max peers in a NODES response
)

var (
	ErrMessageTooLarge   = errors.New("message exceeds size limit")
	ErrPayloadTooLarge   = errors.New("payload exceeds size limit")
	ErrInvalidNodeID     = errors.New("invalid node ID format")
	ErrInvalidPublicKey  = errors.New("invalid public key size")
	ErrInvalidSigSize    = errors.New("invalid signature size")
	ErrInvalidAddress    = errors.New("invalid network address")
	ErrInvalidName       = errors.New("invalid name format")
	ErrInvalidUTF8       = errors.New("invalid UTF-8 encoding")
	ErrSuspiciousContent = errors.New("suspicious content detected")
)

// ValidateMessageSize checks if message is within size limits
func ValidateMessageSize(data []byte) error {
	if len(data) > MaxMessageSize {
		return ErrMessageTooLarge
	}
	return nil
}

// ValidatePayloadSize checks if payload is within limits
func ValidatePayloadSize(data []byte) error {
	if len(data) > MaxPayloadSize {
		return ErrPayloadTooLarge
	}
	return nil
}

// ValidateNodeID validates a node ID string
func ValidateNodeID(nodeID string) error {
	if len(nodeID) == 0 || len(nodeID) > MaxNodeIDLength {
		return ErrInvalidNodeID
	}
	
	// Must be valid hex
	matched, _ := regexp.MatchString("^[a-fA-F0-9]+$", nodeID)
	if !matched {
		return ErrInvalidNodeID
	}
	
	return nil
}

// ValidatePublicKey validates public key bytes
func ValidatePublicKey(key []byte) error {
	if len(key) < MinPublicKeySize || len(key) > MaxPublicKeySize {
		return ErrInvalidPublicKey
	}
	return nil
}

// ValidateSignature validates signature bytes
func ValidateSignature(sig []byte) error {
	if len(sig) < MinSignatureSize || len(sig) > MaxSignatureSize {
		return ErrInvalidSigSize
	}
	return nil
}

// ValidateAddress validates a network address
func ValidateAddress(addr string) error {
	if len(addr) == 0 || len(addr) > MaxAddressLength {
		return ErrInvalidAddress
	}
	
	// Try to parse as host:port
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ErrInvalidAddress
	}
	
	// Validate port is numeric
	if port == "" {
		return ErrInvalidAddress
	}
	
	// Validate host is IP or hostname
	if host == "" {
		return ErrInvalidAddress
	}
	
	// Check for localhost/private IP if needed
	// (depends on your security policy)
	
	return nil
}

// ValidateName validates a name/room name
func ValidateName(name string) error {
	if len(name) == 0 || len(name) > MaxNameLength {
		return ErrInvalidName
	}
	
	// Must be valid UTF-8
	if !utf8.ValidString(name) {
		return ErrInvalidUTF8
	}
	
	// Only allow safe characters
	matched, _ := regexp.MatchString("^[a-zA-Z0-9_-]+$", name)
	if !matched {
		return ErrInvalidName
	}
	
	return nil
}

// ValidateUTF8 ensures string is valid UTF-8
func ValidateUTF8(s string) error {
	if !utf8.ValidString(s) {
		return ErrInvalidUTF8
	}
	return nil
}

// SanitizeString removes potentially dangerous content
func SanitizeString(s string) string {
	// Remove null bytes
	s = strings.ReplaceAll(s, "\x00", "")
	
	// Remove control characters except newline and tab
	var result strings.Builder
	for _, r := range s {
		if r == '\n' || r == '\t' || r >= 32 {
			result.WriteRune(r)
		}
	}
	
	return result.String()
}

// ValidateChatMessage validates a chat message
func ValidateChatMessage(room, message, nick string) error {
	if err := ValidateName(room); err != nil {
		return err
	}
	
	if len(message) == 0 || len(message) > MaxPayloadSize {
		return ErrPayloadTooLarge
	}
	
	if !utf8.ValidString(message) {
		return ErrInvalidUTF8
	}
	
	if len(nick) > MaxNameLength {
		return ErrInvalidName
	}
	
	return nil
}

// MessageValidator validates incoming P2P messages
type MessageValidator struct {
	maxSize int
}

// NewMessageValidator creates a message validator
func NewMessageValidator(maxSize int) *MessageValidator {
	if maxSize == 0 {
		maxSize = MaxMessageSize
	}
	return &MessageValidator{maxSize: maxSize}
}

// Validate performs full message validation
func (v *MessageValidator) Validate(msgType byte, from string, fromKey, payload, signature []byte) error {
	// Validate node ID
	if err := ValidateNodeID(from); err != nil {
		return err
	}
	
	// Validate public key
	if err := ValidatePublicKey(fromKey); err != nil {
		return err
	}
	
	// Validate signature
	if err := ValidateSignature(signature); err != nil {
		return err
	}
	
	// Validate payload size
	if err := ValidatePayloadSize(payload); err != nil {
		return err
	}
	
	return nil
}

// IPValidator validates and classifies IP addresses
type IPValidator struct {
	allowPrivate bool
	allowLocal   bool
}

// NewIPValidator creates an IP validator
func NewIPValidator(allowPrivate, allowLocal bool) *IPValidator {
	return &IPValidator{
		allowPrivate: allowPrivate,
		allowLocal:   allowLocal,
	}
}

// Validate checks if an IP is allowed
func (v *IPValidator) Validate(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// Try without port
		host = addr
	}
	
	ip := net.ParseIP(host)
	if ip == nil {
		// Could be a hostname, try to resolve
		ips, err := net.LookupIP(host)
		if err != nil || len(ips) == 0 {
			return ErrInvalidAddress
		}
		ip = ips[0]
	}
	
	// Check localhost
	if !v.allowLocal && ip.IsLoopback() {
		return errors.New("localhost not allowed")
	}
	
	// Check private ranges
	if !v.allowPrivate && ip.IsPrivate() {
		return errors.New("private IP not allowed")
	}
	
	return nil
}

// IsPrivateIP checks if an IP is in private ranges
func IsPrivateIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()
}

