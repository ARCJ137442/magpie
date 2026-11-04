// Package quotaguard decides whether a credential may start another upstream
// attempt. It never estimates a request's cost and never edits a user's switch
// or an upstream's health/backoff. Observations are the vendor's, not a local
// spending ledger. A threshold trip latches until an explicit human action,
// unless the opt-in auto-rearm policy has confirmed a durable recovery.
package quotaguard

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	Version       = 1
	DefaultMaxAge = 60 * time.Second
	MaxOverride   = 24 * time.Hour
)

const (
	RefreshModeCached       = "cached"
	RefreshModeNearReserve  = "near"
	RefreshModeEveryAttempt = "everyAttempt"
)

const (
	defaultAutoRearmConsecutive     = 2
	defaultAutoRearmHeadroomPercent = 10
)

var ErrConflict = errors.New("quota guard changed; reload before applying this action")

// Rule names a reported metric, not a vendor or a synthetic local budget.
// Minimum is decimal text so a money boundary is not rounded through a float.
// A reading equal to Minimum trips as well: reserve 20 means stop at 20 left.
type Rule struct {
	Metric        string `json:"metric"`
	Unit          string `json:"unit"`
	Minimum       string `json:"minimum"`
	RefreshWithin string `json:"refreshWithin,omitempty"`
}

// AutoRearm clears a latched trip only after repeated fresh readings clear
// every rule by a configured headroom. It is opt-in; a nil value keeps the
// original latch semantics.
type AutoRearm struct {
	Enabled         bool `json:"enabled"`
	Consecutive     int  `json:"consecutive,omitempty"`
	HeadroomPercent int  `json:"headroomPercent,omitempty"`
	MaxAgeSeconds   int  `json:"maxAgeSeconds,omitempty"`
}

func (a AutoRearm) consecutive() int {
	if a.Consecutive == 0 {
		return defaultAutoRearmConsecutive
	}
	return a.Consecutive
}

func (a AutoRearm) headroomPercent() int {
	if a.HeadroomPercent == 0 {
		return defaultAutoRearmHeadroomPercent
	}
	return a.HeadroomPercent
}

func (a AutoRearm) maxAge(policyMaxAge time.Duration) time.Duration {
	if a.MaxAgeSeconds == 0 {
		return policyMaxAge
	}
	return time.Duration(a.MaxAgeSeconds) * time.Second
}

type Policy struct {
	Enabled       bool       `json:"enabled"`
	Rules         []Rule     `json:"rules"`
	MaxAgeSeconds int        `json:"maxAgeSeconds,omitempty"`
	RefreshMode   string     `json:"refreshMode,omitempty"`
	AutoRearm     *AutoRearm `json:"autoRearm,omitempty"`
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
	switch p.RefreshMode {
	case "", RefreshModeCached, RefreshModeNearReserve, RefreshModeEveryAttempt:
	default:
		return errors.New("unknown quota refresh mode")
	}
	if a := p.AutoRearm; a != nil {
		if a.Consecutive != 0 && (a.Consecutive < 2 || a.Consecutive > 10) {
			return errors.New("automatic re-arm needs 2 to 10 consecutive readings")
		}
		if a.HeadroomPercent != 0 && (a.HeadroomPercent < 1 || a.HeadroomPercent > 100) {
			return errors.New("automatic re-arm headroom must be 1 to 100 percent")
		}
		if a.MaxAgeSeconds != 0 {
			if a.MaxAgeSeconds < 5 || a.MaxAgeSeconds > 600 {
				return errors.New("automatic re-arm reading age must be 5 to 600 seconds")
			}
			if p.Enabled && a.Enabled && time.Duration(a.MaxAgeSeconds)*time.Second > p.MaxAge() {
				return errors.New("automatic re-arm reading age must not exceed the policy reading age")
			}
		}
	}
	if p.Enabled && len(p.Rules) == 0 {
		return errors.New("an enabled quota guard needs a rule")
	}
	if len(p.Rules) > 32 {
		return errors.New("at most 32 quota rules per credential")
	}
	seen := map[string]bool{}
	hasNearBand := false
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
		if r.RefreshWithin != "" {
			near, err := Number(r.RefreshWithin)
			if err != nil || near.Sign() <= 0 {
				return errors.New("a near-reserve refresh range must be a positive decimal in the rule's unit")
			}
			hasNearBand = true
		}
		if p.Enabled && p.AutoRearm != nil && p.AutoRearm.Enabled && r.Unit == "percent" {
			headroom := new(big.Rat).Mul(n, big.NewRat(int64(p.AutoRearm.headroomPercent()), 100))
			threshold := new(big.Rat).Add(new(big.Rat).Set(n), headroom)
			if threshold.Cmp(big.NewRat(100, 1)) >= 0 {
				return errors.New("automatic re-arm headroom leaves no recoverable percentage above the reserve")
			}
		}
	}
	if p.Enabled && p.RefreshMode == RefreshModeNearReserve && !hasNearBand {
		return errors.New("near-reserve refresh needs a positive range on at least one rule")
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
	ID             string     `json:"id"`
	Label          string     `json:"label"`
	Unit           string     `json:"unit"`
	Remaining      string     `json:"remaining"`
	Scope          string     `json:"scope"`
	ResetAt        *time.Time `json:"resetAt,omitempty"`
	LegacyWindowID bool       `json:"legacyWindowId,omitempty"`
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
	Tripped       bool             `json:"tripped"`
	TrippedAt     *time.Time       `json:"trippedAt,omitempty"`
	Reasons       []Reason         `json:"reasons,omitempty"`
	OverrideUntil *time.Time       `json:"overrideUntil,omitempty"`
	LastAction    string           `json:"lastAction,omitempty"`
	ActionAt      *time.Time       `json:"actionAt,omitempty"`
	RecoveryCount int              `json:"recoveryCount,omitempty"`
	LastAutoRearm *AutoRearmRecord `json:"lastAutoRearm,omitempty"`
}

