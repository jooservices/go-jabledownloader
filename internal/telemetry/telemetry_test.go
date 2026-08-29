package telemetry

import (
	"context"
	"testing"
)

func TestZeroConfigIsDisabled(t *testing.T) {
	tel := New(Config{})
	if tel.Enabled() {
		t.Fatal("expected disabled telemetry for empty config")
	}

	// All methods must be safe no-ops when disabled.
	ctx, end := tel.StartSpan(context.Background(), "span")
	end()
	tel.Info(ctx, "info")
	tel.Warn(ctx, "warn")
	tel.Error(ctx, "error")
	tel.Count(ctx, "counter", 1)
	tel.Record(ctx, "histogram", 12.5)
	tel.Shutdown(ctx)
}

func TestUnreachableEndpointStaysFailOpen(t *testing.T) {
	cfg := Config{
		Endpoint: "http://127.0.0.1:1", // nothing listens here
		Org:      "jooservices",
		Stream:   "test",
		User:     "u",
		Password: "p",
	}
	tel := New(cfg)
	if !tel.Enabled() {
		t.Fatal("expected enabled telemetry: exporters are created eagerly")
	}

	// Records must not panic or block even though the endpoint is dead.
	ctx := context.Background()
	ctx, end := tel.StartSpan(ctx, "span")
	tel.Info(ctx, "hello")
	tel.Count(ctx, "counter", 1)
	tel.Record(ctx, "histogram", 1)
	end()
	tel.Shutdown(ctx)
}
