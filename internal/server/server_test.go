package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jooservices/go-jabledownloader/internal/domain"
	"github.com/jooservices/go-jabledownloader/internal/store"
)

type fakeBackend struct {
	items []domain.Item
	err   error
	sites []string
}

func (f *fakeBackend) Latest(_ context.Context, sites []string, _ int) ([]domain.Item, error) {
	f.sites = sites
	return f.items, f.err
}

func (f *fakeBackend) Search(_ context.Context, sites []string, _ string, _ int) ([]domain.Item, error) {
	f.sites = sites
	return f.items, f.err
}

func (f *fakeBackend) Inspect(_ context.Context, input string) (*domain.Detail, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &domain.Detail{Site: "jable", Code: input, Title: "T"}, nil
}

type fakeQueue struct {
	store *store.Store
	err   error
}

func (q *fakeQueue) Enqueue(input, origin string) (domain.Job, bool, error) {
	if q.err != nil {
		return domain.Job{}, false, q.err
	}
	return q.store.AddJob(domain.Job{Input: input, Origin: origin})
}

type fakePoller struct{ err error }

func (p fakePoller) Poll(context.Context, string) error { return p.err }

func newCore(t *testing.T) (*Core, *fakeBackend) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "s.json"))
	if err != nil {
		t.Fatal(err)
	}
	be := &fakeBackend{items: []domain.Item{{Site: "jable", Code: "A-1", Title: "One"}}}
	return &Core{Backend: be, Queue: &fakeQueue{store: st}, Poller: fakePoller{}, Store: st, Version: "9.9"}, be
}

func do(t *testing.T, h http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return out
}

