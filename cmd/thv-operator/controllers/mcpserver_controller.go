// Package controllers contains the reconciliation logic for the MCPServer custom resource.
// It handles the creation, update, and deletion of MCP servers in Kubernetes.
package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"reflect"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	mcpv1alpha1 "github.com/stacklok/toolhive/cmd/thv-operator/api/v1alpha1"
	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
)

// MCPServerReconciler reconciles a MCPServer object
type MCPServerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// defaultRBACRules are the default RBAC rules that the
// ToolHive ProxyRunner and/or MCP server needs to have in order to run.
var defaultRBACRules = []rbacv1.PolicyRule{
	{
		APIGroups: []string{"apps"},
		Resources: []string{"statefulsets"},
		Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete", "apply"},
	},
	{
		APIGroups: []string{""},
		Resources: []string{"services"},
		Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete", "apply"},
	},
	{
		APIGroups: []string{""},
		Resources: []string{"pods"},
		Verbs:     []string{"get", "list", "watch"},
	},
	{
		APIGroups: []string{""},
		Resources: []string{"pods/log"},
		Verbs:     []string{"get"},
	},
	{
		APIGroups: []string{""},
		Resources: []string{"pods/attach"},
		Verbs:     []string{"create", "get"},
	},
}

var ctxLogger = log.FromContext(context.Background())

// mcpContainerName is the name of the mcp container used in pod templates
const mcpContainerName = "mcp"

