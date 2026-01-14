// Package chat implements extensive P2P chat with user discovery, DMs, and media sharing
// All chat is anonymous and encrypted through KayakNet's onion routing
package chat

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// MaxMessageLength is the maximum chat message length
	MaxMessageLength = 8000
	// MaxRoomNameLength is the maximum room name length
	MaxRoomNameLength = 64
	// MaxMessagesPerRoom is how many messages to keep per room
	MaxMessagesPerRoom = 500
	// MessageTTL is how long messages are kept
	MessageTTL = 72 * time.Hour
	// MaxMediaSize is max media attachment size (1MB base64)
	MaxMediaSize = 1024 * 1024
	// UserPresenceTTL is how long user presence is valid
	UserPresenceTTL = 5 * time.Minute
)

// MessageType represents different message types
type MessageType int

const (
	MsgTypeText MessageType = iota
	MsgTypeImage
	MsgTypeFile
	MsgTypeEmoji
	MsgTypeSystem
	MsgTypePresence
	MsgTypeDM
)

// Message represents a chat message
type Message struct {
	ID        string      `json:"id"`
	Type      MessageType `json:"type"`
	Room      string      `json:"room"`
	SenderID  string      `json:"sender_id"`
	SenderKey []byte      `json:"sender_key"`
	Nick      string      `json:"nick"`
	Content   string      `json:"content"`
	Timestamp time.Time   `json:"timestamp"`
	Signature []byte      `json:"signature"`

	// Optional fields
	ReplyTo    string `json:"reply_to,omitempty"`
	Edited     bool   `json:"edited,omitempty"`
	EditedAt   int64  `json:"edited_at,omitempty"`
	ReceiverID string `json:"receiver_id,omitempty"` // For DMs

	// Media attachment
	Media *MediaAttachment `json:"media,omitempty"`

	// Reactions
	Reactions map[string][]string `json:"reactions,omitempty"` // emoji -> list of user IDs
}

// MediaAttachment represents an attached file or image
type MediaAttachment struct {
	Type     string `json:"type"`      // "image/png", "image/jpeg", "application/pdf", etc.
	Name     string `json:"name"`      // Original filename
	Size     int64  `json:"size"`      // Size in bytes
	Data     string `json:"data"`      // Base64 encoded data
	Checksum string `json:"checksum"`  // SHA256 hash
	Preview  string `json:"preview"`   // Base64 thumbnail for images
}

// User represents a chat user profile
type User struct {
	ID          string    `json:"id"`
	PublicKey   []byte    `json:"public_key"`
	Nick        string    `json:"nick"`
	Status      string    `json:"status"`       // "online", "away", "busy", "offline"
	StatusMsg   string    `json:"status_msg"`   // Custom status message
	Avatar      string    `json:"avatar"`       // Base64 encoded avatar image
	Bio         string    `json:"bio"`          // User bio
	JoinedAt    time.Time `json:"joined_at"`
	LastSeen    time.Time `json:"last_seen"`
	MessagesSent int64    `json:"messages_sent"`
	
	// Privacy settings
	AllowDMs    bool     `json:"allow_dms"`
	BlockedIDs  []string `json:"blocked_ids,omitempty"`
}

// Room represents a chat room
type Room struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Topic       string    `json:"topic"`
	CreatedAt   time.Time `json:"created_at"`
	CreatorID   string    `json:"creator_id"`
	Private     bool      `json:"private"`
	Members     []string  `json:"members"`
	Moderators  []string  `json:"moderators"`
	Pinned      []string  `json:"pinned"` // Pinned message IDs
	Icon        string    `json:"icon"`   // Room icon emoji or image

	// Runtime
	messages     []*Message
	onlineUsers  map[string]time.Time // userID -> last activity
	typingUsers  map[string]time.Time // userID -> typing started
	mu           sync.RWMutex
}

