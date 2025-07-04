// Package networking provides utilities for network operations,
// such as finding available ports and checking network connectivity.
package networking

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net"

	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
)

const (
	// MinPort is the minimum port number to use
	MinPort = 10000
	// MaxPort is the maximum port number to use
	MaxPort = 65535
	// MaxAttempts is the maximum number of attempts to find an available port
	MaxAttempts = 10
)

// IsAvailable checks if a port is available
func IsAvailable(port int) bool {
	// Check TCP
	tcpAddr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}

	tcpListener, err := net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		return false
	}
	if err := tcpListener.Close(); err != nil {
		// Log the error but continue, as we're just checking if the port is available
		logger.Warnf("Warning: Failed to close TCP listener: %v", err)
	}

	// Check UDP
	udpAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}

	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return false
	}
	if err := udpConn.Close(); err != nil {
		// Log the error but continue, as we're just checking if the port is available
		logger.Warnf("Warning: Failed to close UDP connection: %v", err)
	}

	return true
}

// IsIPv6Available checks if IPv6 is available on the system
// by looking for IPv6 addresses on network interfaces
func IsIPv6Available() bool {
	interfaces, err := net.Interfaces()
	if err != nil {
		return false
	}

	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 {
			// Interface is down
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}

			if ipNet.IP.To4() == nil && !ipNet.IP.IsLoopback() {
				// This is an IPv6 address and not a loopback
				return true
			}
		}
	}

	return false
}

// FindAvailable finds an available port
func FindAvailable() int {
	for i := 0; i < MaxAttempts; i++ {
		// Generate a cryptographically secure random number
		n, err := rand.Int(rand.Reader, big.NewInt(int64(MaxPort-MinPort)))
		if err != nil {
			// Fall back to sequential search if random generation fails
			break
		}
		port := int(n.Int64()) + MinPort
		if IsAvailable(port) {
			return port
		}
	}

	// If we can't find a random port, try sequential ports
	for port := MinPort; port <= MaxPort; port++ {
		if IsAvailable(port) {
			return port
		}
	}

	// If we still can't find a port, return 0
	return 0
}

// FindOrUsePort checks if the provided port is available or finds an available port if none is provided.
// If port is 0, it will find an available port.
// If port is not 0, it will check if the port is available.
// Returns the selected port and an error if any.
func FindOrUsePort(port int) (int, error) {
	if port == 0 {
		// Find an available port
		port = FindAvailable()
		if port == 0 {
			return 0, fmt.Errorf("could not find an available port")
		}
		return port, nil
	}

	if IsAvailable(port) {
		return port, nil
	}

	// Requested port is busy — find an alternative
	alt := FindAvailable()
	if alt == 0 {
		return 0, fmt.Errorf("failed to find an alternative port after requested port %d was unavailable", port)
	}
	return alt, nil
}
