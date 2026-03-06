package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/gradient/gradient/cmd/cli/commands"
)

// JSON-RPC 2.0 types
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type ToolInfo struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema"`
}

type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ToolResult struct {
	Content []ContentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type Server struct {
	apiClient *commands.APIClient
}

func NewServer() (*Server, error) {
	client, err := commands.NewAPIClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create API client: %w", err)
	}

	return &Server{
		apiClient: client,
	}, nil
}

// Run starts the MCP server on stdio (stdin/stdout)
func (s *Server) Run() error {
	reader := bufio.NewReader(os.Stdin)
	writer := os.Stdout

	log.SetOutput(os.Stderr) // Log to stderr so stdout is clean for JSON-RPC

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("read error: %w", err)
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			s.writeResponse(writer, &Response{
				JSONRPC: "2.0",
				Error: &RPCError{
					Code:    -32700,
					Message: "parse error",
				},
			})
			continue
		}

		resp := s.handleRequest(&req)
		if resp != nil {
			s.writeResponse(writer, resp)
		}
	}
}

func (s *Server) writeResponse(w io.Writer, resp *Response) {
	data, err := json.Marshal(resp)
	if err != nil {
		log.Printf("[mcp] failed to marshal response: %v", err)
		// Write a minimal JSON-RPC error response
		fmt.Fprintf(w, `{"jsonrpc":"2.0","error":{"code":-32603,"message":"internal marshal error"}}`+"\n")
		return
	}
	fmt.Fprintf(w, "%s\n", data)
}

func (s *Server) handleRequest(req *Request) *Response {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req)
	case "notifications/initialized":
		return nil
	default:
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &RPCError{
				Code:    -32601,
				Message: fmt.Sprintf("method not found: %s", req.Method),
			},
		}
	}
}

func (s *Server) handleInitialize(req *Request) *Response {
	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
			"serverInfo": map[string]interface{}{
				"name":    "gradient",
				"version": "0.1.0",
			},
		},
	}
}

