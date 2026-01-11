// Package pubsub implements topic-based publish/subscribe for KayakNet
package pubsub

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

// Config configures the PubSub system
type Config struct {
	LocalID            string
	LocalPubKey        []byte
	Signer             func([]byte) []byte
	MaxTopics          int
	MaxPeersPerTopic   int
	MessageBufferSize  int
	ScoreDecayInterval time.Duration
	RateLimitWindow    time.Duration
	RateLimitMessages  int
}

// Message represents a pub/sub message
type Message struct {
	ID        string    `json:"id"`
	Topic     string    `json:"topic"`
	From      string    `json:"from"`
	FromKey   []byte    `json:"from_key"`
	Data      []byte    `json:"data"`
	Timestamp time.Time `json:"timestamp"`
	Signature []byte    `json:"signature"`
}

// Topic represents a pub/sub topic
type Topic struct {
	mu          sync.RWMutex
	name        string
	subscribers map[string]*Subscriber
	messages    chan *Message
	config      *Config
}

// Subscriber represents a topic subscriber
type Subscriber struct {
	NodeID    string
	PubKey    []byte
	Score     float64
	JoinedAt  time.Time
	LastSeen  time.Time
	MsgCount  int
	LastMsgAt time.Time
}

// PubSub manages topics and message delivery
type PubSub struct {
	mu       sync.RWMutex
	config   Config
	topics   map[string]*Topic
	handlers map[string][]MessageHandler
	closed   bool
}

// MessageHandler handles incoming messages
type MessageHandler func(*Message)

// NewPubSub creates a new PubSub instance
func NewPubSub(config Config) *PubSub {
	if config.MaxTopics == 0 {
		config.MaxTopics = 100
	}
	if config.MaxPeersPerTopic == 0 {
		config.MaxPeersPerTopic = 50
	}
	if config.MessageBufferSize == 0 {
		config.MessageBufferSize = 1000
	}
	if config.ScoreDecayInterval == 0 {
		config.ScoreDecayInterval = time.Minute
	}
	if config.RateLimitWindow == 0 {
		config.RateLimitWindow = time.Second
	}
	if config.RateLimitMessages == 0 {
		config.RateLimitMessages = 10
	}

	ps := &PubSub{
		config:   config,
		topics:   make(map[string]*Topic),
		handlers: make(map[string][]MessageHandler),
	}

	// Start score decay routine
	go ps.scoreDecayRoutine()

	return ps
}

// Subscribe subscribes to a topic
func (ps *PubSub) Subscribe(topicName string, handler MessageHandler) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if ps.closed {
		return nil
	}

	// Create topic if doesn't exist
	topic, ok := ps.topics[topicName]
	if !ok {
		if len(ps.topics) >= ps.config.MaxTopics {
			return nil // Silently ignore, too many topics
		}
		topic = &Topic{
			name:        topicName,
			subscribers: make(map[string]*Subscriber),
			messages:    make(chan *Message, ps.config.MessageBufferSize),
			config:      &ps.config,
		}
		ps.topics[topicName] = topic

		// Start topic message handler
		go ps.handleTopicMessages(topic)
	}

	// Add self as subscriber
	topic.mu.Lock()
	topic.subscribers[ps.config.LocalID] = &Subscriber{
		NodeID:   ps.config.LocalID,
		PubKey:   ps.config.LocalPubKey,
		Score:    0,
		JoinedAt: time.Now(),
		LastSeen: time.Now(),
	}
	topic.mu.Unlock()

	// Register handler
	ps.handlers[topicName] = append(ps.handlers[topicName], handler)

	return nil
}

// Unsubscribe unsubscribes from a topic
func (ps *PubSub) Unsubscribe(topicName string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if topic, ok := ps.topics[topicName]; ok {
		topic.mu.Lock()
		delete(topic.subscribers, ps.config.LocalID)
		topic.mu.Unlock()
	}
	delete(ps.handlers, topicName)
}

// Publish publishes a message to a topic
func (ps *PubSub) Publish(topicName string, data []byte) error {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	if ps.closed {
		return nil
	}

	topic, ok := ps.topics[topicName]
	if !ok {
		return nil // Not subscribed to topic
	}

	msg := &Message{
		ID:        generateMessageID(),
		Topic:     topicName,
		From:      ps.config.LocalID,
		FromKey:   ps.config.LocalPubKey,
		Data:      data,
		Timestamp: time.Now(),
	}

	// Sign the message
	toSign, _ := json.Marshal(struct {
		ID    string    `json:"id"`
		Topic string    `json:"topic"`
		From  string    `json:"from"`
		Data  []byte    `json:"data"`
		Ts    time.Time `json:"ts"`
	}{msg.ID, msg.Topic, msg.From, msg.Data, msg.Timestamp})
	msg.Signature = ps.config.Signer(toSign)

	// Send to topic channel
	select {
	case topic.messages <- msg:
	default:
		// Buffer full, drop message
	}

	return nil
}

