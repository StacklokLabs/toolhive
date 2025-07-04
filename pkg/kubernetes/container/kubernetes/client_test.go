package kubernetes

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1apply "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/stacklok/toolhive/pkg/kubernetes/container/runtime"
	"github.com/stacklok/toolhive/pkg/kubernetes/logger"
)

func init() {
	// Initialize the logger for tests
	logger.Initialize()
}

// mockWaitForStatefulSetReady is used to mock the waitForStatefulSetReady function in tests
var mockWaitForStatefulSetReady = func(_ context.Context, _ kubernetes.Interface, _, _ string) error {
	return nil
}

// TestCreateContainerWithPodTemplatePatch tests that the pod template patch is correctly applied
func TestCreateContainerWithPodTemplatePatch(t *testing.T) {
	t.Parallel()
	// Test cases will create their own clientset

	// Test cases
	testCases := []struct {
		name                             string
		k8sPodTemplatePatch              string
		expectedVolumes                  []corev1.Volume
		expectedTolerations              []corev1.Toleration
		expectedPodSecurityContext       *corev1apply.PodSecurityContextApplyConfiguration
		expectedContainerSecurityContext *corev1apply.SecurityContextApplyConfiguration
	}{
		{
			name: "with pod template patch",
			k8sPodTemplatePatch: `{
				"spec": {
					"securityContext": {
						"runAsNonRoot": false,
						"runAsUser": 2000,
						"runAsGroup": 2000,
						"fsGroup": 2000
					},
					"containers": [
						{
							"name": "mcp",
							"securityContext": {
								"privileged": true,
								"runAsNonRoot": false,
								"runAsUser": 2000,
								"runAsGroup": 2000,
								"fsGroup": 2000,
								"readOnlyRootFilesystem": false,
								"allowPrivilegeEscalation": true
							}
						}
					],
					"volumes": [
						{
							"name": "test-volume",
							"emptyDir": {}
						}
					],
					"tolerations": [
						{
							"key": "key1",
							"operator": "Equal",
							"value": "value1",
							"effect": "NoSchedule"
						}
					]
				}
			}`,
			expectedPodSecurityContext: corev1apply.PodSecurityContext().
				WithRunAsNonRoot(false).
				WithRunAsUser(int64(2000)).
				WithRunAsGroup(int64(2000)).
				WithFSGroup(int64(2000)),
			expectedContainerSecurityContext: corev1apply.SecurityContext().
				WithRunAsNonRoot(false).
				WithRunAsUser(int64(2000)).
				WithRunAsGroup(int64(2000)).
				WithPrivileged(true).
				WithReadOnlyRootFilesystem(false).
				WithAllowPrivilegeEscalation(true),
			expectedVolumes: []corev1.Volume{
				{
					Name: "test-volume",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
			expectedTolerations: []corev1.Toleration{
				{
					Key:      "key1",
					Operator: "Equal",
					Value:    "value1",
					Effect:   "NoSchedule",
				},
			},
		},
		{
			name:                "without pod template patch",
			k8sPodTemplatePatch: "",
			expectedPodSecurityContext: corev1apply.PodSecurityContext().
				WithRunAsNonRoot(true).
				WithRunAsUser(int64(1000)).
				WithRunAsGroup(int64(1000)).
				WithFSGroup(int64(1000)),
			expectedContainerSecurityContext: corev1apply.SecurityContext().
				WithRunAsNonRoot(true).
				WithRunAsUser(int64(1000)).
				WithRunAsGroup(int64(1000)).
				WithPrivileged(false).
				WithReadOnlyRootFilesystem(true).
				WithAllowPrivilegeEscalation(false),
			expectedVolumes:     nil,
			expectedTolerations: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Create a fake Kubernetes clientset with a mock statefulset
			mockStatefulSet := &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-container",
					Namespace: "default",
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: func() *int32 { i := int32(1); return &i }(),
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "mcp",
									Image: "test-image",
									Args:  []string{"test-command"},
								},
							},
						},
					},
				},
				Status: appsv1.StatefulSetStatus{
					ReadyReplicas: 1,
				},
			}
			clientset := fake.NewSimpleClientset(mockStatefulSet)

			// Create a client with the fake clientset
			client := &Client{
				runtimeType:                 runtime.TypeKubernetes,
				client:                      clientset,
				waitForStatefulSetReadyFunc: mockWaitForStatefulSetReady,
			}
			// Create workload options with the pod template patch
			options := runtime.NewDeployWorkloadOptions()
			options.K8sPodTemplatePatch = tc.k8sPodTemplatePatch

			// Deploy the workload
			containerID, err := client.DeployWorkload(
				context.Background(),
				"test-image",
				"test-container",
				[]string{"test-command"},
				map[string]string{"TEST_ENV": "test-value"},
				map[string]string{"test-label": "test-value"},
				nil,
				"stdio",
				options,
				false,
			)

			// Check that there was no error
			require.NoError(t, err)
			assert.NotEmpty(t, containerID)

			// Get the created StatefulSet
			statefulSet, err := clientset.AppsV1().StatefulSets("default").Get(
				context.Background(),
				"test-container",
				metav1.GetOptions{},
			)
			require.NoError(t, err)

			// Check that the StatefulSet was created with the correct values
			assert.Equal(t, "test-container", statefulSet.Name)
			assert.Equal(t, "test-image", statefulSet.Spec.Template.Spec.Containers[0].Image)
			assert.Equal(t, []string{"test-command"}, statefulSet.Spec.Template.Spec.Containers[0].Args)

			// Check that the pod template patch was applied correctly
			if tc.k8sPodTemplatePatch != "" {
				// Check volumes
				assert.Equal(t, tc.expectedVolumes, statefulSet.Spec.Template.Spec.Volumes)

				// Check tolerations
				assert.Equal(t, tc.expectedTolerations, statefulSet.Spec.Template.Spec.Tolerations)
			} else {
				assert.Empty(t, statefulSet.Spec.Template.Spec.Volumes)
				assert.Empty(t, statefulSet.Spec.Template.Spec.Tolerations)
			}

			// Check pod security context
			assert.NotNil(t, statefulSet.Spec.Template.Spec.SecurityContext, "Pod security context should not be nil")
			assert.Equal(t, tc.expectedPodSecurityContext.RunAsNonRoot, statefulSet.Spec.Template.Spec.SecurityContext.RunAsNonRoot, "RunAsNonRoot should be true")
			assert.Equal(t, tc.expectedPodSecurityContext.RunAsUser, statefulSet.Spec.Template.Spec.SecurityContext.RunAsUser, "RunAsUser should be 1000")
			assert.Equal(t, tc.expectedPodSecurityContext.RunAsGroup, statefulSet.Spec.Template.Spec.SecurityContext.RunAsGroup, "RunAsGroup should be 1000")
			assert.Equal(t, tc.expectedPodSecurityContext.FSGroup, statefulSet.Spec.Template.Spec.SecurityContext.FSGroup, "FSGroup should be 1000")

			// Check container security context
			container := statefulSet.Spec.Template.Spec.Containers[0]
			assert.NotNil(t, container.SecurityContext, "Container security context should not be nil")
			assert.Equal(t, tc.expectedContainerSecurityContext.RunAsNonRoot, container.SecurityContext.RunAsNonRoot, "Container RunAsNonRoot should be true")
			assert.Equal(t, tc.expectedContainerSecurityContext.RunAsUser, container.SecurityContext.RunAsUser, "Container RunAsUser should be 1000")
			assert.Equal(t, tc.expectedContainerSecurityContext.RunAsGroup, container.SecurityContext.RunAsGroup, "Container RunAsGroup should be 1000")
			assert.Equal(t, tc.expectedContainerSecurityContext.Privileged, container.SecurityContext.Privileged, "Container Privileged should be false")
			assert.Equal(t, tc.expectedContainerSecurityContext.ReadOnlyRootFilesystem, container.SecurityContext.ReadOnlyRootFilesystem, "Container ReadOnlyRootFilesystem should be true")
			assert.Equal(t, tc.expectedContainerSecurityContext.AllowPrivilegeEscalation, container.SecurityContext.AllowPrivilegeEscalation, "Container AllowPrivilegeEscalation should be false")
		})
	}
}

