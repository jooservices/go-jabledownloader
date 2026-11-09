package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/jooservices/go-jabledownloader/internal/domain"
)

// MCP speaks JSON-RPC 2.0 for the Model Context Protocol: one request per
// HTTP POST (/mcp), or one JSON message per line over stdio.

const (
	mcpProtocolVersion = "2025-03-26"
	errParse           = -32700
	errInvalidRequest  = -32600
	errMethodNotFound  = -32601
	errInvalidParams   = -32602
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// tool describes one MCP tool.
type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	run         func(ctx context.Context, c *Core, args json.RawMessage) (any, error)
}

func schema(required []string, props map[string]any) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

var (
	strProp   = map[string]any{"type": "string"}
	intProp   = map[string]any{"type": "integer"}
	boolProp  = map[string]any{"type": "boolean"}
	sitesProp = map[string]any{"type": "array", "items": strProp, "description": "Limit to these sites (default: all)"}
)

func tools() []tool {
	return []tool{
		{Name: "search_videos", Description: "Search every supported site (or the given ones) for a keyword such as an actor, tag, or title words.",
			InputSchema: schema([]string{"keyword"}, map[string]any{"keyword": strProp, "sites": sitesProp, "count": intProp}),
			run: func(ctx context.Context, c *Core, raw json.RawMessage) (any, error) {
				var a struct {
					Keyword string   `json:"keyword"`
					Sites   []string `json:"sites"`
					Count   int      `json:"count"`
				}
				if err := unmarshalArgs(raw, &a); err != nil {
					return nil, err
				}
				return itemsResult(c.Search(ctx, a.Sites, a.Keyword, a.Count))
			}},
		{Name: "latest_videos", Description: "List the newest videos on every supported site (or the given ones).",
			InputSchema: schema(nil, map[string]any{"sites": sitesProp, "count": intProp}),
			run: func(ctx context.Context, c *Core, raw json.RawMessage) (any, error) {
				var a struct {
					Sites []string `json:"sites"`
					Count int      `json:"count"`
				}
				if err := unmarshalArgs(raw, &a); err != nil {
					return nil, err
				}
				return itemsResult(c.Latest(ctx, a.Sites, a.Count))
			}},
		{Name: "inspect_video", Description: "Resolve a video URL or code into its title and download sources without downloading it.",
			InputSchema: schema([]string{"input"}, map[string]any{"input": strProp}),
			run: func(ctx context.Context, c *Core, raw json.RawMessage) (any, error) {
				var a struct {
					Input string `json:"input"`
				}
				if err := unmarshalArgs(raw, &a); err != nil {
					return nil, err
				}
				return c.Inspect(ctx, a.Input)
			}},
		{Name: "download_video", Description: "Queue a download of a video or photo gallery by URL or code. Returns a job id to track.",
			InputSchema: schema([]string{"input"}, map[string]any{"input": strProp}),
			run: func(_ context.Context, c *Core, raw json.RawMessage) (any, error) {
				var a struct {
					Input string `json:"input"`
				}
				if err := unmarshalArgs(raw, &a); err != nil {
					return nil, err
				}
				job, created, err := c.Download(a.Input, "mcp")
				return map[string]any{"job": job, "created": created}, err
			}},
		{Name: "list_downloads", Description: "List download jobs (newest first) with their state: queued, running, done, or failed.",
			InputSchema: schema(nil, map[string]any{}),
			run:         func(context.Context, *Core, json.RawMessage) (any, error) { return nil, nil }},
		{Name: "get_download", Description: "Get the state of one download job by id.",
			InputSchema: schema([]string{"id"}, map[string]any{"id": strProp}),
			run: func(_ context.Context, c *Core, raw json.RawMessage) (any, error) {
				var a struct {
					ID string `json:"id"`
				}
				if err := unmarshalArgs(raw, &a); err != nil {
					return nil, err
				}
				return c.Job(a.ID)
			}},
		{Name: "subscribe", Description: "Monitor a source for new videos and download them automatically. kind is 'search' (a keyword: actor, tag, studio) or 'view' (a listing such as latest).",
			InputSchema: schema([]string{"query"}, map[string]any{"query": strProp, "kind": strProp, "site": strProp,
				"auto_download": boolProp, "backfill": boolProp, "interval_minutes": intProp, "max_per_poll": intProp}),
			run: func(_ context.Context, c *Core, raw json.RawMessage) (any, error) {
				var a SubscribeRequest
				if err := unmarshalArgs(raw, &a); err != nil {
					return nil, err
				}
				return c.Subscribe(a)
			}},
		{Name: "list_subscriptions", Description: "List the monitored sources and when each was last checked.",
			InputSchema: schema(nil, map[string]any{}),
			run:         func(context.Context, *Core, json.RawMessage) (any, error) { return nil, nil }},
		{Name: "unsubscribe", Description: "Stop monitoring a source by subscription id.",
			InputSchema: schema([]string{"id"}, map[string]any{"id": strProp}),
			run: func(_ context.Context, c *Core, raw json.RawMessage) (any, error) {
				var a struct {
					ID string `json:"id"`
				}
				if err := unmarshalArgs(raw, &a); err != nil {
					return nil, err
				}
				return map[string]string{"removed": a.ID}, c.Unsubscribe(a.ID)
			}},
		{Name: "check_subscription", Description: "Check a monitored source for new videos right now.",
			InputSchema: schema([]string{"id"}, map[string]any{"id": strProp}),
			run: func(ctx context.Context, c *Core, raw json.RawMessage) (any, error) {
				var a struct {
					ID string `json:"id"`
				}
				if err := unmarshalArgs(raw, &a); err != nil {
					return nil, err
				}
				return map[string]string{"checked": a.ID}, c.PollNow(ctx, a.ID)
			}},
	}
}

