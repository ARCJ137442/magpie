package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/quotaguard"
)

func quotaGuardStore() quotaguard.Store {
	return quotaguard.Store{Path: filepath.Join(appdir.Config(), "quota-guards.json")}
}

func quotaGuardID(p Provider, id string) string {
	if p.Account != nil {
		return id + "/account/" + strings.ToLower(p.Account.User)
	}
	return id + "/key/" + KeyID(p.Key)
}

// quotaGuardEntry keeps a provider rename on its existing policy. The
// credential fingerprint, not the currently first key or its label, is used.
func quotaGuardEntry(p Provider) (string, quotaguard.Entry, error) {
	id := quotaGuardID(p, p.ID)
	e, err := quotaGuardStore().Get(id)
	if err != nil || e.Revision != 0 {
		return id, guardCurrentSnapshots(p, e), err
	}
	for _, was := range p.Was {
		old := quotaGuardID(p, was)
		e, err = quotaGuardStore().Get(old)
		if err != nil || e.Revision != 0 {
			return old, guardCurrentSnapshots(p, e), err
		}
	}
	return id, e, nil
}

// Source changes invalidate snapshots without erasing a latched trip. Only a
// digest is kept; credentials and authentication headers never enter the store.
// Resolve the full saved key plan so protocol-narrowed request copies agree.
func quotaSourceVersion(p Provider) string {
	if saved, err := Find(p.ID); err == nil {
		if p.Account == nil {
			for _, k := range append([]KeyAccount{saved.first()}, saved.Keys...) {
				if k.Key == p.Key {
					p = saved.WithKey(k)
					break
				}
			}
		}
	}
	return quotaReadSourceVersion(p)
}

