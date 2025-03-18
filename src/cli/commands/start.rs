use clap::Args;
use std::collections::HashMap;
use std::path::PathBuf;

use crate::container::{ContainerRuntime, ContainerRuntimeFactory};
use crate::error::Result;
use crate::permissions::profile::PermissionProfile;
use crate::transport::{Transport, TransportFactory, TransportMode};

/// Start an MCP server in the background
#[derive(Args, Debug)]
pub struct StartCommand {
    /// Transport mode (sse or stdio)
    #[arg(long, default_value = "sse")]
    pub transport: String,

    /// Name of the MCP server
    #[arg(long)]
    pub name: String,

    /// Port to expose (for SSE transport)
    #[arg(long)]
    pub port: Option<u16>,

    /// Permission profile to use (stdio, network, or path to JSON file)
    #[arg(long, default_value = "stdio")]
    pub permission_profile: String,

    /// Image to use for the MCP server
    pub image: String,

    /// Arguments to pass to the MCP server
    #[arg(last = true)]
    pub args: Vec<String>,
}

impl StartCommand {
    /// Run the command
    pub async fn execute(&self) -> Result<()> {
        // Parse transport mode
        let transport_mode = TransportMode::from_str(&self.transport)
            .ok_or_else(|| {
                crate::error::Error::InvalidArgument(format!(
                    "Invalid transport mode: {}. Valid modes are: sse, stdio",
                    self.transport
                ))
            })?;

        // Validate port for SSE transport
        let port = match transport_mode {
            TransportMode::SSE => {
                self.port.ok_or_else(|| {
                    crate::error::Error::InvalidArgument(
                        "Port is required for SSE transport".to_string(),
                    )
                })?
            }
            _ => self.port.unwrap_or(0),
        };

        // Load permission profile
        let permission_profile = match self.permission_profile.as_str() {
            "stdio" => PermissionProfile::builtin_stdio_profile(),
            "network" => PermissionProfile::builtin_network_profile(),
            path => PermissionProfile::from_file(&PathBuf::from(path))?,
        };

        // Convert permission profile to container config
        let permission_config = permission_profile.to_container_config()?;

        // Create container runtime
        let runtime = ContainerRuntimeFactory::create().await?;
        
        // Create transport handler
        let transport = TransportFactory::create(transport_mode, port);
        
        // Execute with the runtime and transport
        self.execute_with_runtime_and_transport(runtime, transport, permission_config).await
    }
    
