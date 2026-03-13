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
	memoryPath      = "/gradient/context/memory.json"
	sessionPath     = "/gradient/context/session.json"
	cursorPath      = "/gradient/context/read_cursor.json"
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
				"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
				"serverInfo":      map[string]interface{}{"name": "gradient-context", "version": "0.2.0"},
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
						Description: "Return unread operational deltas from the live context mesh, including peer package changes, config updates, contract changes, and urgent issues.",
						InputSchema: map[string]interface{}{
							"type":       "object",
							"properties": map[string]interface{}{},
						},
					},
					{
						Name:        "get_memory_guidance",
						Description: "Inspect the retrieved durable guidance for this task or a specific subtask before making changes.",
						InputSchema: map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"subtask": map[string]string{
									"type":        "string",
									"description": "Optional subtask name",
								},
								"failure_signature": map[string]string{
									"type":        "string",
									"description": "Optional failure signature to filter recovery guidance",
								},
								"goal": map[string]string{
									"type":        "string",
									"description": "Optional current goal or intent to refine guidance selection",
								},
							},
						},
					},
					{
						Name:        "publish_event",
						Description: "Share a structured discovery or decision with other agents working on the same task.",
						InputSchema: map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"event_type": map[string]string{
									"type":        "string",
									"description": "Event type: error_encountered, decision_made, package_installed, config_changed, contract_updated, custom",
								},
								"message": map[string]string{
									"type":        "string",
									"description": "Short summary of the event",
								},
								"subtask": map[string]string{
									"type":        "string",
									"description": "Related subtask name",
								},
								"outcome": map[string]string{
									"type":        "string",
									"description": "completed, failed, blocked, success",
								},
								"failure_signature": map[string]string{
									"type":        "string",
									"description": "Stable failure signature",
								},
								"related_files": map[string]interface{}{
									"type":        "array",
									"description": "Related files",
									"items":       map[string]string{"type": "string"},
								},
								"data": map[string]interface{}{
									"type":        "object",
									"description": "Structured data for the event (optional)",
								},
							},
							"required": []string{"event_type"},
						},
					},
					{
						Name:        "mark_subtask",
						Description: "Record a subtask boundary and its outcome so the trajectory memory pipeline can learn from it.",
						InputSchema: map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"name": map[string]string{
									"type":        "string",
									"description": "Subtask name",
								},
								"outcome": map[string]string{
									"type":        "string",
									"description": "started, completed, failed, blocked",
								},
								"summary": map[string]string{
									"type":        "string",
									"description": "Short subtask result summary",
								},
								"failure_signature": map[string]string{
									"type":        "string",
									"description": "Stable failure signature",
								},
								"related_files": map[string]interface{}{
									"type":        "array",
									"description": "Related files",
									"items":       map[string]string{"type": "string"},
								},
							},
							"required": []string{"name", "outcome"},
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
	case "get_memory_guidance":
		var args map[string]interface{}
		json.Unmarshal(params.Arguments, &args)
		return toolGetMemoryGuidance(req.ID, args)
	case "publish_event":
		var args map[string]interface{}
		json.Unmarshal(params.Arguments, &args)
		return toolPublishEvent(req.ID, args)
	case "mark_subtask":
		var args map[string]interface{}
		json.Unmarshal(params.Arguments, &args)
		return toolMarkSubtask(req.ID, args)
	default:
		return &response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32602, Message: fmt.Sprintf("unknown tool: %s", params.Name)}}
	}
}

