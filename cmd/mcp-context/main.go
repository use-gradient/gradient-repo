package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"
)

// gradient-mcp-context is a lightweight MCP server that runs INSIDE the Docker
// container alongside Claude. It provides two tools for cross-agent communication:
//
//   - get_context_updates: reads /gradient/context/live.json (written by gradient-agent
//     on the host via NATS mesh) and returns events from other agents.
//   - publish_event: appends to /gradient/context/outbox.jsonl which the gradient-agent
//     picks up and publishes to NATS.
//
// No network dependencies — all communication is file-based through the shared volume.

const (
	liveContextPath = "/gradient/context/live.json"
	outboxPath      = "/gradient/context/outbox.jsonl"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolInfo struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema"`
}

type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolResult struct {
	Content []contentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

func main() {
	log.SetOutput(os.Stderr)
	log.Printf("[mcp-context] starting gradient-mcp-context (live.json=%s, outbox=%s)", liveContextPath, outboxPath)

	reader := bufio.NewReader(os.Stdin)
	writer := os.Stdout

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return
			}
			log.Printf("[mcp-context] read error: %v", err)
			return
		}

		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			writeResp(writer, &response{
				JSONRPC: "2.0",
				Error:   &rpcError{Code: -32700, Message: "parse error"},
			})
			continue
		}

		resp := handleRequest(&req)
		if resp != nil {
			writeResp(writer, resp)
		}
	}
}

func writeResp(w io.Writer, resp *response) {
	data, err := json.Marshal(resp)
	if err != nil {
		fmt.Fprintf(w, `{"jsonrpc":"2.0","error":{"code":-32603,"message":"marshal error"}}`+"\n")
		return
	}
	fmt.Fprintf(w, "%s\n", data)
}

func handleRequest(req *request) *response {
	switch req.Method {
	case "initialize":
		return &response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":   map[string]interface{}{"tools": map[string]interface{}{}},
				"serverInfo":     map[string]interface{}{"name": "gradient-context", "version": "0.1.0"},
			},
		}

	case "tools/list":
		return &response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"tools": []toolInfo{
					{
						Name:        "get_context_updates",
						Description: "Check for updates from other agents working on the same task. Returns packages installed, errors encountered, patterns learned, and config changes shared by peer agents via the Live Context Mesh. Call this periodically to stay aware of what other agents are doing.",
						InputSchema: map[string]interface{}{
							"type":       "object",
							"properties": map[string]interface{}{},
						},
					},
					{
						Name:        "publish_event",
						Description: "Share a discovery or decision with other agents working on the same task. Use this to report errors you've encountered, patterns you've learned, packages you've installed, or important decisions you've made.",
						InputSchema: map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"event_type": map[string]string{
									"type":        "string",
									"description": "Event type: error_encountered, pattern_learned, package_installed, config_changed, decision_made",
								},
								"message": map[string]string{
									"type":        "string",
									"description": "Human-readable description of the event",
								},
								"data": map[string]interface{}{
									"type":        "object",
									"description": "Structured data for the event (optional)",
								},
							},
							"required": []string{"event_type", "message"},
						},
					},
				},
			},
		}

	case "tools/call":
		return handleToolsCall(req)

	case "notifications/initialized":
		return nil

	default:
		return &response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)},
		}
	}
}

func handleToolsCall(req *request) *response {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32602, Message: "invalid params"}}
	}

	switch params.Name {
	case "get_context_updates":
		return toolGetContextUpdates(req.ID)
	case "publish_event":
		var args map[string]interface{}
		json.Unmarshal(params.Arguments, &args)
		return toolPublishEvent(req.ID, args)
	default:
		return &response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32602, Message: fmt.Sprintf("unknown tool: %s", params.Name)}}
	}
}

