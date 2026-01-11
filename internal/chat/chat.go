// Package chat implements persistent chat rooms accessible only through KayakNet
// No web interface, no clearnet - all chat stays inside the network
package chat

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

const (
	// MaxMessageLength is the maximum chat message length
	MaxMessageLength = 4000
	// MaxRoomNameLength is the maximum room name length
	MaxRoomNameLength = 64
	// MaxMessagesPerRoom is how many messages to keep per room
	MaxMessagesPerRoom = 1000
	// MessageTTL is how long messages are kept
	MessageTTL = 24 * time.Hour
)

// Message represents a chat message
type Message struct {
	ID        string    `json:"id"`
	Room      string    `json:"room"`
	SenderID  string    `json:"sender_id"`  // Node ID (anonymous)
	SenderKey []byte    `json:"sender_key"` // For verification
	Nick      string    `json:"nick"`       // Display name
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
	Signature []byte    `json:"signature"`
	
	// Optional
	ReplyTo   string    `json:"reply_to,omitempty"` // Reply to message ID
	Edited    bool      `json:"edited,omitempty"`
}

// Room represents a chat room
type Room struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	CreatorID   string    `json:"creator_id"`
	Private     bool      `json:"private"`     // If true, invite-only
	Members     []string  `json:"members"`     // For private rooms
	Moderators  []string  `json:"moderators"`
	
	// Runtime
	messages    []*Message
	mu          sync.RWMutex
}

// ChatManager manages chat rooms and messages
type ChatManager struct {
	mu        sync.RWMutex
	rooms     map[string]*Room
	localID   string
	localKey  ed25519.PublicKey
	localNick string
	signFunc  func([]byte) []byte
	
	// Callbacks
	onMessage func(*Message)
}

// NewChatManager creates a new chat manager
func NewChatManager(localID string, pubKey ed25519.PublicKey, nick string, signFunc func([]byte) []byte) *ChatManager {
	cm := &ChatManager{
		rooms:     make(map[string]*Room),
		localID:   localID,
		localKey:  pubKey,
		localNick: nick,
		signFunc:  signFunc,
	}
	
	// Create default public rooms
	cm.CreateRoom("general", "General discussion", false)
	cm.CreateRoom("market", "Marketplace discussion", false)
	cm.CreateRoom("help", "Help and support", false)
	
	return cm
}

// SetNick sets the local nickname
func (cm *ChatManager) SetNick(nick string) {
	cm.mu.Lock()
	cm.localNick = nick
	cm.mu.Unlock()
}

// OnMessage sets the callback for new messages
func (cm *ChatManager) OnMessage(callback func(*Message)) {
	cm.mu.Lock()
	cm.onMessage = callback
	cm.mu.Unlock()
}

// CreateRoom creates a new room
func (cm *ChatManager) CreateRoom(name, description string, private bool) *Room {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	
	if _, exists := cm.rooms[name]; exists {
		return cm.rooms[name]
	}
	
	room := &Room{
		Name:        name,
		Description: description,
		CreatedAt:   time.Now(),
		CreatorID:   cm.localID,
		Private:     private,
		Members:     []string{cm.localID},
		Moderators:  []string{cm.localID},
		messages:    make([]*Message, 0),
	}
	
	cm.rooms[name] = room
	return room
}

// GetRoom returns a room by name
func (cm *ChatManager) GetRoom(name string) *Room {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.rooms[name]
}

// ListRooms returns all rooms
func (cm *ChatManager) ListRooms() []*Room {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	
	rooms := make([]*Room, 0, len(cm.rooms))
	for _, room := range cm.rooms {
		// Skip private rooms we're not a member of
		if room.Private && !cm.isMember(room) {
			continue
		}
		rooms = append(rooms, room)
	}
	return rooms
}

// SendMessage sends a message to a room
func (cm *ChatManager) SendMessage(roomName, content string) (*Message, error) {
	cm.mu.RLock()
	room, exists := cm.rooms[roomName]
	nick := cm.localNick
	cm.mu.RUnlock()
	
	if !exists {
		// Create room if doesn't exist
		room = cm.CreateRoom(roomName, "", false)
	}
	
	if room.Private && !cm.isMember(room) {
		return nil, ErrNotMember
	}
	
	if len(content) > MaxMessageLength {
		content = content[:MaxMessageLength]
	}
	
	msg := &Message{
		ID:        generateMsgID(),
		Room:      roomName,
		SenderID:  cm.localID,
		SenderKey: cm.localKey,
		Nick:      nick,
		Content:   content,
		Timestamp: time.Now(),
	}
	
	// Sign the message
	msg.Signature = cm.signMessage(msg)
	
	// Add to room
	room.mu.Lock()
	room.messages = append(room.messages, msg)
	if len(room.messages) > MaxMessagesPerRoom {
		room.messages = room.messages[1:]
	}
	room.mu.Unlock()
	
	return msg, nil
}

