// Package config handles configuration for KayakNet nodes
package config

import (
	"encoding/json"
	"os"
	"time"
)

// Config is the main configuration for a KayakNet node
type Config struct {
	Node      NodeConfig      `json:"node"`
	DHT       DHTConfig       `json:"dht"`
	PubSub    PubSubConfig    `json:"pubsub"`
	Transport TransportConfig `json:"transport"`
}

// NodeConfig configures the node identity and storage
type NodeConfig struct {
	Name         string `json:"name"`
	DataDir      string `json:"data_dir"`
	IdentityFile string `json:"identity_file"`
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
			DataDir:      "./data",
			IdentityFile: "./data/identity.json",
		},
		DHT: DHTConfig{
			K:               20,
			Alpha:           3,
			RecordTTL:       24 * time.Hour,
			RefreshInterval: time.Hour,
			BootstrapNodes:  []string{},
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
				Enabled:      true,
				ListenAddr:   "0.0.0.0:4242",
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
	}
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
