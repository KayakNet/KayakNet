// Package mix implements mix network defenses against global passive adversaries
// Techniques: constant-rate padding, random delays, dummy traffic, batch mixing
package mix

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"time"
)

const (
	// PaddingSize is the fixed size of all messages (padded or truncated)
	PaddingSize = 4096

	// BatchInterval is how often batches are released
	BatchInterval = 2 * time.Second

	// MinBatchSize is minimum messages before release
	MinBatchSize = 3

	// MaxDelay is maximum random delay added to messages
	MaxDelay = 5 * time.Second

	// DummyRate is dummy messages per second per peer
	DummyRate = 0.5
)

// Mixer handles traffic analysis resistance
type Mixer struct {
	mu            sync.Mutex
	incoming      []*MixMessage
	outgoing      chan *MixMessage
	sendFunc      func(dest string, data []byte) error
	running       bool
	stopCh        chan struct{}
	peers         map[string]bool
	lastDummy     map[string]time.Time
}

// MixMessage is a message in the mix
type MixMessage struct {
	Destination string
	Data        []byte
	ReceivedAt  time.Time
	ReleaseAt   time.Time
	IsDummy     bool
}

// NewMixer creates a new mixer
func NewMixer(sendFunc func(dest string, data []byte) error) *Mixer {
	return &Mixer{
		incoming:  make([]*MixMessage, 0),
		outgoing:  make(chan *MixMessage, 1000),
		sendFunc:  sendFunc,
		stopCh:    make(chan struct{}),
		peers:     make(map[string]bool),
		lastDummy: make(map[string]time.Time),
	}
}

// Start starts the mixer
func (m *Mixer) Start() {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return
	}
	m.running = true
	m.mu.Unlock()

	go m.batchProcessor()
	go m.dummyGenerator()
	go m.sender()
}

// Stop stops the mixer
func (m *Mixer) Stop() {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return
	}
	m.running = false
	m.mu.Unlock()
	close(m.stopCh)
}

// AddPeer registers a peer for dummy traffic
func (m *Mixer) AddPeer(peerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.peers[peerID] = true
	m.lastDummy[peerID] = time.Now()
}

// RemovePeer removes a peer
func (m *Mixer) RemovePeer(peerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.peers, peerID)
	delete(m.lastDummy, peerID)
}

// QueueMessage queues a real message for mixing
func (m *Mixer) QueueMessage(dest string, data []byte) {
	// Pad to fixed size
	padded := PadMessage(data)

	// Add random delay
	delay := randomDelay(MaxDelay)

	msg := &MixMessage{
		Destination: dest,
		Data:        padded,
		ReceivedAt:  time.Now(),
		ReleaseAt:   time.Now().Add(delay),
		IsDummy:     false,
	}

	m.mu.Lock()
	m.incoming = append(m.incoming, msg)
	m.mu.Unlock()
}

// batchProcessor collects and releases message batches
func (m *Mixer) batchProcessor() {
	ticker := time.NewTicker(BatchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.processBatch()
		}
	}
}

// processBatch releases a batch of messages
func (m *Mixer) processBatch() {
	m.mu.Lock()

	now := time.Now()
	var ready []*MixMessage
	var remaining []*MixMessage

	for _, msg := range m.incoming {
		if now.After(msg.ReleaseAt) {
			ready = append(ready, msg)
		} else {
			remaining = append(remaining, msg)
		}
	}
	m.incoming = remaining
	m.mu.Unlock()

	if len(ready) < MinBatchSize {
		// Not enough messages - add dummies to batch
		for i := len(ready); i < MinBatchSize; i++ {
			// Pick random peer for dummy
			m.mu.Lock()
			var randomPeer string
			for p := range m.peers {
				randomPeer = p
				break
			}
			m.mu.Unlock()

			if randomPeer != "" {
				ready = append(ready, &MixMessage{
					Destination: randomPeer,
					Data:        GenerateDummy(),
					IsDummy:     true,
				})
			}
		}
	}

	// Shuffle the batch
	shuffleMessages(ready)

	// Release all at once
	for _, msg := range ready {
		select {
		case m.outgoing <- msg:
		default:
			// Channel full, drop
		}
	}
}

