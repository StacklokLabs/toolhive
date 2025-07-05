// Package runner provides functionality for running MCP servers
package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/stacklok/toolhive/pkg/kubernetes/audit"
	"github.com/stacklok/toolhive/pkg/kubernetes/auth"
	"github.com/stacklok/toolhive/pkg/kubernetes/authz"
	"github.com/stacklok/toolhive/pkg/kubernetes/container"
	rt "github.com/stacklok/toolhive/pkg/kubernetes/container/runtime"
	"github.com/stacklok/toolhive/pkg/kubernetes/environment"
	"github.com/stacklok/toolhive/pkg/kubernetes/labels"
	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
	"github.com/stacklok/toolhive/pkg/kubernetes/networking"
	"github.com/stacklok/toolhive/pkg/kubernetes/permissions"
	"github.com/stacklok/toolhive/pkg/kubernetes/registry"
	"github.com/stacklok/toolhive/pkg/kubernetes/secrets"
	"github.com/stacklok/toolhive/pkg/kubernetes/telemetry"
	"github.com/stacklok/toolhive/pkg/kubernetes/transport"
	"github.com/stacklok/toolhive/pkg/kubernetes/transport/types"
)

// RunConfig contains all the configuration needed to run an MCP server
// It is serializable to JSON and YAML
type RunConfig struct {
	// Image is the Docker image to run
	Image string `json:"image" yaml:"image"`

	// CmdArgs are the arguments to pass to the container
	CmdArgs []string `json:"cmd_args,omitempty" yaml:"cmd_args,omitempty"`

	// Name is the name of the MCP server
	Name string `json:"name" yaml:"name"`

	// ContainerName is the name of the container
	ContainerName string `json:"container_name" yaml:"container_name"`

	// BaseName is the base name used for the container (without prefixes)
	BaseName string `json:"base_name" yaml:"base_name"`

	// Transport is the transport mode (sse or stdio)
	Transport types.TransportType `json:"transport" yaml:"transport"`

	// Host is the host for the HTTP proxy
	Host string `json:"host" yaml:"host"`

	// Port is the port for the HTTP proxy to listen on (host port)
	Port int `json:"port" yaml:"port"`

	// TargetPort is the port for the container to expose (only applicable to SSE transport)
	TargetPort int `json:"target_port,omitempty" yaml:"target_port,omitempty"`

	// TargetHost is the host to forward traffic to (only applicable to SSE transport)
	TargetHost string `json:"target_host,omitempty" yaml:"target_host,omitempty"`

	// PermissionProfileNameOrPath is the name or path of the permission profile
	PermissionProfileNameOrPath string `json:"permission_profile_name_or_path,omitempty" yaml:"permission_profile_name_or_path,omitempty"` //nolint:lll

	// PermissionProfile is the permission profile to use
	PermissionProfile *permissions.Profile `json:"permission_profile" yaml:"permission_profile"`

	// EnvVars are the parsed environment variables as key-value pairs
	EnvVars map[string]string `json:"env_vars,omitempty" yaml:"env_vars,omitempty"`

	// Debug indicates whether debug mode is enabled
	Debug bool `json:"debug,omitempty" yaml:"debug,omitempty"`

	// Volumes are the directory mounts to pass to the container
	// Format: "host-path:container-path[:ro]"
	Volumes []string `json:"volumes,omitempty" yaml:"volumes,omitempty"`

	// ContainerLabels are the labels to apply to the container
	ContainerLabels map[string]string `json:"container_labels,omitempty" yaml:"container_labels,omitempty"`

	// OIDCConfig contains OIDC configuration
	OIDCConfig *auth.TokenValidatorConfig `json:"oidc_config,omitempty" yaml:"oidc_config,omitempty"`

	// AuthzConfig contains the authorization configuration
	AuthzConfig *authz.Config `json:"authz_config,omitempty" yaml:"authz_config,omitempty"`

	// AuthzConfigPath is the path to the authorization configuration file
	AuthzConfigPath string `json:"authz_config_path,omitempty" yaml:"authz_config_path,omitempty"`

	// AuditConfig contains the audit logging configuration
	AuditConfig *audit.Config `json:"audit_config,omitempty" yaml:"audit_config,omitempty"`

	// AuditConfigPath is the path to the audit configuration file
	AuditConfigPath string `json:"audit_config_path,omitempty" yaml:"audit_config_path,omitempty"`

	// TelemetryConfig contains the OpenTelemetry configuration
	TelemetryConfig *telemetry.Config `json:"telemetry_config,omitempty" yaml:"telemetry_config,omitempty"`

	// Secrets are the secret parameters to pass to the container
	// Format: "<secret name>,target=<target environment variable>"
	Secrets []string `json:"secrets,omitempty" yaml:"secrets,omitempty"`

	// K8sPodTemplatePatch is a JSON string to patch the Kubernetes pod template
	// Only applicable when using Kubernetes runtime
	K8sPodTemplatePatch string `json:"k8s_pod_template_patch,omitempty" yaml:"k8s_pod_template_patch,omitempty"`

	// Runtime is the container runtime to use (not serialized)
	Runtime rt.Runtime `json:"-" yaml:"-"`

	// IsolateNetwork indicates whether to isolate the network for the container
	IsolateNetwork bool `json:"isolate_network,omitempty" yaml:"isolate_network,omitempty"`
}

