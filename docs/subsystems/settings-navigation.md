# Quick navigation

The palette locates an existing page, object or named control. Choosing a result
navigates, reveals and focuses; it does not activate arbitrary controls or save
their values. Existing page and provider navigation still owns discard prompts.

## Source of truth and loading

The shell loads `settings-navigation-launcher.js`: its magnifier, configurable
shortcut and editor entry point. On first use it loads the pure matching core,
the feature's words and DOM adapter, in order. A failed load can be retried;
a canceled invocation cannot open a late palette. The search feature adds no
dependency, remote service, telemetry or credential-value index.

The page directory is derived from the existing navigation buttons, including
plugin pages. Rendered subpage strips reuse `NAV_SUBS` and their existing
handlers. A page may expose `navigationTabs()`, returning its existing directory
as `{id, title, run}` targets before rendering. Usage derives these targets from
`USAGE_TABS`; both its strip and navigation call `selectUsageTab`.
Common `segs` buttons expose their existing option id as `data-nav-key`.

Settings rows are found through their stable row/control ids. Current-page and
editor controls are discovered from labels, accessible names and settings-row
names, without reading input values. Five common provider fields still have
pre-render descriptors. An unrendered custom control with no schema or named
target is not automatically covered. Framed community pages expose their page,
not the frame's private DOM. New custom tabs can use the same page hook; they
must not maintain a second list of names for search.

## Query and order

Matching preserves original names and adds aliases. Continuous matches rank
above ordered English-letter subsequences; fuzzy matching is not spelling
correction or pinyin search. Quotes require a continuous phrase. Spaces combine
terms, `|` separates alternatives, and `!` excludes. `page:`, `object:` and
`type:` limit a term. Page filters accept the stable page id in every language.
Entering `?` displays help. Query text is not persisted.

Successful destinations are remembered by stable id, at most 30, in
`magpie.quickNavigation` in localStorage. Empty queries prefer recent entries;
nonempty queries compare text relevance before recency. Canceled and failed
navigation do not enter history. Clearing history affects this browser/webview
only. Async catalog reads preserve the selected target rather than selecting
a newly inserted row by its old index.

## Interaction and preferences

Ctrl/Cmd+K opens the palette by default, only while the app has keyboard focus.
The options button changes the binding and the desired visible rows (4–20),
resets preferences or clears recent destinations. These presentation preferences
are local to this browser/webview, like the shell's navigation layout; they do not
change gateway settings. Browser/OS reserved shortcuts may remain unavailable.

Arrow keys select, Enter goes, and Ctrl/Cmd+1–9 chooses the first nine results.
The list can scroll beyond the viewport-constrained desired row count. IME
confirmation does not activate results. Escape or an outside pointer click
cancels and restores focus; dragging a selection out over the backdrop is not an
outside click. The footer shows noninteractive key/syntax hints. The palette
stays centered horizontally below the title bar in narrow and wide windows.

The initial launcher retains the public `magpieSettingsNav` registration facade:
pre-load registrations are queued. `register` replaces a named group and
`remove` removes it; targets must remain navigation-only. `coverage` exposes ids,
paths, scopes and types, not credential values.

## Verification

- `node --test internal/gui/tests/settings-navigation-core.test.cjs internal/gui/tests/settings-navigation-strings.test.cjs`
- `node --test internal/gui/tests/settings-navigation-browser.test.cjs` with
  Playwright available; runs Chromium and WebKit unless BROWSER selects one.
- `go test -tags nogui ./internal/gui -run '^TestCoreScriptBudget$'`

Browser fixtures cover five languages, light/dark and narrow/wide layouts,
outside cancellation, short Chinese queries, numeric selection, recents, IME,
draft preservation, new page discovery, subpage navigation, preferences/reload,
and no settings writes. A passing browser test is not native-host acceptance;
the Windows desktop candidate still needs the user's real workflow test.
