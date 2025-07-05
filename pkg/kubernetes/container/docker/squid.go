package docker

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"

	"github.com/stacklok/toolhive/pkg/kubernetes/container/runtime"
	lb "github.com/stacklok/toolhive/pkg/kubernetes/labels"
	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
	"github.com/stacklok/toolhive/pkg/kubernetes/permissions"
)

const defaultSquidImage = "ghcr.io/stacklok/toolhive/egress-proxy:latest"

// createIngressSquidContainer creates an instance of the squid proxy for ingress traffic.
func createIngressSquidContainer(
	ctx context.Context,
	c *Client,
	containerName string,
	squidContainerName string,
	attachStdio bool,
	upstreamPort int,
	squidPort int,
	exposedPorts map[string]struct{},
	endpointsConfig map[string]*network.EndpointSettings,
	portBindings map[string][]runtime.PortBinding,
) (string, error) {
	squidConfPath, err := createTempIngressSquidConf(containerName, upstreamPort, squidPort)
	if err != nil {
		return "", fmt.Errorf("failed to create temporary squid.conf: %v", err)
	}

	return createSquidContainer(
		ctx,
		c,
		squidContainerName,
		attachStdio,
		exposedPorts,
		endpointsConfig,
		portBindings,
		squidConfPath,
	)
}

// createEgressSquidContainer creates an instance of the squid proxy for egress traffic.
func createEgressSquidContainer(
	ctx context.Context,
	c *Client,
	containerName string,
	squidContainerName string,
	attachStdio bool,
	exposedPorts map[string]struct{},
	endpointsConfig map[string]*network.EndpointSettings,
	perm *permissions.NetworkPermissions,
) (string, error) {
	squidConfPath, err := createTempSquidConf(perm, containerName)
	if err != nil {
		return "", fmt.Errorf("failed to create temporary squid.conf: %v", err)
	}

	return createSquidContainer(
		ctx,
		c,
		squidContainerName,
		attachStdio,
		exposedPorts,
		endpointsConfig,
		nil,
		squidConfPath,
	)
}

// createSquidContainer contains the shared logic for creating a squid container.
func createSquidContainer(
	ctx context.Context,
	c *Client, // TODO: refactor the methods we need from docker.Client into a lower level interface.
	squidContainerName string,
	attachStdio bool,
	exposedPorts map[string]struct{},
	endpointsConfig map[string]*network.EndpointSettings,
	portBindings map[string][]runtime.PortBinding, // used for ingress only
	squidConfPath string,
) (string, error) {

	logger.Infof("Setting up squid container for %s with image %s...", squidContainerName, getSquidImage())
	squidLabels := map[string]string{}
	lb.AddStandardLabels(squidLabels, squidContainerName, squidContainerName, "stdio", 80)
	squidLabels[ToolhiveAuxiliaryWorkloadLabel] = LabelValueTrue

	// pull the squid image if it is not already pulled
	squidImage := getSquidImage()
	// TODO: Move these down into an image operations layer.
	err := c.imageManager.PullImage(ctx, squidImage)
	if err != nil {
		// Check if the squid image exists locally before failing
		_, inspectErr := c.client.ImageInspect(ctx, squidImage)
		if inspectErr == nil {
			logger.Infof("Squid image %s exists locally, continuing despite pull failure", squidImage)
		} else {
			return "", fmt.Errorf("failed to pull squid image: %v", err)
		}
	}

	// Create container options
	config := &container.Config{
		Image:        getSquidImage(),
		Cmd:          nil,
		Env:          nil,
		Labels:       squidLabels,
		AttachStdin:  attachStdio,
		AttachStdout: attachStdio,
		AttachStderr: attachStdio,
		OpenStdin:    attachStdio,
		Tty:          false,
	}

	mounts := []runtime.Mount{}
	mounts = append(mounts, runtime.Mount{
		Source:   squidConfPath,
		Target:   "/etc/squid/squid.conf",
		ReadOnly: true,
	})

	// Create squid host configuration
	squidHostConfig := &container.HostConfig{
		Mounts:      convertMounts(mounts),
		NetworkMode: container.NetworkMode("bridge"),
		CapAdd:      []string{"CAP_SETUID", "CAP_SETGID"},
		CapDrop:     nil,
		SecurityOpt: nil,
		RestartPolicy: container.RestartPolicy{
			Name: "unless-stopped",
		},
	}

	// Setup port bindings
	if portBindings != nil {
		if err := setupPortBindings(squidHostConfig, portBindings); err != nil {
			return "", NewContainerError(err, "", err.Error())
		}
	}

	// Setup port bindings
	if err := setupExposedPorts(config, exposedPorts); err != nil {
		return "", NewContainerError(err, "", err.Error())
	}

	// Create squid container itself
	squidContainerId, err := c.createContainer(ctx, squidContainerName, config, squidHostConfig, endpointsConfig)
	if err != nil {
		return "", fmt.Errorf("failed to create egress container: %v", err)
	}

	return squidContainerId, nil
}

