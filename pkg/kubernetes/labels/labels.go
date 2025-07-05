// Package labels provides utilities for managing container labels
// used by the toolhive application.
package labels

import (
	"fmt"
	"strings"
)

const (
	// LabelPrefix is the prefix for all ToolHive labels
	LabelPrefix = "toolhive"

	// LabelEnabled is the label that indicates a container is managed by ToolHive
	LabelEnabled = "toolhive"

	// LabelName is the label that contains the container name
	LabelName = "toolhive-name"

	// LabelBaseName is the label that contains the base container name (without timestamp)
	LabelBaseName = "toolhive-basename"

	// LabelTransport is the label that contains the transport mode
	LabelTransport = "toolhive-transport"

	// LabelPort is the label that contains the port
	LabelPort = "toolhive-port"

	// LabelToolType is the label that indicates the type of tool
	LabelToolType = "toolhive-tool-type"

	// LabelEnabledValue is the value for the LabelEnabled label
	LabelEnabledValue = "true"
)

// AddStandardLabels adds standard labels to a container
func AddStandardLabels(labels map[string]string, containerName, containerBaseName, transportType string, port int) {
	// Add standard labels
	labels[LabelEnabled] = LabelEnabledValue
	labels[LabelName] = containerName
	labels[LabelBaseName] = containerBaseName
	labels[LabelTransport] = transportType
	labels[LabelPort] = fmt.Sprintf("%d", port)

	// TODO: In the future, we'll support different tool types beyond just "mcp"
	labels[LabelToolType] = "mcp"
}

// AddNetworkLabels adds network-related labels to a network
func AddNetworkLabels(labels map[string]string, networkName string) {
	labels[LabelEnabled] = LabelEnabledValue
	labels[LabelName] = networkName
}

// FormatToolHiveFilter formats a filter for ToolHive containers
func FormatToolHiveFilter() string {
	return fmt.Sprintf("%s=%s", LabelEnabled, LabelEnabledValue)
}

// IsToolHiveContainer checks if a container is managed by ToolHive
func IsToolHiveContainer(labels map[string]string) bool {
	value, ok := labels[LabelEnabled]
	return ok && strings.ToLower(value) == LabelEnabledValue
}

// GetContainerName gets the container name from labels
func GetContainerName(labels map[string]string) string {
	return labels[LabelName]
}

// GetContainerBaseName gets the base container name from labels
func GetContainerBaseName(labels map[string]string) string {
	return labels[LabelBaseName]
}

// GetTransportType gets the transport type from labels
func GetTransportType(labels map[string]string) string {
	return labels[LabelTransport]
}

// GetPort gets the port from labels
func GetPort(labels map[string]string) (int, error) {
	portStr, ok := labels[LabelPort]
	if !ok {
		return 0, fmt.Errorf("port label not found")
	}

	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		return 0, fmt.Errorf("invalid port: %s", portStr)
	}

	return port, nil
}

// GetToolType gets the tool type from labels
func GetToolType(labels map[string]string) string {
	return labels[LabelToolType]
}
