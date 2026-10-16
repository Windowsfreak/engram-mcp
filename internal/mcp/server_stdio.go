package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// StdioServer serves the MCP protocol over standard I/O (stdio).
type StdioServer struct {
	handler     *Handler
	workspaceID string
	reader      io.Reader
	writer      io.Writer
}

// NewStdioServer creates a new Stdio MCP server.
func NewStdioServer(handler *Handler, workspaceID string) *StdioServer {
	return &StdioServer{
		handler:     handler,
		workspaceID: workspaceID,
		reader:      os.Stdin,
		writer:      os.Stdout,
	}
}

// SetIO overrides stdin and stdout for testing.
func (s *StdioServer) SetIO(r io.Reader, w io.Writer) {
	s.reader = r
	s.writer = w
}

// Serve reads JSON-RPC requests line by line from reader and writes responses to writer.
func (s *StdioServer) Serve(ctx context.Context) error {
	scanner := bufio.NewScanner(s.reader)
	// Allow large tokens (up to 10MB) for file transfers
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.writeError(nil, -32700, "Parse error", nil)
			continue
		}

		resp := s.handleRequest(ctx, req)
		if resp != nil {
			respBytes, err := json.Marshal(resp)
			if err == nil {
				_, _ = fmt.Fprintf(s.writer, "%s\n", respBytes)
			}
		}
	}

	return scanner.Err()
}

func (s *StdioServer) handleRequest(ctx context.Context, req JSONRPCRequest) *JSONRPCResponse {
	// Notifications (no ID)
	if req.ID == nil {
		if req.Method == "notifications/initialized" {
			// Acknowledged client initialization
			return nil
		}
		// Discard other notifications
		return nil
	}

	switch req.Method {
	case "initialize":
		result := map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
			"serverInfo": map[string]interface{}{
				"name":    "engram-mcp",
				"version": "0.1.0",
			},
		}
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  result,
		}

	case "ping":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]interface{}{},
		}

	case "tools/list":
		tools := s.handler.GetTools()
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"tools": tools,
			},
		}

	case "tools/call":
		var callParams ToolCallParams
		if err := json.Unmarshal(req.Params, &callParams); err != nil {
			return &JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &JSONRPCError{Code: -32602, Message: "Invalid params: " + err.Error()},
			}
		}

		res, err := s.handler.HandleCall(ctx, s.workspaceID, callParams)
		if err != nil {
			return &JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &JSONRPCError{Code: -32000, Message: err.Error()},
			}
		}

		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  res,
		}

	default:
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &JSONRPCError{Code: -32601, Message: fmt.Sprintf("Method not found: %s", req.Method)},
		}
	}
}

func (s *StdioServer) writeError(id interface{}, code int, message string, data interface{}) {
	resp := &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
			Data:    data,
		},
	}
	bytes, _ := json.Marshal(resp)
	_, _ = fmt.Fprintf(s.writer, "%s\n", bytes)
}
