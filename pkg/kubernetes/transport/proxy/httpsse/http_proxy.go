// Package httpsse provides an HTTP proxy implementation for Server-Sent Events (SSE)
// used in communication between the client and MCP server.
package httpsse

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/exp/jsonrpc2"

	"github.com/stacklok/toolhive/pkg/kubernetes/healthcheck"
	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
	"github.com/stacklok/toolhive/pkg/kubernetes/transport/ssecommon"
	"github.com/stacklok/toolhive/pkg/kubernetes/transport/types"
)

// Proxy defines the interface for proxying messages between clients and destinations.
type Proxy interface {
	// Start starts the proxy.
	Start(ctx context.Context) error

	// Stop stops the proxy.
	Stop(ctx context.Context) error

	// GetMessageChannel returns the channel for messages to/from the destination.
	GetMessageChannel() chan jsonrpc2.Message

	// GetResponseChannel returns the channel for receiving messages from the destination.
	GetResponseChannel() <-chan jsonrpc2.Message

	// SendMessageToDestination sends a message to the destination.
	SendMessageToDestination(msg jsonrpc2.Message) error

	// ForwardResponseToClients forwards a response from the destination to clients.
	ForwardResponseToClients(ctx context.Context, msg jsonrpc2.Message) error

	// SendResponseMessage sends a message to the response channel.
	SendResponseMessage(msg jsonrpc2.Message) error
}

// HTTPSSEProxy encapsulates the HTTP proxy functionality for SSE transports.
// It provides SSE endpoints and JSON-RPC message handling.
//
//nolint:revive // Intentionally named HTTPSSEProxy despite package name
type HTTPSSEProxy struct {
	// Basic configuration
	host          string
	port          int
	containerName string
	middlewares   []types.Middleware

	// HTTP server
	server     *http.Server
	shutdownCh chan struct{}

	// Optional Prometheus metrics handler
	prometheusHandler http.Handler

	// SSE clients
	sseClients      map[string]*ssecommon.SSEClient
	sseClientsMutex sync.Mutex

	// Pending messages for SSE clients
	pendingMessages []*ssecommon.PendingSSEMessage
	pendingMutex    sync.Mutex

	// Message channel
	messageCh chan jsonrpc2.Message

	// Health checker
	healthChecker *healthcheck.HealthChecker
}

// NewHTTPSSEProxy creates a new HTTP SSE proxy for transports.
func NewHTTPSSEProxy(
	host string, port int, containerName string, prometheusHandler http.Handler, middlewares ...types.Middleware,
) *HTTPSSEProxy {
	proxy := &HTTPSSEProxy{
		middlewares:       middlewares,
		host:              host,
		port:              port,
		containerName:     containerName,
		shutdownCh:        make(chan struct{}),
		messageCh:         make(chan jsonrpc2.Message, 100),
		sseClients:        make(map[string]*ssecommon.SSEClient),
		pendingMessages:   []*ssecommon.PendingSSEMessage{},
		prometheusHandler: prometheusHandler,
	}

	// Create MCP pinger and health checker
	mcpPinger := NewMCPPinger(proxy)
	proxy.healthChecker = healthcheck.NewHealthChecker("stdio", mcpPinger)

	return proxy
}

// applyMiddlewares applies a chain of middlewares to a handler
func applyMiddlewares(handler http.Handler, middlewares ...types.Middleware) http.Handler {
	// Apply middleware chain in reverse order (last middleware is applied first)
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}

