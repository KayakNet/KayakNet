// Package config handles configuration for KayakNet nodes
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Config is the main configuration for a KayakNet node
type Config struct {
	Node      NodeConfig      `json:"node"`
	DHT       DHTConfig       `json:"dht"`
	PubSub    PubSubConfig    `json:"pubsub"`
	Transport TransportConfig `json:"transport"`
	Security  SecurityConfig  `json:"security"`
	Proxy     ProxyConfig     `json:"proxy"`
	Crypto    CryptoConfig    `json:"crypto"`
	Logging   LoggingConfig   `json:"logging"`
}

// NodeConfig configures the node identity and storage
type NodeConfig struct {
	Name         string `json:"name"`
	DataDir      string `json:"data_dir"`
	IdentityFile string `json:"identity_file"`
	Mode         string `json:"mode"` // "user", "vendor", "bootstrap"
}

// SecurityConfig configures security parameters
type SecurityConfig struct {
	// Rate limiting
	RateLimitWindow   time.Duration `json:"rate_limit_window"`
	RateLimitMessages int           `json:"rate_limit_messages"`
	
	// Banning
	BanThreshold      int           `json:"ban_threshold"`
	InitialBanTime    time.Duration `json:"initial_ban_time"`
	MaxBanTime        time.Duration `json:"max_ban_time"`
	
	// Onion routing
	OnionHops         int           `json:"onion_hops"`
	CircuitTimeout    time.Duration `json:"circuit_timeout"`
	
	// Traffic analysis resistance
	PaddingSize       int           `json:"padding_size"`
	BatchInterval     time.Duration `json:"batch_interval"`
	MaxDelay          time.Duration `json:"max_delay"`
	DummyTrafficRate  float64       `json:"dummy_traffic_rate"`
}

// ProxyConfig configures the browser proxy
type ProxyConfig struct {
	Enabled       bool   `json:"enabled"`
	HTTPPort      int    `json:"http_port"`
	SOCKS5Port    int    `json:"socks5_port"`
	HomepagePort  int    `json:"homepage_port"`
	AllowExternal bool   `json:"allow_external"`
	BindAddress   string `json:"bind_address"`
}

// CryptoConfig configures cryptocurrency wallets
type CryptoConfig struct {
	Monero MoneroConfig `json:"monero"`
	Zcash  ZcashConfig  `json:"zcash"`
}

// MoneroConfig configures Monero wallet
type MoneroConfig struct {
	Enabled     bool   `json:"enabled"`
	RPCHost     string `json:"rpc_host"`
	RPCPort     int    `json:"rpc_port"`
	RPCUser     string `json:"rpc_user"`
	RPCPassword string `json:"rpc_password"`
	WalletFile  string `json:"wallet_file"`
	Network     string `json:"network"` // "mainnet", "stagenet", "testnet"
}

// ZcashConfig configures Zcash wallet
type ZcashConfig struct {
	Enabled     bool   `json:"enabled"`
	RPCHost     string `json:"rpc_host"`
	RPCPort     int    `json:"rpc_port"`
	RPCUser     string `json:"rpc_user"`
	RPCPassword string `json:"rpc_password"`
	Network     string `json:"network"` // "mainnet", "testnet"
}

// LoggingConfig configures logging
type LoggingConfig struct {
	Level      string `json:"level"` // "debug", "info", "warn", "error"
	File       string `json:"file"`
	MaxSize    int    `json:"max_size_mb"`
	MaxBackups int    `json:"max_backups"`
	MaxAge     int    `json:"max_age_days"`
	Compress   bool   `json:"compress"`
}

// DHTConfig configures the distributed hash table
type DHTConfig struct {
	K               int           `json:"k"`
	Alpha           int           `json:"alpha"`
	RecordTTL       time.Duration `json:"record_ttl"`
	RefreshInterval time.Duration `json:"refresh_interval"`
	BootstrapNodes  []string      `json:"bootstrap_nodes"`
}

// PubSubConfig configures the pub/sub system
type PubSubConfig struct {
	MaxTopics         int           `json:"max_topics"`
	MaxPeersPerTopic  int           `json:"max_peers_per_topic"`
	MessageBufferSize int           `json:"message_buffer_size"`
	RateLimitWindow   time.Duration `json:"rate_limit_window"`
	RateLimitMessages int           `json:"rate_limit_messages"`
}

// TransportConfig configures network transports
type TransportConfig struct {
	UDP UDPConfig `json:"udp"`
	TCP TCPConfig `json:"tcp"`
	Tor TorConfig `json:"tor"`
	I2P I2PConfig `json:"i2p"`
}

// UDPConfig configures UDP transport
type UDPConfig struct {
	Enabled     bool   `json:"enabled"`
	ListenAddr  string `json:"listen_addr"`
	MaxPacketSize int  `json:"max_packet_size"`
}

// TCPConfig configures TCP transport
type TCPConfig struct {
	Enabled    bool   `json:"enabled"`
	ListenAddr string `json:"listen_addr"`
}

// TorConfig configures Tor transport (optional)
type TorConfig struct {
	Enabled   bool   `json:"enabled"`
	ProxyAddr string `json:"proxy_addr"`
}