func (s *Server) handleToolsList(req *Request) *Response {
	tools := []ToolInfo{
		{
			Name:        "gradient_env_create",
			Description: "Create a new Gradient environment (Docker container on a cloud server). Supports multiple providers (Hetzner, AWS, GCP, etc.), size selection, and context branch replay. If a snapshot exists for the branch, it auto-restores from the container registry.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":           map[string]string{"type": "string", "description": "Environment name"},
					"provider":       map[string]string{"type": "string", "description": "Cloud provider (hetzner, aws, gcp, etc. — defaults to primary configured provider)"},
					"region":         map[string]string{"type": "string", "description": "Provider-specific region/location (e.g. fsn1, us-east-1, us-west1)"},
					"size":           map[string]string{"type": "string", "description": "Size: small, medium, large, gpu (mapped to provider-specific machine types)"},
					"context_branch": map[string]string{"type": "string", "description": "Git branch to replay context + snapshot from"},
				},
				"required": []string{"name", "region"},
			},
		},
		{
			Name:        "gradient_env_list",
			Description: "List all active environments in the current organization.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "gradient_env_destroy",
			Description: "Destroy a Gradient environment by ID.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"env_id": map[string]string{"type": "string", "description": "Environment ID to destroy"},
				},
				"required": []string{"env_id"},
			},
		},
		{
			Name:        "gradient_env_status",
			Description: "Get the status of a specific environment.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"env_id": map[string]string{"type": "string", "description": "Environment ID"},
				},
				"required": []string{"env_id"},
			},
		},
		{
			Name:        "gradient_env_snapshot",
			Description: "Take a container commit snapshot of a running environment. Captures the full filesystem state for later restore.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"env_id": map[string]string{"type": "string", "description": "Environment ID to snapshot"},
					"tag":    map[string]string{"type": "string", "description": "Snapshot tag (auto-generated if empty)"},
				},
				"required": []string{"env_id"},
			},
		},
		{
			Name:        "gradient_context_get",
			Description: "Get the saved context for a git branch (installed packages, test failures, fixes, patterns).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"branch": map[string]string{"type": "string", "description": "Git branch name"},
				},
				"required": []string{"branch"},
			},
		},
		{
			Name:        "gradient_context_save",
			Description: "Save or update context for a git branch.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"branch":  map[string]string{"type": "string", "description": "Git branch name"},
					"commit":  map[string]string{"type": "string", "description": "Commit SHA"},
					"base_os": map[string]string{"type": "string", "description": "Base OS (default: ubuntu-24.04)"},
				},
				"required": []string{"branch"},
			},
		},
		{
			Name:        "gradient_repo_connect",
			Description: "Connect a GitHub repository for auto-fork. New branches automatically inherit context + snapshots from the parent branch.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"repo": map[string]string{"type": "string", "description": "GitHub repository (format: owner/repo)"},
				},
				"required": []string{"repo"},
			},
		},
		{
			Name:        "gradient_repo_list",
			Description: "List all connected GitHub repositories.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "gradient_snapshot_list",
			Description: "List all snapshots for a given branch.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"branch": map[string]string{"type": "string", "description": "Git branch name"},
				},
				"required": []string{"branch"},
			},
		},
		{
			Name:        "gradient_billing_usage",
			Description: "Get billing usage summary for the current org.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"month": map[string]string{"type": "string", "description": "Month in YYYY-MM format"},
				},
			},
		},
		{
			Name:        "gradient_secret_sync",
			Description: "Sync a secret from HashiCorp Vault to a running environment. Reads secrets from Vault and injects them as environment variables into the container via SSH.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"environment_id": map[string]string{"type": "string", "description": "Target environment ID"},
					"secret_key":     map[string]string{"type": "string", "description": "Secret key name"},
					"backend":        map[string]string{"type": "string", "description": "Backend: vault"},
					"backend_path":   map[string]string{"type": "string", "description": "Vault secret path (e.g. secret/data/myapp)"},
				},
				"required": []string{"environment_id", "secret_key", "backend"},
			},
		},
		{
			Name:        "gradient_env_ssh",
			Description: "Get SSH connection info for a running environment. Returns the public IP, user, and port for SSH access. Works with any provider that exposes network info.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"env_id": map[string]string{"type": "string", "description": "Environment ID"},
				},
				"required": []string{"env_id"},
			},
		},
		{
			Name:        "gradient_context_events",
			Description: "Query Live Context Mesh events for a branch. Returns structured context events (package installs, test failures, patterns, config changes) shared in real-time between environments.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"branch":    map[string]string{"type": "string", "description": "Git branch name"},
					"type":      map[string]string{"type": "string", "description": "Event type filter (e.g. package_installed, test_failed)"},
					"limit":     map[string]string{"type": "integer", "description": "Max events to return (default 50)"},
					"env_id":    map[string]string{"type": "string", "description": "Filter by source environment ID"},
					"after_seq": map[string]string{"type": "integer", "description": "Only return events after this sequence number"},
				},
				"required": []string{"branch"},
			},
		},
		{
			Name:        "gradient_context_publish",
			Description: "Publish a context event to the Live Context Mesh. Events are broadcast in real-time to all environments on the same branch.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"branch": map[string]string{"type": "string", "description": "Git branch name"},
					"type":   map[string]string{"type": "string", "description": "Event type (e.g. package_installed, test_failed, pattern_learned, config_changed)"},
					"data":   map[string]string{"type": "object", "description": "Event payload data"},
				},
				"required": []string{"branch", "type", "data"},
			},
		},
		{
			Name:        "gradient_mesh_health",
			Description: "Check the health of the Live Context Mesh (NATS JetStream event bus).",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "gradient_env_autoscale",
			Description: "Configure horizontal autoscaling for a running environment. Set min/max replicas and CPU/memory thresholds. Gradient will automatically scale replicas up/down based on agent-reported metrics.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"environment_id": map[string]string{"type": "string", "description": "Environment ID to configure autoscaling for"},
					"action":         map[string]string{"type": "string", "description": "Action: enable, disable, or status"},
					"min_replicas":   map[string]interface{}{"type": "integer", "description": "Minimum replicas (default: 1)"},
					"max_replicas":   map[string]interface{}{"type": "integer", "description": "Maximum replicas (default: 10)"},
					"target_cpu":     map[string]interface{}{"type": "number", "description": "Target CPU utilization 0-1 (default: 0.7)"},
					"target_memory":  map[string]interface{}{"type": "number", "description": "Target memory utilization 0-1 (default: 0.8)"},
				},
				"required": []string{"environment_id", "action"},
			},
		},
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"tools": tools,
		},
	}
}

