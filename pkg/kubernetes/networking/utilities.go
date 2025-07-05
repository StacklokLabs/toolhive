package networking

import (
	"errors"
	"fmt"
	"net"
)

const (
	// ErrPrivateIpAddress is the error returned when the provided URL redirects to a private IP address
	ErrPrivateIpAddress = "the provided registry URL redirects to a private IP address, which is not allowed; " +
		"to override this, reset the registry URL using the --allow-private-ip (-p) flag"
)

func init() {
	for _, cidr := range []string{
		"127.0.0.0/8",    // IPv4 loopback
		"10.0.0.0/8",     // RFC1918
		"172.16.0.0/12",  // RFC1918
		"192.168.0.0/16", // RFC1918
		"169.254.0.0/16", // RFC3927 link-local
		"::1/128",        // IPv6 loopback
		"fe80::/10",      // IPv6 link-local
		"fc00::/7",       // IPv6 unique local addr
	} {
		_, block, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(fmt.Errorf("parse error on %q: %v", cidr, err))
		}
		privateIPBlocks = append(privateIPBlocks, block)
	}
}

func isPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	for _, block := range privateIPBlocks {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

// AddressReferencesPrivateIp returns an error if the address references a private IP address
func AddressReferencesPrivateIp(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	// Check for a private IP address or loopback
	ip := net.ParseIP(host)
	if isPrivateIP(ip) {
		return errors.New(ErrPrivateIpAddress)
	}

	return nil
}
