package state

const (
	// RunConfigsDir is the directory name for storing run configurations
	RunConfigsDir = "runconfigs"

	// GroupConfigsDir is the directory name for storing group configurations
	GroupConfigsDir = "groups"
)

// NewRunConfigStore creates a store for run configuration state
func NewRunConfigStore(appName string) (Store, error) {
	return NewLocalStore(appName, RunConfigsDir)
}

// NewGroupConfigStore creates a store for group configurations
func NewGroupConfigStore(appName string) (Store, error) {
	return NewLocalStore(appName, GroupConfigsDir)
}
