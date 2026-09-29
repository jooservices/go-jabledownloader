package httpx

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultIdleTimeout is how long a media body may stay silent before the
// request is aborted.
const DefaultIdleTimeout = 60 * time.Second

// ErrIdleTimeout reports a response body that stopped delivering data.
var ErrIdleTimeout = errors.New("idle timeout: no data received")

// Do sends req and guards the response body with an idle timeout: when no
// byte arrives for idle, the request is cancelled and reads return
// ErrIdleTimeout. The caller must close resp.Body as usual. A non-positive
// idle uses DefaultIdleTimeout.
func Do(client *http.Client, req *http.Request, idle time.Duration) (*http.Response, error) {
	if idle <= 0 {
		idle = DefaultIdleTimeout
	}
	ctx, cancel := context.WithCancel(req.Context())
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = newIdleBody(resp.Body, idle, cancel)
	return resp, nil
}

// idleBody resets a timer on every read that returns data. The timer, not a
// goroutine per read, cancels the request once the stream goes quiet.
type idleBody struct {
	body    io.ReadCloser
	idle    time.Duration
	cancel  context.CancelFunc
	timer   *time.Timer
	expired atomic.Bool
	once    sync.Once
}

func newIdleBody(body io.ReadCloser, idle time.Duration, cancel context.CancelFunc) *idleBody {
	b := &idleBody{body: body, idle: idle, cancel: cancel}
	b.timer = time.AfterFunc(idle, func() {
		b.expired.Store(true)
		cancel()
	})
	return b
}

func (b *idleBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if n > 0 && !b.expired.Load() {
		b.timer.Reset(b.idle)
	}
	if err != nil && err != io.EOF && b.expired.Load() {
		err = ErrIdleTimeout
	}
	return n, err
}

func (b *idleBody) Close() error {
	var err error
	b.once.Do(func() {
		b.timer.Stop()
		err = b.body.Close()
		b.cancel()
	})
	return err
}