// WriteJSON serializes the RunConfig to JSON and writes it to the provided writer
func (c *RunConfig) WriteJSON(w io.Writer) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(c)
}

// ReadJSON deserializes the RunConfig from JSON read from the provided reader
func ReadJSON(r io.Reader) (*RunConfig, error) {
	var config RunConfig
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(&config); err != nil {
		return nil, err
	}
	return &config, nil
}

// NewRunConfig creates a new RunConfig with default values
func NewRunConfig() *RunConfig {
	return &RunConfig{
		ContainerLabels: make(map[string]string),
		EnvVars:         make(map[string]string),
	}
}

// NewRunConfigFromFlags creates a new RunConfig with values from command-line flags
// TODO: We may want to use some sort of builder pattern here to make it more readable.
func NewRunConfigFromFlags(
	ctx context.Context,
	runtime rt.Runtime,
	cmdArgs []string,
	name string,
	imageURL string,
	imageMetadata *registry.ImageMetadata,
	host string,
	debug bool,
	volumes []string,
	secretsList []string,
	authzConfigPath string,
	auditConfigPath string,
	enableAudit bool,
	permissionProfile string,
	targetHost string,
	mcpTransport string,
	port int,
	targetPort int,
	envVars []string,
	oidcIssuer string,
	oidcAudience string,
	oidcJwksURL string,
	oidcClientID string,
	oidcAllowOpaqueTokens bool,
	otelEndpoint string,
	otelServiceName string,
	otelSamplingRate float64,
	otelHeaders []string,
	otelInsecure bool,
	otelEnablePrometheusMetricsPath bool,
	otelEnvironmentVariables []string,
	isolateNetwork bool,
	k8sPodPatch string,
	envVarValidator EnvVarValidator,
) (*RunConfig, error) {
	// Ensure default values for host and targetHost
	if host == "" {
		host = transport.LocalhostIPv4
	}
	if targetHost == "" {
		targetHost = transport.LocalhostIPv4
	}

	config := &RunConfig{
		Runtime:                     runtime,
		CmdArgs:                     cmdArgs,
		Name:                        name,
		Image:                       imageURL,
		Debug:                       debug,
		Volumes:                     volumes,
		Secrets:                     secretsList,
		AuthzConfigPath:             authzConfigPath,
		AuditConfigPath:             auditConfigPath,
		PermissionProfileNameOrPath: permissionProfile,
		TargetHost:                  targetHost,
		ContainerLabels:             make(map[string]string),
		EnvVars:                     make(map[string]string),
		Host:                        host,
		IsolateNetwork:              isolateNetwork,
		K8sPodTemplatePatch:         k8sPodPatch,
	}

	// Configure audit if enabled
	configureAudit(config, enableAudit, auditConfigPath)

	// Configure OIDC if any values are provided
	configureOIDC(config, oidcIssuer, oidcAudience, oidcJwksURL, oidcClientID, oidcAllowOpaqueTokens)

	// Configure telemetry if endpoint or metrics port is provided
	configureTelemetry(config, otelEndpoint, otelEnablePrometheusMetricsPath, otelServiceName,
		otelSamplingRate, otelHeaders, otelInsecure, otelEnvironmentVariables)

	// When using the CLI validation strategy, this is where the prompting for
	// missing environment variables will happen.
	processedEnvVars, err := envVarValidator.Validate(ctx, imageMetadata, config, envVars)
	if err != nil {
		return nil, fmt.Errorf("failed to validate environment variables: %v", err)
	}

	// Do some final validation which can only be done after everything else is set.
	// Apply image metadata overrides if needed.
	if err := config.validateConfig(imageMetadata, mcpTransport, port, targetPort); err != nil {
		return nil, fmt.Errorf("failed to validate run config: %v", err)
	}

	// Now set environment variables with the correct transport and ports resolved
	if _, err := config.WithEnvironmentVariables(processedEnvVars); err != nil {
		return nil, fmt.Errorf("failed to set environment variables: %v", err)
	}

	return config, nil
}

