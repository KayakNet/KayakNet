// Package main implements the KayakNet CLI tool for pure P2P operations
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/kayaknet/kayaknet/internal/cap"
	"github.com/kayaknet/kayaknet/internal/identity"
	"github.com/spf13/cobra"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "kayakctl",
		Short: "KayakNet P2P network CLI",
		Long: `KayakNet CLI - Control your KayakNet node

This is a pure P2P network - no central servers.
All operations happen directly between nodes.`,
	}

	rootCmd.AddCommand(identityCmd())
	rootCmd.AddCommand(capabilityCmd())
	rootCmd.AddCommand(pingCmd())
	rootCmd.AddCommand(nodeCmd())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Identity commands
func identityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "identity",
		Short: "Manage node identity",
	}

	// Generate new identity
	generateCmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate a new node identity",
		RunE: func(cmd *cobra.Command, args []string) error {
			output, _ := cmd.Flags().GetString("output")

			id, err := identity.New()
			if err != nil {
				return fmt.Errorf("failed to generate identity: %w", err)
			}

			fmt.Println("╔════════════════════════════════════════════════════════════╗")
			fmt.Println("║              New KayakNet Identity Generated                ║")
			fmt.Println("╠════════════════════════════════════════════════════════════╣")
			fmt.Printf("║  Node ID:     %s...  ║\n", id.NodeID()[:32])
			fmt.Println("╚════════════════════════════════════════════════════════════╝")
			fmt.Println()
			fmt.Println("Public Key:")
			fmt.Println(" ", id.PublicKeyHex())
			fmt.Println()

			if output != "" {
				dir := filepath.Dir(output)
				if err := os.MkdirAll(dir, 0700); err != nil {
					return fmt.Errorf("failed to create directory: %w", err)
				}
				if err := id.Save(output); err != nil {
					return fmt.Errorf("failed to save identity: %w", err)
				}
				fmt.Printf("[OK] Identity saved to: %s\n", output)
			} else {
				fmt.Println("[WARN]  Identity not saved. Use --output to save.")
			}

			return nil
		},
	}
	generateCmd.Flags().StringP("output", "o", "", "Output file path")
	cmd.AddCommand(generateCmd)

	// Show identity
	showCmd := &cobra.Command{
		Use:   "show <identity-file>",
		Short: "Show identity details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := identity.Load(args[0])
			if err != nil {
				return fmt.Errorf("failed to load identity: %w", err)
			}

			fmt.Println("Node ID:   ", id.NodeID())
			fmt.Println("Public Key:", id.PublicKeyHex())
			fmt.Println("Created:   ", id.CreatedAt().Format(time.RFC3339))

			return nil
		},
	}
	cmd.AddCommand(showCmd)

	return cmd
}

