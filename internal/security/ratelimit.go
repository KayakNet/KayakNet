// Package security - Rate limiting and DoS protection
package security

import (
	"sync"
	"time"
)

// RateLimiter implements a token bucket rate limiter per peer
type RateLimiter struct {
	mu       sync.RWMutex
	buckets  map[string]*tokenBucket
	rate     float64       // tokens per second
	burst    int           // max tokens (burst capacity)
	window   time.Duration // cleanup window
}

type tokenBucket struct {
	tokens    float64
	lastCheck time.Time
}

// NewRateLimiter creates a rate limiter
// rate: messages per second allowed
// burst: max burst capacity
func NewRateLimiter(rate float64, burst int) *RateLimiter {
	rl := &RateLimiter{
		buckets: make(map[string]*tokenBucket),
		rate:    rate,
		burst:   burst,
		window:  5 * time.Minute,
	}
	go rl.cleanup()
	return rl
}

// Allow checks if a peer is within rate limits
func (rl *RateLimiter) Allow(peerID string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	bucket, exists := rl.buckets[peerID]
	if !exists {
		bucket = &tokenBucket{
			tokens:    float64(rl.burst),
			lastCheck: time.Now(),
		}
		rl.buckets[peerID] = bucket
	}

	// Refill tokens based on elapsed time
	now := time.Now()
	elapsed := now.Sub(bucket.lastCheck).Seconds()
	bucket.tokens += elapsed * rl.rate
	if bucket.tokens > float64(rl.burst) {
		bucket.tokens = float64(rl.burst)
	}
	bucket.lastCheck = now

	// Check if we have tokens
	if bucket.tokens >= 1 {
		bucket.tokens--
		return true
	}

	return false
}

// GetTokens returns remaining tokens for a peer
func (rl *RateLimiter) GetTokens(peerID string) float64 {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	if bucket, exists := rl.buckets[peerID]; exists {
		return bucket.tokens
	}
	return float64(rl.burst)
}

// cleanup removes stale buckets
func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(rl.window)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()
		cutoff := time.Now().Add(-rl.window)
		for id, bucket := range rl.buckets {
			if bucket.lastCheck.Before(cutoff) {
				delete(rl.buckets, id)
			}
		}
		rl.mu.Unlock()
	}
}

// BanList manages banned peers
type BanList struct {
	mu      sync.RWMutex
	banned  map[string]*BanEntry
	maxBans int
}

// BanEntry tracks ban information
type BanEntry struct {
	PeerID    string
	IP        string
	Reason    string
	BannedAt  time.Time
	ExpiresAt time.Time
	Score     int // negative score accumulation
}

// NewBanList creates a new ban list
func NewBanList(maxBans int) *BanList {
	bl := &BanList{
		banned:  make(map[string]*BanEntry),
		maxBans: maxBans,
	}
	go bl.cleanup()
	return bl
}

// Ban adds a peer to the ban list
func (bl *BanList) Ban(peerID, ip, reason string, duration time.Duration) {
	bl.mu.Lock()
	defer bl.mu.Unlock()

	bl.banned[peerID] = &BanEntry{
		PeerID:    peerID,
		IP:        ip,
		Reason:    reason,
		BannedAt:  time.Now(),
		ExpiresAt: time.Now().Add(duration),
		Score:     -100,
	}

	// Also ban by IP to prevent easy reconnection
	bl.banned[ip] = &BanEntry{
		PeerID:    peerID,
		IP:        ip,
		Reason:    reason,
		BannedAt:  time.Now(),
		ExpiresAt: time.Now().Add(duration),
		Score:     -100,
	}
}

// IsBanned checks if a peer or IP is banned
func (bl *BanList) IsBanned(identifier string) bool {
	bl.mu.RLock()
	defer bl.mu.RUnlock()

	entry, exists := bl.banned[identifier]
	if !exists {
		return false
	}

	// Check if ban expired
	if time.Now().After(entry.ExpiresAt) {
		return false
	}

	return true
}

// Unban removes a peer from the ban list
func (bl *BanList) Unban(identifier string) {
	bl.mu.Lock()
	defer bl.mu.Unlock()
	delete(bl.banned, identifier)
}

// GetBanEntry returns ban details
func (bl *BanList) GetBanEntry(identifier string) *BanEntry {
	bl.mu.RLock()
	defer bl.mu.RUnlock()
	return bl.banned[identifier]
}

// ListBanned returns all active bans
func (bl *BanList) ListBanned() []*BanEntry {
	bl.mu.RLock()
	defer bl.mu.RUnlock()

	var bans []*BanEntry
	now := time.Now()
	for _, entry := range bl.banned {
		if now.Before(entry.ExpiresAt) {
			bans = append(bans, entry)
		}
	}
	return bans
}