// TestCreatePodTemplateFromPatch tests the createPodTemplateFromPatch function
func TestCreatePodTemplateFromPatch(t *testing.T) {
	t.Parallel()
	// Test cases
	testCases := []struct {
		name      string
		patchJSON string
		expectErr bool
	}{
		{
			name: "valid patch",
			patchJSON: `{
				"metadata": {
					"labels": {
						"app": "test-app"
					}
				},
				"spec": {
					"volumes": [
						{
							"name": "test-volume",
							"emptyDir": {}
						}
					]
				}
			}`,
			expectErr: false,
		},
		{
			name:      "invalid JSON",
			patchJSON: `{invalid json`,
			expectErr: true,
		},
		{
			name:      "empty patch",
			patchJSON: `{}`,
			expectErr: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Call the function
			podTemplateSpec, err := createPodTemplateFromPatch(tc.patchJSON)

			// Check the result
			if tc.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, podTemplateSpec)

				// If the patch is not empty, check that it was parsed correctly
				if tc.patchJSON != "{}" {
					// Convert the patch to a map for comparison
					var patchMap map[string]interface{}
					err := json.Unmarshal([]byte(tc.patchJSON), &patchMap)
					require.NoError(t, err)

					// Convert the pod template spec to JSON
					podTemplateJSON, err := json.Marshal(podTemplateSpec)
					require.NoError(t, err)

					// Convert the JSON back to a map
					var podTemplateMap map[string]interface{}
					err = json.Unmarshal(podTemplateJSON, &podTemplateMap)
					require.NoError(t, err)

					// Check that the pod template contains the patch data
					// This is a simplified check, as the exact structure may differ
					if metadata, ok := patchMap["metadata"].(map[string]interface{}); ok {
						if labels, ok := metadata["labels"].(map[string]interface{}); ok {
							if app, ok := labels["app"].(string); ok {
								assert.Equal(t, "test-app", app)
							}
						}
					}
				}
			}
		})
	}
}