// Authorization ConfigMap label constants
const (
	// authzLabelKey is the label key for authorization configuration type
	authzLabelKey = "toolhive.stacklok.io/authz"

	// authzLabelValueInline is the label value for inline authorization configuration
	authzLabelValueInline = "inline"
)

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
//nolint:gocyclo
func (r *MCPServerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctxLogger = log.FromContext(ctx)

	// Fetch the MCPServer instance
	mcpServer := &mcpv1alpha1.MCPServer{}
	err := r.Get(ctx, req.NamespacedName, mcpServer)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Return and don't requeue
			ctxLogger.Info("MCPServer resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		ctxLogger.Error(err, "Failed to get MCPServer")
		return ctrl.Result{}, err
	}

	// Check if the MCPServer instance is marked to be deleted
	if mcpServer.GetDeletionTimestamp() != nil {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(mcpServer, "mcpserver.toolhive.stacklok.dev/finalizer") {
			// Run finalization logic. If the finalization logic fails,
			// don't remove the finalizer so that we can retry during the next reconciliation.
			if err := r.finalizeMCPServer(ctx, mcpServer); err != nil {
				return ctrl.Result{}, err
			}

			// Remove the finalizer. Once all finalizers have been removed, the object will be deleted.
			controllerutil.RemoveFinalizer(mcpServer, "mcpserver.toolhive.stacklok.dev/finalizer")
			err := r.Update(ctx, mcpServer)
			if err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer for this CR
	if !controllerutil.ContainsFinalizer(mcpServer, "mcpserver.toolhive.stacklok.dev/finalizer") {
		controllerutil.AddFinalizer(mcpServer, "mcpserver.toolhive.stacklok.dev/finalizer")
		err = r.Update(ctx, mcpServer)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	// Update the MCPServer status with the pod status
	if err := r.updateMCPServerStatus(ctx, mcpServer); err != nil {
		ctxLogger.Error(err, "Failed to update MCPServer status")
		return ctrl.Result{}, err
	}

	// check if the RBAC resources are in place for the MCP server
	if err := r.ensureRBACResources(ctx, mcpServer); err != nil {
		ctxLogger.Error(err, "Failed to ensure RBAC resources")
		return ctrl.Result{}, err
	}

	// Ensure authorization ConfigMap for inline configuration
	if err := r.ensureAuthzConfigMap(ctx, mcpServer); err != nil {
		ctxLogger.Error(err, "Failed to ensure authorization ConfigMap")
		return ctrl.Result{}, err
	}

	// Check if the deployment already exists, if not create a new one
	deployment := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Name: mcpServer.Name, Namespace: mcpServer.Namespace}, deployment)
	if err != nil && errors.IsNotFound(err) {
		// Define a new deployment
		dep := r.deploymentForMCPServer(mcpServer)
		if dep == nil {
			ctxLogger.Error(nil, "Failed to create Deployment object")
			return ctrl.Result{}, fmt.Errorf("failed to create Deployment object")
		}
		ctxLogger.Info("Creating a new Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
		err = r.Create(ctx, dep)
		if err != nil {
			ctxLogger.Error(err, "Failed to create new Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
			return ctrl.Result{}, err
		}
		// Deployment created successfully - return and requeue
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		ctxLogger.Error(err, "Failed to get Deployment")
		return ctrl.Result{}, err
	}

	// Ensure the deployment size is the same as the spec
	if *deployment.Spec.Replicas != 1 {
		deployment.Spec.Replicas = int32Ptr(1)
		err = r.Update(ctx, deployment)
		if err != nil {
			ctxLogger.Error(err, "Failed to update Deployment",
				"Deployment.Namespace", deployment.Namespace,
				"Deployment.Name", deployment.Name)
			return ctrl.Result{}, err
		}
		// Spec updated - return and requeue
		return ctrl.Result{Requeue: true}, nil
	}

	// Check if the Service already exists, if not create a new one
	serviceName := createServiceName(mcpServer.Name)
	service := &corev1.Service{}
	err = r.Get(ctx, types.NamespacedName{Name: serviceName, Namespace: mcpServer.Namespace}, service)
	if err != nil && errors.IsNotFound(err) {
		// Define a new service
		svc := r.serviceForMCPServer(mcpServer)
		if svc == nil {
			ctxLogger.Error(nil, "Failed to create Service object")
			return ctrl.Result{}, fmt.Errorf("failed to create Service object")
		}
		ctxLogger.Info("Creating a new Service", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
		err = r.Create(ctx, svc)
		if err != nil {
			ctxLogger.Error(err, "Failed to create new Service", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
			return ctrl.Result{}, err
		}
		// Service created successfully - return and requeue
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		ctxLogger.Error(err, "Failed to get Service")
		return ctrl.Result{}, err
	}

	// Update the MCPServer status with the service URL
	if mcpServer.Status.URL == "" {
		mcpServer.Status.URL = fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", service.Name, service.Namespace, mcpServer.Spec.Port)
		err = r.Status().Update(ctx, mcpServer)
		if err != nil {
			ctxLogger.Error(err, "Failed to update MCPServer status")
			return ctrl.Result{}, err
		}
	}

	// Check if the deployment spec changed
	if deploymentNeedsUpdate(deployment, mcpServer) {
		// Update the deployment
		newDeployment := r.deploymentForMCPServer(mcpServer)
		deployment.Spec = newDeployment.Spec
		err = r.Update(ctx, deployment)
		if err != nil {
			ctxLogger.Error(err, "Failed to update Deployment",
				"Deployment.Namespace", deployment.Namespace,
				"Deployment.Name", deployment.Name)
			return ctrl.Result{}, err
		}
		// Spec updated - return and requeue
		return ctrl.Result{Requeue: true}, nil
	}

	// Check if the service spec changed
	if serviceNeedsUpdate(service, mcpServer) {
		// Update the service
		newService := r.serviceForMCPServer(mcpServer)
		service.Spec.Ports = newService.Spec.Ports
		err = r.Update(ctx, service)
		if err != nil {
			ctxLogger.Error(err, "Failed to update Service", "Service.Namespace", service.Namespace, "Service.Name", service.Name)
			return ctrl.Result{}, err
		}
		// Spec updated - return and requeue
		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

// ensureRBACResource is a generic helper function to ensure a Kubernetes resource exists and is up to date
func (r *MCPServerReconciler) ensureRBACResource(
	ctx context.Context,
	mcpServer *mcpv1alpha1.MCPServer,
	resourceType string,
	createResource func() client.Object,
) error {
	current := createResource()
	objectKey := types.NamespacedName{Name: current.GetName(), Namespace: current.GetNamespace()}
	err := r.Get(ctx, objectKey, current)

	if errors.IsNotFound(err) {
		return r.createRBACResource(ctx, mcpServer, resourceType, createResource)
	} else if err != nil {
		return fmt.Errorf("failed to get %s: %w", resourceType, err)
	}

	return r.updateRBACResourceIfNeeded(ctx, mcpServer, resourceType, createResource, current)
}

// createRBACResource creates a new RBAC resource
func (r *MCPServerReconciler) createRBACResource(
	ctx context.Context,
	mcpServer *mcpv1alpha1.MCPServer,
	resourceType string,
	createResource func() client.Object,
) error {
	desired := createResource()
	if err := controllerutil.SetControllerReference(mcpServer, desired, r.Scheme); err != nil {
		logger.Error(fmt.Sprintf("Failed to set controller reference for %s", resourceType), err)
		return nil
	}

	ctxLogger.Info(
		fmt.Sprintf("%s does not exist, creating %s", resourceType, resourceType),
		fmt.Sprintf("%s.Name", resourceType),
		desired.GetName(),
	)
	if err := r.Create(ctx, desired); err != nil {
		return fmt.Errorf("failed to create %s: %w", resourceType, err)
	}
	ctxLogger.Info(fmt.Sprintf("%s created", resourceType), fmt.Sprintf("%s.Name", resourceType), desired.GetName())
	return nil
}

// updateRBACResourceIfNeeded updates an RBAC resource if changes are detected
func (r *MCPServerReconciler) updateRBACResourceIfNeeded(
	ctx context.Context,
	mcpServer *mcpv1alpha1.MCPServer,
	resourceType string,
	createResource func() client.Object,
	current client.Object,
) error {
	desired := createResource()
	if err := controllerutil.SetControllerReference(mcpServer, desired, r.Scheme); err != nil {
		logger.Error(fmt.Sprintf("Failed to set controller reference for %s", resourceType), err)
		return nil
	}

	if !reflect.DeepEqual(current, desired) {
		ctxLogger.Info(
			fmt.Sprintf("%s exists, updating %s", resourceType, resourceType),
			fmt.Sprintf("%s.Name", resourceType),
			desired.GetName(),
		)
		if err := r.Update(ctx, desired); err != nil {
			return fmt.Errorf("failed to update %s: %w", resourceType, err)
		}
		ctxLogger.Info(fmt.Sprintf("%s updated", resourceType), fmt.Sprintf("%s.Name", resourceType), desired.GetName())
	}
	return nil
}

// ensureRBACResources ensures that the RBAC resources are in place for the MCP server
func (r *MCPServerReconciler) ensureRBACResources(ctx context.Context, mcpServer *mcpv1alpha1.MCPServer) error {
	proxyRunnerNameForRBAC := fmt.Sprintf("%s-proxy-runner", mcpServer.Name)

	// Ensure Role
	if err := r.ensureRBACResource(ctx, mcpServer, "Role", func() client.Object {
		return &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      proxyRunnerNameForRBAC,
				Namespace: mcpServer.Namespace,
			},
			Rules: defaultRBACRules,
		}
	}); err != nil {
		return err
	}

	// Ensure ServiceAccount
	if err := r.ensureRBACResource(ctx, mcpServer, "ServiceAccount", func() client.Object {
		return &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      proxyRunnerNameForRBAC,
				Namespace: mcpServer.Namespace,
			},
		}
	}); err != nil {
		return err
	}

	// Ensure RoleBinding
	return r.ensureRBACResource(ctx, mcpServer, "RoleBinding", func() client.Object {
		return &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      proxyRunnerNameForRBAC,
				Namespace: mcpServer.Namespace,
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "Role",
				Name:     proxyRunnerNameForRBAC,
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      proxyRunnerNameForRBAC,
					Namespace: mcpServer.Namespace,
				},
			},
		}
	})
}