// configureAudit sets up audit configuration if enabled
func configureAudit(config *RunConfig, enableAudit bool, auditConfigPath string) {
	if enableAudit && auditConfigPath == "" {
		config.AuditConfig = audit.DefaultConfig()
	}
}

// configureOIDC sets up OIDC configuration if any values are provided
func configureOIDC(config *RunConfig, oidcIssuer, oidcAudience, oidcJwksURL, oidcClientID string, oidcAllowOpaqueTokens bool) {
	if oidcIssuer != "" || oidcAudience != "" || oidcJwksURL != "" || oidcClientID != "" {
		config.OIDCConfig = &auth.TokenValidatorConfig{
			Issuer:            oidcIssuer,
			Audience:          oidcAudience,
			JWKSURL:           oidcJwksURL,
			ClientID:          oidcClientID,
			AllowOpaqueTokens: oidcAllowOpaqueTokens,
		}
	}
}

// configureTelemetry sets up telemetry configuration if endpoint or metrics port is provided
func configureTelemetry(config *RunConfig, otelEndpoint string, otelEnablePrometheusMetricsPath bool,
	otelServiceName string, otelSamplingRate float64, otelHeaders []string, otelInsecure bool,
	otelEnvironmentVariables []string) {

	if otelEndpoint == "" && !otelEnablePrometheusMetricsPath {
		return
	}

	// Parse headers from key=value format
	headers := make(map[string]string)
	for _, header := range otelHeaders {
		parts := strings.SplitN(header, "=", 2)
		if len(parts) == 2 {
			headers[parts[0]] = parts[1]
		}
	}

	// Use provided service name or default
	serviceName := otelServiceName
	if serviceName == "" {
		serviceName = telemetry.DefaultConfig().ServiceName
	}

	// Process environment variables - split comma-separated values
	var processedEnvVars []string
	for _, envVarEntry := range otelEnvironmentVariables {
		// Split by comma and trim whitespace
		envVars := strings.Split(envVarEntry, ",")
		for _, envVar := range envVars {
			trimmed := strings.TrimSpace(envVar)
			if trimmed != "" {
				processedEnvVars = append(processedEnvVars, trimmed)
			}
		}
	}

	config.TelemetryConfig = &telemetry.Config{
		Endpoint:                    otelEndpoint,
		ServiceName:                 serviceName,
		ServiceVersion:              telemetry.DefaultConfig().ServiceVersion,
		SamplingRate:                otelSamplingRate,
		Headers:                     headers,
		Insecure:                    otelInsecure,
		EnablePrometheusMetricsPath: otelEnablePrometheusMetricsPath,
		EnvironmentVariables:        processedEnvVars,
	}
}

// WithAuthz adds authorization configuration to the RunConfig
func (c *RunConfig) WithAuthz(config *authz.Config) *RunConfig {
	c.AuthzConfig = config
	return c
}

// WithAudit adds audit configuration to the RunConfig
func (c *RunConfig) WithAudit(config *audit.Config) *RunConfig {
	c.AuditConfig = config
	return c
}

// WithTransport parses and sets the transport type
func (c *RunConfig) WithTransport(t string) (*RunConfig, error) {
	transportType, err := types.ParseTransportType(t)
	if err != nil {
		return c, fmt.Errorf("invalid transport mode: %s. Valid modes are: sse, streamable-http, stdio", t)
	}
	c.Transport = transportType
	return c, nil
}

// WithPorts configures the host and target ports
func (c *RunConfig) WithPorts(port, targetPort int) (*RunConfig, error) {
	// Select a port for the HTTP proxy (host port)
	selectedPort, err := networking.FindOrUsePort(port)
	if err != nil {
		return c, err
	}
	c.Port = selectedPort

	// Select a target port for the container if using SSE or Streamable HTTP transport
	if c.Transport == types.TransportTypeSSE || c.Transport == types.TransportTypeStreamableHTTP {
		selectedTargetPort, err := networking.FindOrUsePort(targetPort)
		if err != nil {
			return c, fmt.Errorf("target port error: %w", err)
		}
		logger.Infof("Using target port: %d", selectedTargetPort)
		c.TargetPort = selectedTargetPort
	}

	return c, nil
}