func (s *Server) handleToolsCall(req *Request) *Response {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}

	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &RPCError{
				Code:    -32602,
				Message: "invalid params",
			},
		}
	}

	var args map[string]interface{}
	if params.Arguments != nil {
		if err := json.Unmarshal(params.Arguments, &args); err != nil {
			return &Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: &RPCError{
					Code:    -32602,
					Message: fmt.Sprintf("invalid tool arguments: %v", err),
				},
			}
		}
	}
	if args == nil {
		args = map[string]interface{}{}
	}

	switch params.Name {
	case "gradient_env_create":
		return s.toolEnvCreate(req.ID, args)
	case "gradient_env_list":
		return s.toolEnvList(req.ID)
	case "gradient_env_destroy":
		return s.toolEnvDestroy(req.ID, args)
	case "gradient_env_status":
		return s.toolEnvStatus(req.ID, args)
	case "gradient_env_snapshot":
		return s.toolEnvSnapshot(req.ID, args)
	case "gradient_context_get":
		return s.toolContextGet(req.ID, args)
	case "gradient_context_save":
		return s.toolContextSave(req.ID, args)
	case "gradient_repo_connect":
		return s.toolRepoConnect(req.ID, args)
	case "gradient_repo_list":
		return s.toolRepoList(req.ID)
	case "gradient_snapshot_list":
		return s.toolSnapshotList(req.ID, args)
	case "gradient_billing_usage":
		return s.toolBillingUsage(req.ID, args)
	case "gradient_secret_sync":
		return s.toolSecretSync(req.ID, args)
	case "gradient_env_ssh":
		return s.toolEnvSSH(req.ID, args)
	case "gradient_context_events":
		return s.toolContextEvents(req.ID, args)
	case "gradient_context_publish":
		return s.toolContextPublish(req.ID, args)
	case "gradient_mesh_health":
		return s.toolMeshHealth(req.ID)
	case "gradient_env_autoscale":
		return s.toolEnvAutoscale(req.ID, args)
	default:
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &RPCError{
				Code:    -32602,
				Message: fmt.Sprintf("unknown tool: %s", params.Name),
			},
		}
	}
}

// --- Tool implementations (all call the API via HTTP client) ---

func (s *Server) toolEnvCreate(id interface{}, args map[string]interface{}) *Response {
	var result map[string]interface{}
	err := s.apiClient.DoJSON("POST", "/api/v1/environments", args, &result)
	if err != nil {
		return s.errorResult(id, err)
	}
	return s.textResult(id, result)
}

func (s *Server) toolEnvList(id interface{}) *Response {
	var result []map[string]interface{}
	err := s.apiClient.DoJSON("GET", "/api/v1/environments", nil, &result)
	if err != nil {
		return s.errorResult(id, err)
	}
	return s.textResult(id, result)
}

func (s *Server) toolEnvDestroy(id interface{}, args map[string]interface{}) *Response {
	envID, _ := args["env_id"].(string)
	if envID == "" {
		return s.errorResult(id, fmt.Errorf("env_id is required"))
	}

	var result map[string]interface{}
	err := s.apiClient.DoJSON("DELETE", "/api/v1/environments/"+envID, nil, &result)
	if err != nil {
		return s.errorResult(id, err)
	}
	return s.textResult(id, result)
}

func (s *Server) toolEnvStatus(id interface{}, args map[string]interface{}) *Response {
	envID, _ := args["env_id"].(string)
	if envID == "" {
		return s.errorResult(id, fmt.Errorf("env_id is required"))
	}

	var result map[string]interface{}
	err := s.apiClient.DoJSON("GET", "/api/v1/environments/"+envID, nil, &result)
	if err != nil {
		return s.errorResult(id, err)
	}
	return s.textResult(id, result)
}