func TestHTTPDiscovery(t *testing.T) {
	c, be := newCore(t)
	h := c.Handler("")

	rec := do(t, h, "GET", "/v1/latest?site=jable&site=eporner&count=500", "", "")
	if rec.Code != 200 || len(decodeBody(t, rec)["items"].([]any)) != 1 || len(be.sites) != 2 {
		t.Fatalf("latest: %d %s sites=%v", rec.Code, rec.Body, be.sites)
	}
	if rec := do(t, h, "GET", "/v1/search?q=abc", "", ""); rec.Code != 200 {
		t.Fatalf("search: %d", rec.Code)
	}
	if rec := do(t, h, "GET", "/v1/search", "", ""); rec.Code != 400 {
		t.Fatalf("missing keyword must be 400, got %d", rec.Code)
	}
	if rec := do(t, h, "GET", "/v1/inspect?input=A-1", "", ""); rec.Code != 200 || decodeBody(t, rec)["code"] != "A-1" {
		t.Fatalf("inspect: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, "GET", "/v1/inspect", "", ""); rec.Code != 400 {
		t.Fatal("missing input must be 400")
	}
	be.err = errors.New("site down")
	be.items = nil
	if rec := do(t, h, "GET", "/v1/latest", "", ""); rec.Code != 500 {
		t.Fatalf("backend error: %d", rec.Code)
	}
	be.items = []domain.Item{{Code: "partial"}}
	if rec := do(t, h, "GET", "/v1/latest", "", ""); rec.Code != 200 {
		t.Fatalf("partial results must still be served: %d", rec.Code)
	}
	if rec := do(t, h, "GET", "/v1/inspect?input=x", "", ""); rec.Code != 500 {
		t.Fatalf("inspect error: %d", rec.Code)
	}
	if rec := do(t, h, "GET", "/healthz", "", ""); rec.Code != 200 || decodeBody(t, rec)["version"] != "9.9" {
		t.Fatal("healthz")
	}
}

func TestHTTPDownloadsAndJobs(t *testing.T) {
	c, _ := newCore(t)
	h := c.Handler("")

	rec := do(t, h, "POST", "/v1/downloads", `{"input":"https://x/1"}`, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	id := decodeBody(t, rec)["id"].(string)
	if rec := do(t, h, "POST", "/v1/downloads", `{"input":"https://x/1"}`, ""); rec.Code != 200 {
		t.Fatalf("duplicate must return the existing job with 200, got %d", rec.Code)
	}
	if rec := do(t, h, "POST", "/v1/downloads", `{"input":""}`, ""); rec.Code != 400 {
		t.Fatalf("empty input: %d", rec.Code)
	}
	if rec := do(t, h, "POST", "/v1/downloads", `{"input":"x","extra":1}`, ""); rec.Code != 400 {
		t.Fatalf("unknown fields are rejected: %d", rec.Code)
	}
	if rec := do(t, h, "POST", "/v1/downloads", `nope`, ""); rec.Code != 400 {
		t.Fatalf("bad JSON: %d", rec.Code)
	}
	if rec := do(t, h, "GET", "/v1/jobs", "", ""); rec.Code != 200 || len(decodeBody(t, rec)["jobs"].([]any)) != 1 {
		t.Fatalf("jobs: %s", rec.Body)
	}
	if rec := do(t, h, "GET", "/v1/jobs/"+id, "", ""); rec.Code != 200 {
		t.Fatalf("job: %d", rec.Code)
	}
	if rec := do(t, h, "GET", "/v1/jobs/nope", "", ""); rec.Code != 404 {
		t.Fatalf("unknown job: %d", rec.Code)
	}
	c.Queue = &fakeQueue{err: errors.New("disk full")}
	if rec := do(t, h, "POST", "/v1/downloads", `{"input":"https://x/2"}`, ""); rec.Code != 500 {
		t.Fatalf("queue error: %d", rec.Code)
	}
}

func TestHTTPSubscriptions(t *testing.T) {
	c, _ := newCore(t)
	h := c.Handler("")

	rec := do(t, h, "POST", "/v1/subscriptions", `{"query":"  Some Actor  ","site":"jable"}`, "")
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	sub := decodeBody(t, rec)
	if sub["query"] != "Some Actor" || sub["kind"] != "search" || sub["auto_download"] != true || sub["enabled"] != true {
		t.Fatalf("defaults: %v", sub)
	}
	for _, bad := range []string{`{}`, `{"query":"x","kind":"bogus"}`, `{"query":"x","interval_minutes":-1}`, `bad`} {
		if rec := do(t, h, "POST", "/v1/subscriptions", bad, ""); rec.Code != 400 {
			t.Fatalf("%s: %d", bad, rec.Code)
		}
	}
	off := do(t, h, "POST", "/v1/subscriptions", `{"query":"x","kind":"view","auto_download":false,"interval_minutes":15,"backfill":true}`, "")
	if got := decodeBody(t, off); got["auto_download"] != false || got["interval"].(float64) != float64(15*time.Minute) {
		t.Fatalf("options: %v", got)
	}
	if rec := do(t, h, "GET", "/v1/subscriptions", "", ""); len(decodeBody(t, rec)["subscriptions"].([]any)) != 2 {
		t.Fatal("list")
	}
	id := sub["id"].(string)
	if rec := do(t, h, "POST", "/v1/subscriptions/"+id+"/poll", "", ""); rec.Code != 200 {
		t.Fatalf("poll: %d", rec.Code)
	}
	c.Poller = fakePoller{err: store.ErrNotFound}
	if rec := do(t, h, "POST", "/v1/subscriptions/zzz/poll", "", ""); rec.Code != 404 {
		t.Fatalf("poll unknown: %d", rec.Code)
	}
	c.Poller = nil
	if rec := do(t, h, "POST", "/v1/subscriptions/"+id+"/poll", "", ""); rec.Code != 500 {
		t.Fatalf("poll without monitor: %d", rec.Code)
	}
	if rec := do(t, h, "DELETE", "/v1/subscriptions/"+id, "", ""); rec.Code != 204 {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec := do(t, h, "DELETE", "/v1/subscriptions/"+id, "", ""); rec.Code != 404 {
		t.Fatalf("delete again: %d", rec.Code)
	}
}

func TestAuthentication(t *testing.T) {
	c, _ := newCore(t)
	h := c.Handler("s3cret")
	if rec := do(t, h, "GET", "/v1/jobs", "", ""); rec.Code != 401 || rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("no token: %d", rec.Code)
	}
	if rec := do(t, h, "GET", "/v1/jobs", "", "wrong"); rec.Code != 401 {
		t.Fatalf("wrong token: %d", rec.Code)
	}
	if rec := do(t, h, "GET", "/v1/jobs", "", "s3cret"); rec.Code != 200 {
		t.Fatalf("right token: %d", rec.Code)
	}
	if rec := do(t, h, "GET", "/healthz", "", ""); rec.Code != 200 {
		t.Fatal("health check stays open")
	}
	if rec := do(t, h, "POST", "/mcp", `{}`, ""); rec.Code != 401 {
		t.Fatal("MCP is protected too")
	}
}

func TestHTTPConfigValidate(t *testing.T) {
	cases := []struct {
		cfg HTTPConfig
		ok  bool
	}{
		{HTTPConfig{Addr: "127.0.0.1:8787"}, true},
		{HTTPConfig{Addr: "localhost:1"}, true},
		{HTTPConfig{Addr: "[::1]:1"}, true},
		{HTTPConfig{Addr: "0.0.0.0:8787"}, false},
		{HTTPConfig{Addr: "192.168.1.5:8787"}, false},
		{HTTPConfig{Addr: "0.0.0.0:8787", Token: "t"}, true},
		{HTTPConfig{Addr: "nonsense"}, false},
	}
	for _, tc := range cases {
		if err := tc.cfg.Validate(); (err == nil) != tc.ok {
			t.Errorf("%+v: err=%v, want ok=%v", tc.cfg, err, tc.ok)
		}
	}
}

func TestListenAndServe(t *testing.T) {
	c, _ := newCore(t)
	ctx, cancel := context.WithCancel(context.Background())
	addrc := make(chan string, 1)
	errc := make(chan error, 1)
	go func() { errc <- c.ListenAndServe(ctx, HTTPConfig{Addr: "127.0.0.1:0"}, func(a string) { addrc <- a }) }()
	addr := <-addrc
	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("healthz: %v %v", resp, err)
	}
	_ = resp.Body.Close()
	cancel()
	if err := <-errc; err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := c.ListenAndServe(context.Background(), HTTPConfig{Addr: "0.0.0.0:0"}, nil); err == nil {
		t.Fatal("must refuse an open unauthenticated bind")
	}
	if err := c.ListenAndServe(context.Background(), HTTPConfig{Addr: "127.0.0.1:99999"}, nil); err == nil {
		t.Fatal("bad port")
	}
}

// rpc posts one JSON-RPC message to /mcp and decodes the reply.
func rpc(t *testing.T, h http.Handler, msg string) map[string]any {
	t.Helper()
	rec := do(t, h, "POST", "/mcp", msg, "")
	if rec.Code == http.StatusAccepted {
		return nil
	}
	if rec.Code != 200 {
		t.Fatalf("mcp status %d: %s", rec.Code, rec.Body)
	}
	return decodeBody(t, rec)
}

func toolText(t *testing.T, resp map[string]any) (string, bool) {
	t.Helper()
	result := resp["result"].(map[string]any)
	content := result["content"].([]any)[0].(map[string]any)
	isErr, _ := result["isError"].(bool)
	return content["text"].(string), isErr
}

func TestMCPHandshakeAndToolList(t *testing.T) {
	c, _ := newCore(t)
	h := c.Handler("")

	init := rpc(t, h, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`)
	res := init["result"].(map[string]any)
	if res["protocolVersion"] != "2024-11-05" || res["serverInfo"].(map[string]any)["name"] != "jabledownloader" {
		t.Fatalf("initialize: %v", res)
	}
	if rpc(t, h, `{"jsonrpc":"2.0","method":"notifications/initialized"}`) != nil {
		t.Fatal("notifications get no reply")
	}
	if rpc(t, h, `{"jsonrpc":"2.0","id":2,"method":"ping"}`)["result"] == nil {
		t.Fatal("ping")
	}
	if rpc(t, h, `{"jsonrpc":"2.0","id":3,"method":"initialize"}`)["result"].(map[string]any)["protocolVersion"] != mcpProtocolVersion {
		t.Fatal("default protocol version")
	}
	list := rpc(t, h, `{"jsonrpc":"2.0","id":4,"method":"tools/list"}`)["result"].(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, tl := range list {
		m := tl.(map[string]any)
		names[m["name"].(string)] = true
		if m["description"] == "" || m["inputSchema"] == nil {
			t.Fatalf("tool %v lacks a description or schema", m["name"])
		}
	}
	for _, want := range []string{"search_videos", "latest_videos", "inspect_video", "download_video", "list_downloads",
		"get_download", "subscribe", "list_subscriptions", "unsubscribe", "check_subscription"} {
		if !names[want] {
			t.Errorf("missing tool %s", want)
		}
	}
}

func TestMCPProtocolErrors(t *testing.T) {
	c, _ := newCore(t)
	h := c.Handler("")
	code := func(msg string) float64 {
		return rpc(t, h, msg)["error"].(map[string]any)["code"].(float64)
	}
	if code(`not json`) != errParse {
		t.Fatal("parse error")
	}
	if code(`{"jsonrpc":"1.0","id":1,"method":"x"}`) != errInvalidRequest {
		t.Fatal("invalid request")
	}
	if code(`{"jsonrpc":"2.0","id":1,"method":"nope"}`) != errMethodNotFound {
		t.Fatal("method not found")
	}
	if code(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`) != errInvalidParams {
		t.Fatal("missing tool name")
	}
	if code(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nope"}}`) != errInvalidParams {
		t.Fatal("unknown tool")
	}
	if rec := do(t, h, "POST", "/mcp", `{"jsonrpc":"2.0","method":"x"}`, ""); rec.Code != http.StatusAccepted {
		t.Fatal("notification")
	}
}

func call(t *testing.T, h http.Handler, name, args string) (string, bool) {
	t.Helper()
	msg := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
	return toolText(t, rpc(t, h, msg))
}

func TestMCPToolsEndToEnd(t *testing.T) {
	c, _ := newCore(t)
	h := c.Handler("")

	if text, isErr := call(t, h, "search_videos", `{"keyword":"abc","count":3}`); isErr || !strings.Contains(text, `"A-1"`) {
		t.Fatalf("search: %v %s", isErr, text)
	}
	if text, isErr := call(t, h, "search_videos", `{}`); !isErr || !strings.Contains(text, "keyword") {
		t.Fatalf("search without keyword must be an in-band error: %v %s", isErr, text)
	}
	if _, isErr := call(t, h, "search_videos", `{"keyword":5}`); !isErr {
		t.Fatal("bad argument types are an in-band error")
	}
	if text, _ := call(t, h, "latest_videos", `null`); !strings.Contains(text, "A-1") {
		t.Fatalf("latest: %s", text)
	}
	if text, _ := call(t, h, "inspect_video", `{"input":"A-1"}`); !strings.Contains(text, `"title": "T"`) {
		t.Fatalf("inspect: %s", text)
	}

	text, isErr := call(t, h, "download_video", `{"input":"https://x/1"}`)
	if isErr || !strings.Contains(text, `"created": true`) {
		t.Fatalf("download: %v %s", isErr, text)
	}
	var created struct{ Job struct{ ID string } }
	_ = json.Unmarshal([]byte(text), &created)
	if text, _ := call(t, h, "list_downloads", `{}`); !strings.Contains(text, "https://x/1") {
		t.Fatalf("list_downloads: %s", text)
	}
	if text, isErr := call(t, h, "get_download", `{"id":"`+created.Job.ID+`"}`); isErr || !strings.Contains(text, "queued") {
		t.Fatalf("get_download: %s", text)
	}
	if _, isErr := call(t, h, "get_download", `{"id":"nope"}`); !isErr {
		t.Fatal("unknown job is an in-band error")
	}

	text, isErr = call(t, h, "subscribe", `{"query":"Some Actor","site":"jable"}`)
	if isErr || !strings.Contains(text, "Some Actor") {
		t.Fatalf("subscribe: %v %s", isErr, text)
	}
	var sub struct{ ID string }
	_ = json.Unmarshal([]byte(text), &sub)
	if text, _ := call(t, h, "list_subscriptions", `{}`); !strings.Contains(text, sub.ID) {
		t.Fatalf("list_subscriptions: %s", text)
	}
	if _, isErr := call(t, h, "check_subscription", `{"id":"`+sub.ID+`"}`); isErr {
		t.Fatal("check_subscription")
	}
	if _, isErr := call(t, h, "unsubscribe", `{"id":"`+sub.ID+`"}`); isErr {
		t.Fatal("unsubscribe")
	}
	if _, isErr := call(t, h, "unsubscribe", `{"id":"`+sub.ID+`"}`); !isErr {
		t.Fatal("unsubscribing twice is an in-band error")
	}
	if text, isErr := call(t, h, "subscribe", `{"query":""}`); !isErr || !strings.Contains(text, "query") {
		t.Fatalf("subscribe without a query: %s", text)
	}
}

func TestMCPReportsUnserialisableResults(t *testing.T) {
	c, _ := newCore(t)
	out := c.callTool(context.Background(), tool{Name: "x", run: func(context.Context, *Core, json.RawMessage) (any, error) {
		return make(chan int), nil
	}}, nil)
	if out["isError"] != true {
		t.Fatalf("result = %v", out)
	}
}

func TestServeStdio(t *testing.T) {
	c, _ := newCore(t)
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
		``,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	}, "\n")
	var out bytes.Buffer
	if err := c.ServeStdio(context.Background(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 replies (no reply to the notification), got %d: %q", len(lines), out.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.ServeStdio(ctx, strings.NewReader(""), io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	if err := c.ServeStdio(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"), failWriter{}); err == nil {
		t.Fatal("write errors must surface")
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }
