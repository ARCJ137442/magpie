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

The accompanying source candidate has not been built or tested. Before release,
audit all HTTP/SDK/stream/compaction/search/image/decision senders and retry paths,
verify backup/restore/sync policy (do not sync active human overrides), and test
native GUI draft/confirmation behavior. Unregistered or unverified paths must not
be advertised as a completed economic-safety guarantee.

Verification sources: `internal/quotaguard/*_test.go`,
`internal/provider/quota_guard_test.go`, and
`internal/gateway/quota_guard_test.go`. The central integration assertion is zero
model-upstream requests after protection, not merely a particular HTTP status.