// Start starts the HTTP SSE proxy.
func (p *HTTPSSEProxy) Start(_ context.Context) error {
	// Create a new HTTP server
	mux := http.NewServeMux()

	// Add handlers for SSE and JSON-RPC with middlewares
	// At some point we should add support for Streamable HTTP transport here
	// https://modelcontextprotocol.io/specification/2025-03-26/basic/transports#streamable-http
	mux.Handle(ssecommon.HTTPSSEEndpoint, applyMiddlewares(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
				return
			}
			p.handleSSEConnection(w, r)
		}),
		p.middlewares...,
	))

	mux.Handle(ssecommon.HTTPMessagesEndpoint, applyMiddlewares(http.HandlerFunc(p.handlePostRequest), p.middlewares...))

	// Add health check endpoint with MCP status (no middlewares)
	mux.Handle("/health", p.healthChecker)

	// Add Prometheus metrics endpoint if handler is provided (no middlewares)
	if p.prometheusHandler != nil {
		mux.Handle("/metrics", p.prometheusHandler)
		logger.Info("Prometheus metrics endpoint enabled at /metrics")
	}

	// Create the server
	p.server = &http.Server{
		Addr:              fmt.Sprintf("%s:%d", p.host, p.port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second, // Prevent Slowloris attacks
	}

	// Start the server in a goroutine
	go func() {
		logger.Infof("HTTP proxy started for container %s on port %d", p.containerName, p.port)
		logger.Infof("SSE endpoint: http://%s:%d%s", p.host, p.port, ssecommon.HTTPSSEEndpoint)
		logger.Infof("JSON-RPC endpoint: http://%s:%d%s", p.host, p.port, ssecommon.HTTPMessagesEndpoint)

		if err := p.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Errorf("HTTP server error: %v", err)
		}
	}()

	return nil
}

// Stop stops the HTTP SSE proxy.
func (p *HTTPSSEProxy) Stop(ctx context.Context) error {
	// Signal shutdown
	close(p.shutdownCh)

	// Stop the HTTP server
	if p.server != nil {
		return p.server.Shutdown(ctx)
	}

	return nil
}

// GetMessageChannel returns the channel for messages to/from the destination.
func (p *HTTPSSEProxy) GetMessageChannel() chan jsonrpc2.Message {
	return p.messageCh
}

// SendMessageToDestination sends a message to the destination via the message channel.
func (p *HTTPSSEProxy) SendMessageToDestination(msg jsonrpc2.Message) error {
	select {
	case p.messageCh <- msg:
		// Message sent successfully
		return nil
	default:
		// Channel is full or closed
		return fmt.Errorf("failed to send message to destination")
	}
}

// ForwardResponseToClients forwards a response from the destination to all connected SSE clients.
func (p *HTTPSSEProxy) ForwardResponseToClients(_ context.Context, msg jsonrpc2.Message) error {
	// Serialize the message to JSON
	data, err := jsonrpc2.EncodeMessage(msg)
	if err != nil {
		return fmt.Errorf("failed to encode JSON-RPC message: %w", err)
	}

	// Create an SSE message
	sseMsg := ssecommon.NewSSEMessage("message", string(data))

	// Check if there are any connected clients
	p.sseClientsMutex.Lock()
	hasClients := len(p.sseClients) > 0
	p.sseClientsMutex.Unlock()

	if hasClients {
		// Send the message to all connected clients
		return p.sendSSEEvent(sseMsg)
	}

	// Queue the message for later delivery
	p.pendingMutex.Lock()
	p.pendingMessages = append(p.pendingMessages, ssecommon.NewPendingSSEMessage(sseMsg))
	p.pendingMutex.Unlock()

	return nil
}

