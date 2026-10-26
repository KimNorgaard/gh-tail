package source

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Transport makes GET requests conditional with ETags. It records X-Poll-Interval into
// the request context's PollInterval, if any.
// A 304 is answered from the cache as a 200, so callers see an unchanged body and
// rely on their own dedup. GitHub doesn't count a 304 against the rate limit.
//
// ponytail: one cache entry per URL for the process lifetime, add eviction if that grows.
type Transport struct {
	next     http.RoundTripper
	mu       sync.Mutex
	cache    map[string]cachedResponse
	requests atomic.Int64
	cached   atomic.Int64
}

type cachedResponse struct {
	etag   string
	header http.Header
	body   []byte
}

func NewTransport(next http.RoundTripper) *Transport {
	return &Transport{next: next, cache: map[string]cachedResponse{}}
}

// PollInterval holds the largest X-Poll-Interval that requests made with its context
// received. Endpoints differ, such as 60 seconds for events and 300 for refs, so each
// source gets its own.
type PollInterval struct{ seconds atomic.Int64 }

func (p *PollInterval) Get() time.Duration { return time.Duration(p.seconds.Load()) * time.Second }

type pollIntervalKey struct{}

// WithPollInterval returns a context whose requests record into p.
func WithPollInterval(ctx context.Context, p *PollInterval) context.Context {
	return context.WithValue(ctx, pollIntervalKey{}, p)
}

// Stats returns the number of requests made and how many of them the cache answered.
func (t *Transport) Stats() (requests, cached int64) {
	return t.requests.Load(), t.cached.Load()
}

func (t *Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.requests.Add(1)
	if r.Method != http.MethodGet {
		return t.next.RoundTrip(r)
	}

	key := r.URL.String()
	t.mu.Lock()
	cached, ok := t.cache[key]
	t.mu.Unlock()
	if ok {
		r = r.Clone(r.Context())
		r.Header.Set("If-None-Match", cached.etag)
	}

	resp, err := t.next.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	if p, ok := r.Context().Value(pollIntervalKey{}).(*PollInterval); ok {
		if s, err := strconv.ParseInt(resp.Header.Get("X-Poll-Interval"), 10, 64); err == nil && s > p.seconds.Load() {
			p.seconds.Store(s)
		}
	}

	if ok && resp.StatusCode == http.StatusNotModified {
		t.cached.Add(1)
		_ = resp.Body.Close()
		return &http.Response{
			Status:        "200 OK",
			StatusCode:    http.StatusOK,
			Proto:         resp.Proto,
			ProtoMajor:    resp.ProtoMajor,
			ProtoMinor:    resp.ProtoMinor,
			Header:        cached.header.Clone(),
			Body:          io.NopCloser(bytes.NewReader(cached.body)),
			ContentLength: int64(len(cached.body)),
			Request:       r,
		}, nil
	}

	etag := resp.Header.Get("ETag")
	if resp.StatusCode != http.StatusOK || etag == "" {
		return resp, nil
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.cache[key] = cachedResponse{etag: etag, header: resp.Header.Clone(), body: body}
	t.mu.Unlock()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}
