// Package state provides functionality for storing and retrieving runner state
// across different environments (local filesystem, Kubernetes, etc.)
package state

import (
	"context"
	"io"
)

// Store defines the interface for runner state storage operations
type Store interface {
	// Save stores the data for the given name from the provided reader
	Save(ctx context.Context, name string, r io.Reader) error

	// Load retrieves the data for the given name and writes it to the provided writer
	// Returns an error if the state doesn't exist
	Load(ctx context.Context, name string, w io.Writer) error

	// GetReader returns a reader for the state data
	// This is useful for streaming large state data
	GetReader(ctx context.Context, name string) (io.ReadCloser, error)

	// GetWriter returns a writer for the state data
	// This is useful for streaming large state data
	GetWriter(ctx context.Context, name string) (io.WriteCloser, error)

	// Delete removes the data for the given name
	Delete(ctx context.Context, name string) error

	// List returns all available state names
	List(ctx context.Context) ([]string, error)

	// Exists checks if data exists for the given name
	Exists(ctx context.Context, name string) (bool, error)
}