func createTempSquidConf(
	networkPermissions *permissions.NetworkPermissions,
	serverHostname string,
) (string, error) {
	var sb strings.Builder

	sb.WriteString(
		"http_port 3128\n" +
			"visible_hostname " + serverHostname + "-egress\n" +
			"access_log stdio:/dev/stdout squid\n" +
			"pid_filename /tmp/squid.pid\n" +
			"# Disable memory and disk caching\n" +
			"cache deny all\n" +
			"cache_mem 0 MB\n" +
			"maximum_object_size 0 KB\n" +
			"maximum_object_size_in_memory 0 KB\n" +
			"# Don't use cache directories\n" +
			"cache_dir null /tmp\n" +
			"cache_store_log none\n\n")

	if networkPermissions == nil || (networkPermissions.Outbound != nil && networkPermissions.Outbound.InsecureAllowAll) {
		sb.WriteString("# Allow all traffic\nhttp_access allow all\n")
	} else {
		writeOutboundACLs(&sb, networkPermissions.Outbound)
		writeHttpAccessRules(&sb, networkPermissions.Outbound)
	}

	sb.WriteString("http_access deny all\n")

	tmpFile, err := os.CreateTemp("", "squid-*.conf")
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	if _, err := tmpFile.WriteString(sb.String()); err != nil {
		return "", fmt.Errorf("failed to write to temporary file: %v", err)
	}

	// Set file permissions to be readable by all users (including squid user in container)
	if err := tmpFile.Chmod(0644); err != nil {
		return "", fmt.Errorf("failed to set file permissions: %v", err)
	}

	return tmpFile.Name(), nil
}

func writeOutboundACLs(sb *strings.Builder, outbound *permissions.OutboundNetworkPermissions) {
	if len(outbound.AllowPort) > 0 {
		sb.WriteString("# Define allowed ports\nacl allowed_ports port")
		for _, port := range outbound.AllowPort {
			sb.WriteString(" " + strconv.Itoa(port))
		}
		sb.WriteString("\n")
	}

	if len(outbound.AllowHost) > 0 {
		sb.WriteString("# Define allowed destinations\nacl allowed_dsts dstdomain")
		for _, host := range outbound.AllowHost {
			sb.WriteString(" " + host)
		}
		sb.WriteString("\n")
	}

	if len(outbound.AllowTransport) > 0 {
		sb.WriteString("# Define allowed methods\nacl allowed_methods method")
		for _, method := range outbound.AllowTransport {
			if strings.ToUpper(method) == "TCP" {
				sb.WriteString(" CONNECT GET POST HEAD")
			}
			sb.WriteString(" " + strings.ToUpper(method))
		}
	}
}

func writeHttpAccessRules(sb *strings.Builder, outbound *permissions.OutboundNetworkPermissions) {
	var conditions []string
	if len(outbound.AllowPort) > 0 {
		conditions = append(conditions, "allowed_ports")
	}
	if len(outbound.AllowHost) > 0 {
		conditions = append(conditions, "allowed_dsts")
	}
	if len(outbound.AllowTransport) > 0 {
		conditions = append(conditions, "allowed_methods")
	}
	if len(conditions) > 0 {
		sb.WriteString("\n# Define http_access rules\n")
		sb.WriteString("http_access allow " + strings.Join(conditions, " ") + "\n")
	}
}

func getSquidImage() string {
	if egressImage := os.Getenv("TOOLHIVE_EGRESS_IMAGE"); egressImage != "" {
		return egressImage
	}
	return defaultSquidImage
}

func createTempIngressSquidConf(
	serverHostname string,
	upstreamPort int,
	squidPort int,
) (string, error) {
	var sb strings.Builder

	sb.WriteString(
		"visible_hostname " + serverHostname + "-ingress\n" +
			"access_log stdio:/dev/stdout squid\n" +
			"pid_filename /tmp/squid.pid\n" +
			"# Disable memory and disk caching\n" +
			"cache deny all\n" +
			"cache_mem 0 MB\n" +
			"maximum_object_size 0 KB\n" +
			"maximum_object_size_in_memory 0 KB\n" +
			"# Don't use cache directories\n" +
			"cache_dir null /tmp\n" +
			"cache_store_log none\n\n")

	writeIngressProxyConfig(&sb, serverHostname, upstreamPort, squidPort)
	sb.WriteString("http_access deny all\n")

	tmpFile, err := os.CreateTemp("", "squid-*.conf")
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	if _, err := tmpFile.WriteString(sb.String()); err != nil {
		return "", fmt.Errorf("failed to write to temporary file: %v", err)
	}

	// Set file permissions to be readable by all users (including squid user in container)
	if err := tmpFile.Chmod(0644); err != nil {
		return "", fmt.Errorf("failed to set file permissions: %v", err)
	}

	return tmpFile.Name(), nil
}

func writeIngressProxyConfig(sb *strings.Builder, serverHostname string, upstreamPort int, squidPort int) {
	portNum := strconv.Itoa(upstreamPort)
	squidPortNum := strconv.Itoa(squidPort)
	sb.WriteString(
		"\n# Reverse proxy setup for port " + portNum + "\n" +
			"http_port 0.0.0.0:" + squidPortNum + " accel defaultsite=" + serverHostname + "\n" +
			"cache_peer " + serverHostname + " parent " + portNum + " 0 no-query originserver name=origin_" +
			portNum + " connect-timeout=5 connect-fail-limit=5\n" +
			"acl site_" + portNum + " dstdomain " + serverHostname + "\n" +
			"acl local_dst dst 127.0.0.1\n" +
			"acl local_domain dstdomain localhost\n" +
			"http_access allow site_" + portNum + "\n" +
			"http_access allow local_dst\n" +
			"http_access allow local_domain\n")
}
