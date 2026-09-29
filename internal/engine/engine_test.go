package engine

import (
	"context"
	"testing"

	"github.com/jooservices/go-jabledownloader/internal/domain"
)

type fakeEngine struct{}

func (fakeEngine) Download(context.Context, Request, domain.EventSink) (*Result, error) {
	return &Result{}, nil
}

func TestRegistry(t *testing.T) {
	const kind domain.SourceKind = 98
	if _, err := For(kind); err == nil || err.Error() != "no engine for kind 98" {
		t.Fatalf("unregistered For error = %v", err)
	}

	Register(kind, fakeEngine{})
	t.Cleanup(func() { Register(kind, nil) })

	if got, err := For(kind); err != nil || got == nil {
		t.Fatalf("For = %v, %v", got, err)
	}
}

func TestRequestValidate(t *testing.T) {
	valid := Request{Dir: "/out", Code: "abc-1", Source: domain.Source{URL: "https://cdn.test/v"}}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, req := range map[string]Request{
		"no dir":  {Code: "a", Source: domain.Source{URL: "u"}},
		"no url":  {Dir: "/out", Code: "a"},
		"no name": {Dir: "/out", Source: domain.Source{URL: "u"}},
	} {
		if err := req.Validate(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestRequestOutputNameAndWorkers(t *testing.T) {
	req := Request{Code: "abc-1"}
	if got := req.OutputName("h264"); got != "abc-1-h264.mp4" {
		t.Fatal(got)
	}
	if got := req.OutputName(""); got != "abc-1-video.mp4" {
		t.Fatal(got)
	}
	req.FileName = "custom.mp4"
	if got := req.OutputName("h264"); got != "custom.mp4" {
		t.Fatal(got)
	}
	if req.WorkerCount() != 1 {
		t.Fatal(req.WorkerCount())
	}
}

func TestEmitSkipsNilSink(t *testing.T) {
	Emit(nil, domain.Event{})
	var got domain.Event
	Emit(func(ev domain.Event) { got = ev }, domain.Event{Kind: domain.EventDone})
	if got.Kind != domain.EventDone {
		t.Fatal(got)
	}
}