// DirectConversation represents a DM thread between two users
type DirectConversation struct {
	ID           string     `json:"id"`
	Participants []string   `json:"participants"` // Two user IDs
	CreatedAt    time.Time  `json:"created_at"`
	LastMessage  time.Time  `json:"last_message"`
	Messages     []*Message `json:"messages"`
	Unread       int        `json:"unread"`
	mu           sync.RWMutex
}

// ChatManager manages chat rooms, users, and messages
type ChatManager struct {
	mu            sync.RWMutex
	rooms         map[string]*Room
	users         map[string]*User              // Known users
	conversations map[string]*DirectConversation // DM conversations
	localID       string
	localKey      ed25519.PublicKey
	localNick     string
	localUser     *User
	signFunc      func([]byte) []byte
	dataDir       string                        // Directory for persistent storage

	// Callbacks
	onMessage     func(*Message)
	onUserJoin    func(*User, string) // user, room
	onUserLeave   func(*User, string)
	onPresence    func(*User)
	onDM          func(*Message)
}

// NewChatManager creates a new chat manager
func NewChatManager(localID string, pubKey ed25519.PublicKey, nick string, signFunc func([]byte) []byte) *ChatManager {
	cm := &ChatManager{
		rooms:         make(map[string]*Room),
		users:         make(map[string]*User),
		conversations: make(map[string]*DirectConversation),
		localID:       localID,
		localKey:      pubKey,
		localNick:     nick,
		signFunc:      signFunc,
	}

	// Create local user profile
	cm.localUser = &User{
		ID:        localID,
		PublicKey: pubKey,
		Nick:      nick,
		Status:    "online",
		JoinedAt:  time.Now(),
		LastSeen:  time.Now(),
		AllowDMs:  true,
	}
	cm.users[localID] = cm.localUser

	// Create default public rooms
	cm.CreateRoom("general", "General discussion - welcome to KayakNet!", false)
	cm.CreateRoom("market", "Marketplace discussion and deals", false)
	cm.CreateRoom("help", "Help and support for KayakNet", false)
	cm.CreateRoom("random", "Random chat and off-topic", false)
	cm.CreateRoom("tech", "Technology and development", false)
	cm.CreateRoom("privacy", "Privacy and security discussion", false)

	return cm
}

// SetDataDir sets the data directory for persistence
func (cm *ChatManager) SetDataDir(dataDir string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	
	cm.dataDir = filepath.Join(dataDir, "chat")
	if err := os.MkdirAll(cm.dataDir, 0700); err != nil {
		return err
	}
	
	// Load existing data
	cm.loadData()
	return nil
}

// ChatData holds all chat data for persistence
type ChatData struct {
	Rooms         map[string]*Room               `json:"rooms"`
	Users         map[string]*User               `json:"users"`
	Conversations map[string]*DirectConversation `json:"conversations"`
	LocalNick     string                         `json:"local_nick"`
}

// Save persists chat data to disk
func (cm *ChatManager) Save() error {
	if cm.dataDir == "" {
		return nil
	}
	
	cm.mu.RLock()
	data := ChatData{
		Rooms:         cm.rooms,
		Users:         cm.users,
		Conversations: cm.conversations,
		LocalNick:     cm.localNick,
	}
	cm.mu.RUnlock()
	
	path := filepath.Join(cm.dataDir, "chat.json")
	tmpPath := path + ".tmp"
	
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return err
	}
	
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	
	return os.Rename(tmpPath, path)
}

// loadData loads chat data from disk
func (cm *ChatManager) loadData() {
	if cm.dataDir == "" {
		return
	}
	
	path := filepath.Join(cm.dataDir, "chat.json")
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	
	var data ChatData
	if err := json.NewDecoder(f).Decode(&data); err != nil {
		return
	}
	
	if data.Rooms != nil {
		cm.rooms = data.Rooms
	}
	if data.Users != nil {
		cm.users = data.Users
	}
	if data.Conversations != nil {
		cm.conversations = data.Conversations
	}
	if data.LocalNick != "" {
		cm.localNick = data.LocalNick
	}
}