// deploymentForMCPServer returns a MCPServer Deployment object
//
//nolint:gocyclo
func (r *MCPServerReconciler) deploymentForMCPServer(m *mcpv1alpha1.MCPServer) *appsv1.Deployment {
	ls := labelsForMCPServer(m.Name)
	replicas := int32(1)

	// Prepare container args
	args := []string{"run", "--foreground=true"}
	args = append(args, fmt.Sprintf("--port=%d", m.Spec.Port))
	args = append(args, fmt.Sprintf("--name=%s", m.Name))
	args = append(args, fmt.Sprintf("--transport=%s", m.Spec.Transport))
	args = append(args, fmt.Sprintf("--host=%s", getProxyHost()))

	if m.Spec.TargetPort != 0 {
		args = append(args, fmt.Sprintf("--target-port=%d", m.Spec.TargetPort))
	}

	// Generate pod template patch for secrets and merge with user-provided patch
	finalPodTemplateSpec := generateAndMergePodTemplateSpecs(m.Spec.Secrets, m.Spec.PodTemplateSpec)

	// Add pod template patch if we have one
	if finalPodTemplateSpec != nil {
		podTemplatePatch, err := json.Marshal(finalPodTemplateSpec)
		if err != nil {
			logger.Errorf("Failed to marshal pod template spec: %v", err)
		} else {
			args = append(args, fmt.Sprintf("--k8s-pod-patch=%s", string(podTemplatePatch)))
		}
	}

	// Add permission profile args
	if m.Spec.PermissionProfile != nil {
		switch m.Spec.PermissionProfile.Type {
		case mcpv1alpha1.PermissionProfileTypeBuiltin:
			args = append(args, fmt.Sprintf("--permission-profile=%s", m.Spec.PermissionProfile.Name))
		case mcpv1alpha1.PermissionProfileTypeConfigMap:
			args = append(args, fmt.Sprintf("--permission-profile-path=/etc/toolhive/profiles/%s", m.Spec.PermissionProfile.Key))
		}
	}

	// Add OIDC configuration args
	if m.Spec.OIDCConfig != nil {
		// Create a context with timeout for OIDC configuration operations
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		oidcArgs := r.generateOIDCArgs(ctx, m)
		args = append(args, oidcArgs...)
	}

	// Add authorization configuration args
	if m.Spec.AuthzConfig != nil {
		authzArgs := r.generateAuthzArgs(m)
		args = append(args, authzArgs...)
	}

	// Add environment variables as --env flags for the MCP server
	for _, e := range m.Spec.Env {
		args = append(args, fmt.Sprintf("--env=%s=%s", e.Name, e.Value))
	}

	// Add the image
	args = append(args, m.Spec.Image)

	// Add additional args
	if len(m.Spec.Args) > 0 {
		args = append(args, "--")
		args = append(args, m.Spec.Args...)
	}

	// Prepare container env vars for the proxy container
	env := []corev1.EnvVar{}

	// Prepare container volume mounts
	volumeMounts := []corev1.VolumeMount{}
	volumes := []corev1.Volume{}

	// Add volume mounts for user-defined volumes
	for _, v := range m.Spec.Volumes {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      v.Name,
			MountPath: v.MountPath,
			ReadOnly:  v.ReadOnly,
		})

		volumes = append(volumes, corev1.Volume{
			Name: v.Name,
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: v.HostPath,
				},
			},
		})
	}

	// Add volume mount for permission profile if using configmap
	if m.Spec.PermissionProfile != nil && m.Spec.PermissionProfile.Type == mcpv1alpha1.PermissionProfileTypeConfigMap {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "permission-profile",
			MountPath: "/etc/toolhive/profiles",
			ReadOnly:  true,
		})

		volumes = append(volumes, corev1.Volume{
			Name: "permission-profile",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: m.Spec.PermissionProfile.Name,
					},
				},
			},
		})
	}

	// Add volume mounts for authorization configuration
	authzVolumeMount, authzVolume := r.generateAuthzVolumeConfig(m)
	if authzVolumeMount != nil {
		volumeMounts = append(volumeMounts, *authzVolumeMount)
		volumes = append(volumes, *authzVolume)
	}

	// Prepare container resources
	resources := corev1.ResourceRequirements{}
	if m.Spec.Resources.Limits.CPU != "" || m.Spec.Resources.Limits.Memory != "" {
		resources.Limits = corev1.ResourceList{}
		if m.Spec.Resources.Limits.CPU != "" {
			resources.Limits[corev1.ResourceCPU] = resource.MustParse(m.Spec.Resources.Limits.CPU)
		}
		if m.Spec.Resources.Limits.Memory != "" {
			resources.Limits[corev1.ResourceMemory] = resource.MustParse(m.Spec.Resources.Limits.Memory)
		}
	}
	if m.Spec.Resources.Requests.CPU != "" || m.Spec.Resources.Requests.Memory != "" {
		resources.Requests = corev1.ResourceList{}
		if m.Spec.Resources.Requests.CPU != "" {
			resources.Requests[corev1.ResourceCPU] = resource.MustParse(m.Spec.Resources.Requests.CPU)
		}
		if m.Spec.Resources.Requests.Memory != "" {
			resources.Requests[corev1.ResourceMemory] = resource.MustParse(m.Spec.Resources.Requests.Memory)
		}
	}

	// Prepare deployment metadata with overrides
	deploymentLabels := ls
	deploymentAnnotations := make(map[string]string)

	if m.Spec.ResourceOverrides != nil && m.Spec.ResourceOverrides.ProxyDeployment != nil {
		if m.Spec.ResourceOverrides.ProxyDeployment.Labels != nil {
			deploymentLabels = mergeLabels(ls, m.Spec.ResourceOverrides.ProxyDeployment.Labels)
		}
		if m.Spec.ResourceOverrides.ProxyDeployment.Annotations != nil {
			deploymentAnnotations = mergeAnnotations(make(map[string]string), m.Spec.ResourceOverrides.ProxyDeployment.Annotations)
		}
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        m.Name,
			Namespace:   m.Namespace,
			Labels:      deploymentLabels,
			Annotations: deploymentAnnotations,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls, // Keep original labels for selector
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls, // Keep original labels for pod template
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: fmt.Sprintf("%s-proxy-runner", m.Name),
					Containers: []corev1.Container{{
						Image:        getToolhiveRunnerImage(),
						Name:         "toolhive",
						Args:         args,
						Env:          env,
						VolumeMounts: volumeMounts,
						Resources:    resources,
						Ports: []corev1.ContainerPort{{
							ContainerPort: m.Spec.Port,
							Name:          "http",
							Protocol:      corev1.ProtocolTCP,
						}},
						LivenessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/health",
									Port: intstr.FromString("http"),
								},
							},
							InitialDelaySeconds: 30,
							PeriodSeconds:       10,
							TimeoutSeconds:      5,
							FailureThreshold:    3,
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/health",
									Port: intstr.FromString("http"),
								},
							},
							InitialDelaySeconds: 5,
							PeriodSeconds:       5,
							TimeoutSeconds:      3,
							FailureThreshold:    3,
						},
					}},
					Volumes: volumes,
				},
			},
		},
	}

	// Set MCPServer instance as the owner and controller
	if err := controllerutil.SetControllerReference(m, dep, r.Scheme); err != nil {
		logger.Error("Failed to set controller reference for Deployment", err)
		return nil
	}
	return dep
}

