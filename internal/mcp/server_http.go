package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/windowsfreak/engram-mcp/internal/config"
)

// HTTPServer implements the Streamable HTTP & JSON-RPC MCP server.
type HTTPServer struct {
	cfg        *config.Config
	handler    *Handler
	server     *http.Server
	sessions   map[string]*sseSession
	sessionsMu sync.RWMutex
}

type sseSession struct {
	id          string
	workspaceID string
	msgCh       chan []byte
}

// NewHTTPServer creates a new HTTP MCP server.
func NewHTTPServer(cfg *config.Config, handler *Handler) *HTTPServer {
	s := &HTTPServer{
		cfg:      cfg,
		handler:  handler,
		sessions: make(map[string]*sseSession),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/mcp", s.handleJSONRPC)
	mux.HandleFunc("/rpc", s.handleJSONRPC)
	mux.HandleFunc("/sse", s.handleSSE)
	mux.HandleFunc("/messages", s.handleSSEMessage)

	s.server = &http.Server{
		Addr:    cfg.Server.HTTPAddr,
		Handler: s.corsMiddleware(mux),
	}

	return s
}

// Start begins listening on the configured HTTP address.
func (s *HTTPServer) Start() error {
	return s.server.ListenAndServe()
}

// Shutdown gracefully shuts down the HTTP server.
func (s *HTTPServer) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

func (s *HTTPServer) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *HTTPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"service": "engram-mcp",
		"version": "0.1.0",
	})
}

// resolveWorkspace extracts the workspace ID from the Bearer token or falls back to default.
func (s *HTTPServer) resolveWorkspace(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		return s.cfg.ResolveWorkspaceForToken(authHeader)
	}
	// Also check ?workspace= query param
	if q := r.URL.Query().Get("workspace"); q != "" {
		return q
	}
	return s.cfg.Storage.DefaultWorkspaceID
}

// handleJSONRPC handles standard stateless HTTP POST JSON-RPC 2.0 requests.
func (s *HTTPServer) handleJSONRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	wsID := s.resolveWorkspace(r)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeJSONError(w, nil, -32700, "Parse error")
		return
	}

	var req JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.writeJSONError(w, nil, -32700, "Parse error")
		return
	}

	resp := s.executeRequest(r.Context(), wsID, req)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleSSE handles GET /sse requests for streamable HTTP transport.
func (s *HTTPServer) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	wsID := s.resolveWorkspace(r)
	sessionID := generateID()

	session := &sseSession{
		id:          sessionID,
		workspaceID: wsID,
		msgCh:       make(chan []byte, 32),
	}

	s.sessionsMu.Lock()
	s.sessions[sessionID] = session
	s.sessionsMu.Unlock()

	defer func() {
		s.sessionsMu.Lock()
		delete(s.sessions, sessionID)
		close(session.msgCh)
		s.sessionsMu.Unlock()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Emit initial endpoint event with URI to post messages to
	endpointURL := fmt.Sprintf("/messages?session_id=%s", sessionID)
	_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", endpointURL)
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-session.msgCh:
			if !ok {
				return
			}
			_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", msg)
			flusher.Flush()
		}
	}
}

// handleSSEMessage processes messages posted to an SSE session.
func (s *HTTPServer) handleSSEMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		http.Error(w, "Missing session_id", http.StatusBadRequest)
		return
	}

	s.sessionsMu.RLock()
	session, exists := s.sessions[sessionID]
	s.sessionsMu.RUnlock()

	if !exists {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Read error", http.StatusBadRequest)
		return
	}

	var req JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	resp := s.executeRequest(r.Context(), session.workspaceID, req)
	if resp != nil {
		respBytes, err := json.Marshal(resp)
		if err == nil {
			select {
			case session.msgCh <- respBytes:
			case <-time.After(5 * time.Second):
			}
		}
	}

	w.WriteHeader(http.StatusAccepted)
}

func (s *HTTPServer) executeRequest(ctx context.Context, workspaceID string, req JSONRPCRequest) *JSONRPCResponse {
	if req.ID == nil {
		return nil
	}

	switch req.Method {
	case "initialize":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities": map[string]interface{}{
					"tools": map[string]interface{}{},
				},
				"serverInfo": map[string]interface{}{
					"name":    "engram-mcp",
					"version": "0.1.0",
				},
			},
		}

	case "ping":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]interface{}{},
		}

	case "tools/list":
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"tools": s.handler.GetTools(),
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

		res, err := s.handler.HandleCall(ctx, workspaceID, callParams)
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

func (s *HTTPServer) writeJSONError(w http.ResponseWriter, id interface{}, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(&JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &JSONRPCError{Code: code, Message: message},
	})
}

func generateID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
