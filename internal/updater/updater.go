// Package updater implements auto-update functionality for KayakNet
package updater

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// GitHub releases API
	ReleasesURL = "https://api.github.com/repos/KayakNet/downloads/releases/latest"
	// Check interval
	CheckInterval = 1 * time.Hour
)

// GitHubRelease represents a GitHub release
type GitHubRelease struct {
	TagName string  `json:"tag_name"`
	Name    string  `json:"name"`
	Assets  []Asset `json:"assets"`
}

// Asset represents a release asset
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// Updater manages auto-updates
type Updater struct {
	currentVersion string
	checkInterval  time.Duration
	onUpdate       func(oldVersion, newVersion string)
	stopCh         chan struct{}
	running        bool
}

// NewUpdater creates a new updater
func NewUpdater(currentVersion string) *Updater {
	return &Updater{
		currentVersion: currentVersion,
		checkInterval:  CheckInterval,
		stopCh:         make(chan struct{}),
	}
}

// SetUpdateCallback sets a callback for when updates are available
func (u *Updater) SetUpdateCallback(cb func(oldVersion, newVersion string)) {
	u.onUpdate = cb
}

// Start begins periodic update checks
func (u *Updater) Start() {
	if u.running {
		return
	}
	u.running = true
	
	go func() {
		// Check immediately on start
		u.CheckAndUpdate()
		
		ticker := time.NewTicker(u.checkInterval)
		defer ticker.Stop()
		
		for {
			select {
			case <-ticker.C:
				u.CheckAndUpdate()
			case <-u.stopCh:
				return
			}
		}
	}()
}

// Stop stops the updater
func (u *Updater) Stop() {
	if u.running {
		close(u.stopCh)
		u.running = false
	}
}

// CheckAndUpdate checks for updates and applies them if available
func (u *Updater) CheckAndUpdate() error {
	latest, err := u.GetLatestVersion()
	if err != nil {
		log.Printf("[UPDATE] Failed to check for updates: %v", err)
		return err
	}
	
	if !u.isNewerVersion(latest.TagName) {
		return nil
	}
	
	log.Printf("[UPDATE] New version available: %s (current: %s)", latest.TagName, u.currentVersion)
	
	if u.onUpdate != nil {
		u.onUpdate(u.currentVersion, latest.TagName)
	}
	
	// Find the right asset for this platform
	asset := u.findAsset(latest.Assets)
	if asset == nil {
		log.Printf("[UPDATE] No suitable binary found for %s/%s", runtime.GOOS, runtime.GOARCH)
		return fmt.Errorf("no suitable binary for this platform")
	}
	
	// Download and apply update
	if err := u.downloadAndApply(asset, latest.TagName); err != nil {
		log.Printf("[UPDATE] Failed to apply update: %v", err)
		return err
	}
	
	log.Printf("[UPDATE] Successfully updated to %s - restart required", latest.TagName)
	return nil
}

// GetLatestVersion fetches the latest release info from GitHub
func (u *Updater) GetLatestVersion() (*GitHubRelease, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	
	req, err := http.NewRequest("GET", ReleasesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "KayakNet-Updater/1.0")
	
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}
	
	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}
	
	return &release, nil
}

// isNewerVersion compares versions (simple string comparison for vX.Y.Z format)
func (u *Updater) isNewerVersion(newVersion string) bool {
	// Strip 'v' prefix if present
	current := strings.TrimPrefix(u.currentVersion, "v")
	new := strings.TrimPrefix(newVersion, "v")
	
	// Simple comparison - works for semantic versioning
	return new > current
}

// findAsset finds the appropriate asset for the current platform
func (u *Updater) findAsset(assets []Asset) *Asset {
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	
	// Map to expected filename pattern
	var ext string
	if goos == "windows" {
		ext = ".zip"
	} else {
		ext = ".tar.gz"
	}
	
	pattern := fmt.Sprintf("kayakd-v")
	suffix := fmt.Sprintf("-%s-%s%s", goos, goarch, ext)
	
	for i, asset := range assets {
		if strings.HasPrefix(asset.Name, pattern) && strings.HasSuffix(asset.Name, suffix) {
			return &assets[i]
		}
	}
	
	return nil
}

