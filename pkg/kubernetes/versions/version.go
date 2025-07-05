// Package versions provides version information for the ToolHive application.
package versions

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

const (
	unknownStr = "unknown"
)

// Version information set by build using -ldflags
var (
	// Version is the current version of ToolHive
	Version = "dev"
	// Commit is the git commit hash of the build
	//nolint:goconst // This is a placeholder for the commit hash
	Commit = unknownStr
	// BuildDate is the date when the binary was built
	// nolint:goconst // This is a placeholder for the build date
	BuildDate = unknownStr
	// BuildType indicates if this is a release build.
	// Set to "release" only in official release builds, everything else is considered "development".
	BuildType = "development"
)

// VersionInfo represents the version information
type VersionInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
}

// GetVersionInfo returns the version information
func GetVersionInfo() VersionInfo {
	// If version is still "dev", try to get it from build info
	ver := Version
	commit := Commit
	buildDate := BuildDate

	if strings.HasPrefix(ver, "dev") {
		if info, ok := debug.ReadBuildInfo(); ok {
			// Try to get version from build info
			for _, setting := range info.Settings {
				switch setting.Key {
				case "vcs.revision":
					if commit == unknownStr {
						commit = setting.Value
					}
				case "vcs.time":
					if buildDate == unknownStr {
						buildDate = setting.Value
					}
				}
			}
		}
	}

	// Format the build date if it's a timestamp
	if buildDate != unknownStr {
		if t, err := time.Parse(time.RFC3339, buildDate); err == nil {
			buildDate = t.Format("2006-01-02 15:04:05 MST")
		}
	}

	// If the version is just "dev" - manufacture a version string using the commit.
	// NOTE: Ignore any IDE warnings about this condition always being true - it is
	// overridden by the build flags.
	if ver == "dev" {
		// Truncate commit to 8 characters for brevity.
		ver = fmt.Sprintf("build-%s", fmt.Sprintf("%.*s", 8, commit))
	}

	return VersionInfo{
		Version:   ver,
		Commit:    commit,
		BuildDate: buildDate,
		GoVersion: runtime.Version(),
		Platform:  fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}
}