// ParsePermissionProfile loads and sets the permission profile
func (c *RunConfig) ParsePermissionProfile() (*RunConfig, error) {
	if c.PermissionProfileNameOrPath == "" {
		return c, fmt.Errorf("permission profile name or path is required")
	}

	var permProfile *permissions.Profile
	var err error

	switch c.PermissionProfileNameOrPath {
	case permissions.ProfileNone, "stdio":
		permProfile = permissions.BuiltinNoneProfile()
	case permissions.ProfileNetwork:
		permProfile = permissions.BuiltinNetworkProfile()
	default:
		// Try to load from file
		permProfile, err = permissions.FromFile(c.PermissionProfileNameOrPath)
		if err != nil {
			return c, fmt.Errorf("failed to load permission profile: %v", err)
		}
	}

	c.PermissionProfile = permProfile
	return c, nil
}

// WithEnvironmentVariables parses and sets environment variables
func (c *RunConfig) WithEnvironmentVariables(envVarStrings []string) (*RunConfig, error) {
	envVars, err := environment.ParseEnvironmentVariables(envVarStrings)
	if err != nil {
		return c, fmt.Errorf("failed to parse environment variables: %v", err)
	}

	// Initialize EnvVars if it's nil
	if c.EnvVars == nil {
		c.EnvVars = make(map[string]string)
	}

	// Merge the parsed environment variables with existing ones
	for key, value := range envVars {
		c.EnvVars[key] = value
	}

	// Set transport-specific environment variables
	environment.SetTransportEnvironmentVariables(c.EnvVars, string(c.Transport), c.TargetPort)
	return c, nil
}

// WithSecrets processes secrets and adds them to environment variables
func (c *RunConfig) WithSecrets(ctx context.Context, secretManager secrets.Provider) (*RunConfig, error) {
	if len(c.Secrets) == 0 {
		return c, nil // No secrets to process
	}

	secretVariables, err := environment.ParseSecretParameters(ctx, c.Secrets, secretManager)
	if err != nil {
		return c, fmt.Errorf("failed to get secrets: %v", err)
	}

	// Initialize EnvVars if it's nil
	if c.EnvVars == nil {
		c.EnvVars = make(map[string]string)
	}

	// Add secret variables to environment variables
	for key, value := range secretVariables {
		c.EnvVars[key] = value
	}

	return c, nil
}

// WithContainerName generates container name if not already set
func (c *RunConfig) WithContainerName() *RunConfig {
	if c.ContainerName == "" && c.Image != "" {
		containerName, baseName := container.GetOrGenerateContainerName(c.Name, c.Image)
		c.ContainerName = containerName
		c.BaseName = baseName
	}
	return c
}

// WithStandardLabels adds standard labels to the container
func (c *RunConfig) WithStandardLabels() *RunConfig {
	if c.ContainerLabels == nil {
		c.ContainerLabels = make(map[string]string)
	}
	// Use Name if ContainerName is not set
	containerName := c.ContainerName
	if containerName == "" {
		containerName = c.Name
	}
	labels.AddStandardLabels(c.ContainerLabels, containerName, c.BaseName, string(c.Transport), c.Port)
	return c
}

// ProcessVolumeMounts processes volume mounts and adds them to the permission profile
func (c *RunConfig) ProcessVolumeMounts() error {
	// Skip if no volumes to process
	if len(c.Volumes) == 0 {
		return nil
	}

	// Ensure permission profile is loaded
	if c.PermissionProfile == nil {
		if c.PermissionProfileNameOrPath == "" {
			return fmt.Errorf("permission profile is required when using volume mounts")
		}

		// Load the permission profile from the specified name or path
		_, err := c.ParsePermissionProfile()
		if err != nil {
			return fmt.Errorf("failed to load permission profile: %v", err)
		}
	}

	// Create a map of existing mount targets for quick lookup
	existingMounts := make(map[string]string)

	// Add existing read mounts to the map
	for _, m := range c.PermissionProfile.Read {
		source, target, _ := m.Parse()
		existingMounts[target] = source
	}

	// Add existing write mounts to the map
	for _, m := range c.PermissionProfile.Write {
		source, target, _ := m.Parse()
		existingMounts[target] = source
	}

	// Process each volume mount
	for _, volume := range c.Volumes {
		// Parse read-only flag
		readOnly := strings.HasSuffix(volume, ":ro")
		volumeSpec := volume
		if readOnly {
			volumeSpec = strings.TrimSuffix(volume, ":ro")
		}

		// Create and parse mount declaration
		mount := permissions.MountDeclaration(volumeSpec)
		source, target, err := mount.Parse()
		if err != nil {
			return fmt.Errorf("invalid volume format: %s (%v)", volume, err)
		}

		// Check for duplicate mount target
		if existingSource, isDuplicate := existingMounts[target]; isDuplicate {
			logger.Warnf("Skipping duplicate mount target: %s (already mounted from %s)",
				target, existingSource)
			continue
		}

		// Add the mount to the appropriate permission list
		if readOnly {
			c.PermissionProfile.Read = append(c.PermissionProfile.Read, mount)
		} else {
			c.PermissionProfile.Write = append(c.PermissionProfile.Write, mount)
		}

		// Add to the map of existing mounts
		existingMounts[target] = source

		logger.Infof("Adding volume mount: %s -> %s (%s)",
			source, target,
			map[bool]string{true: "read-only", false: "read-write"}[readOnly])
	}

	return nil
}

