// Package labels provides utilities for managing container labels
// used by the toolhive application.
package labels

import (
	"fmt"
)

const (
	// LabelEnabled is the label that indicates a container is managed by ToolHive
	LabelEnabled = "toolhive"

	// LabelEnabledValue is the value for the LabelEnabled label
	LabelEnabledValue = "true"

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
