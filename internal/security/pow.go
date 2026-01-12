// Package security - Proof of Work for anti-Sybil protection
// New nodes must solve a computational puzzle before joining
// Makes it expensive to create many fake nodes
package security

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	// DefaultDifficulty - number of leading zero bits required
	// 20 bits = ~1 million hashes = ~1 second on modern CPU
	// 24 bits = ~16 million hashes = ~10-15 seconds
	// 28 bits = ~256 million hashes = ~2-3 minutes
	DefaultDifficulty = 20

	// MaxDifficulty - prevent unreasonable difficulty
	MaxDifficulty = 32

	// ProofValidityDuration - how long a proof is valid
	ProofValidityDuration = 24 * time.Hour

	// ChallengeExpiry - how long a challenge is valid
	ChallengeExpiry = 10 * time.Minute
)

var (
	ErrInvalidProof      = errors.New("invalid proof of work")
	ErrProofExpired      = errors.New("proof of work expired")
	ErrChallengeExpired  = errors.New("challenge expired")
	ErrDifficultyTooLow  = errors.New("difficulty too low")
	ErrAlreadyVerified   = errors.New("node already verified")
)

// ProofOfWork represents a solved PoW challenge
type ProofOfWork struct {
	NodeID     string    `json:"node_id"`
	Challenge  []byte    `json:"challenge"`
	Nonce      uint64    `json:"nonce"`
	Difficulty int       `json:"difficulty"`
	Hash       []byte    `json:"hash"`
	Timestamp  time.Time `json:"timestamp"`
	Signature  []byte    `json:"signature"` // Signed by the node
}

