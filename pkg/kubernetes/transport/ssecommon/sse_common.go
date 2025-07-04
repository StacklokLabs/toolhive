// Package ssecommon provides common types and utilities for Server-Sent Events (SSE)
// used in communication between the client and MCP server.
package ssecommon

import (
	"fmt"
	"strings"
	"time"
)

const (
	// HTTPSSEEndpoint is the endpoint for SSE connections
	HTTPSSEEndpoint = "/sse"
	// HTTPMessagesEndpoint is the endpoint for JSON-RPC messages
	HTTPMessagesEndpoint = "/messages"
)

// SSEMessage represents a Server-Sent Event message
type SSEMessage struct {
	// EventType is the type of event (e.g., "message", "endpoint")
	EventType string
	// Data is the event data
	Data string
	// TargetClientID is the ID of the target client (if any)
	TargetClientID string
	// CreatedAt is the time the message was created
	CreatedAt time.Time
}

// NewSSEMessage creates a new SSE message
func NewSSEMessage(eventType, data string) *SSEMessage {
	return &SSEMessage{
		EventType: eventType,
		Data:      data,
		CreatedAt: time.Now(),
	}
}

// WithTargetClientID sets the target client ID for the message
func (m *SSEMessage) WithTargetClientID(clientID string) *SSEMessage {
	m.TargetClientID = clientID
	return m
}

// ToSSEString converts the message to an SSE-formatted string
func (m *SSEMessage) ToSSEString() string {
	var sb strings.Builder

	// Add event type
	sb.WriteString(fmt.Sprintf("event: %s\n", m.EventType))

	// Add data (split by newlines to ensure proper formatting)
	for _, line := range strings.Split(m.Data, "\n") {
		sb.WriteString(fmt.Sprintf("data: %s\n", line))
	}

	// End the message with a blank line
	sb.WriteString("\n")

	return sb.String()
}

// PendingSSEMessage represents an SSE message that is pending delivery
type PendingSSEMessage struct {
	// Message is the SSE message
	Message *SSEMessage
	// CreatedAt is the time the message was created
	CreatedAt time.Time
}

// NewPendingSSEMessage creates a new pending SSE message
func NewPendingSSEMessage(message *SSEMessage) *PendingSSEMessage {
	return &PendingSSEMessage{
		Message:   message,
		CreatedAt: time.Now(),
	}
}

// SSEClient represents a connected SSE client
type SSEClient struct {
	// MessageCh is the channel for sending messages to the client
	MessageCh chan string
	// CreatedAt is the time the client connected
	CreatedAt time.Time
}