// Hash the provider value that will actually be used by this read. Looking up
// current configuration here would bless a stale reader with the new source's
// identity. Normalize only the key plan/protocol, as a routed key view does.
func quotaReadSourceVersion(p Provider) string {
	if p.Account == nil {
		p = p.WithKey(p.first())
	}
	b, _ := json.Marshal([]any{p.Chat, p.Responses, p.Anthropic, p.Gemini, p.Decide, p.KeyPlan, p.BalanceURL, p.BalancePath, p.BalanceMethod, p.BalanceToken, p.Headers, p.ZhipuTeam, p.AccessKeyID, p.SecretAccessKey})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func guardCurrentSnapshots(p Provider, e quotaguard.Entry) quotaguard.Entry {
	version := quotaSourceVersion(p)
	kept := make(map[string]quotaguard.Snapshot, len(e.Snapshots))
	for k, s := range e.Snapshots {
		if s.SourceVersion == version {
			kept[k] = s
		}
	}
	e.Snapshots = kept
	return e
}
func guardFaultID(subject string) string { return appdir.Config() + "\x00" + subject }

// GuardCredential resolves a NONSECRET key id, including a manually disabled
// key so its policy remains editable. Its existing plan/protocol mapping stays.
func GuardCredential(id, keyID string) (Provider, error) {
	p, err := Find(id)
	if err != nil {
		return Provider{}, err
	}
	if p.Account != nil {
		return Provider{}, errors.New("use the subscription's existing cap; this editor currently manages API keys")
	}
	keys := append([]KeyAccount{p.first()}, p.Keys...)
	for _, k := range keys {
		if k.Key != "" && KeyID(k.Key) == keyID {
			return p.WithKey(k), nil
		}
	}
	return Provider{}, errors.New("provider no longer has this key")
}

// guardManualOn re-reads the user's intent after a queued request resumes.
// It never changes it. The guard is not a replacement for existing health,
// model eligibility, account permissions or legacy subscription caps.
func guardManualOn(p Provider) bool {
	q, err := Find(p.ID)
	if err != nil || q.Off {
		return false
	}
	if p.Account != nil {
		for _, l := range Logins(p.Account.UsageAgent()) {
			if strings.EqualFold(l.User, p.Account.User) {
				return l.On && !l.Paused && l.Lapsed == ""
			}
		}
		return false
	}
	for _, k := range q.KeysOn() {
		if k.Key == p.Key {
			return true
		}
	}
	return false
}

type QuotaGuardView struct {
	Subject  string              `json:"subject"`
	Entry    quotaguard.Entry    `json:"entry"`
	Decision quotaguard.Decision `json:"decision"`
}

func QuotaGuardOf(p Provider) (QuotaGuardView, error) {
	id, e, err := quotaGuardEntry(p)
	if err != nil {
		return QuotaGuardView{}, err
	}
	return QuotaGuardView{Subject: id, Entry: e, Decision: quotaguard.Assess(e, guardManualOn(p), time.Now())}, nil
}

func ConfigureQuotaGuard(p Provider, policy quotaguard.Policy, revision uint64) (QuotaGuardView, error) {
	id, _, err := quotaGuardEntry(p)
	if err != nil {
		return QuotaGuardView{}, err
	}
	if _, err = quotaGuardStore().Configure(id, policy, revision, time.Now()); err != nil {
		return QuotaGuardView{}, err
	}
	return QuotaGuardOf(p)
}

func ActQuotaGuard(p Provider, action string, until *time.Time, revision uint64) (QuotaGuardView, error) {
	id, e, err := quotaGuardEntry(p)
	if err != nil {
		return QuotaGuardView{}, err
	}
	if action == "override" || action == "rearm" {
		probe := e
		if action == "rearm" {
			probe.Protection = quotaguard.Protection{}
		}
		d := quotaguard.Assess(probe, true, time.Now())
		if d.Unknown || action == "rearm" && !d.Allowed {
			return QuotaGuardView{}, errors.New("refresh valid, recovered quota data before this action")
		}
	}
	if _, err = quotaGuardStore().HumanAction(id, action, until, revision, time.Now()); err != nil {
		return QuotaGuardView{}, err
	}
	return QuotaGuardOf(p)
}

// QuotaGuardError is LOCAL admission policy, never a vendor refusal. Routing
// skips it without resting the key, promoting a last key, or trying it again.
type QuotaGuardError struct{ Decision quotaguard.Decision }

func (e *QuotaGuardError) Error() string { return e.Decision.Message() }
func (e *QuotaGuardError) Status() int {
	if e.Decision.Unknown {
		return 503
	}
	return 403
}
func (e *QuotaGuardError) Code() string {
	if e.Decision.Unknown {
		return "upstream_quota_unknown"
	}
	if e.Decision.ManualOff {
		return "upstream_key_disabled"
	}
	return "upstream_quota_protected"
}

// Guard failures never include vendor bodies, URLs with query keys, or secrets.
func guardUnavailable() error {
	return &QuotaGuardError{Decision: quotaguard.Decision{GuardEnabled: true, Unknown: true}}
}

// CheckQuotaGuard is an admission boundary. Every new physical retry needs it,
// including a retry within one logical attempt. Already admitted/in-flight
// requests are not aborted; no request's future consumption is reserved.
func CheckQuotaGuard(ctx context.Context, p Provider) error {
	return checkQuotaGuard(ctx, p, false)
}

// checkQuotaGuard checks the saved observation for a route candidate. A
// physical send uses beforeAttempt to apply the policy's selected freshness
// mode at its last common gate.
func checkQuotaGuard(ctx context.Context, p Provider, beforeAttempt bool) error {
	if p.Key == "" && p.Account == nil {
		return nil
	}
	id, e, err := quotaGuardEntry(p)
	if err != nil {
		return guardUnavailable()
	}
	if !e.Policy.Enabled {
		return nil
	}
	manual := guardManualOn(p)
	if !manual {
		return &QuotaGuardError{Decision: quotaguard.Assess(e, false, time.Now())}
	}
	d := quotaguard.Assess(e, true, time.Now())
	_, readHere := guardReadThisProcess.Load(guardReadID(p, id, e.Revision))
	forceFresh := false
	if beforeAttempt && d.Allowed {
		switch e.Policy.RefreshMode {
		case quotaguard.RefreshModeNearReserve:
			forceFresh = d.NearReserve
		case quotaguard.RefreshModeEveryAttempt:
			forceFresh = true
		}
	}
	// Cached mode performs an initial read for this process before it admits
	// a credential. Near/strict modes defer a fresh read to the physical-send
	// gate unless the stored observation is unknown or stale; this avoids
	// fetching twice merely because routing planned the request.
	needRead := d.Unknown || (!readHere && e.Policy.RefreshMode == quotaguard.RefreshModeCached) || forceFresh
	if needRead {
		if err = refreshQuotaGuardOnce(ctx, p, id, forceFresh); err != nil {
			return guardUnavailable()
		}
		_, e, err = quotaGuardEntry(p)
		if err != nil {
			return guardUnavailable()
		}
		d = quotaguard.Assess(e, guardManualOn(p), time.Now())
	}
	if _, bad := guardWriteFault.Load(guardFaultID(id)); bad {
		return guardUnavailable()
	}
	if !d.Allowed {
		return &QuotaGuardError{Decision: d}
	}
	return nil
}

// A failed persistence cannot let this process keep serving an old healthy
// snapshot. A later successfully persisted observation clears the local fault.
var guardWriteFault sync.Map
var guardReadThisProcess sync.Map

func guardReadID(p Provider, subject string, revision uint64) string {
	return guardFaultID(subject) + "/" + strconv.FormatUint(revision, 10) + "/" + quotaSourceVersion(p)
}

type guardCaptureKey struct{}
type guardCapture struct {
	snapshots []quotaguard.Snapshot
	policy    quotaguard.Policy
}
type quotaObservation struct {
	p             Provider
	subject       string
	entry         quotaguard.Entry
	started       time.Time
	sourceVersion string
	capture       *guardCapture
	err           error
}

// beginQuotaObservation is called at the actual source read, not when a card
// is redrawn. It carries policy revision across the network without a lock.
func beginQuotaObservation(ctx context.Context, p Provider) quotaObservation {
	id, e, err := quotaGuardEntry(p)
	capture, _ := ctx.Value(guardCaptureKey{}).(*guardCapture)
	return quotaObservation{p: p, subject: id, entry: e, started: time.Now(), sourceVersion: quotaReadSourceVersion(p), capture: capture, err: err}
}

func guardWindowMetrics(ws []QuotaWindow) []quotaguard.Metric {
	out := make([]quotaguard.Metric, 0, len(ws))
	for _, w := range ws {
		// Text, unrelated on-demand pools and unlimited windows are not
		// machine-readable finite allowance. No invented zero or reset.
		if w.Text || w.Unlimited || w.Aside || math.IsNaN(w.Used) || math.IsInf(w.Used, 0) || w.Used < 0 {
			continue
		}
		left := math.Max(0, 100-w.Used)
		out = append(out, quotaguard.Metric{ID: "window:" + WindowCapID(w.Name), Label: w.Name, Unit: "percent", Remaining: strconv.FormatFloat(left, 'f', -1, 64), Scope: "plan", ResetAt: w.ResetsAt})
	}
	return out
}

func (o quotaObservation) finish(source string, metrics []quotaguard.Metric) error {
	snap := quotaguard.Snapshot{Source: source, SourceVersion: o.sourceVersion, StartedAt: o.started, ObservedAt: time.Now(), Metrics: metrics}
	if o.capture != nil {
		o.capture.snapshots = append(o.capture.snapshots, snap)
	}
	if o.err != nil {
		guardWriteFault.Store(guardFaultID(o.subject), true)
		return errors.New("quota protection could not read its state")
	}
	if !o.entry.Policy.Enabled {
		return nil
	}
	if quotaSourceVersion(o.p) != o.sourceVersion {
		return nil
	} // changed while the source read was in flight
	err := quotaGuardStore().Observe(o.subject, o.entry.Revision, snap, time.Now())
	// A human's newer action wins over a read started before it. The next
	// guard check evaluates that new revision rather than overwriting it.
	if errors.Is(err, quotaguard.ErrConflict) {
		return nil
	}
	if err != nil {
		guardWriteFault.Store(guardFaultID(o.subject), true)
		return errors.New("quota protection could not save its observation")
	}
	guardWriteFault.Delete(guardFaultID(o.subject))
	guardReadThisProcess.Store(guardReadID(o.p, o.subject, o.entry.Revision), true)
	return nil
}

func (o quotaObservation) windows(ws []QuotaWindow) error {
	return o.finish("windows", guardWindowMetrics(ws))
}

func (o quotaObservation) balance(raw []byte, ws []QuotaWindow) error {
	if o.capture == nil && !o.entry.Policy.Enabled {
		return o.err
	}
	// A draft mapping is a preview only. It must never contaminate the live
	// policy's observations. The same response is parsed separately for each.
	var previewErr error
	if o.capture != nil {
		metrics, err := guardBalanceMetrics(o.p, raw, o.capture.policy)
		previewErr = err
		if err == nil {
			metrics = append(metrics, guardWindowMetrics(ws)...)
			o.capture.snapshots = append(o.capture.snapshots, quotaguard.Snapshot{Source: "balance", SourceVersion: o.sourceVersion, StartedAt: o.started, ObservedAt: time.Now(), Metrics: metrics})
		}
	}
	if !o.entry.Policy.Enabled {
		return previewErr
	}
	metrics, err := guardBalanceMetrics(o.p, raw, o.entry.Policy)
	if err != nil {
		return err
	}
	metrics = append(metrics, guardWindowMetrics(ws)...)
	o.capture = nil
	return errors.Join(previewErr, o.finish("balance", metrics))
}

// guardBalanceMetrics reads original numeric JSON tokens. It NEVER parses
// the rounded/localized Balance string. Unsupported sources have no metrics.
func guardBalanceMetrics(p Provider, b []byte, policy quotaguard.Policy) ([]quotaguard.Metric, error) {
	var root any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return nil, errors.New("balance response is not JSON")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, errors.New("balance response has trailing data")
	}
	amount := func(v any) (string, error) {
		var s string
		switch x := v.(type) {
		case string:
			s = x
		case json.Number:
			s = string(x)
		default:
			return "", errors.New("balance is not numeric")
		}
		if _, err := quotaguard.Number(s); err != nil {
			return "", err
		}
		return s, nil
	}
	metric := func(id, label, unit, scope string, v any) (quotaguard.Metric, error) {
		n, err := amount(v)
		return quotaguard.Metric{ID: id, Label: label, Unit: unit, Scope: scope, Remaining: n}, err
	}
	at := func(pointer string) (any, error) {
		v := root
		for _, part := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
			part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
			switch x := v.(type) {
			case map[string]any:
				var ok bool
				v, ok = x[part]
				if !ok {
					return nil, errors.New("balance JSON pointer is missing")
				}
			case []any:
				i, err := strconv.Atoi(part)
				if err != nil || i < 0 || i >= len(x) {
					return nil, errors.New("balance JSON array index is invalid")
				}
				v = x[i]
			default:
				return nil, errors.New("balance JSON pointer does not reach a number")
			}
		}
		return v, nil
	}
	if policy.BalancePointer != "" {
		v, err := at(policy.BalancePointer)
		if err != nil {
			return nil, err
		}
		m, err := metric("balance:custom", "Custom balance", policy.BalanceUnit, "declared-by-user", v)
		if err != nil {
			return nil, err
		}
		return []quotaguard.Metric{m}, nil
	}
	// A custom URL is not assumed to implement the official host's schema.
	if p.BalanceURL != "" {
		return nil, nil
	}
	hosts := []string{hostOf(p.Chat), hostOf(p.Responses), hostOf(p.Anthropic)}
	for _, h := range hosts {
		switch h {
		case "api.deepseek.com":
			v, err := at("/balance_infos")
			if err != nil {
				return nil, err
			}
			rows, ok := v.([]any)
			if !ok {
				return nil, errors.New("balance_infos is not an array")
			}
			var out []quotaguard.Metric
			for _, row := range rows {
				obj, ok := row.(map[string]any)
				if !ok {
					return nil, errors.New("invalid balance record")
				}
				unit, ok := obj["currency"].(string)
				if !ok || unit == "" {
					return nil, errors.New("balance currency is missing")
				}
				unit = strings.ToUpper(unit)
				m, err := metric("balance:"+unit, "Account balance", unit, "account", obj["total_balance"])
				if err != nil {
					return nil, err
				}
				out = append(out, m)
			}
			return out, nil
		case "api.moonshot.cn", "api.moonshot.ai":
			unit := "CNY"
			if h == "api.moonshot.ai" {
				unit = "USD"
			}
			v, err := at("/data/available_balance")
			if err != nil {
				return nil, err
			}
			m, err := metric("balance:"+unit, "Account balance", unit, "account", v)
			if err != nil {
				return nil, err
			}
			return []quotaguard.Metric{m}, nil
		}
	}
	return nil, nil
}

