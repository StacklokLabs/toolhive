package transport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/exp/jsonrpc2"

	"github.com/stacklok/toolhive/pkg/container"
	rt "github.com/stacklok/toolhive/pkg/container/runtime"
	"github.com/stacklok/toolhive/pkg/logger"
	"github.com/stacklok/toolhive/pkg/permissions"
	"github.com/stacklok/toolhive/pkg/transport/errors"
	"github.com/stacklok/toolhive/pkg/transport/proxy/httpsse"
	"github.com/stacklok/toolhive/pkg/transport/proxy/streamable"
	"github.com/stacklok/toolhive/pkg/transport/types"
)

// StdioTransport implements the Transport interface using standard input/output.
// It acts as a proxy between the MCP client and the container's stdin/stdout.
type StdioTransport struct {
	host              string
	port              int
	containerID       string
	containerName     string
	runtime           rt.Runtime
	debug             bool
	middlewares       []types.Middleware
	prometheusHandler http.Handler

	// Mutex for protecting shared state
	mutex sync.Mutex

	// Channels for communication
	shutdownCh chan struct{}
	errorCh    <-chan error

	// Proxy (SSE or Streamable HTTP)
	httpProxy types.Proxy
	proxyMode types.ProxyMode

	// Container I/O
	stdin  io.WriteCloser
	stdout io.ReadCloser

	// Container monitor
	monitor rt.Monitor
}

// NewStdioTransport creates a new stdio transport.
func NewStdioTransport(
	host string,
	port int,
	runtime rt.Runtime,
	debug bool,
	prometheusHandler http.Handler,
	middlewares ...types.Middleware,
) *StdioTransport {
	return &StdioTransport{
		host:              host,
		port:              port,
		runtime:           runtime,
		debug:             debug,
		middlewares:       middlewares,
		prometheusHandler: prometheusHandler,
		shutdownCh:        make(chan struct{}),
		proxyMode:         types.ProxyModeSSE, // default to SSE for backward compatibility
	}
}

// SetProxyMode allows configuring the proxy mode (SSE or Streamable HTTP)
func (t *StdioTransport) SetProxyMode(mode types.ProxyMode) {
	t.proxyMode = mode
}

// Mode returns the transport mode.
func (*StdioTransport) Mode() types.TransportType {
	return types.TransportTypeStdio
}

// Port returns the port used by the transport.
func (t *StdioTransport) Port() int {
	return t.port
}

// Setup prepares the transport for use.
func (t *StdioTransport) Setup(
	ctx context.Context,
	runtime rt.Runtime,
	containerName string,
	image string,
	cmdArgs []string,
	envVars, labels map[string]string,
	permissionProfile *permissions.Profile,
	k8sPodTemplatePatch string,
	isolateNetwork bool,
) error {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	t.runtime = runtime
	t.containerName = containerName

	// Add transport-specific environment variables
	envVars["MCP_TRANSPORT"] = "stdio"

	// Create workload options
	containerOptions := rt.NewDeployWorkloadOptions()
	containerOptions.AttachStdio = true
	containerOptions.K8sPodTemplatePatch = k8sPodTemplatePatch

	// Create the container
	logger.Infof("Deploying workload %s from image %s...", containerName, image)
	containerID, _, err := t.runtime.DeployWorkload(
		ctx,
		image,
		containerName,
		cmdArgs,
		envVars,
		labels,
		permissionProfile,
		"stdio",
		containerOptions,
		isolateNetwork,
	)
	if err != nil {
		return fmt.Errorf("failed to create container: %v", err)
	}
	t.containerID = containerID
	logger.Infof("Container created with ID: %s", containerID)

	return nil
}

// Start initializes the transport and begins processing messages.
// The transport is responsible for starting the container and attaching to it.
func (t *StdioTransport) Start(ctx context.Context) error {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	// logger.Infof("Starting stdio transport with proxy mode: %s", t.proxyMode)
	fmt.Printf("DEBUG: Starting stdio transport with proxy mode: %s\n", t.proxyMode)

	if t.containerID == "" {
		return errors.ErrContainerIDNotSet
	}

	if t.containerName == "" {
		return errors.ErrContainerNameNotSet
	}

	if t.runtime == nil {
		return fmt.Errorf("container runtime not set")
	}

	// Attach to the container
	var err error
	t.stdin, t.stdout, err = t.runtime.AttachToWorkload(ctx, t.containerID)
	if err != nil {
		return fmt.Errorf("failed to attach to container: %w", err)
	}

	// Create and start the correct proxy with middlewares
	switch t.proxyMode {
	case types.ProxyModeStreamableHTTP:
		t.httpProxy = streamable.NewHTTPProxy(t.host, t.port, t.containerName, t.prometheusHandler, t.middlewares...)
		if err := t.httpProxy.Start(ctx); err != nil {
			return err
		}
		logger.Info("Streamable HTTP proxy started, processing messages...")
	case types.ProxyModeSSE:
		t.httpProxy = httpsse.NewHTTPSSEProxy(t.host, t.port, t.containerName, t.prometheusHandler, t.middlewares...)
		if err := t.httpProxy.Start(ctx); err != nil {
			return err
		}
		logger.Info("HTTP SSE proxy started, processing messages...")
	default:
		return fmt.Errorf("unsupported proxy mode: %v", t.proxyMode)
	}

	// Start processing messages in a goroutine
	go t.processMessages(ctx, t.stdin, t.stdout)

	// Create a container monitor
	monitorRuntime, err := container.NewFactory().Create(ctx)
	if err != nil {
		return fmt.Errorf("failed to create container monitor: %v", err)
	}
	t.monitor = container.NewMonitor(monitorRuntime, t.containerID, t.containerName)

	// Start monitoring the container
	t.errorCh, err = t.monitor.StartMonitoring(ctx)
	if err != nil {
		return fmt.Errorf("failed to start container monitoring: %v", err)
	}

	// Start a goroutine to handle container exit
	go t.handleContainerExit(ctx)

	return nil
}

