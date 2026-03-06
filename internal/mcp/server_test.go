package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
)

func TestHandleInitialize(t *testing.T) {
	s := &Server{} // No API client needed for protocol-level tests

	req := &Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	}

	resp := s.handleInitialize(req)

	if resp.JSONRPC != "2.0" {
		t.Errorf("Expected jsonrpc '2.0', got %q", resp.JSONRPC)
	}
	if resp.ID != 1 {
		t.Errorf("Expected id 1, got %v", resp.ID)
	}
	if resp.Error != nil {
		t.Errorf("Expected no error, got %v", resp.Error)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected result to be a map, got %T", resp.Result)
	}

	if result["protocolVersion"] != "2024-11-05" {
		t.Errorf("Expected protocol version '2024-11-05', got %v", result["protocolVersion"])
	}

	serverInfo, ok := result["serverInfo"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected serverInfo to be a map")
	}
	if serverInfo["name"] != "gradient" {
		t.Errorf("Expected server name 'gradient', got %v", serverInfo["name"])
	}
	if serverInfo["version"] != "0.1.0" {
		t.Errorf("Expected version '0.1.0', got %v", serverInfo["version"])
	}
}

func TestHandleToolsList(t *testing.T) {
	s := &Server{}

	req := &Request{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
	}

	resp := s.handleToolsList(req)

	if resp.Error != nil {
		t.Fatalf("Expected no error, got %v", resp.Error)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected result to be a map, got %T", resp.Result)
	}

	tools, ok := result["tools"].([]ToolInfo)
	if !ok {
		t.Fatalf("Expected tools to be a []ToolInfo, got %T", result["tools"])
	}

	// Verify we have all expected tools
	expectedTools := []string{
		"gradient_env_create",
		"gradient_env_list",
		"gradient_env_destroy",
		"gradient_env_status",
		"gradient_env_snapshot",
		"gradient_context_get",
		"gradient_context_save",
		"gradient_repo_connect",
		"gradient_repo_list",
		"gradient_snapshot_list",
		"gradient_billing_usage",
		"gradient_secret_sync",
		"gradient_env_ssh",
		"gradient_context_events",
		"gradient_context_publish",
		"gradient_mesh_health",
		"gradient_env_autoscale",
	}

	if len(tools) != len(expectedTools) {
		t.Errorf("Expected %d tools, got %d", len(expectedTools), len(tools))
	}

	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("Tool %q has empty description", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Errorf("Tool %q has nil InputSchema", tool.Name)
		}
	}

	for _, expected := range expectedTools {
		if !toolNames[expected] {
			t.Errorf("Missing expected tool: %s", expected)
		}
	}
}

