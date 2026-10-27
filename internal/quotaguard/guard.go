// Package quotaguard decides whether a credential may start another upstream
// attempt. It never estimates a request's cost and never edits a user's switch
// or an upstream's health/backoff. Observations are the vendor's, not a local
// spending ledger. A threshold trip latches until an explicit human action.
package quotaguard

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
)

const (
	Version       = 1
	DefaultMaxAge = 60 * time.Second
	MaxOverride   = 24 * time.Hour
)

var ErrConflict = errors.New("quota guard changed; reload before applying this action")

// Rule names a reported metric, not a vendor or a synthetic local budget.
// Minimum is decimal text so a money boundary is not rounded through a float.
// A reading equal to Minimum trips as well: reserve 20 means stop at 20 left.
type Rule struct {
	Metric  string `json:"metric"`
	Unit    string `json:"unit"`
	Minimum string `json:"minimum"`
}

type Policy struct {
	Enabled       bool   `json:"enabled"`
	Rules         []Rule `json:"rules"`
	MaxAgeSeconds int    `json:"maxAgeSeconds,omitempty"`
	// A custom balance uses the provider's EXISTING Balance URL and auth.
	// The explicit raw JSON pointer is a numeric field, not BalancePath's
	// formatted text/expression. No new destination or script is introduced.
	BalancePointer string `json:"balancePointer,omitempty"`
	BalanceUnit    string `json:"balanceUnit,omitempty"`
}

func (p Policy) MaxAge() time.Duration {
	if p.MaxAgeSeconds == 0 {
		return DefaultMaxAge
	}
	return time.Duration(p.MaxAgeSeconds) * time.Second
}

var decimal = regexp.MustCompile(`^[+-]?[0-9]{1,24}(\.[0-9]{1,18})?$`)

// Number accepts finite fixed-point decimal data only; fractions, exponent
// strings, NaN, infinity and unbounded input cannot masquerade as balances.
func Number(s string) (*big.Rat, error) {
	if !decimal.MatchString(s) {
		return nil, errors.New("expected a finite decimal amount")
	}
	n, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, errors.New("invalid decimal amount")
	}
	return n, nil
}

func (p Policy) Validate() error {
	if p.MaxAgeSeconds != 0 && (p.MaxAgeSeconds < 5 || p.MaxAgeSeconds > 600) {
		return errors.New("quota observation age must be 5 to 600 seconds")
	}
	if p.Enabled && len(p.Rules) == 0 {
		return errors.New("an enabled quota guard needs a rule")
	}
	if len(p.Rules) > 32 {
		return errors.New("at most 32 quota rules per credential")
	}
	seen := map[string]bool{}
	for _, r := range p.Rules {
		if r.Metric == "" || len(r.Metric) > 256 || r.Unit == "" || len(r.Unit) > 32 {
			return errors.New("a quota rule needs a metric and its unit")
		}
		if seen[r.Metric] {
			return errors.New("a quota metric can have only one reserve rule")
		}
		seen[r.Metric] = true
		n, err := Number(r.Minimum)
		if err != nil || n.Sign() < 0 {
			return errors.New("a minimum reserve must be a nonnegative decimal")
		}
		if r.Unit == "percent" && n.Cmp(big.NewRat(100, 1)) > 0 {
			return errors.New("a percentage reserve must not exceed 100")
		}
	}
	if p.BalancePointer != "" {
		if !strings.HasPrefix(p.BalancePointer, "/") || len(p.BalancePointer) > 512 || p.BalanceUnit == "" || len(p.BalanceUnit) > 32 {
			return errors.New("a custom balance needs a JSON pointer and an explicit unit")
		}
	} else if p.BalanceUnit != "" {
		return errors.New("a custom unit needs a JSON pointer")
	}
	return nil
}

// Scope is provenance, not an assertion that a key owns an independent pool.
// Several credentials may observe one account balance. No totals are added.
type Metric struct {
	ID        string     `json:"id"`
	Label     string     `json:"label"`
	Unit      string     `json:"unit"`
	Remaining string     `json:"remaining"`
	Scope     string     `json:"scope"`
	ResetAt   *time.Time `json:"resetAt,omitempty"`
}

type Snapshot struct {
	Source        string    `json:"source"`
	SourceVersion string    `json:"sourceVersion,omitempty"`
	StartedAt     time.Time `json:"startedAt"`
	ObservedAt    time.Time `json:"observedAt"`
	Metrics       []Metric  `json:"metrics"`
}

type Reason struct {
	Metric    string `json:"metric"`
	Code      string `json:"code"`
	Remaining string `json:"remaining,omitempty"`
	Minimum   string `json:"minimum,omitempty"`
	Unit      string `json:"unit,omitempty"`
}

type Protection struct {
	Tripped       bool       `json:"tripped"`
	TrippedAt     *time.Time `json:"trippedAt,omitempty"`
	Reasons       []Reason   `json:"reasons,omitempty"`
	OverrideUntil *time.Time `json:"overrideUntil,omitempty"`
	LastAction    string     `json:"lastAction,omitempty"`
	ActionAt      *time.Time `json:"actionAt,omitempty"`
}

