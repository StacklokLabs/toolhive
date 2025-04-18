// Package kubernetes provides a client for the Kubernetes runtime
// including creating, starting, stopping, and retrieving container information.
package kubernetes

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	backoff "github.com/cenkalti/backoff/v4"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	apimwatch "k8s.io/apimachinery/pkg/watch"
	appsv1apply "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1apply "k8s.io/client-go/applyconfigurations/core/v1"
	metav1apply "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/tools/watch"

	"github.com/StacklokLabs/toolhive/pkg/container/runtime"
	"github.com/StacklokLabs/toolhive/pkg/logger"
	"github.com/StacklokLabs/toolhive/pkg/permissions"
	transtypes "github.com/StacklokLabs/toolhive/pkg/transport/types"
)

// Constants for container status
const (
	// UnknownStatus represents an unknown container status
	UnknownStatus = "unknown"
)

// Client implements the Runtime interface for container operations
type Client struct {
	runtimeType runtime.Type
	client      *kubernetes.Clientset
}

// NewClient creates a new container client
func NewClient(_ context.Context) (*Client, error) {
	// creates the in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to create in-cluster config: %v", err)
	}
	// creates the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	return &Client{
		runtimeType: runtime.TypeKubernetes,
		client:      clientset,
	}, nil
}

// getNamespaceFromServiceAccount attempts to read the namespace from the service account token file
func getNamespaceFromServiceAccount() (string, error) {
	data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return "", fmt.Errorf("failed to read namespace file: %w", err)
	}
	return string(data), nil
}

// getNamespaceFromEnv attempts to get the namespace from environment variables
func getNamespaceFromEnv() (string, error) {
	ns := os.Getenv("POD_NAMESPACE")
	if ns == "" {
		return "", fmt.Errorf("POD_NAMESPACE environment variable not set")
	}
	return ns, nil
}

// getCurrentNamespace returns the namespace the pod is running in.
// It tries multiple methods in order:
// 1. Reading from the service account token file
// 2. Getting the namespace from environment variables
// 3. Falling back to "default" if both methods fail
func getCurrentNamespace() string {
	// Method 1: Try to read from the service account namespace file
	ns, err := getNamespaceFromServiceAccount()
	if err == nil {
		return ns
	}

	// Method 2: Try to get the namespace from environment variables
	ns, err = getNamespaceFromEnv()
	if err == nil {
		return ns
	}

	// Method 3: Fall back to default
	return "default"
}