// Capability commands
func capabilityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cap",
		Short: "Manage capabilities",
	}

	// Issue capability
	issueCmd := &cobra.Command{
		Use:   "issue",
		Short: "Issue a new capability",
		RunE: func(cmd *cobra.Command, args []string) error {
			identityFile, _ := cmd.Flags().GetString("identity")
			serviceID, _ := cmd.Flags().GetString("service")
			granteePubKey, _ := cmd.Flags().GetString("grantee")
			permissions, _ := cmd.Flags().GetStringSlice("permissions")
			ttlHours, _ := cmd.Flags().GetInt("ttl")

			// Load issuer identity
			id, err := identity.Load(identityFile)
			if err != nil {
				return fmt.Errorf("failed to load identity: %w", err)
			}

			// Parse grantee public key
			granteeBytes, err := hex.DecodeString(granteePubKey)
			if err != nil {
				return fmt.Errorf("invalid grantee public key: %w", err)
			}

			// Create capability
			ttl := time.Duration(ttlHours) * time.Hour
			capability := cap.NewCapability(serviceID, granteeBytes, permissions, ttl)

			// Sign with issuer's key (simplified - in real impl need private key access)
			// For now we'll use the identity's sign function
			if err := capability.Sign(ed25519.PrivateKey(id.PublicKey()), id.PublicKey()); err != nil {
				return fmt.Errorf("failed to sign capability: %w", err)
			}

			// Output as JSON
			data, err := capability.Marshal()
			if err != nil {
				return fmt.Errorf("failed to marshal capability: %w", err)
			}

			fmt.Println(string(data))
			return nil
		},
	}
	issueCmd.Flags().StringP("identity", "i", "./data/identity.json", "Issuer identity file")
	issueCmd.Flags().StringP("service", "s", "", "Service ID")
	issueCmd.Flags().StringP("grantee", "g", "", "Grantee public key (hex)")
	issueCmd.Flags().StringSliceP("permissions", "p", []string{"read"}, "Permissions")
	issueCmd.Flags().Int("ttl", 24, "TTL in hours")
	issueCmd.MarkFlagRequired("service")
	issueCmd.MarkFlagRequired("grantee")
	cmd.AddCommand(issueCmd)

	// Verify capability
	verifyCmd := &cobra.Command{
		Use:   "verify <capability-json>",
		Short: "Verify a capability",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			capability, err := cap.Unmarshal([]byte(args[0]))
			if err != nil {
				return fmt.Errorf("invalid capability JSON: %w", err)
			}

			if err := capability.Verify(); err != nil {
				fmt.Println("[ERR] Invalid signature:", err)
				return nil
			}

			if capability.IsExpired() {
				fmt.Println("[WARN]  Capability is expired")
				return nil
			}

			fmt.Println("[OK] Capability is valid")
			fmt.Println("   Service:    ", capability.ServiceID)
			fmt.Println("   Permissions:", capability.Permissions)
			fmt.Println("   Expires:    ", capability.ExpiresAt.Format(time.RFC3339))

			return nil
		},
	}
	cmd.AddCommand(verifyCmd)

	return cmd
}

// Ping command
func pingCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ping <address:port>",
		Short: "Ping a KayakNet node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			addr, err := net.ResolveUDPAddr("udp", args[0])
			if err != nil {
				return fmt.Errorf("invalid address: %w", err)
			}

			conn, err := net.DialUDP("udp", nil, addr)
			if err != nil {
				return fmt.Errorf("failed to connect: %w", err)
			}
			defer conn.Close()

			// Generate temporary identity for ping
			pub, _, _ := ed25519.GenerateKey(rand.Reader)

			msg := struct {
				Type    byte   `json:"type"`
				From    string `json:"from"`
				FromKey []byte `json:"from_key"`
				Ts      int64  `json:"ts"`
				Payload string `json:"payload"`
			}{
				Type:    0x01, // Ping
				From:    hex.EncodeToString(pub[:20]),
				FromKey: pub,
				Ts:      time.Now().UnixNano(),
				Payload: "ping",
			}

			data, _ := json.Marshal(msg)
			start := time.Now()

			_, err = conn.Write(data)
			if err != nil {
				return fmt.Errorf("failed to send: %w", err)
			}

			// Wait for response
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			buf := make([]byte, 65535)
			n, err := conn.Read(buf)
			if err != nil {
				return fmt.Errorf("no response (timeout): %w", err)
			}

			rtt := time.Since(start)

			var resp struct {
				Type byte   `json:"type"`
				From string `json:"from"`
			}
			json.Unmarshal(buf[:n], &resp)

			fmt.Printf("[OK] Pong from %s... (RTT: %s)\n", resp.From[:16], rtt.Round(time.Microsecond))
			return nil
		},
	}
}

// Node commands
func nodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "Node operations",
	}

	// Start node (just shows how to start)
	cmd.AddCommand(&cobra.Command{
		Use:   "start",
		Short: "Show how to start a node",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("To start a KayakNet node:")
			fmt.Println()
			fmt.Println("  # Start a new node (interactive mode)")
			fmt.Println("  kayakd -i --data-dir ./mynode --listen 0.0.0.0:4242")
			fmt.Println()
			fmt.Println("  # Connect to an existing network")
			fmt.Println("  kayakd -i --bootstrap 192.168.1.100:4242")
			fmt.Println()
			fmt.Println("  # With a custom name")
			fmt.Println("  kayakd -i --name \"my-node\" --listen 0.0.0.0:4242")
		},
	})

	return cmd
}
