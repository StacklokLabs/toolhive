package healthcheck

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
	"github.com/stacklok/toolhive/pkg/kubernetes/versions"
)

// mockMCPPinger implements MCPPinger for testing
type mockMCPPinger struct {
	pingDuration time.Duration
	pingError    error
}

func (m *mockMCPPinger) Ping(_ context.Context) (time.Duration, error) {
	if m.pingError != nil {
		return m.pingDuration, m.pingError
	}
	return m.pingDuration, nil
}

func TestHealthChecker_CheckHealth(t *testing.T) {
	t.Parallel()

	// Initialize logger for tests
	logger.Initialize()

	tests := []struct {
		name              string
		transport         string
		pinger            MCPPinger
		expectedStatus    HealthStatus
		expectedMCPStatus bool
	}{
		{
			name:              "healthy with successful MCP ping",
			transport:         "stdio",
			pinger:            &mockMCPPinger{pingDuration: 50 * time.Millisecond},
			expectedStatus:    StatusHealthy,
			expectedMCPStatus: true,
		},
		{
			name:              "degraded with failed MCP ping",
			transport:         "sse",
			pinger:            &mockMCPPinger{pingDuration: 100 * time.Millisecond, pingError: assert.AnError},
			expectedStatus:    StatusDegraded,
			expectedMCPStatus: false,
		},
		{
			name:              "healthy without MCP pinger",
			transport:         "stdio",
			pinger:            nil,
			expectedStatus:    StatusHealthy,
			expectedMCPStatus: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			hc := NewHealthChecker(tt.transport, tt.pinger)

			ctx := context.Background()
			health := hc.CheckHealth(ctx)

			assert.Equal(t, tt.expectedStatus, health.Status)
			assert.Equal(t, tt.transport, health.Transport)
			assert.NotEmpty(t, health.Version.Version)
			assert.WithinDuration(t, time.Now(), health.Timestamp, 1*time.Second)

			if tt.pinger != nil {
				require.NotNil(t, health.MCP)
				assert.Equal(t, tt.expectedMCPStatus, health.MCP.Available)
				assert.WithinDuration(t, time.Now(), health.MCP.LastChecked, 1*time.Second)

				if tt.expectedMCPStatus {
					assert.NotNil(t, health.MCP.ResponseTime)
					assert.Greater(t, *health.MCP.ResponseTime, int64(0))
					assert.Empty(t, health.MCP.Error)
				} else {
					assert.NotEmpty(t, health.MCP.Error)
				}
			} else {
				assert.Nil(t, health.MCP)
			}
		})
	}
}

func TestHealthChecker_ServeHTTP(t *testing.T) {
	t.Parallel()

	// Initialize logger for tests
	logger.Initialize()

	tests := []struct {
		name           string
		method         string
		pinger         MCPPinger
		expectedStatus int
		expectedBody   func(t *testing.T, body []byte)
	}{
		{
			name:           "GET request with healthy status",
			method:         http.MethodGet,
			pinger:         &mockMCPPinger{pingDuration: 50 * time.Millisecond},
			expectedStatus: http.StatusOK,
			expectedBody: func(t *testing.T, body []byte) {
				t.Helper()
				var response HealthResponse
				err := json.Unmarshal(body, &response)
				require.NoError(t, err)
				assert.Equal(t, StatusHealthy, response.Status)
				assert.Equal(t, "stdio", response.Transport)
				assert.NotEmpty(t, response.Version.Version)
				assert.NotNil(t, response.MCP)
				assert.True(t, response.MCP.Available)
			},
		},
		{
			name:           "GET request with degraded status",
			method:         http.MethodGet,
			pinger:         &mockMCPPinger{pingDuration: 100 * time.Millisecond, pingError: assert.AnError},
			expectedStatus: http.StatusOK, // Still 200 for degraded
			expectedBody: func(t *testing.T, body []byte) {
				t.Helper()
				var response HealthResponse
				err := json.Unmarshal(body, &response)
				require.NoError(t, err)
				assert.Equal(t, StatusDegraded, response.Status)
				assert.NotEmpty(t, response.Version.Version)
				assert.NotNil(t, response.MCP)
				assert.False(t, response.MCP.Available)
			},
		},
		{
			name:           "POST request should return method not allowed",
			method:         http.MethodPost,
			pinger:         &mockMCPPinger{pingDuration: 50 * time.Millisecond},
			expectedStatus: http.StatusMethodNotAllowed,
			expectedBody: func(t *testing.T, body []byte) {
				t.Helper()
				assert.Contains(t, string(body), "Method not allowed")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			hc := NewHealthChecker("stdio", tt.pinger)

			req := httptest.NewRequest(tt.method, "/health", nil)
			w := httptest.NewRecorder()

			hc.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectedBody != nil {
				tt.expectedBody(t, w.Body.Bytes())
			}

			if tt.expectedStatus == http.StatusOK {
				assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
			}
		})
	}
}

func TestHealthResponse_JSON(t *testing.T) {
	t.Parallel()

	// Initialize logger for tests
	logger.Initialize()

	response := &HealthResponse{
		Status:    StatusHealthy,
		Timestamp: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		Version:   versions.GetVersionInfo(),
		Transport: "stdio",
		MCP: &MCPStatus{
			Available:    true,
			ResponseTime: func() *int64 { v := int64(50); return &v }(),
			LastChecked:  time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		},
	}

	data, err := json.Marshal(response)
	require.NoError(t, err)

	var unmarshaled HealthResponse
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)

	assert.Equal(t, response.Status, unmarshaled.Status)
	assert.Equal(t, response.Transport, unmarshaled.Transport)
	assert.Equal(t, response.Version.Version, unmarshaled.Version.Version)
	assert.True(t, response.Timestamp.Equal(unmarshaled.Timestamp))

	require.NotNil(t, unmarshaled.MCP)
	assert.Equal(t, response.MCP.Available, unmarshaled.MCP.Available)
	assert.Equal(t, *response.MCP.ResponseTime, *unmarshaled.MCP.ResponseTime)
	assert.True(t, response.MCP.LastChecked.Equal(unmarshaled.MCP.LastChecked))
}
