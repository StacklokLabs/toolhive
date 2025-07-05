package workloads

import (
	"time"

	"github.com/stacklok/toolhive/pkg/kubernetes/client"
	"github.com/stacklok/toolhive/pkg/kubernetes/container/runtime"
	"github.com/stacklok/toolhive/pkg/kubernetes/labels"
	"github.com/stacklok/toolhive/pkg/kubernetes/transport"
	"github.com/stacklok/toolhive/pkg/kubernetes/transport/types"
)

// WorkloadStatus is an enum representing the possible statuses of a workload.
type WorkloadStatus string

const (
	// WorkloadStatusRunning indicates that the workload is currently running.
	WorkloadStatusRunning WorkloadStatus = "running"
	// WorkloadStatusStopped indicates that the workload is stopped.
	WorkloadStatusStopped WorkloadStatus = "stopped"
	// WorkloadStatusError indicates that the workload encountered an error.
	WorkloadStatusError WorkloadStatus = "error"
	// WorkloadStatusStarting indicates that the workload is in the process of starting.
	// TODO: this is not used yet.
	WorkloadStatusStarting WorkloadStatus = "starting"
	// WorkloadStatusUnknown indicates that the workload status is unknown.
	WorkloadStatusUnknown WorkloadStatus = "unknown"
)

// Workload is a domain model representing a workload in the system.
// This is used in our API to hide details of the underlying runtime.
type Workload struct {
	// Name is the name of the workload.
	// It is used as a unique identifier.
	Name string `json:"name"`
	// Package specifies the Workload Package used to create this Workload.
	Package string `json:"package"`
	// URL is the URL of the workload exposed by the ToolHive proxy.
	URL string `json:"url"`
	// Port is the port on which the workload is exposed.
	// This is embedded in the URL.
	Port int `json:"port"`
	// ToolType is the type of tool this workload represents.
	// For now, it will always be "mcp" - representing an MCP server.
	ToolType string `json:"tool_type"`
	// TransportType is the type of transport used for this workload.
	TransportType types.TransportType `json:"transport_type"`
	// Status is the current status of the workload.
	Status WorkloadStatus `json:"status"`
	// StatusContext provides additional context about the workload's status.
	// The exact meaning is determined by the status and the underlying runtime.
	StatusContext string `json:"status_context,omitempty"`
	// CreatedAt is the timestamp when the workload was created.
	CreatedAt time.Time `json:"created_at"`
}

// WorkloadFromContainerInfo creates a Workload struct from the runtime container info.
func WorkloadFromContainerInfo(container *runtime.ContainerInfo) (Workload, error) {
	// Get container name from labels
	name := labels.GetContainerName(container.Labels)
	if name == "" {
		name = container.Name // Fallback to container name
	}

	// Get tool type from labels
	toolType := labels.GetToolType(container.Labels)

	// Get port from labels
	port, err := labels.GetPort(container.Labels)
	if err != nil {
		port = 0
	}

	// check if we have the label for transport type (toolhive-transport)
	transportType := labels.GetTransportType(container.Labels)

	// Generate URL for the MCP server
	url := ""
	if port > 0 {
		url = client.GenerateMCPServerURL(transportType, transport.LocalhostIPv4, port, name)
	}

	// https://docs.docker.com/reference/api/engine/version/v1.45/#tag/Container/operation/ContainerList
	// TODO: This mapping needs some refinement. For example, we should display the starting state
	// before we start a container in Docker/Podman.
	workloadStatus := WorkloadStatusUnknown
	switch container.State {
	case "running":
		workloadStatus = WorkloadStatusRunning
	case "paused", "exited", "dead":
		workloadStatus = WorkloadStatusStopped
	case "restarting", "creating": // TODO: add handling new workload creation
		workloadStatus = WorkloadStatusStarting
	}

	tType, err := types.ParseTransportType(transportType)
	if err != nil {
		// If we can't parse the transport type, default to SSE.
		tType = types.TransportTypeSSE
	}

	// Translate to domain model.
	return Workload{
		Name: container.Name,
		// TODO: make this return the thv-specific name.
		Package:       container.Image,
		URL:           url,
		ToolType:      toolType,
		TransportType: tType,
		Status:        workloadStatus,
		StatusContext: container.Status,
		CreatedAt:     container.Created,
		Port:          port,
	}, nil
}
