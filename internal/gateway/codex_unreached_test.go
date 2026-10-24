package gateway

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

// One of Codex's own models, when OpenAI can't be reached, fails saying so
// and what to do, not with the dial error alone (#322).
func TestCodexOwnModelUnreached(t *testing.T) {
	setup(t, provider.Chat, &fake{t: t})
	// port 1: refused at once, and no test can listen on it. A closed
	// server's port was handed to another test's server on Linux CI, whose
	// fake answered "pong" with 200.
	was := codexAPIBase
	codexAPIBase = "http://127.0.0.1:1"
	t.Cleanup(func() { codexAPIBase = was })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", CodexPath+"/responses", strings.NewReader(`{"model":"gpt-5.5","input":"hi","stream":true}`))
	req.Header.Set("Authorization", "Bearer sk-relay-key")
	New().Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != 502 || !strings.Contains(body, "OpenAI can't be reached") || !strings.Contains(body, "gpt-5.5 is one of Codex's own models") ||
		!strings.Contains(body, "pick one of magpie's models") {
		t.Fatalf("%d %s", rec.Code, body)
	}
}

func TestCodexTransportInterrupted(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "EOF", err: io.EOF, want: true},
		{name: "unexpected EOF", err: io.ErrUnexpectedEOF, want: true},
		{name: "timeout", err: os.ErrDeadlineExceeded, want: true},
		{name: "wrapped connection timeout", err: &url.Error{Op: "Post", Err: &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}}, want: true},
		{name: "wrapped request timeout", err: &url.Error{Op: "Post", Err: os.ErrDeadlineExceeded}, want: true},
		{name: "network operation", err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection reset")}, want: true},
		{name: "temporary DNS", err: &net.DNSError{IsTemporary: true}, want: true},
		{name: "permanent DNS", err: &net.DNSError{Err: "no such host"}},
		{name: "local configuration", err: errors.New("certificate signed by unknown authority")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := codexTransportInterrupted(tc.err); got != tc.want {
				t.Fatalf("codexTransportInterrupted(%v) = %t, want %t", tc.err, got, tc.want)
			}
		})
	}
}

func TestCodexTransportUnsent(t *testing.T) {
	target := "https://chatgpt.com/backend-api/codex/responses"
	for _, tc := range []struct {
		name string
		err  error
		url  string
		sent bool
		want bool
	}{
		{name: "dial failed", url: target, err: &url.Error{Op: "Post", URL: target, Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}, want: true},
		{name: "temporary DNS", url: target, err: &url.Error{Op: "Post", URL: target, Err: &net.DNSError{IsTemporary: true}}, want: true},
		{name: "proxy CONNECT refused", url: target, err: &url.Error{Op: "Post", URL: target, Err: errors.New("Bad Gateway")}, want: true},
		{name: "proxy CONNECT refused after origin request", url: target, err: &url.Error{Op: "Post", URL: target, Err: errors.New("Bad Gateway")}, sent: true},
		{name: "EOF before request headers", url: target, err: &url.Error{Op: "Post", URL: target, Err: io.EOF}, want: true},
		{name: "EOF after request headers", url: target, err: &url.Error{Op: "Post", URL: target, Err: io.EOF}, sent: true},
		{name: "permanent DNS", url: target, err: &url.Error{Op: "Post", URL: target, Err: &net.DNSError{Err: "no such host"}}},
		{name: "read timeout after request", url: target, err: &url.Error{Op: "Post", URL: target, Err: &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}}, sent: true},
		{name: "response header timeout", url: target, err: &url.Error{Op: "Post", URL: target, Err: os.ErrDeadlineExceeded}, sent: true},
		{name: "redirect target dial failed", url: target, err: &url.Error{Op: "Post", URL: "https://other.example/responses", Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := codexTransportUnsent(tc.err, tc.url, tc.sent); got != tc.want {
				t.Fatalf("codexTransportUnsent(%v, %q, sent=%t) = %t, want %t", tc.err, tc.url, tc.sent, got, tc.want)
			}
		})
	}
}

