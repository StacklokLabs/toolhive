// Package streamable provides a streamable HTTP proxy for MCP servers.
package streamable

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/exp/jsonrpc2"

	"github.com/stacklok/toolhive/pkg/logger"
	"github.com/stacklok/toolhive/pkg/transport/types"
)

const (
	// StreamableHTTPEndpoint is the endpoint for streamable HTTP.
	StreamableHTTPEndpoint = "/mcp"
)

// HTTPProxy implements a proxy for streamable HTTP transport.
type HTTPProxy struct {
	host              string
	port              int
	containerName     string
	shutdownCh        chan struct{}
	prometheusHandler http.Handler
	middlewares       []types.Middleware

	// Message channel for sending JSON-RPC to the container
	messageCh chan jsonrpc2.Message
	// Response channel for receiving JSON-RPC from the container
	responseCh chan jsonrpc2.Message

	server *http.Server
}

// NewHTTPProxy creates a new HTTPProxy for streamable HTTP transport.
func NewHTTPProxy(
	host string,
	port int,
	containerName string,
	prometheusHandler http.Handler,
	middlewares ...types.Middleware,
) *HTTPProxy {
	return &HTTPProxy{
		host:              host,
		port:              port,
		containerName:     containerName,
		shutdownCh:        make(chan struct{}),
		prometheusHandler: prometheusHandler,
		middlewares:       middlewares,
		messageCh:         make(chan jsonrpc2.Message, 100),
		responseCh:        make(chan jsonrpc2.Message, 100),
	}
}

// Start starts the HTTPProxy server.
func (p *HTTPProxy) Start(_ context.Context) error {
	mux := http.NewServeMux()

	mux.Handle(StreamableHTTPEndpoint, p.applyMiddlewares(http.HandlerFunc(p.handleStreamableRequest)))

	if p.prometheusHandler != nil {
		mux.Handle("/metrics", p.prometheusHandler)
	}

	p.server = &http.Server{
		Addr:              fmt.Sprintf("%s:%d", p.host, p.port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.Infof("Streamable HTTP proxy started for container %s on port %d", p.containerName, p.port)
		logger.Infof("Streamable HTTP endpoint: http://%s:%d%s", p.host, p.port, StreamableHTTPEndpoint)
		if err := p.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Errorf("Streamable HTTP server error: %v", err)
		}
	}()

	return nil
}

// Stop gracefully shuts down the HTTPProxy server.
func (p *HTTPProxy) Stop(ctx context.Context) error {
	close(p.shutdownCh)
	if p.server != nil {
		return p.server.Shutdown(ctx)
	}
	return nil
}

// GetMessageChannel returns the message channel for sending JSON-RPC to the container.
func (p *HTTPProxy) GetMessageChannel() chan jsonrpc2.Message {
	return p.messageCh
}

// GetResponseChannel returns the response channel for receiving JSON-RPC from the container.
func (p *HTTPProxy) GetResponseChannel() <-chan jsonrpc2.Message {
	return p.responseCh
}

// SendMessageToDestination sends a message to the container.
func (p *HTTPProxy) SendMessageToDestination(msg jsonrpc2.Message) error {
	select {
	case p.messageCh <- msg:
		return nil
	default:
		return fmt.Errorf("failed to send message to destination")
	}
}

// ForwardResponseToClients forwards a response from the container to the client.
func (p *HTTPProxy) ForwardResponseToClients(_ context.Context, msg jsonrpc2.Message) error {
	select {
	case p.responseCh <- msg:
		return nil
	default:
		return fmt.Errorf("failed to forward response to client")
	}
}

// SendResponseMessage is for compatibility with the Proxy interface.
func (p *HTTPProxy) SendResponseMessage(msg jsonrpc2.Message) error {
	return p.ForwardResponseToClients(context.Background(), msg)
}

func (p *HTTPProxy) applyMiddlewares(handler http.Handler) http.Handler {
	for i := len(p.middlewares) - 1; i >= 0; i-- {
		handler = p.middlewares[i](handler)
	}
	return handler
}

func isNotification(msg jsonrpc2.Message) bool {
	if req, ok := msg.(*jsonrpc2.Request); ok {
		return req.ID.Raw() == nil
	}
	return false
}

