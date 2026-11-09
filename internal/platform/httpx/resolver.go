package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// DNSEnv selects how host names are resolved. Values:
//
//	""/"auto"  system DNS first; DNS-over-HTTPS when the system answer is empty,
//	           fails, or points at a loopback/unspecified address (a sinkhole)
//	"off"      system DNS only
//	"doh"      DNS-over-HTTPS only
//	<urls>     comma-separated DoH JSON endpoints, used in "auto" mode
const DNSEnv = "JABLEDOWNLOADER_DNS"

// DefaultDoHEndpoints are public DNS-over-HTTPS JSON endpoints addressed by IP,
// so reaching them needs no DNS lookup of their own.
var DefaultDoHEndpoints = []string{
	"https://1.1.1.1/dns-query",
	"https://8.8.8.8/resolve",
	"https://9.9.9.9:5053/dns-query",
}

type dnsMode int

const (
	modeAuto dnsMode = iota
	modeOff
	modeDoH
)

const dohCacheTTL = 5 * time.Minute

// ipLookup is the system resolver contract (net.Resolver implements it).
type ipLookup interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// Resolver resolves host names, falling back to DNS-over-HTTPS when the system
// resolver is blocked or lying. It is safe for concurrent use.
type Resolver struct {
	mode      dnsMode
	system    ipLookup
	endpoints []string
	client    *http.Client

	mu    sync.Mutex
	cache map[string]cacheEntry
	now   func() time.Time
}

type cacheEntry struct {
	ips     []net.IP
	expires time.Time
}

// NewResolver builds a Resolver from a DNSEnv-style setting.
func NewResolver(setting string) *Resolver {
	r := &Resolver{
		system:    net.DefaultResolver,
		endpoints: DefaultDoHEndpoints,
		client:    &http.Client{Timeout: 8 * time.Second},
		cache:     map[string]cacheEntry{},
		now:       time.Now,
	}
	switch s := strings.TrimSpace(setting); strings.ToLower(s) {
	case "", "auto":
	case "off":
		r.mode = modeOff
	case "doh":
		r.mode = modeDoH
	default:
		if urls := splitList(s); len(urls) > 0 {
			r.endpoints = urls
		}
	}
	return r
}

// EnvResolver is NewResolver configured from the DNSEnv variable.
func EnvResolver() *Resolver { return NewResolver(os.Getenv(DNSEnv)) }

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Lookup returns the addresses for host. IP literals and localhost names are
// returned (or resolved by the system) untouched.
func (r *Resolver) Lookup(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	if isLocalName(host) || r.mode == modeOff {
		return r.systemLookup(ctx, host)
	}
	if r.mode == modeAuto {
		ips, err := r.systemLookup(ctx, host)
		if err == nil && len(ips) > 0 && !sinkholed(ips) {
			return ips, nil
		}
	}
	ips, err := r.dohLookup(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", host, err)
	}
	return ips, nil
}

func (r *Resolver) systemLookup(ctx context.Context, host string) ([]net.IP, error) {
	addrs, err := r.system.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		ips = append(ips, a.IP)
	}
	return ips, nil
}

func isLocalName(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	return h == "localhost" || strings.HasSuffix(h, ".localhost")
}

// sinkholed reports an answer made only of loopback or unspecified addresses,
// which is how DNS filters block a real site.
func sinkholed(ips []net.IP) bool {
	for _, ip := range ips {
		if !ip.IsLoopback() && !ip.IsUnspecified() {
			return false
		}
	}
	return true
}

func (r *Resolver) dohLookup(ctx context.Context, host string) ([]net.IP, error) {
	key := strings.ToLower(host)
	r.mu.Lock()
	if e, ok := r.cache[key]; ok && r.now().Before(e.expires) {
		r.mu.Unlock()
		return e.ips, nil
	}
	r.mu.Unlock()

	var errs []error
	for _, endpoint := range r.endpoints {
		var ips []net.IP
		var err error
		for _, qtype := range []string{"A", "AAAA"} {
			var got []net.IP
			got, err = r.query(ctx, endpoint, host, qtype)
			if err != nil {
				break
			}
			ips = append(ips, got...)
			if len(ips) > 0 {
				break
			}
		}
		if err == nil && len(ips) > 0 {
			r.mu.Lock()
			r.cache[key] = cacheEntry{ips: ips, expires: r.now().Add(dohCacheTTL)}
			r.mu.Unlock()
			return ips, nil
		}
		if err == nil {
			err = errors.New("no records")
		}
		errs = append(errs, fmt.Errorf("%s: %w", endpoint, err))
	}
	return nil, errors.Join(errs...)
}