// Challenge represents a PoW challenge issued to a new node
type Challenge struct {
	ID         string    `json:"id"`
	Data       []byte    `json:"data"`
	Difficulty int       `json:"difficulty"`
	IssuedAt   time.Time `json:"issued_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	ForNodeID  string    `json:"for_node_id"`
}

// PoWManager manages proof of work challenges and verification
type PoWManager struct {
	mu              sync.RWMutex
	difficulty      int
	challenges      map[string]*Challenge  // challengeID -> challenge
	verifiedNodes   map[string]*ProofOfWork // nodeID -> proof
	pendingProofs   map[string]time.Time   // nodeID -> when they started
}

// NewPoWManager creates a new PoW manager
func NewPoWManager(difficulty int) *PoWManager {
	if difficulty <= 0 {
		difficulty = DefaultDifficulty
	}
	if difficulty > MaxDifficulty {
		difficulty = MaxDifficulty
	}

	pm := &PoWManager{
		difficulty:    difficulty,
		challenges:    make(map[string]*Challenge),
		verifiedNodes: make(map[string]*ProofOfWork),
		pendingProofs: make(map[string]time.Time),
	}

	go pm.cleanup()
	return pm
}

// GenerateChallenge creates a new challenge for a node
func (pm *PoWManager) GenerateChallenge(nodeID string) *Challenge {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Generate random challenge data
	challengeData := make([]byte, 32)
	timestamp := time.Now()
	binary.BigEndian.PutUint64(challengeData[:8], uint64(timestamp.UnixNano()))
	copy(challengeData[8:], []byte(nodeID)[:min(24, len(nodeID))])

	// Hash to create final challenge
	hash := sha256.Sum256(challengeData)

	challenge := &Challenge{
		ID:         hex.EncodeToString(hash[:16]),
		Data:       hash[:],
		Difficulty: pm.difficulty,
		IssuedAt:   timestamp,
		ExpiresAt:  timestamp.Add(ChallengeExpiry),
		ForNodeID:  nodeID,
	}

	pm.challenges[challenge.ID] = challenge
	pm.pendingProofs[nodeID] = timestamp

	return challenge
}

// SolveChallenge solves a PoW challenge (CPU-intensive)
// Returns the nonce that produces a hash with required leading zeros
func SolveChallenge(challenge *Challenge, nodeID string) (*ProofOfWork, error) {
	if time.Now().After(challenge.ExpiresAt) {
		return nil, ErrChallengeExpired
	}

	var nonce uint64
	target := calculateTarget(challenge.Difficulty)

	// Prepare base data for hashing
	baseData := make([]byte, len(challenge.Data)+len(nodeID)+8)
	copy(baseData, challenge.Data)
	copy(baseData[len(challenge.Data):], []byte(nodeID))

	startTime := time.Now()
	maxIterations := uint64(1) << 40 // Safety limit

	for nonce = 0; nonce < maxIterations; nonce++ {
		// Add nonce to data
		binary.BigEndian.PutUint64(baseData[len(baseData)-8:], nonce)

		// Hash
		hash := sha256.Sum256(baseData)

		// Check if hash meets difficulty
		if meetsTarget(hash[:], target) {
			return &ProofOfWork{
				NodeID:     nodeID,
				Challenge:  challenge.Data,
				Nonce:      nonce,
				Difficulty: challenge.Difficulty,
				Hash:       hash[:],
				Timestamp:  time.Now(),
			}, nil
		}

		// Check timeout (don't run forever)
		if nonce%1000000 == 0 && time.Since(startTime) > 10*time.Minute {
			return nil, errors.New("solving timeout - difficulty too high")
		}
	}

	return nil, errors.New("failed to find solution")
}

// VerifyProof verifies a submitted proof of work
func (pm *PoWManager) VerifyProof(proof *ProofOfWork) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Check if already verified
	if _, exists := pm.verifiedNodes[proof.NodeID]; exists {
		return ErrAlreadyVerified
	}

	// Check timestamp
	if time.Since(proof.Timestamp) > ProofValidityDuration {
		return ErrProofExpired
	}

	// Check difficulty
	if proof.Difficulty < pm.difficulty {
		return ErrDifficultyTooLow
	}

	// Verify the hash
	baseData := make([]byte, len(proof.Challenge)+len(proof.NodeID)+8)
	copy(baseData, proof.Challenge)
	copy(baseData[len(proof.Challenge):], []byte(proof.NodeID))
	binary.BigEndian.PutUint64(baseData[len(baseData)-8:], proof.Nonce)

	hash := sha256.Sum256(baseData)

	// Check hash matches
	if !bytes.Equal(hash[:], proof.Hash) {
		return ErrInvalidProof
	}

	// Check difficulty is met
	target := calculateTarget(proof.Difficulty)
	if !meetsTarget(hash[:], target) {
		return ErrInvalidProof
	}

	// Store verified proof
	pm.verifiedNodes[proof.NodeID] = proof
	delete(pm.pendingProofs, proof.NodeID)

	return nil
}

// IsVerified checks if a node has completed PoW
func (pm *PoWManager) IsVerified(nodeID string) bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	proof, exists := pm.verifiedNodes[nodeID]
	if !exists {
		return false
	}

	// Check if proof is still valid
	if time.Since(proof.Timestamp) > ProofValidityDuration {
		return false
	}

	return true
}

// GetProof returns the proof for a node
func (pm *PoWManager) GetProof(nodeID string) *ProofOfWork {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.verifiedNodes[nodeID]
}

// RequireProof checks if a node needs to submit proof
func (pm *PoWManager) RequireProof(nodeID string) bool {
	return !pm.IsVerified(nodeID)
}

// SetDifficulty updates the difficulty (for network adaptation)
func (pm *PoWManager) SetDifficulty(difficulty int) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if difficulty > 0 && difficulty <= MaxDifficulty {
		pm.difficulty = difficulty
	}
}

// GetDifficulty returns current difficulty
func (pm *PoWManager) GetDifficulty() int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.difficulty
}

// Stats returns PoW statistics
func (pm *PoWManager) Stats() map[string]interface{} {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	return map[string]interface{}{
		"difficulty":      pm.difficulty,
		"verified_nodes":  len(pm.verifiedNodes),
		"pending_proofs":  len(pm.pendingProofs),
		"active_challenges": len(pm.challenges),
	}
}

// cleanup removes expired challenges and proofs
func (pm *PoWManager) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		pm.mu.Lock()
		now := time.Now()

		// Clean expired challenges
		for id, challenge := range pm.challenges {
			if now.After(challenge.ExpiresAt) {
				delete(pm.challenges, id)
			}
		}

		// Clean expired proofs
		for nodeID, proof := range pm.verifiedNodes {
			if now.Sub(proof.Timestamp) > ProofValidityDuration {
				delete(pm.verifiedNodes, nodeID)
			}
		}

		// Clean stale pending proofs
		for nodeID, startTime := range pm.pendingProofs {
			if now.Sub(startTime) > ChallengeExpiry*2 {
				delete(pm.pendingProofs, nodeID)
			}
		}

		pm.mu.Unlock()
	}
}

// calculateTarget returns the target value for a given difficulty
// Difficulty is number of leading zero bits required
func calculateTarget(difficulty int) []byte {
	target := make([]byte, 32)
	
	// Set all bits to 1
	for i := range target {
		target[i] = 0xFF
	}

	// Clear leading bits based on difficulty
	fullBytes := difficulty / 8
	remainingBits := difficulty % 8

	for i := 0; i < fullBytes && i < 32; i++ {
		target[i] = 0x00
	}

	if fullBytes < 32 && remainingBits > 0 {
		target[fullBytes] = 0xFF >> remainingBits
	}

	return target
}

// meetsTarget checks if hash is less than or equal to target
func meetsTarget(hash, target []byte) bool {
	for i := 0; i < len(hash) && i < len(target); i++ {
		if hash[i] < target[i] {
			return true
		}
		if hash[i] > target[i] {
			return false
		}
	}
	return true
}

// EstimateSolveTime estimates time to solve at given difficulty
func EstimateSolveTime(difficulty int) time.Duration {
	// Average hashes needed = 2^difficulty
	// Assume 1 million hashes per second on average CPU
	hashesNeeded := uint64(1) << difficulty
	hashesPerSecond := uint64(1000000)
	seconds := hashesNeeded / hashesPerSecond
	return time.Duration(seconds) * time.Second
}

// AdaptiveDifficulty adjusts difficulty based on network conditions
type AdaptiveDifficulty struct {
	mu              sync.Mutex
	baseDifficulty  int
	currentDifficulty int
	recentSolveTimes []time.Duration
	targetSolveTime time.Duration
	maxSamples      int
}

// NewAdaptiveDifficulty creates adaptive difficulty manager
func NewAdaptiveDifficulty(baseDifficulty int, targetSolveTime time.Duration) *AdaptiveDifficulty {
	return &AdaptiveDifficulty{
		baseDifficulty:    baseDifficulty,
		currentDifficulty: baseDifficulty,
		targetSolveTime:   targetSolveTime,
		maxSamples:        100,
		recentSolveTimes:  make([]time.Duration, 0),
	}
}

// RecordSolveTime records how long a proof took to solve
func (ad *AdaptiveDifficulty) RecordSolveTime(duration time.Duration) {
	ad.mu.Lock()
	defer ad.mu.Unlock()

	ad.recentSolveTimes = append(ad.recentSolveTimes, duration)
	if len(ad.recentSolveTimes) > ad.maxSamples {
		ad.recentSolveTimes = ad.recentSolveTimes[1:]
	}

	// Adjust difficulty if we have enough samples
	if len(ad.recentSolveTimes) >= 10 {
		ad.adjust()
	}
}

// adjust changes difficulty based on solve times
func (ad *AdaptiveDifficulty) adjust() {
	// Calculate average solve time
	var total time.Duration
	for _, t := range ad.recentSolveTimes {
		total += t
	}
	avg := total / time.Duration(len(ad.recentSolveTimes))

	// Adjust difficulty
	if avg < ad.targetSolveTime/2 && ad.currentDifficulty < MaxDifficulty {
		ad.currentDifficulty++
	} else if avg > ad.targetSolveTime*2 && ad.currentDifficulty > ad.baseDifficulty {
		ad.currentDifficulty--
	}
}

// GetDifficulty returns current adaptive difficulty
func (ad *AdaptiveDifficulty) GetDifficulty() int {
	ad.mu.Lock()
	defer ad.mu.Unlock()
	return ad.currentDifficulty
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ProofOfWorkChallenge for network protocol
type PoWChallengeMessage struct {
	Type       string `json:"type"` // "pow_challenge"
	ChallengeID string `json:"challenge_id"`
	Data       []byte `json:"data"`
	Difficulty int    `json:"difficulty"`
	ExpiresAt  int64  `json:"expires_at"`
}

// ProofOfWorkResponse for network protocol
type PoWResponseMessage struct {
	Type        string `json:"type"` // "pow_response"
	ChallengeID string `json:"challenge_id"`
	NodeID      string `json:"node_id"`
	Nonce       uint64 `json:"nonce"`
	Hash        []byte `json:"hash"`
	Signature   []byte `json:"signature"`
}

// String returns human-readable difficulty info
func (pm *PoWManager) String() string {
	return fmt.Sprintf("PoW[difficulty=%d, verified=%d, pending=%d]",
		pm.difficulty, len(pm.verifiedNodes), len(pm.pendingProofs))
}