func toolGetContextUpdates(id interface{}) *response {
	data, err := os.ReadFile(liveContextPath)
	if err != nil {
		return textResult(id, "No new operational updates from peers.")
	}

	var ctx struct {
		Packages     map[string]string `json:"packages"`
		Configs      map[string]string `json:"configs"`
		Contracts    map[string]string `json:"contracts"`
		UrgentIssues []struct {
			Type    string `json:"type"`
			Summary string `json:"summary"`
		} `json:"urgent_issues"`
		RecentUpdates []struct {
			Seq     int64  `json:"seq"`
			Type    string `json:"type"`
			Summary string `json:"summary"`
		} `json:"recent_updates"`
		LastUpdate string `json:"last_update"`
	}
	if err := json.Unmarshal(data, &ctx); err != nil {
		return textResult(id, "Context file exists but could not be parsed.")
	}

	cursor := struct {
		LastSeq int64 `json:"last_seq"`
	}{}
	if data, err := os.ReadFile(cursorPath); err == nil {
		_ = json.Unmarshal(data, &cursor)
	}

	var unread []struct {
		Seq     int64
		Type    string
		Summary string
	}
	maxSeq := cursor.LastSeq
	for _, update := range ctx.RecentUpdates {
		if update.Seq > cursor.LastSeq {
			unread = append(unread, struct {
				Seq     int64
				Type    string
				Summary string
			}{Seq: update.Seq, Type: update.Type, Summary: update.Summary})
		}
		if update.Seq > maxSeq {
			maxSeq = update.Seq
		}
	}
	_ = writeJSONFile(cursorPath, map[string]interface{}{
		"last_seq": maxSeq,
		"read_at":  time.Now().UTC().Format(time.RFC3339),
	})

	var sb strings.Builder
	sb.WriteString("## Operational Updates\n\n")

	if ctx.LastUpdate != "" {
		sb.WriteString(fmt.Sprintf("Last update: %s\n\n", ctx.LastUpdate))
	}

	if len(ctx.Packages) > 0 {
		sb.WriteString("### Shared Packages\n")
		for name, version := range ctx.Packages {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", name, version))
		}
		sb.WriteString("\n")
	}

	if len(ctx.Configs) > 0 {
		sb.WriteString("### Shared Config\n")
		for key, value := range ctx.Configs {
			sb.WriteString(fmt.Sprintf("- %s = %s\n", key, value))
		}
		sb.WriteString("\n")
	}

	if len(ctx.Contracts) > 0 {
		sb.WriteString("### Contract Changes\n")
		for key, value := range ctx.Contracts {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", key, value))
		}
		sb.WriteString("\n")
	}

	if len(ctx.UrgentIssues) > 0 {
		sb.WriteString("### Urgent Peer Issues\n")
		limit := len(ctx.UrgentIssues)
		if limit > 5 {
			limit = 5
		}
		for _, item := range ctx.UrgentIssues[len(ctx.UrgentIssues)-limit:] {
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", item.Type, item.Summary))
		}
		sb.WriteString("\n")
	}

	if len(unread) > 0 {
		sb.WriteString("### New Unread Updates\n")
		for _, item := range unread {
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", item.Type, item.Summary))
		}
	}

	if sb.Len() == len("## Operational Updates\n\n") {
		sb.WriteString("No new operational updates from peers.\n")
	}

	return textResult(id, sb.String())
}