func toolGetContextUpdates(id interface{}) *response {
	data, err := os.ReadFile(liveContextPath)
	if err != nil {
		return textResult(id, "No context updates available yet. Other agents have not published any events.")
	}

	var ctx struct {
		Events     []json.RawMessage `json:"events"`
		Packages   map[string]string `json:"packages"`
		Patterns   map[string]string `json:"patterns"`
		Configs    map[string]string `json:"configs"`
		Errors     []json.RawMessage `json:"errors"`
		LastUpdate string            `json:"last_update"`
	}
	if err := json.Unmarshal(data, &ctx); err != nil {
		return textResult(id, "Context file exists but could not be parsed.")
	}

	var sb strings.Builder
	sb.WriteString("## Context Updates from Other Agents\n\n")

	if ctx.LastUpdate != "" {
		sb.WriteString(fmt.Sprintf("Last update: %s\n\n", ctx.LastUpdate))
	}

	if len(ctx.Packages) > 0 {
		sb.WriteString("### Packages Installed by Peers\n")
		for name, version := range ctx.Packages {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", name, version))
		}
		sb.WriteString("\n")
	}

	if len(ctx.Errors) > 0 {
		sb.WriteString(fmt.Sprintf("### Errors Reported (%d)\n", len(ctx.Errors)))
		limit := len(ctx.Errors)
		if limit > 10 {
			limit = 10
		}
		for _, e := range ctx.Errors[len(ctx.Errors)-limit:] {
			sb.WriteString(fmt.Sprintf("- %s\n", string(e)))
		}
		sb.WriteString("\n")
	}

	if len(ctx.Patterns) > 0 {
		sb.WriteString("### Patterns Learned\n")
		for key, value := range ctx.Patterns {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", key, value))
		}
		sb.WriteString("\n")
	}

	if len(ctx.Configs) > 0 {
		sb.WriteString("### Config Changes\n")
		for key, value := range ctx.Configs {
			sb.WriteString(fmt.Sprintf("- %s = %s\n", key, value))
		}
		sb.WriteString("\n")
	}

	recentCount := 10
	if len(ctx.Events) < recentCount {
		recentCount = len(ctx.Events)
	}
	if recentCount > 0 {
		sb.WriteString(fmt.Sprintf("### Recent Events (%d total, showing last %d)\n", len(ctx.Events), recentCount))
		for _, e := range ctx.Events[len(ctx.Events)-recentCount:] {
			sb.WriteString(fmt.Sprintf("- %s\n", string(e)))
		}
	}

	if sb.Len() == len("## Context Updates from Other Agents\n\n") {
		sb.WriteString("No updates from other agents yet.\n")
	}

	return textResult(id, sb.String())
}

func toolPublishEvent(id interface{}, args map[string]interface{}) *response {
	eventType, _ := args["event_type"].(string)
	message, _ := args["message"].(string)
	if eventType == "" || message == "" {
		return errorResult(id, "event_type and message are required")
	}

	entry := map[string]interface{}{
		"type":      eventType,
		"message":   message,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}
	if data, ok := args["data"]; ok {
		entry["data"] = data
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return errorResult(id, fmt.Sprintf("failed to marshal event: %v", err))
	}

	os.MkdirAll("/gradient/context", 0755)
	f, err := os.OpenFile(outboxPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return errorResult(id, fmt.Sprintf("failed to open outbox: %v", err))
	}
	defer f.Close()

	if _, err := f.Write(append(line, '\n')); err != nil {
		return errorResult(id, fmt.Sprintf("failed to write to outbox: %v", err))
	}

	log.Printf("[mcp-context] Published event: type=%s message=%s", eventType, message)
	return textResult(id, fmt.Sprintf("Event published successfully: [%s] %s", eventType, message))
}

func textResult(id interface{}, text string) *response {
	return &response{
		JSONRPC: "2.0",
		ID:      id,
		Result: toolResult{
			Content: []contentItem{{Type: "text", Text: text}},
		},
	}
}

func errorResult(id interface{}, msg string) *response {
	return &response{
		JSONRPC: "2.0",
		ID:      id,
		Result: toolResult{
			Content: []contentItem{{Type: "text", Text: fmt.Sprintf("Error: %s", msg)}},
			IsError: true,
		},
	}
}
