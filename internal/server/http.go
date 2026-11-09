package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/store"
)

// maxBody bounds request bodies.
const maxBody = 1 << 20

// HTTPConfig configures the HTTP server.
type HTTPConfig struct {
	Addr  string // default 127.0.0.1:8787
	Token string // bearer token; required for a non-loopback address
}

// DefaultAddr listens on loopback only.
const DefaultAddr = "127.0.0.1:8787"

// Validate refuses to expose an unauthenticated API beyond this machine.
func (c HTTPConfig) Validate() error {
	host, _, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", c.Addr, err)
	}
	if c.Token == "" && !isLoopbackHost(host) {
		return fmt.Errorf("listening on %s without a token would expose the API; set an API token or use a loopback address", c.Addr)
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Handler returns the HTTP API plus the MCP endpoint.
func (c *Core) Handler(token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": c.Version})
	})
	mux.HandleFunc("GET /v1/latest", c.handleLatest)
	mux.HandleFunc("GET /v1/search", c.handleSearch)
	mux.HandleFunc("GET /v1/inspect", c.handleInspect)
	mux.HandleFunc("POST /v1/downloads", c.handleDownload)
	mux.HandleFunc("GET /v1/jobs", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"jobs": c.Jobs()})
	})
	mux.HandleFunc("GET /v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		job, err := c.Job(r.PathValue("id"))
		respond(w, job, err)
	})
	mux.HandleFunc("GET /v1/subscriptions", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"subscriptions": c.Subscriptions()})
	})
	mux.HandleFunc("POST /v1/subscriptions", c.handleSubscribe)
	mux.HandleFunc("DELETE /v1/subscriptions/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := c.Unsubscribe(r.PathValue("id")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /v1/subscriptions/{id}/poll", func(w http.ResponseWriter, r *http.Request) {
		if err := c.PollNow(r.Context(), r.PathValue("id")); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "polled"})
	})
	mux.HandleFunc("POST /mcp", c.handleMCP)
	return authenticate(token, mux)
}

// authenticate requires "Authorization: Bearer <token>" on everything but the
// health check when a token is configured.
func authenticate(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="jabledownloader"`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ListenAndServe serves until ctx ends, then shuts down gracefully.
func (c *Core) ListenAndServe(ctx context.Context, cfg HTTPConfig, ready func(addr string)) error {
	if cfg.Addr == "" {
		cfg.Addr = DefaultAddr
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: c.Handler(cfg.Token), ReadHeaderTimeout: 10 * time.Second}
	if ready != nil {
		ready(ln.Addr().String())
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
		return nil
	}
}

func (c *Core) handleLatest(w http.ResponseWriter, r *http.Request) {
	items, err := c.Latest(r.Context(), r.URL.Query()["site"], queryInt(r, "count"))
	respondItems(w, items, err)
}

func (c *Core) handleSearch(w http.ResponseWriter, r *http.Request) {
	items, err := c.Search(r.Context(), r.URL.Query()["site"], r.URL.Query().Get("q"), queryInt(r, "count"))
	respondItems(w, items, err)
}

func (c *Core) handleInspect(w http.ResponseWriter, r *http.Request) {
	detail, err := c.Inspect(r.Context(), r.URL.Query().Get("input"))
	respond(w, detail, err)
}

func (c *Core) handleDownload(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Input string `json:"input"`
	}
	if !decode(w, r, &body) {
		return
	}
	job, created, err := c.Download(body.Input, "api")
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	writeJSON(w, status, job)
}

func (c *Core) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	var body SubscribeRequest
	if !decode(w, r, &body) {
		return
	}
	sub, err := c.Subscribe(body)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sub)
}

func queryInt(r *http.Request, key string) int {
	n, _ := strconv.Atoi(r.URL.Query().Get(key))
	return n
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, invalid("bad JSON body: %v", err))
		return false
	}
	return true
}

func respondItems(w http.ResponseWriter, items []domain.Item, err error) {
	if err != nil && len(items) == 0 {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func respond(w http.ResponseWriter, v any, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, store.ErrNotFound):
		status = http.StatusNotFound
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