type AutoRearmRecord struct {
	At              time.Time `json:"at"`
	Source          string    `json:"source"`
	ObservedAt      time.Time `json:"observedAt"`
	Consecutive     int       `json:"consecutive"`
	HeadroomPercent int       `json:"headroomPercent"`
	MaxAgeSeconds   int       `json:"maxAgeSeconds"`
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
	NearReserve    bool       `json:"nearReserve,omitempty"`
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
		value, metric, why := readRule(e, rule, now, e.Policy.MaxAge())
		if why != "" {
			d.Unknown = true
			d.Reasons = append(d.Reasons, Reason{Metric: rule.Metric, Code: why})
			continue
		}
		minimum, merr := Number(rule.Minimum)
		if merr != nil {
			d.Unknown = true
			d.Reasons = append(d.Reasons, Reason{Metric: rule.Metric, Code: "invalid_amount"})
			continue
		}
		if value.Cmp(minimum) <= 0 {
			d.Protected = true
			d.Reasons = append(d.Reasons, Reason{Metric: rule.Metric, Code: "reserve_reached", Remaining: metric.Remaining, Minimum: rule.Minimum, Unit: rule.Unit})
		}
		if e.Policy.RefreshMode == RefreshModeNearReserve && rule.RefreshWithin != "" {
			near, _ := Number(rule.RefreshWithin) // Policy.Validate checked it above.
			if value.Cmp(new(big.Rat).Add(minimum, near)) <= 0 {
				d.NearReserve = true
			}
		}
	}
	if d.Protected && len(d.Reasons) == 0 {
		d.Reasons = append(d.Reasons, e.Protection.Reasons...)
	}
	d.Allowed = manualEnabled && !d.Unknown && (!d.Protected || d.OverrideActive)
	return d
}