// TestEnsurePodTemplateConfig tests the ensurePodTemplateConfig function
func TestEnsurePodTemplateConfig(t *testing.T) {
	t.Parallel()
	// Test cases
	testCases := []struct {
		name            string
		podTemplateSpec *corev1apply.PodTemplateSpecApplyConfiguration
		containerLabels map[string]string
	}{
		{
			name:            "empty pod template",
			podTemplateSpec: corev1apply.PodTemplateSpec(),
			containerLabels: map[string]string{"app": "test-app"},
		},
		{
			name:            "pod template with existing labels",
			podTemplateSpec: corev1apply.PodTemplateSpec().WithLabels(map[string]string{"existing": "label"}),
			containerLabels: map[string]string{"app": "test-app"},
		},
		{
			name:            "pod template with existing spec",
			podTemplateSpec: corev1apply.PodTemplateSpec().WithSpec(corev1apply.PodSpec()),
			containerLabels: map[string]string{"app": "test-app"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Call the function
			result := ensurePodTemplateConfig(tc.podTemplateSpec, tc.containerLabels)

			// Check the result
			assert.NotNil(t, result)
			assert.NotNil(t, result.Labels)
			assert.NotNil(t, result.Spec)
			assert.NotNil(t, result.Spec.RestartPolicy)

			// Check that the labels were merged correctly
			for k, v := range tc.containerLabels {
				assert.Equal(t, v, result.Labels[k])
			}

			// Check that the restart policy was set
			assert.Equal(t, corev1.RestartPolicyAlways, *result.Spec.RestartPolicy)
		})
	}
}

// TestGetMCPContainer tests the getMCPContainer function
func TestGetMCPContainer(t *testing.T) {
	t.Parallel()
	// Test cases
	testCases := []struct {
		name            string
		podTemplateSpec *corev1apply.PodTemplateSpecApplyConfiguration
		expectNil       bool
		expectedName    string
	}{
		{
			name:            "empty pod template",
			podTemplateSpec: corev1apply.PodTemplateSpec().WithSpec(corev1apply.PodSpec()),
			expectNil:       true,
		},
		{
			name: "pod template with existing mcp container",
			podTemplateSpec: corev1apply.PodTemplateSpec().WithSpec(corev1apply.PodSpec().
				WithContainers(corev1apply.Container().WithName(mcpContainerName).WithImage("existing-image"))),
			expectNil:    false,
			expectedName: "mcp",
		},
		{
			name: "pod template with different container",
			podTemplateSpec: corev1apply.PodTemplateSpec().WithSpec(corev1apply.PodSpec().
				WithContainers(corev1apply.Container().WithName("other-container"))),
			expectNil: true,
		},
		{
			name: "pod template with multiple existing containers but no mcp",
			podTemplateSpec: corev1apply.PodTemplateSpec().WithSpec(corev1apply.PodSpec().
				WithContainers(
					corev1apply.Container().WithName("container1"),
					corev1apply.Container().WithName("container2"),
				)),
			expectNil: true,
		},
		{
			name: "pod template with multiple existing containers including mcp",
			podTemplateSpec: corev1apply.PodTemplateSpec().WithSpec(corev1apply.PodSpec().
				WithContainers(
					corev1apply.Container().WithName("container1"),
					corev1apply.Container().WithName(mcpContainerName),
					corev1apply.Container().WithName("container2"),
				)),
			expectNil:    false,
			expectedName: "mcp",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Call the function
			result := getMCPContainer(tc.podTemplateSpec)

			if tc.expectNil {
				// Check that the result is nil
				assert.Nil(t, result, "Expected nil result for %s", tc.name)
			} else {
				// Check that the result is not nil and has the expected name
				assert.NotNil(t, result, "Expected non-nil result for %s", tc.name)
				assert.NotNil(t, result.Name, "Expected non-nil name for %s", tc.name)
				assert.Equal(t, tc.expectedName, *result.Name, "Expected name %s for %s", tc.expectedName, tc.name)
			}
		})
	}
}