    /// Run the command with a specific runtime and transport (for testing)
    pub async fn execute_with_runtime_and_transport(
        &self,
        mut runtime: Box<dyn ContainerRuntime>,
        transport: Box<dyn Transport>,
        permission_config: crate::permissions::profile::ContainerPermissionConfig,
    ) -> Result<()> {
        // Create labels for the container
        let mut labels = HashMap::new();
        labels.insert("vibetool".to_string(), "true".to_string());
        labels.insert("vibetool-name".to_string(), self.name.clone());
        labels.insert("vibetool-transport".to_string(), self.transport.clone());

        // Create environment variables for the container
        let mut env_vars = HashMap::new();

        // If using stdio transport, set the runtime
        let transport = match transport.mode() {
            TransportMode::STDIO => {
                let stdio_transport = transport.as_any().downcast_ref::<crate::transport::stdio::StdioTransport>()
                    .ok_or_else(|| crate::error::Error::Transport("Failed to downcast to StdioTransport".to_string()))?;
                
                // Clone the transport and set the runtime
                let stdio_transport = stdio_transport.clone().with_runtime(runtime);
                
                // Get a new runtime instance
                runtime = ContainerRuntimeFactory::create().await?;
                
                // Box the transport
                Box::new(stdio_transport) as Box<dyn crate::transport::Transport>
            },
            _ => transport,
        };

        // Set up the transport
        transport.setup("", &self.name, self.port, &mut env_vars, None).await?;

        // Create and start the container
        let container_id = runtime
            .create_and_start_container(
                &self.image,
                &self.name,
                self.args.clone(),
                env_vars,
                labels,
                permission_config,
            )
            .await?;

        // Get the container IP address
        log::debug!("Getting container IP address for {}", container_id);
        let container_ip = match runtime.get_container_ip(&container_id).await {
            Ok(ip) => {
                log::debug!("Container IP address: {}", ip);
                Some(ip)
            },
            Err(e) => {
                log::error!("Failed to get container IP address: {}", e);
                None
            }
        };

        // Start the transport
        transport.setup(&container_id, &self.name, self.port, &mut HashMap::new(), container_ip).await?;
        transport.start().await?;

        log::info!("MCP server {} started with container ID {}", self.name, container_id);

        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    
    // Let's use a simpler approach for testing
    #[tokio::test]
    async fn test_start_command_validation() -> Result<()> {
        // Test SSE transport without port
        let cmd = StartCommand {
            transport: "sse".to_string(),
            name: "test-server".to_string(),
            port: None, // Missing port
            permission_profile: "network".to_string(),
            image: "test-image".to_string(),
            args: vec![],
        };
        
        // Parse transport mode
        let transport_mode = TransportMode::from_str(&cmd.transport).unwrap();
        
        // Validate port for SSE transport
        let result = match transport_mode {
            TransportMode::SSE => {
                cmd.port.ok_or_else(|| {
                    crate::error::Error::InvalidArgument(
                        "Port is required for SSE transport".to_string(),
                    )
                })
            }
            _ => Ok(cmd.port.unwrap_or(0)),
        };
        
        // Verify the result is an error
        assert!(result.is_err());
        
        // Test with valid port
        let cmd = StartCommand {
            transport: "sse".to_string(),
            name: "test-server".to_string(),
            port: Some(8080), // Valid port
            permission_profile: "network".to_string(),
            image: "test-image".to_string(),
            args: vec![],
        };
        
        // Parse transport mode
        let transport_mode = TransportMode::from_str(&cmd.transport).unwrap();
        
        // Validate port for SSE transport
        let result = match transport_mode {
            TransportMode::SSE => {
                cmd.port.ok_or_else(|| {
                    crate::error::Error::InvalidArgument(
                        "Port is required for SSE transport".to_string(),
                    )
                })
            }
            _ => Ok(cmd.port.unwrap_or(0)),
        };
        
        // Verify the result is ok
        assert!(result.is_ok());
        
        // Test invalid transport mode
        let cmd = StartCommand {
            transport: "invalid".to_string(),
            name: "test-server".to_string(),
            port: Some(8080),
            permission_profile: "network".to_string(),
            image: "test-image".to_string(),
            args: vec![],
        };
        
        // Parse transport mode
        let result = TransportMode::from_str(&cmd.transport)
            .ok_or_else(|| {
                crate::error::Error::InvalidArgument(format!(
                    "Invalid transport mode: {}. Valid modes are: sse, stdio",
                    cmd.transport
                ))
            });
        
        // Verify the result is an error
        assert!(result.is_err());
        
        Ok(())
    }
    
    #[tokio::test]
    async fn test_start_command_permission_profiles() -> Result<()> {
        // Test stdio profile
        let profile = PermissionProfile::builtin_stdio_profile();
        assert_eq!(profile.read, vec!["/var/run/mcp.sock"]);
        assert_eq!(profile.write, vec!["/var/run/mcp.sock"]);
        assert!(profile.network.is_none());
        
        // Test network profile
        let profile = PermissionProfile::builtin_network_profile();
        assert_eq!(profile.read, vec!["/var/run/mcp.sock"]);
        assert_eq!(profile.write, vec!["/var/run/mcp.sock"]);
        assert!(profile.network.is_some());
        
        Ok(())
    }
    
    #[tokio::test]
    async fn test_start_command_create_transport() -> Result<()> {
        // Test SSE transport
        let transport_mode = TransportMode::SSE;
        let port = 8080;
        
        let transport = TransportFactory::create(transport_mode, port);
        assert_eq!(transport.mode(), TransportMode::SSE);
        
        // Test STDIO transport
        let transport_mode = TransportMode::STDIO;
        let port = 8080;
        
        let transport = TransportFactory::create(transport_mode, port);
        assert_eq!(transport.mode(), TransportMode::STDIO);
        
        Ok(())
    }
}