// AttachContainer implements runtime.Runtime.
func (c *Client) AttachContainer(ctx context.Context, containerID string) (io.WriteCloser, io.ReadCloser, error) {
	// AttachContainer attaches to a container in Kubernetes
	// This is a more complex operation in Kubernetes compared to Docker/Podman
	// as it requires setting up an exec session to the pod

	// First, we need to find the pod associated with the containerID (which is actually the statefulset name)
	namespace := getCurrentNamespace()
	pods, err := c.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", containerID),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to find pod for container %s: %w", containerID, err)
	}

	if len(pods.Items) == 0 {
		return nil, nil, fmt.Errorf("no pods found for container %s", containerID)
	}

	// Use the first pod found
	podName := pods.Items[0].Name

	attachOpts := &corev1.PodAttachOptions{
		Container: containerID,
		Stdin:     true,
		Stdout:    true,
		Stderr:    true,
		TTY:       false,
	}

	// Set up the attach request
	req := c.client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(getCurrentNamespace()).
		SubResource("attach").
		VersionedParams(attachOpts, scheme.ParameterCodec)

	config, err := rest.InClusterConfig()
	if err != nil {
		panic(fmt.Errorf("failed to create k8s config: %v", err))
	}
	// Create a SPDY executor
	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create SPDY executor: %v", err)
	}

	logger.Log.Infof("Attaching to pod %s container %s...", podName, containerID)

	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	//nolint:gosec // we don't check for an error here because it's not critical
	// and it also returns with an error of statuscode `0`'. perhaps someone
	// who knows the function a bit more can fix this.
	go func() {
		// wrap with retry so we can retry if the connection fails
		// Create exponential backoff with max 5 retries
		expBackoff := backoff.NewExponentialBackOff()
		backoffWithRetries := backoff.WithMaxRetries(expBackoff, 5)

		err := backoff.RetryNotify(func() error {
			return exec.StreamWithContext(ctx, remotecommand.StreamOptions{
				Stdin:  stdinReader,
				Stdout: stdoutWriter,
				Stderr: stdoutWriter,
				Tty:    false,
			})
		}, backoffWithRetries, func(err error, duration time.Duration) {
			logger.Log.Errorf("Error attaching to container %s: %v. Retrying in %s...", containerID, err, duration)
		})
		if err != nil {
			if statusErr, ok := err.(*errors.StatusError); ok {
				logger.Log.Errorf("Kubernetes API error: Status=%s, Message=%s, Reason=%s, Code=%d",
					statusErr.ErrStatus.Status,
					statusErr.ErrStatus.Message,
					statusErr.ErrStatus.Reason,
					statusErr.ErrStatus.Code)

				if statusErr.ErrStatus.Code == 0 && statusErr.ErrStatus.Message == "" {
					logger.Log.Infof("Empty status error - this typically means the connection was closed unexpectedly")
					logger.Log.Infof("This often happens when the container terminates or doesn't read from stdin")
				}
			} else {
				logger.Log.Errorf("Non-status error: %v", err)
			}
		}
	}()

	return stdinWriter, stdoutReader, nil
}

// ContainerLogs implements runtime.Runtime.
func (c *Client) ContainerLogs(ctx context.Context, containerID string) (string, error) {
	// In Kubernetes, containerID is the statefulset name
	namespace := getCurrentNamespace()

	// Get the pods associated with this statefulset
	pods, err := c.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", containerID),
	})
	if err != nil {
		return "", fmt.Errorf("failed to list pods for statefulset %s: %w", containerID, err)
	}

	if len(pods.Items) == 0 {
		return "", fmt.Errorf("no pods found for statefulset %s", containerID)
	}

	// Use the first pod
	podName := pods.Items[0].Name

	// Get logs from the pod
	logOptions := &corev1.PodLogOptions{
		Container:  containerID, // Use the container name within the pod
		Follow:     false,
		Previous:   false,
		Timestamps: true,
	}

	req := c.client.CoreV1().Pods(namespace).GetLogs(podName, logOptions)
	podLogs, err := req.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get logs for pod %s: %w", podName, err)
	}
	defer podLogs.Close()

	// Read logs
	logBytes, err := io.ReadAll(podLogs)
	if err != nil {
		return "", fmt.Errorf("failed to read logs for pod %s: %w", podName, err)
	}

	return string(logBytes), nil
}

