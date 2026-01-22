package kayaker

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	ErrUnauthorized    = errors.New("unauthorized")
	ErrNotFound        = errors.New("not found")
	ErrHandleTaken     = errors.New("handle already taken")
	ErrInvalidHandle   = errors.New("invalid handle format")
	ErrInvalidContent  = errors.New("invalid content")
)

// HandleRegex validates handle format
var HandleRegex = regexp.MustCompile(`^[a-zA-Z0-9_]{3,20}$`)

// Manager handles all Kayaker operations
type Manager struct {
	mu      sync.RWMutex
	store   *Store
	nodeID  string
	pubKey  []byte
	privKey []byte
	
	// Callbacks for P2P sync
	onNewPost    func(*Post)
	onNewProfile func(*Profile)
}

// NewManager creates a new Kayaker manager
func NewManager(dataDir string, nodeID string, pubKey, privKey []byte) (*Manager, error) {
	store, err := NewStore(dataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create store: %w", err)
	}

	return &Manager{
		store:   store,
		nodeID:  nodeID,
		pubKey:  pubKey,
		privKey: privKey,
	}, nil
}

// Close closes the manager
func (m *Manager) Close() error {
	return m.store.Close()
}

// SetSyncCallbacks sets callbacks for P2P synchronization
func (m *Manager) SetSyncCallbacks(onPost func(*Post), onProfile func(*Profile)) {
	m.onNewPost = onPost
	m.onNewProfile = onProfile
}

