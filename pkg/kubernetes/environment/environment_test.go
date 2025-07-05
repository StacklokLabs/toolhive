package environment

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/stacklok/toolhive/pkg/kubernetes/secrets"
)

// mockSecretsProvider is a mock implementation of the secrets.Provider interface
type mockSecretsProvider struct {
	secrets map[string]string
	getErr  error
}

// Ensure mockSecretsProvider implements secrets.Provider
var _ secrets.Provider = (*mockSecretsProvider)(nil)

func (m *mockSecretsProvider) GetSecret(_ context.Context, name string) (string, error) {
	if m.getErr != nil {
		return "", m.getErr
	}
	if val, ok := m.secrets[name]; ok {
		return val, nil
	}
	return "", errors.New("secret not found")
}

func (*mockSecretsProvider) SetSecret(_ context.Context, _ string, _ string) error {
	return nil
}

func (*mockSecretsProvider) DeleteSecret(_ context.Context, _ string) error {
	return nil
}

func (*mockSecretsProvider) ListSecrets(_ context.Context) ([]secrets.SecretDescription, error) {
	return nil, nil
}

func (*mockSecretsProvider) Cleanup() error {
	return nil
}

func (*mockSecretsProvider) Capabilities() secrets.ProviderCapabilities {
	return secrets.ProviderCapabilities{
		CanRead:    true,
		CanWrite:   true,
		CanDelete:  true,
		CanList:    true,
		CanCleanup: true,
	}
}

func TestParseSecretParameters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		parameters []string
		provider   *mockSecretsProvider
		want       map[string]string
		wantErr    bool
	}{
		{
			name:       "Success case",
			parameters: []string{"secret1,target=ENV_VAR1", "secret2,target=ENV_VAR2"},
			provider: &mockSecretsProvider{
				secrets: map[string]string{
					"secret1": "value1",
					"secret2": "value2",
				},
			},
			want: map[string]string{
				"ENV_VAR1": "value1",
				"ENV_VAR2": "value2",
			},
			wantErr: false,
		},
		{
			name:       "Invalid parameter format",
			parameters: []string{"invalid-format"},
			provider:   &mockSecretsProvider{},
			want:       nil,
			wantErr:    true,
		},
		{
			name:       "GetSecret error",
			parameters: []string{"secret1,target=ENV_VAR1"},
			provider: &mockSecretsProvider{
				getErr: errors.New("failed to get secret"),
			},
			want:    nil,
			wantErr: true,
		},
		{
			name:       "Empty parameters",
			parameters: []string{},
			provider:   &mockSecretsProvider{},
			want:       map[string]string{},
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseSecretParameters(t.Context(), tt.parameters, tt.provider)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseSecretParameters() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseSecretParameters() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseEnvironmentVariables(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		envVars []string
		want    map[string]string
		wantErr bool
	}{
		{
			name:    "Success case",
			envVars: []string{"KEY1=value1", "KEY2=value2"},
			want: map[string]string{
				"KEY1": "value1",
				"KEY2": "value2",
			},
			wantErr: false,
		},
		{
			name:    "Empty value",
			envVars: []string{"KEY="},
			want: map[string]string{
				"KEY": "",
			},
			wantErr: false,
		},
		{
			name:    "Value with equals sign",
			envVars: []string{"KEY=value=with=equals"},
			want: map[string]string{
				"KEY": "value=with=equals",
			},
			wantErr: false,
		},
		{
			name:    "Invalid format (missing equals)",
			envVars: []string{"INVALID_FORMAT"},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "Empty key",
			envVars: []string{"=value"},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "Empty environment variables",
			envVars: []string{},
			want:    map[string]string{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseEnvironmentVariables(tt.envVars)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseEnvironmentVariables() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseEnvironmentVariables() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSetTransportEnvironmentVariables(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		transportType string
		port          int
		initialEnv    map[string]string
		expectedEnv   map[string]string
	}{
		{
			name:          "SSE transport with port",
			transportType: "sse",
			port:          8080,
			initialEnv:    map[string]string{},
			expectedEnv: map[string]string{
				"MCP_TRANSPORT": "sse",
				"MCP_PORT":      "8080",
				"FASTMCP_PORT":  "8080",
			},
		},
		{
			name:          "STDIO transport with port",
			transportType: "stdio",
			port:          8080,
			initialEnv:    map[string]string{},
			expectedEnv: map[string]string{
				"MCP_TRANSPORT": "stdio",
			},
		},
		{
			name:          "SSE transport with port zero",
			transportType: "sse",
			port:          0,
			initialEnv:    map[string]string{},
			expectedEnv: map[string]string{
				"MCP_TRANSPORT": "sse",
			},
		},
		{
			name:          "SSE transport with negative port",
			transportType: "sse",
			port:          -1,
			initialEnv:    map[string]string{},
			expectedEnv: map[string]string{
				"MCP_TRANSPORT": "sse",
			},
		},
		{
			name:          "With existing environment variables",
			transportType: "sse",
			port:          8080,
			initialEnv: map[string]string{
				"EXISTING_VAR": "value",
			},
			expectedEnv: map[string]string{
				"EXISTING_VAR":  "value",
				"MCP_TRANSPORT": "sse",
				"MCP_PORT":      "8080",
				"FASTMCP_PORT":  "8080",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			envVars := make(map[string]string)
			for k, v := range tt.initialEnv {
				envVars[k] = v
			}

			SetTransportEnvironmentVariables(envVars, tt.transportType, tt.port)

			if !reflect.DeepEqual(envVars, tt.expectedEnv) {
				t.Errorf("SetTransportEnvironmentVariables() = %v, want %v", envVars, tt.expectedEnv)
			}
		})
	}
}