func createServiceName(mcpServerName string) string {
	return fmt.Sprintf("mcp-%s-proxy", mcpServerName)
}

// serviceForMCPServer returns a MCPServer Service object
func (r *MCPServerReconciler) serviceForMCPServer(m *mcpv1alpha1.MCPServer) *corev1.Service {
	ls := labelsForMCPServer(m.Name)

	// we want to generate a service name that is unique for the proxy service
	// to avoid conflicts with the headless service
	svcName := createServiceName(m.Name)

	// Prepare service metadata with overrides
	serviceLabels := ls
	serviceAnnotations := make(map[string]string)

	if m.Spec.ResourceOverrides != nil && m.Spec.ResourceOverrides.ProxyService != nil {
		if m.Spec.ResourceOverrides.ProxyService.Labels != nil {
			serviceLabels = mergeLabels(ls, m.Spec.ResourceOverrides.ProxyService.Labels)
		}
		if m.Spec.ResourceOverrides.ProxyService.Annotations != nil {
			serviceAnnotations = mergeAnnotations(make(map[string]string), m.Spec.ResourceOverrides.ProxyService.Annotations)
		}
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        svcName,
			Namespace:   m.Namespace,
			Labels:      serviceLabels,
			Annotations: serviceAnnotations,
		},
		Spec: corev1.ServiceSpec{
			Selector: ls, // Keep original labels for selector
			Ports: []corev1.ServicePort{{
				Port:       m.Spec.Port,
				TargetPort: intstr.FromInt(int(m.Spec.Port)),
				Protocol:   corev1.ProtocolTCP,
				Name:       "http",
			}},
		},
	}

	// Set MCPServer instance as the owner and controller
	if err := controllerutil.SetControllerReference(m, svc, r.Scheme); err != nil {
		logger.Error("Failed to set controller reference for Service", err)
		return nil
	}
	return svc
}

// updateMCPServerStatus updates the status of the MCPServer
func (r *MCPServerReconciler) updateMCPServerStatus(ctx context.Context, m *mcpv1alpha1.MCPServer) error {
	// List the pods for this MCPServer's deployment
	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(m.Namespace),
		client.MatchingLabels(labelsForMCPServer(m.Name)),
	}
	if err := r.List(ctx, podList, listOpts...); err != nil {
		return err
	}

	// Update the status based on the pod status
	var running, pending, failed int
	for _, pod := range podList.Items {
		switch pod.Status.Phase {
		case corev1.PodRunning:
			running++
		case corev1.PodPending:
			pending++
		case corev1.PodFailed:
			failed++
		case corev1.PodSucceeded:
			// Treat succeeded pods as running for status purposes
			running++
		case corev1.PodUnknown:
			// Treat unknown pods as pending for status purposes
			pending++
		}
	}

	// Update the status
	if running > 0 {
		m.Status.Phase = mcpv1alpha1.MCPServerPhaseRunning
		m.Status.Message = "MCP server is running"
	} else if pending > 0 {
		m.Status.Phase = mcpv1alpha1.MCPServerPhasePending
		m.Status.Message = "MCP server is starting"
	} else if failed > 0 {
		m.Status.Phase = mcpv1alpha1.MCPServerPhaseFailed
		m.Status.Message = "MCP server failed to start"
	} else {
		m.Status.Phase = mcpv1alpha1.MCPServerPhasePending
		m.Status.Message = "No pods found for MCP server"
	}

	// Update the status
	return r.Status().Update(ctx, m)
}

// finalizeMCPServer performs the finalizer logic for the MCPServer
func (r *MCPServerReconciler) finalizeMCPServer(ctx context.Context, m *mcpv1alpha1.MCPServer) error {
	// Update the MCPServer status
	m.Status.Phase = mcpv1alpha1.MCPServerPhaseTerminating
	m.Status.Message = "MCP server is being terminated"
	if err := r.Status().Update(ctx, m); err != nil {
		return err
	}

	// Step 2: Attempt to delete associated StatefulSet by name
	sts := &appsv1.StatefulSet{}
	err := r.Get(ctx, types.NamespacedName{Name: m.Name, Namespace: m.Namespace}, sts)
	if err == nil {
		// StatefulSet found, delete it
		if delErr := r.Delete(ctx, sts); delErr != nil && !errors.IsNotFound(delErr) {
			return fmt.Errorf("failed to delete StatefulSet %s: %w", m.Name, delErr)
		}
	} else if !errors.IsNotFound(err) {
		// Unexpected error (not just "not found")
		return fmt.Errorf("failed to get StatefulSet %s: %w", m.Name, err)
	}

	// Step 3: Attempt to delete associated service by name
	svc := &corev1.Service{}
	serviceName := fmt.Sprintf("mcp-%s-headless", m.Name)
	err = r.Get(ctx, types.NamespacedName{Name: serviceName, Namespace: m.Namespace}, svc)
	if err == nil {
		if delErr := r.Delete(ctx, svc); delErr != nil && !errors.IsNotFound(delErr) {
			return fmt.Errorf("failed to delete Service %s: %w", serviceName, delErr)
		}
	} else if !errors.IsNotFound(err) {
		return fmt.Errorf("failed to check Service %s: %w", serviceName, err)
	}
	// The owner references will automatically delete the deployment and service
	// when the MCPServer is deleted, so we don't need to do anything here.
	return nil
}

