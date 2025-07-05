package transport

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"golang.org/x/exp/jsonrpc2"

	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
)

// MockHTTPProxy is a mock implementation of types.Proxy
type MockHTTPProxy struct {
	mock.Mock
}

func (m *MockHTTPProxy) Start(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockHTTPProxy) Stop(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockHTTPProxy) GetMessageChannel() chan jsonrpc2.Message {
	args := m.Called()
	return args.Get(0).(chan jsonrpc2.Message)
}

func (m *MockHTTPProxy) ForwardResponseToClients(ctx context.Context, msg jsonrpc2.Message) error {
	args := m.Called(ctx, msg)
	return args.Error(0)
}

func (m *MockHTTPProxy) SendMessageToDestination(msg jsonrpc2.Message) error {
	args := m.Called(msg)
	return args.Error(0)
}

func TestSanitizeJSONString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    []byte
		expected string
	}{
		{
			name:     "valid JSON",
			input:    []byte(`{"jsonrpc": "2.0", "method": "test", "params": {}}`),
			expected: `{"jsonrpc": "2.0", "method": "test", "params": {}}`,
		},
		{
			name: "JSON with replacement character",
			input: []byte(
				`[` +
					`{` +
					string([]byte{0xEF, 0xBF, 0xBD}) + // U+FFFD
					`"jsonrpc": "2.0", "method": "test", "params": {"data": "test"}` +
					string([]byte{0xEF, 0xBF, 0xBD}) + // U+FFFD
					`}` +
					`]`),
			expected: `{"jsonrpc": "2.0", "method": "test", "params": {"data": "test"}}`,
		},
		{
			name:     "JSON with control characters",
			input:    []byte("\x01{\"jsonrpc\": \"2.0\", \"method\": \"test\", \"params\": {\"data\": \"test\"}\x01}"),
			expected: `{"jsonrpc": "2.0", "method": "test", "params": {"data": "test"}}`,
		},
		{
			name:     "empty array",
			input:    []byte(`[]`),
			expected: ``,
		},
		{
			name:     "invalid JSON",
			input:    []byte(`not a json`),
			expected: ``,
		},
		{
			name:     "JSON with extra content",
			input:    []byte(`extra{"jsonrpc": "2.0"}extra`),
			expected: `{"jsonrpc": "2.0"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fmt.Println(string(tt.input))
			result := sanitizeJSONString(string(tt.input))
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseAndForwardJSONRPC(t *testing.T) {
	t.Parallel()
	// Initialize logger for testing
	logger.Initialize()

	tests := []struct {
		name          string
		input         []byte
		shouldForward bool
	}{
		{
			name:          "valid JSON-RPC",
			input:         []byte(`{"jsonrpc": "2.0", "method": "test", "params": {}}`),
			shouldForward: true,
		},
		{
			name:          "empty array",
			input:         []byte(`[]`),
			shouldForward: false,
		},
		{
			name:          "empty string",
			input:         []byte(``),
			shouldForward: false,
		},
		{
			name: "JSON with replacement character",
			input: []byte(
				`{` +
					`"jsonrpc": "2.0", "method": "test", "params": {"data": "test"}` +
					string([]byte{0xEF, 0xBF, 0xBD}) + // U+FFFD
					`}`),
			shouldForward: true,
		},
		{
			name:          "JSON with control characters",
			input:         []byte("\x01{\"jsonrpc\": \"2.0\", \"method\": \"test\", \"params\": {\"data\": \"test\"}\x01}"),
			shouldForward: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Create mock HTTP proxy
			mockProxy := new(MockHTTPProxy)

			// Create transport with mock proxy
			transport := &StdioTransport{
				httpProxy: mockProxy,
			}

			// Set up expectations if the message should be forwarded
			if tt.shouldForward {
				mockProxy.On("ForwardResponseToClients", mock.Anything, mock.Anything).Return(nil)
			}

			// Call the function
			transport.parseAndForwardJSONRPC(context.Background(), string(tt.input))

			// Verify expectations
			mockProxy.AssertExpectations(t)
		})
	}
}

func TestIsSpace(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    rune
		expected bool
	}{
		{
			name:     "space character",
			input:    ' ',
			expected: true,
		},
		{
			name:     "newline character",
			input:    '\n',
			expected: true,
		},
		{
			name:     "tab character",
			input:    '\t',
			expected: false,
		},
		{
			name:     "carriage return",
			input:    '\r',
			expected: false,
		},
		{
			name:     "regular character",
			input:    'a',
			expected: false,
		},
		{
			name:     "number",
			input:    '1',
			expected: false,
		},
		{
			name:     "special character",
			input:    '@',
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := isSpace(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