func (s *Server) toolEnvSnapshot(id interface{}, args map[string]interface{}) *Response {
	envID, _ := args["env_id"].(string)
	if envID == "" {
		return s.errorResult(id, fmt.Errorf("env_id is required"))
	}

	body := map[string]interface{}{}
	if tag, ok := args["tag"].(string); ok {
		body["tag"] = tag
	}

	var result map[string]interface{}
	err := s.apiClient.DoJSON("POST", "/api/v1/environments/"+envID+"/snapshot", body, &result)
	if err != nil {
		return s.errorResult(id, err)
	}
	return s.textResult(id, result)
}

func (s *Server) toolContextGet(id interface{}, args map[string]interface{}) *Response {
	branch, _ := args["branch"].(string)
	if branch == "" {
		return s.errorResult(id, fmt.Errorf("branch is required"))
	}

	var result map[string]interface{}
	err := s.apiClient.DoJSON("GET", "/api/v1/contexts/"+branch, nil, &result)
	if err != nil {
		return s.errorResult(id, err)
	}
	return s.textResult(id, result)
}

func (s *Server) toolContextSave(id interface{}, args map[string]interface{}) *Response {
	body := map[string]interface{}{
		"branch": args["branch"],
	}
	if commit, ok := args["commit"]; ok {
		body["commit_sha"] = commit
	}
	if baseOS, ok := args["base_os"]; ok {
		body["base_os"] = baseOS
	}

	var result map[string]interface{}
	err := s.apiClient.DoJSON("POST", "/api/v1/contexts", body, &result)
	if err != nil {
		return s.errorResult(id, err)
	}
	return s.textResult(id, result)
}

func (s *Server) toolRepoConnect(id interface{}, args map[string]interface{}) *Response {
	repo, _ := args["repo"].(string)
	if repo == "" {
		return s.errorResult(id, fmt.Errorf("repo is required (format: owner/repo)"))
	}

	body := map[string]string{"repo": repo}
	var result map[string]interface{}
	err := s.apiClient.DoJSON("POST", "/api/v1/repos", body, &result)
	if err != nil {
		return s.errorResult(id, err)
	}
	return s.textResult(id, result)
}

func (s *Server) toolRepoList(id interface{}) *Response {
	var result []map[string]interface{}
	err := s.apiClient.DoJSON("GET", "/api/v1/repos", nil, &result)
	if err != nil {
		return s.errorResult(id, err)
	}
	return s.textResult(id, result)
}

func (s *Server) toolSnapshotList(id interface{}, args map[string]interface{}) *Response {
	branch, _ := args["branch"].(string)
	if branch == "" {
		return s.errorResult(id, fmt.Errorf("branch is required"))
	}

	var result []map[string]interface{}
	err := s.apiClient.DoJSON("GET", "/api/v1/snapshots?branch="+branch, nil, &result)
	if err != nil {
		return s.errorResult(id, err)
	}
	return s.textResult(id, result)
}

func (s *Server) toolBillingUsage(id interface{}, args map[string]interface{}) *Response {
	path := "/api/v1/billing/usage"
	if month, ok := args["month"].(string); ok && month != "" {
		path += "?month=" + month
	}

	var result map[string]interface{}
	err := s.apiClient.DoJSON("GET", path, nil, &result)
	if err != nil {
		return s.errorResult(id, err)
	}
	return s.textResult(id, result)
}

func (s *Server) toolSecretSync(id interface{}, args map[string]interface{}) *Response {
	var result map[string]interface{}
	err := s.apiClient.DoJSON("POST", "/api/v1/secrets/sync", args, &result)
	if err != nil {
		return s.errorResult(id, err)
	}
	return s.textResult(id, result)
}

func (s *Server) toolEnvSSH(id interface{}, args map[string]interface{}) *Response {
	envID, _ := args["env_id"].(string)
	if envID == "" {
		return s.errorResult(id, fmt.Errorf("env_id is required"))
	}

	var result map[string]interface{}
	err := s.apiClient.DoJSON("GET", "/api/v1/environments/"+envID+"/ssh-info", nil, &result)
	if err != nil {
		return s.errorResult(id, err)
	}
	return s.textResult(id, result)
}

