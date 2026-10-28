// The shell's entry point is small; search and its words load on first use.
(() => {
  magpie.strings({ zh: { "Quick navigation": "快速导航" }, "zh-TW": { "Quick navigation": "快速導覽" }, ja: { "Quick navigation": "クイック移動" }, de: { "Quick navigation": "Schnellnavigation" } });
  let loading = null, requested = false, request = 0;
  const pending = new Map();
  const key = "magpie.quickNavigation", mac = /^Mac/.test(navigator.platform);
  const defaults = { shortcut: "Mod+K", rows: 8, recent: [] };
  let prefs = defaults;
  try {
    const saved = JSON.parse(localStorage.getItem(key));
    if (saved && typeof saved === "object") prefs = { ...defaults, ...saved };
  } catch { /* unavailable storage uses this window's defaults */ }
  if (!Number.isInteger(prefs.rows) || prefs.rows < 4 || prefs.rows > 20) prefs.rows = defaults.rows;
  if (typeof prefs.shortcut !== "string") prefs.shortcut = defaults.shortcut;
  if (!Array.isArray(prefs.recent)) prefs.recent = [];
  prefs.recent = prefs.recent.filter(id => typeof id === "string").slice(0, 30);
  function matches(event) {
    const parts = prefs.shortcut.toLowerCase().replace("mod", mac ? "meta" : "ctrl").split("+");
    return !!prefs.shortcut && event.key.toLowerCase() === parts.at(-1)
      && event.ctrlKey === parts.includes("ctrl") && event.metaKey === parts.includes("meta")
      && event.altKey === parts.includes("alt") && event.shiftKey === parts.includes("shift");
  }
  const label = () => prefs.shortcut.replace("Mod", mac ? "⌘" : "Ctrl").replace("Meta", "⌘");
  function update(change) {
    prefs = { ...prefs, ...change };
    try { localStorage.setItem(key, JSON.stringify(prefs)); } catch { /* usable without persistence */ }
    const button = document.querySelector(".settings-command-open");
    if (button) button.title = t("Quick navigation") + (label() ? ` (${label()})` : "");
  }
  window.magpieNavigationPrefs = { get: () => prefs, update, label, matches };
  function script(src) {
    return new Promise((resolve, reject) => {
      const element = document.createElement("script");
      element.src = src;
      element.onload = resolve;
      element.onerror = () => { element.remove(); reject(new Error(src)); };
      document.head.append(element);
    });
  }
  async function open(event, scope) {
    const token = ++request; requested = true;
    try {
      if (!loading) loading = (async () => {
        if (!window.MagpieNavigationCore) await script("settings-navigation-core.js");
        if (!window.magpieNavigationStrings) await script("settings-navigation-i18n.js");
        await script("settings-navigation.js");
      })().catch(error => { loading = null; throw error; });
      await loading;
      if (token !== request) return;
      requested = false;
      for (const [name, descriptors] of pending) window.magpieSettingsNav.register(name, descriptors);
      pending.clear();
      update({});
      return window.magpieSettingsNav.open(event, scope);
    } catch (error) { if (token === request) { requested = false; status(error.message, "err"); } }
  }
  function mountEditor(editor) {
    if (editor.querySelector(".settings-find-editor")) return;
    const button = document.createElement("button");
    button.type = "button";
    button.className = "text settings-find-editor";
    // The settings search's existing translation is available before lazy load.
    button.textContent = t("Quick navigation");
    button.onclick = event => open(event, "editor");
    editor.querySelector(".ehead")?.append(button);
    editor.addEventListener("keydown", event => {
      if (event.isComposing || event.altKey || event.shiftKey || !(event.ctrlKey || event.metaKey) || event.key.toLowerCase() !== "f") return;
      event.preventDefault(); event.stopPropagation(); open(event, "editor");
    }, true);
  }
  window.magpieSettingsNav = { open, mountEditor, close: () => { request++; requested = false; },
    register: (name, descriptors) => pending.set(name, descriptors), remove: name => pending.delete(name),
    coverage: () => [...pending.values()].flat().map(({ id, path, scope }) => ({ id, path, scope })) };
  function boot() {
    if (mode === "panel") return;
    const button = document.createElement("button");
    button.type = "button"; button.className = "icon settings-command-open";
    button.setAttribute("aria-label", t("Quick navigation"));
    button.innerHTML = '<svg viewBox="0 0 16 16" width="15" height="15" aria-hidden="true"><circle cx="6.8" cy="6.8" r="4.4" fill="none" stroke="currentColor" stroke-width="1.5"/><path d="m10.2 10.2 3.6 3.6" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>';
    button.title = t("Quick navigation") + ` (${label()})`;
    button.onclick = event => open(event);
    document.querySelector("header.top .actions")?.prepend(button);
    document.addEventListener("keydown", event => {
      if (event.defaultPrevented || event.isComposing || event.repeat || confirmationPending || event.target.closest?.(".settings-command-options") || !matches(event)) return;
      event.preventDefault(); event.stopPropagation();
      if (requested) { request++; requested = false; }
      else if (document.querySelector(".settings-command")?.open) window.magpieSettingsNav.close();
      else open(event);
    }, true);
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", boot, { once: true });
  else queueMicrotask(boot);
})();
