// Package persist provides persistent storage for KayakNet data
package persist

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Store provides JSON file-based persistence
type Store struct {
	mu      sync.RWMutex
	dataDir string
}

// NewStore creates a new persistence store
func NewStore(dataDir string) (*Store, error) {
	// Create data directory if it doesn't exist
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}
	
	return &Store{
		dataDir: dataDir,
	}, nil
}

// Save saves data to a JSON file
func (s *Store) Save(filename string, data interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	path := filepath.Join(s.dataDir, filename)
	
	// Create temp file first
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
	
	// Atomic rename
	return os.Rename(tmpPath, path)
}

// Load loads data from a JSON file
func (s *Store) Load(filename string, data interface{}) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	path := filepath.Join(s.dataDir, filename)
	
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // File doesn't exist yet, not an error
		}
		return err
	}
	defer f.Close()
	
	return json.NewDecoder(f).Decode(data)
}

// Exists checks if a file exists
func (s *Store) Exists(filename string) bool {
	path := filepath.Join(s.dataDir, filename)
	_, err := os.Stat(path)
	return err == nil
}

// Delete removes a file
func (s *Store) Delete(filename string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	path := filepath.Join(s.dataDir, filename)
	return os.Remove(path)
}

// DataDir returns the data directory path
func (s *Store) DataDir() string {
	return s.dataDir
}

