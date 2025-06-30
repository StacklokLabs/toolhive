## thv mcp list

List MCP server capabilities

### Synopsis

List tools, resources, and prompts available from an MCP server. Use subcommands to list specific types.

```
thv mcp list [tools|resources|prompts] [flags]
```

### Options

```
      --format string      Output format (json or text) (default "text")
  -h, --help               help for list
      --server string      MCP server URL (required)
      --timeout duration   Connection timeout (default 30s)
```

### Options inherited from parent commands

```
      --debug   Enable debug mode
```

### SEE ALSO

* [thv mcp](thv_mcp.md)	 - Interact with MCP servers for debugging
* [thv mcp list prompts](thv_mcp_list_prompts.md)	 - List available prompts from MCP server
* [thv mcp list resources](thv_mcp_list_resources.md)	 - List available resources from MCP server
* [thv mcp list tools](thv_mcp_list_tools.md)	 - List available tools from MCP server