// generateID creates a unique ID
func generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// CreateProfile creates or updates the user's profile
func (m *Manager) CreateProfile(handle, displayName, bio string) (*Profile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Validate handle
	handle = strings.TrimPrefix(strings.ToLower(handle), "@")
	if !HandleRegex.MatchString(handle) {
		return nil, ErrInvalidHandle
	}

	// Check if handle is taken by another user
	existing, _ := m.store.GetProfileByHandle(handle)
	if existing != nil && existing.ID != m.nodeID {
		return nil, ErrHandleTaken
	}

	// Check if user already has a profile
	profile, _ := m.store.GetProfile(m.nodeID)
	
	// Generate follower key if new profile
	var followerKey []byte
	if profile == nil {
		var err error
		followerKey, err = GenerateFollowerKey()
		if err != nil {
			return nil, err
		}
	} else {
		followerKey = profile.FollowerKey
	}

	// Encrypt display name and bio (for followers-only visibility later)
	encDisplayName := displayName // For now, store plaintext for public profiles
	encBio := bio

	now := time.Now()
	newProfile := &Profile{
		ID:          m.nodeID,
		Handle:      handle,
		DisplayName: encDisplayName,
		Bio:         encBio,
		PublicKey:   m.pubKey,
		FollowerKey: followerKey,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if profile == nil {
		if err := m.store.CreateProfile(newProfile); err != nil {
			return nil, err
		}
	} else {
		newProfile.FollowerCount = profile.FollowerCount
		newProfile.FollowingCount = profile.FollowingCount
		newProfile.PostCount = profile.PostCount
		newProfile.CreatedAt = profile.CreatedAt
		if err := m.store.UpdateProfile(newProfile); err != nil {
			return nil, err
		}
	}

	// Notify for P2P sync
	if m.onNewProfile != nil {
		m.onNewProfile(newProfile)
	}

	return newProfile, nil
}

// GetProfile gets a profile by ID or handle
func (m *Manager) GetProfile(idOrHandle string) (*Profile, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if strings.HasPrefix(idOrHandle, "@") || !strings.HasPrefix(idOrHandle, "kyk") {
		return m.store.GetProfileByHandle(strings.TrimPrefix(idOrHandle, "@"))
	}
	return m.store.GetProfile(idOrHandle)
}

// GetMyProfile gets the current user's profile
func (m *Manager) GetMyProfile() (*Profile, error) {
	return m.store.GetProfile(m.nodeID)
}

// CreatePost creates a new post
func (m *Manager) CreatePost(req CreatePostRequest) (*Post, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Get author profile
	profile, err := m.store.GetProfile(m.nodeID)
	if err != nil || profile == nil {
		return nil, errors.New("profile not found, create profile first")
	}

	// Validate content
	content := strings.TrimSpace(req.Content)
	if len(content) == 0 || len(content) > 500 {
		return nil, ErrInvalidContent
	}

	// Generate post encryption key
	postKey, err := GenerateSymmetricKey()
	if err != nil {
		return nil, err
	}

	// Encrypt content
	encryptedContent, err := EncryptContent([]byte(content), postKey)
	if err != nil {
		return nil, err
	}

	// Hash plaintext for deduplication
	contentHash := HashContent([]byte(content))

	// Extract mentions from content
	mentions := extractMentions(content)

	// Clean hashtags
	var hashtags []string
	for _, tag := range req.Hashtags {
		tag = strings.TrimPrefix(strings.ToLower(tag), "#")
		if len(tag) > 0 && len(tag) <= 30 {
			hashtags = append(hashtags, tag)
		}
	}

	now := time.Now()
	post := &Post{
		ID:            generateID(),
		AuthorID:      m.nodeID,
		AuthorHandle:  profile.Handle,
		Content:       base64.StdEncoding.EncodeToString(encryptedContent),
		ContentHash:   contentHash,
		Visibility:    req.Visibility,
		ReplyToID:     req.ReplyToID,
		QuoteID:       req.QuoteID,
		Mentions:      mentions,
		Hashtags:      hashtags,
		CreatedAt:     now,
		UpdatedAt:     now,
		EncryptionKey: postKey, // In production, this would be encrypted per-visibility
	}

	// Sign the post
	signData := fmt.Sprintf("%s:%s:%s:%d", post.ID, post.AuthorID, post.ContentHash, post.CreatedAt.Unix())
	post.Signature = Sign([]byte(signData), m.privKey)

	if err := m.store.CreatePost(post); err != nil {
		return nil, err
	}

	// Create notifications for mentions
	for _, mention := range mentions {
		mentionedProfile, _ := m.store.GetProfileByHandle(mention)
		if mentionedProfile != nil && mentionedProfile.ID != m.nodeID {
			m.store.CreateNotification(&Notification{
				ID:        generateID(),
				UserID:    mentionedProfile.ID,
				Type:      NotificationMention,
				ActorID:   m.nodeID,
				PostID:    post.ID,
				CreatedAt: now,
			})
		}
	}

	// Create notification for reply
	if req.ReplyToID != "" {
		originalPost, _ := m.store.GetPost(req.ReplyToID)
		if originalPost != nil && originalPost.AuthorID != m.nodeID {
			m.store.CreateNotification(&Notification{
				ID:        generateID(),
				UserID:    originalPost.AuthorID,
				Type:      NotificationReply,
				ActorID:   m.nodeID,
				PostID:    post.ID,
				CreatedAt: now,
			})
		}
	}

	// Notify for P2P sync
	if m.onNewPost != nil {
		m.onNewPost(post)
	}

	return post, nil
}

// GetPost gets a post by ID with decrypted content
func (m *Manager) GetPost(postID string) (*Post, error) {
	post, err := m.store.GetPost(postID)
	if err != nil {
		return nil, err
	}
	if post == nil {
		return nil, ErrNotFound
	}
	return post, nil
}

// DeletePost deletes a post (only by author)
func (m *Manager) DeletePost(postID string) error {
	return m.store.DeletePost(postID, m.nodeID)
}

// DecryptPostContent decrypts a post's content
func (m *Manager) DecryptPostContent(post *Post) (string, error) {
	if post.EncryptionKey == nil {
		return "", errors.New("no encryption key")
	}

	ciphertext, err := base64.StdEncoding.DecodeString(post.Content)
	if err != nil {
		return "", err
	}

	plaintext, err := DecryptContent(ciphertext, post.EncryptionKey)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

// Repost creates a repost
func (m *Manager) Repost(postID string) (*Post, error) {
	original, err := m.store.GetPost(postID)
	if err != nil || original == nil {
		return nil, ErrNotFound
	}

	return m.CreatePost(CreatePostRequest{
		Content:    "", // Empty for pure repost
		Visibility: VisibilityPublic,
	})
}

// GetFeed gets a feed
func (m *Manager) GetFeed(req FeedRequest) (*FeedResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if req.Limit <= 0 || req.Limit > 50 {
		req.Limit = 20
	}

	var posts []Post
	var nextCursor string
	var err error

	switch req.Type {
	case FeedHome:
		posts, nextCursor, err = m.store.GetHomeFeed(m.nodeID, req.Limit, req.Cursor)
	case FeedExplore:
		posts, nextCursor, err = m.store.GetExploreFeed(req.Limit, req.Cursor)
	case FeedProfile:
		userID := req.UserID
		if userID == "" {
			userID = m.nodeID
		}
		posts, nextCursor, err = m.store.GetProfileFeed(userID, req.Limit, req.Cursor)
	case FeedReplies:
		posts, nextCursor, err = m.store.GetReplies(req.PostID, req.Limit, req.Cursor)
	default:
		posts, nextCursor, err = m.store.GetExploreFeed(req.Limit, req.Cursor)
	}

	if err != nil {
		return nil, err
	}

	return &FeedResponse{
		Posts:      posts,
		NextCursor: nextCursor,
		HasMore:    nextCursor != "",
	}, nil
}

// Follow follows a user
func (m *Manager) Follow(userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if userID == m.nodeID {
		return errors.New("cannot follow yourself")
	}

	// Get target profile
	target, err := m.store.GetProfile(userID)
	if err != nil || target == nil {
		return ErrNotFound
	}

	// Encrypt follower key for the follower (using their public key)
	// This allows them to decrypt followers-only posts
	var encryptedKey []byte
	if len(target.FollowerKey) > 0 && len(m.pubKey) > 0 {
		encryptedKey, _ = EncryptForRecipient(target.FollowerKey, Ed25519ToX25519Public(m.pubKey))
	}

	if err := m.store.Follow(m.nodeID, userID, encryptedKey); err != nil {
		return err
	}

	// Create notification
	m.store.CreateNotification(&Notification{
		ID:        generateID(),
		UserID:    userID,
		Type:      NotificationFollow,
		ActorID:   m.nodeID,
		CreatedAt: time.Now(),
	})

	return nil
}

// Unfollow unfollows a user
func (m *Manager) Unfollow(userID string) error {
	return m.store.Unfollow(m.nodeID, userID)
}

// IsFollowing checks if current user follows target
func (m *Manager) IsFollowing(userID string) (bool, error) {
	return m.store.IsFollowing(m.nodeID, userID)
}

// GetFollowers gets followers
func (m *Manager) GetFollowers(userID string, limit, offset int) ([]Profile, error) {
	if userID == "" {
		userID = m.nodeID
	}
	return m.store.GetFollowers(userID, limit, offset)
}

// GetFollowing gets following
func (m *Manager) GetFollowing(userID string, limit, offset int) ([]Profile, error) {
	if userID == "" {
		userID = m.nodeID
	}
	return m.store.GetFollowing(userID, limit, offset)
}

// LikePost likes a post
func (m *Manager) LikePost(postID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	post, _ := m.store.GetPost(postID)
	if post == nil {
		return ErrNotFound
	}

	if err := m.store.LikePost(m.nodeID, postID); err != nil {
		return err
	}

	// Create notification if not own post
	if post.AuthorID != m.nodeID {
		m.store.CreateNotification(&Notification{
			ID:        generateID(),
			UserID:    post.AuthorID,
			Type:      NotificationLike,
			ActorID:   m.nodeID,
			PostID:    postID,
			CreatedAt: time.Now(),
		})
	}

	return nil
}

// UnlikePost unlikes a post
func (m *Manager) UnlikePost(postID string) error {
	return m.store.UnlikePost(m.nodeID, postID)
}

// HasLiked checks if user has liked a post
func (m *Manager) HasLiked(postID string) (bool, error) {
	return m.store.HasLiked(m.nodeID, postID)
}

// GetNotifications gets notifications
func (m *Manager) GetNotifications(limit int, unreadOnly bool) ([]Notification, error) {
	return m.store.GetNotifications(m.nodeID, limit, unreadOnly)
}

// MarkNotificationsRead marks all notifications as read
func (m *Manager) MarkNotificationsRead() error {
	return m.store.MarkNotificationsRead(m.nodeID)
}

// Search searches posts and profiles
func (m *Manager) Search(query string, searchType string, limit int) (interface{}, error) {
	query = strings.TrimSpace(query)
	if len(query) < 2 {
		return nil, errors.New("query too short")
	}

	if limit <= 0 || limit > 50 {
		limit = 20
	}

	switch searchType {
	case "hashtag":
		tag := strings.TrimPrefix(query, "#")
		return m.store.SearchHashtags(tag, limit)
	case "user":
		handle := strings.TrimPrefix(query, "@")
		return m.store.SearchHandles(handle, limit)
	default:
		// Search both
		profiles, _ := m.store.SearchHandles(query, limit/2)
		posts, _ := m.store.SearchHashtags(query, limit/2)
		return map[string]interface{}{
			"profiles": profiles,
			"posts":    posts,
		}, nil
	}
}

// GetPostsForSync gets posts for P2P sync
func (m *Manager) GetPostsForSync(since time.Time, limit int) ([]Post, error) {
	return m.store.GetAllPosts(since, limit)
}

// ImportPost imports a post from P2P sync
func (m *Manager) ImportPost(p *Post) error {
	// Verify signature
	signData := fmt.Sprintf("%s:%s:%s:%d", p.ID, p.AuthorID, p.ContentHash, p.CreatedAt.Unix())
	
	// Get author's public key
	author, _ := m.store.GetProfile(p.AuthorID)
	if author != nil && len(author.PublicKey) > 0 {
		if !Verify([]byte(signData), p.Signature, author.PublicKey) {
			return errors.New("invalid signature")
		}
	}

	return m.store.ImportPost(p)
}

// ImportProfile imports a profile from P2P sync
func (m *Manager) ImportProfile(p *Profile) error {
	return m.store.ImportProfile(p)
}

// extractMentions extracts @mentions from content
func extractMentions(content string) []string {
	re := regexp.MustCompile(`@([a-zA-Z0-9_]{3,20})`)
	matches := re.FindAllStringSubmatch(content, -1)
	
	var mentions []string
	seen := make(map[string]bool)
	for _, match := range matches {
		handle := strings.ToLower(match[1])
		if !seen[handle] {
			mentions = append(mentions, handle)
			seen[handle] = true
		}
	}
	return mentions
}

// PostToJSON converts post to JSON for API
func PostToJSON(p *Post) map[string]interface{} {
	return map[string]interface{}{
		"id":            p.ID,
		"author_id":     p.AuthorID,
		"author_handle": p.AuthorHandle,
		"content":       p.Content,
		"visibility":    p.Visibility,
		"reply_to_id":   p.ReplyToID,
		"repost_id":     p.RepostID,
		"quote_id":      p.QuoteID,
		"mentions":      p.Mentions,
		"hashtags":      p.Hashtags,
		"like_count":    p.LikeCount,
		"reply_count":   p.ReplyCount,
		"repost_count":  p.RepostCount,
		"quote_count":   p.QuoteCount,
		"created_at":    p.CreatedAt.Format(time.RFC3339),
	}
}

// ProfileToJSON converts profile to JSON for API
func ProfileToJSON(p *Profile) map[string]interface{} {
	return map[string]interface{}{
		"id":              p.ID,
		"handle":          p.Handle,
		"display_name":    p.DisplayName,
		"bio":             p.Bio,
		"avatar_url":      p.AvatarURL,
		"banner_url":      p.BannerURL,
		"follower_count":  p.FollowerCount,
		"following_count": p.FollowingCount,
		"post_count":      p.PostCount,
		"verified":        p.Verified,
		"verified_domain": p.VerifiedDomain,
		"created_at":      p.CreatedAt.Format(time.RFC3339),
	}
}

// MarshalPost marshals a post for P2P sync
func MarshalPost(p *Post) ([]byte, error) {
	return json.Marshal(p)
}

// UnmarshalPost unmarshals a post from P2P sync
func UnmarshalPost(data []byte) (*Post, error) {
	p := &Post{}
	err := json.Unmarshal(data, p)
	return p, err
}

// MarshalProfile marshals a profile for P2P sync
func MarshalProfile(p *Profile) ([]byte, error) {
	return json.Marshal(p)
}

// UnmarshalProfile unmarshals a profile from P2P sync
func UnmarshalProfile(data []byte) (*Profile, error) {
	p := &Profile{}
	err := json.Unmarshal(data, p)
	return p, err
}

