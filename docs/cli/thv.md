## thv

ToolHive (thv) is a lightweight, secure, and fast manager for MCP servers

### Synopsis

ToolHive (thv) is a lightweight, secure, and fast manager for MCP (Model Context Protocol) servers.
It is written in Go and has extensive test coverage—including input validation—to ensure reliability and security.

Under the hood, ToolHive acts as a very thin client for the Docker/Podman Unix socket API.
This design choice allows it to remain both efficient and lightweight while still providing powerful,
container-based isolation for running MCP servers.

```
thv [flags]
```

### Options

```
      --debug   Enable debug mode
  -h, --help    help for thv
```

### SEE ALSO

* [thv client](thv_client.md)	 - Manage MCP clients
* [thv config](thv_config.md)	 - Manage application configuration
* [thv inspector](thv_inspector.md)	 - Launches the MCP Inspector UI and connects it to the specified MCP server
* [thv list](thv_list.md)	 - List running MCP servers
* [thv logs](thv_logs.md)	 - Output the logs of an MCP server or manage log files
* [thv mcp](thv_mcp.md)	 - Interact with MCP servers for debugging
* [thv proxy](thv_proxy.md)	 - Create a transparent proxy for an MCP server with authentication support
* [thv registry](thv_registry.md)	 - Manage MCP server registry
* [thv restart](thv_restart.md)	 - Restart a tooling server
* [thv rm](thv_rm.md)	 - Remove an MCP server
* [thv run](thv_run.md)	 - Run an MCP server
* [thv search](thv_search.md)	 - Search for MCP servers
* [thv secret](thv_secret.md)	 - Manage secrets
* [thv serve](thv_serve.md)	 - Start the ToolHive API server
* [thv stop](thv_stop.md)	 - Stop an MCP server
* [thv version](thv_version.md)	 - Show the version of ToolHive