// SetNick sets the local nickname
func (cm *ChatManager) SetNick(nick string) {
	cm.mu.Lock()
	cm.localNick = nick
	if cm.localUser != nil {
		cm.localUser.Nick = nick
	}
	cm.mu.Unlock()
}

// GetLocalUser returns the local user profile
func (cm *ChatManager) GetLocalUser() *User {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.localUser
}

// UpdateProfile updates the local user profile
func (cm *ChatManager) UpdateProfile(nick, status, statusMsg, bio, avatar string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if nick != "" {
		cm.localNick = nick
		cm.localUser.Nick = nick
	}
	if status != "" {
		cm.localUser.Status = status
	}
	cm.localUser.StatusMsg = statusMsg
	cm.localUser.Bio = bio
	if avatar != "" {
		cm.localUser.Avatar = avatar
	}
	cm.localUser.LastSeen = time.Now()
}

// OnMessage sets the callback for new messages
func (cm *ChatManager) OnMessage(callback func(*Message)) {
	cm.mu.Lock()
	cm.onMessage = callback
	cm.mu.Unlock()
}

// OnDM sets the callback for direct messages
func (cm *ChatManager) OnDM(callback func(*Message)) {
	cm.mu.Lock()
	cm.onDM = callback
	cm.mu.Unlock()
}

