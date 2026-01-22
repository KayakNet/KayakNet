package kayaker

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Store handles persistence for Kayaker
type Store struct {
	db      *sql.DB
	dataDir string
}

// NewStore creates a new Kayaker store
func NewStore(dataDir string) (*Store, error) {
	kayakerDir := filepath.Join(dataDir, "kayaker")
	if err := os.MkdirAll(kayakerDir, 0755); err != nil {
		return nil, err
	}

	dbPath := filepath.Join(kayakerDir, "kayaker.db")
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}

	store := &Store{
		db:      db,
		dataDir: kayakerDir,
	}

	if err := store.migrate(); err != nil {
		db.Close()
		return nil, err
	}

	return store, nil
}

// migrate creates the database schema
func (s *Store) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS profiles (
		id TEXT PRIMARY KEY,
		handle TEXT UNIQUE NOT NULL,
		display_name TEXT,
		bio TEXT,
		avatar_url TEXT,
		banner_url TEXT,
		follower_count INTEGER DEFAULT 0,
		following_count INTEGER DEFAULT 0,
		post_count INTEGER DEFAULT 0,
		public_key BLOB,
		follower_key BLOB,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		verified BOOLEAN DEFAULT FALSE,
		verified_domain TEXT
	);

	CREATE TABLE IF NOT EXISTS posts (
		id TEXT PRIMARY KEY,
		author_id TEXT NOT NULL,
		author_handle TEXT,
		content TEXT NOT NULL,
		content_hash TEXT,
		media_urls TEXT,
		visibility TEXT DEFAULT 'public',
		reply_to_id TEXT,
		repost_id TEXT,
		quote_id TEXT,
		mentions TEXT,
		hashtags TEXT,
		like_count INTEGER DEFAULT 0,
		reply_count INTEGER DEFAULT 0,
		repost_count INTEGER DEFAULT 0,
		quote_count INTEGER DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		signature BLOB,
		encryption_key BLOB,
		FOREIGN KEY (author_id) REFERENCES profiles(id)
	);

	CREATE TABLE IF NOT EXISTS follows (
		follower_id TEXT NOT NULL,
		following_id TEXT NOT NULL,
		encrypted_follower_key BLOB,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (follower_id, following_id)
	);

	CREATE TABLE IF NOT EXISTS likes (
		user_id TEXT NOT NULL,
		post_id TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (user_id, post_id)
	);

	CREATE TABLE IF NOT EXISTS notifications (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		type TEXT NOT NULL,
		actor_id TEXT,
		post_id TEXT,
		read BOOLEAN DEFAULT FALSE,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS key_exchanges (
		id TEXT PRIMARY KEY,
		from_id TEXT NOT NULL,
		to_id TEXT NOT NULL,
		encrypted_key BLOB NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		signature BLOB
	);

	CREATE INDEX IF NOT EXISTS idx_posts_author ON posts(author_id);
	CREATE INDEX IF NOT EXISTS idx_posts_created ON posts(created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_posts_reply ON posts(reply_to_id);
	CREATE INDEX IF NOT EXISTS idx_posts_hashtags ON posts(hashtags);
	CREATE INDEX IF NOT EXISTS idx_follows_follower ON follows(follower_id);
	CREATE INDEX IF NOT EXISTS idx_follows_following ON follows(following_id);
	CREATE INDEX IF NOT EXISTS idx_notifications_user ON notifications(user_id, created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_profiles_handle ON profiles(handle);
	`

	_, err := s.db.Exec(schema)
	return err
}

// Close closes the database
func (s *Store) Close() error {
	return s.db.Close()
}

// CreateProfile creates a new profile
func (s *Store) CreateProfile(p *Profile) error {
	_, err := s.db.Exec(`
		INSERT INTO profiles (id, handle, display_name, bio, avatar_url, banner_url, 
			public_key, follower_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, p.ID, p.Handle, p.DisplayName, p.Bio, p.AvatarURL, p.BannerURL,
		p.PublicKey, p.FollowerKey, p.CreatedAt, p.UpdatedAt)
	return err
}

// GetProfile retrieves a profile by ID
func (s *Store) GetProfile(id string) (*Profile, error) {
	p := &Profile{}
	err := s.db.QueryRow(`
		SELECT id, handle, display_name, bio, avatar_url, banner_url,
			follower_count, following_count, post_count, public_key, follower_key,
			created_at, updated_at, verified, verified_domain
		FROM profiles WHERE id = ?
	`, id).Scan(&p.ID, &p.Handle, &p.DisplayName, &p.Bio, &p.AvatarURL, &p.BannerURL,
		&p.FollowerCount, &p.FollowingCount, &p.PostCount, &p.PublicKey, &p.FollowerKey,
		&p.CreatedAt, &p.UpdatedAt, &p.Verified, &p.VerifiedDomain)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

// GetProfileByHandle retrieves a profile by handle
func (s *Store) GetProfileByHandle(handle string) (*Profile, error) {
	p := &Profile{}
	handle = strings.TrimPrefix(handle, "@")
	err := s.db.QueryRow(`
		SELECT id, handle, display_name, bio, avatar_url, banner_url,
			follower_count, following_count, post_count, public_key, follower_key,
			created_at, updated_at, verified, verified_domain
		FROM profiles WHERE handle = ?
	`, handle).Scan(&p.ID, &p.Handle, &p.DisplayName, &p.Bio, &p.AvatarURL, &p.BannerURL,
		&p.FollowerCount, &p.FollowingCount, &p.PostCount, &p.PublicKey, &p.FollowerKey,
		&p.CreatedAt, &p.UpdatedAt, &p.Verified, &p.VerifiedDomain)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

// UpdateProfile updates a profile
func (s *Store) UpdateProfile(p *Profile) error {
	p.UpdatedAt = time.Now()
	_, err := s.db.Exec(`
		UPDATE profiles SET handle = ?, display_name = ?, bio = ?, 
			avatar_url = ?, banner_url = ?, updated_at = ?
		WHERE id = ?
	`, p.Handle, p.DisplayName, p.Bio, p.AvatarURL, p.BannerURL, p.UpdatedAt, p.ID)
	return err
}

// CreatePost creates a new post
func (s *Store) CreatePost(p *Post) error {
	mediaJSON, _ := json.Marshal(p.MediaURLs)
	mentionsJSON, _ := json.Marshal(p.Mentions)
	hashtagsJSON, _ := json.Marshal(p.Hashtags)

	_, err := s.db.Exec(`
		INSERT INTO posts (id, author_id, author_handle, content, content_hash, media_urls,
			visibility, reply_to_id, repost_id, quote_id, mentions, hashtags,
			created_at, updated_at, signature, encryption_key)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, p.ID, p.AuthorID, p.AuthorHandle, p.Content, p.ContentHash, string(mediaJSON),
		p.Visibility, p.ReplyToID, p.RepostID, p.QuoteID, string(mentionsJSON), string(hashtagsJSON),
		p.CreatedAt, p.UpdatedAt, p.Signature, p.EncryptionKey)

	if err == nil {
		// Update author's post count
		s.db.Exec(`UPDATE profiles SET post_count = post_count + 1 WHERE id = ?`, p.AuthorID)
		// Update reply count if this is a reply
		if p.ReplyToID != "" {
			s.db.Exec(`UPDATE posts SET reply_count = reply_count + 1 WHERE id = ?`, p.ReplyToID)
		}
		// Update repost count
		if p.RepostID != "" {
			s.db.Exec(`UPDATE posts SET repost_count = repost_count + 1 WHERE id = ?`, p.RepostID)
		}
		// Update quote count
		if p.QuoteID != "" {
			s.db.Exec(`UPDATE posts SET quote_count = quote_count + 1 WHERE id = ?`, p.QuoteID)
		}
	}
	return err
}

// GetPost retrieves a post by ID
func (s *Store) GetPost(id string) (*Post, error) {
	p := &Post{}
	var mediaJSON, mentionsJSON, hashtagsJSON string
	err := s.db.QueryRow(`
		SELECT id, author_id, author_handle, content, content_hash, media_urls,
			visibility, reply_to_id, repost_id, quote_id, mentions, hashtags,
			like_count, reply_count, repost_count, quote_count,
			created_at, updated_at, signature, encryption_key
		FROM posts WHERE id = ?
	`, id).Scan(&p.ID, &p.AuthorID, &p.AuthorHandle, &p.Content, &p.ContentHash, &mediaJSON,
		&p.Visibility, &p.ReplyToID, &p.RepostID, &p.QuoteID, &mentionsJSON, &hashtagsJSON,
		&p.LikeCount, &p.ReplyCount, &p.RepostCount, &p.QuoteCount,
		&p.CreatedAt, &p.UpdatedAt, &p.Signature, &p.EncryptionKey)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	json.Unmarshal([]byte(mediaJSON), &p.MediaURLs)
	json.Unmarshal([]byte(mentionsJSON), &p.Mentions)
	json.Unmarshal([]byte(hashtagsJSON), &p.Hashtags)

	return p, nil
}

// DeletePost deletes a post
func (s *Store) DeletePost(id string, authorID string) error {
	result, err := s.db.Exec(`DELETE FROM posts WHERE id = ? AND author_id = ?`, id, authorID)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected > 0 {
		s.db.Exec(`UPDATE profiles SET post_count = post_count - 1 WHERE id = ?`, authorID)
	}
	return nil
}

// GetHomeFeed gets posts from followed users
func (s *Store) GetHomeFeed(userID string, limit int, cursor string) ([]Post, string, error) {
	query := `
		SELECT p.id, p.author_id, p.author_handle, p.content, p.content_hash, p.media_urls,
			p.visibility, p.reply_to_id, p.repost_id, p.quote_id, p.mentions, p.hashtags,
			p.like_count, p.reply_count, p.repost_count, p.quote_count,
			p.created_at, p.updated_at, p.signature, p.encryption_key
		FROM posts p
		INNER JOIN follows f ON p.author_id = f.following_id
		WHERE f.follower_id = ?
	`
	args := []interface{}{userID}

	if cursor != "" {
		query += ` AND p.created_at < ?`
		args = append(args, cursor)
	}

	query += ` ORDER BY p.created_at DESC LIMIT ?`
	args = append(args, limit+1)

	return s.queryPosts(query, args, limit)
}

// GetExploreFeed gets trending/recent public posts
func (s *Store) GetExploreFeed(limit int, cursor string) ([]Post, string, error) {
	query := `
		SELECT id, author_id, author_handle, content, content_hash, media_urls,
			visibility, reply_to_id, repost_id, quote_id, mentions, hashtags,
			like_count, reply_count, repost_count, quote_count,
			created_at, updated_at, signature, encryption_key
		FROM posts
		WHERE visibility = 'public' AND reply_to_id = ''
	`
	args := []interface{}{}

	if cursor != "" {
		query += ` AND created_at < ?`
		args = append(args, cursor)
	}

	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit+1)

	return s.queryPosts(query, args, limit)
}

// GetProfileFeed gets posts by a specific user
func (s *Store) GetProfileFeed(userID string, limit int, cursor string) ([]Post, string, error) {
	query := `
		SELECT id, author_id, author_handle, content, content_hash, media_urls,
			visibility, reply_to_id, repost_id, quote_id, mentions, hashtags,
			like_count, reply_count, repost_count, quote_count,
			created_at, updated_at, signature, encryption_key
		FROM posts
		WHERE author_id = ?
	`
	args := []interface{}{userID}

	if cursor != "" {
		query += ` AND created_at < ?`
		args = append(args, cursor)
	}

	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit+1)

	return s.queryPosts(query, args, limit)
}

// GetReplies gets replies to a post
func (s *Store) GetReplies(postID string, limit int, cursor string) ([]Post, string, error) {
	query := `
		SELECT id, author_id, author_handle, content, content_hash, media_urls,
			visibility, reply_to_id, repost_id, quote_id, mentions, hashtags,
			like_count, reply_count, repost_count, quote_count,
			created_at, updated_at, signature, encryption_key
		FROM posts
		WHERE reply_to_id = ?
	`
	args := []interface{}{postID}

	if cursor != "" {
		query += ` AND created_at < ?`
		args = append(args, cursor)
	}

	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit+1)

	return s.queryPosts(query, args, limit)
}

func (s *Store) queryPosts(query string, args []interface{}, limit int) ([]Post, string, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var posts []Post
	var nextCursor string

	for rows.Next() {
		p := Post{}
		var mediaJSON, mentionsJSON, hashtagsJSON string
		err := rows.Scan(&p.ID, &p.AuthorID, &p.AuthorHandle, &p.Content, &p.ContentHash, &mediaJSON,
			&p.Visibility, &p.ReplyToID, &p.RepostID, &p.QuoteID, &mentionsJSON, &hashtagsJSON,
			&p.LikeCount, &p.ReplyCount, &p.RepostCount, &p.QuoteCount,
			&p.CreatedAt, &p.UpdatedAt, &p.Signature, &p.EncryptionKey)
		if err != nil {
			continue
		}
		json.Unmarshal([]byte(mediaJSON), &p.MediaURLs)
		json.Unmarshal([]byte(mentionsJSON), &p.Mentions)
		json.Unmarshal([]byte(hashtagsJSON), &p.Hashtags)

		if len(posts) < limit {
			posts = append(posts, p)
		} else {
			nextCursor = p.CreatedAt.Format(time.RFC3339Nano)
		}
	}

	return posts, nextCursor, nil
}

// Follow creates a follow relationship
func (s *Store) Follow(followerID, followingID string, encryptedKey []byte) error {
	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO follows (follower_id, following_id, encrypted_follower_key, created_at)
		VALUES (?, ?, ?, ?)
	`, followerID, followingID, encryptedKey, time.Now())

	if err == nil {
		s.db.Exec(`UPDATE profiles SET following_count = following_count + 1 WHERE id = ?`, followerID)
		s.db.Exec(`UPDATE profiles SET follower_count = follower_count + 1 WHERE id = ?`, followingID)
	}
	return err
}

// Unfollow removes a follow relationship
func (s *Store) Unfollow(followerID, followingID string) error {
	result, err := s.db.Exec(`DELETE FROM follows WHERE follower_id = ? AND following_id = ?`,
		followerID, followingID)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected > 0 {
		s.db.Exec(`UPDATE profiles SET following_count = following_count - 1 WHERE id = ?`, followerID)
		s.db.Exec(`UPDATE profiles SET follower_count = follower_count - 1 WHERE id = ?`, followingID)
	}
	return nil
}

// IsFollowing checks if user A follows user B
func (s *Store) IsFollowing(followerID, followingID string) (bool, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM follows WHERE follower_id = ? AND following_id = ?`,
		followerID, followingID).Scan(&count)
	return count > 0, err
}

// GetFollowers gets followers of a user
func (s *Store) GetFollowers(userID string, limit int, offset int) ([]Profile, error) {
	rows, err := s.db.Query(`
		SELECT p.id, p.handle, p.display_name, p.bio, p.avatar_url, p.follower_count, p.following_count
		FROM profiles p
		INNER JOIN follows f ON p.id = f.follower_id
		WHERE f.following_id = ?
		ORDER BY f.created_at DESC
		LIMIT ? OFFSET ?
	`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var profiles []Profile
	for rows.Next() {
		p := Profile{}
		rows.Scan(&p.ID, &p.Handle, &p.DisplayName, &p.Bio, &p.AvatarURL, &p.FollowerCount, &p.FollowingCount)
		profiles = append(profiles, p)
	}
	return profiles, nil
}

// GetFollowing gets users that a user follows
func (s *Store) GetFollowing(userID string, limit int, offset int) ([]Profile, error) {
	rows, err := s.db.Query(`
		SELECT p.id, p.handle, p.display_name, p.bio, p.avatar_url, p.follower_count, p.following_count
		FROM profiles p
		INNER JOIN follows f ON p.id = f.following_id
		WHERE f.follower_id = ?
		ORDER BY f.created_at DESC
		LIMIT ? OFFSET ?
	`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var profiles []Profile
	for rows.Next() {
		p := Profile{}
		rows.Scan(&p.ID, &p.Handle, &p.DisplayName, &p.Bio, &p.AvatarURL, &p.FollowerCount, &p.FollowingCount)
		profiles = append(profiles, p)
	}
	return profiles, nil
}

// LikePost adds a like
func (s *Store) LikePost(userID, postID string) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO likes (user_id, post_id, created_at) VALUES (?, ?, ?)`,
		userID, postID, time.Now())
	if err == nil {
		s.db.Exec(`UPDATE posts SET like_count = like_count + 1 WHERE id = ?`, postID)
	}
	return err
}

// UnlikePost removes a like
func (s *Store) UnlikePost(userID, postID string) error {
	result, err := s.db.Exec(`DELETE FROM likes WHERE user_id = ? AND post_id = ?`, userID, postID)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected > 0 {
		s.db.Exec(`UPDATE posts SET like_count = like_count - 1 WHERE id = ?`, postID)
	}
	return nil
}

// HasLiked checks if user has liked a post
func (s *Store) HasLiked(userID, postID string) (bool, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM likes WHERE user_id = ? AND post_id = ?`,
		userID, postID).Scan(&count)
	return count > 0, err
}

// CreateNotification creates a notification
func (s *Store) CreateNotification(n *Notification) error {
	_, err := s.db.Exec(`
		INSERT INTO notifications (id, user_id, type, actor_id, post_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, n.ID, n.UserID, n.Type, n.ActorID, n.PostID, n.CreatedAt)
	return err
}

// GetNotifications gets notifications for a user
func (s *Store) GetNotifications(userID string, limit int, unreadOnly bool) ([]Notification, error) {
	query := `SELECT id, user_id, type, actor_id, post_id, read, created_at 
		FROM notifications WHERE user_id = ?`
	if unreadOnly {
		query += ` AND read = FALSE`
	}
	query += ` ORDER BY created_at DESC LIMIT ?`

	rows, err := s.db.Query(query, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notifications []Notification
	for rows.Next() {
		n := Notification{}
		rows.Scan(&n.ID, &n.UserID, &n.Type, &n.ActorID, &n.PostID, &n.Read, &n.CreatedAt)
		notifications = append(notifications, n)
	}
	return notifications, nil
}

// MarkNotificationsRead marks notifications as read
func (s *Store) MarkNotificationsRead(userID string) error {
	_, err := s.db.Exec(`UPDATE notifications SET read = TRUE WHERE user_id = ?`, userID)
	return err
}

// SearchHashtags searches posts by hashtag
func (s *Store) SearchHashtags(tag string, limit int) ([]Post, error) {
	query := `
		SELECT id, author_id, author_handle, content, content_hash, media_urls,
			visibility, reply_to_id, repost_id, quote_id, mentions, hashtags,
			like_count, reply_count, repost_count, quote_count,
			created_at, updated_at, signature, encryption_key
		FROM posts
		WHERE hashtags LIKE ? AND visibility = 'public'
		ORDER BY created_at DESC LIMIT ?
	`
	posts, _, err := s.queryPosts(query, []interface{}{fmt.Sprintf("%%%s%%", tag), limit}, limit)
	return posts, err
}

// SearchHandles searches profiles by handle
func (s *Store) SearchHandles(query string, limit int) ([]Profile, error) {
	rows, err := s.db.Query(`
		SELECT id, handle, display_name, bio, avatar_url, follower_count, following_count
		FROM profiles
		WHERE handle LIKE ?
		ORDER BY follower_count DESC
		LIMIT ?
	`, "%"+query+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var profiles []Profile
	for rows.Next() {
		p := Profile{}
		rows.Scan(&p.ID, &p.Handle, &p.DisplayName, &p.Bio, &p.AvatarURL, &p.FollowerCount, &p.FollowingCount)
		profiles = append(profiles, p)
	}
	return profiles, nil
}

// GetAllPosts returns all posts for P2P sync (limited)
func (s *Store) GetAllPosts(since time.Time, limit int) ([]Post, error) {
	query := `
		SELECT id, author_id, author_handle, content, content_hash, media_urls,
			visibility, reply_to_id, repost_id, quote_id, mentions, hashtags,
			like_count, reply_count, repost_count, quote_count,
			created_at, updated_at, signature, encryption_key
		FROM posts
		WHERE created_at > ?
		ORDER BY created_at ASC LIMIT ?
	`
	posts, _, err := s.queryPosts(query, []interface{}{since, limit}, limit)
	return posts, err
}

// ImportPost imports a post from P2P sync
func (s *Store) ImportPost(p *Post) error {
	// Check if post already exists
	existing, _ := s.GetPost(p.ID)
	if existing != nil {
		return nil // Already have it
	}
	return s.CreatePost(p)
}

// ImportProfile imports a profile from P2P sync
func (s *Store) ImportProfile(p *Profile) error {
	existing, _ := s.GetProfile(p.ID)
	if existing != nil {
		// Update if newer
		if p.UpdatedAt.After(existing.UpdatedAt) {
			return s.UpdateProfile(p)
		}
		return nil
	}
	return s.CreateProfile(p)
}

