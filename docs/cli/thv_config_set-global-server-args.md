## thv config set-global-server-args

Set global arguments for all MCP servers

### Synopsis

Set global arguments that will be applied to all MCP servers.
These arguments will be used as defaults for all servers unless overridden by server-specific arguments.

Example:
  thv config set-global-server-args debug=true log-level=info

```
thv config set-global-server-args KEY=VALUE [KEY=VALUE...] [flags]
```

### Options

```
  -h, --help   help for set-global-server-args
```

### Options inherited from parent commands

```
      --debug   Enable debug mode
```

### SEE ALSO

* [thv config](thv_config.md)	 - Manage application configuration

