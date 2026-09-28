package telemetry

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func TestZeroConfigIsDisabled(t *testing.T) {
	tel, err := New(Config{})
	if err != nil || tel.Enabled() {
		t.Fatalf("enabled=%v err=%v", tel.Enabled(), err)
	}
	// All methods must be safe no-ops when disabled, including on nil.
	for _, handle := range []*T{tel, nil} {
		ctx, end := handle.StartSpan(context.Background(), "span")
		end()
		handle.Info(ctx, "info")
		handle.Warn(ctx, "warn")
		handle.Error(ctx, "error")
		handle.Count(ctx, "counter", 1)
		handle.Record(ctx, "histogram", 12.5)
		handle.Shutdown(ctx)
	}
}

func TestCheckEndpoint(t *testing.T) {
	for _, tc := range []struct {
		endpoint string
		creds    bool
		wantErr  string
	}{
		{"https://obs.example.test", true, ""},
		{"http://127.0.0.1:5080", true, ""},
		{"http://localhost:5080", true, ""},
		{"http://[::1]:5080", true, ""},
		{"http://obs.example.test", false, ""},
		{"http://obs.example.test", true, "plain HTTP"},
		{"ftp://obs.example.test", false, "invalid"},
		{"obs.example.test", false, "invalid"},
	} {
		err := checkEndpoint(tc.endpoint, tc.creds)
		if (tc.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.wantErr)) {
			t.Errorf("checkEndpoint(%q, %v) = %v", tc.endpoint, tc.creds, err)
		}
	}
}

func TestCredentialsOverRemoteHTTPDisableTelemetry(t *testing.T) {
	tel, err := New(Config{Endpoint: "http://obs.example.test", User: "u", Password: "p"})
	if err == nil || tel.Enabled() {
		t.Fatalf("enabled=%v err=%v", tel.Enabled(), err)
	}
}

func TestUnreachableEndpointStaysFailOpen(t *testing.T) {
	tel, err := New(Config{Endpoint: "http://127.0.0.1:1", Org: "jooservices", Stream: "test", User: "u", Password: "p", Version: "v1.2.3"})
	if err != nil || !tel.Enabled() {
		t.Fatalf("enabled=%v err=%v", tel.Enabled(), err)
	}
	ctx, end := tel.StartSpan(context.Background(), "span")
	tel.Info(ctx, "hello")
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tel.Count(ctx, "counter", 1)
			tel.Record(ctx, "histogram", 1)
		}()
	}
	wg.Wait()
	end()
	tel.Shutdown(ctx)
}
