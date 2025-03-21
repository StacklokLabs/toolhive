use cucumber::World;
use std::path::PathBuf;

// Import our cucumber world and steps
mod common;
mod steps;

// Define our cucumber world
#[derive(cucumber::World, Debug, Default)]
pub struct VibeToolWorld {
    // State shared between steps
    pub command_output: Option<std::process::Output>,
    pub container_id: Option<String>,
    pub server_name: Option<String>,
    pub transport_type: Option<String>,
    pub port: Option<u16>,
    pub image_name: Option<String>,
    pub error_message: Option<String>,
    pub temp_dir: Option<PathBuf>,
}

// Main function that runs our tests
#[tokio::main]
async fn main() {
    // Set up logging
    let _ = env_logger::builder().is_test(true).try_init();

    // Create a new cucumber runner
    VibeToolWorld::run("tests/e2e/features").await;
}