// handleSSEConnection handles an SSE connection.
func (p *HTTPSSEProxy) handleSSEConnection(w http.ResponseWriter, r *http.Request) {
	// Set headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Create a unique client ID
	clientID := uuid.New().String()

	// Create a channel for sending messages to this client
	messageCh := make(chan string, 100)

	// Register the client
	p.sseClientsMutex.Lock()
	p.sseClients[clientID] = &ssecommon.SSEClient{
		MessageCh: messageCh,
		CreatedAt: time.Now(),
	}
	p.sseClientsMutex.Unlock()

	// Process any pending messages for this client
	p.processPendingMessages(clientID, messageCh)

	// Create a flusher for SSE
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Get the base URL for the POST endpoint
	host := r.Host
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwardedProto := r.Header.Get("X-Forwarded-Proto"); forwardedProto != "" {
		scheme = forwardedProto
	}

	baseURL := fmt.Sprintf("%s://%s", scheme, host)

	// Create and send the endpoint event
	endpointURL := fmt.Sprintf("%s%s?session_id=%s", baseURL, ssecommon.HTTPMessagesEndpoint, clientID)
	endpointMsg := ssecommon.NewSSEMessage("endpoint", endpointURL)

	// Send the initial event
	fmt.Fprint(w, endpointMsg.ToSSEString())
	flusher.Flush()

	// Create a context that is canceled when the client disconnects
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Start keep-alive ticker
	keepAliveTicker := time.NewTicker(30 * time.Second)
	defer keepAliveTicker.Stop()

	// Create a goroutine to monitor for client disconnection
	go func() {
		<-ctx.Done()
		p.sseClientsMutex.Lock()
		delete(p.sseClients, clientID)
		p.sseClientsMutex.Unlock()
		close(messageCh)
		logger.Infof("Client %s disconnected", clientID)
	}()

	// Send messages to the client
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-messageCh:
			if !ok {
				return
			}
			fmt.Fprint(w, msg)
			flusher.Flush()
		case <-keepAliveTicker.C:
			// Send SSE comment as keep-alive
			fmt.Fprint(w, ": keep-alive\n\n")
			flusher.Flush()
		}
	}
}

// handlePostRequest handles a POST request with a JSON-RPC message.
func (p *HTTPSSEProxy) handlePostRequest(w http.ResponseWriter, r *http.Request) {
	// Only accept POST requests
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract session ID from query parameters
	query := r.URL.Query()
	sessionID := query.Get("session_id")
	if sessionID == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}

	// Check if the session exists
	p.sseClientsMutex.Lock()
	_, exists := p.sseClients[sessionID]
	p.sseClientsMutex.Unlock()

	if !exists {
		http.Error(w, "Could not find session", http.StatusNotFound)
		return
	}

	// Read the request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error reading request body: %v", err), http.StatusInternalServerError)
		return
	}

	// Parse the JSON-RPC message
	msg, err := jsonrpc2.DecodeMessage(body)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error parsing JSON-RPC message: %v", err), http.StatusBadRequest)
		return
	}

	// Log the message
	logger.Infof("Received JSON-RPC message: %T", msg)

	// Send the message to the destination
	if err := p.SendMessageToDestination(msg); err != nil {
		http.Error(w, "Failed to send message to destination", http.StatusInternalServerError)
		return
	}

	// Return a success response
	w.WriteHeader(http.StatusAccepted)
	if _, err := w.Write([]byte("Accepted")); err != nil {
		logger.Warnf("Warning: Failed to write response: %v", err)
	}
}

// sendSSEEvent sends an SSE event to all connected clients.
func (p *HTTPSSEProxy) sendSSEEvent(msg *ssecommon.SSEMessage) error {
	// Convert the message to an SSE-formatted string
	sseString := msg.ToSSEString()

	// Send to all clients
	p.sseClientsMutex.Lock()
	defer p.sseClientsMutex.Unlock()

	for clientID, client := range p.sseClients {
		select {
		case client.MessageCh <- sseString:
			// Message sent successfully
		default:
			// Channel is full or closed, remove the client
			delete(p.sseClients, clientID)
			close(client.MessageCh)
			logger.Infof("Client %s removed (channel full or closed)", clientID)
		}
	}

	return nil
}

// processPendingMessages processes any pending messages for a new client.
func (p *HTTPSSEProxy) processPendingMessages(clientID string, messageCh chan<- string) {
	p.pendingMutex.Lock()
	defer p.pendingMutex.Unlock()

	if len(p.pendingMessages) == 0 {
		return
	}

	// Find messages for this client (all messages for now)
	for _, pendingMsg := range p.pendingMessages {
		// Convert to SSE string
		sseString := pendingMsg.Message.ToSSEString()

		// Send to the client
		select {
		case messageCh <- sseString:
			// Message sent successfully
		default:
			// Channel is full, stop sending
			logger.Errorf("Failed to send pending message to client %s (channel full)", clientID)
			return
		}
	}

	// Clear the pending messages
	p.pendingMessages = nil
}