type Entry struct {
	Revision   uint64              `json:"revision"`
	Policy     Policy              `json:"policy"`
	Protection Protection          `json:"protection"`
	Snapshots  map[string]Snapshot `json:"snapshots,omitempty"`
}

// Decision keeps independent dimensions. A valid override never turns a
// manually disabled key on, clears vendor backoff, or claims stale data fresh.
type Decision struct {
	Allowed        bool       `json:"allowed"`
	ManualOff      bool       `json:"manualOff"`
	GuardEnabled   bool       `json:"guardEnabled"`
	Protected      bool       `json:"protected"`
	Unknown        bool       `json:"unknown"`
	OverrideActive bool       `json:"overrideActive"`
	OverrideUntil  *time.Time `json:"overrideUntil,omitempty"`
	Reasons        []Reason   `json:"reasons,omitempty"`
}

func Assess(e Entry, manualEnabled bool, now time.Time) Decision {
	d := Decision{ManualOff: !manualEnabled, GuardEnabled: e.Policy.Enabled}
	if !e.Policy.Enabled {
		d.Allowed = manualEnabled
		return d
	}
	if err := e.Policy.Validate(); err != nil {
		d.Unknown = true
		d.Reasons = []Reason{{Code: "invalid_policy"}}
		return d
	}
	d.Protected = e.Protection.Tripped
	d.OverrideUntil = e.Protection.OverrideUntil
	d.OverrideActive = d.OverrideUntil != nil && now.Before(*d.OverrideUntil)
	for _, rule := range e.Policy.Rules {
		var metric *Metric
		var at time.Time
		ambiguous := false
		for _, snap := range e.Snapshots {
			for _, m := range snap.Metrics {
				if m.ID != rule.Metric {
					continue
				}
				if metric != nil {
					ambiguous = true
					continue
				}
				copy := m
				metric = &copy
				at = snap.ObservedAt
			}
		}
		why := ""
		switch {
		case ambiguous:
			why = "ambiguous_metric"
		case metric == nil:
			why = "missing_metric"
		case at.IsZero() || at.After(now) || now.Sub(at) > e.Policy.MaxAge():
			why = "stale_metric"
		case metric.Unit != rule.Unit:
			why = "unit_mismatch"
		case metric.ResetAt != nil && !now.Before(*metric.ResetAt):
			why = "window_needs_refresh"
		}
		if why != "" {
			d.Unknown = true
			d.Reasons = append(d.Reasons, Reason{Metric: rule.Metric, Code: why})
			continue
		}
		value, err := Number(metric.Remaining)
		minimum, merr := Number(rule.Minimum)
		if err != nil || merr != nil {
			d.Unknown = true
			d.Reasons = append(d.Reasons, Reason{Metric: rule.Metric, Code: "invalid_amount"})
			continue
		}
		if value.Cmp(minimum) <= 0 {
			d.Protected = true
			d.Reasons = append(d.Reasons, Reason{Metric: rule.Metric, Code: "reserve_reached", Remaining: metric.Remaining, Minimum: rule.Minimum, Unit: rule.Unit})
		}
	}
	if d.Protected && len(d.Reasons) == 0 {
		d.Reasons = append(d.Reasons, e.Protection.Reasons...)
	}
	d.Allowed = manualEnabled && !d.Unknown && (!d.Protected || d.OverrideActive)
	return d
}

// Latch records a known crossing, even if another configured metric is unknown.
// An unknown reading alone is a temporary denial, never a permanent trip.
func Latch(e *Entry, now time.Time) {
	d := Assess(*e, true, now)
	for _, r := range d.Reasons {
		if r.Code == "reserve_reached" && !e.Protection.Tripped {
			e.Protection.Tripped = true
			at := now
			e.Protection.TrippedAt = &at
			e.Protection.Reasons = append([]Reason(nil), d.Reasons...)
			return
		}
	}
}

func (d Decision) Message() string {
	if d.ManualOff {
		return "credential is manually disabled"
	}
	if d.Unknown {
		return "upstream_quota_unknown: a protected metric is missing, stale or invalid; refresh its quota before retrying"
	}
	return "upstream_quota_protected: reserve reached; a person must change the reserve, re-arm after recovery, or grant a time-limited override"
}

func ValidateSnapshot(s Snapshot, now time.Time) error {
	if s.Source == "" || len(s.Source) > 64 || s.StartedAt.IsZero() || s.ObservedAt.IsZero() || s.ObservedAt.Before(s.StartedAt) || s.ObservedAt.After(now) {
		return errors.New("invalid quota observation timestamps or source")
	}
	if len(s.Metrics) > 128 {
		return errors.New("too many quota metrics")
	}
	seen := map[string]bool{}
	for _, m := range s.Metrics {
		if m.ID == "" || len(m.ID) > 256 || seen[m.ID] || m.Unit == "" || len(m.Unit) > 32 || len(m.Label) > 256 || len(m.Scope) > 64 {
			return errors.New("invalid or duplicate quota metric")
		}
		seen[m.ID] = true
		n, err := Number(m.Remaining)
		if err != nil {
			return fmt.Errorf("metric %q: %w", m.ID, err)
		}
		if m.Unit == "percent" && (n.Sign() < 0 || n.Cmp(big.NewRat(100, 1)) > 0) {
			return errors.New("percentage outside 0 to 100")
		}
	}
	return nil
}
