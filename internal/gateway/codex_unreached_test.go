package gateway

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

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