// CreateContainer implements runtime.Runtime.
func (c *Client) CreateContainer(ctx context.Context,
	image string,
	containerName string,
	command []string,
	envVars map[string]string,
	containerLabels map[string]string,
	_ *permissions.Profile, // TODO: Implement permission profile support for Kubernetes
	transportType string,
	options *runtime.CreateContainerOptions) (string, error) {
	namespace := getCurrentNamespace()
	containerLabels["app"] = containerName
	containerLabels["toolhive"] = "true"

	attachStdio := options == nil || options.AttachStdio

	// Convert environment variables to Kubernetes format
	var envVarList []*corev1apply.EnvVarApplyConfiguration
	for k, v := range envVars {
		envVarList = append(envVarList, corev1apply.EnvVar().WithName(k).WithValue(v))
	}

	// Create container configuration
	containerConfig := corev1apply.Container().
		WithName(containerName).
		WithImage(image).
		WithArgs(command...).
		WithStdin(attachStdio).
		WithTTY(false).
		WithEnv(envVarList...)

	// Configure ports if needed for SSE transport
	if options != nil && transportType == string(transtypes.TransportTypeSSE) {
		var err error
		containerConfig, err = configureContainerPorts(containerConfig, options)
		if err != nil {
			return "", err
		}
	}

	// Create an apply configuration for the statefulset
	statefulSetApply := appsv1apply.StatefulSet(containerName, namespace).
		WithLabels(containerLabels).
		WithSpec(appsv1apply.StatefulSetSpec().
			WithReplicas(1).
			WithSelector(metav1apply.LabelSelector().
				WithMatchLabels(map[string]string{
					"app": containerName,
				})).
			WithServiceName(containerName).
			WithTemplate(corev1apply.PodTemplateSpec().
				WithLabels(containerLabels).
				WithSpec(corev1apply.PodSpec().
					WithContainers(containerConfig).
					WithRestartPolicy(corev1.RestartPolicyAlways))))

	// Apply the statefulset using server-side apply
	fieldManager := "toolhive-container-manager"
	createdStatefulSet, err := c.client.AppsV1().StatefulSets(namespace).
		Apply(ctx, statefulSetApply, metav1.ApplyOptions{
			FieldManager: fieldManager,
			Force:        true,
		})
	if err != nil {
		return "", fmt.Errorf("failed to apply statefulset: %v", err)
	}

	logger.Log.Infof("Applied statefulset %s", createdStatefulSet.Name)

	if transportType == string(transtypes.TransportTypeSSE) && options != nil {
		// Create a headless service for SSE transport
		err := c.createHeadlessService(ctx, containerName, namespace, containerLabels, options)
		if err != nil {
			return "", fmt.Errorf("failed to create headless service: %v", err)
		}
	}

	// Wait for the statefulset to be ready
	err = waitForStatefulSetReady(ctx, c.client, namespace, createdStatefulSet.Name)
	if err != nil {
		return createdStatefulSet.Name, fmt.Errorf("statefulset applied but failed to become ready: %w", err)
	}

	return createdStatefulSet.Name, nil
}

// GetContainerInfo implements runtime.Runtime.
func (c *Client) GetContainerInfo(ctx context.Context, containerID string) (runtime.ContainerInfo, error) {
	// In Kubernetes, containerID is the statefulset name
	namespace := getCurrentNamespace()

	// Get the statefulset
	statefulset, err := c.client.AppsV1().StatefulSets(namespace).Get(ctx, containerID, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return runtime.ContainerInfo{}, fmt.Errorf("statefulset %s not found", containerID)
		}
		return runtime.ContainerInfo{}, fmt.Errorf("failed to get statefulset %s: %w", containerID, err)
	}

	// Get the pods associated with this statefulset
	pods, err := c.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", containerID),
	})
	if err != nil {
		return runtime.ContainerInfo{}, fmt.Errorf("failed to list pods for statefulset %s: %w", containerID, err)
	}

	// Extract port mappings from pods
	ports := make([]runtime.PortMapping, 0)
	if len(pods.Items) > 0 {
		ports = extractPortMappingsFromPod(&pods.Items[0])
	}

	// Get ports from associated service (for SSE transport)
	service, err := c.client.CoreV1().Services(namespace).Get(ctx, containerID, metav1.GetOptions{})
	if err == nil {
		// Service exists, add its ports
		ports = extractPortMappingsFromService(service, ports)
	}

	// Determine status and state
	var status, state string
	if statefulset.Status.ReadyReplicas > 0 {
		status = "Running"
		state = "running"
	} else if statefulset.Status.Replicas > 0 {
		status = "Pending"
		state = "pending"
	} else {
		status = "Stopped"
		state = "stopped"
	}

	// Get the image from the pod template
	image := ""
	if len(statefulset.Spec.Template.Spec.Containers) > 0 {
		image = statefulset.Spec.Template.Spec.Containers[0].Image
	}

	return runtime.ContainerInfo{
		ID:      string(statefulset.UID),
		Name:    statefulset.Name,
		Image:   image,
		Status:  status,
		State:   state,
		Created: statefulset.CreationTimestamp.Time,
		Labels:  statefulset.Labels,
		Ports:   ports,
	}, nil
}

