# Upstream credential reserve protection

This opt-in core feature stops new upstream attempts once an accepted quota
observation reaches a configured reserve. It does not predict a request's cost,
reserve future usage, cancel an in-flight response or guarantee a remote balance
floor. A vendor reading may itself lag the vendor's accounting.

## Independent state

The user's enabled switch, reserve protection and vendor health are independent.
`quotaguard.Decision` is their local quota projection, not a replacement for the
routing engine's health and eligibility. A protected last key is still protected.
No routing mode, affinity, fallback or probe grants quota permission.

A trip is latched across restarts. Recovery alone does not clear it. A person may
change the policy, re-arm after a fresh recovered observation, or grant a bounded
override. The GUI offers 15 minutes; the API permits at most 24 hours. An override
never enables a manually disabled key or makes unknown data valid.

## Refresh sensitivity

Refresh sensitivity belongs to the opt-in quota policy for a credential. It does
not change how the Usage page or ordinary provider balance cards refresh. The
quota editor initially shows its saved observation; its refresh action asks the
existing upstream balance or plan reader again. A gateway attempt also refreshes
when its saved observation is unknown or older than the policy's maximum age.

- **Cached** is the default for existing and new policies: an accepted reading
  may be reused until `maxAgeSeconds` (60 seconds by default; configurable from
  5 to 600).
- **Near reserve** lets each rule set `refreshWithin` in that metric's unit. When
  a fresh reading is at or below `minimum + refreshWithin`, the last common
  sender gate asks for another reading before each physical attempt.
- **Every attempt** asks for a fresh reading before each physical attempt,
  regardless of distance from the reserve.

The common sender applies near/strict refreshes to retries as well as the first
send. Simultaneous checks for one credential share an in-flight quota read to
avoid bursting the vendor's usage endpoint. They do not reserve future spend or
serialize model requests, so parallel in-flight consumption can still cross a
reserve. A reading may also lag the vendor's own accounting.

## Observation and storage

`internal/quotaguard` contains the rule evaluator and revisioned persistent store.
`internal/provider/quota_guard.go` adapts existing plan windows and raw numeric
balance responses. Amounts are fixed-point decimal text, not formatted UI strings.
Currency and scope stay explicit; an account balance is not per-key money.

The actual source read captures the policy revision and source fingerprint before
network I/O. Late results cannot overwrite a newer policy. A preview mapping is
parsed separately and does not replace the active mapping. Failed or stale data
is unknown, never zero and never an automatic authorization.

State is local to `appdir.Config()/quota-guards.json`, with an enablement marker.
The store checks OS-lock ownership and fails closed on unreadable or missing
previously configured state. It does not hold the lock during network I/O.

## Admission

The gateway candidate loop checks quota before an attempt and after queue/RPM
waits. `Provider.Do` checks before each call through the common sender; its file
name, `plugins.go`, does not make this implementation a plugin. Quota-reader
contexts carry an internal exemption which caller headers cannot set. Kimi Code's
direct search sender has an explicit gate. Local policy denials must not become
vendor failures, health backoffs, model retirement or last-key retries.

## Compatibility and release verification

Existing account caps are not silently migrated to latched semantics. The first
editor integration is for API keys; subscription migration needs explicit behavior
tests and a user-visible compatibility decision.

## Verification and remaining coverage

The rule evaluator, persistent store, provider adapter and selected gateway paths have focused tests. The central integration assertion is zero model-upstream requests after protection, not merely a particular HTTP status.

```sh
go test -tags nogui ./internal/quotaguard
go test -tags nogui ./internal/provider -run 'TestGuard'
go test -tags nogui ./internal/gateway -run 'TestQuotaGuard'
```

These checks do not cover every sender. Before treating the guard as a general spending limit, audit HTTP, SDK, streaming, compaction, search, image and decision paths, including internal retry boundaries and error classification. Also define backup, restore and sync behavior; active human overrides should not be extended to another device automatically. Test GUI confirmation and cross-process updates. Unverified paths must not be presented as covered.
