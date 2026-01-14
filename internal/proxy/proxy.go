// Package proxy implements an HTTP/SOCKS5 proxy for browser access to KayakNet
// Users configure browser to use 127.0.0.1:8118 and can browse .kyk domains
package proxy

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultProxyPort is the default port for the browser proxy
	DefaultProxyPort = 8118

	// SOCKS5 constants
	SOCKS5Version     = 0x05
	SOCKS5AuthNone    = 0x00
	SOCKS5CmdConnect  = 0x01
	SOCKS5AddrIPv4    = 0x01
	SOCKS5AddrDomain  = 0x03
	SOCKS5AddrIPv6    = 0x04
	SOCKS5Success     = 0x00
	SOCKS5ConnRefused = 0x05
)

// NameResolver resolves .kyk domains to node addresses
type NameResolver interface {
	Resolve(domain string) (nodeID string, address string, err error)
}

// MessageSender sends messages through the KayakNet network
type MessageSender interface {
	SendToNode(nodeID string, data []byte) error
	SendHTTPRequest(nodeID string, req *http.Request) (*http.Response, error)
}

// Proxy provides browser access to KayakNet
type Proxy struct {
	mu           sync.RWMutex
	port         int
	resolver     NameResolver
	sender       MessageSender
	httpListener net.Listener
	socksListener net.Listener
	ctx          context.Context
	cancel       context.CancelFunc
	running      bool

	// Built-in homepage
	homepagePort int
	homepageAddr string

	// Stats
	requestCount  int64
	bytesIn       int64
	bytesOut      int64
}

// Config for the proxy
type Config struct {
	HTTPPort  int  // HTTP proxy port (default 8118)
	SOCKSPort int  // SOCKS5 proxy port (default 8119)
	Enabled   bool
}

// NewProxy creates a new browser proxy
func NewProxy(resolver NameResolver, sender MessageSender) *Proxy {
	ctx, cancel := context.WithCancel(context.Background())
	return &Proxy{
		port:         DefaultProxyPort,
		resolver:     resolver,
		sender:       sender,
		ctx:          ctx,
		cancel:       cancel,
		homepagePort: 8080,
		homepageAddr: "127.0.0.1:8080",
	}
}

// SetHomepagePort sets the port where the built-in homepage is running
func (p *Proxy) SetHomepagePort(port int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.homepagePort = port
	p.homepageAddr = fmt.Sprintf("127.0.0.1:%d", port)
}

// Start starts the proxy servers
func (p *Proxy) Start(httpPort, socksPort int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return fmt.Errorf("proxy already running")
	}

	// Start HTTP proxy
	httpAddr := fmt.Sprintf("127.0.0.1:%d", httpPort)
	httpListener, err := net.Listen("tcp", httpAddr)
	if err != nil {
		return fmt.Errorf("failed to start HTTP proxy: %w", err)
	}
	p.httpListener = httpListener

	// Start SOCKS5 proxy
	socksAddr := fmt.Sprintf("127.0.0.1:%d", socksPort)
	socksListener, err := net.Listen("tcp", socksAddr)
	if err != nil {
		httpListener.Close()
		return fmt.Errorf("failed to start SOCKS5 proxy: %w", err)
	}
	p.socksListener = socksListener

	p.running = true
	p.port = httpPort

	// Handle HTTP connections
	go p.serveHTTP()

	// Handle SOCKS5 connections
	go p.serveSOCKS5()

	log.Printf("[PROXY] HTTP proxy listening on %s", httpAddr)
	log.Printf("[PROXY] SOCKS5 proxy listening on %s", socksAddr)

	return nil
}

// Stop stops the proxy servers
func (p *Proxy) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running {
		return
	}

	p.cancel()
	if p.httpListener != nil {
		p.httpListener.Close()
	}
	if p.socksListener != nil {
		p.socksListener.Close()
	}
	p.running = false
}

// serveHTTP handles HTTP proxy requests
func (p *Proxy) serveHTTP() {
	for {
		conn, err := p.httpListener.Accept()
		if err != nil {
			select {
			case <-p.ctx.Done():
				return
			default:
				continue
			}
		}
		go p.handleHTTPConn(conn)
	}
}

