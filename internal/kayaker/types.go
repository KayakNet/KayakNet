// Package kayaker implements a Twitter/Threads-like microblogging platform for KayakNet
// All posts are end-to-end encrypted and synced via P2P
package kayaker

import (
	"time"
)

// Visibility determines who can decrypt and view a post
type Visibility string

const (
	VisibilityPublic    Visibility = "public"    // Anyone with the public view key
	VisibilityFollowers Visibility = "followers" // Only followers with follower keys
	VisibilityMentioned Visibility = "mentioned" // Only mentioned users
)

// Post represents an encrypted microblog post
type Post struct {
	ID            string     `json:"id"`
	AuthorID      string     `json:"author_id"`      // KayakNet address (kyk1...)
	AuthorHandle  string     `json:"author_handle"`  // @handle
	Content       string     `json:"content"`        // Encrypted content (ciphertext)
	ContentHash   string     `json:"content_hash"`   // Hash of plaintext for dedup
	MediaURLs     []string   `json:"media_urls"`     // Optional encrypted media references
	Visibility    Visibility `json:"visibility"`
	ReplyToID     string     `json:"reply_to_id,omitempty"`    // If this is a reply
	RepostID      string     `json:"repost_id,omitempty"`      // If this is a repost
	QuoteID       string     `json:"quote_id,omitempty"`       // If this is a quote post
	Mentions      []string   `json:"mentions,omitempty"`       // Mentioned user IDs
	Hashtags      []string   `json:"hashtags,omitempty"`       // Public hashtags (optional, user choice)
	LikeCount     int        `json:"like_count"`
	ReplyCount    int        `json:"reply_count"`
	RepostCount   int        `json:"repost_count"`
	QuoteCount    int        `json:"quote_count"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	Signature     []byte     `json:"signature"`      // Signed by author's identity key
	EncryptionKey []byte     `json:"encryption_key"` // Encrypted symmetric key for this post
}

// Profile represents a user profile
type Profile struct {
	ID              string    `json:"id"`               // KayakNet address
	Handle          string    `json:"handle"`           // Unique @handle
	DisplayName     string    `json:"display_name"`     // Encrypted
	Bio             string    `json:"bio"`              // Encrypted
	AvatarURL       string    `json:"avatar_url"`       // Encrypted reference
	BannerURL       string    `json:"banner_url"`       // Encrypted reference
	FollowerCount   int       `json:"follower_count"`
	FollowingCount  int       `json:"following_count"`
	PostCount       int       `json:"post_count"`
	PublicKey       []byte    `json:"public_key"`       // Identity public key for encryption
	FollowerKey     []byte    `json:"follower_key"`     // Key shared with followers
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	Verified        bool      `json:"verified"`         // Domain verification
	VerifiedDomain  string    `json:"verified_domain"`  // e.g., example.kyk
}

// Follow represents a follow relationship
type Follow struct {
	FollowerID  string    `json:"follower_id"`
	FollowingID string    `json:"following_id"`
	CreatedAt   time.Time `json:"created_at"`
	// Encrypted follower key for the follower to decrypt follower-only posts
	EncryptedFollowerKey []byte `json:"encrypted_follower_key"`
}

// Like represents a like on a post
type Like struct {
	UserID    string    `json:"user_id"`
	PostID    string    `json:"post_id"`
	CreatedAt time.Time `json:"created_at"`
}

// Notification represents a user notification
type Notification struct {
	ID        string           `json:"id"`
	UserID    string           `json:"user_id"`
	Type      NotificationType `json:"type"`
	ActorID   string           `json:"actor_id"`   // Who triggered it
	PostID    string           `json:"post_id"`    // Related post if any
	Read      bool             `json:"read"`
	CreatedAt time.Time        `json:"created_at"`
}

// NotificationType categorizes notifications
type NotificationType string

const (
	NotificationMention  NotificationType = "mention"
	NotificationReply    NotificationType = "reply"
	NotificationLike     NotificationType = "like"
	NotificationRepost   NotificationType = "repost"
	NotificationFollow   NotificationType = "follow"
	NotificationQuote    NotificationType = "quote"
)

// FeedType determines the type of feed
type FeedType string

const (
	FeedHome     FeedType = "home"     // Posts from followed users
	FeedExplore  FeedType = "explore"  // Trending/discovery
	FeedProfile  FeedType = "profile"  // Single user's posts
	FeedReplies  FeedType = "replies"  // Replies to a post
	FeedMentions FeedType = "mentions" // Posts mentioning user
)

// FeedRequest represents a request for a feed
type FeedRequest struct {
	Type      FeedType `json:"type"`
	UserID    string   `json:"user_id,omitempty"`    // For profile feed
	PostID    string   `json:"post_id,omitempty"`    // For replies feed
	Cursor    string   `json:"cursor,omitempty"`     // Pagination cursor
	Limit     int      `json:"limit,omitempty"`      // Items per page
}

// FeedResponse contains feed results
type FeedResponse struct {
	Posts      []Post `json:"posts"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// CreatePostRequest for creating a new post
type CreatePostRequest struct {
	Content    string     `json:"content"`              // Plaintext (will be encrypted)
	Visibility Visibility `json:"visibility"`
	ReplyToID  string     `json:"reply_to_id,omitempty"`
	QuoteID    string     `json:"quote_id,omitempty"`
	Hashtags   []string   `json:"hashtags,omitempty"`   // Optional public hashtags
}

// KeyExchange for distributing follower keys
type KeyExchange struct {
	FromID       string    `json:"from_id"`
	ToID         string    `json:"to_id"`
	EncryptedKey []byte    `json:"encrypted_key"` // Follower key encrypted to recipient's public key
	CreatedAt    time.Time `json:"created_at"`
	Signature    []byte    `json:"signature"`
}