// RefreshQuotaGuard reads through Magpie's existing sources/auth/proxy, never
// through a second vendor URL registry. Failed reads retain prior observations
// with their original timestamps; they are not stamped as newly successful.
func RefreshQuotaGuard(ctx context.Context, p Provider, preview *quotaguard.Policy) ([]quotaguard.Snapshot, error) {
	subject, e, err := quotaGuardEntry(p)
	if err != nil {
		return nil, err
	}
	policy := e.Policy
	if preview != nil {
		policy = *preview
	}
	capture := &guardCapture{policy: policy}
	ctx, cancel := context.WithTimeout(context.WithValue(ctx, guardCaptureKey{}, capture), 8*time.Second)
	defer cancel()
	if p.Account != nil {
		q, ok := LoginUsage(ctx, p.Account.UsageAgent())[p.Account.User]
		if !ok || q.Error != "" {
			return nil, errors.New("subscription quota could not be read")
		}
		at := q.ReadAt
		if q.AsOf != nil {
			at = q.AsOf
		}
		if at == nil {
			return nil, errors.New("subscription quota has no trustworthy observation time")
		}
		snap := quotaguard.Snapshot{Source: "windows", SourceVersion: quotaSourceVersion(p), StartedAt: *at, ObservedAt: *at, Metrics: guardWindowMetrics(q.Windows)}
		if e.Policy.Enabled {
			if err = quotaGuardStore().Observe(subject, e.Revision, snap, time.Now()); err != nil && !errors.Is(err, quotaguard.ErrConflict) {
				return nil, err
			}
		}
		return []quotaguard.Snapshot{snap}, nil
	}
	var failures []error
	discover := preview != nil || !e.Policy.Enabled
	needsWindows, needsBalance := discover, discover
	for _, rule := range e.Policy.Rules {
		needsWindows = needsWindows || strings.HasPrefix(rule.Metric, "window:")
		needsBalance = needsBalance || strings.HasPrefix(rule.Metric, "balance:")
	}
	if src, ok := planQuotaSourceOf(p); ok && !sub2APIKeyLimits(p) && needsWindows {
		if _, _, _, err = planKeyWindows(ctx, p, src, p.Key); err != nil {
			failures = append(failures, err)
		}
	}
	if _, ok := balanceSourceOf(p); ok && (needsBalance || needsWindows && sub2APIKeyLimits(p)) {
		if _, _, _, _, _, _, err = balanceRead(ctx, p); err != nil {
			failures = append(failures, err)
		}
	}
	if len(capture.snapshots) == 0 {
		return nil, errors.New("no machine-readable quota was obtained; this provider may need a supported source or explicit raw balance mapping")
	}
	// A successful plan reading does not conceal a failing balance query.
	return capture.snapshots, errors.Join(failures...)
}