// OnPresence sets the callback for user presence updates
func (cm *ChatManager) OnPresence(callback func(*User)) {
	cm.mu.Lock()
	cm.onPresence = callback
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
		onlineUsers: make(map[string]time.Time),
		typingUsers: make(map[string]time.Time),
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

// ListRooms returns all accessible rooms
func (cm *ChatManager) ListRooms() []*Room {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	rooms := make([]*Room, 0, len(cm.rooms))
	for _, room := range cm.rooms {
		if room.Private && !cm.isMember(room) {
			continue
		}
		rooms = append(rooms, room)
	}
	return rooms
}

// SendMessage sends a message to a room
func (cm *ChatManager) SendMessage(roomName, content string) (*Message, error) {
	return cm.SendMessageWithMedia(roomName, content, nil)
}

// SendMessageWithMedia sends a message with optional media attachment
func (cm *ChatManager) SendMessageWithMedia(roomName, content string, media *MediaAttachment) (*Message, error) {
	cm.mu.RLock()
	room, exists := cm.rooms[roomName]
	nick := cm.localNick
	cm.mu.RUnlock()

	if !exists {
		room = cm.CreateRoom(roomName, "", false)
	}

	if room.Private && !cm.isMember(room) {
		return nil, ErrNotMember
	}

	if len(content) > MaxMessageLength {
		content = content[:MaxMessageLength]
	}

	msgType := MsgTypeText
	if media != nil {
		if strings.HasPrefix(media.Type, "image/") {
			msgType = MsgTypeImage
		} else {
			msgType = MsgTypeFile
		}
	}

	msg := &Message{
		ID:        generateMsgID(),
		Type:      msgType,
		Room:      roomName,
		SenderID:  cm.localID,
		SenderKey: cm.localKey,
		Nick:      nick,
		Content:   content,
		Timestamp: time.Now(),
		Media:     media,
		Reactions: make(map[string][]string),
	}

	msg.Signature = cm.signMessage(msg)

	room.mu.Lock()
	room.messages = append(room.messages, msg)
	if len(room.messages) > MaxMessagesPerRoom {
		room.messages = room.messages[1:]
	}
	room.onlineUsers[cm.localID] = time.Now()
	room.mu.Unlock()

	// Update message count
	cm.mu.Lock()
	cm.localUser.MessagesSent++
	cm.localUser.LastSeen = time.Now()
	cm.mu.Unlock()

	// Auto-save after sending
	go cm.Save()

	return msg, nil
}

// SendDM sends a direct message to another user
func (cm *ChatManager) SendDM(receiverID, content string, media *MediaAttachment) (*Message, error) {
	cm.mu.Lock()
	receiver, exists := cm.users[receiverID]
	nick := cm.localNick
	
	// If user doesn't exist, create a placeholder entry (they may be on another node)
	if !exists {
		receiver = &User{
			ID:       receiverID,
			Nick:     receiverID[:8] + "...",
			Status:   "online",
			AllowDMs: true, // Assume allowed until told otherwise
			JoinedAt: time.Now(),
		}
		cm.users[receiverID] = receiver
	}
	cm.mu.Unlock()

	if !receiver.AllowDMs {
		return nil, ErrDMsDisabled
	}

	// Check if blocked
	for _, blocked := range receiver.BlockedIDs {
		if blocked == cm.localID {
			return nil, ErrBlocked
		}
	}

	msgType := MsgTypeDM
	if media != nil {
		if strings.HasPrefix(media.Type, "image/") {
			msgType = MsgTypeImage
		} else {
			msgType = MsgTypeFile
		}
	}

	msg := &Message{
		ID:         generateMsgID(),
		Type:       msgType,
		SenderID:   cm.localID,
		SenderKey:  cm.localKey,
		Nick:       nick,
		ReceiverID: receiverID,
		Content:    content,
		Timestamp:  time.Now(),
		Media:      media,
	}

	msg.Signature = cm.signMessage(msg)

	// Store in conversation
	convID := cm.getConversationID(cm.localID, receiverID)
	cm.mu.Lock()
	conv, exists := cm.conversations[convID]
	if !exists {
		conv = &DirectConversation{
			ID:           convID,
			Participants: []string{cm.localID, receiverID},
			CreatedAt:    time.Now(),
			Messages:     make([]*Message, 0),
		}
		cm.conversations[convID] = conv
	}
	cm.mu.Unlock()

	conv.mu.Lock()
	conv.Messages = append(conv.Messages, msg)
	conv.LastMessage = time.Now()
	if len(conv.Messages) > MaxMessagesPerRoom {
		conv.Messages = conv.Messages[1:]
	}
	conv.mu.Unlock()

	// Auto-save after sending DM
	go cm.Save()

	return msg, nil
}

// ReceiveMessage receives a message from the network
func (cm *ChatManager) ReceiveMessage(msg *Message) error {
	if !cm.verifyMessage(msg) {
		return ErrInvalidSignature
	}

	// Handle DMs separately
	if msg.Type == MsgTypeDM || msg.ReceiverID != "" {
		return cm.receiveDM(msg)
	}

	cm.mu.Lock()
	room, exists := cm.rooms[msg.Room]
	if !exists {
		room = &Room{
			Name:        msg.Room,
			CreatedAt:   time.Now(),
			messages:    make([]*Message, 0),
			onlineUsers: make(map[string]time.Time),
			typingUsers: make(map[string]time.Time),
		}
		cm.rooms[msg.Room] = room
	}

	// Register sender as known user
	if _, known := cm.users[msg.SenderID]; !known {
		cm.users[msg.SenderID] = &User{
			ID:        msg.SenderID,
			PublicKey: msg.SenderKey,
			Nick:      msg.Nick,
			Status:    "online",
			LastSeen:  msg.Timestamp,
			AllowDMs:  true,
		}
	} else {
		cm.users[msg.SenderID].Nick = msg.Nick
		cm.users[msg.SenderID].LastSeen = msg.Timestamp
	}

	callback := cm.onMessage
	cm.mu.Unlock()

	// Dedup
	room.mu.Lock()
	for _, m := range room.messages {
		if m.ID == msg.ID {
			room.mu.Unlock()
			return nil
		}
	}

	room.messages = append(room.messages, msg)
	if len(room.messages) > MaxMessagesPerRoom {
		room.messages = room.messages[1:]
	}
	room.onlineUsers[msg.SenderID] = time.Now()
	room.mu.Unlock()

	if callback != nil {
		callback(msg)
	}

	// Persist to disk
	go cm.Save()

	return nil
}

// receiveDM handles incoming direct messages
func (cm *ChatManager) receiveDM(msg *Message) error {
	// Only accept if we're the receiver
	if msg.ReceiverID != cm.localID {
		return nil
	}

	// Check if sender is blocked
	cm.mu.RLock()
	for _, blocked := range cm.localUser.BlockedIDs {
		if blocked == msg.SenderID {
			cm.mu.RUnlock()
			return ErrBlocked
		}
	}
	cm.mu.RUnlock()

	convID := cm.getConversationID(cm.localID, msg.SenderID)

	cm.mu.Lock()
	conv, exists := cm.conversations[convID]
	if !exists {
		conv = &DirectConversation{
			ID:           convID,
			Participants: []string{cm.localID, msg.SenderID},
			CreatedAt:    time.Now(),
			Messages:     make([]*Message, 0),
		}
		cm.conversations[convID] = conv
	}

	// Register sender
	if _, known := cm.users[msg.SenderID]; !known {
		cm.users[msg.SenderID] = &User{
			ID:        msg.SenderID,
			PublicKey: msg.SenderKey,
			Nick:      msg.Nick,
			Status:    "online",
			LastSeen:  msg.Timestamp,
			AllowDMs:  true,
		}
	}

	callback := cm.onDM
	cm.mu.Unlock()

	conv.mu.Lock()
	// Dedup
	for _, m := range conv.Messages {
		if m.ID == msg.ID {
			conv.mu.Unlock()
			return nil
		}
	}
	conv.Messages = append(conv.Messages, msg)
	conv.LastMessage = time.Now()
	conv.Unread++
	conv.mu.Unlock()

	if callback != nil {
		callback(msg)
	}

	// Auto-save after receiving DM
	go cm.Save()

	return nil
}

// ReceivePresence handles user presence updates
func (cm *ChatManager) ReceivePresence(user *User) {
	cm.mu.Lock()
	existing, exists := cm.users[user.ID]
	if exists {
		existing.Status = user.Status
		existing.StatusMsg = user.StatusMsg
		existing.LastSeen = user.LastSeen
		if user.Nick != "" {
			existing.Nick = user.Nick
		}
		if user.Avatar != "" {
			existing.Avatar = user.Avatar
		}
		if user.Bio != "" {
			existing.Bio = user.Bio
		}
	} else {
		cm.users[user.ID] = user
	}
	callback := cm.onPresence
	cm.mu.Unlock()

	if callback != nil {
		callback(user)
	}
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

// GetDMMessages returns messages from a DM conversation
func (cm *ChatManager) GetDMMessages(userID string, limit int) []*Message {
	convID := cm.getConversationID(cm.localID, userID)

	cm.mu.RLock()
	conv, exists := cm.conversations[convID]
	cm.mu.RUnlock()

	if !exists {
		return nil
	}

	conv.mu.RLock()
	defer conv.mu.RUnlock()

	if limit <= 0 || limit > len(conv.Messages) {
		limit = len(conv.Messages)
	}

	start := len(conv.Messages) - limit
	if start < 0 {
		start = 0
	}

	// Mark as read
	conv.Unread = 0

	return conv.Messages[start:]
}

// GetConversations returns all DM conversations
func (cm *ChatManager) GetConversations() []*DirectConversation {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	convs := make([]*DirectConversation, 0, len(cm.conversations))
	for _, conv := range cm.conversations {
		convs = append(convs, conv)
	}

	// Sort by last message time
	sort.Slice(convs, func(i, j int) bool {
		return convs[i].LastMessage.After(convs[j].LastMessage)
	})

	return convs
}

// GetUser returns a user by ID
func (cm *ChatManager) GetUser(userID string) *User {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.users[userID]
}

// GetUsers returns all known users
func (cm *ChatManager) GetUsers() []*User {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	users := make([]*User, 0, len(cm.users))
	for _, user := range cm.users {
		users = append(users, user)
	}
	return users
}

// GetOnlineUsers returns users online in a room
func (cm *ChatManager) GetOnlineUsers(roomName string) []*User {
	cm.mu.RLock()
	room, exists := cm.rooms[roomName]
	cm.mu.RUnlock()

	if !exists {
		return nil
	}

	room.mu.RLock()
	defer room.mu.RUnlock()

	cutoff := time.Now().Add(-UserPresenceTTL)
	users := make([]*User, 0)

	for userID, lastSeen := range room.onlineUsers {
		if lastSeen.After(cutoff) {
			cm.mu.RLock()
			if user, ok := cm.users[userID]; ok {
				users = append(users, user)
			}
			cm.mu.RUnlock()
		}
	}

	return users
}

// SearchUsers searches for users by nick
func (cm *ChatManager) SearchUsers(query string) []*User {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	query = strings.ToLower(query)
	var results []*User

	for _, user := range cm.users {
		if strings.Contains(strings.ToLower(user.Nick), query) ||
			strings.Contains(strings.ToLower(user.ID), query) {
			results = append(results, user)
		}
	}

	return results
}

// BlockUser blocks a user
func (cm *ChatManager) BlockUser(userID string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	for _, blocked := range cm.localUser.BlockedIDs {
		if blocked == userID {
			return
		}
	}
	cm.localUser.BlockedIDs = append(cm.localUser.BlockedIDs, userID)
}

// UnblockUser unblocks a user
func (cm *ChatManager) UnblockUser(userID string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	for i, blocked := range cm.localUser.BlockedIDs {
		if blocked == userID {
			cm.localUser.BlockedIDs = append(cm.localUser.BlockedIDs[:i], cm.localUser.BlockedIDs[i+1:]...)
			return
		}
	}
}

// AddReaction adds a reaction to a message
func (cm *ChatManager) AddReaction(roomName, messageID, emoji string) error {
	cm.mu.RLock()
	room, exists := cm.rooms[roomName]
	cm.mu.RUnlock()

	if !exists {
		return ErrRoomNotFound
	}

	room.mu.Lock()
	defer room.mu.Unlock()

	for _, msg := range room.messages {
		if msg.ID == messageID {
			if msg.Reactions == nil {
				msg.Reactions = make(map[string][]string)
			}
			// Check if already reacted
			for _, uid := range msg.Reactions[emoji] {
				if uid == cm.localID {
					return nil
				}
			}
			msg.Reactions[emoji] = append(msg.Reactions[emoji], cm.localID)
			return nil
		}
	}

	return ErrMessageNotFound
}

// JoinRoom joins a room
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
		room.onlineUsers[cm.localID] = time.Now()
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
		delete(room.onlineUsers, cm.localID)
		room.mu.Unlock()
	}
}