// TestConfigureMCPContainer tests the configureMCPContainer function
func TestConfigureMCPContainer(t *testing.T) {
	t.Parallel()
	// Test cases
	testCases := []struct {
		name                string
		podTemplateSpec     *corev1apply.PodTemplateSpecApplyConfiguration
		image               string
		command             []string
		attachStdio         bool
		envVars             []*corev1apply.EnvVarApplyConfiguration
		transportType       string
		options             *runtime.DeployWorkloadOptions
		expectedContainers  int
		expectedImage       string
		expectedCommand     []string
		expectedEnvVarCount int
		expectedPorts       int
	}{
		{
			name: "create new container",
			podTemplateSpec: corev1apply.PodTemplateSpec().WithSpec(corev1apply.PodSpec().
				WithContainers(corev1apply.Container().WithName("other-container"))),
			image:               "test-image",
			command:             []string{"test-command"},
			attachStdio:         true,
			envVars:             []*corev1apply.EnvVarApplyConfiguration{corev1apply.EnvVar().WithName("TEST_ENV").WithValue("test-value")},
			transportType:       "stdio",
			options:             nil,
			expectedContainers:  2,
			expectedImage:       "test-image",
			expectedCommand:     []string{"test-command"},
			expectedEnvVarCount: 1,
			expectedPorts:       0,
		},
		{
			name: "configure existing container",
			podTemplateSpec: corev1apply.PodTemplateSpec().WithSpec(corev1apply.PodSpec().
				WithContainers(
					corev1apply.Container().WithName(mcpContainerName).WithImage("old-image"),
					corev1apply.Container().WithName("other-container"),
				)),
			image:               "test-image",
			command:             []string{"test-command"},
			attachStdio:         true,
			envVars:             []*corev1apply.EnvVarApplyConfiguration{corev1apply.EnvVar().WithName("TEST_ENV").WithValue("test-value")},
			transportType:       "stdio",
			options:             nil,
			expectedContainers:  2,
			expectedImage:       "test-image",
			expectedCommand:     []string{"test-command"},
			expectedEnvVarCount: 1,
			expectedPorts:       0,
		},
		{
			name:            "configure with SSE transport",
			podTemplateSpec: corev1apply.PodTemplateSpec().WithSpec(corev1apply.PodSpec()),
			image:           "test-image",
			command:         []string{"test-command"},
			attachStdio:     true,
			envVars:         []*corev1apply.EnvVarApplyConfiguration{corev1apply.EnvVar().WithName("TEST_ENV").WithValue("test-value")},
			transportType:   "sse",
			options: &runtime.DeployWorkloadOptions{
				ExposedPorts: map[string]struct{}{
					"8080/tcp": {},
				},
			},
			expectedContainers:  1,
			expectedImage:       "test-image",
			expectedCommand:     []string{"test-command"},
			expectedEnvVarCount: 1,
			expectedPorts:       1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Call the function
			err := configureMCPContainer(
				tc.podTemplateSpec,
				tc.image,
				tc.command,
				tc.attachStdio,
				tc.envVars,
				tc.transportType,
				tc.options,
			)

			// Check that there was no error
			require.NoError(t, err)

			// Check that the pod template has a spec
			assert.NotNil(t, tc.podTemplateSpec.Spec)

			// Check that the container list is not nil
			assert.NotNil(t, tc.podTemplateSpec.Spec.Containers)

			// Check the number of containers
			assert.Equal(t, tc.expectedContainers, len(tc.podTemplateSpec.Spec.Containers))

			// Find the mcp container
			var mcpContainer *corev1apply.ContainerApplyConfiguration
			for i := range tc.podTemplateSpec.Spec.Containers {
				container := &tc.podTemplateSpec.Spec.Containers[i]
				if container.Name != nil && *container.Name == mcpContainerName {
					mcpContainer = container
					break
				}
			}

			// Check that the mcp container exists
			assert.NotNil(t, mcpContainer)

			// Check the container configuration
			assert.Equal(t, tc.expectedImage, *mcpContainer.Image)
			assert.Equal(t, tc.expectedCommand, mcpContainer.Args)
			assert.Equal(t, tc.attachStdio, *mcpContainer.Stdin)
			assert.Equal(t, tc.expectedEnvVarCount, len(mcpContainer.Env))

			// Check ports if expected
			if tc.expectedPorts > 0 {
				assert.NotNil(t, mcpContainer.Ports)
				assert.Equal(t, tc.expectedPorts, len(mcpContainer.Ports))
			}
		})
	}
}