func TestCodexTransportRetryWaitIsBounded(t *testing.T) {
	old := retryPause
	retryPause = time.Second
	t.Cleanup(func() { retryPause = old })
	first, retry := codexTransportRetryWait(0, 0)
	if !retry || first < 750*time.Millisecond || first > time.Second {
		t.Fatalf("first retry wait = %s, retry=%t", first, retry)
	}
	last, retry := codexTransportRetryWait(5, 0)
	if !retry || last < 22*time.Second || last > 30*time.Second {
		t.Fatalf("capped retry wait = %s, retry=%t", last, retry)
	}
	remaining, retry := codexTransportRetryWait(5, codexTransportRetryWindow-250*time.Millisecond)
	if !retry || remaining != 250*time.Millisecond {
		t.Fatalf("retry wait at the deadline = %s, retry=%t", remaining, retry)
	}
	if wait, retry := codexTransportRetryWait(5, codexTransportRetryWindow); retry || wait != 0 {
		t.Fatalf("retry after the window = %s, retry=%t", wait, retry)
	}
}

func TestCodexProxyConnectRefusalIsUnsent(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			t.Errorf("proxy got %s, want CONNECT", r.Method)
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	target := "https://chatgpt.com/backend-api/codex/responses"
	var wroteHeaders atomic.Bool
	ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{WroteHeaders: func() { wroteHeaders.Store(true) }})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(`{"model":"gpt-5.5","input":"hi","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(req)
	if err == nil {
		t.Fatal("proxy CONNECT refusal unexpectedly succeeded")
	}
	var requestErr *url.Error
	if !errors.As(err, &requestErr) || requestErr.URL != target {
		t.Fatalf("proxy CONNECT error = %#v, want url.Error for %q", err, target)
	}
	if wroteHeaders.Load() {
		t.Fatal("CONNECT was rejected after the origin request headers were sent")
	}
	if !codexTransportUnsent(err, target, wroteHeaders.Load()) {
		t.Fatalf("proxy CONNECT error should be safe to retry: %v", err)
	}
}

func TestCodexProxyConnectEOFIsUnsent(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			t.Errorf("proxy got %s, want CONNECT", r.Method)
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack proxy connection: %v", err)
			return
		}
		conn.Close()
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	target := "https://chatgpt.com/backend-api/codex/responses"
	var wroteHeaders atomic.Bool
	ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{WroteHeaders: func() { wroteHeaders.Store(true) }})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(`{"model":"gpt-5.5","input":"hi","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(req)
	if err == nil {
		t.Fatal("proxy EOF unexpectedly succeeded")
	}
	if wroteHeaders.Load() {
		t.Fatal("proxy EOF occurred after the origin request headers were sent")
	}
	if !codexTransportUnsent(err, target, wroteHeaders.Load()) {
		t.Fatalf("proxy CONNECT EOF should be safe to retry: %v", err)
	}
}

func TestCodexOriginEOFIsNotUnsent(t *testing.T) {
	var calls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack origin connection: %v", err)
			return
		}
		conn.Close()
	}))
	defer origin.Close()
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	var wroteHeaders atomic.Bool
	ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{WroteHeaders: func() { wroteHeaders.Store(true) }})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, origin.URL, strings.NewReader(`{"model":"gpt-5.5","input":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(req)
	if err == nil {
		t.Fatal("origin EOF unexpectedly succeeded")
	}
	if !wroteHeaders.Load() {
		t.Fatal("origin received the POST without the client reporting WroteHeaders")
	}
	if codexTransportUnsent(err, req.URL.String(), wroteHeaders.Load()) {
		t.Fatalf("origin EOF after the POST was sent must not be replayed: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("origin received %d POSTs after one ambiguous EOF, want 1", got)
	}
}

func TestCodexNativeTurnRetriesSafeConnectFailure(t *testing.T) {
	f := &fake{t: t, reply: sse(
		"event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\",\"model\":\"gpt-5.5\"}}",
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1},\"output\":[]}}",
	)}
	up := setup(t, provider.Responses, f)
	oldBase, oldPause := provider.CodexBase, retryPause
	provider.CodexBase, retryPause = up.URL, time.Millisecond
	t.Cleanup(func() { provider.CodexBase, retryPause = oldBase, oldPause })
	attempts := 0
	s := New()
	s.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return nil, &url.Error{Op: req.Method, URL: req.URL.String(), Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}
		}
		return http.DefaultTransport.RoundTrip(req)
	})}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", CodexPath+"/responses", strings.NewReader(`{"model":"gpt-5.5","input":"hi","stream":true}`))
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || attempts != 2 || f.calls != 1 || f.path != "/responses" || !strings.Contains(rec.Body.String(), "response.completed") {
		t.Fatalf("status=%d attempts=%d upstream_calls=%d path=%q body=%s", rec.Code, attempts, f.calls, f.path, rec.Body.String())
	}
}