// ImageExists implements runtime.Runtime.
func (*Client) ImageExists(_ context.Context, imageName string) (bool, error) {
	// In Kubernetes, we can't directly check if an image exists in the cluster
	// without trying to use it. For simplicity, we'll assume the image exists
	// if it's a valid image name.
	//
	// In a more complete implementation, we could:
	// 1. Create a temporary pod with the image to see if it can be pulled
	// 2. Use the Kubernetes API to check node status for the image
	// 3. Use an external registry API to check if the image exists

	// For now, just return true if the image name is not empty
	if imageName == "" {
		return false, fmt.Errorf("image name cannot be empty")
	}

	// We could add more validation here if needed
	return true, nil
}

// IsContainerRunning implements runtime.Runtime.
func (c *Client) IsContainerRunning(ctx context.Context, containerID string) (bool, error) {
	// In Kubernetes, containerID is the statefulset name
	namespace := getCurrentNamespace()

	// Get the statefulset
	statefulset, err := c.client.AppsV1().StatefulSets(namespace).Get(ctx, containerID, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return false, fmt.Errorf("statefulset %s not found", containerID)
		}
		return false, fmt.Errorf("failed to get statefulset %s: %w", containerID, err)
	}

	// Check if the statefulset has at least one ready replica
	return statefulset.Status.ReadyReplicas > 0, nil
}

// ListContainers implements runtime.Runtime.
func (c *Client) ListContainers(ctx context.Context) ([]runtime.ContainerInfo, error) {
	// Create label selector for toolhive containers
	labelSelector := "toolhive=true"

	// List pods with the toolhive label
	namespace := getCurrentNamespace()
	pods, err := c.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %v", err)
	}

	// Convert to our ContainerInfo format
	result := make([]runtime.ContainerInfo, 0, len(pods.Items))
	for _, pod := range pods.Items {
		// Extract port mappings from pod
		ports := extractPortMappingsFromPod(&pod)

		// Get ports from associated service (for SSE transport)
		service, err := c.client.CoreV1().Services(namespace).Get(ctx, pod.Name, metav1.GetOptions{})
		if err == nil {
			// Service exists, add its ports
			ports = extractPortMappingsFromService(service, ports)
		}

		// Get container status
		status := UnknownStatus
		state := UnknownStatus
		if len(pod.Status.ContainerStatuses) > 0 {
			containerStatus := pod.Status.ContainerStatuses[0]
			if containerStatus.State.Running != nil {
				state = "running"
				status = "Running"
			} else if containerStatus.State.Waiting != nil {
				state = "waiting"
				status = containerStatus.State.Waiting.Reason
			} else if containerStatus.State.Terminated != nil {
				state = "terminated"
				status = containerStatus.State.Terminated.Reason
			}
		}

		result = append(result, runtime.ContainerInfo{
			ID:      string(pod.UID),
			Name:    pod.Name,
			Image:   pod.Spec.Containers[0].Image,
			Status:  status,
			State:   state,
			Created: pod.CreationTimestamp.Time,
			Labels:  pod.Labels,
			Ports:   ports,
		})
	}

	return result, nil
}

// PullImage implements runtime.Runtime.
func (*Client) PullImage(_ context.Context, imageName string) error {
	// In Kubernetes, we don't need to explicitly pull images as they are pulled
	// automatically when creating pods. The kubelet on each node will pull the
	// image when needed.

	// Log that we're skipping the pull operation
	logger.Log.Infof("Skipping explicit image pull for %s in Kubernetes - "+
		"images are pulled automatically when pods are created", imageName)

	return nil
}