// ReceiveMessage receives a message from the network
func (cm *ChatManager) ReceiveMessage(msg *Message) error {
	// Verify signature
	if !cm.verifyMessage(msg) {
		return ErrInvalidSignature
	}
	
	cm.mu.Lock()
	room, exists := cm.rooms[msg.Room]
	if !exists {
		// Create room for incoming message
		room = &Room{
			Name:      msg.Room,
			CreatedAt: time.Now(),
			messages:  make([]*Message, 0),
		}
		cm.rooms[msg.Room] = room
	}
	callback := cm.onMessage
	cm.mu.Unlock()
	
	// Check if message already exists (dedup)
	room.mu.Lock()
	for _, m := range room.messages {
		if m.ID == msg.ID {
			room.mu.Unlock()
			return nil // Already have it
		}
	}
	
	room.messages = append(room.messages, msg)
	if len(room.messages) > MaxMessagesPerRoom {
		room.messages = room.messages[1:]
	}
	room.mu.Unlock()
	
	// Trigger callback
	if callback != nil {
		callback(msg)
	}
	
	return nil
}

// GetMessages returns messages from a room
func (cm *ChatManager) GetMessages(roomName string, limit int) []*Message {
	cm.mu.RLock()
	room, exists := cm.rooms[roomName]
	cm.mu.RUnlock()
	
	if !exists {
		return nil
	}
	
	room.mu.RLock()
	defer room.mu.RUnlock()
	
	if limit <= 0 || limit > len(room.messages) {
		limit = len(room.messages)
	}
	
	start := len(room.messages) - limit
	if start < 0 {
		start = 0
	}
	
	return room.messages[start:]
}

// JoinRoom joins a room (for membership tracking)
func (cm *ChatManager) JoinRoom(roomName string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	
	if room, exists := cm.rooms[roomName]; exists {
		room.mu.Lock()
		for _, m := range room.Members {
			if m == cm.localID {
				room.mu.Unlock()
				return
			}
		}
		room.Members = append(room.Members, cm.localID)
		room.mu.Unlock()
	}
}

// LeaveRoom leaves a room
func (cm *ChatManager) LeaveRoom(roomName string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	
	if room, exists := cm.rooms[roomName]; exists {
		room.mu.Lock()
		for i, m := range room.Members {
			if m == cm.localID {
				room.Members = append(room.Members[:i], room.Members[i+1:]...)
				break
			}
		}
		room.mu.Unlock()
	}
}

// Marshal serializes a message for network transmission
func (m *Message) Marshal() ([]byte, error) {
	return json.Marshal(m)
}

// UnmarshalMessage deserializes a message
func UnmarshalMessage(data []byte) (*Message, error) {
	var m Message
	err := json.Unmarshal(data, &m)
	return &m, err
}

// Helper functions

func (cm *ChatManager) signMessage(m *Message) []byte {
	data, _ := json.Marshal(struct {
		ID       string    `json:"id"`
		Room     string    `json:"room"`
		SenderID string    `json:"sender_id"`
		Content  string    `json:"content"`
		Time     time.Time `json:"time"`
	}{m.ID, m.Room, m.SenderID, m.Content, m.Timestamp})
	return cm.signFunc(data)
}

func (cm *ChatManager) verifyMessage(m *Message) bool {
	if len(m.SenderKey) != ed25519.PublicKeySize {
		return false
	}
	data, _ := json.Marshal(struct {
		ID       string    `json:"id"`
		Room     string    `json:"room"`
		SenderID string    `json:"sender_id"`
		Content  string    `json:"content"`
		Time     time.Time `json:"time"`
	}{m.ID, m.Room, m.SenderID, m.Content, m.Timestamp})
	return ed25519.Verify(m.SenderKey, data, m.Signature)
}

func (cm *ChatManager) isMember(room *Room) bool {
	room.mu.RLock()
	defer room.mu.RUnlock()
	for _, m := range room.Members {
		if m == cm.localID {
			return true
		}
	}
	return false
}

func generateMsgID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// CleanOldMessages removes messages older than TTL
func (cm *ChatManager) CleanOldMessages() {
	cm.mu.RLock()
	rooms := make([]*Room, 0, len(cm.rooms))
	for _, r := range cm.rooms {
		rooms = append(rooms, r)
	}
	cm.mu.RUnlock()
	
	cutoff := time.Now().Add(-MessageTTL)
	
	for _, room := range rooms {
		room.mu.Lock()
		var kept []*Message
		for _, msg := range room.messages {
			if msg.Timestamp.After(cutoff) {
				kept = append(kept, msg)
			}
		}
		room.messages = kept
		room.mu.Unlock()
	}
}

// Errors
var (
	ErrNotMember        = &ChatError{"not a member of private room"}
	ErrInvalidSignature = &ChatError{"invalid message signature"}
)

type ChatError struct {
	msg string
}

func (e *ChatError) Error() string {
	return e.msg
}

