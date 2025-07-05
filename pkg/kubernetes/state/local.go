package state

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/adrg/xdg"
)

const (
	// DefaultAppName is the default application name used for XDG paths
	DefaultAppName = "toolhive"

	// FileExtension is the file extension for stored configurations
	FileExtension = ".json"
)

// LocalStore implements the Store interface using the local filesystem
// following the XDG Base Directory Specification
type LocalStore struct {
	// basePath is the base directory path for storing configurations
	basePath string
}

// NewLocalStore creates a new LocalStore with the given application name and store type
// If appName is empty, DefaultAppName will be used
func NewLocalStore(appName string, storeName string) (*LocalStore, error) {
	if appName == "" {
		appName = DefaultAppName
	}

	// Create the base directory path following XDG spec
	basePath := filepath.Join(xdg.StateHome, appName, storeName)

	// Ensure the directory exists
	if err := os.MkdirAll(basePath, 0750); err != nil {
		return nil, fmt.Errorf("failed to create state directory: %w", err)
	}

	return &LocalStore{
		basePath: basePath,
	}, nil
}

// getFilePath returns the full file path for a configuration
func (s *LocalStore) getFilePath(name string) string {
	// Ensure the name has the correct extension
	if !strings.HasSuffix(name, FileExtension) {
		name = name + FileExtension
	}
	return filepath.Join(s.basePath, name)
}

// Save stores the data for the given name from the provided reader
func (s *LocalStore) Save(_ context.Context, name string, r io.Reader) error {
	// Create the file
	filePath := s.getFilePath(name)
	// #nosec G304 - filePath is controlled by getFilePath which ensures it's within our designated directory
	file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	// Copy the data from the reader to the file
	_, err = io.Copy(file, r)
	if err != nil {
		return fmt.Errorf("failed to write data to file: %w", err)
	}

	return nil
}

// Load retrieves the data for the given name and writes it to the provided writer
func (s *LocalStore) Load(_ context.Context, name string, w io.Writer) error {
	// Open the file
	filePath := s.getFilePath(name)
	// #nosec G304 - filePath is controlled by getFilePath which ensures it's within our designated directory
	file, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("state '%s' not found", name)
		}
		return fmt.Errorf("failed to open state file: %w", err)
	}
	defer file.Close()

	// Copy the data from the file to the writer
	_, err = io.Copy(w, file)
	if err != nil {
		return fmt.Errorf("failed to read data from file: %w", err)
	}

	return nil
}

// GetReader returns a reader for the state data
func (s *LocalStore) GetReader(_ context.Context, name string) (io.ReadCloser, error) {
	// Open the file
	filePath := s.getFilePath(name)
	// #nosec G304 - filePath is controlled by getFilePath which ensures it's within our designated directory
	file, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("state '%s' not found", name)
		}
		return nil, fmt.Errorf("failed to open state file: %w", err)
	}

	return file, nil
}

// GetWriter returns a writer for the state data
func (s *LocalStore) GetWriter(_ context.Context, name string) (io.WriteCloser, error) {
	// Create the file
	filePath := s.getFilePath(name)
	// #nosec G304 - filePath is controlled by getFilePath which ensures it's within our designated directory
	file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to create file: %w", err)
	}

	return file, nil
}

// Delete removes the data for the given name
func (s *LocalStore) Delete(_ context.Context, name string) error {
	filePath := s.getFilePath(name)
	// #nosec G304 - filePath is controlled by getFilePath which ensures it's within our designated directory
	if err := os.Remove(filePath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("state '%s' not found", name)
		}
		return fmt.Errorf("failed to delete state file: %w", err)
	}
	return nil
}

// List returns all available state names
func (s *LocalStore) List(_ context.Context) ([]string, error) {
	// Read the directory
	entries, err := os.ReadDir(s.basePath)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to read state directory: %w", err)
	}

	// Filter and process the file names
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if strings.HasSuffix(name, FileExtension) {
			// Remove the file extension
			name = strings.TrimSuffix(name, FileExtension)
			names = append(names, name)
		}
	}

	return names, nil
}

// Exists checks if data exists for the given name
func (s *LocalStore) Exists(_ context.Context, name string) (bool, error) {
	filePath := s.getFilePath(name)
	_, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check if state exists: %w", err)
	}
	return true, nil
}
