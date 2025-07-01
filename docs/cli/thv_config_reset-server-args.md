## thv config reset-server-args

Reset/delete arguments for MCP servers from the config

### Synopsis

Reset/delete configured arguments for MCP servers from the config.
This command removes saved arguments from the config file.
Future runs of the affected servers will not use any pre-configured arguments unless explicitly provided.

Examples:
  # Reset arguments for a specific server
  thv config reset-server-args my-server

  # Reset arguments for all servers
  thv config reset-server-args --all

```
thv config reset-server-args [SERVER_NAME] [flags]
```

### Options

```
  -a, --all    Reset arguments for all MCP servers
  -h, --help   help for reset-server-args
```

### Options inherited from parent commands

```
      --debug   Enable debug mode
```

### SEE ALSO

* [thv config](thv_config.md)	 - Manage application configuration

