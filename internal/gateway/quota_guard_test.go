package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/quotaguard"
)

type guardCalls struct {
	primary atomic.Int64
	spare   atomic.Int64
}

func (c *guardCalls) total() int64 { return c.primary.Load() + c.spare.Load() }

// All responses are local fixtures. No key, vendor or billable service is used.
func guardFixture(t *testing.T) (provider.Provider, *guardCalls) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	count := new(guardCalls)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/balance" {
			if strings.Contains(r.Header.Get("Authorization"), "spare") {
				fmt.Fprint(w, `{"data":{"balance":"50"}}`)
			} else {
				fmt.Fprint(w, `{"data":{"balance":"9.99"}}`)
			}
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		switch r.Header.Get("Authorization") {
		case "Bearer fixture-only-primary":
			count.primary.Add(1)
		case "Bearer fixture-only-spare":
			count.spare.Add(1)
		default:
			t.Errorf("model request used an unexpected credential")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"id":"fixture","object":"chat.completion","model":"mock-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	t.Cleanup(upstream.Close)
	p := provider.Provider{ID: "quota-fixture", Name: "Quota fixture", Chat: upstream.URL + "/v1", Key: "fixture-only-primary", Models: []string{"mock-model"}, Routing: "order", BalanceURL: upstream.URL + "/balance", BalancePath: "$data.balance"}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	configureGuard(t, p, "10")
	return p, count
}

func configureGuard(t *testing.T, p provider.Provider, minimum string) {
	t.Helper()
	policy := quotaguard.Policy{Enabled: true, Rules: []quotaguard.Rule{{Metric: "balance:custom", Unit: "CNY", Minimum: minimum}}, BalancePointer: "/data/balance", BalanceUnit: "CNY"}
	if _, err := provider.ConfigureQuotaGuard(p, policy, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.RefreshQuotaGuard(context.Background(), p, nil); err != nil {
		t.Fatal(err)
	}
}

func guardSpare(t *testing.T, p provider.Provider, minimum string) provider.Provider {
	t.Helper()
	if err := provider.AddKey(p.ID, "spare", "fixture-only-spare", ""); err != nil {
		t.Fatal(err)
	}
	spare, err := provider.GuardCredential(p.ID, provider.KeyID("fixture-only-spare"))
	if err != nil {
		t.Fatal(err)
	}
	configureGuard(t, spare, minimum)
	return spare
}
func postGuard(t *testing.T, srv *Server, p provider.Provider) *httptest.ResponseRecorder {
	t.Helper()
	body := []byte(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"stream":false}`, p.ID+"/mock-model"))
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.serve(w, r, provider.Chat, body)
	return w
}
func TestQuotaGuardLastKeyNeverCallsUpstream(t *testing.T) {
	p, count := guardFixture(t)
	w := postGuard(t, New(), p)
	if count.total() != 0 || w.Code != 403 || !strings.Contains(w.Body.String(), "upstream_quota_protected") {
		t.Fatalf("calls=%d status=%d body=%s", count.total(), w.Code, w.Body.String())
	}
	if w.Header().Get("Retry-After") != "" {
		t.Fatal("human latch advertised automatic recovery")
	}
}
func TestQuotaGuardRoutesOnlyToSpareAboveReserve(t *testing.T) {
	p, count := guardFixture(t)
	guardSpare(t, p, "10")
	w := postGuard(t, New(), p)
	if count.primary.Load() != 0 || count.spare.Load() != 1 || w.Code != 200 {
		t.Fatalf("primary=%d spare=%d status=%d body=%s", count.primary.Load(), count.spare.Load(), w.Code, w.Body.String())
	}
}

func TestQuotaGuardAllKeysProtectedNeverCallsUpstream(t *testing.T) {
	p, count := guardFixture(t)
	guardSpare(t, p, "50")
	w := postGuard(t, New(), p)
	if count.total() != 0 || w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "upstream_quota_protected") {
		t.Fatalf("primary=%d spare=%d status=%d body=%s", count.primary.Load(), count.spare.Load(), w.Code, w.Body.String())
	}
}
func TestQuotaGuardProviderDoCannotProbeHeldKey(t *testing.T) {
	p, count := guardFixture(t)
	req := httptest.NewRequest(http.MethodPost, p.Chat+"/chat/completions", strings.NewReader(`{}`))
	// A client request, rather than a server-side Request with RequestURI set.
	req.RequestURI = ""
	req.Header.Set("Authorization", "Bearer "+p.Key)
	if _, err := p.Do(http.DefaultClient, req); err == nil {
		t.Fatal("last-key probe was allowed")
	}
	if count.total() != 0 {
		t.Fatal("local refusal reached vendor")
	}
}