type dohAnswer struct {
	Status int `json:"Status"`
	Answer []struct {
		Type int    `json:"type"`
		Data string `json:"data"`
	} `json:"Answer"`
}

func (r *Resolver) query(ctx context.Context, endpoint, host, qtype string) ([]net.IP, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("name", host)
	q.Set("type", qtype)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/dns-json")
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var out dohAnswer
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, err
	}
	if out.Status != 0 {
		return nil, fmt.Errorf("dns status %d", out.Status)
	}
	var ips []net.IP
	for _, a := range out.Answer {
		if a.Type != 1 && a.Type != 28 { // A, AAAA (skip CNAME hops)
			continue
		}
		if ip := net.ParseIP(a.Data); ip != nil {
			ips = append(ips, ip)
		}
	}
	return ips, nil
}

// DialContext dials addr ("host:port") using the resolver, trying every
// address in turn. The original host name is kept for TLS by the caller.
func (r *Resolver) DialContext(dialer *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := r.Lookup(ctx, host)
		if err != nil {
			return nil, err
		}
		return raceDial(ctx, dialer, network, port, ips)
	}
}

var (
	sharedOnce     sync.Once
	sharedResolver *Resolver
)

// SharedResolver returns the process-wide resolver (and DoH cache) configured
// from the DNSEnv variable on first use.
func SharedResolver() *Resolver {
	sharedOnce.Do(func() { sharedResolver = EnvResolver() })
	return sharedResolver
}

// Overridden reports the DNS-over-HTTPS addresses for host when the system
// resolver is blocked, failing, or sinkholed for it. ok is false when system
// DNS is fine (or DoH is off), so callers can keep using the system.
func (r *Resolver) Overridden(ctx context.Context, host string) (ips []net.IP, ok bool) {
	if net.ParseIP(host) != nil || isLocalName(host) || r.mode == modeOff {
		return nil, false
	}
	if r.mode == modeAuto {
		if sys, err := r.systemLookup(ctx, host); err == nil && len(sys) > 0 && !sinkholed(sys) {
			return nil, false
		}
	}
	ips, err := r.dohLookup(ctx, host)
	return ips, err == nil && len(ips) > 0
}

const (
	// raceStagger is the delay before the next address is tried in parallel.
	raceStagger = 300 * time.Millisecond
	// attemptTimeout caps one TCP connect when several addresses are available,
	// so a dead address in a round-robin set does not stall the download.
	attemptTimeout = 6 * time.Second
)

// raceDial connects to the first address that answers. Addresses start in a
// shuffled order, staggered, so dead members of a DNS round-robin set cost
// milliseconds instead of a full timeout.
func raceDial(ctx context.Context, dialer *net.Dialer, network, port string, ips []net.IP) (net.Conn, error) {
	if len(ips) == 1 {
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
	order := make([]net.IP, len(ips))
	copy(order, ips)
	rand.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })

	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan dialResult, len(order))
	started := 0
	start := func(ip net.IP) {
		started++
		go func() {
			attemptCtx, stop := context.WithTimeout(raceCtx, attemptTimeout)
			defer stop()
			conn, err := dialer.DialContext(attemptCtx, network, net.JoinHostPort(ip.String(), port))
			results <- dialResult{conn, err}
		}()
	}
	var errs []error
	start(order[0])
	next, pending := 1, 1
	timer := time.NewTimer(raceStagger)
	defer timer.Stop()
	for pending > 0 {
		select {
		case res := <-results:
			pending--
			if res.err == nil {
				cancel()
				go drainDial(results, pending)
				return res.conn, nil
			}
			errs = append(errs, res.err)
			if next < len(order) { // a failure frees the next address at once
				start(order[next])
				next, pending = next+1, pending+1
			}
		case <-timer.C:
			if next < len(order) {
				start(order[next])
				next, pending = next+1, pending+1
				timer.Reset(raceStagger)
			}
		case <-ctx.Done():
			cancel()
			go drainDial(results, pending)
			return nil, ctx.Err()
		}
	}
	return nil, errors.Join(errs...)
}

// drainDial closes connections from losing attempts.
type dialResult struct {
	conn net.Conn
	err  error
}

func drainDial(results <-chan dialResult, pending int) {
	for range pending {
		if res := <-results; res.conn != nil {
			_ = res.conn.Close()
		}
	}
}
