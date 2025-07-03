// Package api contains the REST API for ToolHive.
package api

// The OpenAPI spec is generated using "github.com/swaggo/swag/v2/cmd/swag@v2.0.0-rc4"
// To update the OpenAPI spec, run:
// install swag:
//	go install github.com/swaggo/swag/v2/cmd/swag@v2.0.0-rc4
// generate the spec:
//	swag init -g pkg/api/server.go --v3.1 -o docs/server

// @title           ToolHive API
// @version         1.0
// @description     This is the ToolHive API server.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	v1 "github.com/stacklok/toolhive/pkg/api/v1"
	"github.com/stacklok/toolhive/pkg/auth"
	"github.com/stacklok/toolhive/pkg/client"
	"github.com/stacklok/toolhive/pkg/container"
	"github.com/stacklok/toolhive/pkg/logger"
	"github.com/stacklok/toolhive/pkg/registry"
	"github.com/stacklok/toolhive/pkg/workloads"
)

// Not sure if these values need to be configurable.
const (
	middlewareTimeout = 60 * time.Second
	readHeaderTimeout = 10 * time.Second
	socketPermissions = 0660 // Socket file permissions (owner/group read-write)
)

func setupTCPListener(address string) (net.Listener, error) {
	return net.Listen("tcp", address)
}

func setupUnixSocket(address string) (net.Listener, error) {
	// Remove the socket file if it already exists
	if _, err := os.Stat(address); err == nil {
		if err := os.Remove(address); err != nil {
			return nil, fmt.Errorf("failed to remove existing socket: %v", err)
		}
	}

	// Create the directory for the socket file if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(address), 0750); err != nil {
		return nil, fmt.Errorf("failed to create socket directory: %v", err)
	}

	// Create UNIX socket listener
	listener, err := net.Listen("unix", address)
	if err != nil {
		return nil, fmt.Errorf("failed to create UNIX socket listener: %v", err)
	}

	// Set file permissions on the socket to allow other local processes to connect
	if err := os.Chmod(address, socketPermissions); err != nil {
		return nil, fmt.Errorf("failed to set socket permissions: %v", err)
	}

	return listener, nil
}

func cleanupUnixSocket(address string) {
	if err := os.Remove(address); err != nil && !os.IsNotExist(err) {
		logger.Warnf("failed to remove socket file: %v", err)
	}
}

func headersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "application/json")
		}
		next.ServeHTTP(w, r)
	})
}

// Serve starts the server on the given address and serves the API.
// It is assumed that the caller sets up appropriate signal handling.
// If isUnixSocket is true, address is treated as a UNIX socket path.
// If oidcConfig is provided, OIDC authentication will be enabled for all API endpoints.
func Serve(
	ctx context.Context,
	address string,
	isUnixSocket bool,
	debugMode bool,
	enableDocs bool,
	oidcConfig *auth.TokenValidatorConfig,
) error {
	r := chi.NewRouter()
	r.Use(
		middleware.RequestID,
		// TODO: Figure out logging middleware. We may want to use a different logger.
		middleware.Timeout(middlewareTimeout),
		headersMiddleware,
	)

	// Add authentication middleware
	authMiddleware, err := auth.GetAuthenticationMiddleware(ctx, oidcConfig, false)
	if err != nil {
		return fmt.Errorf("failed to create authentication middleware: %v", err)
	}
	r.Use(authMiddleware)

	manager, err := workloads.NewManager(ctx)
	if err != nil {
		logger.Panicf("failed to create lifecycle manager: %v", err)
	}

	// Create container runtime
	rt, err := container.NewFactory().Create(ctx)
	if err != nil {
		return fmt.Errorf("failed to create container runtime: %v", err)
	}

	// Create registry provider
	registryProvider, err := registry.GetDefaultProvider()
	if err != nil {
		return fmt.Errorf("failed to create registry provider: %v", err)
	}

	clientManager, err := client.NewManager(ctx)
	if err != nil {
		return fmt.Errorf("failed to create client manager: %v", err)
	}
	routers := map[string]http.Handler{
		"/health":               v1.HealthcheckRouter(rt),
		"/api/v1beta/version":   v1.VersionRouter(),
		"/api/v1beta/workloads": v1.WorkloadRouter(manager, rt, debugMode),
		"/api/v1beta/registry":  v1.RegistryRouter(registryProvider),
		"/api/v1beta/discovery": v1.DiscoveryRouter(),
		"/api/v1beta/clients":   v1.ClientRouter(clientManager),
		"/api/v1beta/secrets":   v1.SecretsRouter(),
	}

	// Only mount docs router if enabled
	if enableDocs {
		routers["/api/"] = DocsRouter()
	}

	for prefix, router := range routers {
		r.Mount(prefix, router)
	}

	srv := &http.Server{
		BaseContext:       func(net.Listener) context.Context { return ctx },
		Addr:              address,
		Handler:           r,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	// Create a listener based on the connection type
	var listener net.Listener
	var addrType string

	if isUnixSocket {
		listener, err = setupUnixSocket(address)
		addrType = "UNIX socket"
	} else {
		listener, err = setupTCPListener(address)
		addrType = "HTTP"
	}
	if err != nil {
		return err
	}

	logger.Infof("starting %s server", addrType, address)

	// Start server.
	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Panicf("server stopped with error: %v", err)
		}
	}()

	<-ctx.Done()
	if err := srv.Shutdown(ctx); err != nil {
		if isUnixSocket {
			cleanupUnixSocket(address)
		}
		return fmt.Errorf("server shutdown failed: %w", err)
	}

	if isUnixSocket {
		cleanupUnixSocket(address)
	}

	logger.Infof("%s server stopped", addrType)
	return nil
}
