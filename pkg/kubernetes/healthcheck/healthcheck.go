// Package healthcheck provides common healthcheck functionality for ToolHive proxies.
// It includes MCP server status information and standardized response formats.
package healthcheck

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
	"github.com/stacklok/toolhive/pkg/kubernetes/versions"
)

// HealthStatus represents the overall health status
type HealthStatus string

const (
	// StatusHealthy indicates the service is healthy
	StatusHealthy HealthStatus = "healthy"
	// StatusUnhealthy indicates the service is unhealthy
	StatusUnhealthy HealthStatus = "unhealthy"
	// StatusDegraded indicates the service is partially healthy
	StatusDegraded HealthStatus = "degraded"
)

// MCPStatus represents the status of an MCP server connection
type MCPStatus struct {
	// Available indicates if the MCP server is reachable
	Available bool `json:"available"`
	// ResponseTime is the time taken to ping the MCP server (in milliseconds)
	ResponseTime *int64 `json:"response_time_ms,omitempty"`
	// Error contains any error message if the MCP server is not available
	Error string `json:"error,omitempty"`
	// LastChecked is the timestamp of the last health check
	LastChecked time.Time `json:"last_checked"`
}

// HealthResponse represents the standardized health check response
type HealthResponse struct {
	// Status is the overall health status
	Status HealthStatus `json:"status"`
	// Timestamp is when the health check was performed
	Timestamp time.Time `json:"timestamp"`
	// Version contains ToolHive version information
	Version versions.VersionInfo `json:"version"`
	// Transport indicates the type of transport used by the MCP server (stdio, sse)
	Transport string `json:"transport"`
	// MCP contains MCP server status information
	MCP *MCPStatus `json:"mcp,omitempty"`
}

// MCPPinger defines the interface for pinging MCP servers
// Implementations should follow the MCP ping specification:
// https://modelcontextprotocol.io/specification/2025-03-26/basic/utilities/ping
type MCPPinger interface {
	// Ping sends a ping request to the MCP server and returns the response time
	// The implementation should send a JSON-RPC request with method "ping" and no parameters,
	// and expect an empty response: {"jsonrpc": "2.0", "id": "123", "result": {}}
	Ping(ctx context.Context) (time.Duration, error)
}

// HealthChecker provides health check functionality for proxies
type HealthChecker struct {
	transport string
	mcpPinger MCPPinger
}

// NewHealthChecker creates a new health checker instance
func NewHealthChecker(transport string, mcpPinger MCPPinger) *HealthChecker {
	return &HealthChecker{
		transport: transport,
		mcpPinger: mcpPinger,
	}
}

// CheckHealth performs a comprehensive health check including MCP server status
func (hc *HealthChecker) CheckHealth(ctx context.Context) *HealthResponse {
	response := &HealthResponse{
		Status:    StatusHealthy,
		Timestamp: time.Now(),
		Version:   versions.GetVersionInfo(),
		Transport: hc.transport,
	}

	// Check MCP server status if pinger is available
	if hc.mcpPinger != nil {
		mcpStatus := hc.checkMCPStatus(ctx)
		response.MCP = mcpStatus

		// Adjust overall status based on MCP status
		if !mcpStatus.Available {
			response.Status = StatusDegraded
		}
	}

	return response
}

// checkMCPStatus checks the status of the MCP server using ping
func (hc *HealthChecker) checkMCPStatus(ctx context.Context) *MCPStatus {
	status := &MCPStatus{
		LastChecked: time.Now(),
	}

	// Create a context with timeout for the ping
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	duration, err := hc.mcpPinger.Ping(pingCtx)

	if err != nil {
		status.Available = false
		status.Error = err.Error()
		logger.Debugf("MCP ping failed: %v", err)
	} else {
		status.Available = true
		responseTimeMs := duration.Milliseconds()
		status.ResponseTime = &responseTimeMs
		logger.Debugf("MCP ping successful: %v", duration)
	}

	return status
}

// ServeHTTP implements http.Handler for health check endpoints
func (hc *HealthChecker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	health := hc.CheckHealth(r.Context())

	w.Header().Set("Content-Type", "application/json")

	// Set HTTP status code based on health status
	switch health.Status {
	case StatusHealthy:
		w.WriteHeader(http.StatusOK)
	case StatusDegraded:
		w.WriteHeader(http.StatusOK) // Still return 200 for degraded state
	case StatusUnhealthy:
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	if err := json.NewEncoder(w).Encode(health); err != nil {
		logger.Warnf("Failed to encode health response: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}
