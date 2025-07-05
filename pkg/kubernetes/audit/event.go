// Package audit provides audit logging functionality for ToolHive.
// This package includes audit event structures and utilities based on
// the auditevent library from metal-toolbox/auditevent to ensure
// NIST SP 800-53 compliance.
package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// The following code is adapted from github.com/metal-toolbox/auditevent
// Original copyright notice:
/*
Copyright 2022 Equinix, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// AuditEvent represents an audit event.
// It provides the minimal information needed to audit an event, as well as
// a uniform format to persist the events in audit logs.
//
// It is highly recommended to use the NewAuditEvent function to create
// audit events and set the required fields.
//
//nolint:revive // AuditEvent name is intentional for compatibility with auditevent library
type AuditEvent struct {
	Metadata EventMetadata `json:"metadata"`
	// Type: Defines the type of event that occurred
	// This is a small identifier to quickly determine what happened.
	// e.g. UserLogin, UserLogout, UserCreate, UserDelete, etc.
	Type string `json:"type"`
	// LoggedAt: determines when the event occurred.
	// Note that this should have sufficient information to authoritatively
	// determine the exact time the event was logged at. The output must be in
	// Coordinated Universal Time (UTC) format, a modern continuation of
	// Greenwich Mean Time (GMT), or local time with an offset from UTC to satisfy
	// NIST SP 800-53 requirement AU-8.
	LoggedAt time.Time `json:"loggedAt"`
	// Source: determines the source of the event.
	// Normally, using the IP address of the client, or pod name is sufficient.
	// One must be careful of the data that's added here as we don't want to
	// leak Personally Identifiable Information.
	Source EventSource `json:"source"`
	// Outcome: determines whether the event was successful or not, e.g. successful login
	// It may also determine if the event was approved or denied.
	Outcome string `json:"outcome"`
	// Subject: is the identity of the subject of the event.
	// e.g. who triggered the event? Additional information
	// may be added, such as group membership and/or role
	Subjects map[string]string `json:"subjects"`
	// Component: allows to determine in which component the event occurred
	// (Answering the "Where" question of section c in the NIST SP 800-53
	// Revision 5.1 Control AU-3).
	Component string `json:"component"`
	// Target: Defines where the target of the operation. e.g. the path of
	// the REST resource
	// (Answering the "Where" question of section c in the NIST SP 800-53
	// Revision 5.1 Control AU-3 as well as indicating an entity
	// associated for section f).
	Target map[string]string `json:"target,omitempty"`
	// Data: enhances the audit event with extra information that may be
	// useful for forensic analysis.
	Data *json.RawMessage `json:"data,omitempty"`
}

// EventMetadata contains metadata about the audit event.
type EventMetadata struct {
	// AuditID: is a unique identifier for the audit event.
	AuditID string `json:"auditId"`
	// Extra allows for including additional information about the event
	// that aids in tracking, parsing or auditing
	Extra map[string]any `json:"extra,omitempty"`
}

// EventSource represents the source of an audit event.
type EventSource struct {
	// Type indicates the source type. e.g. Network, File, local, etc.
	// The intent is to determine where a request came from.
	Type string `json:"type"`
	// Value aims to indicate the source of the event. e.g. IP address,
	// hostname, etc.
	Value string `json:"value"`
	// Extra allows for including additional information about the event
	// source that aids in tracking, parsing or auditing
	Extra map[string]any `json:"extra,omitempty"`
}

// NewAuditEvent returns a new AuditEvent with an appropriately set AuditID and logging time.
func NewAuditEvent(
	eventType string,
	source EventSource,
	outcome string,
	subjects map[string]string,
	component string,
) *AuditEvent {
	return &AuditEvent{
		Metadata: EventMetadata{
			AuditID: uuid.New().String(),
		},
		Type:      eventType,
		LoggedAt:  time.Now().UTC(),
		Source:    source,
		Outcome:   outcome,
		Subjects:  subjects,
		Component: component,
	}
}

// NewAuditEventWithID returns a new AuditEvent with the passed AuditID.
func NewAuditEventWithID(
	auditID string,
	eventType string,
	source EventSource,
	outcome string,
	subjects map[string]string,
	component string,
) *AuditEvent {
	return &AuditEvent{
		Metadata: EventMetadata{
			AuditID: auditID,
		},
		Type:      eventType,
		LoggedAt:  time.Now().UTC(),
		Source:    source,
		Outcome:   outcome,
		Subjects:  subjects,
		Component: component,
	}
}

// WithTarget sets the target of the event.
func (e *AuditEvent) WithTarget(target map[string]string) *AuditEvent {
	e.Target = target
	return e
}

// WithData sets the data of the event.
func (e *AuditEvent) WithData(data *json.RawMessage) *AuditEvent {
	e.Data = data
	return e
}

// WithDataFromString sets the data of the event from a string.
// Note that validating that this is properly JSON-formatted
// is the responsibility of the caller.
func (e *AuditEvent) WithDataFromString(data string) *AuditEvent {
	rawMsg := json.RawMessage(data)
	return e.WithData(&rawMsg)
}

// LogTo logs the audit event to the provided slog.Logger using the custom audit level.
func (e *AuditEvent) LogTo(ctx context.Context, logger *slog.Logger, level slog.Level) {
	// Create slog attributes for the audit event
	attrs := []slog.Attr{
		slog.String("audit_id", e.Metadata.AuditID),
		slog.String("type", e.Type),
		slog.Time("logged_at", e.LoggedAt),
		slog.String("outcome", e.Outcome),
		slog.String("component", e.Component),
		slog.Group("source",
			slog.String("type", e.Source.Type),
			slog.String("value", e.Source.Value),
			slog.Any("extra", e.Source.Extra),
		),
		slog.Any("subjects", e.Subjects),
	}

	// Add target if present
	if e.Target != nil {
		attrs = append(attrs, slog.Any("target", e.Target))
	}

	// Add metadata extra if present
	if e.Metadata.Extra != nil {
		attrs = append(attrs, slog.Group("metadata", slog.Any("extra", e.Metadata.Extra)))
	}

	// Add data if present
	if e.Data != nil {
		attrs = append(attrs, slog.Any("data", e.Data))
	}

	// Log with the specified level
	logger.LogAttrs(ctx, level, "audit_event", attrs...)
}

// Common event outcomes
const (
	// OutcomeSuccess indicates the event was successful
	OutcomeSuccess = "success"
	// OutcomeFailure indicates the event failed
	OutcomeFailure = "failure"
	// OutcomeError indicates the event resulted in an error
	OutcomeError = "error"
	// OutcomeDenied indicates the event was denied (e.g., by authorization)
	OutcomeDenied = "denied"
)

// Common source types
const (
	// SourceTypeNetwork indicates the event came from a network request
	SourceTypeNetwork = "network"
	// SourceTypeLocal indicates the event came from a local source
	SourceTypeLocal = "local"
)

// Component name for ToolHive
const (
	// ComponentToolHive is the component name for ToolHive API audit events.
	// Note that events directed for an MCP server will have the name of the
	// MCP server as the component instead.
	ComponentToolHive = "toolhive-api"
)
