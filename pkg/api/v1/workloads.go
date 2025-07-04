package v1

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/stacklok/toolhive/pkg/container/runtime"
	"github.com/stacklok/toolhive/pkg/logger"
	"github.com/stacklok/toolhive/pkg/permissions"
	"github.com/stacklok/toolhive/pkg/runner"
	"github.com/stacklok/toolhive/pkg/runner/retriever"
	"github.com/stacklok/toolhive/pkg/secrets"
	"github.com/stacklok/toolhive/pkg/transport"
	"github.com/stacklok/toolhive/pkg/transport/types"
	"github.com/stacklok/toolhive/pkg/workloads"
)

// WorkloadRoutes defines the routes for workload management.
type WorkloadRoutes struct {
	manager          workloads.Manager
	containerRuntime runtime.Runtime
	debugMode        bool
}

//	@title			ToolHive API
//	@version		1.0
//	@description	This is the ToolHive API workload.
//	@workloads		[ { "url": "http://localhost:8080/api/v1beta" } ]
//	@basePath		/api/v1beta

// WorkloadRouter creates a new WorkloadRoutes instance.
func WorkloadRouter(
	manager workloads.Manager,
	containerRuntime runtime.Runtime,
	debugMode bool,
) http.Handler {
	routes := WorkloadRoutes{
		manager:          manager,
		containerRuntime: containerRuntime,
		debugMode:        debugMode,
	}

	r := chi.NewRouter()
	r.Get("/", routes.listWorkloads)
	r.Post("/", routes.createWorkload)
	r.Post("/stop", routes.stopWorkloadsBulk)
	r.Post("/restart", routes.restartWorkloadsBulk)
	r.Post("/delete", routes.deleteWorkloadsBulk)
	r.Get("/{name}", routes.getWorkload)
	r.Post("/{name}/stop", routes.stopWorkload)
	r.Post("/{name}/restart", routes.restartWorkload)
	r.Get("/{name}/logs", routes.getLogsForWorkload)
	r.Delete("/{name}", routes.deleteWorkload)

	return r
}

