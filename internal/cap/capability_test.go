package cap

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

func TestCapabilitySignAndVerify(t *testing.T) {
	// Generate issuer keys
	issuerPub, issuerPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate issuer keys: %v", err)
	}

	// Generate grantee keys
	granteePub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate grantee keys: %v", err)
	}

	// Create capability
	cap := NewCapability("test-service", granteePub, []string{"read", "write"}, time.Hour)

	// Sign capability
	if err := cap.Sign(issuerPriv, issuerPub); err != nil {
		t.Fatalf("failed to sign capability: %v", err)
	}

	// Verify capability
	if err := cap.Verify(); err != nil {
		t.Errorf("capability verification failed: %v", err)
	}

	// Check fields
	if cap.ServiceID != "test-service" {
		t.Errorf("unexpected service ID: %s", cap.ServiceID)
	}

	if !cap.HasPermission("read") {
		t.Error("capability should have read permission")
	}

	if !cap.HasPermission("write") {
		t.Error("capability should have write permission")
	}

	if cap.HasPermission("delete") {
		t.Error("capability should not have delete permission")
	}
}

func TestCapabilityExpiry(t *testing.T) {
	issuerPub, issuerPriv, _ := ed25519.GenerateKey(rand.Reader)
	granteePub, _, _ := ed25519.GenerateKey(rand.Reader)

	// Create capability with very short TTL
	cap := NewCapability("test-service", granteePub, []string{"read"}, time.Millisecond*10)
	cap.Sign(issuerPriv, issuerPub)

	// Should be valid immediately
	if !cap.IsValid() {
		t.Error("capability should be valid immediately after creation")
	}

	if cap.IsExpired() {
		t.Error("capability should not be expired immediately")
	}

	// Wait for expiry
	time.Sleep(time.Millisecond * 20)

	if cap.IsValid() {
		t.Error("capability should be invalid after expiry")
	}

	if !cap.IsExpired() {
		t.Error("capability should be expired")
	}
}

func TestCapabilityTamperDetection(t *testing.T) {
	issuerPub, issuerPriv, _ := ed25519.GenerateKey(rand.Reader)
	granteePub, _, _ := ed25519.GenerateKey(rand.Reader)

	cap := NewCapability("test-service", granteePub, []string{"read"}, time.Hour)
	cap.Sign(issuerPriv, issuerPub)

	// Verify original is valid
	if err := cap.Verify(); err != nil {
		t.Fatalf("original capability should be valid: %v", err)
	}

	// Tamper with permissions
	cap.Permissions = append(cap.Permissions, "admin")

	// Verification should fail
	if err := cap.Verify(); err == nil {
		t.Error("tampered capability should fail verification")
	}
}

func TestCapabilityStore(t *testing.T) {
	store := NewStore()

	issuerPub, issuerPriv, _ := ed25519.GenerateKey(rand.Reader)
	granteePub, _, _ := ed25519.GenerateKey(rand.Reader)

	// Create and store capability
	cap := NewCapability("test-service", granteePub, []string{"read"}, time.Hour)
	cap.Sign(issuerPriv, issuerPub)

	if err := store.Store(cap); err != nil {
		t.Fatalf("failed to store capability: %v", err)
	}

	// Retrieve capability
	retrieved, err := store.Get(cap.CapID)
	if err != nil {
		t.Fatalf("failed to retrieve capability: %v", err)
	}

	if retrieved.CapID != cap.CapID {
		t.Error("retrieved capability ID mismatch")
	}

	// Check count
	if store.Count() != 1 {
		t.Errorf("unexpected capability count: %d", store.Count())
	}
}

