package quotaguard

import (
	"testing"
	"time"
)

func fixture(now time.Time, left string) Entry {
	return Entry{Revision: 1, Policy: Policy{Enabled: true, Rules: []Rule{{Metric: "window:weekly", Unit: "percent", Minimum: "20"}}}, Snapshots: map[string]Snapshot{"windows": {Source: "windows", StartedAt: now, ObservedAt: now, Metrics: []Metric{{ID: "window:weekly", Unit: "percent", Remaining: left}}}}}
}

func noteWithSnapshot(e *Entry, snap Snapshot, now time.Time) {
	if e.Snapshots == nil {
		e.Snapshots = map[string]Snapshot{}
	}
	e.Snapshots[snap.Source] = snap
	noteAutoRearm(e, snap, now)
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

func TestLegacyDurationRuleFollowsStableWindow(t *testing.T) {
	now := time.Now()
	e := fixture(now, "80")
	e.Policy.Rules = []Rule{{Metric: "window:5 hours", Unit: "percent", Minimum: "30"}}
	e.Snapshots = map[string]Snapshot{"windows": {Source: "windows", StartedAt: now, ObservedAt: now, Metrics: []Metric{{ID: "window:minimax-general-interval", Label: "4 hours", Unit: "percent", Remaining: "80", LegacyWindowID: true}}}}
	if d := Assess(e, true, now); !d.Allowed || d.Unknown {
		t.Fatalf("legacy 5-hour rule did not follow the current 4-hour window: %+v", d)
	}
	s := e.Snapshots["windows"]
	s.Metrics[0].Remaining = "30"
	e.Snapshots["windows"] = s
	if d := Assess(e, true, now); d.Allowed || !d.Protected || d.Unknown {
		t.Fatalf("legacy rule did not enforce its reserve: %+v", d)
	}
}

func TestLegacyDurationRuleNeedsUnambiguousStableWindow(t *testing.T) {
	now := time.Now()
	e := fixture(now, "80")
	e.Policy.Rules = []Rule{{Metric: "window:5 hours", Unit: "percent", Minimum: "30"}}
	s := e.Snapshots["windows"]
	s.Metrics = []Metric{
		{ID: "window:minimax-general-interval", Unit: "percent", Remaining: "80", LegacyWindowID: true},
		{ID: "window:other-stable-interval", Unit: "percent", Remaining: "80", LegacyWindowID: true},
	}
	e.Snapshots["windows"] = s
	if d := Assess(e, true, now); d.Allowed || !d.Unknown {
		t.Fatalf("ambiguous legacy window was not denied: %+v", d)
	}
}

func TestLegacyDurationRuleDoesNotMatchNamedOrBalanceMetrics(t *testing.T) {
	now := time.Now()
	e := fixture(now, "80")
	e.Policy.Rules = []Rule{{Metric: "window:weekly", Unit: "percent", Minimum: "30"}}
	s := e.Snapshots["windows"]
	s.Metrics = []Metric{{ID: "window:minimax-general-interval", Unit: "percent", Remaining: "80", LegacyWindowID: true}}
	e.Snapshots["windows"] = s
	if d := Assess(e, true, now); d.Allowed || !d.Unknown {
		t.Fatalf("non-duration legacy id followed dynamic window: %+v", d)
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

func TestAutoRearmNeedsHeadroomAndTwoFreshReadings(t *testing.T) {
	now := time.Now()
	e := fixture(now, "10")
	e.Policy.AutoRearm = &AutoRearm{Enabled: true, Consecutive: 2, HeadroomPercent: 10, MaxAgeSeconds: 60}
	Latch(&e, now)

	atReserve := fixture(now.Add(time.Second), "22").Snapshots["windows"]
	noteWithSnapshot(&e, atReserve, now.Add(time.Second))
	if !e.Protection.Tripped || e.Protection.RecoveryCount != 0 {
		t.Fatalf("headroom boundary re-armed: %+v", e.Protection)
	}

	first := fixture(now.Add(2*time.Second), "22.000001").Snapshots["windows"]
	noteWithSnapshot(&e, first, now.Add(2*time.Second))
	if !e.Protection.Tripped || e.Protection.RecoveryCount != 1 {
		t.Fatalf("first recovery reading: %+v", e.Protection)
	}

	second := fixture(now.Add(3*time.Second), "23").Snapshots["windows"]
	noteWithSnapshot(&e, second, now.Add(3*time.Second))
	if e.Protection.Tripped || e.Protection.LastAction != "auto-rearm" || e.Protection.LastAutoRearm == nil {
		t.Fatalf("second recovery reading did not re-arm: %+v", e.Protection)
	}
	if got := e.Protection.LastAutoRearm; got.Consecutive != 2 || got.HeadroomPercent != 10 || got.Source != "windows" {
		t.Fatalf("re-arm audit record: %+v", got)
	}
}

func TestAutoRearmResetsOnLowStaleOrUnknownReading(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name string
		make func() Snapshot
	}{
		{"low", func() Snapshot { return fixture(now.Add(time.Second), "10").Snapshots["windows"] }},
		{"stale", func() Snapshot {
			s := fixture(now.Add(-2*time.Minute), "90").Snapshots["windows"]
			s.ObservedAt = now.Add(-2 * time.Minute)
			return s
		}},
		{"unknown", func() Snapshot {
			s := fixture(now.Add(time.Second), "90").Snapshots["windows"]
			s.Metrics = nil
			return s
		}},
		{"unit", func() Snapshot {
			s := fixture(now.Add(time.Second), "90").Snapshots["windows"]
			s.Metrics[0].Unit = "CNY"
			return s
		}},
		{"invalid amount", func() Snapshot {
			s := fixture(now.Add(time.Second), "90").Snapshots["windows"]
			s.Metrics[0].Remaining = "NaN"
			return s
		}},
		{"elapsed reset", func() Snapshot {
			s := fixture(now.Add(time.Second), "90").Snapshots["windows"]
			s.Metrics[0].ResetAt = &now
			return s
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := fixture(now, "10")
			e.Policy.AutoRearm = &AutoRearm{Enabled: true, Consecutive: 2, HeadroomPercent: 10, MaxAgeSeconds: 5}
			Latch(&e, now)
			tripAt := now.Add(-10 * time.Minute)
			e.Protection.TrippedAt = &tripAt
			e.Protection.RecoveryCount = 1
			snap := tc.make()
			noteWithSnapshot(&e, snap, now.Add(3*time.Second))
			if !e.Protection.Tripped || e.Protection.RecoveryCount != 0 {
				t.Fatalf("%s did not fail closed: %+v", tc.name, e.Protection)
			}
		})
	}
}

func TestAutoRearmDoesNotCountObservationStartedBeforeTrip(t *testing.T) {
	now := time.Now()
	e := fixture(now, "10")
	e.Policy.AutoRearm = &AutoRearm{Enabled: true, Consecutive: 2, HeadroomPercent: 10, MaxAgeSeconds: 5}
	Latch(&e, now)
	tripAt := now.Add(-time.Second)
	e.Protection.TrippedAt = &tripAt
	e.Protection.RecoveryCount = 1
	noteWithSnapshot(&e, fixture(now.Add(-2*time.Second), "90").Snapshots["windows"], now)
	if !e.Protection.Tripped || e.Protection.RecoveryCount != 0 {
		t.Fatalf("pre-trip observation confirmed recovery: %+v", e.Protection)
	}
}

func TestAutoRearmIsOffByDefault(t *testing.T) {
	now := time.Now()
	for _, auto := range []*AutoRearm{nil, &AutoRearm{Enabled: false, Consecutive: 2, HeadroomPercent: 10, MaxAgeSeconds: 60}} {
		e := fixture(now, "10")
		e.Policy.AutoRearm = auto
		Latch(&e, now)
		e.Snapshots = fixture(now.Add(time.Second), "90").Snapshots
		snap := e.Snapshots["windows"]
		noteAutoRearm(&e, snap, now.Add(time.Second))
		if !e.Protection.Tripped {
			t.Fatalf("auto-rearm ran while disabled: %+v", auto)
		}
	}
}

func TestAutoRearmNeverTurnsOnAManuallyDisabledKey(t *testing.T) {
	now := time.Now()
	e := fixture(now, "10")
	e.Policy.AutoRearm = &AutoRearm{Enabled: true, Consecutive: 2, HeadroomPercent: 10, MaxAgeSeconds: 60}
	Latch(&e, now)
	noteWithSnapshot(&e, fixture(now.Add(time.Second), "90").Snapshots["windows"], now.Add(time.Second))
	noteWithSnapshot(&e, fixture(now.Add(2*time.Second), "90").Snapshots["windows"], now.Add(2*time.Second))
	if e.Protection.Tripped || Assess(e, false, now.Add(2*time.Second)).Allowed {
		t.Fatalf("auto-rearm changed manual intent: %+v", e.Protection)
	}
}

func TestAutoRearmRequiresEveryConfiguredMetric(t *testing.T) {
	now := time.Now()
	e := fixture(now, "90")
	e.Policy.Rules = append(e.Policy.Rules, Rule{Metric: "window:other", Unit: "percent", Minimum: "20"})
	e.Policy.AutoRearm = &AutoRearm{Enabled: true, Consecutive: 2, HeadroomPercent: 10, MaxAgeSeconds: 60}
	at := now
	e.Protection.Tripped = true
	e.Protection.TrippedAt = &at
	s := e.Snapshots["windows"]
	s.StartedAt = now.Add(time.Second)
	s.ObservedAt = now.Add(time.Second)
	e.Snapshots["windows"] = s
	noteAutoRearm(&e, s, now.Add(time.Second))
	if !e.Protection.Tripped || e.Protection.RecoveryCount != 0 {
		t.Fatalf("missing configured metric allowed recovery: %+v", e.Protection)
	}
}

func TestAutoRearmDoesNotCountAnUnrelatedObservation(t *testing.T) {
	now := time.Now()
	e := fixture(now, "90")
	e.Policy.AutoRearm = &AutoRearm{Enabled: true, Consecutive: 2, HeadroomPercent: 10, MaxAgeSeconds: 60}
	at := now
	e.Protection.Tripped = true
	e.Protection.TrippedAt = &at
	e.Protection.RecoveryCount = 1
	snap := Snapshot{Source: "balance", StartedAt: now.Add(time.Second), ObservedAt: now.Add(time.Second), Metrics: []Metric{{ID: "balance:CNY", Unit: "CNY", Remaining: "90"}}}
	noteAutoRearm(&e, snap, now.Add(time.Second))
	if !e.Protection.Tripped || e.Protection.RecoveryCount != 0 {
		t.Fatalf("unrelated observation confirmed recovery: %+v", e.Protection)
	}
}

func TestAutoRearmPolicyValidation(t *testing.T) {
	now := time.Now()
	p := fixture(now, "50").Policy
	p.AutoRearm = &AutoRearm{Enabled: true}
	if err := p.Validate(); err != nil {
		t.Fatalf("defaults should be valid: %v", err)
	}
	for _, bad := range []*AutoRearm{
		{Enabled: true, Consecutive: 1},
		{Enabled: true, Consecutive: 11},
		{Enabled: true, HeadroomPercent: 101},
		{Enabled: true, MaxAgeSeconds: 4},
		{Enabled: true, MaxAgeSeconds: 61},
	} {
		p.AutoRearm = bad
		if err := p.Validate(); err == nil {
			t.Fatalf("accepted auto-rearm policy %+v", bad)
		}
	}
	p.MaxAgeSeconds = 30
	p.AutoRearm = &AutoRearm{Enabled: true, MaxAgeSeconds: 60}
	if err := p.Validate(); err == nil {
		t.Fatal("accepted an auto-rearm age longer than the policy age")
	}
	p.MaxAgeSeconds = 0
	p.Rules[0].Minimum = "95"
	p.AutoRearm = &AutoRearm{Enabled: true, HeadroomPercent: 10}
	if err := p.Validate(); err == nil {
		t.Fatal("accepted an unrecoverable percentage headroom")
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
