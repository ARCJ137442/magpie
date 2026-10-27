package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yetone/magpie/internal/quotaguard"
)

func TestGuardBalanceReadsOriginalDecimal(t *testing.T) {
	p := Provider{Chat: "https://api.deepseek.com/v1"}
	ms, err := guardBalanceMetrics(p, []byte(`{"balance_infos":[{"currency":"CNY","total_balance":"9.999999999999999999"}]}`), quotaguard.Policy{})
	if err != nil || len(ms) != 1 || ms[0].Remaining != "9.999999999999999999" || ms[0].Scope != "account" {
		t.Fatalf("%+v %v", ms, err)
	}
}
func TestGuardUnknownBalanceIsNotZero(t *testing.T) {
	p := Provider{Chat: "https://api.deepseek.com/v1"}
	if _, err := guardBalanceMetrics(p, []byte(`{"balance_infos":[{"currency":"CNY"}]}`), quotaguard.Policy{}); err == nil {
		t.Fatal("missing balance read as zero")
	}
}
func TestGuardCustomMappingAndCurrency(t *testing.T) {
	p := Provider{Chat: "https://api.deepseek.com/v1", BalanceURL: "https://example.invalid/balance"}
	b := []byte(`{"data":{"wallet":"10.01"}}`)
	ms, err := guardBalanceMetrics(p, b, quotaguard.Policy{})
	if err != nil || len(ms) != 0 {
		t.Fatal("custom source inherited official schema")
	}
	ms, err = guardBalanceMetrics(p, b, quotaguard.Policy{BalancePointer: "/data/wallet", BalanceUnit: "credits"})
	if err != nil || len(ms) != 1 || ms[0].Unit != "credits" || ms[0].Remaining != "10.01" {
		t.Fatalf("%+v %v", ms, err)
	}
}
func TestGuardWindowUsesActualNames(t *testing.T) {
	ms := guardWindowMetrics([]QuotaWindow{{Name: "5 hours", Used: 80}, {Name: "weekly", Used: 10}, {Name: "aside", Used: 100, Aside: true}, {Name: "unknown", Text: true}, {Name: "free", Unlimited: true}})
	if len(ms) != 2 || ms[0].ID != "window:5 hours" || ms[0].Remaining != "20" {
		t.Fatalf("%+v", ms)
	}
}

func TestGuardOldProviderCannotRefreshChangedSource(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	serveBalance := func(left string) *httptest.Server {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"data":{"balance":%q}}`, left)
		}))
		t.Cleanup(s.Close)
		return s
	}
	oldSource, currentSource := serveBalance("50"), serveBalance("5")
	p := Provider{ID: "guard-source", Key: "fixture-only", Chat: oldSource.URL + "/v1", BalanceURL: oldSource.URL + "/balance", BalancePath: "$data.balance"}
	if err := Save(p); err != nil {
		t.Fatal(err)
	}
	policy := quotaguard.Policy{Enabled: true, Rules: []quotaguard.Rule{{Metric: "balance:custom", Unit: "CNY", Minimum: "10"}}, BalancePointer: "/data/balance", BalanceUnit: "CNY"}
	if _, err := ConfigureQuotaGuard(p, policy, 0); err != nil {
		t.Fatal(err)
	}
	current := p
	current.BalanceURL = currentSource.URL + "/balance"
	if err := Save(current); err != nil {
		t.Fatal(err)
	}
	// A queued/background reader may still hold the previous provider value.
	if _, err := RefreshQuotaGuard(context.Background(), p, nil); err != nil {
		t.Fatal(err)
	}
	view, err := QuotaGuardOf(current)
	if err != nil {
		t.Fatal(err)
	}
	if view.Decision.Allowed {
		t.Fatal("the previous balance source authorized the changed source")
	}
}