// DeliverMessage delivers an incoming message from the network
func (ps *PubSub) DeliverMessage(msg *Message) bool {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	if ps.closed {
		return false
	}

	topic, ok := ps.topics[msg.Topic]
	if !ok {
		return false
	}

	// Verify signature
	if !verifyMessage(msg) {
		return false
	}

	// Check rate limit
	topic.mu.Lock()
	sub, ok := topic.subscribers[msg.From]
	if ok {
		if !ps.checkRateLimit(sub) {
			sub.Score -= 1 // Penalize rate limit violation
			topic.mu.Unlock()
			return false
		}
		sub.MsgCount++
		sub.LastMsgAt = time.Now()
		sub.Score += 0.1 // Reward valid messages
	}
	topic.mu.Unlock()

	// Send to topic channel
	select {
	case topic.messages <- msg:
		return true
	default:
		return false
	}
}

// AddPeer adds a peer to a topic
func (ps *PubSub) AddPeer(topicName, nodeID string, pubKey []byte) {
	ps.mu.RLock()
	topic, ok := ps.topics[topicName]
	ps.mu.RUnlock()
	if !ok {
		return
	}

	topic.mu.Lock()
	defer topic.mu.Unlock()

	if len(topic.subscribers) >= ps.config.MaxPeersPerTopic {
		return
	}

	if _, exists := topic.subscribers[nodeID]; !exists {
		topic.subscribers[nodeID] = &Subscriber{
			NodeID:   nodeID,
			PubKey:   pubKey,
			Score:    0,
			JoinedAt: time.Now(),
			LastSeen: time.Now(),
		}
	}
}

// RemovePeer removes a peer from a topic
func (ps *PubSub) RemovePeer(topicName, nodeID string) {
	ps.mu.RLock()
	topic, ok := ps.topics[topicName]
	ps.mu.RUnlock()
	if !ok {
		return
	}

	topic.mu.Lock()
	delete(topic.subscribers, nodeID)
	topic.mu.Unlock()
}

// GetPeers returns all peers for a topic
func (ps *PubSub) GetPeers(topicName string) []string {
	ps.mu.RLock()
	topic, ok := ps.topics[topicName]
	ps.mu.RUnlock()
	if !ok {
		return nil
	}

	topic.mu.RLock()
	defer topic.mu.RUnlock()

	peers := make([]string, 0, len(topic.subscribers))
	for id := range topic.subscribers {
		if id != ps.config.LocalID {
			peers = append(peers, id)
		}
	}
	return peers
}

// GetTopics returns all subscribed topics
func (ps *PubSub) GetTopics() []string {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	topics := make([]string, 0, len(ps.topics))
	for name := range ps.topics {
		topics = append(topics, name)
	}
	return topics
}

// Close shuts down the PubSub system
func (ps *PubSub) Close() {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	ps.closed = true
	for _, topic := range ps.topics {
		close(topic.messages)
	}
}

// handleTopicMessages processes messages for a topic
func (ps *PubSub) handleTopicMessages(topic *Topic) {
	for msg := range topic.messages {
		ps.mu.RLock()
		handlers := ps.handlers[topic.name]
		ps.mu.RUnlock()

		for _, handler := range handlers {
			handler(msg)
		}
	}
}

// checkRateLimit checks if a subscriber is within rate limits
func (ps *PubSub) checkRateLimit(sub *Subscriber) bool {
	if time.Since(sub.LastMsgAt) > ps.config.RateLimitWindow {
		sub.MsgCount = 0
	}
	return sub.MsgCount < ps.config.RateLimitMessages
}

// scoreDecayRoutine periodically decays peer scores
func (ps *PubSub) scoreDecayRoutine() {
	ticker := time.NewTicker(ps.config.ScoreDecayInterval)
	defer ticker.Stop()

	for range ticker.C {
		ps.mu.RLock()
		if ps.closed {
			ps.mu.RUnlock()
			return
		}
		topics := make([]*Topic, 0, len(ps.topics))
		for _, t := range ps.topics {
			topics = append(topics, t)
		}
		ps.mu.RUnlock()

		for _, topic := range topics {
			topic.mu.Lock()
			for _, sub := range topic.subscribers {
				sub.Score *= 0.9 // Decay by 10%
			}
			topic.mu.Unlock()
		}
	}
}

// verifyMessage verifies a message signature
func verifyMessage(msg *Message) bool {
	if len(msg.FromKey) != ed25519.PublicKeySize {
		return false
	}

	toSign, _ := json.Marshal(struct {
		ID    string    `json:"id"`
		Topic string    `json:"topic"`
		From  string    `json:"from"`
		Data  []byte    `json:"data"`
		Ts    time.Time `json:"ts"`
	}{msg.ID, msg.Topic, msg.From, msg.Data, msg.Timestamp})

	return ed25519.Verify(msg.FromKey, toSign, msg.Signature)
}

// generateMessageID creates a unique message ID
func generateMessageID() string {
	b := make([]byte, 16)
	for i := range b {
		b[i] = byte(time.Now().UnixNano() >> (i * 8))
	}
	return hex.EncodeToString(b)
}