// deploymentNeedsUpdate checks if the deployment needs to be updated
//
//nolint:gocyclo
func deploymentNeedsUpdate(deployment *appsv1.Deployment, mcpServer *mcpv1alpha1.MCPServer) bool {
	// Check if the container args have changed
	if len(deployment.Spec.Template.Spec.Containers) > 0 {
		container := deployment.Spec.Template.Spec.Containers[0]

		// Check if the toolhive runner image has changed
		if container.Image != getToolhiveRunnerImage() {
			return true
		}

		// Check if the args contain the correct image
		imageArg := mcpServer.Spec.Image
		found := false
		for _, arg := range container.Args {
			if arg == imageArg {
				found = true
				break
			}
		}
		if !found {
			return true
		}

		// Check if the port has changed
		portArg := fmt.Sprintf("--port=%d", mcpServer.Spec.Port)
		found = false
		for _, arg := range container.Args {
			if arg == portArg {
				found = true
				break
			}
		}
		if !found {
			return true
		}

		// Check if the transport has changed
		transportArg := fmt.Sprintf("--transport=%s", mcpServer.Spec.Transport)
		found = false
		for _, arg := range container.Args {
			if arg == transportArg {
				found = true
				break
			}
		}
		if !found {
			return true
		}

		// Check if the pod template spec has changed

		// TODO: Add more comprehensive checks for PodTemplateSpec changes beyond just the args
		// This would involve comparing the actual pod template spec fields with what would be
		// generated by the operator, rather than just checking the command-line arguments.
		if mcpServer.Spec.PodTemplateSpec != nil {
			podTemplatePatch, err := json.Marshal(mcpServer.Spec.PodTemplateSpec)
			if err == nil {
				podTemplatePatchArg := fmt.Sprintf("--k8s-pod-patch=%s", string(podTemplatePatch))
				found := false
				for _, arg := range container.Args {
					if arg == podTemplatePatchArg {
						found = true
						break
					}
				}
				if !found {
					return true
				}
			}
		}

		// Check if the container port has changed
		if len(container.Ports) > 0 && container.Ports[0].ContainerPort != mcpServer.Spec.Port {
			return true
		}

		// Check if the environment variables have changed (now passed as --env flags)
		for _, envVar := range mcpServer.Spec.Env {
			envArg := fmt.Sprintf("--env=%s=%s", envVar.Name, envVar.Value)
			found := false
			for _, arg := range container.Args {
				if arg == envArg {
					found = true
					break
				}
			}
			if !found {
				return true
			}
		}

		// Check if the pod template spec has changed (including secrets)
		expectedPodTemplateSpec := generateAndMergePodTemplateSpecs(mcpServer.Spec.Secrets, mcpServer.Spec.PodTemplateSpec)

		// Find the current pod template patch in the container args
		var currentPodTemplatePatch string
		for _, arg := range container.Args {
			if strings.HasPrefix(arg, "--k8s-pod-patch=") {
				currentPodTemplatePatch = arg[16:] // Remove "--k8s-pod-patch=" prefix
				break
			}
		}

		// Compare expected vs current pod template spec
		if expectedPodTemplateSpec != nil {
			expectedPatch, err := json.Marshal(expectedPodTemplateSpec)
			if err != nil {
				logger.Errorf("Failed to marshal expected pod template spec: %v", err)
				return true // Assume change if we can't marshal
			}
			expectedPatchString := string(expectedPatch)

			if currentPodTemplatePatch != expectedPatchString {
				return true
			}
		} else if currentPodTemplatePatch != "" {
			// Expected no patch but current has one
			return true
		}

		// Check if the resource requirements have changed
		if !reflect.DeepEqual(container.Resources, resourceRequirementsForMCPServer(mcpServer)) {
			return true
		}

		// Check if the targetPort has changed
		if mcpServer.Spec.TargetPort != 0 {
			targetPortArg := fmt.Sprintf("--target-port=%d", mcpServer.Spec.TargetPort)
			found := false
			for _, arg := range container.Args {
				if arg == targetPortArg {
					found = true
					break
				}
			}
			if !found {
				return true
			}
		} else {
			for _, arg := range container.Args {
				if strings.HasPrefix(arg, "--target-port=") {
					return true
				}
			}
		}

	}

	// Check if the service account name has changed
	if deployment.Spec.Template.Spec.ServiceAccountName != "toolhive" {
		return true
	}

	// Check if the deployment metadata (labels/annotations) have changed due to resource overrides
	expectedLabels := labelsForMCPServer(mcpServer.Name)
	expectedAnnotations := make(map[string]string)

	if mcpServer.Spec.ResourceOverrides != nil && mcpServer.Spec.ResourceOverrides.ProxyDeployment != nil {
		if mcpServer.Spec.ResourceOverrides.ProxyDeployment.Labels != nil {
			expectedLabels = mergeLabels(expectedLabels, mcpServer.Spec.ResourceOverrides.ProxyDeployment.Labels)
		}
		if mcpServer.Spec.ResourceOverrides.ProxyDeployment.Annotations != nil {
			expectedAnnotations = mergeAnnotations(make(map[string]string), mcpServer.Spec.ResourceOverrides.ProxyDeployment.Annotations)
		}
	}

	if !maps.Equal(deployment.Labels, expectedLabels) {
		return true
	}

	if !maps.Equal(deployment.Annotations, expectedAnnotations) {
		return true
	}

	return false
}