// SetTyping marks user as typing in a room
func (cm *ChatManager) SetTyping(roomName string, userID string) {
	cm.mu.RLock()
	room, exists := cm.rooms[roomName]
	cm.mu.RUnlock()

	if !exists {
		return
	}

	room.mu.Lock()
	if room.typingUsers == nil {
		room.typingUsers = make(map[string]time.Time)
	}
	room.typingUsers[userID] = time.Now()
	room.mu.Unlock()
}

// GetTyping returns list of nicknames of users currently typing
func (cm *ChatManager) GetTyping(roomName string) []string {
	cm.mu.RLock()
	room, exists := cm.rooms[roomName]
	cm.mu.RUnlock()

	if !exists {
		return nil
	}

	room.mu.RLock()
	defer room.mu.RUnlock()

	cutoff := time.Now().Add(-5 * time.Second)
	var names []string

	for userID, typingTime := range room.typingUsers {
		if typingTime.After(cutoff) && userID != cm.localID {
			cm.mu.RLock()
			if user, ok := cm.users[userID]; ok {
				names = append(names, user.Nick)
			} else {
				names = append(names, userID[:8])
			}
			cm.mu.RUnlock()
		}
	}
	return names
}

// EditMessage edits a message (only owner can edit)
func (cm *ChatManager) EditMessage(msgID, content, userID string) error {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	
	for _, room := range cm.rooms {
		room.mu.Lock()
		for _, msg := range room.messages {
			if msg.ID == msgID {
				if msg.SenderID != userID {
					room.mu.Unlock()
					return errors.New("not message owner")
				}
				msg.Content = content
				msg.Edited = true
				msg.EditedAt = time.Now().Unix()
				room.mu.Unlock()
				return nil
			}
		}
		room.mu.Unlock()
	}
	return errors.New("message not found")
}

