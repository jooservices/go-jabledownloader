package httpx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSystem struct {
	ips []string
	err error
	n   atomic.Int32
}

func (f *fakeSystem) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	f.n.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	var out []net.IPAddr
	for _, s := range f.ips {
		out = append(out, net.IPAddr{IP: net.ParseIP(s)})
	}
	return out, nil
}

// dohServer answers JSON DNS queries; answers maps "type" to data.
func dohServer(t *testing.T, hits *atomic.Int32, answers map[string]string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		typ := r.URL.Query().Get("type")
		data, ok := answers[typ]
		if !ok {
			_, _ = fmt.Fprint(w, `{"Status":0}`)
			return
		}
		code := 1
		if typ == "AAAA" {
			code = 28
		}
		_, _ = fmt.Fprintf(w, `{"Status":0,"Answer":[{"type":5,"data":"alias."},{"type":%d,"data":%q}]}`, code, data)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func newTestResolver(setting string, sys *fakeSystem, endpoints ...string) *Resolver {
	r := NewResolver(setting)
	r.system = sys
	if len(endpoints) > 0 {
		r.endpoints = endpoints
	}
	return r
}

func TestLookupSystemAnswerIsTrusted(t *testing.T) {
	var hits atomic.Int32
	url := dohServer(t, &hits, map[string]string{"A": "9.9.9.9"})
	sys := &fakeSystem{ips: []string{"203.0.113.5"}}
	ips, err := newTestResolver("", sys, url).Lookup(context.Background(), "example.com")
	if err != nil || len(ips) != 1 || ips[0].String() != "203.0.113.5" {
		t.Fatalf("got %v, %v", ips, err)
	}
	if hits.Load() != 0 {
		t.Fatal("DoH must not be used when the system answer is fine")
	}
}

func TestLookupFallsBackOnSinkhole(t *testing.T) {
	for _, sinkhole := range [][]string{{"127.0.0.1"}, {"0.0.0.0", "::"}} {
		var hits atomic.Int32
		url := dohServer(t, &hits, map[string]string{"A": "104.20.42.172"})
		sys := &fakeSystem{ips: sinkhole}
		ips, err := newTestResolver("auto", sys, url).Lookup(context.Background(), "blocked.example")
		if err != nil || len(ips) != 1 || ips[0].String() != "104.20.42.172" {
			t.Fatalf("%v: got %v, %v", sinkhole, ips, err)
		}
	}
}

func TestLookupFallsBackOnSystemError(t *testing.T) {
	var hits atomic.Int32
	url := dohServer(t, &hits, map[string]string{"A": "198.51.100.7"})
	sys := &fakeSystem{err: errors.New("no such host")}
	ips, err := newTestResolver("", sys, url).Lookup(context.Background(), "x.example")
	if err != nil || ips[0].String() != "198.51.100.7" {
		t.Fatalf("got %v, %v", ips, err)
	}
}

func TestLookupUsesIPv6WhenNoA(t *testing.T) {
	var hits atomic.Int32
	url := dohServer(t, &hits, map[string]string{"AAAA": "2001:db8::1"})
	sys := &fakeSystem{err: errors.New("fail")}
	ips, err := newTestResolver("", sys, url).Lookup(context.Background(), "v6.example")
	if err != nil || ips[0].String() != "2001:db8::1" {
		t.Fatalf("got %v, %v", ips, err)
	}
}

func TestLookupCachesDoH(t *testing.T) {
	var hits atomic.Int32
	url := dohServer(t, &hits, map[string]string{"A": "198.51.100.7"})
	r := newTestResolver("doh", &fakeSystem{}, url)
	now := time.Now()
	r.now = func() time.Time { return now }
	for range 3 {
		if _, err := r.Lookup(context.Background(), "c.example"); err != nil {
			t.Fatal(err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("expected one DoH query, got %d", hits.Load())
	}
	now = now.Add(dohCacheTTL + time.Second)
	if _, err := r.Lookup(context.Background(), "c.example"); err != nil || hits.Load() != 2 {
		t.Fatalf("cache must expire: hits=%d err=%v", hits.Load(), err)
	}
}

func TestLookupOffUsesOnlySystem(t *testing.T) {
	var hits atomic.Int32
	url := dohServer(t, &hits, map[string]string{"A": "198.51.100.7"})
	sys := &fakeSystem{ips: []string{"127.0.0.1"}}
	ips, err := newTestResolver("off", sys, url).Lookup(context.Background(), "blocked.example")
	if err != nil || ips[0].String() != "127.0.0.1" || hits.Load() != 0 {
		t.Fatalf("got %v, %v, hits=%d", ips, err, hits.Load())
	}
}

func TestLookupDoHModeSkipsSystem(t *testing.T) {
	var hits atomic.Int32
	url := dohServer(t, &hits, map[string]string{"A": "198.51.100.7"})
	sys := &fakeSystem{ips: []string{"203.0.113.5"}}
	ips, err := newTestResolver("doh", sys, url).Lookup(context.Background(), "d.example")
	if err != nil || ips[0].String() != "198.51.100.7" || sys.n.Load() != 0 {
		t.Fatalf("got %v, %v, system calls=%d", ips, err, sys.n.Load())
	}
}

func TestLookupLiteralAndLocalhostBypass(t *testing.T) {
	var hits atomic.Int32
	url := dohServer(t, &hits, map[string]string{"A": "198.51.100.7"})
	sys := &fakeSystem{ips: []string{"127.0.0.1"}}
	r := newTestResolver("", sys, url)
	if ips, err := r.Lookup(context.Background(), "127.0.0.1"); err != nil || ips[0].String() != "127.0.0.1" {
		t.Fatalf("literal: %v %v", ips, err)
	}
	for _, h := range []string{"localhost", "app.localhost", "localhost."} {
		if ips, err := r.Lookup(context.Background(), h); err != nil || !ips[0].IsLoopback() {
			t.Fatalf("%s: %v %v", h, ips, err)
		}
	}
	if hits.Load() != 0 {
		t.Fatal("local names must never reach DoH")
	}
}

func TestLookupTriesNextEndpoint(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer bad.Close()
	var hits atomic.Int32
	good := dohServer(t, &hits, map[string]string{"A": "198.51.100.7"})
	r := newTestResolver("doh", &fakeSystem{}, bad.URL, good)
	if ips, err := r.Lookup(context.Background(), "n.example"); err != nil || ips[0].String() != "198.51.100.7" {
		t.Fatalf("got %v, %v", ips, err)
	}
}

func TestLookupAllEndpointsFail(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"Status":3}`)
	}))
	defer bad.Close()
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"Status":0}`)
	}))
	defer empty.Close()
	garbage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `not json`)
	}))
	defer garbage.Close()
	r := newTestResolver("doh", &fakeSystem{}, bad.URL, empty.URL, garbage.URL, "http://127.0.0.1:1/x", "://bad")
	if _, err := r.Lookup(context.Background(), "z.example"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestNewResolverSettings(t *testing.T) {
	if r := NewResolver("off"); r.mode != modeOff {
		t.Fatal("off")
	}
	if r := NewResolver("DoH"); r.mode != modeDoH {
		t.Fatal("doh")
	}
	r := NewResolver(" https://a.example/q , ,https://b.example/q ")
	if r.mode != modeAuto || len(r.endpoints) != 2 || r.endpoints[1] != "https://b.example/q" {
		t.Fatalf("custom endpoints: %+v", r.endpoints)
	}
	if r := NewResolver(" , "); len(r.endpoints) != len(DefaultDoHEndpoints) {
		t.Fatal("empty list keeps defaults")
	}
}

func TestEnvAndSharedResolver(t *testing.T) {
	t.Setenv(DNSEnv, "off")
	if EnvResolver().mode != modeOff {
		t.Fatal("env resolver ignores the variable")
	}
	if SharedResolver() != SharedResolver() {
		t.Fatal("shared resolver must be a singleton")
	}
}

func TestDialContextUsesResolvedAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		if c, err := ln.Accept(); err == nil {
			_ = c.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())

	var hits atomic.Int32
	url := dohServer(t, &hits, map[string]string{"A": "127.0.0.1"})
	r := newTestResolver("doh", &fakeSystem{}, url)
	dial := r.DialContext(&net.Dialer{Timeout: time.Second})
	conn, err := dial(context.Background(), "tcp", net.JoinHostPort("blocked.example", port))
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	if _, err := dial(context.Background(), "tcp", "no-port"); err == nil {
		t.Fatal("expected an address error")
	}
	// Resolves, but nothing listens there: every address fails.
	if _, err := dial(context.Background(), "tcp", net.JoinHostPort("blocked.example", "1")); err == nil {
		t.Fatal("expected a dial error")
	}
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := dial(cctx, "tcp", net.JoinHostPort("blocked.example", port)); err == nil {
		t.Fatal("expected a context error")
	}
	failing := newTestResolver("doh", &fakeSystem{}, "http://127.0.0.1:1/x")
	if _, err := failing.DialContext(&net.Dialer{})(context.Background(), "tcp", "y.example:80"); err == nil {
		t.Fatal("expected a resolve error")
	}
}

func TestRaceDialSkipsDeadAddresses(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	// 192.0.2.1 (TEST-NET-1) is unroutable; the live loopback address must win
	// whatever the shuffle order is.
	ips := []net.IP{net.ParseIP("192.0.2.1"), net.ParseIP("127.0.0.1"), net.ParseIP("192.0.2.2")}
	for range 5 {
		conn, err := raceDial(context.Background(), &net.Dialer{Timeout: time.Second}, "tcp", port, ips)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.Close()
	}
}

func TestRaceDialAllFailAndCancel(t *testing.T) {
	// Closed loopback ports fail immediately, exercising the "next on failure" path.
	ips := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("127.0.0.2")}
	if _, err := raceDial(context.Background(), &net.Dialer{Timeout: time.Second}, "tcp", "1", ips); err == nil {
		t.Fatal("expected failure")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	slow := []net.IP{net.ParseIP("192.0.2.1"), net.ParseIP("192.0.2.2")}
	if _, err := raceDial(ctx, &net.Dialer{Timeout: time.Second}, "tcp", "80", slow); err == nil {
		t.Fatal("expected cancellation")
	}
}