// serviceNeedsUpdate checks if the service needs to be updated
func serviceNeedsUpdate(service *corev1.Service, mcpServer *mcpv1alpha1.MCPServer) bool {
	// Check if the service port has changed
	if len(service.Spec.Ports) > 0 && service.Spec.Ports[0].Port != mcpServer.Spec.Port {
		return true
	}

	// Check if the service metadata (labels/annotations) have changed due to resource overrides
	expectedLabels := labelsForMCPServer(mcpServer.Name)
	expectedAnnotations := make(map[string]string)

	if mcpServer.Spec.ResourceOverrides != nil && mcpServer.Spec.ResourceOverrides.ProxyService != nil {
		if mcpServer.Spec.ResourceOverrides.ProxyService.Labels != nil {
			expectedLabels = mergeLabels(expectedLabels, mcpServer.Spec.ResourceOverrides.ProxyService.Labels)
		}
		if mcpServer.Spec.ResourceOverrides.ProxyService.Annotations != nil {
			expectedAnnotations = mergeAnnotations(make(map[string]string), mcpServer.Spec.ResourceOverrides.ProxyService.Annotations)
		}
	}

	if !maps.Equal(service.Labels, expectedLabels) {
		return true
	}

	if !maps.Equal(service.Annotations, expectedAnnotations) {
		return true
	}

	return false
}

// resourceRequirementsForMCPServer returns the resource requirements for the MCPServer
func resourceRequirementsForMCPServer(m *mcpv1alpha1.MCPServer) corev1.ResourceRequirements {
	resources := corev1.ResourceRequirements{}
	if m.Spec.Resources.Limits.CPU != "" || m.Spec.Resources.Limits.Memory != "" {
		resources.Limits = corev1.ResourceList{}
		if m.Spec.Resources.Limits.CPU != "" {
			resources.Limits[corev1.ResourceCPU] = resource.MustParse(m.Spec.Resources.Limits.CPU)
		}
		if m.Spec.Resources.Limits.Memory != "" {
			resources.Limits[corev1.ResourceMemory] = resource.MustParse(m.Spec.Resources.Limits.Memory)
		}
	}
	if m.Spec.Resources.Requests.CPU != "" || m.Spec.Resources.Requests.Memory != "" {
		resources.Requests = corev1.ResourceList{}
		if m.Spec.Resources.Requests.CPU != "" {
			resources.Requests[corev1.ResourceCPU] = resource.MustParse(m.Spec.Resources.Requests.CPU)
		}
		if m.Spec.Resources.Requests.Memory != "" {
			resources.Requests[corev1.ResourceMemory] = resource.MustParse(m.Spec.Resources.Requests.Memory)
		}
	}
	return resources
}

// labelsForMCPServer returns the labels for selecting the resources
// belonging to the given MCPServer CR name.
func labelsForMCPServer(name string) map[string]string {
	return map[string]string{
		"app":                        "mcpserver",
		"app.kubernetes.io/name":     "mcpserver",
		"app.kubernetes.io/instance": name,
		"toolhive":                   "true",
		"toolhive-name":              name,
	}
}

// labelsForInlineAuthzConfig returns the labels for inline authorization ConfigMaps
// belonging to the given MCPServer CR name.
func labelsForInlineAuthzConfig(name string) map[string]string {
	labels := labelsForMCPServer(name)
	labels[authzLabelKey] = authzLabelValueInline
	return labels
}

// generateAuthzVolumeConfig generates volume mount and volume configuration for authorization policies
// Returns nil for both if no authorization configuration is present
func (*MCPServerReconciler) generateAuthzVolumeConfig(m *mcpv1alpha1.MCPServer) (*corev1.VolumeMount, *corev1.Volume) {
	if m.Spec.AuthzConfig == nil {
		return nil, nil
	}

	switch m.Spec.AuthzConfig.Type {
	case mcpv1alpha1.AuthzConfigTypeConfigMap:
		if m.Spec.AuthzConfig.ConfigMap == nil {
			return nil, nil
		}

		volumeMount := &corev1.VolumeMount{
			Name:      "authz-config",
			MountPath: "/etc/toolhive/authz",
			ReadOnly:  true,
		}

		volume := &corev1.Volume{
			Name: "authz-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: m.Spec.AuthzConfig.ConfigMap.Name,
					},
					Items: []corev1.KeyToPath{
						{
							Key: func() string {
								if m.Spec.AuthzConfig.ConfigMap.Key != "" {
									return m.Spec.AuthzConfig.ConfigMap.Key
								}
								return "authz.json"
							}(),
							Path: "authz.json",
						},
					},
				},
			},
		}

		return volumeMount, volume

	case mcpv1alpha1.AuthzConfigTypeInline:
		if m.Spec.AuthzConfig.Inline == nil {
			return nil, nil
		}

		volumeMount := &corev1.VolumeMount{
			Name:      "authz-config",
			MountPath: "/etc/toolhive/authz",
			ReadOnly:  true,
		}

		volume := &corev1.Volume{
			Name: "authz-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: fmt.Sprintf("%s-authz-inline", m.Name),
					},
					Items: []corev1.KeyToPath{
						{
							Key:  "authz.json",
							Path: "authz.json",
						},
					},
				},
			},
		}

		return volumeMount, volume

	default:
		return nil, nil
	}
}

// mergeStringMaps merges override map with default map, with default map taking precedence
// This ensures that operator-required metadata is preserved for proper functionality
func mergeStringMaps(defaultMap, overrideMap map[string]string) map[string]string {
	result := maps.Clone(overrideMap)
	maps.Copy(result, defaultMap) // default map takes precedence
	return result
}

