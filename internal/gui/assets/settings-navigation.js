// Navigation targets reveal existing UI. They never activate a setting's action.
(() => {
  "use strict";
  const core = window.MagpieNavigationCore, registry = new core.Registry();
  const prefs = window.magpieNavigationPrefs;
  const tr = (text, args) => t(text, args);
  const make = (tag, cls, text) => {
    const node = document.createElement(tag);
    if (cls) node.className = cls;
    if (text !== undefined) node.textContent = text;
    return node;
  };
  const interactive = 'input:not([type=hidden]),textarea,button,[role=switch],[role=combobox],[tabindex="0"],summary';
  const isHiddenFeature = node => featureHides(node);
  let panel, query, results, notice, help, options, footer;
  let entries = [], cursor = 0, owner = 0, scope = "global", returnFocus, composing = false;
  let navigation = null, recording = false;

  function focusTarget(control, event) {
    if (!control?.isConnected || control.disabled || isHiddenFeature(control)) throw new Error(tr("This setting is not available in the current context"));
    for (let node = control.parentElement; node; node = node.parentElement) if (node.tagName === "DETAILS") node.open = true;
    if (!control.getClientRects().length) throw new Error(tr("This setting is not available in the current context"));
    if (event && scrollOnPurpose(event, 2500)) control.scrollIntoView({ block: "center", behavior: "auto" });
    control.focus({ preventScroll: true });
    control.classList.add("nav-focus");
    setTimeout(() => control.classList.remove("nav-focus"), 1500);
    return true;
  }
  function waitFor(find, signal, within = 2500) {
    return new Promise((resolve, reject) => {
      if (signal?.aborted) { reject(new DOMException("Canceled", "AbortError")); return; }
      const now = find();
      if (now) { resolve(now); return; }
      let timer;
      const observer = new MutationObserver(() => { const node = find(); if (node) { finish(); resolve(node); } });
      const abort = () => { finish(); reject(new DOMException("Canceled", "AbortError")); };
      function finish() { observer.disconnect(); clearTimeout(timer); signal?.removeEventListener("abort", abort); }
      observer.observe(document.body, { childList: true, subtree: true });
      signal?.addEventListener("abort", abort, { once: true });
      timer = setTimeout(() => { finish(); reject(new Error(tr("This setting is not available in the current context"))); }, within);
    });
  }
  async function revealProvider(id, field, event) {
    navigation?.abort();
    const run = new AbortController(); navigation = run;
    if (!(view === "providers" && editing === id)) await openProvider(id);
    if (run.signal.aborted || editing !== id || view !== "providers") return false;
    if (field?.advanced) { advanced = { of: editing, open: true }; renderProviders(); }
    const control = await waitFor(() => {
      if (editing !== id) return null;
      const editor = document.querySelector("#modal .editor");
      return field ? editor?.querySelector(field.selector) : editor?.querySelector(interactive);
    }, run.signal);
    if (!run.signal.aborted && editing === id) return focusTarget(control, event);
    return false;
  }
  function collectControls(root, { group, object = "", page = "", scope = "global" }) {
    const found = [], seen = new Set();
    for (const control of root.querySelectorAll(interactive)) {
      if (control.disabled || isHiddenFeature(control) || control.closest("[hidden]")) continue;
      const row = control.closest(".row.pref"), label = control.id ? root.querySelector('label[for="' + CSS.escape(control.id) + '"]') : null;
      const labelledBy = (control.getAttribute("aria-labelledby") || "").split(/\s+/)
        .map(id => document.getElementById(id)?.textContent || "").join(" ").trim();
      const title = labelledBy || control.getAttribute("aria-label") || label?.textContent.trim() || row?.querySelector(".name")?.textContent.trim();
      const stable = control.id || row?.id || row?.dataset.setting;
      if (!title || !stable || seen.has(stable)) continue;
      seen.add(stable);
      found.push({ id: group + ":" + stable, scope, title, object, page, pageId: root.closest(".view")?.id.replace("view-", "") || "providers",
        type: control.matches("input,textarea") ? "input" : "control",
        path: [page, object].filter(Boolean).join(" › "), keywords: [],
        run: event => focusTarget(control, event) });
    }
    return found;
  }
  function mountEditor(editor, provider) {
    registry.replace("current-editor", collectControls(editor, { group: "editor:" + (provider?.id || "current"),
      object: provider?.name || "", page: tr("Providers"), scope: "editor" }));
    if (editor.querySelector(".settings-find-editor")) return;
    const button = make("button", "text settings-find-editor", tr("Find a setting in this editor"));
    button.type = "button"; button.onclick = event => open(event, "editor");
    editor.querySelector(".ehead")?.append(button);
    editor.addEventListener("keydown", event => {
      if (event.isComposing || event.altKey || event.shiftKey || !(event.ctrlKey || event.metaKey) || event.key.toLowerCase() !== "f") return;
      event.preventDefault(); event.stopPropagation(); open(event, "editor");
    }, true);
  }
  function addPageEntries() {
    const pages = [], tabs = [];
    for (const button of document.querySelectorAll("#nav button[data-view],#prefs")) {
      const id = button.dataset.view || "settings", page = document.getElementById("view-" + id);
      if (!page || button.hidden || button.dataset.replaced || isHiddenFeature(button) || isHiddenFeature(page)) continue;
      if (gatewayMode && GATEWAY_HIDES.includes(id)) continue;
      const nameNode = button.querySelector("[data-t]") || button.querySelector("span");
      const title = nameNode?.textContent.trim() || button.getAttribute("aria-label") || button.title;
      if (!title) continue;
      pages.push({ id: "page:" + id, scope: "global", type: "page", title, object: "", page: title, pageId: id,
        path: tr("Pages"), keywords: [id], run: () => show(id) });
      // A page can expose its existing tab directory before its DOM is drawn.
      // Rendered strips otherwise reuse the sidebar's own source and handlers.
      const declared = magpie.page(id)?.navigationTabs?.();
      if (declared) {
        for (const tab of declared) tabs.push({ id: "tab:" + id + ":" + tab.id, scope: "global", type: "page",
          title: tr(tab.title), object: title, page: title, pageId: id, path: title, keywords: [id, tab.id, tab.title],
          run: async () => { if (!await show(id)) return false; return tab.run(); } });
      } else {
        const strip = typeof NAV_SUBS === "object" && NAV_SUBS[id]?.();
        if (!strip) continue;
        for (const tab of strip.querySelectorAll(":scope > button")) {
          if (tab.hidden || isHiddenFeature(tab)) continue;
          const key = tab.dataset.navKey || tab.id;
          if (!key) continue;
          const name = tab.textContent.trim().split(" · ")[0];
          tabs.push({ id: "tab:" + id + ":" + key, scope: "global", type: "page", title: name, object: title,
            page: title, pageId: id, path: title, keywords: [id, key], run: async () => {
              if (!await show(id)) return false;
              if (id === "settings") { const search = document.getElementById("setQ"); if (search) search.value = ""; searchSettings(""); }
              const live = [...(NAV_SUBS[id]?.()?.querySelectorAll(":scope > button") || [])].find(node => (node.dataset.navKey || node.id) === key);
              if (!live || live.hidden || isHiddenFeature(live)) throw new Error(tr("This setting is not available in the current context"));
              live.click(); return true;
            } });
        }
      }
    }
    registry.replace("pages", pages); registry.replace("tabs", tabs);
  }
  function collectSettings() {
    const root = document.getElementById("view-settings"), found = [];
    if (!root) return;
    for (const row of root.querySelectorAll(".row.pref")) {
      if (isHiddenFeature(row)) continue;
      const section = row.closest(".set-page"), label = row.querySelector(".name");
      const title = label?.textContent.trim(), stable = row.id || row.querySelector("[id]")?.id;
      if (!section || !title || !stable) continue;
      const tab = section.id.replace("setPage-", "");
      found.push({ id: "setting:" + stable, scope: "global", type: "control", title, object: tr("Settings"), page: tr("Settings"), pageId: "settings",
        path: tr("Settings") + " › " + (document.getElementById("setTab-" + tab)?.textContent.trim() || tab), keywords: [tab, label.dataset.en || ""],
        run: async event => {
          if (!await show("settings")) return false;
          const search = document.getElementById("setQ"); if (search) search.value = ""; searchSettings(""); setSetTab(tab, true);
          const anchor = document.getElementById(stable), live = anchor?.matches(".row.pref") ? anchor : anchor?.closest(".row.pref");
          return focusTarget(live?.querySelector(interactive), event);
        } });
    }
    registry.replace("settings-fields", found);
  }
  function refreshCatalog() {
    addPageEntries(); collectSettings();
    registry.replace("navigation-preferences", [{ id: "navigation:preferences", scope: "global", type: "page", title: tr("Quick navigation"),
      object: "", page: tr("Settings"), pageId: "settings", path: tr("Navigation preferences"),
      keywords: ["设置搜索", "settings search", "快捷键", "shortcut"], run: async () => { await open(); if (options?.hidden) showOptions(); return true; } }]);
    const editor = document.querySelector("#modal:not([hidden]) .editor");
    registry.remove("current-editor"); registry.remove("current-page");
    if (editor) mountEditor(editor, providers?.providers?.find(provider => provider.id === editing));
    const page = document.getElementById("view-" + view);
    if (page && view !== "settings") registry.replace("current-page", collectControls(page, {
      group: "element:" + view, page: magpie.page(view)?.title ? tr(magpie.page(view).title) : view }));
  }
  function showOptions() {
    options.hidden = !options.hidden;
    panel.querySelector(".settings-command-config").setAttribute("aria-expanded", String(!options.hidden));
  }
  function preferencesUI() {
    const box = make("div", "settings-command-options"); box.hidden = true;
    const shortcutRow = make("label", "", tr("Navigation shortcut"));
    const shortcut = make("button", "field nav-shortcut", prefs.label() || tr("Disabled")); shortcut.type = "button";
    shortcut.onclick = () => { recording = !recording; shortcut.textContent = recording ? tr("Press a shortcut") : prefs.label() || tr("Disabled"); };
    shortcut.onkeydown = event => {
      if (!recording || ["Control", "Meta", "Alt", "Shift"].includes(event.key)) return;
      event.preventDefault(); event.stopPropagation();
      if (event.key === "Escape") { recording = false; shortcut.textContent = prefs.label() || tr("Disabled"); return; }
      if (!(event.ctrlKey || event.metaKey || event.altKey) || event.isComposing) return;
      if ((event.ctrlKey || event.metaKey) && !event.altKey && !event.shiftKey && /^[0-9,fxcvzay+=_-]$/i.test(event.key)) {
        shortcut.textContent = tr("Choose another shortcut"); return;
      }
      const keys = [event.ctrlKey && "Ctrl", event.metaKey && "Meta", event.altKey && "Alt", event.shiftKey && "Shift", event.key.toUpperCase()].filter(Boolean);
      prefs.update({ shortcut: keys.join("+") }); recording = false; shortcut.textContent = prefs.label(); render(true);
    };
    shortcutRow.append(shortcut);
    const rowLabel = make("label", "", tr("Visible results"));
    const rows = make("input", "set-field nav-rows"); rows.type = "number"; rows.min = "4"; rows.max = "20"; rows.step = "1"; rows.value = prefs.get().rows;
    rows.setAttribute("aria-label", tr("Visible results"));
    rows.onchange = () => {
      const value = Number(rows.value);
      if (!Number.isInteger(value) || value < 4 || value > 20) { rows.value = prefs.get().rows; return; }
      prefs.update({ rows: value }); render(true);
    };
    rowLabel.append(rows);
    const reset = make("button", "text", tr("Reset navigation preferences")); reset.type = "button";
    reset.onclick = () => { prefs.update({ shortcut: "Mod+K", rows: 8 }); recording = false; shortcut.textContent = prefs.label(); rows.value = 8; render(true); };
    const clear = make("button", "text", tr("Clear navigation history")); clear.type = "button";
    clear.onclick = () => { prefs.update({ recent: [] }); render(false); };
    box.append(shortcutRow, rowLabel, reset, clear);
    return box;
  }
  function ensurePanel() {
    if (panel) return;
    panel = make("dialog", "settings-command"); panel.setAttribute("aria-labelledby", "settings-command-title");
    const head = make("div", "settings-command-head"), title = make("h2", "", tr("Quick navigation")); title.id = "settings-command-title";
    const config = make("button", "icon settings-command-config"); config.type = "button";
    config.setAttribute("aria-label", tr("Navigation preferences")); config.title = tr("Navigation preferences"); config.setAttribute("aria-expanded", "false");
    config.innerHTML = '<svg viewBox="0 0 16 16" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" aria-hidden="true"><path d="M2 4h12M2 12h12"/><circle cx="6" cy="4" r="2" fill="var(--card)"/><circle cx="10" cy="12" r="2" fill="var(--card)"/></svg>';
    config.onclick = showOptions; head.append(title, config);
    query = make("input", "words"); query.type = "search"; query.autocomplete = "off"; query.spellcheck = false;
    query.setAttribute("role", "combobox"); query.setAttribute("aria-autocomplete", "list");
    query.setAttribute("aria-controls", "settings-command-results"); query.setAttribute("aria-expanded", "true");
    query.setAttribute("aria-label", tr("Search objects and settings")); query.placeholder = tr("Search pages, objects and controls");
    results = make("div", "settings-command-results"); results.id = "settings-command-results"; results.setAttribute("role", "listbox");
    notice = make("p", "hint settings-command-notice"); notice.setAttribute("role", "status");
    help = make("div", "settings-command-help"); help.hidden = true;
    help.append(make("p", "", tr("Search names or abbreviations. Spaces combine terms; | matches either; ! excludes.")),
      make("code", "", 'page:providers object:DeepSeek queue !wait'),
      make("p", "", tr('Use quotes for a phrase, for example "queue wait". Filters: page:, object:, type:.')));
    options = preferencesUI();
    footer = make("div", "settings-command-footer");
    panel.append(head, query, options, results, help, notice, footer); document.body.append(panel);
    query.addEventListener("compositionstart", () => { composing = true; });
    query.addEventListener("compositionend", () => { composing = false; render(false); });
    query.addEventListener("input", () => { if (!composing) render(false); });
    panel.addEventListener("keydown", event => {
      event.stopPropagation();
      if (event.isComposing || composing) return;
      if (recording && options.contains(event.target)) return;
      if (event.key === "Escape") { event.preventDefault(); dismiss(true); return; }
      if (event.target !== query) return;
      if ((event.ctrlKey || event.metaKey) && !event.altKey && !event.shiftKey && /^[1-9]$/.test(event.key)) {
        event.preventDefault(); const at = Number(event.key) - 1;
        if (at < entries.length) { cursor = at; activate(event); } return;
      }
      if (event.key === "ArrowDown" || event.key === "ArrowUp") {
        event.preventDefault(); cursor = (cursor + (event.key === "ArrowDown" ? 1 : -1) + entries.length) % Math.max(1, entries.length); select();
      }
      if (event.key === "Enter") { event.preventDefault(); activate(event); }
    });
    panel.addEventListener("cancel", event => { event.preventDefault(); dismiss(true); });
    const outside = backdropClick(panel, event => {
      const rect = panel.getBoundingClientRect();
      return event.target === panel && (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom);
    });
    panel.addEventListener("click", event => { if (outside(event)) dismiss(true); });
  }
  function select() {
    [...results.children].forEach((node, index) => node.setAttribute("aria-selected", String(index === cursor)));
    const active = results.children[cursor];
    if (active) { query.setAttribute("aria-activedescendant", active.id); active.scrollIntoView({ block: "nearest" }); }
    else query.removeAttribute("aria-activedescendant");
  }
  function render(keep = true) {
    if (!panel?.open) return;
    const selected = keep ? entries[cursor]?.id : null;
    const showingHelp = query.value.trim() === "?";
    help.hidden = !showingHelp; results.hidden = showingHelp;
    entries = showingHelp ? [] : registry.search(query.value, scope === "editor" ? "editor" : null, 200, prefs.get().recent);
    cursor = Math.max(0, entries.findIndex(entry => entry.id === selected)); results.replaceChildren();
    panel.style.setProperty("--nav-rows", prefs.get().rows);
    const mod = /^Mac/.test(navigator.platform) ? "⌘" : "Ctrl+";
    entries.forEach((entry, index) => {
      const option = make("div", "settings-command-result"); option.id = "settings-command-option-" + index; option.dataset.target = entry.id;
      option.setAttribute("role", "option");
      const text = make("div", "settings-command-label");
      text.append(make("div", "settings-command-name", entry.title), make("div", "hint", entry.path));
      option.append(text);
      if (index < 9) option.append(make("kbd", "", mod + (index + 1)));
      option.onmousedown = event => event.preventDefault();
      option.onclick = event => { cursor = index; activate(event); }; results.append(option);
    });
    notice.hidden = showingHelp || !!entries.length;
    notice.textContent = tr("No matching setting; try an object name or a shorter term");
    footer.replaceChildren(make("span", "", tr("↑↓ choose · Enter go · {keys}1…9 · ? syntax", { keys: mod })),
      make("span", "", query.value ? String(entries.length) : prefs.get().recent.length ? tr("Recent destinations first") : ""));
    select();
  }
  function dismiss(restore) {
    owner++; panel?.close(); panel?.remove();
    panel = query = results = notice = help = options = footer = null; composing = recording = false;
    if (restore && returnFocus?.isConnected) returnFocus.focus({ preventScroll: true });
  }
  async function activate(event) {
    if (composing || !entries[cursor]) return;
    const entry = entries[cursor]; dismiss(false);
    try {
      if (await entry.run(event) === false) return;
      prefs.update({ recent: [entry.id, ...prefs.get().recent.filter(id => id !== entry.id)].slice(0, 30) });
    } catch (error) { if (error.name !== "AbortError") status(error.message, "err"); }
  }
  async function open(event, nextScope = "global") {
    navigation?.abort();
    if (panel?.open) { query.focus(); return; }
    returnFocus = document.activeElement; scope = nextScope; refreshCatalog(); ensurePanel();
    const token = ++owner; query.value = ""; panel.showModal(); query.focus(); render(false);
    if (nextScope === "editor") return;
    try {
      const current = await api("providers");
      if (token !== owner || !panel?.open) return;
      registry.replace("providers", core.providerDescriptors(current.providers, tr, revealProvider));
      addPageEntries(); render(true);
    } catch {
      if (token === owner && panel?.open) { notice.hidden = false; notice.textContent = tr("Provider list unavailable; existing navigation results are still shown"); }
    }
  }
  window.magpieSettingsNav = { mountEditor, open, close: () => dismiss(true),
    register: (name, descriptors) => registry.replace("extension:" + name, descriptors),
    remove: name => registry.remove("extension:" + name),
    coverage: () => registry.entries().map(({ id, path, scope, type }) => ({ id, path, scope, type })) };
})();
