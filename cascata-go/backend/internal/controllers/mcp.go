package controllers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"cascata-backend/internal/types"
)

type McpController struct{}

// ConnectSSE establishes a real-time MCP connection for AI agents
func (c *McpController) ConnectSSE(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	// Check Governance Perimeter
	if ctx.Project == nil || !ctx.Project.Metadata.AiGovernance.McpEnabled {
		http.Error(w, `{"error":"MCP not enabled for this project"}`, 403)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	fmt.Fprintf(w, "event: handshake\ndata: %s\n\n", `{"status":"connected","engine":"cascata-go-v1"}`)
	w.(http.Flusher).Flush()

	// Keep alive or handle messages via another channel
	<-r.Context().Done()
}

// HandleMessage processes JSON-RPC 2.0 messages from AI models
func (c *McpController) HandleMessage(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	_ = val.(*types.CascataRequest)

	var msg struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
		ID      interface{}     `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, `{"error":"Invalid JSON-RPC"}`, 400)
		return
	}

	// Governance logic: filter by allowed IPs/URLs defined in Project Metadata
	// perimeter := ctx.Project.Metadata.AiGovernance.McpPerimeter
	
	// Mock JSON-RPC Response for discovery
	res := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      msg.ID,
		"result": map[string]interface{}{
			"tools": []string{"query_data", "insert_data", "reveal_secret"},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}
