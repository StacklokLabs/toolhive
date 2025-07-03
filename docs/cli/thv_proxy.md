## thv proxy

Create a transparent proxy for an MCP server with authentication support

### Synopsis

Create a transparent HTTP proxy that forwards requests to an MCP server endpoint.
This command starts a standalone proxy without launching a container, providing:

• Transparent request forwarding to the target MCP server
• Optional OAuth/OIDC authentication to remote MCP servers
• Automatic authentication detection via WWW-Authenticate headers
• OIDC-based access control for incoming proxy requests
• Secure credential handling via files or environment variables

AUTHENTICATION MODES:
The proxy supports multiple authentication scenarios:

1. No Authentication: Simple transparent forwarding
2. Outgoing Authentication: Authenticate to remote MCP servers using OAuth/OIDC
3. Incoming Authentication: Protect the proxy endpoint with OIDC validation
4. Bidirectional: Both incoming and outgoing authentication

OAUTH CLIENT SECRET SOURCES:
OAuth client secrets can be provided via (in order of precedence):
1. --remote-auth-client-secret flag (not recommended for production)
2. --remote-auth-client-secret-file flag (secure file-based approach)
3. TOOLHIVE_REMOTE_OAUTH_CLIENT_SECRET environment variable

EXAMPLES:
  # Basic transparent proxy
  thv proxy my-server --target-uri http://localhost:8080

  # Proxy with OAuth authentication to remote server
  thv proxy my-server --target-uri https://api.example.com \
    --remote-auth --remote-auth-issuer https://auth.example.com \
    --remote-auth-client-id my-client-id \
    --remote-auth-client-secret-file /path/to/secret

  # Proxy with OIDC protection for incoming requests
  thv proxy my-server --target-uri http://localhost:8080 \
    --oidc-issuer https://auth.example.com \
    --oidc-audience my-audience

  # Auto-detect authentication requirements
  thv proxy my-server --target-uri https://protected-api.com \
    --remote-auth-client-id my-client-id

```
thv proxy [flags] SERVER_NAME
```

### Options

```
  -h, --help                                    help for proxy
      --host string                             Host for the HTTP proxy to listen on (IP or hostname) (default "127.0.0.1")
      --oidc-audience string                    Expected audience for the token
      --oidc-client-id string                   OIDC client ID
      --oidc-issuer string                      OIDC issuer URL (e.g., https://accounts.google.com)
      --oidc-jwks-url string                    URL to fetch the JWKS from
      --oidc-skip-opaque-token-validation       Allow skipping validation of opaque tokens
      --port int                                Port for the HTTP proxy to listen on (host port)
      --remote-auth                             Enable OAuth authentication to remote MCP server
      --remote-auth-callback-port int           Port for OAuth callback server during remote authentication (default: 8666) (default 8666)
      --remote-auth-client-id string            OAuth client ID for remote server authentication
      --remote-auth-client-secret string        OAuth client secret for remote server authentication (optional for PKCE)
      --remote-auth-client-secret-file string   Path to file containing OAuth client secret (alternative to --remote-auth-client-secret)
      --remote-auth-issuer string               OAuth/OIDC issuer URL for remote server authentication (e.g., https://accounts.google.com)
      --remote-auth-scopes strings              OAuth scopes to request for remote server authentication (default [openid,profile,email])
      --remote-auth-skip-browser                Skip opening browser for remote server OAuth flow
      --remote-auth-timeout duration            Timeout for OAuth authentication flow (e.g., 30s, 1m, 2m30s) (default 30s)
      --target-uri string                       URI for the target MCP server (e.g., http://localhost:8080) (required)
```

### Options inherited from parent commands

```
      --debug   Enable debug mode
```

### SEE ALSO

* [thv](thv.md)	 - ToolHive (thv) is a lightweight, secure, and fast manager for MCP servers

