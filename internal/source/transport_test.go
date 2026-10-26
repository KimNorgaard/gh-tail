package source

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTransportConditionalRequests(t *testing.T) {
	var conditional int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Poll-Interval", "60")
		if r.Header.Get("If-None-Match") == `"v1"` {
			conditional++
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		_, _ = io.WriteString(w, "body")
	}))
	defer srv.Close()

	tr := NewTransport(http.DefaultTransport)
	client := &http.Client{Transport: tr}
	var interval PollInterval
	ctx := WithPollInterval(context.Background(), &interval)
	for i := range 3 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || string(body) != "body" {
			t.Errorf("request %d: got %d %q, want 200 \"body\"", i, resp.StatusCode, body)
		}
	}
	if conditional != 2 {
		t.Errorf("got %d conditional requests, want 2", conditional)
	}
	if interval.Get() != time.Minute {
		t.Errorf("got poll interval %v, want 1m", interval.Get())
	}
}