// downloadAndApply downloads the update and applies it
func (u *Updater) downloadAndApply(asset *Asset, version string) error {
	// Get current executable path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %v", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("failed to resolve executable path: %v", err)
	}
	
	// Create temp directory for download
	tmpDir, err := os.MkdirTemp("", "kayaknet-update-")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	
	// Download the archive
	archivePath := filepath.Join(tmpDir, asset.Name)
	log.Printf("[UPDATE] Downloading %s (%d bytes)...", asset.Name, asset.Size)
	
	if err := u.downloadFile(asset.BrowserDownloadURL, archivePath); err != nil {
		return fmt.Errorf("failed to download: %v", err)
	}
	
	// Extract the binary
	var newBinaryPath string
	if strings.HasSuffix(asset.Name, ".zip") {
		newBinaryPath, err = u.extractZip(archivePath, tmpDir)
	} else {
		newBinaryPath, err = u.extractTarGz(archivePath, tmpDir)
	}
	if err != nil {
		return fmt.Errorf("failed to extract: %v", err)
	}
	
	// Backup current binary
	backupPath := execPath + ".backup"
	if err := os.Rename(execPath, backupPath); err != nil {
		return fmt.Errorf("failed to backup current binary: %v", err)
	}
	
	// Move new binary to executable path
	if err := copyFile(newBinaryPath, execPath); err != nil {
		// Restore backup on failure
		os.Rename(backupPath, execPath)
		return fmt.Errorf("failed to install new binary: %v", err)
	}
	
	// Make executable
	if err := os.Chmod(execPath, 0755); err != nil {
		log.Printf("[UPDATE] Warning: failed to set permissions: %v", err)
	}
	
	// Remove backup
	os.Remove(backupPath)
	
	// Update current version
	u.currentVersion = version
	
	return nil
}

// downloadFile downloads a file from URL to local path
func (u *Updater) downloadFile(url, destPath string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %d", resp.StatusCode)
	}
	
	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()
	
	_, err = io.Copy(out, resp.Body)
	return err
}

// extractZip extracts a zip archive and returns the binary path
func (u *Updater) extractZip(archivePath, destDir string) (string, error) {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", err
	}
	defer r.Close()
	
	var binaryPath string
	for _, f := range r.File {
		// Skip directories and non-executable files
		if f.FileInfo().IsDir() {
			continue
		}
		
		// Look for the kayakd binary
		if strings.Contains(f.Name, "kayakd") && (strings.HasSuffix(f.Name, ".exe") || !strings.Contains(f.Name, ".")) {
			destPath := filepath.Join(destDir, filepath.Base(f.Name))
			
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			
			out, err := os.Create(destPath)
			if err != nil {
				rc.Close()
				return "", err
			}
			
			_, err = io.Copy(out, rc)
			out.Close()
			rc.Close()
			
			if err != nil {
				return "", err
			}
			
			binaryPath = destPath
			break
		}
	}
	
	if binaryPath == "" {
		return "", fmt.Errorf("no binary found in archive")
	}
	
	return binaryPath, nil
}

// extractTarGz extracts a tar.gz archive and returns the binary path
func (u *Updater) extractTarGz(archivePath, destDir string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	
	gzr, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gzr.Close()
	
	tr := tar.NewReader(gzr)
	
	var binaryPath string
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		
		// Skip directories
		if header.Typeflag == tar.TypeDir {
			continue
		}
		
		// Look for the kayakd binary
		if strings.Contains(header.Name, "kayakd") && !strings.Contains(header.Name, ".") {
			destPath := filepath.Join(destDir, filepath.Base(header.Name))
			
			out, err := os.Create(destPath)
			if err != nil {
				return "", err
			}
			
			_, err = io.Copy(out, tr)
			out.Close()
			
			if err != nil {
				return "", err
			}
			
			binaryPath = destPath
			break
		}
	}
	
	if binaryPath == "" {
		return "", fmt.Errorf("no binary found in archive")
	}
	
	return binaryPath, nil
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	
	_, err = io.Copy(out, in)
	return err
}

// GetCurrentVersion returns the current version
func (u *Updater) GetCurrentVersion() string {
	return u.currentVersion
}

// ForceCheck triggers an immediate update check
func (u *Updater) ForceCheck() (*GitHubRelease, error) {
	return u.GetLatestVersion()
}


