package client

import (
	"fmt"
	"sync"

	"github.com/stacklok/toolhive/pkg/kubernetes/config"
	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
)

// migrationOnce ensures the migration only runs once
var migrationOnce sync.Once

// CheckAndPerformAutoDiscoveryMigration checks if auto-discovery migration is needed and performs it
// This is called once at application startup
func CheckAndPerformAutoDiscoveryMigration() {
	migrationOnce.Do(func() {
		appConfig := config.GetConfig()

		// Check if auto-discovery flag is set to true, use of deprecated object is expected here
		if appConfig.Clients.AutoDiscovery {
			performAutoDiscoveryMigration()
		}
	})
}

// performAutoDiscoveryMigration discovers and registers all installed clients
func performAutoDiscoveryMigration() {
	fmt.Println("Migrating from deprecated auto-discovery to manual client registration...")
	fmt.Println()

	// Get current client statuses to determine what to register
	clientStatuses, err := GetClientStatus()
	if err != nil {
		logger.Errorf("Error discovering clients during migration: %v", err)
		return
	}

	// Get current config to see what's already registered
	appConfig := config.GetConfig()

	var clientsToRegister []string
	var alreadyRegistered = appConfig.Clients.RegisteredClients

	// Find installed clients that aren't registered yet
	for _, status := range clientStatuses {
		if status.Installed && !status.Registered {
			clientsToRegister = append(clientsToRegister, string(status.ClientType))
			fmt.Println("Registering client", string(status.ClientType))
		}
	}

	// Register new clients and remove the auto-discovery flag
	err = config.UpdateConfig(func(c *config.Config) {
		for _, clientName := range clientsToRegister {
			// Double-check if not already registered (safety check)
			found := false
			for _, registered := range c.Clients.RegisteredClients {
				if registered == clientName {
					found = true
					break
				}
			}

			if !found {
				c.Clients.RegisteredClients = append(c.Clients.RegisteredClients, clientName)
			}
		}

		// Remove the auto-discovery flag during the same config update
		c.Clients.AutoDiscovery = false
	})

	if err != nil {
		logger.Errorf("Error updating config during migration: %v", err)
		return
	}

	// Print success messages for newly registered clients
	for _, clientName := range clientsToRegister {
		fmt.Printf("  ✓ Automatically registered client: %s\n", clientName)
	}

	fmt.Println()
	fmt.Println("NOTICE: Auto-discovery of MCP clients has been deprecated and is no longer supported.")
	fmt.Println("Your existing clients have been automatically migrated to the new manual registration system.")
	fmt.Println()
	fmt.Println("Going forward, use 'thv client setup' to discover and register new MCP clients.")
	fmt.Println("This provides better control and security for your client configurations.")
	fmt.Println()

	// Show all registered clients (both newly registered and previously registered)
	allRegisteredClients := append(alreadyRegistered, clientsToRegister...)
	if len(allRegisteredClients) > 0 {
		fmt.Println("Registered clients:")
		for _, clientName := range allRegisteredClients {
			fmt.Printf("  - %s\n", clientName)
		}
		fmt.Println()
	}
}
