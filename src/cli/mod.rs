use clap::{Parser, Subcommand};

pub mod commands;

/// Vibe Tool (vt): A lightweight, secure, and fast manager for MCP servers
#[derive(Parser, Debug)]
#[command(author, version, about, long_about = None)]
pub struct Cli {
    /// Enable debug output
    #[arg(short, long)]
    pub debug: bool,

    #[command(subcommand)]
    pub command: Option<Commands>,
}

/// Commands for Vibe Tool
#[derive(Subcommand, Debug)]
pub enum Commands {
    /// Run an MCP server
    #[command(name = "run")]
    Run(commands::run::RunCommand),

    /// List running MCP servers
    #[command(name = "list")]
    List(commands::list::ListCommand),

    /// Stop an MCP server
    #[command(name = "stop")]
    Stop(commands::stop::StopCommand),

    /// Remove an MCP server
    #[command(name = "rm")]
    Remove(commands::rm::RemoveCommand),
}