// handleHTTPConn handles a single HTTP proxy connection
func (p *Proxy) handleHTTPConn(conn net.Conn) {
	defer conn.Close()

	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		return
	}

	// Handle CONNECT method (HTTPS tunneling)
	if req.Method == "CONNECT" {
		p.handleHTTPConnect(conn, req)
		return
	}

	// Check if this is a .kyk domain
	host := req.Host
	if strings.Contains(host, ":") {
		host = strings.Split(host, ":")[0]
	}

	if strings.HasSuffix(host, ".kyk") {
		p.handleKykRequest(conn, req)
		return
	}

	// For non-.kyk domains, return error (we only proxy KayakNet)
	resp := &http.Response{
		StatusCode: http.StatusForbidden,
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("KayakNet proxy only handles .kyk domains")),
	}
	resp.Header.Set("Content-Type", "text/plain")
	resp.Write(conn)
}

// handleHTTPConnect handles HTTPS CONNECT tunneling
func (p *Proxy) handleHTTPConnect(conn net.Conn, req *http.Request) {
	host := req.Host
	if strings.Contains(host, ":") {
		host = strings.Split(host, ":")[0]
	}

	if !strings.HasSuffix(host, ".kyk") {
		conn.Write([]byte("HTTP/1.1 403 Forbidden\r\n\r\nKayakNet proxy only handles .kyk domains"))
		return
	}

	// Resolve the .kyk domain
	nodeID, _, err := p.resolver.Resolve(host)
	if err != nil {
		conn.Write([]byte("HTTP/1.1 404 Not Found\r\n\r\nDomain not found: " + host))
		return
	}

	// Tell client we're connected
	conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	// Create tunnel through KayakNet
	p.tunnelToNode(conn, nodeID)
}

// handleKykRequest handles HTTP requests to .kyk domains
func (p *Proxy) handleKykRequest(conn net.Conn, req *http.Request) {
	host := req.Host
	if strings.Contains(host, ":") {
		host = strings.Split(host, ":")[0]
	}

	// Special handling for built-in .kyk domains
	switch host {
	case "home.kyk", "kayaknet.kyk":
		p.proxyToHomepage(conn, req)
		return
	case "chat.kyk":
		// Preserve API paths, only rewrite root to /chat
		if req.URL.Path == "/" || req.URL.Path == "" {
			req.URL.Path = "/chat"
		}
		p.proxyToHomepage(conn, req)
		return
	case "market.kyk", "marketplace.kyk":
		// Preserve API paths, only rewrite root to /marketplace
		if req.URL.Path == "/" || req.URL.Path == "" {
			req.URL.Path = "/marketplace"
		}
		p.proxyToHomepage(conn, req)
		return
	case "domains.kyk":
		// Preserve API paths, only rewrite root to /domains
		if req.URL.Path == "/" || req.URL.Path == "" {
			req.URL.Path = "/domains"
		}
		p.proxyToHomepage(conn, req)
		return
	case "network.kyk":
		// Preserve API paths, only rewrite root to /network
		if req.URL.Path == "/" || req.URL.Path == "" {
			req.URL.Path = "/network"
		}
		p.proxyToHomepage(conn, req)
		return
	}

	// Resolve the .kyk domain
	nodeID, _, err := p.resolver.Resolve(host)
	if err != nil {
		resp := &http.Response{
			StatusCode: http.StatusNotFound,
			ProtoMajor: 1,
			ProtoMinor: 1,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("Domain not found: " + host)),
		}
		resp.Header.Set("Content-Type", "text/plain")
		resp.Write(conn)
		return
	}

	// Forward request through KayakNet
	resp, err := p.sender.SendHTTPRequest(nodeID, req)
	if err != nil {
		resp = &http.Response{
			StatusCode: http.StatusBadGateway,
			ProtoMajor: 1,
			ProtoMinor: 1,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("Failed to reach node: " + err.Error())),
		}
		resp.Header.Set("Content-Type", "text/plain")
	}

	resp.Write(conn)
	p.mu.Lock()
	p.requestCount++
	p.mu.Unlock()
}