// handleBatchRequest processes a batch JSON-RPC request and returns true if it handled the request.
func (p *HTTPProxy) handleBatchRequest(w http.ResponseWriter, body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return false
	}

	// Decode batch
	var rawMessages []json.RawMessage
	if err := json.Unmarshal(trimmed, &rawMessages); err != nil {
		logger.Warnf("Failed to decode batch JSON-RPC: %s", string(body))
		http.Error(w, "Invalid batch JSON-RPC", http.StatusBadRequest)
		return true
	}

	var responses []json.RawMessage
	for _, raw := range rawMessages {
		msg, err := jsonrpc2.DecodeMessage(raw)
		if err != nil {
			logger.Warnf("Skipping invalid message in batch: %s", string(raw))
			continue
		}

		// Send each message to the container
		if err := p.SendMessageToDestination(msg); err != nil {
			logger.Errorf("Failed to send message to destination: %v", err)
			continue
		}

		// Wait for response (sequential, can be improved for concurrency)
		select {
		case resp := <-p.responseCh:
			if r, ok := resp.(*jsonrpc2.Response); ok && r.ID.IsValid() {
				data, err := jsonrpc2.EncodeMessage(r)
				if err != nil {
					logger.Errorf("Failed to encode JSON-RPC response: %v", err)
					continue
				}
				responses = append(responses, data)
			}
		case <-time.After(10 * time.Second):
			logger.Warnf("Timeout waiting for response from container for batch message")
			// Optionally, append a JSON-RPC error response here
		}
	}

	// Write the batch response
	w.Header().Set("Content-Type", "application/json")
	if len(responses) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	respBytes, err := json.Marshal(responses)
	if err != nil {
		logger.Errorf("Failed to marshal batch response: %v", err)
		http.Error(w, "Failed to encode batch response", http.StatusInternalServerError)
		return true
	}
	if _, err := w.Write(respBytes); err != nil {
		logger.Errorf("Failed to write batch response: %v", err)
	}
	return true
}

// handleStreamableRequest handles HTTP POST requests to /mcp.
func (p *HTTPProxy) handleStreamableRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error reading request body: %v", err), http.StatusInternalServerError)
		return
	}

	if p.handleBatchRequest(w, body) {
		return
	}

	msg, ok := decodeJSONRPCMessage(w, body)
	if !ok {
		return
	}

	if p.handleNotificationOrResponse(w, msg) {
		return
	}

	p.handleRequestResponse(w, msg)
}

func (p *HTTPProxy) handleNotificationOrResponse(w http.ResponseWriter, msg jsonrpc2.Message) bool {
	if isNotification(msg) || (func() bool { _, ok := msg.(*jsonrpc2.Response); return ok })() {
		if err := p.SendMessageToDestination(msg); err != nil {
			logger.Errorf("Failed to send message to destination: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
		return true
	}
	return false
}

func (p *HTTPProxy) handleRequestResponse(w http.ResponseWriter, msg jsonrpc2.Message) {
	if err := p.SendMessageToDestination(msg); err != nil {
		http.Error(w, "Failed to send message to destination", http.StatusInternalServerError)
		return
	}
	select {
	case resp := <-p.responseCh:
		if r, ok := resp.(*jsonrpc2.Response); ok && r.ID.IsValid() {
			w.Header().Set("Content-Type", "application/json")
			data, err := jsonrpc2.EncodeMessage(r)
			if err != nil {
				logger.Errorf("Failed to encode JSON-RPC response: %v", err)
				http.Error(w, "Failed to encode response", http.StatusInternalServerError)
				return
			}
			if _, err := w.Write(data); err != nil {
				logger.Errorf("Failed to write response: %v", err)
			}
		} else {
			w.Header().Set("Content-Type", "application/json")
			errResp := getInvalidJsonrpcError()
			if err := json.NewEncoder(w).Encode(errResp); err != nil {
				logger.Errorf("Failed to encode error response: %v", err)
			}
		}
	case <-time.After(10 * time.Second):
		http.Error(w, "Timeout waiting for response from container", http.StatusGatewayTimeout)
	}
}

func getInvalidJsonrpcError() map[string]interface{} {
	errResp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      nil,
		"error": map[string]interface{}{
			"code":    -32603,
			"message": "Invalid JSON-RPC 2.0 response from server",
		},
	}
	return errResp
}

// decodeJSONRPCMessage decodes a JSON-RPC message from the request body.
func decodeJSONRPCMessage(w http.ResponseWriter, body []byte) (jsonrpc2.Message, bool) {
	msg, err := jsonrpc2.DecodeMessage(body)
	if err != nil {
		logger.Warnf("Skipping message that failed to decode: %s", string(body))
		http.Error(w, "Invalid JSON-RPC 2.0 message", http.StatusBadRequest)
		return nil, false
	}
	return msg, true
}