// BuildImage implements runtime.Runtime.
func (*Client) BuildImage(_ context.Context, _, _ string) error {
	// In Kubernetes, we don't build images directly within the cluster.
	// Images should be built externally and pushed to a registry.
	logger.Log.Warnf("BuildImage is not supported in Kubernetes runtime. " +
		"Images should be built externally and pushed to a registry.")
	return fmt.Errorf("building images directly is not supported in Kubernetes runtime")
}

// RemoveContainer implements runtime.Runtime.
func (c *Client) RemoveContainer(ctx context.Context, containerID string) error {
	// In Kubernetes, we remove a container by deleting the statefulset
	namespace := getCurrentNamespace()

	// Delete the statefulset
	deleteOptions := metav1.DeleteOptions{}
	err := c.client.AppsV1().StatefulSets(namespace).Delete(ctx, containerID, deleteOptions)
	if err != nil {
		if errors.IsNotFound(err) {
			// If the statefulset doesn't exist, that's fine
			logger.Log.Infof("Statefulset %s not found, nothing to remove", containerID)
			return nil
		}
		return fmt.Errorf("failed to delete statefulset %s: %w", containerID, err)
	}

	logger.Log.Infof("Deleted statefulset %s", containerID)
	return nil
}

// StopContainer implements runtime.Runtime.
func (*Client) StopContainer(_ context.Context, _ string) error {
	return nil
}

// waitForStatefulSetReady waits for a statefulset to be ready using the watch API
func waitForStatefulSetReady(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) error {
	// Create a field selector to watch only this specific statefulset
	fieldSelector := fmt.Sprintf("metadata.name=%s", name)

	// Set up the watch
	watcher, err := clientset.AppsV1().StatefulSets(namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: fieldSelector,
		Watch:         true,
	})
	if err != nil {
		return fmt.Errorf("error watching statefulset: %w", err)
	}

	// Define the condition function that checks if the statefulset is ready
	isStatefulSetReady := func(event apimwatch.Event) (bool, error) {
		// Check if the event is a statefulset
		statefulSet, ok := event.Object.(*appsv1.StatefulSet)
		if !ok {
			return false, fmt.Errorf("unexpected object type: %T", event.Object)
		}

		// Check if the statefulset is ready
		if statefulSet.Status.ReadyReplicas == *statefulSet.Spec.Replicas {
			return true, nil
		}

		logger.Log.Infof("Waiting for statefulset %s to be ready (%d/%d replicas ready)...",
			name, statefulSet.Status.ReadyReplicas, *statefulSet.Spec.Replicas)
		return false, nil
	}

	// Create a context with timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	// Wait for the statefulset to be ready
	_, err = watch.UntilWithoutRetry(timeoutCtx, watcher, isStatefulSetReady)
	if err != nil {
		return fmt.Errorf("error waiting for statefulset to be ready: %w", err)
	}

	return nil
}

// parsePortString parses a port string in the format "port/protocol" and returns the port number
func parsePortString(portStr string) (int, error) {
	// Split the port string to get just the port number
	port := strings.Split(portStr, "/")[0]
	portNum, err := strconv.Atoi(port)
	if err != nil {
		return 0, fmt.Errorf("failed to parse port %s: %v", port, err)
	}
	return portNum, nil
}

