// Package transport provides utilities for handling different transport modes
// for communication between the client and MCP server.
package transport

import (
	"github.com/stacklok/toolhive/pkg/transport/errors"
	"github.com/stacklok/toolhive/pkg/transport/types"
)

// Factory creates transports
type Factory struct{}

// NewFactory creates a new transport factory
func NewFactory() *Factory {
	return &Factory{}
}

// Create creates a transport based on the provided configuration
func (*Factory) Create(config types.Config) (types.Transport, error) {
	switch config.Type {
	case types.TransportTypeStdio:
		tr := NewStdioTransport(
			config.Host, config.Port, config.Runtime, config.Debug, config.PrometheusHandler, config.Middlewares...)
		tr.SetProxyMode(config.ProxyMode)
		return tr, nil
	case types.TransportTypeSSE:
		return NewHTTPTransport(
			types.TransportTypeSSE,
			config.Host,
			config.Port,
			config.TargetPort,
			config.Runtime,
			config.Debug,
			config.TargetHost,
			config.PrometheusHandler,
			config.Middlewares...,
		), nil
	case types.TransportTypeStreamableHTTP:
		return NewHTTPTransport(
			types.TransportTypeStreamableHTTP,
			config.Host,
			config.Port,
			config.TargetPort,
			config.Runtime,
			config.Debug,
			config.TargetHost,
			config.PrometheusHandler,
			config.Middlewares...,
		), nil
	case types.TransportTypeInspector:
		// HTTP transport is not implemented yet
		return nil, errors.ErrUnsupportedTransport
	default:
		return nil, errors.ErrUnsupportedTransport
	}
}