// I2PConfig configures I2P transport (optional)
type I2PConfig struct {
	Enabled   bool   `json:"enabled"`
	SAMAddr   string `json:"sam_addr"`
}

// Default returns a default configuration
func Default() *Config {
	return &Config{
		Node: NodeConfig{
			Name:         "kayaknet-node",
			DataDir:      "./data",
			IdentityFile: "./data/identity.json",
			Mode:         "user",
		},
		DHT: DHTConfig{
			K:               20,
			Alpha:           3,
			RecordTTL:       24 * time.Hour,
			RefreshInterval: time.Hour,
			BootstrapNodes:  []string{"203.161.33.237:4242"},
		},
		PubSub: PubSubConfig{
			MaxTopics:         100,
			MaxPeersPerTopic:  50,
			MessageBufferSize: 1000,
			RateLimitWindow:   time.Second,
			RateLimitMessages: 10,
		},
		Transport: TransportConfig{
			UDP: UDPConfig{
				Enabled:       true,
				ListenAddr:    "0.0.0.0:4242",
				MaxPacketSize: 65535,
			},
			TCP: TCPConfig{
				Enabled:    false,
				ListenAddr: "0.0.0.0:4243",
			},
			Tor: TorConfig{
				Enabled:   false,
				ProxyAddr: "127.0.0.1:9050",
			},
			I2P: I2PConfig{
				Enabled: false,
				SAMAddr: "127.0.0.1:7656",
			},
		},
		Security: SecurityConfig{
			RateLimitWindow:   time.Second,
			RateLimitMessages: 100,
			BanThreshold:      10,
			InitialBanTime:    5 * time.Minute,
			MaxBanTime:        24 * time.Hour,
			OnionHops:         3,
			CircuitTimeout:    30 * time.Second,
			PaddingSize:       2048,
			BatchInterval:     100 * time.Millisecond,
			MaxDelay:          500 * time.Millisecond,
			DummyTrafficRate:  0.1,
		},
		Proxy: ProxyConfig{
			Enabled:       true,
			HTTPPort:      8118,
			SOCKS5Port:    8119,
			HomepagePort:  8080,
			AllowExternal: false,
			BindAddress:   "127.0.0.1",
		},
		Crypto: CryptoConfig{
			Monero: MoneroConfig{
				Enabled:  true,
				RPCHost:  "127.0.0.1",
				RPCPort:  18082,
				Network:  "mainnet",
			},
			Zcash: ZcashConfig{
				Enabled:  true,
				RPCHost:  "127.0.0.1",
				RPCPort:  8232,
				Network:  "mainnet",
			},
		},
		Logging: LoggingConfig{
			Level:      "info",
			File:       "",
			MaxSize:    100,
			MaxBackups: 3,
			MaxAge:     30,
			Compress:   true,
		},
	}
}

// Bootstrap returns a configuration for a bootstrap node
func Bootstrap() *Config {
	cfg := Default()
	cfg.Node.Mode = "bootstrap"
	cfg.Node.Name = "kayaknet-bootstrap"
	cfg.Proxy.Enabled = false
	cfg.Security.RateLimitMessages = 1000 // Higher limit for bootstrap
	cfg.DHT.BootstrapNodes = []string{}   // Bootstrap doesn't need other bootstraps
	cfg.Logging.Level = "info"
	return cfg
}

// Vendor returns a configuration for a vendor node
func Vendor() *Config {
	cfg := Default()
	cfg.Node.Mode = "vendor"
	cfg.Node.Name = "kayaknet-vendor"
	cfg.Proxy.Enabled = true
	cfg.Crypto.Monero.Enabled = true
	cfg.Crypto.Zcash.Enabled = true
	cfg.Logging.Level = "info"
	return cfg
}

// User returns a configuration for a regular user node
func User() *Config {
	cfg := Default()
	cfg.Node.Mode = "user"
	cfg.Node.Name = "kayaknet-user"
	cfg.Proxy.Enabled = true
	cfg.Crypto.Monero.Enabled = false // Users don't need wallet by default
	cfg.Crypto.Zcash.Enabled = false
	cfg.Logging.Level = "warn"
	return cfg
}

// Production returns a hardened production configuration
func Production() *Config {
	cfg := Default()
	cfg.Security.RateLimitMessages = 50
	cfg.Security.BanThreshold = 5
	cfg.Security.MaxBanTime = 7 * 24 * time.Hour // 1 week max ban
	cfg.Logging.Level = "warn"
	cfg.Logging.Compress = true
	return cfg
}

// GetConfigDir returns the default config directory
func GetConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "./config"
	}
	return filepath.Join(home, ".kayaknet")
}

// GetDefaultConfigPath returns the default config file path
func GetDefaultConfigPath() string {
	return filepath.Join(GetConfigDir(), "config.json")
}

// EnsureConfigDir creates the config directory if it doesn't exist
func EnsureConfigDir() error {
	return os.MkdirAll(GetConfigDir(), 0700)
}

// Load loads configuration from a JSON file
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := Default()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Save saves configuration to a JSON file
func (c *Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