// proxyToHomepage forwards requests to the built-in homepage server
func (p *Proxy) proxyToHomepage(conn net.Conn, req *http.Request) {
	p.mu.RLock()
	addr := p.homepageAddr
	p.mu.RUnlock()

	// Connect to the local homepage server
	targetConn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		resp := &http.Response{
			StatusCode: http.StatusBadGateway,
			ProtoMajor: 1,
			ProtoMinor: 1,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("Homepage server not running")),
		}
		resp.Header.Set("Content-Type", "text/plain")
		resp.Write(conn)
		return
	}
	defer targetConn.Close()

	// Modify request to target local server
	req.Host = addr
	req.URL.Host = addr

	// Forward the request
	if err := req.Write(targetConn); err != nil {
		return
	}

	// Read and forward the response
	reader := bufio.NewReader(targetConn)
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	resp.Write(conn)
	p.mu.Lock()
	p.requestCount++
	p.mu.Unlock()
}

// tunnelToNode creates a bidirectional tunnel to a KayakNet node
func (p *Proxy) tunnelToNode(conn net.Conn, nodeID string) {
	// This would create an encrypted tunnel through the onion network
	// For now, we'll implement basic forwarding
	// In production, this tunnels through the onion router

	// Read from conn, encrypt, send to node
	// Read from node, decrypt, send to conn
	buf := make([]byte, 32*1024)
	for {
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, err := conn.Read(buf)
		if err != nil {
			return
		}

		// Send through KayakNet
		if err := p.sender.SendToNode(nodeID, buf[:n]); err != nil {
			return
		}

		p.mu.Lock()
		p.bytesOut += int64(n)
		p.mu.Unlock()
	}
}

// serveSOCKS5 handles SOCKS5 proxy requests
func (p *Proxy) serveSOCKS5() {
	for {
		conn, err := p.socksListener.Accept()
		if err != nil {
			select {
			case <-p.ctx.Done():
				return
			default:
				continue
			}
		}
		go p.handleSOCKS5Conn(conn)
	}
}

// handleSOCKS5Conn handles a SOCKS5 connection
func (p *Proxy) handleSOCKS5Conn(conn net.Conn) {
	defer conn.Close()

	// Read version and auth methods
	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil || n < 2 {
		return
	}

	if buf[0] != SOCKS5Version {
		return
	}

	// Send auth response (no auth required)
	conn.Write([]byte{SOCKS5Version, SOCKS5AuthNone})

	// Read connect request
	n, err = conn.Read(buf)
	if err != nil || n < 7 {
		return
	}

	if buf[0] != SOCKS5Version || buf[1] != SOCKS5CmdConnect {
		p.sendSOCKS5Error(conn, SOCKS5ConnRefused)
		return
	}

	// Parse address
	var host string
	var port uint16

	switch buf[3] {
	case SOCKS5AddrIPv4:
		if n < 10 {
			return
		}
		host = fmt.Sprintf("%d.%d.%d.%d", buf[4], buf[5], buf[6], buf[7])
		port = binary.BigEndian.Uint16(buf[8:10])

	case SOCKS5AddrDomain:
		domainLen := int(buf[4])
		if n < 5+domainLen+2 {
			return
		}
		host = string(buf[5 : 5+domainLen])
		port = binary.BigEndian.Uint16(buf[5+domainLen : 7+domainLen])

	case SOCKS5AddrIPv6:
		if n < 22 {
			return
		}
		// IPv6 - not commonly used with .kyk
		p.sendSOCKS5Error(conn, SOCKS5ConnRefused)
		return

	default:
		p.sendSOCKS5Error(conn, SOCKS5ConnRefused)
		return
	}

	// Check if .kyk domain
	if !strings.HasSuffix(host, ".kyk") {
		p.sendSOCKS5Error(conn, SOCKS5ConnRefused)
		return
	}

	// Resolve domain
	nodeID, _, err := p.resolver.Resolve(host)
	if err != nil {
		p.sendSOCKS5Error(conn, SOCKS5ConnRefused)
		return
	}

	// Send success response
	resp := []byte{
		SOCKS5Version, SOCKS5Success, 0x00, SOCKS5AddrIPv4,
		127, 0, 0, 1, // Bind address (localhost)
		0, 0, // Bind port
	}
	binary.BigEndian.PutUint16(resp[8:10], port)
	conn.Write(resp)

	// Tunnel the connection
	p.tunnelToNode(conn, nodeID)
}

// sendSOCKS5Error sends a SOCKS5 error response
func (p *Proxy) sendSOCKS5Error(conn net.Conn, errCode byte) {
	conn.Write([]byte{
		SOCKS5Version, errCode, 0x00, SOCKS5AddrIPv4,
		0, 0, 0, 0, 0, 0,
	})
}