// TestCreateContainerWithMCP tests the CreateContainer function with MCP container configuration
func TestCreateContainerWithMCP(t *testing.T) {
	t.Parallel()
	// Test cases
	testCases := []struct {
		name                string
		existingContainers  []corev1.Container
		image               string
		command             []string
		envVars             map[string]string
		attachStdio         bool
		transportType       string
		options             *runtime.DeployWorkloadOptions
		expectedContainers  int
		expectedImage       string
		expectedCommand     []string
		expectedEnvVarCount int
	}{
		{
			name:                "create container with no existing containers",
			existingContainers:  []corev1.Container{},
			image:               "test-image",
			command:             []string{"test-command"},
			envVars:             map[string]string{"TEST_ENV": "test-value"},
			attachStdio:         true,
			transportType:       "stdio",
			options:             nil,
			expectedContainers:  1,
			expectedImage:       "test-image",
			expectedCommand:     []string{"test-command"},
			expectedEnvVarCount: 1,
		},
		{
			name: "create container with existing non-mcp container",
			existingContainers: []corev1.Container{
				{
					Name:  "other-container",
					Image: "other-image",
				},
			},
			image:               "test-image",
			command:             []string{"test-command"},
			envVars:             map[string]string{"TEST_ENV": "test-value"},
			attachStdio:         true,
			transportType:       "stdio",
			options:             nil,
			expectedContainers:  2,
			expectedImage:       "test-image",
			expectedCommand:     []string{"test-command"},
			expectedEnvVarCount: 1,
		},
		{
			name: "create container with existing mcp container",
			existingContainers: []corev1.Container{
				{
					Name:  mcpContainerName,
					Image: "old-image",
				},
			},
			image:               "test-image",
			command:             []string{"test-command"},
			envVars:             map[string]string{"TEST_ENV": "test-value"},
			attachStdio:         true,
			transportType:       "stdio",
			options:             nil,
			expectedContainers:  1,
			expectedImage:       "test-image",
			expectedCommand:     []string{"test-command"},
			expectedEnvVarCount: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Create a fake Kubernetes clientset with a mock statefulset
			mockStatefulSet := &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-container",
					Namespace: "default",
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: func() *int32 { i := int32(1); return &i }(),
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: tc.existingContainers,
						},
					},
				},
				Status: appsv1.StatefulSetStatus{
					ReadyReplicas: 1,
				},
			}
			clientset := fake.NewSimpleClientset(mockStatefulSet)

			// Create a client with the fake clientset
			client := &Client{
				runtimeType:                 runtime.TypeKubernetes,
				client:                      clientset,
				waitForStatefulSetReadyFunc: mockWaitForStatefulSetReady,
			}

			// Deploy the workload
			containerID, err := client.DeployWorkload(
				context.Background(),
				tc.image,
				"test-container",
				tc.command,
				tc.envVars,
				map[string]string{"test-label": "test-value"},
				nil,
				tc.transportType,
				tc.options,
				false,
			)

			// Check that there was no error
			require.NoError(t, err)
			assert.NotEmpty(t, containerID)

			// Get the created StatefulSet
			statefulSet, err := clientset.AppsV1().StatefulSets("default").Get(
				context.Background(),
				"test-container",
				metav1.GetOptions{},
			)
			require.NoError(t, err)

			// Check that the StatefulSet was created with the correct values
			assert.Equal(t, "test-container", statefulSet.Name)

			// Check the number of containers
			assert.Equal(t, tc.expectedContainers, len(statefulSet.Spec.Template.Spec.Containers))

			// Find the mcp container
			var mcpContainer *corev1.Container
			for i := range statefulSet.Spec.Template.Spec.Containers {
				container := &statefulSet.Spec.Template.Spec.Containers[i]
				if container.Name == mcpContainerName {
					mcpContainer = container
					break
				}
			}

			// Check that the mcp container exists
			assert.NotNil(t, mcpContainer)

			// Check the container configuration
			assert.Equal(t, tc.expectedImage, mcpContainer.Image)
			assert.Equal(t, tc.expectedCommand, mcpContainer.Args)
			assert.Equal(t, tc.attachStdio, mcpContainer.Stdin)
			assert.Equal(t, tc.expectedEnvVarCount, len(mcpContainer.Env))
		})
	}
}