func TestCapabilityRevocation(t *testing.T) {
	store := NewStore()

	issuerPub, issuerPriv, _ := ed25519.GenerateKey(rand.Reader)
	granteePub, _, _ := ed25519.GenerateKey(rand.Reader)

	// Create and store capability
	cap := NewCapability("test-service", granteePub, []string{"read"}, time.Hour)
	cap.Sign(issuerPriv, issuerPub)
	store.Store(cap)

	// Capability should be valid
	if err := store.Validate(cap); err != nil {
		t.Fatalf("capability should be valid before revocation: %v", err)
	}

	// Revoke capability
	if err := store.Revoke(cap.CapID, issuerPriv, issuerPub, "test revocation"); err != nil {
		t.Fatalf("failed to revoke capability: %v", err)
	}

	// Capability should be revoked
	if !store.IsRevoked(cap.CapID) {
		t.Error("capability should be marked as revoked")
	}

	// Validation should fail
	if err := store.Validate(cap); err == nil {
		t.Error("validation should fail for revoked capability")
	}

	// Check revocation info
	rev, ok := store.GetRevocation(cap.CapID)
	if !ok {
		t.Fatal("should be able to retrieve revocation")
	}

	if rev.Reason != "test revocation" {
		t.Errorf("unexpected revocation reason: %s", rev.Reason)
	}

	// Verify revocation signature
	if !VerifyRevocation(rev) {
		t.Error("revocation signature should be valid")
	}
}

func TestCapabilityValidateAccess(t *testing.T) {
	store := NewStore()

	issuerPub, issuerPriv, _ := ed25519.GenerateKey(rand.Reader)
	granteePub, _, _ := ed25519.GenerateKey(rand.Reader)

	// Create capability with limited permissions
	cap := NewCapability("test-service", granteePub, []string{"read", "list"}, time.Hour)
	cap.Sign(issuerPriv, issuerPub)
	store.Store(cap)

	// Should allow read
	if err := store.ValidateAccess(cap, "read"); err != nil {
		t.Errorf("should allow read access: %v", err)
	}

	// Should allow list
	if err := store.ValidateAccess(cap, "list"); err != nil {
		t.Errorf("should allow list access: %v", err)
	}

	// Should deny write
	if err := store.ValidateAccess(cap, "write"); err == nil {
		t.Error("should deny write access")
	}

	// Should deny admin
	if err := store.ValidateAccess(cap, "admin"); err == nil {
		t.Error("should deny admin access")
	}
}

func TestCapabilityWildcardPermission(t *testing.T) {
	issuerPub, issuerPriv, _ := ed25519.GenerateKey(rand.Reader)
	granteePub, _, _ := ed25519.GenerateKey(rand.Reader)

	// Create capability with wildcard permission
	cap := NewCapability("test-service", granteePub, []string{"*"}, time.Hour)
	cap.Sign(issuerPriv, issuerPub)

	// Should allow any permission
	if !cap.HasPermission("read") {
		t.Error("wildcard should allow read")
	}
	if !cap.HasPermission("write") {
		t.Error("wildcard should allow write")
	}
	if !cap.HasPermission("admin") {
		t.Error("wildcard should allow admin")
	}
	if !cap.HasPermission("anything") {
		t.Error("wildcard should allow any permission")
	}
}

func TestCapabilityWithQuota(t *testing.T) {
	issuerPub, issuerPriv, _ := ed25519.GenerateKey(rand.Reader)
	granteePub, _, _ := ed25519.GenerateKey(rand.Reader)

	cap := NewCapability("test-service", granteePub, []string{"read"}, time.Hour)
	cap.Quota = &Quota{
		MaxRequests:        1000,
		MaxBytes:           1024 * 1024 * 100, // 100MB
		MaxConcurrent:      10,
		RateLimitPerSecond: 100,
	}
	cap.Sign(issuerPriv, issuerPub)

	if err := cap.Verify(); err != nil {
		t.Errorf("capability with quota should be valid: %v", err)
	}

	if cap.Quota.MaxRequests != 1000 {
		t.Errorf("unexpected max requests: %d", cap.Quota.MaxRequests)
	}
}

func TestCapabilityMarshalUnmarshal(t *testing.T) {
	issuerPub, issuerPriv, _ := ed25519.GenerateKey(rand.Reader)
	granteePub, _, _ := ed25519.GenerateKey(rand.Reader)

	original := NewCapability("test-service", granteePub, []string{"read", "write"}, time.Hour)
	original.Metadata["custom"] = "value"
	original.Sign(issuerPriv, issuerPub)

	// Marshal
	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	// Unmarshal
	restored, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	// Verify restored capability
	if err := restored.Verify(); err != nil {
		t.Errorf("restored capability should be valid: %v", err)
	}

	if restored.CapID != original.CapID {
		t.Error("cap ID mismatch")
	}

	if restored.Metadata["custom"] != "value" {
		t.Error("metadata mismatch")
	}
}