// configureContainerPorts adds port configurations to a container for SSE transport
func configureContainerPorts(
	containerConfig *corev1apply.ContainerApplyConfiguration,
	options *runtime.CreateContainerOptions,
) (*corev1apply.ContainerApplyConfiguration, error) {
	if options == nil {
		return containerConfig, nil
	}

	// Use a map to track which ports have been added
	portMap := make(map[int32]bool)
	var containerPorts []*corev1apply.ContainerPortApplyConfiguration

	// Process exposed ports
	for portStr := range options.ExposedPorts {
		portNum, err := parsePortString(portStr)
		if err != nil {
			return nil, err
		}

		// Check for integer overflow
		if portNum < 0 || portNum > 65535 {
			return nil, fmt.Errorf("port number %d is out of valid range (0-65535)", portNum)
		}

		// Add port if not already in the map
		portInt32 := int32(portNum)
		if !portMap[portInt32] {
			containerPorts = append(containerPorts, corev1apply.ContainerPort().
				WithContainerPort(portInt32).
				WithProtocol(corev1.ProtocolTCP))
			portMap[portInt32] = true
		}
	}

	// Process port bindings
	for portStr := range options.PortBindings {
		portNum, err := parsePortString(portStr)
		if err != nil {
			return nil, err
		}

		// Check for integer overflow
		if portNum < 0 || portNum > 65535 {
			return nil, fmt.Errorf("port number %d is out of valid range (0-65535)", portNum)
		}

		// Add port if not already in the map
		portInt32 := int32(portNum)
		if !portMap[portInt32] {
			containerPorts = append(containerPorts, corev1apply.ContainerPort().
				WithContainerPort(portInt32).
				WithProtocol(corev1.ProtocolTCP))
			portMap[portInt32] = true
		}
	}

	// Add ports to container config
	if len(containerPorts) > 0 {
		containerConfig = containerConfig.WithPorts(containerPorts...)
	}

	return containerConfig, nil
}

// validatePortNumber checks if a port number is within the valid range
func validatePortNumber(portNum int) error {
	if portNum < 0 || portNum > 65535 {
		return fmt.Errorf("port number %d is out of valid range (0-65535)", portNum)
	}
	return nil
}

// createServicePortConfig creates a service port configuration for a given port number
func createServicePortConfig(portNum int) *corev1apply.ServicePortApplyConfiguration {
	//nolint:gosec // G115: Safe int->int32 conversion, range is checked in validatePortNumber
	portInt32 := int32(portNum)
	return corev1apply.ServicePort().
		WithName(fmt.Sprintf("port-%d", portNum)).
		WithPort(portInt32).
		WithTargetPort(intstr.FromInt(portNum)).
		WithProtocol(corev1.ProtocolTCP)
}

// processExposedPorts processes exposed ports and adds them to the port map
func processExposedPorts(
	options *runtime.CreateContainerOptions,
	portMap map[int32]*corev1apply.ServicePortApplyConfiguration,
) error {
	for portStr := range options.ExposedPorts {
		portNum, err := parsePortString(portStr)
		if err != nil {
			return err
		}

		if err := validatePortNumber(portNum); err != nil {
			return err
		}

		//nolint:gosec // G115: Safe int->int32 conversion, range is checked in validatePortNumber
		portInt32 := int32(portNum)
		// Add port if not already in the map
		if _, exists := portMap[portInt32]; !exists {
			portMap[portInt32] = createServicePortConfig(portNum)
		}
	}
	return nil
}

// createServicePorts creates service port configurations from container options
func createServicePorts(options *runtime.CreateContainerOptions) ([]*corev1apply.ServicePortApplyConfiguration, error) {
	if options == nil {
		return nil, nil
	}

	// Use a map to track which ports have been added
	portMap := make(map[int32]*corev1apply.ServicePortApplyConfiguration)

	// Process exposed ports
	if err := processExposedPorts(options, portMap); err != nil {
		return nil, err
	}

	// Process port bindings
	for portStr, bindings := range options.PortBindings {
		portNum, err := parsePortString(portStr)
		if err != nil {
			return nil, err
		}

		if err := validatePortNumber(portNum); err != nil {
			return nil, err
		}

		//nolint:gosec // G115: Safe int->int32 conversion, range is checked in validatePortNumber
		portInt32 := int32(portNum)
		servicePort := portMap[portInt32]
		if servicePort == nil {
			// Create new service port if not in map
			servicePort = createServicePortConfig(portNum)
		}

		// If there are bindings with a host port, use the first one as node port
		if len(bindings) > 0 && bindings[0].HostPort != "" {
			hostPort, err := strconv.Atoi(bindings[0].HostPort)
			if err == nil && hostPort >= 30000 && hostPort <= 32767 {
				// NodePort must be in range 30000-32767
				// Safe to convert to int32 since we've verified the range (30000-32767)
				// which is well within int32 range (-2,147,483,648 to 2,147,483,647)
				//nolint:gosec // G109: Safe int->int32 conversion, range is checked above
				nodePort := int32(hostPort)
				servicePort = servicePort.WithNodePort(nodePort)
			}
		}

		//nolint:gosec // G115: Safe int->int32 conversion, range is checked above
		portMap[int32(portNum)] = servicePort
	}

	// Convert map to slice
	var servicePorts []*corev1apply.ServicePortApplyConfiguration
	for _, port := range portMap {
		servicePorts = append(servicePorts, port)
	}

	return servicePorts, nil
}