// dummyGenerator sends constant-rate dummy traffic
func (m *Mixer) dummyGenerator() {
	interval := time.Duration(float64(time.Second) / DummyRate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.sendDummies()
		}
	}
}

// sendDummies sends dummy messages to all peers
func (m *Mixer) sendDummies() {
	m.mu.Lock()
	peers := make([]string, 0, len(m.peers))
	for p := range m.peers {
		peers = append(peers, p)
	}
	m.mu.Unlock()

	for _, peer := range peers {
		msg := &MixMessage{
			Destination: peer,
			Data:        GenerateDummy(),
			IsDummy:     true,
		}
		select {
		case m.outgoing <- msg:
		default:
		}
	}
}

// sender sends messages from the outgoing queue
func (m *Mixer) sender() {
	for {
		select {
		case <-m.stopCh:
			return
		case msg := <-m.outgoing:
			// Add small random jitter even to batch sends
			jitter := randomDelay(100 * time.Millisecond)
			time.Sleep(jitter)

			m.sendFunc(msg.Destination, msg.Data)
		}
	}
}

// PadMessage pads a message to fixed size
func PadMessage(data []byte) []byte {
	if len(data) >= PaddingSize {
		return data[:PaddingSize]
	}

	padded := make([]byte, PaddingSize)
	// First 2 bytes: actual data length
	binary.BigEndian.PutUint16(padded[:2], uint16(len(data)))
	// Copy data
	copy(padded[2:], data)
	// Rest is random padding
	rand.Read(padded[2+len(data):])

	return padded
}

// UnpadMessage removes padding from a message
func UnpadMessage(data []byte) []byte {
	if len(data) < 2 {
		return data
	}

	length := binary.BigEndian.Uint16(data[:2])
	if int(length) > len(data)-2 {
		return data
	}

	return data[2 : 2+length]
}

// GenerateDummy creates a dummy message (indistinguishable from real)
func GenerateDummy() []byte {
	dummy := make([]byte, PaddingSize)
	// Mark as dummy (will be ignored by receiver)
	// First byte 0xFF indicates dummy
	dummy[0] = 0xFF
	dummy[1] = 0xFF
	rand.Read(dummy[2:])
	return dummy
}

// IsDummy checks if a message is a dummy
func IsDummy(data []byte) bool {
	if len(data) < 2 {
		return false
	}
	return data[0] == 0xFF && data[1] == 0xFF
}

// randomDelay generates a random delay up to max
func randomDelay(max time.Duration) time.Duration {
	b := make([]byte, 8)
	rand.Read(b)
	n := binary.BigEndian.Uint64(b)
	return time.Duration(n % uint64(max))
}

// shuffleMessages randomly shuffles a slice of messages
func shuffleMessages(msgs []*MixMessage) {
	for i := len(msgs) - 1; i > 0; i-- {
		b := make([]byte, 1)
		rand.Read(b)
		j := int(b[0]) % (i + 1)
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
}

// LoopbackDefense prevents traffic analysis via loopback
// By sending cover traffic through the network back to yourself
type LoopbackDefense struct {
	mixer    *Mixer
	selfID   string
	interval time.Duration
	stopCh   chan struct{}
}

// NewLoopbackDefense creates loopback defense
func NewLoopbackDefense(mixer *Mixer, selfID string) *LoopbackDefense {
	return &LoopbackDefense{
		mixer:    mixer,
		selfID:   selfID,
		interval: 5 * time.Second,
		stopCh:   make(chan struct{}),
	}
}

// Start starts sending loopback traffic
func (l *LoopbackDefense) Start() {
	go func() {
		ticker := time.NewTicker(l.interval)
		defer ticker.Stop()

		for {
			select {
			case <-l.stopCh:
				return
			case <-ticker.C:
				// Send a message to self through the network
				// This creates cover traffic
				l.mixer.QueueMessage(l.selfID, GenerateDummy())
			}
		}
	}()
}

// Stop stops loopback defense
func (l *LoopbackDefense) Stop() {
	close(l.stopCh)
}

