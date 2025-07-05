package process

import "os"

// ToolHiveDetachedEnv is the environment variable used to indicate that the process is running in detached mode.
const ToolHiveDetachedEnv = "TOOLHIVE_DETACHED"

// ToolHiveDetachedValue is the expected value of ToolHiveDetachedEnv when set.
const ToolHiveDetachedValue = "1"

// IsDetached checks if the process is running in detached mode.
func IsDetached() bool {
	return os.Getenv(ToolHiveDetachedEnv) == ToolHiveDetachedValue
}
