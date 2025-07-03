## thv run

Run an MCP server

### Synopsis

Run an MCP server with the specified name, image, or protocol scheme.

ToolHive supports three ways to run an MCP server:

1. From the registry:
   $ thv run server-name [-- args...]
   Looks up the server in the registry and uses its predefined settings
   (transport, permissions, environment variables, etc.)

2. From a container image:
   $ thv run ghcr.io/example/mcp-server:latest [-- args...]
   Runs the specified container image directly with the provided arguments

3. Using a protocol scheme:
   $ thv run uvx://package-name [-- args...]
   $ thv run npx://package-name [-- args...]
   $ thv run go://package-name [-- args...]
   $ thv run go://./local-path [-- args...]
   Automatically generates a container that runs the specified package
   using either uvx (Python with uv package manager), npx (Node.js),
   or go (Golang). For Go, you can also specify local paths starting
   with './' or '../' to build and run local Go projects.

The container will be started with the specified transport mode and
permission profile. Additional configuration can be provided via flags.

```
thv run [flags] SERVER_OR_IMAGE_OR_PROTOCOL [-- ARGS...]
```

### Options

```
      --audit-config string                   Path to the audit configuration file
      --authz-config string                   Path to the authorization configuration file
      --ca-cert string                        Path to a custom CA certificate file to use for container builds
      --enable-audit                          Enable audit logging with default configuration
  -e, --env stringArray                       Environment variables to pass to the MCP server (format: KEY=VALUE)
  -f, --foreground                            Run in foreground mode (block until container exits)
  -h, --help                                  help for run
      --host string                           Host for the HTTP proxy to listen on (IP or hostname) (default "127.0.0.1")
      --image-verification string             Set image verification mode (warn, enabled, disabled) (default "warn")
      --isolate-network                       Isolate the container network from the host (default: false)
      --name string                           Name of the MCP server (auto-generated from image if not provided)
      --oidc-audience string                  Expected audience for the token
      --oidc-client-id string                 OIDC client ID
      --oidc-issuer string                    OIDC issuer URL (e.g., https://accounts.google.com)
      --oidc-jwks-url string                  URL to fetch the JWKS from
      --oidc-skip-opaque-token-validation     Allow skipping validation of opaque tokens
      --otel-enable-prometheus-metrics-path   Enable Prometheus-style /metrics endpoint on the main transport port
      --otel-endpoint string                  OpenTelemetry OTLP endpoint URL (e.g., https://api.honeycomb.io)
      --otel-env-vars stringArray             Environment variable names to include in OpenTelemetry spans (comma-separated: ENV1,ENV2)
      --otel-headers stringArray              OpenTelemetry OTLP headers in key=value format (e.g., x-honeycomb-team=your-api-key)
      --otel-insecure                         Disable TLS verification for OpenTelemetry endpoint
      --otel-sampling-rate float              OpenTelemetry trace sampling rate (0.0-1.0) (default 0.1)
      --otel-service-name string              OpenTelemetry service name (defaults to toolhive-mcp-proxy)
      --permission-profile string             Permission profile to use (none, network, or path to JSON file) (default "network")
      --port int                              Port for the HTTP proxy to listen on (host port)
      --secret stringArray                    Specify a secret to be fetched from the secrets manager and set as an environment variable (format: NAME,target=TARGET)
      --target-host string                    Host to forward traffic to (only applicable to SSE or Streamable HTTP transport) (default "127.0.0.1")
      --target-port int                       Port for the container to expose (only applicable to SSE or Streamable HTTP transport)
      --transport string                      Transport mode (sse, streamable-http or stdio)
  -v, --volume stringArray                    Mount a volume into the container (format: host-path:container-path[:ro])
```

### Options inherited from parent commands

```
      --debug   Enable debug mode
```

### SEE ALSO

* [thv](thv.md)	 - ToolHive (thv) is a lightweight, secure, and fast manager for MCP servers