// DeleteMessage deletes a message (only owner can delete)
func (cm *ChatManager) DeleteMessage(msgID, userID string) error {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	
	for _, room := range cm.rooms {
		room.mu.Lock()
		for i, msg := range room.messages {
			if msg.ID == msgID {
				if msg.SenderID != userID {
					room.mu.Unlock()
					return errors.New("not message owner")
				}
				// Mark as deleted rather than removing
				msg.Content = "[deleted]"
				msg.Type = MsgTypeSystem
				room.messages[i] = msg
				room.mu.Unlock()
				return nil
			}
		}
		room.mu.Unlock()
	}
	return errors.New("message not found")
}

// PinMessage pins a message in a room
func (cm *ChatManager) PinMessage(roomName, msgID string) {
	cm.mu.RLock()
	room, exists := cm.rooms[roomName]
	cm.mu.RUnlock()

	if !exists {
		return
	}

	room.mu.Lock()
	defer room.mu.Unlock()
	
	// Check if already pinned
	for _, id := range room.Pinned {
		if id == msgID {
			return
		}
	}
	room.Pinned = append(room.Pinned, msgID)
}

// GetPinnedMessages returns pinned messages for a room
func (cm *ChatManager) GetPinnedMessages(roomName string) []*Message {
	cm.mu.RLock()
	room, exists := cm.rooms[roomName]
	cm.mu.RUnlock()

	if !exists {
		return nil
	}

	room.mu.RLock()
	defer room.mu.RUnlock()
	
	var pinned []*Message
	for _, pinnedID := range room.Pinned {
		for _, msg := range room.messages {
			if msg.ID == pinnedID {
				pinned = append(pinned, msg)
				break
			}
		}
	}
	return pinned
}

