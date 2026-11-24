// Package mcp serves the MCP endpoint /mcp (design §7.1): a stateless Streamable HTTP server
// (JSON responses only, no SSE stream, no sessions) with the vox_* tools. It controls the amp as an
// in-process controller of the bridge, like a browser tab does over /ws.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/TomKalina/pi-velvetronix/internal/bridge"
	"github.com/TomKalina/pi-velvetronix/internal/voxproto"
)

const (
	maxBody = 64 << 10
	// latestVersion is answered when the client asks for a protocol version we do not know.
	latestVersion = "2025-06-18"
)

var knownVersions = []string{"2024-11-05", "2025-03-26", "2025-06-18"}

// Controller is the bridge's controller handle (*bridge.Controller, web.Controller).
type Controller interface {
	Send(kind byte, frame []byte) error
	Frames() <-chan bridge.ObservedFrame
	Replaced() <-chan struct{}
	Lost() <-chan struct{}
	Detach()
}

// Config holds the endpoint's dependencies.
type Config struct {
	Version string
	// Attach takes a controller slot; ok is false when all slots are taken.
	Attach func() (c Controller, ok bool)
	// CheckToken reports whether a bearer token is valid.
	CheckToken func(token string) bool
	Log        *slog.Logger
}

// Server is the MCP endpoint.
type Server struct {
	cfg Config
	amp *session
}

// New creates the endpoint. Its amp connection is closed when ctx is done.
func New(ctx context.Context, cfg Config) *Server {
	s := &Server{cfg: cfg, amp: newSession(cfg.Attach)}
	context.AfterFunc(ctx, s.amp.close)
	return s
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// JSON-RPC error codes.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// token returns the bearer token, or else the one in the path /mcp/{token} (for clients that
// cannot send headers, e.g. claude.ai connectors through the tunnel).
func token(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return r.PathValue("token")
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.CheckToken(token(r)) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="pi-velvetronix"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		// No server-to-client stream and no sessions to delete.
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	body = bytes.TrimSpace(body)
	var req rpcRequest
	if len(body) > 0 && body[0] == '[' {
		// Batches were removed from MCP (2025-06-18).
		writeRPC(w, http.StatusBadRequest, rpcResponse{ID: json.RawMessage("null"), Error: &rpcError{codeInvalidRequest, "batches are not supported"}})
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPC(w, http.StatusBadRequest, rpcResponse{ID: json.RawMessage("null"), Error: &rpcError{codeParse, "invalid JSON"}})
		return
	}
	if req.Method == "" {
		// A response from the client (we never send requests) or garbage.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if len(req.ID) == 0 || string(req.ID) == "null" {
		// Notifications (initialized, cancelled) need no answer.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	result, rerr := s.dispatch(r.Context(), req)
	writeRPC(w, http.StatusOK, rpcResponse{ID: req.ID, Result: result, Error: rerr})
}

func writeRPC(w http.ResponseWriter, code int, resp rpcResponse) {
	resp.JSONRPC = "2.0"
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) dispatch(ctx context.Context, req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		v := latestVersion
		if slices.Contains(knownVersions, p.ProtocolVersion) {
			v = p.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "velvetronix", "version": s.cfg.Version},
			"instructions":    instructions,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": toolList()}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{codeInvalidParams, "invalid params"}
		}
		t, ok := toolByName(p.Name)
		if !ok {
			return nil, &rpcError{codeInvalidParams, "unknown tool: " + p.Name}
		}
		start := time.Now()
		res := s.call(t, p.Arguments)
		s.cfg.Log.Info("mcp tool call", "tool", p.Name, "error", res.IsError, "ms", time.Since(start).Milliseconds())
		return res, nil
	}
	return nil, &rpcError{codeMethodNotFound, "method not found: " + req.Method}
}

const instructions = "Controls a VOX Valvetronix X guitar amp (VT100X) through pi-velvetronix. " +
	"vox_set_sound and the play tools change only the live sound; nothing is stored. " +
	"vox_save_program overwrites a user program: use it only when the user named the slot."

type content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type callResult struct {
	Content []content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

// userError is a message for the model as it is.
type userError string

func (e userError) Error() string { return string(e) }

func (s *Server) call(t tool, args json.RawMessage) callResult {
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	v, err := t.run(s, args)
	if err != nil {
		return callResult{IsError: true, Content: []content{{"text", errorText(err)}}}
	}
	text, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return callResult{IsError: true, Content: []content{{"text", "Error: " + err.Error()}}}
	}
	return callResult{Content: []content{{"text", string(text)}}}
}

func errorText(err error) string {
	var ue userError
	var ve *voxproto.Error
	switch {
	case errors.As(err, &ue):
		return string(ue)
	case errors.As(err, &ve):
		return "Amp request failed: " + ve.Code
	case errors.Is(err, voxproto.ErrRange):
		return "Invalid value: " + strings.TrimPrefix(err.Error(), voxproto.ErrRange.Error()+": ")
	}
	return "Error: " + err.Error()
}