// cleanup removes expired bans
func (bl *BanList) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		bl.mu.Lock()
		now := time.Now()
		for id, entry := range bl.banned {
			if now.After(entry.ExpiresAt) {
				delete(bl.banned, id)
			}
		}
		bl.mu.Unlock()
	}
}

// PeerScorer tracks peer behavior and scores them
type PeerScorer struct {
	mu     sync.RWMutex
	scores map[string]*PeerScore
	banList *BanList
}

// PeerScore tracks individual peer reputation
type PeerScore struct {
	PeerID        string
	Score         float64
	GoodMessages  int
	BadMessages   int
	InvalidSigs   int
	RateLimitHits int
	LastUpdate    time.Time
}

// NewPeerScorer creates a peer scorer with ban list integration
func NewPeerScorer(banList *BanList) *PeerScorer {
	ps := &PeerScorer{
		scores:  make(map[string]*PeerScore),
		banList: banList,
	}
	go ps.decay()
	return ps
}

// RecordGood records good behavior
func (ps *PeerScorer) RecordGood(peerID string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	score := ps.getOrCreate(peerID)
	score.GoodMessages++
	score.Score += 1
	if score.Score > 100 {
		score.Score = 100
	}
	score.LastUpdate = time.Now()
}

// RecordBad records bad behavior - automatically bans repeat offenders
func (ps *PeerScorer) RecordBad(peerID, ip, reason string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	score := ps.getOrCreate(peerID)
	score.BadMessages++
	score.Score -= 10
	score.LastUpdate = time.Now()

	// Automatic escalating bans based on severity
	if score.BadMessages >= 5 {
		// 5+ bad messages: 1 hour ban
		ps.banList.Ban(peerID, ip, reason, 1*time.Hour)
	}
	if score.BadMessages >= 10 {
		// 10+ bad messages: 24 hour ban
		ps.banList.Ban(peerID, ip, reason, 24*time.Hour)
	}
	if score.BadMessages >= 20 {
		// 20+ bad messages: 7 day ban
		ps.banList.Ban(peerID, ip, reason, 7*24*time.Hour)
	}
}

// RecordInvalidSignature records signature verification failure
// Invalid signatures are serious - likely an attacker
func (ps *PeerScorer) RecordInvalidSignature(peerID, ip string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	score := ps.getOrCreate(peerID)
	score.InvalidSigs++
	score.Score -= 50 // Heavy penalty

	// Immediate escalating bans - invalid sigs are always malicious
	if score.InvalidSigs >= 1 {
		// First offense: 1 hour (could be network corruption)
		ps.banList.Ban(peerID, ip, "invalid signature", 1*time.Hour)
	}
	if score.InvalidSigs >= 2 {
		// Second offense: 24 hours
		ps.banList.Ban(peerID, ip, "repeated invalid signatures", 24*time.Hour)
	}
	if score.InvalidSigs >= 3 {
		// Third offense: permanent (30 days)
		ps.banList.Ban(peerID, ip, "persistent signature attacks", 30*24*time.Hour)
	}
}

// RecordRateLimitHit records rate limit violation
// Automatic throttling and banning for flooding
func (ps *PeerScorer) RecordRateLimitHit(peerID, ip string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	score := ps.getOrCreate(peerID)
	score.RateLimitHits++
	score.Score -= 5

	// Automatic escalating response to flooding
	if score.RateLimitHits >= 10 {
		// Light flooding: 5 minute cooldown
		ps.banList.Ban(peerID, ip, "rate limit exceeded", 5*time.Minute)
	}
	if score.RateLimitHits >= 30 {
		// Moderate flooding: 1 hour ban
		ps.banList.Ban(peerID, ip, "flooding", 1*time.Hour)
	}
	if score.RateLimitHits >= 100 {
		// Heavy flooding: 24 hour ban
		ps.banList.Ban(peerID, ip, "DoS attempt", 24*time.Hour)
	}
}

// GetScore returns a peer's score
func (ps *PeerScorer) GetScore(peerID string) float64 {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	if score, exists := ps.scores[peerID]; exists {
		return score.Score
	}
	return 0
}

// getOrCreate returns existing score or creates new one (caller must hold lock)
func (ps *PeerScorer) getOrCreate(peerID string) *PeerScore {
	if score, exists := ps.scores[peerID]; exists {
		return score
	}

	score := &PeerScore{
		PeerID:     peerID,
		Score:      0,
		LastUpdate: time.Now(),
	}
	ps.scores[peerID] = score
	return score
}

// decay periodically decays scores toward zero
func (ps *PeerScorer) decay() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		ps.mu.Lock()
		for _, score := range ps.scores {
			// Decay toward zero by 10%
			score.Score *= 0.9
		}
		ps.mu.Unlock()
	}
}