func itemsResult(items []domain.Item, err error) (any, error) {
	if err != nil && len(items) == 0 {
		return nil, err
	}
	return map[string]any{"items": items}, nil
}

func unmarshalArgs(raw json.RawMessage, v any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return invalid("bad arguments: %v", err)
	}
	return nil
}

// HandleMCP processes one JSON-RPC message. It returns nil for notifications.
func (c *Core) HandleMCP(ctx context.Context, msg []byte) *rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		return &rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{errParse, "parse error"}}
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		return &rpcResponse{JSONRPC: "2.0", ID: idOrNull(req.ID), Error: &rpcError{errInvalidRequest, "invalid request"}}
	}
	if len(req.ID) == 0 { // notification: no reply
		return nil
	}
	reply := func(result any) *rpcResponse { return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result} }
	fail := func(code int, format string, args ...any) *rpcResponse {
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{code, fmt.Sprintf(format, args...)}}
	}

	switch req.Method {
	case "initialize":
		version := mcpProtocolVersion
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if json.Unmarshal(req.Params, &p) == nil && p.ProtocolVersion != "" {
			version = p.ProtocolVersion
		}
		return reply(map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]string{"name": "jabledownloader", "version": c.Version},
		})
	case "ping":
		return reply(map[string]any{})
	case "tools/list":
		list := tools()
		out := make([]map[string]any, 0, len(list))
		for _, t := range list {
			out = append(out, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.InputSchema})
		}
		return reply(map[string]any{"tools": out})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
			return fail(errInvalidParams, "tools/call needs a tool name")
		}
		for _, t := range tools() {
			if t.Name == p.Name {
				return reply(c.callTool(ctx, t, p.Arguments))
			}
		}
		return fail(errInvalidParams, "unknown tool %q", p.Name)
	default:
		return fail(errMethodNotFound, "method %q not found", req.Method)
	}
}

func idOrNull(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

// callTool runs a tool; tool failures are reported inside the result
// (isError) so the model can read and react to them.
func (c *Core) callTool(ctx context.Context, t tool, args json.RawMessage) map[string]any {
	var (
		result any
		err    error
	)
	switch t.Name {
	case "list_downloads":
		result = map[string]any{"jobs": c.Jobs()}
	case "list_subscriptions":
		result = map[string]any{"subscriptions": c.Subscriptions()}
	default:
		result, err = t.run(ctx, c, args)
	}
	if err != nil {
		return map[string]any{"isError": true, "content": []map[string]string{{"type": "text", "text": err.Error()}}}
	}
	text, merr := json.MarshalIndent(result, "", "  ")
	if merr != nil {
		return map[string]any{"isError": true, "content": []map[string]string{{"type": "text", "text": merr.Error()}}}
	}
	return map[string]any{"content": []map[string]string{{"type": "text", "text": string(text)}}}
}

func (c *Core) handleMCP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		writeError(w, invalid("cannot read body"))
		return
	}
	resp := c.HandleMCP(r.Context(), body)
	if resp == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ServeStdio runs the MCP server over a line-delimited stream (the transport
// AI desktop clients use to launch a local server) until in is exhausted.
func (c *Core) ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), maxBody)
	enc := json.NewEncoder(out)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if resp := c.HandleMCP(ctx, line); resp != nil {
			if err := enc.Encode(resp); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return ctx.Err()
}