//	 listWorkloads
//		@Summary		List all workloads
//		@Description	Get a list of all running workloads
//		@Tags			workloads
//		@Produce		json
//		@Param			all	query		bool	false	"List all workloads, including stopped ones"
//		@Success		200	{object}	workloadListResponse
//		@Router			/api/v1beta/workloads [get]
func (s *WorkloadRoutes) listWorkloads(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	listAll := r.URL.Query().Get("all") == "true"
	workloadList, err := s.manager.ListWorkloads(ctx, listAll)
	if err != nil {
		logger.Errorf("Failed to list workloads: %v", err)
		http.Error(w, "Failed to list workloads", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(workloadListResponse{Workloads: workloadList})
	if err != nil {
		http.Error(w, "Failed to marshal workload list", http.StatusInternalServerError)
		return
	}
}

// getWorkload
//
//	@Summary		Get workload details
//	@Description	Get details of a specific workload
//	@Tags			workloads
//	@Produce		json
//	@Param			name	path		string	true	"Workload name"
//	@Success		200		{object}	workloads.Workload
//	@Failure		404		{string}	string	"Not Found"
//	@Router			/api/v1beta/workloads/{name} [get]
func (s *WorkloadRoutes) getWorkload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := chi.URLParam(r, "name")

	workload, err := s.manager.GetWorkload(ctx, name)
	if err != nil {
		if errors.Is(err, workloads.ErrContainerNotFound) {
			http.Error(w, "Workload not found", http.StatusNotFound)
			return
		} else if errors.Is(err, workloads.ErrInvalidWorkloadName) {
			http.Error(w, "Invalid workload name: "+err.Error(), http.StatusBadRequest)
			return
		}
		logger.Errorf("Failed to get workload: %v", err)
		http.Error(w, "Failed to get workload", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(workload)
	if err != nil {
		http.Error(w, "Failed to marshal workload details", http.StatusInternalServerError)
		return
	}
}

// stopWorkload
//
//	@Summary		Stop a workload
//	@Description	Stop a running workload
//	@Tags			workloads
//	@Param			name	path		string	true	"Workload name"
//	@Success		202		{string}	string	"Accepted"
//	@Failure		400		{string}	string	"Bad Request"
//	@Failure		404		{string}	string	"Not Found"
//	@Router			/api/v1beta/workloads/{name}/stop [post]
func (s *WorkloadRoutes) stopWorkload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := chi.URLParam(r, "name")

	// Use the bulk method with a single workload
	_, err := s.manager.StopWorkloads(ctx, []string{name})
	if err != nil {
		if errors.Is(err, workloads.ErrInvalidWorkloadName) {
			http.Error(w, "Invalid workload name: "+err.Error(), http.StatusBadRequest)
			return
		}
		logger.Errorf("Failed to stop workload: %v", err)
		http.Error(w, "Failed to stop workload", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// restartWorkload
//
//	@Summary		Restart a workload
//	@Description	Restart a running workload
//	@Tags			workloads
//	@Param			name	path		string	true	"Workload name"
//	@Success		202		{string}	string	"Accepted"
//	@Failure		400		{string}	string	"Bad Request"
//	@Failure		404		{string}	string	"Not Found"
//	@Router			/api/v1beta/workloads/{name}/restart [post]
func (s *WorkloadRoutes) restartWorkload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := chi.URLParam(r, "name")

	// Use the bulk method with a single workload
	_, err := s.manager.RestartWorkloads(ctx, []string{name})
	if err != nil {
		if errors.Is(err, workloads.ErrInvalidWorkloadName) {
			http.Error(w, "Invalid workload name: "+err.Error(), http.StatusBadRequest)
			return
		}
		logger.Errorf("Failed to restart workload: %v", err)
		http.Error(w, "Failed to restart workload", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// deleteWorkload
//
//	@Summary		Delete a workload
//	@Description	Delete a workload
//	@Tags			workloads
//	@Param			name	path		string	true	"Workload name"
//	@Success		202		{string}	string	"Accepted"
//	@Failure		400		{string}	string	"Bad Request"
//	@Failure		404		{string}	string	"Not Found"
//	@Router			/api/v1beta/workloads/{name} [delete]
func (s *WorkloadRoutes) deleteWorkload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := chi.URLParam(r, "name")

	// Use the bulk method with a single workload
	_, err := s.manager.DeleteWorkloads(ctx, []string{name})
	if err != nil {
		if errors.Is(err, workloads.ErrInvalidWorkloadName) {
			http.Error(w, "Invalid workload name: "+err.Error(), http.StatusBadRequest)
			return
		}
		logger.Errorf("Failed to delete workload: %v", err)
		http.Error(w, "Failed to delete workload", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// createWorkload
//
//	@Summary		Create a new workload
//	@Description	Create and start a new workload
//	@Tags			workloads
//	@Accept			json
//	@Produce		json
//	@Param			request	body		createRequest	true	"Create workload request"
//	@Success		201		{object}	createWorkloadResponse
//	@Failure		400		{string}	string	"Bad Request"
//	@Failure		409		{string}	string	"Conflict"
//	@Router			/api/v1beta/workloads [post]
func (s *WorkloadRoutes) createWorkload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Failed to decode request", http.StatusBadRequest)
		return
	}

	// Mimic behavior of the CLI by defaulting to the "network" permission profile.
	// TODO: Consider moving this into the run config creation logic.
	if req.PermissionProfile == "" {
		req.PermissionProfile = permissions.ProfileNetwork
	}

	// Fetch or build the requested image
	// TODO: Make verification configurable and return errors over the API.
	imageURL, imageMetadata, err := retriever.GetMCPServer(
		ctx,
		req.Image,
		"", // We do not let the user specify a CA cert path here.
		retriever.VerifyImageWarn,
	)
	if err != nil {
		if errors.Is(err, retriever.ErrImageNotFound) {
			http.Error(w, "MCP server image not found", http.StatusNotFound)
		} else {
			http.Error(w, fmt.Sprintf("Failed to retrieve MCP server image: %v", err), http.StatusInternalServerError)
		}
		return
	}

	// NOTE: None of the k8s-related config logic is included here.
	runSecrets := secrets.SecretParametersToCLI(req.Secrets)
	runConfig, err := runner.NewRunConfigFromFlags(
		ctx,
		s.containerRuntime,
		req.CmdArguments,
		req.Name,
		imageURL,
		imageMetadata,
		req.Host,
		s.debugMode,
		req.Volumes,
		runSecrets,
		req.AuthzConfig,
		"",    // req.AuditConfig not set - auditing not exposed through API yet.
		false, // req.EnableAudit not set - auditing not exposed through API yet.
		req.PermissionProfile,
		transport.LocalhostIPv4, // Seems like a reasonable default for now.
		req.Transport,
		0, // Let the manager figure out which port to use.
		req.TargetPort,
		req.EnvVars,
		req.OIDC.Issuer,
		req.OIDC.Audience,
		req.OIDC.JwksURL,
		req.OIDC.ClientID,
		req.OIDC.AllowOpaqueTokens,
		"",    // otelEndpoint - not exposed through API yet
		"",    // otelServiceName - not exposed through API yet
		0.0,   // otelSamplingRate - default value
		nil,   // otelHeaders - not exposed through API yet
		false, // otelInsecure - not exposed through API yet
		false, // otelEnablePrometheusMetricsPath - not exposed through API yet
		nil,   // otelEnvironmentVariables - not exposed through API yet
		false, // isolateNetwork - not exposed through API yet
		"",    // k8s patch - not relevant here.
		&runner.DetachedEnvVarValidator{},
		types.ProxyMode(req.ProxyMode),
	)
	if err != nil {
		logger.Errorf("Failed to create run config: %v", err)
		http.Error(w, "Failed to create run config", http.StatusBadRequest)
		return
	}

	// Start workload with specified RunConfig.
	err = s.manager.RunWorkloadDetached(runConfig)
	if err != nil {
		logger.Errorf("Failed to start workload: %v", err)
		http.Error(w, "Failed to start workload", http.StatusInternalServerError)
		return
	}

	// Return name so that the client will get the auto-generated name.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	resp := createWorkloadResponse{
		Name: runConfig.ContainerName,
		Port: runConfig.Port,
	}
	if err = json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "Failed to marshal workload details", http.StatusInternalServerError)
		return
	}
}

// stopWorkloadsBulk
//
//	@Summary		Stop workloads in bulk
//	@Description	Stop multiple workloads by name
//	@Tags			workloads
//	@Accept			json
//	@Param			request	body		bulkOperationRequest	true	"Bulk stop request"
//	@Success		202		{string}	string	"Accepted"
//	@Failure		400		{string}	string	"Bad Request"
//	@Router			/api/v1beta/workloads/stop [post]
func (s *WorkloadRoutes) stopWorkloadsBulk(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req bulkOperationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Failed to decode request", http.StatusBadRequest)
		return
	}

	if len(req.Names) == 0 {
		http.Error(w, "No workload names provided", http.StatusBadRequest)
		return
	}

	// Note that this is an asynchronous operation.
	// The request is not blocked on completion.
	_, err := s.manager.StopWorkloads(ctx, req.Names)
	if err != nil {
		if errors.Is(err, workloads.ErrInvalidWorkloadName) {
			http.Error(w, "Invalid workload name: "+err.Error(), http.StatusBadRequest)
			return
		}
		logger.Errorf("Failed to stop workloads: %v", err)
		http.Error(w, "Failed to stop workloads", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// restartWorkloadsBulk
//
//	@Summary		Restart workloads in bulk
//	@Description	Restart multiple workloads by name
//	@Tags			workloads
//	@Accept			json
//	@Param			request	body		bulkOperationRequest	true	"Bulk restart request"
//	@Success		202		{string}	string	"Accepted"
//	@Failure		400		{string}	string	"Bad Request"
//	@Router			/api/v1beta/workloads/restart [post]
func (s *WorkloadRoutes) restartWorkloadsBulk(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req bulkOperationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Failed to decode request", http.StatusBadRequest)
		return
	}

	if len(req.Names) == 0 {
		http.Error(w, "No workload names provided", http.StatusBadRequest)
		return
	}

	// Note that this is an asynchronous operation.
	// The request is not blocked on completion.
	_, err := s.manager.RestartWorkloads(ctx, req.Names)
	if err != nil {
		if errors.Is(err, workloads.ErrInvalidWorkloadName) {
			http.Error(w, "Invalid workload name: "+err.Error(), http.StatusBadRequest)
			return
		}
		logger.Errorf("Failed to restart workloads: %v", err)
		http.Error(w, "Failed to restart workloads", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// deleteWorkloadsBulk
//
//	@Summary		Delete workloads in bulk
//	@Description	Delete multiple workloads by name
//	@Tags			workloads
//	@Accept			json
//	@Param			request	body		bulkOperationRequest	true	"Bulk delete request"
//	@Success		202		{string}	string	"Accepted"
//	@Failure		400		{string}	string	"Bad Request"
//	@Router			/api/v1beta/workloads/delete [post]
func (s *WorkloadRoutes) deleteWorkloadsBulk(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req bulkOperationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Failed to decode request", http.StatusBadRequest)
		return
	}

	if len(req.Names) == 0 {
		http.Error(w, "No workload names provided", http.StatusBadRequest)
		return
	}

	// Note that this is an asynchronous operation.
	// The request is not blocked on completion.
	_, err := s.manager.DeleteWorkloads(ctx, req.Names)
	if err != nil {
		if errors.Is(err, workloads.ErrInvalidWorkloadName) {
			http.Error(w, "Invalid workload name: "+err.Error(), http.StatusBadRequest)
			return
		}
		logger.Errorf("Failed to delete workloads: %v", err)
		http.Error(w, "Failed to delete workloads", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// getLogsForWorkload
//
// @Summary      Get logs for a specific workload
// @Description  Retrieve at most 100 lines of logs for a specific workload by name.
// @Tags         logs
// @Produce      text/plain
// @Param        name  path      string  true  "Workload name"
// @Success      200   {string}  string  "Logs for the specified workload"
// @Failure      404   {string}  string  "Not Found"
// @Router       /api/v1beta/workloads/{name}/logs [get]
func (s *WorkloadRoutes) getLogsForWorkload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := chi.URLParam(r, "name")

	logs, err := s.manager.GetLogs(ctx, name, false)
	if err != nil {
		if errors.Is(err, workloads.ErrContainerNotFound) {
			http.Error(w, "Workload not found", http.StatusNotFound)
			return
		}
		logger.Errorf("Failed to get logs: %v", err)
		http.Error(w, "Failed to get logs", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, err = w.Write([]byte(logs))
	if err != nil {
		logger.Errorf("Failed to write logs response: %v", err)
		http.Error(w, "Failed to write logs response", http.StatusInternalServerError)
		return
	}
}

// Response type definitions.

// workloadListResponse represents the response for listing workloads
//
//	@Description	Response containing a list of workloads
type workloadListResponse struct {
	// List of container information for each workload
	Workloads []workloads.Workload `json:"workloads"`
}

// createRequest represents the request to create a new workload
//
//	@Description	Request to create a new workload
type createRequest struct {
	// Name of the workload
	Name string `json:"name"`
	// Docker image to use
	Image string `json:"image"`
	// Host to bind to
	Host string `json:"host"`
	// Command arguments to pass to the container
	CmdArguments []string `json:"cmd_arguments"`
	// Port to expose from the container
	TargetPort int `json:"target_port"`
	// Environment variables to set in the container
	EnvVars []string `json:"env_vars"`
	// Secret parameters to inject
	Secrets []secrets.SecretParameter `json:"secrets"`
	// Volume mounts
	Volumes []string `json:"volumes"`
	// Transport configuration
	Transport string `json:"transport"`
	// Authorization configuration
	AuthzConfig string `json:"authz_config"`
	// OIDC configuration options
	OIDC oidcOptions `json:"oidc"`
	// Permission profile to apply
	PermissionProfile string `json:"permission_profile"`
	// Proxy mode to use
	ProxyMode string `json:"proxy_mode"`
}

// oidcOptions represents OIDC configuration options
//
//	@Description	OIDC configuration for workload authentication
type oidcOptions struct {
	// OIDC issuer URL
	Issuer string `json:"issuer"`
	// Expected audience
	Audience string `json:"audience"`
	// JWKS URL for key verification
	JwksURL string `json:"jwks_url"`
	// OAuth2 client ID
	ClientID string `json:"client_id"`
	// Allow opaque tokens (non-JWT) for OIDC validation
	AllowOpaqueTokens bool `json:"allow_opaque_tokens"`
}

// createWorkloadResponse represents the response for workload creation
//
//	@Description	Response after successfully creating a workload
type createWorkloadResponse struct {
	// Name of the created workload
	Name string `json:"name"`
	// Port the workload is listening on
	Port int `json:"port"`
}

// bulkOperationRequest represents the request for bulk operations
//
//	@Description	Request to perform bulk operations on workloads
type bulkOperationRequest struct {
	// Names of the workloads to operate on
	Names []string `json:"names"`
}