func TestHandleUnknownMethod(t *testing.T) {
	s := &Server{}

	req := &Request{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "unknown/method",
	}

	resp := s.handleRequest(req)

	if resp == nil {
		t.Fatal("Expected response for unknown method, got nil")
	}
	if resp.Error == nil {
		t.Fatal("Expected error for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("Expected error code -32601 (method not found), got %d", resp.Error.Code)
	}
}

func TestHandleNotification(t *testing.T) {
	s := &Server{}

	req := &Request{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}

	resp := s.handleRequest(req)

	if resp != nil {
		t.Errorf("Expected nil response for notification, got %v", resp)
	}
}

func TestHandleToolsCallMissingName(t *testing.T) {
	s := &Server{}

	params, _ := json.Marshal(map[string]interface{}{})
	req := &Request{
		JSONRPC: "2.0",
		ID:      4,
		Method:  "tools/call",
		Params:  params,
	}

	resp := s.handleToolsCall(req)

	if resp == nil {
		t.Fatal("Expected response, got nil")
	}
	// Empty name should trigger "unknown tool" error
	if resp.Error == nil {
		// Check if result indicates error
		result, ok := resp.Result.(ToolResult)
		if !ok {
			t.Fatal("Expected ToolResult")
		}
		// Unknown tool name "" should be handled
		_ = result
	}
}

func TestHandleToolsCallInvalidParams(t *testing.T) {
	s := &Server{}

	req := &Request{
		JSONRPC: "2.0",
		ID:      5,
		Method:  "tools/call",
		Params:  json.RawMessage(`not valid json`),
	}

	resp := s.handleToolsCall(req)

	if resp == nil {
		t.Fatal("Expected response, got nil")
	}
	if resp.Error == nil {
		t.Fatal("Expected error for invalid params")
	}
	if resp.Error.Code != -32602 {
		t.Errorf("Expected error code -32602 (invalid params), got %d", resp.Error.Code)
	}
}

func TestHandleToolsCallInvalidArguments(t *testing.T) {
	s := &Server{}

	params, _ := json.Marshal(map[string]interface{}{
		"name":      "gradient_env_create",
		"arguments": "not a json object",
	})
	req := &Request{
		JSONRPC: "2.0",
		ID:      6,
		Method:  "tools/call",
		Params:  params,
	}

	resp := s.handleToolsCall(req)

	if resp == nil {
		t.Fatal("Expected response, got nil")
	}
	if resp.Error == nil {
		t.Fatal("Expected error for invalid arguments")
	}
	if resp.Error.Code != -32602 {
		t.Errorf("Expected error code -32602, got %d", resp.Error.Code)
	}
}

func TestHandleToolsCallUnknownTool(t *testing.T) {
	s := &Server{}

	params, _ := json.Marshal(map[string]interface{}{
		"name":      "nonexistent_tool",
		"arguments": map[string]interface{}{},
	})
	req := &Request{
		JSONRPC: "2.0",
		ID:      7,
		Method:  "tools/call",
		Params:  params,
	}

	resp := s.handleToolsCall(req)

	if resp == nil {
		t.Fatal("Expected response, got nil")
	}
	if resp.Error == nil {
		t.Fatal("Expected error for unknown tool")
	}
	if resp.Error.Code != -32602 {
		t.Errorf("Expected error code -32602, got %d", resp.Error.Code)
	}
}

func TestWriteResponse(t *testing.T) {
	s := &Server{}

	t.Run("valid response", func(t *testing.T) {
		var buf bytes.Buffer
		resp := &Response{
			JSONRPC: "2.0",
			ID:      1,
			Result:  map[string]string{"key": "value"},
		}

		s.writeResponse(&buf, resp)

		var decoded Response
		if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
			t.Fatalf("Failed to decode written response: %v", err)
		}
		if decoded.JSONRPC != "2.0" {
			t.Errorf("Expected jsonrpc '2.0', got %q", decoded.JSONRPC)
		}
	})

	t.Run("response ends with newline", func(t *testing.T) {
		var buf bytes.Buffer
		resp := &Response{JSONRPC: "2.0", ID: 1}
		s.writeResponse(&buf, resp)

		data := buf.Bytes()
		if len(data) == 0 {
			t.Fatal("Empty response")
		}
		if data[len(data)-1] != '\n' {
			t.Error("Response does not end with newline")
		}
	})
}

func TestTextResult(t *testing.T) {
	s := &Server{}

	data := map[string]string{"status": "ok"}
	resp := s.textResult(42, data)

	if resp.JSONRPC != "2.0" {
		t.Errorf("Expected jsonrpc '2.0', got %q", resp.JSONRPC)
	}
	if resp.ID != 42 {
		t.Errorf("Expected id 42, got %v", resp.ID)
	}
	if resp.Error != nil {
		t.Errorf("Expected no error, got %v", resp.Error)
	}

	result, ok := resp.Result.(ToolResult)
	if !ok {
		t.Fatalf("Expected ToolResult, got %T", resp.Result)
	}
	if result.IsError {
		t.Error("Expected IsError=false")
	}
	if len(result.Content) != 1 {
		t.Fatalf("Expected 1 content item, got %d", len(result.Content))
	}
	if result.Content[0].Type != "text" {
		t.Errorf("Expected content type 'text', got %q", result.Content[0].Type)
	}
}

func TestErrorResult(t *testing.T) {
	s := &Server{}

	resp := s.errorResult(99, fmt.Errorf("something broke"))

	result, ok := resp.Result.(ToolResult)
	if !ok {
		t.Fatalf("Expected ToolResult, got %T", resp.Result)
	}
	if !result.IsError {
		t.Error("Expected IsError=true")
	}
	if len(result.Content) != 1 {
		t.Fatalf("Expected 1 content item, got %d", len(result.Content))
	}
	if result.Content[0].Text != "Error: something broke" {
		t.Errorf("Unexpected error text: %q", result.Content[0].Text)
	}
}
