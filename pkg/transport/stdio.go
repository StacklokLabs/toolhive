package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"unicode"

	"github.com/stacklok/vibetool/pkg/container"
	"github.com/stacklok/vibetool/pkg/permissions"
)

// StdioTransport implements the Transport interface using standard input/output.
// It acts as a proxy between the MCP client and the container's stdin/stdout.
type StdioTransport struct {
	port          int
	containerID   string
	containerName string
	runtime       container.Runtime
	debug         bool
	middlewares   []Middleware

	// Mutex for protecting shared state
	mutex sync.Mutex

	// Channels for communication
	shutdownCh chan struct{}
	errorCh    <-chan error

	// HTTP SSE proxy
	httpProxy Proxy

	// Container I/O
	stdin  io.WriteCloser
	stdout io.ReadCloser

	// Container monitor
	monitor *container.Monitor
}

// NewStdioTransport creates a new stdio transport.
func NewStdioTransport(
	port int,
	runtime container.Runtime,
	debug bool,
	middlewares ...Middleware,
) *StdioTransport {
	return &StdioTransport{
		port:        port,
		runtime:     runtime,
		debug:       debug,
		middlewares: middlewares,
		shutdownCh:  make(chan struct{}),
	}
}

// Mode returns the transport mode.
func (*StdioTransport) Mode() TransportType {
	return TransportTypeStdio
}

// Port returns the port used by the transport.
func (t *StdioTransport) Port() int {
	return t.port
}

// Setup prepares the transport for use.
func (t *StdioTransport) Setup(
	ctx context.Context,
	runtime container.Runtime,
	containerName string,
	image string,
	cmdArgs []string,
	envVars, labels map[string]string,
	permissionProfile *permissions.Profile,
) error {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	t.runtime = runtime
	t.containerName = containerName

	// Add transport-specific environment variables
	envVars["MCP_TRANSPORT"] = "stdio"

	// Get container permission config from the runtime
	containerPermConfig, err := runtime.GetPermissionConfigFromProfile(permissionProfile, "stdio")
	if err != nil {
		return fmt.Errorf("failed to get permission configuration: %v", err)
	}

	// Create container options
	containerOptions := container.NewCreateContainerOptions()
	containerOptions.AttachStdio = true

	// Create the container
	fmt.Printf("Creating container %s from image %s...\n", containerName, image)
	containerID, err := t.runtime.CreateContainer(
		ctx,
		image,
		containerName,
		cmdArgs,
		envVars,
		labels,
		containerPermConfig,
		containerOptions,
	)
	if err != nil {
		return fmt.Errorf("failed to create container: %v", err)
	}
	t.containerID = containerID
	fmt.Printf("Container created with ID: %s\n", containerID)

	return nil
}

