package docker

import (
	"encoding/json"
	"fmt"

	"github.com/StacklokLabs/toolhive/pkg/logger"
	"github.com/StacklokLabs/toolhive/pkg/permissions"
)

// Docker JSON format (should be compat with podman too)
type seccompProfileTemplate struct {
	DefaultAction string                       `json:"defaultAction"`
	Architectures []string                     `json:"architectures,omitempty"`
	Syscalls      []seccompSyscallRuleTemplate `json:"syscalls"`
}

type seccompSyscallRuleTemplate struct {
	Names  []string `json:"names"`
	Action string   `json:"action"`
}

func generateSeccompProfile(profile *permissions.Profile) (string, error) {
	if profile.Seccomp == nil {
		return "", nil
	}

	// Check if seccomp is explicitly disabled for this profile
	// This basically returns empty profile, meaning, but does not mean no profiles
	// at all, as Docker and Podman have pretty good profiles out of the box
	// https://docs.docker.com/engine/security/seccomp/#significant-syscalls-blocked-by-the-default-profile
	if !profile.Seccomp.Enabled {
		logger.Log.Info("Seccomp profile generation skipped as profile.Seccomp.Enabled is false.")
		return "", nil
	}

	// What sort of action to take on a profile match
	defaultAction := "SCMP_ACT_ERRNO"
	if profile.Seccomp.DefaultAction != "" {
		switch profile.Seccomp.DefaultAction {
		case "allow":
			defaultAction = "SCMP_ACT_ALLOW"
		case "errno":
			defaultAction = "SCMP_ACT_ERRNO"
		case "kill":
			defaultAction = "SCMP_ACT_KILL"
		case "trap":
			defaultAction = "SCMP_ACT_TRAP"
		case "trace":
			defaultAction = "SCMP_ACT_TRACE"
		default:
			logger.Log.Warn(fmt.Sprintf("Warning: Unknown seccomp default action: %s, using SCMP_ACT_ERRNO", profile.Seccomp.DefaultAction))
		}
	}

	// seccomp profile template
	seccompProfile := seccompProfileTemplate{
		DefaultAction: defaultAction,
		Architectures: profile.Seccomp.Architectures,
		Syscalls:      []seccompSyscallRuleTemplate{},
	}

	// Add denied syscalls with ERRNO action
	if len(profile.Seccomp.DeniedSyscalls) > 0 {
		seccompProfile.Syscalls = append(seccompProfile.Syscalls, seccompSyscallRuleTemplate{
			Names:  profile.Seccomp.DeniedSyscalls,
			Action: "SCMP_ACT_ERRNO",
		})
	}

	// Add allowed syscalls with ALLOW action
	if len(profile.Seccomp.AllowedSyscalls) > 0 {
		seccompProfile.Syscalls = append(seccompProfile.Syscalls, seccompSyscallRuleTemplate{
			Names:  profile.Seccomp.AllowedSyscalls,
			Action: "SCMP_ACT_ALLOW",
		})
	}

	seccompJSON, err := json.Marshal(seccompProfile)
	if err != nil {
		return "", fmt.Errorf("failed to marshal seccomp profile: %w", err)
	}

	return string(seccompJSON), nil
}