// createHeadlessService creates a headless Kubernetes service for the StatefulSet
func (c *Client) createHeadlessService(
	ctx context.Context,
	containerName string,
	namespace string,
	labels map[string]string,
	options *runtime.CreateContainerOptions,
) error {
	// Create service ports from the container ports
	servicePorts, err := createServicePorts(options)
	if err != nil {
		return err
	}

	// If no ports were configured, don't create a service
	if len(servicePorts) == 0 {
		logger.Log.Infof("No ports configured for SSE transport, skipping service creation")
		return nil
	}

	// Create service type based on whether we have node ports
	serviceType := corev1.ServiceTypeClusterIP
	for _, sp := range servicePorts {
		if sp.NodePort != nil {
			serviceType = corev1.ServiceTypeNodePort
			break
		}
	}

	// Create the service apply configuration
	serviceApply := corev1apply.Service(containerName, namespace).
		WithLabels(labels).
		WithSpec(corev1apply.ServiceSpec().
			WithSelector(map[string]string{
				"app": containerName,
			}).
			WithPorts(servicePorts...).
			WithType(serviceType).
			WithClusterIP("None")) // "None" makes it a headless service

	// Apply the service using server-side apply
	fieldManager := "toolhive-container-manager"
	_, err = c.client.CoreV1().Services(namespace).
		Apply(ctx, serviceApply, metav1.ApplyOptions{
			FieldManager: fieldManager,
			Force:        true,
		})

	if err != nil {
		return fmt.Errorf("failed to apply service: %v", err)
	}

	logger.Log.Infof("Created headless service %s for SSE transport", containerName)
	return nil
}

// extractPortMappingsFromPod extracts port mappings from a pod's containers
func extractPortMappingsFromPod(pod *corev1.Pod) []runtime.PortMapping {
	ports := make([]runtime.PortMapping, 0)

	for _, container := range pod.Spec.Containers {
		for _, port := range container.Ports {
			ports = append(ports, runtime.PortMapping{
				ContainerPort: int(port.ContainerPort),
				HostPort:      int(port.HostPort),
				Protocol:      string(port.Protocol),
			})
		}
	}

	return ports
}

// extractPortMappingsFromService extracts port mappings from a Kubernetes service
func extractPortMappingsFromService(service *corev1.Service, existingPorts []runtime.PortMapping) []runtime.PortMapping {
	// Create a map of existing ports for easy lookup and updating
	portMap := make(map[int]runtime.PortMapping)
	for _, p := range existingPorts {
		portMap[p.ContainerPort] = p
	}

	// Update or add ports from the service
	for _, port := range service.Spec.Ports {
		containerPort := int(port.Port)
		hostPort := 0
		if port.NodePort > 0 {
			hostPort = int(port.NodePort)
		}

		// Update existing port or add new one
		portMap[containerPort] = runtime.PortMapping{
			ContainerPort: containerPort,
			HostPort:      hostPort,
			Protocol:      string(port.Protocol),
		}
	}

	// Convert map back to slice
	result := make([]runtime.PortMapping, 0, len(portMap))
	for _, p := range portMap {
		result = append(result, p)
	}

	return result
}
