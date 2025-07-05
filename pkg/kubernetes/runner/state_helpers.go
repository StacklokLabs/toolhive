// Package runner provides functionality for running MCP servers
package runner

import (
	"context"
	"fmt"

	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
	"github.com/stacklok/toolhive/pkg/kubernetes/state"
)

// DefaultAppName is the default application name used for state storage
const DefaultAppName = "toolhive"

// SaveState saves the runner configuration to the state store
func (r *Runner) SaveState(ctx context.Context) error {
	// Create a state store
	store, err := state.NewRunConfigStore(DefaultAppName)
	if err != nil {
		return fmt.Errorf("failed to create state store: %w", err)
	}

	// Get a writer for the state
	writer, err := store.GetWriter(ctx, r.Config.BaseName)
	if err != nil {
		return fmt.Errorf("failed to get writer for state: %w", err)
	}
	defer writer.Close()

	// Serialize the configuration to JSON and write it directly to the state store
	if err := r.Config.WriteJSON(writer); err != nil {
		return fmt.Errorf("failed to write run configuration: %w", err)
	}

	logger.Infof("Saved run configuration for %s", r.Config.BaseName)
	return nil
}

// LoadState loads the runner configuration from the state store
// This is a static method that returns a new Runner instance
func LoadState(ctx context.Context, name string) (*Runner, error) {
	// Create a state store
	store, err := state.NewRunConfigStore(DefaultAppName)
	if err != nil {
		return nil, fmt.Errorf("failed to create state store: %w", err)
	}

	// Check if the configuration exists
	exists, err := store.Exists(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to check if run configuration exists: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("run configuration for %s not found", name)
	}

	// Get a reader for the state
	reader, err := store.GetReader(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get reader for state: %w", err)
	}
	defer reader.Close()

	// Deserialize the configuration directly from the state store
	config, err := ReadJSON(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read run configuration: %w", err)
	}

	// Create a new Runner with the loaded configuration
	return NewRunner(config), nil
}

// ListSavedConfigs lists all saved run configurations
func ListSavedConfigs(ctx context.Context) ([]string, error) {
	// Create a state store
	store, err := state.NewRunConfigStore(DefaultAppName)
	if err != nil {
		return nil, fmt.Errorf("failed to create state store: %w", err)
	}

	// List the configurations
	return store.List(ctx)
}

// DeleteSavedConfig deletes a saved run configuration
func DeleteSavedConfig(ctx context.Context, name string) error {
	// Create a state store
	store, err := state.NewRunConfigStore(DefaultAppName)
	if err != nil {
		return fmt.Errorf("failed to create state store: %w", err)
	}

	// Check if the configuration exists
	exists, err := store.Exists(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to check if run configuration exists: %w", err)
	}
	if !exists {
		return fmt.Errorf("run configuration for %s not found", name)
	}

	// Delete the configuration
	if err := store.Delete(ctx, name); err != nil {
		return fmt.Errorf("failed to delete run configuration: %w", err)
	}

	logger.Infof("Deleted run configuration for %s", name)
	return nil
}
