package app

import (
	"fmt"
	"os"
	"os/signal"

	"github.com/spf13/cobra"

	s "github.com/stacklok/toolhive/pkg/api"
	"github.com/stacklok/toolhive/pkg/auth"
)

var (
	host       string
	port       int
	enableDocs bool
	socketPath string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the ToolHive API server",
	Long:  `Starts the ToolHive API server and listen for HTTP requests.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		// Ensure server is shutdown gracefully on Ctrl+C.
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
		defer cancel()

		// Get debug mode flag
		debugMode, _ := cmd.Flags().GetBool("debug")

		// If socket path is provided, use it; otherwise use host:port
		address := fmt.Sprintf("%s:%d", host, port)
		isUnixSocket := false
		if socketPath != "" {
			address = socketPath
			isUnixSocket = true
		}

		// Get OIDC configuration if enabled
		var oidcConfig *auth.TokenValidatorConfig
		if IsOIDCEnabled(cmd) {
			// Get OIDC flag values
			issuer := GetStringFlagOrEmpty(cmd, "oidc-issuer")
			audience := GetStringFlagOrEmpty(cmd, "oidc-audience")
			jwksURL := GetStringFlagOrEmpty(cmd, "oidc-jwks-url")
			clientID := GetStringFlagOrEmpty(cmd, "oidc-client-id")

			oidcConfig = &auth.TokenValidatorConfig{
				Issuer:   issuer,
				Audience: audience,
				JWKSURL:  jwksURL,
				ClientID: clientID,
			}
		}

		return s.Serve(ctx, address, isUnixSocket, debugMode, enableDocs, oidcConfig)
	},
}

func init() {
	serveCmd.Flags().StringVar(&host, "host", "127.0.0.1", "Host address to bind the server to")
	serveCmd.Flags().IntVar(&port, "port", 8080, "Port to bind the server to")
	serveCmd.Flags().BoolVar(&enableDocs, "openapi", false,
		"Enable OpenAPI documentation endpoints (/api/openapi.json and /api/doc)")
	serveCmd.Flags().StringVar(&socketPath, "socket", "", "UNIX socket path to bind the "+
		"server to (overrides host and port if provided)")

	// Add OIDC validation flags
	AddOIDCFlags(serveCmd)
}