var quotaGuardRefresh struct {
	sync.Mutex
	calls map[string]*guardRefreshCall
}

type guardRefreshCall struct {
	done  chan struct{}
	ended time.Time
	err   error
}

func refreshQuotaGuardOnce(ctx context.Context, p Provider, id string, forceFresh bool) error {
	// Include the data directory: isolated instances/tests cannot share reads.
	_, entry, readErr := quotaGuardEntry(p)
	if readErr != nil {
		return readErr
	}
	cacheID := guardReadID(p, id, entry.Revision)
	quotaGuardRefresh.Lock()
	if quotaGuardRefresh.calls == nil {
		quotaGuardRefresh.calls = map[string]*guardRefreshCall{}
	}
	if prior := quotaGuardRefresh.calls[cacheID]; prior != nil {
		if prior.ended.IsZero() {
			quotaGuardRefresh.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-prior.done:
				return prior.err
			}
		}
		// Cached mode coalesces very recent completed reads. Near/strict
		// attempt modes require a new observation for this physical send;
		// simultaneous sends still share an in-flight read to avoid a burst.
		if !forceFresh && time.Since(prior.ended) < 5*time.Second {
			quotaGuardRefresh.Unlock()
			return prior.err
		}
	}
	call := &guardRefreshCall{done: make(chan struct{})}
	quotaGuardRefresh.calls[cacheID] = call
	quotaGuardRefresh.Unlock()
	_, err := RefreshQuotaGuard(ctx, p, nil)
	quotaGuardRefresh.Lock()
	call.err = err
	call.ended = time.Now()
	close(call.done)
	quotaGuardRefresh.Unlock()
	return err
}

