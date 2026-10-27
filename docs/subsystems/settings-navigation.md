# Object and field settings navigation

The native settings palette locates an existing page, object and control. A search
result only navigates, reveals and focuses: it never submits a setting, deletes
an object, toggles a key or grants quota permission.

`settings-navigation-core.js` owns descriptor matching and the registry. The DOM
adapter in `settings-navigation.js` reuses `show`, `openProvider`, existing Settings
sections/search and provider drafts. No remote model service, input-value index
or search telemetry is involved.

## Keyboard and ownership

Ctrl/Cmd+Shift+P opens the global palette. Ctrl/Cmd+F in a provider editor opens
its field navigation. Enter during IME composition does not activate a result.
Escape returns focus to the previous control, not to an unrelated page.

Provider IDs identify objects even when labels match. Re-entering the same editor
preserves its draft. Switching objects follows the existing discard-confirmation
path. Async list results and navigation targets belong to their invocation; a
closed palette must not be resurrected by a late response.

## Coverage

The initial pre-render provider descriptors cover price rate, concurrency, RPM,
queue size and queue wait. Labeled controls in the current editor and rendered
Settings rows with stable IDs can be discovered locally. Existing page and
Settings-section navigation is reused.

This is not a claim of complete indexing of all unrendered settings. Core modules
may register stable descriptors through `magpieSettingsNav.register`; registration
functions must remain navigation-only. The coverage export lists IDs and paths,
not secrets. Missing/unavailable controls report their absence rather than
activating a vaguely matching control.

## Verification

`internal/gui/tests/settings-navigation-core.test.cjs` covers descriptor search,
Chinese object-plus-price-rate matching, duplicate object names, replacement and
secret exclusion. Browser and native-host tests must additionally prove disclosure,
focus, IME, draft preservation, cancellation and absence of settings writes.
The prepared source candidate has not run those tests.
