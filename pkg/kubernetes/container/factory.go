// Package container provides utilities for managing containers,
// including creating, starting, stopping, and monitoring containers.
package container

import (
	"context"

	"github.com/stacklok/toolhive/pkg/kubernetes/container/kubernetes"
	"github.com/stacklok/toolhive/pkg/kubernetes/container/runtime"
)

// Factory creates container runtimes
type Factory struct{}

// NewFactory creates a new container factory
func NewFactory() *Factory {
	return &Factory{}
}

// Create creates a container runtime
func (*Factory) Create(ctx context.Context) (runtime.Runtime, error) {
	client, err := kubernetes.NewClient(ctx)
	if err != nil {
		return nil, err
	}

	return client, nil
}