// GuardFailureFor returns a safe local error classification for all gateway
// entry points without string matching vendor errors.
func GuardFailureFor(err error) (status int, code, message string) {
	var g *QuotaGuardError
	if errors.As(err, &g) {
		return g.Status(), g.Code(), g.Error()
	}
	return 503, "upstream_quota_unknown", "upstream_quota_unknown: quota protection is unavailable"
}

// QuotaReadContext is private to provider quota readers. No client header or
// body can request this exemption. A protected key must still read its quota.
type quotaReadContextKey struct{}

func quotaReadContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, quotaReadContextKey{}, true)
}

type quotaSendReportKey struct{}
type quotaSendReport struct {
	sync.Mutex
	err error
}

// WithQuotaSendReport keeps local denials distinguishable after an upstream
// helper has formatted an error. It belongs to this attempt, not a global key.
func WithQuotaSendReport(ctx context.Context) context.Context {
	return context.WithValue(ctx, quotaSendReportKey{}, &quotaSendReport{})
}
func QuotaSendFailure(ctx context.Context) error {
	r, _ := ctx.Value(quotaSendReportKey{}).(*quotaSendReport)
	if r == nil {
		return nil
	}
	r.Lock()
	defer r.Unlock()
	return r.err
}

// guardBeforeSend is the second gate at Provider.Do, reached on each Magpie
// HTTP retry and on direct provider calls, not just when a route is planned.
// A transport call already admitted is in flight and is not canceled.
func guardBeforeSend(ctx context.Context, p Provider) error {
	if read, _ := ctx.Value(quotaReadContextKey{}).(bool); read {
		return nil
	}
	err := checkQuotaGuard(ctx, p, true)
	if err != nil {
		if report, _ := ctx.Value(quotaSendReportKey{}).(*quotaSendReport); report != nil {
			report.Lock()
			report.err = err
			report.Unlock()
		}
	}
	return err
}

// AdmitQuotaAttempt records a typed local denial for the gateway's retry loop.
func AdmitQuotaAttempt(ctx context.Context, p Provider) error { return guardBeforeSend(ctx, p) }