// Stats returns proxy statistics
func (p *Proxy) Stats() (requests int64, bytesIn, bytesOut int64) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.requestCount, p.bytesIn, p.bytesOut
}

// IsRunning returns whether the proxy is running
func (p *Proxy) IsRunning() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.running
}

// Port returns the HTTP proxy port
func (p *Proxy) Port() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.port
}

// SimpleResolver is a basic resolver for testing
type SimpleResolver struct {
	domains map[string]struct {
		NodeID  string
		Address string
	}
	mu sync.RWMutex
}

// NewSimpleResolver creates a simple resolver
func NewSimpleResolver() *SimpleResolver {
	return &SimpleResolver{
		domains: make(map[string]struct {
			NodeID  string
			Address string
		}),
	}
}

// Add adds a domain mapping
func (r *SimpleResolver) Add(domain, nodeID, address string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.domains[domain] = struct {
		NodeID  string
		Address string
	}{nodeID, address}
}

// Resolve resolves a domain
func (r *SimpleResolver) Resolve(domain string) (string, string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if !strings.HasSuffix(domain, ".kyk") {
		domain = domain + ".kyk"
	}

	entry, ok := r.domains[domain]
	if !ok {
		return "", "", fmt.Errorf("domain not found: %s", domain)
	}
	return entry.NodeID, entry.Address, nil
}

// WebServer is a simple web server that runs on a KayakNet node
// This allows nodes to host websites accessible via .kyk domains
type WebServer struct {
	mu       sync.RWMutex
	handler  http.Handler
	listener net.Listener
	nodeID   string
	pubKey   ed25519.PublicKey
}

// NewWebServer creates a web server for a KayakNet node
func NewWebServer(nodeID string, pubKey ed25519.PublicKey, handler http.Handler) *WebServer {
	return &WebServer{
		nodeID:  nodeID,
		pubKey:  pubKey,
		handler: handler,
	}
}

// Start starts the web server on an internal port
func (w *WebServer) Start(port int) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	w.listener = listener

	go http.Serve(listener, w.handler)
	return nil
}

// Stop stops the web server
func (w *WebServer) Stop() {
	if w.listener != nil {
		w.listener.Close()
	}
}

// SimpleWebHandler creates a simple static website handler
func SimpleWebHandler(title, content string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
    <title>%s</title>
    <style>
        body {
            font-family: monospace;
            background: #0a0a0a;
            color: #0f0;
            padding: 40px;
            max-width: 800px;
            margin: 0 auto;
        }
        h1 { color: #0ff; }
        .info { color: #888; font-size: 12px; }
    </style>
</head>
<body>
    <h1>%s</h1>
    <p>%s</p>
    <p class="info">Served via KayakNet</p>
</body>
</html>`, title, title, content)
	})
}

// FileServerHandler creates a file server handler
func FileServerHandler(root string) http.Handler {
	return http.FileServer(http.Dir(root))
}

// ProxyInfo returns setup instructions for browsers
func ProxyInfo(httpPort, socksPort int) string {
	return fmt.Sprintf(`
================================================================================
                    KayakNet Browser Proxy Setup
================================================================================

HTTP Proxy:   127.0.0.1:%d
SOCKS5 Proxy: 127.0.0.1:%d

BROWSER SETUP:

Firefox:
  1. Settings > Network Settings > Settings
  2. Select "Manual proxy configuration"
  3. HTTP Proxy: 127.0.0.1  Port: %d
  4. Check "Also use this proxy for HTTPS"
  5. Click OK

Chrome (use extension like "Proxy SwitchyOmega"):
  1. Install Proxy SwitchyOmega extension
  2. Create new profile "KayakNet"
  3. Set HTTP/HTTPS proxy to 127.0.0.1:%d
  4. Apply changes
  5. Click extension icon > Select "KayakNet"

System-wide (Linux):
  export http_proxy=http://127.0.0.1:%d
  export https_proxy=http://127.0.0.1:%d

After setup, browse to any .kyk domain:
  http://example.kyk
  http://market.kyk
  http://chat.kyk

================================================================================
`, httpPort, socksPort, httpPort, httpPort, httpPort, httpPort)
}