// readRule resolves one accepted metric value and its freshness checks. It is
// shared by normal admission and the stricter automatic re-arm evaluation.
func readRule(e Entry, rule Rule, now time.Time, maxAge time.Duration) (*big.Rat, Metric, string) {
	var metric *Metric
	var at time.Time
	ambiguous := false
	matchRule := func(m Metric) bool { return m.ID == rule.Metric }
	for _, snap := range e.Snapshots {
		for _, m := range snap.Metrics {
			if !matchRule(m) {
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
	// Older policies named this MiniMax allowance by its displayed
	// duration (for example, "window:5 hours"). If that name changes,
	// keep the rule attached to the stable metric identity. Exact IDs
	// always take precedence, and multiple fallback matches stay unknown.
	if metric == nil && !ambiguous && legacyWindowID(rule.Metric) {
		matchRule = func(m Metric) bool { return m.LegacyWindowID }
		for _, snap := range e.Snapshots {
			for _, m := range snap.Metrics {
				if !matchRule(m) {
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
	}
	switch {
	case ambiguous:
		return nil, Metric{}, "ambiguous_metric"
	case metric == nil:
		return nil, Metric{}, "missing_metric"
	case at.IsZero() || at.After(now) || now.Sub(at) > maxAge:
		return nil, *metric, "stale_metric"
	case metric.Unit != rule.Unit:
		return nil, *metric, "unit_mismatch"
	case metric.ResetAt != nil && !now.Before(*metric.ResetAt):
		return nil, *metric, "window_needs_refresh"
	}
	value, err := Number(metric.Remaining)
	if err != nil {
		return nil, *metric, "invalid_amount"
	}
	return value, *metric, ""
}

// autoRearmReady uses the same metric resolution and failure checks as
// admission, but with a stricter age limit and a recovery headroom.
func autoRearmReady(e Entry, now time.Time) bool {
	a := e.Policy.AutoRearm
	if a == nil || !a.Enabled {
		return false
	}
	maxAge := a.maxAge(e.Policy.MaxAge())
	headroom := big.NewRat(int64(a.headroomPercent()), 100)
	for _, rule := range e.Policy.Rules {
		value, _, why := readRule(e, rule, now, maxAge)
		if why != "" || value == nil {
			return false
		}
		minimum, err := Number(rule.Minimum)
		if err != nil {
			return false
		}
		threshold := new(big.Rat).Add(minimum, new(big.Rat).Mul(minimum, headroom))
		if value.Cmp(threshold) <= 0 {
			return false
		}
	}
	return true
}

// A confirmation must be in the observation that caused it. A refresh of an
// unrelated balance/window source cannot count the same older snapshot twice.
func snapshotCoversRules(snap Snapshot, rules []Rule) bool {
	for _, rule := range rules {
		found := false
		for _, m := range snap.Metrics {
			if m.ID == rule.Metric || legacyWindowID(rule.Metric) && m.LegacyWindowID {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// noteAutoRearm counts accepted observations, not retries of the same
// snapshot. A failed, stale or below-headroom observation resets the count.
func noteAutoRearm(e *Entry, snap Snapshot, now time.Time) {
	a := e.Policy.AutoRearm
	if a == nil || !a.Enabled || !e.Protection.Tripped {
		return
	}
	if e.Protection.TrippedAt == nil || !snap.StartedAt.After(*e.Protection.TrippedAt) {
		e.Protection.RecoveryCount = 0
		return
	}
	if !snapshotCoversRules(snap, e.Policy.Rules) || !autoRearmReady(*e, now) {
		e.Protection.RecoveryCount = 0
		return
	}
	count := e.Protection.RecoveryCount + 1
	if count < a.consecutive() {
		e.Protection.RecoveryCount = count
		return
	}
	at := now
	maxAge := a.maxAge(e.Policy.MaxAge())
	e.Protection.Tripped = false
	e.Protection.TrippedAt = nil
	e.Protection.Reasons = nil
	e.Protection.RecoveryCount = 0
	e.Protection.LastAction = "auto-rearm"
	e.Protection.ActionAt = &at
	e.Protection.LastAutoRearm = &AutoRearmRecord{
		At:              at,
		Source:          snap.Source,
		ObservedAt:      snap.ObservedAt,
		Consecutive:     count,
		HeadroomPercent: a.headroomPercent(),
		MaxAgeSeconds:   int(maxAge / time.Second),
	}
}

func legacyWindowID(id string) bool {
	name, ok := strings.CutPrefix(id, "window:")
	if !ok {
		return false
	}
	parts := strings.Fields(name)
	if len(parts) != 2 {
		return false
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil || n < 1 {
		return false
	}
	switch strings.ToLower(parts[1]) {
	case "minute", "minutes", "hour", "hours", "day", "days":
		return true
	default:
		return false
	}
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
			e.Protection.RecoveryCount = 0
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
	return "upstream_quota_protected: reserve reached; a person must change the reserve, re-arm after recovery, or grant a time-limited override (unless automatic re-arm is enabled)"
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