// Stop gracefully shuts down the transport and the container.
func (t *StdioTransport) Stop(ctx context.Context) error {
	// First check if the transport is already stopped without locking
	// to avoid deadlocks if Stop is called from multiple goroutines
	select {
	case <-t.shutdownCh:
		// Channel is already closed, transport is already stopping or stopped
		// Just return without doing anything else
		return nil
	default:
		// Channel is still open, proceed with stopping
	}

	// Now lock the mutex for the actual stopping process
	t.mutex.Lock()
	defer t.mutex.Unlock()

	// Check again after locking to handle race conditions
	select {
	case <-t.shutdownCh:
		// Channel was closed between our first check and acquiring the lock
		return nil
	default:
		// Channel is still open, close it to signal shutdown
		close(t.shutdownCh)
	}

	// Stop the monitor if it's running and we haven't already stopped it
	if t.monitor != nil {
		t.monitor.StopMonitoring()
		t.monitor = nil
	}

	// Stop the HTTP proxy
	if t.httpProxy != nil {
		if err := t.httpProxy.Stop(ctx); err != nil {
			logger.Warnf("Warning: Failed to stop HTTP proxy: %v", err)
		}
	}

	// Close stdin and stdout if they're open
	if t.stdin != nil {
		if err := t.stdin.Close(); err != nil {
			logger.Warnf("Warning: Failed to close stdin: %v", err)
		}
		t.stdin = nil
	}

	// Stop the container if runtime is available and we haven't already stopped it
	if t.runtime != nil && t.containerID != "" {
		// Check if the workload is still running before trying to stop it
		running, err := t.runtime.IsWorkloadRunning(ctx, t.containerID)
		if err != nil {
			// If there's an error checking the workload status, it might be gone already
			logger.Warnf("Warning: Failed to check workload status: %v", err)
		} else if running {
			// Only try to stop the workload if it's still running
			if err := t.runtime.StopWorkload(ctx, t.containerID); err != nil {
				logger.Warnf("Warning: Failed to stop workload: %v", err)
			}
		}
	}

	return nil
}

// IsRunning checks if the transport is currently running.
func (t *StdioTransport) IsRunning(_ context.Context) (bool, error) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	// Check if the shutdown channel is closed
	select {
	case <-t.shutdownCh:
		return false, nil
	default:
		return true, nil
	}
}

// processMessages handles the message exchange between the client and container.
func (t *StdioTransport) processMessages(ctx context.Context, stdin io.WriteCloser, stdout io.ReadCloser) {
	// Create a context that will be canceled when shutdown is signaled
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Monitor for shutdown signal
	go func() {
		select {
		case <-t.shutdownCh:
			cancel()
		case <-ctx.Done():
			// Context was canceled elsewhere
		}
	}()

	// Start a goroutine to read from stdout
	go t.processStdout(ctx, stdout)
	// Process incoming messages and send them to the container
	messageCh := t.httpProxy.GetMessageChannel()

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-messageCh:
			logger.Info("Process incoming messages and sending message to container")
			if err := t.sendMessageToContainer(ctx, stdin, msg); err != nil {
				logger.Errorf("Error sending message to container: %v", err)
			}
			logger.Info("Messages processed")
		}
	}
}

// processStdout reads from the container's stdout and processes JSON-RPC messages.
func (t *StdioTransport) processStdout(ctx context.Context, stdout io.ReadCloser) {
	// Create a buffer for accumulating data
	var buffer bytes.Buffer

	// Create a buffer for reading
	readBuffer := make([]byte, 4096)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Read data from stdout
			n, err := stdout.Read(readBuffer)
			if err != nil {
				if err == io.EOF {
					logger.Info("Container stdout closed")
				} else {
					logger.Errorf("Error reading from container stdout: %v", err)
				}
				return
			}

			if n > 0 {
				// Write the data to the buffer
				buffer.Write(readBuffer[:n])

				// Process the buffer
				t.processBuffer(ctx, &buffer)
			}
		}
	}
}