func toolPublishEvent(id interface{}, args map[string]interface{}) *response {
	eventType, _ := args["event_type"].(string)
	message, _ := args["message"].(string)
	if eventType == "" {
		return errorResult(id, "event_type is required")
	}

	sessionMeta := readJSONFile(sessionPath)
	payload := map[string]interface{}{}
	if data, ok := args["data"].(map[string]interface{}); ok {
		for k, v := range data {
			payload[k] = v
		}
	}
	if value, ok := sessionMeta["task_id"]; ok && payload["task_id"] == nil {
		payload["task_id"] = value
	}
	if value, ok := sessionMeta["session_id"]; ok && payload["session_id"] == nil {
		payload["session_id"] = value
	}
	if value, ok := args["subtask"].(string); ok && value != "" {
		payload["subtask"] = value
	}
	if value, ok := args["outcome"].(string); ok && value != "" {
		payload["outcome"] = value
	}
	if value, ok := args["failure_signature"].(string); ok && value != "" {
		payload["failure_signature"] = value
	}
	if value, ok := args["related_files"].([]interface{}); ok {
		payload["related_files"] = value
	}

	entry := map[string]interface{}{
		"type":      eventType,
		"message":   firstNonEmptyString(message, stringValue(args["summary"]), stringValue(args["subtask"]), "Structured event"),
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"data":      payload,
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
	return textResult(id, fmt.Sprintf("Event published successfully: [%s] %s", eventType, entry["message"]))
}

func toolGetMemoryGuidance(id interface{}, args map[string]interface{}) *response {
	data, err := os.ReadFile(memoryPath)
	if err != nil {
		return textResult(id, "No retrieved memory guidance is available yet.")
	}

	var memory struct {
		Tips []map[string]interface{} `json:"tips"`
	}
	if err := json.Unmarshal(data, &memory); err != nil {
		return textResult(id, "Memory guidance file exists but could not be parsed.")
	}

	subtask := strings.ToLower(stringValue(args["subtask"]))
	failure := strings.ToLower(stringValue(args["failure_signature"]))
	goal := strings.ToLower(stringValue(args["goal"]))
	tips := memory.Tips
	if failure != "" {
		var filtered []map[string]interface{}
		for _, tip := range tips {
			if strings.ToLower(stringValue(tip["failure_signature"])) == failure {
				filtered = append(filtered, tip)
			}
		}
		if len(filtered) > 0 {
			tips = filtered
		}
	}
	if subtask != "" {
		var filtered []map[string]interface{}
		for _, tip := range tips {
			if strings.Contains(strings.ToLower(stringValue(tip["title"])), subtask) ||
				strings.Contains(strings.ToLower(stringValue(tip["content"])), subtask) ||
				strings.Contains(strings.ToLower(stringValue(tip["trigger_condition"])), subtask) {
				filtered = append(filtered, tip)
			}
		}
		if len(filtered) > 0 {
			tips = filtered
		}
	}
	if goal != "" {
		var filtered []map[string]interface{}
		for _, tip := range tips {
			if strings.Contains(strings.ToLower(stringValue(tip["title"])), goal) ||
				strings.Contains(strings.ToLower(stringValue(tip["content"])), goal) ||
				strings.Contains(strings.ToLower(stringValue(tip["trigger_condition"])), goal) {
				filtered = append(filtered, tip)
			}
		}
		if len(filtered) > 0 {
			tips = filtered
		}
	}
	if len(tips) == 0 {
		return textResult(id, "No retrieved memory guidance matched this request.")
	}

	var sb strings.Builder
	sb.WriteString("## Retrieved Memory Guidance\n\n")
	limit := len(tips)
	if limit > 5 {
		limit = 5
	}
	for _, tip := range tips[:limit] {
		sb.WriteString(fmt.Sprintf("[%s][%s] %s\n",
			strings.ToUpper(firstNonEmptyString(stringValue(tip["priority"]), "medium")),
			strings.ToUpper(firstNonEmptyString(stringValue(tip["type"]), "strategy")),
			firstNonEmptyString(stringValue(tip["title"]), "Guidance"),
		))
		sb.WriteString(fmt.Sprintf("- Guidance: %s\n", stringValue(tip["content"])))
		if reason := stringValue(tip["reason"]); reason != "" {
			sb.WriteString(fmt.Sprintf("- Why selected: %s\n", reason))
		}
		if trigger := stringValue(tip["trigger_condition"]); trigger != "" {
			sb.WriteString(fmt.Sprintf("- Apply when: %s\n", trigger))
		}
		if steps, ok := tip["action_steps"].([]interface{}); ok {
			for i, step := range steps {
				sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, stringValue(step)))
			}
		}
		sb.WriteString("\n")
	}

	return textResult(id, strings.TrimSpace(sb.String()))
}

func toolMarkSubtask(id interface{}, args map[string]interface{}) *response {
	name, _ := args["name"].(string)
	outcome, _ := args["outcome"].(string)
	if name == "" || outcome == "" {
		return errorResult(id, "name and outcome are required")
	}

	sessionMeta := readJSONFile(sessionPath)
	entry := map[string]interface{}{
		"type":      "subtask_marked",
		"message":   fmt.Sprintf("%s: %s", name, outcome),
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"data": map[string]interface{}{
			"task_id":           sessionMeta["task_id"],
			"session_id":        sessionMeta["session_id"],
			"subtask":           name,
			"name":              name,
			"outcome":           outcome,
			"summary":           stringValue(args["summary"]),
			"failure_signature": stringValue(args["failure_signature"]),
			"related_files":     args["related_files"],
		},
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return errorResult(id, fmt.Sprintf("failed to marshal subtask event: %v", err))
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
	return textResult(id, fmt.Sprintf("Subtask recorded: %s (%s)", name, outcome))
}

func readJSONFile(path string) map[string]interface{} {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]interface{}{}
	}
	var out map[string]interface{}
	if json.Unmarshal(data, &out) != nil {
		return map[string]interface{}{}
	}
	return out
}

func writeJSONFile(path string, value map[string]interface{}) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func stringValue(value interface{}) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return fmt.Sprint(value)
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
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
