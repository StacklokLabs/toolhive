// Package errors provides error types and constants for the transport package.
package errors

import (
	"errors"
	"fmt"
)

// Common transport errors
var (
	ErrUnsupportedTransport = errors.New("unsupported transport type")
	ErrTransportNotStarted  = errors.New("transport not started")
	ErrTransportClosed      = errors.New("transport closed")
	ErrInvalidMessage       = errors.New("invalid message")
	ErrRuntimeNotSet        = errors.New("container runtime not set")
	ErrContainerIDNotSet    = errors.New("container ID not set")
	ErrContainerNameNotSet  = errors.New("container name not set")
)

// TransportError represents an error related to transport operations
type TransportError struct {
	// Err is the underlying error
	Err error
	// ContainerID is the ID of the container
	ContainerID string
	// Message is an optional error message
	Message string
}

// Error returns the error message
func (e *TransportError) Error() string {
	if e.Message != "" {
		if e.ContainerID != "" {
			return fmt.Sprintf("%s: %s (container: %s)", e.Err, e.Message, e.ContainerID)
		}
		return fmt.Sprintf("%s: %s", e.Err, e.Message)
	}

	if e.ContainerID != "" {
		return fmt.Sprintf("%s (container: %s)", e.Err, e.ContainerID)
	}

	return e.Err.Error()
}

// Unwrap returns the underlying error
func (e *TransportError) Unwrap() error {
	return e.Err
}

// NewTransportError creates a new transport error
func NewTransportError(err error, containerID, message string) *TransportError {
	return &TransportError{
		Err:         err,
		ContainerID: containerID,
		Message:     message,
	}
}