func TestCapabilityGetForService(t *testing.T) {
	store := NewStore()

	issuerPub, issuerPriv, _ := ed25519.GenerateKey(rand.Reader)
	grantee1Pub, _, _ := ed25519.GenerateKey(rand.Reader)
	grantee2Pub, _, _ := ed25519.GenerateKey(rand.Reader)

	// Create capabilities for different services
	cap1 := NewCapability("service-a", grantee1Pub, []string{"read"}, time.Hour)
	cap1.Sign(issuerPriv, issuerPub)
	store.Store(cap1)

	cap2 := NewCapability("service-a", grantee2Pub, []string{"read"}, time.Hour)
	cap2.Sign(issuerPriv, issuerPub)
	store.Store(cap2)

	cap3 := NewCapability("service-b", grantee1Pub, []string{"read"}, time.Hour)
	cap3.Sign(issuerPriv, issuerPub)
	store.Store(cap3)

	// Get capabilities for service-a
	serviceACaps := store.GetForService("service-a")
	if len(serviceACaps) != 2 {
		t.Errorf("expected 2 capabilities for service-a, got %d", len(serviceACaps))
	}

	// Get capabilities for service-b
	serviceBCaps := store.GetForService("service-b")
	if len(serviceBCaps) != 1 {
		t.Errorf("expected 1 capability for service-b, got %d", len(serviceBCaps))
	}
}

func TestCapabilityCleanExpired(t *testing.T) {
	store := NewStore()

	issuerPub, issuerPriv, _ := ed25519.GenerateKey(rand.Reader)
	granteePub, _, _ := ed25519.GenerateKey(rand.Reader)

	// Create expired capability
	expiredCap := NewCapability("test-service", granteePub, []string{"read"}, time.Millisecond)
	expiredCap.Sign(issuerPriv, issuerPub)
	store.Store(expiredCap)

	// Create valid capability
	validCap := NewCapability("test-service", granteePub, []string{"write"}, time.Hour)
	validCap.Sign(issuerPriv, issuerPub)
	store.Store(validCap)

	// Wait for first to expire
	time.Sleep(time.Millisecond * 10)

	// Clean expired
	cleaned := store.CleanExpired()
	if cleaned != 1 {
		t.Errorf("expected 1 cleaned capability, got %d", cleaned)
	}

	if store.Count() != 1 {
		t.Errorf("expected 1 remaining capability, got %d", store.Count())
	}
}

func TestCapabilityUnauthorizedRevocation(t *testing.T) {
	store := NewStore()

	issuerPub, issuerPriv, _ := ed25519.GenerateKey(rand.Reader)
	granteePub, _, _ := ed25519.GenerateKey(rand.Reader)
	attackerPub, attackerPriv, _ := ed25519.GenerateKey(rand.Reader)

	// Create capability
	cap := NewCapability("test-service", granteePub, []string{"read"}, time.Hour)
	cap.Sign(issuerPriv, issuerPub)
	store.Store(cap)

	// Try to revoke as non-issuer
	err := store.Revoke(cap.CapID, attackerPriv, attackerPub, "malicious revocation")
	if err == nil {
		t.Error("non-issuer should not be able to revoke capability")
	}

	// Capability should still be valid
	if store.IsRevoked(cap.CapID) {
		t.Error("capability should not be revoked")
	}
}

func BenchmarkCapabilitySign(b *testing.B) {
	issuerPub, issuerPriv, _ := ed25519.GenerateKey(rand.Reader)
	granteePub, _, _ := ed25519.GenerateKey(rand.Reader)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cap := NewCapability("test-service", granteePub, []string{"read", "write"}, time.Hour)
		cap.Sign(issuerPriv, issuerPub)
	}
}

func BenchmarkCapabilityVerify(b *testing.B) {
	issuerPub, issuerPriv, _ := ed25519.GenerateKey(rand.Reader)
	granteePub, _, _ := ed25519.GenerateKey(rand.Reader)

	cap := NewCapability("test-service", granteePub, []string{"read", "write"}, time.Hour)
	cap.Sign(issuerPriv, issuerPub)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cap.Verify()
	}
}