// mergeLabels merges override labels with default labels, with default labels taking precedence
func mergeLabels(defaultLabels, overrideLabels map[string]string) map[string]string {
	return mergeStringMaps(defaultLabels, overrideLabels)
}

// mergeAnnotations merges override annotations with default annotations, with default annotations taking precedence
func mergeAnnotations(defaultAnnotations, overrideAnnotations map[string]string) map[string]string {
	return mergeStringMaps(defaultAnnotations, overrideAnnotations)
}

// getProxyHost returns the host to bind the proxy to
func getProxyHost() string {
	host := os.Getenv("TOOLHIVE_PROXY_HOST")
	if host == "" {
		host = "0.0.0.0"
	}
	return host
}

// getToolhiveRunnerImage returns the image to use for the toolhive runner container
func getToolhiveRunnerImage() string {
	// Get the image from the environment variable or use a default
	image := os.Getenv("TOOLHIVE_RUNNER_IMAGE")
	if image == "" {
		// Default to the published image
		image = "ghcr.io/stacklok/toolhive:latest"
	}
	return image
}

// generateOIDCArgs generates OIDC command-line arguments based on the OIDC configuration
func (r *MCPServerReconciler) generateOIDCArgs(ctx context.Context, m *mcpv1alpha1.MCPServer) []string {
	var args []string

	if m.Spec.OIDCConfig == nil {
		return args
	}

	switch m.Spec.OIDCConfig.Type {
	case mcpv1alpha1.OIDCConfigTypeKubernetes:
		args = append(args, r.generateKubernetesOIDCArgs(m)...)
	case mcpv1alpha1.OIDCConfigTypeConfigMap:
		args = append(args, r.generateConfigMapOIDCArgs(ctx, m)...)
	case mcpv1alpha1.OIDCConfigTypeInline:
		args = append(args, r.generateInlineOIDCArgs(m)...)
	}

	return args
}

// generateKubernetesOIDCArgs generates OIDC args for Kubernetes service account token validation
func (*MCPServerReconciler) generateKubernetesOIDCArgs(m *mcpv1alpha1.MCPServer) []string {
	var args []string
	config := m.Spec.OIDCConfig.Kubernetes

	// Set defaults if config is nil
	if config == nil {
		config = &mcpv1alpha1.KubernetesOIDCConfig{}
	}

	// Issuer (default: https://kubernetes.default.svc)
	issuer := config.Issuer
	if issuer == "" {
		issuer = "https://kubernetes.default.svc"
	}
	args = append(args, fmt.Sprintf("--oidc-issuer=%s", issuer))

	// Audience (default: toolhive)
	audience := config.Audience
	if audience == "" {
		audience = "toolhive"
	}
	args = append(args, fmt.Sprintf("--oidc-audience=%s", audience))

	// JWKS URL (default: https://kubernetes.default.svc/openid/v1/jwks)
	jwksURL := config.JWKSURL
	if jwksURL == "" {
		jwksURL = "https://kubernetes.default.svc/openid/v1/jwks"
	}
	args = append(args, fmt.Sprintf("--oidc-jwks-url=%s", jwksURL))

	return args
}

// generateConfigMapOIDCArgs generates OIDC args for ConfigMap-based configuration
func (r *MCPServerReconciler) generateConfigMapOIDCArgs(ctx context.Context, m *mcpv1alpha1.MCPServer) []string {
	var args []string
	config := m.Spec.OIDCConfig.ConfigMap

	if config == nil {
		return args
	}

	// Read the ConfigMap and extract OIDC configuration from documented keys
	configMap := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      config.Name,
		Namespace: m.Namespace,
	}, configMap)
	if err != nil {
		logger.Errorf("Failed to get ConfigMap %s: %v", config.Name, err)
		return args
	}

	// Extract OIDC configuration from well-known keys
	if issuer, exists := configMap.Data["issuer"]; exists && issuer != "" {
		args = append(args, fmt.Sprintf("--oidc-issuer=%s", issuer))
	}
	if audience, exists := configMap.Data["audience"]; exists && audience != "" {
		args = append(args, fmt.Sprintf("--oidc-audience=%s", audience))
	}
	if jwksURL, exists := configMap.Data["jwksUrl"]; exists && jwksURL != "" {
		args = append(args, fmt.Sprintf("--oidc-jwks-url=%s", jwksURL))
	}
	if clientID, exists := configMap.Data["clientId"]; exists && clientID != "" {
		args = append(args, fmt.Sprintf("--oidc-client-id=%s", clientID))
	}

	return args
}

// generateInlineOIDCArgs generates OIDC args for inline configuration
func (*MCPServerReconciler) generateInlineOIDCArgs(m *mcpv1alpha1.MCPServer) []string {
	var args []string
	config := m.Spec.OIDCConfig.Inline

	if config == nil {
		return args
	}

	// Issuer (required)
	if config.Issuer != "" {
		args = append(args, fmt.Sprintf("--oidc-issuer=%s", config.Issuer))
	}

	// Audience (optional)
	if config.Audience != "" {
		args = append(args, fmt.Sprintf("--oidc-audience=%s", config.Audience))
	}

	// JWKS URL (optional)
	if config.JWKSURL != "" {
		args = append(args, fmt.Sprintf("--oidc-jwks-url=%s", config.JWKSURL))
	}

	return args
}

// generateAuthzArgs generates authorization command-line arguments based on the configuration type
func (*MCPServerReconciler) generateAuthzArgs(m *mcpv1alpha1.MCPServer) []string {
	var args []string

	if m.Spec.AuthzConfig == nil {
		return args
	}

	// Validate that the configuration is properly set based on type
	switch m.Spec.AuthzConfig.Type {
	case mcpv1alpha1.AuthzConfigTypeConfigMap:
		if m.Spec.AuthzConfig.ConfigMap == nil {
			return args
		}
	case mcpv1alpha1.AuthzConfigTypeInline:
		if m.Spec.AuthzConfig.Inline == nil {
			return args
		}
	default:
		return args
	}

	// Both ConfigMap and inline configurations use the same mounted path
	authzConfigPath := "/etc/toolhive/authz/authz.json"
	args = append(args, fmt.Sprintf("--authz-config=%s", authzConfigPath))

	return args
}