// Start initializes the transport and begins processing messages.
// The transport is responsible for starting the container and attaching to it.
func (t *StdioTransport) Start(ctx context.Context) error {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	if t.containerID == "" {
		return ErrContainerIDNotSet
	}

	if t.containerName == "" {
		return ErrContainerNameNotSet
	}

	if t.runtime == nil {
		return fmt.Errorf("container runtime not set")
	}

	// Start the container
	fmt.Printf("Starting container %s...\n", t.containerName)
	if err := t.runtime.StartContainer(ctx, t.containerID); err != nil {
		return fmt.Errorf("failed to start container: %v", err)
	}

	// Attach to the container
	var err error
	t.stdin, t.stdout, err = t.runtime.AttachContainer(ctx, t.containerID)
	if err != nil {
		return fmt.Errorf("failed to attach to container: %w", err)
	}

	// Create and start the HTTP SSE proxy with middlewares
	t.httpProxy = NewHTTPSSEProxy(t.port, t.containerName, t.middlewares...)
	if err := t.httpProxy.Start(ctx); err != nil {
		return err
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
	t.mutex.Lock()
	defer t.mutex.Unlock()

	// Signal shutdown
	close(t.shutdownCh)

	// Stop the monitor if it's running
	if t.monitor != nil {
		t.monitor.StopMonitoring()
		t.monitor = nil
	}

	// Stop the HTTP proxy
	if t.httpProxy != nil {
		if err := t.httpProxy.Stop(ctx); err != nil {
			fmt.Printf("Warning: Failed to stop HTTP proxy: %v\n", err)
		}
	}

	// Close stdin and stdout if they're open
	if t.stdin != nil {
		if err := t.stdin.Close(); err != nil {
			fmt.Printf("Warning: Failed to close stdin: %v\n", err)
		}
		t.stdin = nil
	}

	// Stop the container if runtime is available
	if t.runtime != nil && t.containerID != "" {
		if err := t.runtime.StopContainer(ctx, t.containerID); err != nil {
			return fmt.Errorf("failed to stop container: %w", err)
		}

		// Remove the container if debug mode is not enabled
		if !t.debug {
			fmt.Printf("Removing container %s...\n", t.containerName)
			if err := t.runtime.RemoveContainer(ctx, t.containerID); err != nil {
				fmt.Printf("Warning: Failed to remove container: %v\n", err)
			}
			fmt.Printf("Container %s removed\n", t.containerName)
		} else {
			fmt.Printf("Debug mode enabled, container %s not removed\n", t.containerName)
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
			if err := t.sendMessageToContainer(ctx, stdin, msg); err != nil {
				fmt.Printf("Error sending message to container: %v\n", err)
			}
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
					fmt.Println("Container stdout closed")
				} else {
					fmt.Printf("Error reading from container stdout: %v\n", err)
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

		// Remove the trailing newline
		line = line[:len(line)-1]

		// Try to parse as JSON-RPC
		if line != "" {
			t.parseAndForwardJSONRPC(ctx, line)
		}
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

	// Remove all whitespace and control characters
	var buffer bytes.Buffer
	inString := false

	for _, r := range jsonObj {
		if r == '"' {
			inString = !inString
			buffer.WriteRune(r)
		} else if inString {
			// Inside string literals, only keep printable characters
			if unicode.IsPrint(r) && !unicode.IsSpace(r) {
				buffer.WriteRune(r)
			}
		} else {
			// Outside string literals, remove whitespace
			if !unicode.IsSpace(r) {
				buffer.WriteRune(r)
			}
		}
	}

	return buffer.String()
}

// parseAndForwardJSONRPC parses a JSON-RPC message and forwards it.
func (t *StdioTransport) parseAndForwardJSONRPC(ctx context.Context, line string) {
	// Log the raw line for debugging
	fmt.Printf("JSON-RPC raw: %s\n", line)

	// Check if the line contains binary data
	hasBinaryData := false
	for _, c := range line {
		if c < 32 && c != '\t' && c != '\r' && c != '\n' {
			hasBinaryData = true
		}
	}

	// If the line contains binary data, try to sanitize it
	var jsonData string
	if hasBinaryData {
		jsonData = sanitizeJSONString(line)
		fmt.Printf("Sanitized JSON: %s\n", jsonData)
	} else {
		jsonData = line
	}

	// Try to parse the JSON
	var msg JSONRPCMessage
	if err := json.Unmarshal([]byte(jsonData), &msg); err != nil {
		fmt.Printf("Error parsing JSON-RPC message: %v\n", err)
		return
	}

	// Validate the message
	if err := msg.Validate(); err != nil {
		fmt.Printf("Invalid JSON-RPC message: %v\n", err)
		return
	}

	// Log the message
	LogJSONRPCMessage(&msg)

	// Forward to SSE clients via the HTTP proxy
	if err := t.httpProxy.ForwardResponseToClients(ctx, &msg); err != nil {
		fmt.Printf("Error forwarding to SSE clients: %v\n", err)
	}

	// Send to the response channel
	if err := t.httpProxy.SendResponseMessage(&msg); err != nil {
		fmt.Printf("Error sending to response channel: %v\n", err)
	}
}

// sendMessageToContainer sends a JSON-RPC message to the container.
func (*StdioTransport) sendMessageToContainer(_ context.Context, stdin io.Writer, msg *JSONRPCMessage) error {
	// Serialize the message
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON-RPC message: %w", err)
	}

	// Add newline
	data = append(data, '\n')

	// Write to stdin
	if _, err := stdin.Write(data); err != nil {
		return fmt.Errorf("failed to write to container stdin: %w", err)
	}

	return nil
}

// handleContainerExit handles container exit events.
func (t *StdioTransport) handleContainerExit(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case err := <-t.errorCh:
		fmt.Printf("Container %s exited: %v\n", t.containerName, err)
		// Stop the transport when the container exits
		if stopErr := t.Stop(ctx); stopErr != nil {
			fmt.Printf("Error stopping transport after container exit: %v\n", stopErr)
		}
	}
}
