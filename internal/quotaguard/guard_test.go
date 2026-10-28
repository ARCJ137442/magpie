package quotaguard

import (
	"testing"
	"time"
)

func fixture(now time.Time, left string) Entry {
	return Entry{Revision: 1, Policy: Policy{Enabled: true, Rules: []Rule{{Metric: "window:weekly", Unit: "percent", Minimum: "20"}}}, Snapshots: map[string]Snapshot{"windows": {Source: "windows", StartedAt: now, ObservedAt: now, Metrics: []Metric{{ID: "window:weekly", Unit: "percent", Remaining: left}}}}}
}
func TestReserveBoundary(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		left  string
		allow bool
	}{{"20.000001", true}, {"20", false}, {"19.999999", false}, {"0", false}} {
		t.Run(tc.left, func(t *testing.T) {
			if got := Assess(fixture(now, tc.left), true, now).Allowed; got != tc.allow {
				t.Fatalf("allowed=%v want %v", got, tc.allow)
			}
		})
	}
}
func TestAllConfiguredMetricsAreRequired(t *testing.T) {
	now := time.Now()
	e := fixture(now, "50")
	e.Policy.Rules = append(e.Policy.Rules, Rule{Metric: "window:5 hours", Unit: "percent", Minimum: "20"})
	d := Assess(e, true, now)
	if d.Allowed || !d.Unknown {
		t.Fatalf("missing window: %+v", d)
	}
	s := e.Snapshots["windows"]
	s.Metrics = append(s.Metrics, Metric{ID: "window:5 hours", Unit: "percent", Remaining: "10"})
	e.Snapshots["windows"] = s
	d = Assess(e, true, now)
	if d.Allowed || !d.Protected || d.Unknown {
		t.Fatalf("independent window: %+v", d)
	}
}
func TestQuotaDimensionsStayIndependent(t *testing.T) {
	now := time.Now()
	e := fixture(now, "10")
	Latch(&e, now)
	until := now.Add(time.Minute)
	e.Protection.OverrideUntil = &until
	d := Assess(e, false, now)
	if d.Allowed || !d.ManualOff || !d.Protected || !d.OverrideActive {
		t.Fatalf("manual override precedence: %+v", d)
	}
	if !Assess(e, true, now).Allowed {
		t.Fatal("explicit override did not allow known quota")
	}
	if Assess(e, true, until).Allowed {
		t.Fatal("override did not expire at boundary")
	}
	d = Assess(e, true, now.Add(61*time.Second))
	if d.Allowed || !d.Unknown {
		t.Fatal("override cannot bypass stale data")
	}
}
func TestProtectionRemainsAfterRecovery(t *testing.T) {
	now := time.Now()
	e := fixture(now, "10")
	Latch(&e, now)
	e.Snapshots = fixture(now, "90").Snapshots
	if Assess(e, true, now).Allowed {
		t.Fatal("background recovery cleared latch")
	}
}
func TestMissingStaleUnitsAndResetDeny(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name   string
		change func(*Entry)
	}{
		{"missing", func(e *Entry) { e.Snapshots = nil }},
		{"stale", func(e *Entry) {
			s := e.Snapshots["windows"]
			s.ObservedAt = now.Add(-61 * time.Second)
			e.Snapshots["windows"] = s
		}},
		{"future", func(e *Entry) {
			s := e.Snapshots["windows"]
			s.ObservedAt = now.Add(time.Second)
			e.Snapshots["windows"] = s
		}},
		{"unit", func(e *Entry) { e.Policy.Rules[0].Unit = "CNY" }},
		{"reset", func(e *Entry) { s := e.Snapshots["windows"]; s.Metrics[0].ResetAt = &now; e.Snapshots["windows"] = s }},
		{"empty rules", func(e *Entry) { e.Policy.Rules = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := fixture(now, "80")
			tc.change(&e)
			d := Assess(e, true, now)
			if d.Allowed || !d.Unknown {
				t.Fatalf("%+v", d)
			}
		})
	}
}
func TestDecimalMoneyIsNotRounded(t *testing.T) {
	now := time.Now()
	e := fixture(now, "80")
	e.Policy.Rules = []Rule{{Metric: "balance:CNY", Unit: "CNY", Minimum: "10.000000000000000001"}}
	e.Snapshots = map[string]Snapshot{"balance": {Source: "balance", StartedAt: now, ObservedAt: now, Metrics: []Metric{{ID: "balance:CNY", Unit: "CNY", Remaining: "10.000000000000000002"}}}}
	if !Assess(e, true, now).Allowed {
		t.Fatal("decimal amount rounded through float")
	}
	for _, s := range []string{"NaN", "Infinity", "1/2", "1e3", "", "¥10", "10,00"} {
		if _, err := Number(s); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
func TestUnknownAloneDoesNotLatch(t *testing.T) {
	now := time.Now()
	e := fixture(now, "80")
	e.Snapshots = nil
	Latch(&e, now)
	if e.Protection.Tripped {
		t.Fatal("unknown became permanent trip")
	}
}

func TestNearReserveRangeIsUnitAware(t *testing.T) {
	now := time.Now()
	e := fixture(now, "35")
	e.Policy.RefreshMode = RefreshModeNearReserve
	e.Policy.Rules[0].Minimum = "30"
	e.Policy.Rules[0].RefreshWithin = "5"
	if err := e.Policy.Validate(); err != nil {
		t.Fatal(err)
	}
	if d := Assess(e, true, now); !d.Allowed || !d.NearReserve {
		t.Fatalf("exact near-band boundary: %+v", d)
	}
	s := e.Snapshots["windows"]
	s.Metrics[0].Remaining = "35.000001"
	e.Snapshots["windows"] = s
	if d := Assess(e, true, now); !d.Allowed || d.NearReserve {
		t.Fatalf("outside near band: %+v", d)
	}
	s.Metrics[0].Remaining = "30"
	e.Snapshots["windows"] = s
	if d := Assess(e, true, now); d.Allowed || !d.Protected || !d.NearReserve {
		t.Fatalf("at reserve: %+v", d)
	}
}

func TestRefreshModesValidate(t *testing.T) {
	now := time.Now()
	e := fixture(now, "80")
	for _, mode := range []string{"", RefreshModeCached, RefreshModeEveryAttempt} {
		e.Policy.RefreshMode = mode
		if err := e.Policy.Validate(); err != nil {
			t.Errorf("mode %q: %v", mode, err)
		}
	}
	e.Policy.RefreshMode = RefreshModeNearReserve
	if err := e.Policy.Validate(); err == nil {
		t.Fatal("near mode without a range was accepted")
	}
	e.Policy.Rules[0].RefreshWithin = "5"
	if err := e.Policy.Validate(); err != nil {
		t.Fatalf("near mode with range: %v", err)
	}
	e.Policy.RefreshMode = "unknown"
	if err := e.Policy.Validate(); err == nil {
		t.Fatal("unknown refresh mode was accepted")
	}
}
