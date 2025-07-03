## thv serve

Start the ToolHive API server

### Synopsis

Starts the ToolHive API server and listen for HTTP requests.

```
thv serve [flags]
```

### Options

```
  -h, --help                                help for serve
      --host string                         Host address to bind the server to (default "127.0.0.1")
      --oidc-audience string                Expected audience for the token
      --oidc-client-id string               OIDC client ID
      --oidc-issuer string                  OIDC issuer URL (e.g., https://accounts.google.com)
      --oidc-jwks-url string                URL to fetch the JWKS from
      --oidc-skip-opaque-token-validation   Allow skipping validation of opaque tokens
      --openapi                             Enable OpenAPI documentation endpoints (/api/openapi.json and /api/doc)
      --port int                            Port to bind the server to (default 8080)
      --socket string                       UNIX socket path to bind the server to (overrides host and port if provided)
```

### Options inherited from parent commands

```
      --debug   Enable debug mode
```

### SEE ALSO

* [thv](thv.md)	 - ToolHive (thv) is a lightweight, secure, and fast manager for MCP servers