// processBuffer processes the accumulated data in the buffer.
func (t *StdioTransport) processBuffer(ctx context.Context, buffer *bytes.Buffer) {
	// Process complete lines
	for {
		line, err := buffer.ReadString('\n')
		if err == io.EOF {
			// No complete line found, put the data back in the buffer
			buffer.WriteString(line)
			break
		}

		// Verify if new line character is present as last character
		// If so, remove it
		if len(line) > 0 && line[len(line)-1] == '\n' {
			// Remove the trailing newline
			line = line[:len(line)-1]
		}
		t.parseAndForwardJSONRPC(ctx, line)
	}
}

// sanitizeJSONString extracts the first valid JSON object from a string
func sanitizeJSONString(input string) string {
	return sanitizeBinaryString(input)
}

// sanitizeBinaryString removes all non-JSON characters and whitespace from a string
func sanitizeBinaryString(input string) string {
	// Find the first opening brace
	startIdx := strings.Index(input, "{")
	if startIdx == -1 {
		return "" // No JSON object found
	}

	// Find the last closing brace
	endIdx := strings.LastIndex(input, "}")
	if endIdx == -1 || endIdx < startIdx {
		return "" // No valid JSON object found
	}

	// Extract just the JSON object, discarding everything else
	jsonObj := input[startIdx : endIdx+1]

	// Remove all whitespace, control characters, and replacement characters
	var buffer bytes.Buffer

	for _, r := range jsonObj {
		// Skip replacement character (U+FFFD) and non-printable characters
		if r != '\uFFFD' && (unicode.IsPrint(r) || isSpace(r)) {
			buffer.WriteRune(r)
		}
	}

	return buffer.String()
}

// isSpace reports whether r is a space character as defined by JSON.
// These are the valid space characters in this implementation:
//   - ' ' (U+0020, SPACE)
//   - '\n' (U+000A, LINE FEED)
func isSpace(r rune) bool {
	return r == ' ' || r == '\n'
}

// parseAndForwardJSONRPC parses a JSON-RPC message and forwards it.
func (t *StdioTransport) parseAndForwardJSONRPC(ctx context.Context, line string) {
	// Log the raw line for debugging
	logger.Infof("JSON-RPC raw: %s", line)
	jsonData := sanitizeJSONString(line)
	logger.Infof("Sanitized JSON: %s", jsonData)

	if jsonData == "" || jsonData == "[]" {
		return
	}

	// Try to parse the JSON
	msg, err := jsonrpc2.DecodeMessage([]byte(jsonData))
	if err != nil {
		logger.Errorf("Error parsing JSON-RPC message: %v", err)
		return
	}

	// Log the message
	logger.Infof("Received JSON-RPC message: %T", msg)

	if err := t.httpProxy.ForwardResponseToClients(ctx, msg); err != nil {
		if t.proxyMode == types.ProxyModeStreamableHTTP {
			logger.Errorf("Error forwarding to streamable-http client: %v", err)
		} else {
			logger.Errorf("Error forwarding to SSE clients: %v", err)
		}
	}
}

// sendMessageToContainer sends a JSON-RPC message to the container.
func (*StdioTransport) sendMessageToContainer(_ context.Context, stdin io.Writer, msg jsonrpc2.Message) error {
	// Serialize the message
	data, err := jsonrpc2.EncodeMessage(msg)
	if err != nil {
		return fmt.Errorf("failed to encode JSON-RPC message: %w", err)
	}

	// Add newline
	data = append(data, '\n')

	// Write to stdin
	logger.Info("Writing to container stdin")
	if _, err := stdin.Write(data); err != nil {
		return fmt.Errorf("failed to write to container stdin: %w", err)
	}
	logger.Info("Wrote to container stdin")

	return nil
}

// handleContainerExit handles container exit events.
func (t *StdioTransport) handleContainerExit(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case err, ok := <-t.errorCh:
		// Check if the channel is closed
		if !ok {
			logger.Infof("Container monitor channel closed for %s", t.containerName)
			return
		}

		logger.Infof("Container %s exited: %v", t.containerName, err)

		// Check if the transport is already stopped before trying to stop it
		select {
		case <-t.shutdownCh:
			// Transport is already stopping or stopped
			logger.Infof("Transport for %s is already stopping or stopped", t.containerName)
			return
		default:
			// Transport is still running, stop it
			// Create a context with timeout for stopping the transport
			stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if stopErr := t.Stop(stopCtx); stopErr != nil {
				logger.Errorf("Error stopping transport after container exit: %v", stopErr)
			}
		}
	}
}