// validateConfig ensures the RunConfig is valid and sets up some of the final
// configuration details which can only be applied after all other flags are added.
// This function also handles setting missing values based on the image metadata (if present).
//
//nolint:gocyclo // This function needs to be refactored to reduce cyclomatic complexity.
func (c *RunConfig) validateConfig(
	imageMetadata *registry.ImageMetadata,
	mcpTransport string,
	port int,
	targetPort int,
) error {
	var err error

	// The old logic claimed to override the name with the name from the registry
	// but didn't. Instead, it used the name passed in from the CLI.
	// See: https://github.com/stacklok/toolhive/blob/2873152b62bf61698cbcdd0aba1707a046151e67/cmd/thv/app/run.go#L425
	// The following code implements what I believe was the intended behavior:
	if c.Name == "" && imageMetadata != nil {
		c.Name = imageMetadata.Name
	}

	// Check to see if the mcpTransport is defined in the metadata.
	// Use this value if it was not set by the user.
	// Else, default to stdio.
	if mcpTransport == "" {
		if imageMetadata != nil && imageMetadata.Transport != "" {
			logger.Debugf("Using registry mcpTransport: %s", imageMetadata.Transport)
			mcpTransport = imageMetadata.Transport
		} else {
			logger.Debugf("Defaulting mcpTransport to stdio")
			mcpTransport = types.TransportTypeStdio.String()
		}
	}
	// Set mcpTransport
	if _, err = c.WithTransport(mcpTransport); err != nil {
		return err
	}

	// Use registry target port if not overridden and if the mcpTransport is HTTP-based.
	if imageMetadata != nil {
		isHTTPServer := mcpTransport == types.TransportTypeSSE.String() ||
			mcpTransport == types.TransportTypeStreamableHTTP.String()
		if targetPort == 0 && isHTTPServer && imageMetadata.TargetPort > 0 {
			logger.Debugf("Using registry target port: %d", imageMetadata.TargetPort)
			targetPort = imageMetadata.TargetPort
		}
	}
	// Configure ports and target host
	if _, err = c.WithPorts(port, targetPort); err != nil {
		return err
	}

	// If we are missing the permission profile, attempt to load one from the image metadata.
	if c.PermissionProfileNameOrPath == "" && imageMetadata != nil {
		permProfilePath, err := CreatePermissionProfileFile(c.Name, imageMetadata.Permissions)
		if err != nil {
			// Just log the error and continue with the default permission profile
			logger.Warnf("Warning: failed to create permission profile file: %v", err)
		} else {
			// Update the permission profile path
			c.PermissionProfileNameOrPath = permProfilePath
		}
	}
	// Set permission profile (mandatory)
	if _, err = c.ParsePermissionProfile(); err != nil {
		return err
	}

	// Process volume mounts
	if err = c.ProcessVolumeMounts(); err != nil {
		return err
	}

	// Generate container name if not already set
	c.WithContainerName()

	// Add standard labels
	c.WithStandardLabels()

	// Add authorization configuration if provided
	if c.AuthzConfigPath != "" {
		authzConfig, err := authz.LoadConfig(c.AuthzConfigPath)
		if err != nil {
			return fmt.Errorf("failed to load authorization configuration: %v", err)
		}
		c.WithAuthz(authzConfig)
	}

	// Add audit configuration if provided
	if c.AuditConfigPath != "" {
		auditConfig, err := audit.LoadFromFile(c.AuditConfigPath)
		if err != nil {
			return fmt.Errorf("failed to load audit configuration: %v", err)
		}
		c.WithAudit(auditConfig)
	}
	// Note: AuditConfig is already set from --enable-audit flag if provided

	// Prepend registry args to command-line args if available
	if imageMetadata != nil && len(imageMetadata.Args) > 0 {
		logger.Debugf("Prepending registry args: %v", imageMetadata.Args)
		c.CmdArgs = append(c.CmdArgs, imageMetadata.Args...)
	}

	return nil
}