// GetTypingUsers returns users currently typing
func (cm *ChatManager) GetTypingUsers(roomName string) []*User {
	cm.mu.RLock()
	room, exists := cm.rooms[roomName]
	cm.mu.RUnlock()

	if !exists {
		return nil
	}

	room.mu.RLock()
	defer room.mu.RUnlock()

	cutoff := time.Now().Add(-5 * time.Second)
	var users []*User

	for userID, typingTime := range room.typingUsers {
		if typingTime.After(cutoff) && userID != cm.localID {
			cm.mu.RLock()
			if user, ok := cm.users[userID]; ok {
				users = append(users, user)
			}
			cm.mu.RUnlock()
		}
	}

	return users
}

// CreateMediaAttachment creates a media attachment from data
func CreateMediaAttachment(name, mimeType string, data []byte) *MediaAttachment {
	if len(data) > MaxMediaSize {
		return nil
	}

	return &MediaAttachment{
		Type:     mimeType,
		Name:     name,
		Size:     int64(len(data)),
		Data:     base64.StdEncoding.EncodeToString(data),
		Checksum: hex.EncodeToString(hashData(data)),
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

// MarshalUser serializes a user for network transmission
func (u *User) Marshal() ([]byte, error) {
	return json.Marshal(u)
}

// UnmarshalUser deserializes a user
func UnmarshalUser(data []byte) (*User, error) {
	var u User
	err := json.Unmarshal(data, &u)
	return &u, err
}

// Helper functions

func (cm *ChatManager) signMessage(m *Message) []byte {
	data, _ := json.Marshal(struct {
		ID         string    `json:"id"`
		Room       string    `json:"room"`
		SenderID   string    `json:"sender_id"`
		ReceiverID string    `json:"receiver_id"`
		Content    string    `json:"content"`
		Time       time.Time `json:"time"`
	}{m.ID, m.Room, m.SenderID, m.ReceiverID, m.Content, m.Timestamp})
	return cm.signFunc(data)
}

func (cm *ChatManager) verifyMessage(m *Message) bool {
	if len(m.SenderKey) != ed25519.PublicKeySize {
		return false
	}
	data, _ := json.Marshal(struct {
		ID         string    `json:"id"`
		Room       string    `json:"room"`
		SenderID   string    `json:"sender_id"`
		ReceiverID string    `json:"receiver_id"`
		Content    string    `json:"content"`
		Time       time.Time `json:"time"`
	}{m.ID, m.Room, m.SenderID, m.ReceiverID, m.Content, m.Timestamp})
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

func (cm *ChatManager) getConversationID(user1, user2 string) string {
	if user1 < user2 {
		return user1 + ":" + user2
	}
	return user2 + ":" + user1
}

func generateMsgID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func hashData(data []byte) []byte {
	// Simple hash for checksum
	h := make([]byte, 32)
	for i, b := range data {
		h[i%32] ^= b
	}
	return h
}

// CleanOldMessages removes messages older than TTL
func (cm *ChatManager) CleanOldMessages() {
	cm.mu.RLock()
	rooms := make([]*Room, 0, len(cm.rooms))
	for _, r := range cm.rooms {
		rooms = append(rooms, r)
	}
	convs := make([]*DirectConversation, 0, len(cm.conversations))
	for _, c := range cm.conversations {
		convs = append(convs, c)
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

		// Clean offline users
		for uid, lastSeen := range room.onlineUsers {
			if lastSeen.Before(time.Now().Add(-UserPresenceTTL)) {
				delete(room.onlineUsers, uid)
			}
		}
		room.mu.Unlock()
	}

	for _, conv := range convs {
		conv.mu.Lock()
		var kept []*Message
		for _, msg := range conv.Messages {
			if msg.Timestamp.After(cutoff) {
				kept = append(kept, msg)
			}
		}
		conv.Messages = kept
		conv.mu.Unlock()
	}
}

// Stats returns chat statistics
func (cm *ChatManager) Stats() map[string]interface{} {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	totalMessages := 0
	for _, room := range cm.rooms {
		room.mu.RLock()
		totalMessages += len(room.messages)
		room.mu.RUnlock()
	}

	return map[string]interface{}{
		"rooms":          len(cm.rooms),
		"users":          len(cm.users),
		"conversations":  len(cm.conversations),
		"total_messages": totalMessages,
	}
}

// Errors
var (
	ErrNotMember        = &ChatError{"not a member of private room"}
	ErrInvalidSignature = &ChatError{"invalid message signature"}
	ErrUserNotFound     = &ChatError{"user not found"}
	ErrDMsDisabled      = &ChatError{"user has disabled direct messages"}
	ErrBlocked          = &ChatError{"you are blocked by this user"}
	ErrRoomNotFound     = &ChatError{"room not found"}
	ErrMessageNotFound  = &ChatError{"message not found"}
)

type ChatError struct {
	msg string
}

func (e *ChatError) Error() string {
	return e.msg
}