// ensureAuthzConfigMap ensures the authorization ConfigMap exists for inline configuration
func (r *MCPServerReconciler) ensureAuthzConfigMap(ctx context.Context, m *mcpv1alpha1.MCPServer) error {
	// Only create ConfigMap for inline authorization configuration
	if m.Spec.AuthzConfig == nil || m.Spec.AuthzConfig.Type != mcpv1alpha1.AuthzConfigTypeInline ||
		m.Spec.AuthzConfig.Inline == nil {
		return nil
	}

	configMapName := fmt.Sprintf("%s-authz-inline", m.Name)

	// Create authorization configuration data
	authzConfigData := map[string]interface{}{
		"version": "1.0",
		"type":    "cedarv1",
		"cedar": map[string]interface{}{
			"policies": m.Spec.AuthzConfig.Inline.Policies,
			"entities_json": func() string {
				if m.Spec.AuthzConfig.Inline.EntitiesJSON != "" {
					return m.Spec.AuthzConfig.Inline.EntitiesJSON
				}
				return "[]"
			}(),
		},
	}

	// Marshal to JSON
	authzConfigJSON, err := json.Marshal(authzConfigData)
	if err != nil {
		return fmt.Errorf("failed to marshal inline authz config: %w", err)
	}

	// Define the ConfigMap
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: m.Namespace,
			Labels:    labelsForInlineAuthzConfig(m.Name),
		},
		Data: map[string]string{
			"authz.json": string(authzConfigJSON),
		},
	}

	// Set the MCPServer as the owner of the ConfigMap
	if err := controllerutil.SetControllerReference(m, configMap, r.Scheme); err != nil {
		return fmt.Errorf("failed to set controller reference for authorization ConfigMap: %w", err)
	}

	// Check if the ConfigMap already exists
	existingConfigMap := &corev1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: m.Namespace}, existingConfigMap)
	if err != nil && errors.IsNotFound(err) {
		// Create the ConfigMap
		ctxLogger.Info("Creating authorization ConfigMap", "ConfigMap.Namespace", configMap.Namespace, "ConfigMap.Name", configMap.Name)
		if err := r.Create(ctx, configMap); err != nil {
			return fmt.Errorf("failed to create authorization ConfigMap: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to get authorization ConfigMap: %w", err)
	} else {
		// ConfigMap exists, check if it needs to be updated
		if !reflect.DeepEqual(existingConfigMap.Data, configMap.Data) {
			ctxLogger.Info("Updating authorization ConfigMap",
				"ConfigMap.Namespace", configMap.Namespace, "ConfigMap.Name", configMap.Name)
			existingConfigMap.Data = configMap.Data
			if err := r.Update(ctx, existingConfigMap); err != nil {
				return fmt.Errorf("failed to update authorization ConfigMap: %w", err)
			}
		}
	}

	return nil
}

// int32Ptr returns a pointer to an int32
func int32Ptr(i int32) *int32 {
	return &i
}

// generateSecretsPodTemplatePatch generates a podTemplateSpec patch for secrets
func generateSecretsPodTemplatePatch(secrets []mcpv1alpha1.SecretRef) *corev1.PodTemplateSpec {
	if len(secrets) == 0 {
		return nil
	}

	envVars := make([]corev1.EnvVar, 0, len(secrets))
	for _, secret := range secrets {
		targetEnv := secret.Key
		if secret.TargetEnvName != "" {
			targetEnv = secret.TargetEnvName
		}

		envVars = append(envVars, corev1.EnvVar{
			Name: targetEnv,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: secret.Name,
					},
					Key: secret.Key,
				},
			},
		})
	}

	return &corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: mcpContainerName,
					Env:  envVars,
				},
			},
		},
	}
}

// mergePodTemplateSpecs merges a secrets patch with a user-provided podTemplateSpec
func mergePodTemplateSpecs(secretsPatch, userPatch *corev1.PodTemplateSpec) *corev1.PodTemplateSpec {
	// If no secrets, return user patch as-is
	if secretsPatch == nil {
		return userPatch
	}

	// If no user patch, return secrets patch
	if userPatch == nil {
		return secretsPatch
	}

	// Start with user patch as base (preserves all user customizations)
	result := userPatch.DeepCopy()

	// Find or create mcp container in result
	mcpIndex := -1
	for i, container := range result.Spec.Containers {
		if container.Name == mcpContainerName {
			mcpIndex = i
			break
		}
	}

	// Get secret env vars from secrets patch
	var secretEnvVars []corev1.EnvVar
	for _, container := range secretsPatch.Spec.Containers {
		if container.Name == mcpContainerName {
			secretEnvVars = container.Env
			break
		}
	}

	if mcpIndex >= 0 {
		// Merge env vars into existing mcp container
		result.Spec.Containers[mcpIndex].Env = append(
			result.Spec.Containers[mcpIndex].Env,
			secretEnvVars...,
		)
	} else {
		// Add new mcp container with just env vars
		result.Spec.Containers = append(result.Spec.Containers, corev1.Container{
			Name: mcpContainerName,
			Env:  secretEnvVars,
		})
	}

	return result
}

// generateAndMergePodTemplateSpecs generates secrets patch and merges with user patch
func generateAndMergePodTemplateSpecs(
	secrets []mcpv1alpha1.SecretRef,
	userPatch *corev1.PodTemplateSpec,
) *corev1.PodTemplateSpec {
	secretsPatch := generateSecretsPodTemplatePatch(secrets)
	return mergePodTemplateSpecs(secretsPatch, userPatch)
}

// SetupWithManager sets up the controller with the Manager.
func (r *MCPServerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mcpv1alpha1.MCPServer{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Complete(r)
}
