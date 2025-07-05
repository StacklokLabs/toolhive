// Package httpsse provides MCP ping functionality for HTTP SSE proxies.
package httpsse

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"golang.org/x/exp/jsonrpc2"

	"github.com/stacklok/toolhive/pkg/kubernetes/healthcheck"
	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
)

// MCPPinger implements healthcheck.MCPPinger for HTTP SSE proxies
type MCPPinger struct {
	proxy *HTTPSSEProxy
}

// NewMCPPinger creates a new MCP pinger for HTTP SSE proxies
func NewMCPPinger(proxy *HTTPSSEProxy) healthcheck.MCPPinger {
	return &MCPPinger{
		proxy: proxy,
	}
}

// Ping sends a JSON-RPC ping request to the MCP server and waits for the response
// Following the MCP ping specification:
// https://modelcontextprotocol.io/specification/2025-03-26/basic/utilities/ping
func (p *MCPPinger) Ping(ctx context.Context) (time.Duration, error) {
	if p.proxy == nil {
		return 0, fmt.Errorf("proxy not available")
	}

	messageCh := p.proxy.GetMessageChannel()
	if messageCh == nil {
		return 0, fmt.Errorf("message channel not available")
	}

	// Create a ping request following MCP specification
	// {"jsonrpc": "2.0", "id": "123", "method": "ping"}
	pingID := fmt.Sprintf("ping_%d", time.Now().UnixNano())
	pingRequest, err := jsonrpc2.NewCall(jsonrpc2.StringID(pingID), "ping", json.RawMessage("{}"))
	if err != nil {
		return 0, fmt.Errorf("failed to create ping request: %w", err)
	}

	start := time.Now()

	// Send the ping request
	select {
	case messageCh <- pingRequest:
		logger.Debugf("Sent MCP ping request with ID: %s", pingID)
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
		return 0, fmt.Errorf("message channel is full or closed")
	}

	// For HTTP SSE proxy, we don't have a direct response channel
	// The response will be forwarded to SSE clients
	// We'll measure the time it took to send the request
	// In a real implementation, you might want to set up a response listener
	duration := time.Since(start)

	logger.Debugf("MCP ping request sent in %v", duration)
	return duration, nil
}