func (s *Server) toolContextEvents(id interface{}, args map[string]interface{}) *Response {
	branch, _ := args["branch"].(string)
	if branch == "" {
		return s.errorResult(id, fmt.Errorf("branch is required"))
	}

	path := "/api/v1/events?branch=" + branch
	if t, ok := args["type"].(string); ok && t != "" {
		path += "&type=" + t
	}
	if envID, ok := args["env_id"].(string); ok && envID != "" {
		path += "&env_id=" + envID
	}
	if limit, ok := args["limit"].(float64); ok {
		path += fmt.Sprintf("&limit=%d", int(limit))
	}
	if afterSeq, ok := args["after_seq"].(float64); ok {
		path += fmt.Sprintf("&after_seq=%d", int(afterSeq))
	}

	var result interface{}
	err := s.apiClient.DoJSON("GET", path, nil, &result)
	if err != nil {
		return s.errorResult(id, err)
	}
	return s.textResult(id, result)
}

func (s *Server) toolContextPublish(id interface{}, args map[string]interface{}) *Response {
	var result map[string]interface{}
	err := s.apiClient.DoJSON("POST", "/api/v1/events", args, &result)
	if err != nil {
		return s.errorResult(id, err)
	}
	return s.textResult(id, result)
}

func (s *Server) toolMeshHealth(id interface{}) *Response {
	var result map[string]interface{}
	err := s.apiClient.DoJSON("GET", "/api/v1/mesh/health", nil, &result)
	if err != nil {
		return s.errorResult(id, err)
	}
	return s.textResult(id, result)
}

func (s *Server) toolEnvAutoscale(id interface{}, args map[string]interface{}) *Response {
	envID, _ := args["environment_id"].(string)
	action, _ := args["action"].(string)

	if envID == "" {
		return s.errorResult(id, fmt.Errorf("environment_id is required"))
	}

	switch action {
	case "enable":
		body := map[string]interface{}{
			"enabled": true,
		}
		if v, ok := args["min_replicas"]; ok {
			body["min_replicas"] = v
		}
		if v, ok := args["max_replicas"]; ok {
			body["max_replicas"] = v
		}
		if v, ok := args["target_cpu"]; ok {
			body["target_cpu"] = v
		}
		if v, ok := args["target_memory"]; ok {
			body["target_memory"] = v
		}
		var result map[string]interface{}
		if err := s.apiClient.DoJSON("PUT", "/api/v1/environments/"+envID+"/autoscale", body, &result); err != nil {
			return s.errorResult(id, err)
		}
		return s.textResult(id, result)

	case "disable":
		var result map[string]interface{}
		if err := s.apiClient.DoJSON("DELETE", "/api/v1/environments/"+envID+"/autoscale", nil, &result); err != nil {
			return s.errorResult(id, err)
		}
		return s.textResult(id, result)

	case "status":
		var result map[string]interface{}
		if err := s.apiClient.DoJSON("GET", "/api/v1/environments/"+envID+"/autoscale/status", nil, &result); err != nil {
			return s.errorResult(id, err)
		}
		return s.textResult(id, result)

	default:
		return s.errorResult(id, fmt.Errorf("action must be 'enable', 'disable', or 'status'"))
	}
}

// --- Result helpers ---

func (s *Server) textResult(id interface{}, data interface{}) *Response {
	text, _ := json.MarshalIndent(data, "", "  ")
	return &Response{
		JSONRPC: "2.0",
		ID:      id,
		Result: ToolResult{
			Content: []ContentItem{
				{Type: "text", Text: string(text)},
			},
		},
	}
}

func (s *Server) errorResult(id interface{}, err error) *Response {
	return &Response{
		JSONRPC: "2.0",
		ID:      id,
		Result: ToolResult{
			Content: []ContentItem{
				{Type: "text", Text: fmt.Sprintf("Error: %s", err.Error())},
			},
			IsError: true,
		},
	}
}
