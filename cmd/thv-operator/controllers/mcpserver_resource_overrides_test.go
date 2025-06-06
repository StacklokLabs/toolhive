// Copyright 2024 Stacklok, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controllers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mcpv1alpha1 "github.com/stacklok/toolhive/cmd/thv-operator/api/v1alpha1"
)

func TestResourceOverrides(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	require.NoError(t, mcpv1alpha1.AddToScheme(scheme))

	tests := []struct {
		name                     string
		mcpServer                *mcpv1alpha1.MCPServer
		expectedDeploymentLabels map[string]string
		expectedDeploymentAnns   map[string]string
		expectedServiceLabels    map[string]string
		expectedServiceAnns      map[string]string
	}{
		{
			name: "no resource overrides",
			mcpServer: &mcpv1alpha1.MCPServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-server",
					Namespace: "default",
				},
				Spec: mcpv1alpha1.MCPServerSpec{
					Image: "test-image",
					Port:  8080,
				},
			},
			expectedDeploymentLabels: map[string]string{
				"app":                        "mcpserver",
				"app.kubernetes.io/name":     "mcpserver",
				"app.kubernetes.io/instance": "test-server",
				"toolhive":                   "true",
				"toolhive-name":              "test-server",
			},
			expectedDeploymentAnns: map[string]string{},
			expectedServiceLabels: map[string]string{
				"app":                        "mcpserver",
				"app.kubernetes.io/name":     "mcpserver",
				"app.kubernetes.io/instance": "test-server",
				"toolhive":                   "true",
				"toolhive-name":              "test-server",
			},
			expectedServiceAnns: map[string]string{},
		},
		{
			name: "with resource overrides",
			mcpServer: &mcpv1alpha1.MCPServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-server",
					Namespace: "default",
				},
				Spec: mcpv1alpha1.MCPServerSpec{
					Image: "test-image",
					Port:  8080,
					ResourceOverrides: &mcpv1alpha1.ResourceOverrides{
						ProxyDeployment: &mcpv1alpha1.ResourceMetadataOverrides{
							Labels: map[string]string{
								"custom-label": "deployment-value",
								"environment":  "test",
								"app":          "should-be-overridden", // This should be overridden by default
							},
							Annotations: map[string]string{
								"custom-annotation": "deployment-annotation",
								"monitoring/scrape": "true",
							},
						},
						ProxyService: &mcpv1alpha1.ResourceMetadataOverrides{
							Labels: map[string]string{
								"custom-label": "service-value",
								"environment":  "test",
								"toolhive":     "should-be-overridden", // This should be overridden by default
							},
							Annotations: map[string]string{
								"custom-annotation": "service-annotation",
								"service.beta.kubernetes.io/aws-load-balancer-type": "nlb",
							},
						},
					},
				},
			},
			expectedDeploymentLabels: map[string]string{
				"app":                        "mcpserver", // Default takes precedence
				"app.kubernetes.io/name":     "mcpserver",
				"app.kubernetes.io/instance": "test-server",
				"toolhive":                   "true",
				"toolhive-name":              "test-server",
				"custom-label":               "deployment-value",
				"environment":                "test",
			},
			expectedDeploymentAnns: map[string]string{
				"custom-annotation": "deployment-annotation",
				"monitoring/scrape": "true",
			},
			expectedServiceLabels: map[string]string{
				"app":                        "mcpserver",
				"app.kubernetes.io/name":     "mcpserver",
				"app.kubernetes.io/instance": "test-server",
				"toolhive":                   "true", // Default takes precedence
				"toolhive-name":              "test-server",
				"custom-label":               "service-value",
				"environment":                "test",
			},
			expectedServiceAnns: map[string]string{
				"custom-annotation": "service-annotation",
				"service.beta.kubernetes.io/aws-load-balancer-type": "nlb",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := fake.NewClientBuilder().WithScheme(scheme).Build()
			r := &MCPServerReconciler{
				Client: client,
				Scheme: scheme,
			}

			// Test deployment creation
			deployment := r.deploymentForMCPServer(tt.mcpServer)
			require.NotNil(t, deployment)

			assert.Equal(t, tt.expectedDeploymentLabels, deployment.Labels)
			assert.Equal(t, tt.expectedDeploymentAnns, deployment.Annotations)

			// Test service creation
			service := r.serviceForMCPServer(tt.mcpServer)
			require.NotNil(t, service)

			assert.Equal(t, tt.expectedServiceLabels, service.Labels)
			assert.Equal(t, tt.expectedServiceAnns, service.Annotations)
		})
	}
}

func TestMergeStringMaps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		defaultMap  map[string]string
		overrideMap map[string]string
		expected    map[string]string
	}{
		{
			name:        "empty maps",
			defaultMap:  map[string]string{},
			overrideMap: map[string]string{},
			expected:    map[string]string{},
		},
		{
			name:        "only default map",
			defaultMap:  map[string]string{"key1": "default1", "key2": "default2"},
			overrideMap: map[string]string{},
			expected:    map[string]string{"key1": "default1", "key2": "default2"},
		},
		{
			name:        "only override map",
			defaultMap:  map[string]string{},
			overrideMap: map[string]string{"key1": "override1", "key2": "override2"},
			expected:    map[string]string{"key1": "override1", "key2": "override2"},
		},
		{
			name:        "default takes precedence",
			defaultMap:  map[string]string{"key1": "default1", "key2": "default2"},
			overrideMap: map[string]string{"key1": "override1", "key3": "override3"},
			expected:    map[string]string{"key1": "default1", "key2": "default2", "key3": "override3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := mergeStringMaps(tt.defaultMap, tt.overrideMap)
			assert.Equal(t, tt.expected, result)
		})
	}
}
