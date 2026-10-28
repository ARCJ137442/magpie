// Core provider editor section. Not a gateway middleware/provider plugin.
(() => {
  "use strict";
  const sessions = new Map();
  const copy = (x) => JSON.parse(JSON.stringify(x));
  const blank = () => ({ enabled: false, rules: [], maxAgeSeconds: 60, refreshMode: "cached" });
  const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);
  const word = (s, args) => t(s, args);
  function node(tag, cls, text) {
    const n = document.createElement(tag); if (cls) n.className = cls;
    if (text !== undefined) n.textContent = text; return n;
  }
  function button(label, fn) {
    const b = node("button", "text", word(label)); b.type = "button";
    b.addEventListener("click", fn); return b;
  }
  function dirty(provider) {
    const s = sessions.get(provider);
    return !!s && [...s.keys.values()].some(k => k.policy && !same(k.policy, k.saved));
  }
  function discard(provider) {
    const s = sessions.get(provider); s?.controller?.abort(); sessions.delete(provider);
  }
  function mount(editor, p) {
    if (!p || p.account || !p.keyList?.length) return;
    let s = sessions.get(p.id);
    if (!s) { s = { keys: new Map(), selected: p.keyList[0].id, generation: 0 }; sessions.set(p.id, s); }
    if (!p.keyList.some(k => k.id === s.selected)) s.selected = p.keyList[0].id;
    s.controller?.abort(); s.controller = new AbortController(); s.generation++;
    for (const k of s.keys.values()) if (k.busy) {
      // Abort does not prove that a POST was not committed. Re-read before
      // accepting another action, while preserving the user's draft.
      k.busy = false; k.view = null;
      k.error = word("The action was not confirmed. Reload its state before retrying; unsaved values are kept.");
    }
    const generation = s.generation;
    const section = node("details", "more advanced quota-guard"); section.dataset.setting = "quota-guard";
    const initialSnapshots = p.quotaGuard?.snapshots || [];
    section.open = !!s.open || initialSnapshots.length > 0 || !!p.quotaGuard?.entry?.policy?.enabled;
    const summary = node("summary", "", word("Quota protection"));
    section.append(summary);
    const content = node("div", "quota-guard-content"); section.append(content);
    section.addEventListener("toggle", () => { s.open = section.open; });
    // Keep the editor's framed grid intact. Mount inside its body so the
    // section participates in the existing width/layout rules.
    const host = editor.querySelector("details.more.advanced .inner") || editor.querySelector(".ebody");
    if (!host) return;
    host.append(section);
    const help = node("p", "hint", word("Quota protection is saved separately. Provider Cancel does not undo a saved protection rule or a confirmed override."));
    content.append(help);
    const filter = node("input", "words"); filter.type = "search"; filter.placeholder = word("Find a key by name or fingerprint"); filter.setAttribute("aria-label", filter.placeholder);
    const keys = node("div", "quota-key-picker");
    content.append(filter, keys);
    const form = node("div", "quota-key-form"); content.append(form);
    const current = () => section.isConnected && s.generation === generation && sessions.get(p.id) === s;
    const keyState = () => {
      if (!s.keys.has(s.selected)) s.keys.set(s.selected, {});
      const k = s.keys.get(s.selected);
      if (!k.nearRanges) k.nearRanges = new Map();
      if (k.ageInvalid === undefined) k.ageInvalid = false;
      return k;
    };
    function drawKeys() {
      keys.replaceChildren();
      const query = filter.value.trim().toLocaleLowerCase();
      const matched = p.keyList.filter(k => [k.name, k.masked, k.id].some(v => String(v || "").toLocaleLowerCase().includes(query)));
      for (const k of matched.slice(0, 30)) {
        const b = node("button", "text", k.name || k.masked || k.id); b.type = "button";
        b.setAttribute("aria-pressed", String(k.id === s.selected));
        b.onclick = () => { s.selected = k.id; drawKeys(); draw(); load(); };
        keys.append(b);
      }
      if (!matched.length) keys.append(node("p", "hint", word("No matching key")));
      if (matched.length > 30) keys.append(node("p", "hint", word("Type more to narrow the key list")));
    }
    filter.oninput = drawKeys;
    function accept(k, v, replacePolicy) {
      k.view = v;
      if (replacePolicy || !k.policy) { const policy = v.entry.policy || {};
        k.saved = { ...blank(), ...copy(policy), rules: Array.isArray(policy.rules) ? copy(policy.rules) : [] };
        k.policy = copy(k.saved); }
    }
    async function load() {
      const id = s.selected, k = keyState(); if (k.view) return;
      try {
        const v = await api(`providers/quota-guard?provider=${encodeURIComponent(p.id)}&key=${encodeURIComponent(id)}`, undefined, undefined, s.controller.signal);
        if (!current() || s.selected !== id) return;
        accept(k, v, !k.policy); k.busy = false; draw();
      } catch (e) { if (current() && s.selected === id && e.name !== "AbortError") { k.error = word("Quota protection state could not be loaded"); draw(); } }
    }
    async function perform(action, extra = {}) {
      const id = s.selected, k = keyState(); if (k.busy || !k.view) return;
      if (action === "configure" && extra.policy?.refreshMode !== "everyAttempt" && k.ageInvalid) {
        k.error = word("Reading age must be between 5 and 600 seconds");
        draw(); return;
      }
      const nearRules = extra.policy?.rules || [];
      const validNearRange = (value) => /^\+?\d+(?:\.\d+)?$/.test(String(value || "")) && Number(value) > 0;
      if (action === "configure" && nearRules.some((r) => r.refreshWithin && !validNearRange(r.refreshWithin))) {
        k.error = word("Near-reserve range must be a positive decimal number");
        draw(); return;
      }
      if (action === "configure" && extra.policy?.refreshMode === "near") {
        const hasRange = nearRules.some((r) => validNearRange(r.refreshWithin));
        if (!hasRange) {
          k.error = word("Near-reserve mode needs a positive range on at least one rule");
          draw(); return;
        }
      }
      k.busy = true; const operation = Symbol(action); k.operation = operation; k.error = ""; draw();
      try {
        const v = await api("providers/quota-guard", { provider: p.id, key: id, action, revision: k.view.entry.revision, ...extra }, undefined, s.controller.signal);
        // A response belongs to the original key. Never replace a later edit,
        // another editor, or the policy draft merely because refresh finished.
        if (!current()) return;
        if (action === "refresh") {
          k.snapshots = v.snapshots || []; accept(k, v.view, false);
          if (v.incomplete) k.error = word("Some quota data could not be read; unknown data does not permit protected requests");
        } else { accept(k, v, action === "configure"); k.snapshots = null; k.notice = word("Quota protection action saved"); }
      } catch (e) {
        if (current() && e.name !== "AbortError") {
          k.error = word("The action was not confirmed. Reload its state before retrying; unsaved values are kept.");
          k.view = null;
        }
      } finally {
        if (k.operation === operation) k.busy = false;
        if (current() && s.selected === id) draw();
      }
    }
    function draw() {
      const k = keyState(); form.replaceChildren();
      if (!k.view) {
        form.append(node("p", "hint", k.error || word("Loading quota protection…")));
        if (k.error) form.append(button("Reload protection state", () => load()));
        return;
      }
      const policy = k.policy, d = k.view.decision;
      const facts = [];
      if (d.manualOff) facts.push(word("Manually disabled"));
      if (d.protected) facts.push(word("Reserve protection latched"));
      if (d.unknown) facts.push(word("Quota unknown"));
      if (d.nearReserve && policy.refreshMode === "near") facts.push(word("Near reserve; refreshing every attempt"));
      if (d.overrideActive) facts.push(word("Override until {time}", { time: new Date(d.overrideUntil).toLocaleString() }));
      if (!facts.length) facts.push(word(d.guardEnabled ? "Protection armed" : "Protection not enabled"));
      const state = node("p", "hint quota-status", facts.join(" · ")); state.setAttribute("role", "status"); form.append(state);
      const enabled = node("input"); enabled.type = "checkbox"; enabled.checked = !!policy.enabled; enabled.disabled = !!k.busy;
      const enabledLabel = node("label", "quota-toggle"); enabledLabel.append(enabled, document.createTextNode(word("Enable this key's quota protection")));
      enabled.onchange = () => { policy.enabled = enabled.checked; }; form.append(enabledLabel);
      const refreshSettings = node("div", "quota-refresh-settings");
      const refreshModeLabel = node("label", "", word("Quota refresh strategy"));
      const refreshMode = node("select", "words");
      for (const [value, label] of [
        ["cached", "Use cached readings until stale"],
        ["near", "Refresh near the reserve"],
        ["everyAttempt", "Refresh before every attempt"],
      ]) {
        const option = node("option", "", word(label)); option.value = value; refreshMode.append(option);
      }
      refreshMode.value = policy.refreshMode || "cached"; refreshMode.disabled = !!k.busy;
      refreshMode.onchange = () => { policy.refreshMode = refreshMode.value; if (refreshMode.value === "everyAttempt") k.ageInvalid = false; draw(); };
      refreshModeLabel.append(refreshMode); refreshSettings.append(refreshModeLabel);
      const ageLabel = node("label", "", word("Maximum reading age (seconds)"));
      const age = node("input", "words"); age.type = "number"; age.min = "5"; age.max = "600"; age.step = "1";
      age.value = String(policy.maxAgeSeconds || 60); age.disabled = !!k.busy || refreshMode.value === "everyAttempt";
      age.onchange = () => {
        const n = Number(age.value);
        if (!Number.isInteger(n) || n < 5 || n > 600) {
          k.ageInvalid = true;
        } else { policy.maxAgeSeconds = n; k.ageInvalid = false; k.error = ""; }
      };
      ageLabel.append(age); refreshSettings.append(ageLabel);
      const modeHint = {
        cached: "Reuse a recent reading; refresh when it expires",
        near: "Near-reserve mode refreshes per attempt inside the configured band",
        everyAttempt: "Every-attempt refresh adds latency and upstream quota reads",
      };
      refreshSettings.append(node("p", "hint", word(modeHint[refreshMode.value]))); form.append(refreshSettings);
      const snapshots = k.snapshots || Object.values(k.view.entry.snapshots || {});
      const metrics = new Map();
      for (const snap of snapshots) for (const m of snap.metrics || []) metrics.set(m.id, { ...m, at: snap.observedAt });
      for (const r of policy.rules || []) if (!metrics.has(r.metric)) metrics.set(r.metric, { id: r.metric, label: r.metric, unit: r.unit });
      for (const m of metrics.values()) {
        const r = policy.rules.find(r => r.metric === m.id);
        const row = node("div", "quota-rule");
        const label = node("label", "", `${m.label || m.id} · ${m.unit}`);
        const limit = node("input", "words"); limit.inputMode = "decimal"; limit.value = r?.minimum ?? "";
        limit.placeholder = word("Minimum remaining; empty disables this rule"); limit.setAttribute("aria-label", `${m.label || m.id}: ${limit.placeholder}`);
        limit.disabled = !!k.busy;
        limit.oninput = () => {
          const refreshWithin = policy.rules.find(r => r.metric === m.id)?.refreshWithin || k.nearRanges.get(m.id) || "";
          policy.rules = policy.rules.filter(r => r.metric !== m.id);
          if (limit.value.trim() !== "") policy.rules.push({
            metric: m.id, unit: m.unit, minimum: limit.value.trim(),
            ...(refreshWithin ? { refreshWithin } : {}),
          });
        };
        label.append(limit); row.append(label);
        const nearValue = r?.refreshWithin || k.nearRanges.get(m.id) || "";
        if (refreshMode.value === "near" || nearValue) {
          const nearText = refreshMode.value === "near" ? "Refresh within this distance above reserve" : "Saved near range (used only in near-reserve mode)";
          const nearLabel = node("label", "quota-near-range", word(nearText));
          const near = node("input", "words"); near.inputMode = "decimal"; near.value = nearValue;
          near.placeholder = word("Same unit as this metric"); near.disabled = !!k.busy;
          near.setAttribute("aria-label", String(m.label || m.id) + ": " + word("Refresh within this distance above reserve"));
          near.oninput = () => {
            const value = near.value.trim();
            if (value) k.nearRanges.set(m.id, value); else k.nearRanges.delete(m.id);
            const currentRule = policy.rules.find(rule => rule.metric === m.id);
            if (!currentRule) return;
            currentRule.refreshWithin = value;
            if (!currentRule.refreshWithin) delete currentRule.refreshWithin;
          };
          nearLabel.append(near); row.append(nearLabel);
        }
        row.append(node("div", "hint", m.remaining === undefined ? word("No valid reading") : `${m.remaining} ${m.unit} · ${m.scope || ""} · ${new Date(m.at).toLocaleString()}`));
        form.append(row);
      }
      if (!metrics.size) form.append(node("p", "hint", word("No valid quota reading is stored yet. Read upstream quota once to discover the available percentage or balance fields.")));
      const custom = node("details", "quota-custom"); custom.append(node("summary", "", word("Custom numeric balance source")));
      for (const [prop, text] of [["balancePointer", "Raw balance JSON pointer"], ["balanceUnit", "Balance unit, for example CNY"]]) {
        const lab = node("label", "", word(text)), input = node("input", "words"); input.value = policy[prop] || ""; input.disabled = !!k.busy;
        input.oninput = () => { policy[prop] = input.value.trim(); }; lab.append(input); custom.append(lab);
      }
      custom.append(node("p", "hint", word("Uses the existing Balance URL and authentication. Enter a raw numeric field, not a formatted currency string."))); form.append(custom);
      const actions = node("div", "quota-actions");
      const reload = button("Reload protection state", async () => {
        if (!same(k.policy, k.saved) && !await confirmAction(word("Discard"), word("Discard unsaved quota changes?"), word("Discard"))) return;
        if (!current()) return; k.view = null; k.policy = null; k.snapshots = null; k.nearRanges.clear(); k.error = ""; draw(); load();
      });
      const refresh = button(metrics.size ? "Refresh upstream quota" : "Read upstream quota", () => perform("refresh", { policy: copy(policy) }));
      const save = button("Save quota protection", () => perform("configure", { policy: copy(policy) }));
      const arm = button("Re-arm after recovery", () => confirmThen("rearm"));
      const grant = button("Allow for 15 minutes", () => confirmThen("override"));
      const revoke = button("Revoke override", () => confirmThen("revoke"));
      for (const b of [reload, refresh, save, arm, grant, revoke]) { b.disabled = !!k.busy; actions.append(b); }
      arm.disabled ||= !d.guardEnabled; grant.disabled ||= !d.guardEnabled || d.unknown; revoke.disabled ||= !d.overrideActive;
      form.append(actions);
      if (k.error) form.append(node("p", "err", k.error));
      if (k.notice) form.append(node("p", "hint", k.notice));
      async function confirmThen(action) {
        const id = s.selected;
        const text = action === "override" ? "Allow this quota policy to be exceeded for 15 minutes? Manual switches, unknown data and upstream restrictions still apply." : "Apply this quota protection action now? This does not change the manual key switch.";
        if (!await confirmAction(word("Quota protection"), word(text), word("Confirm"))) return;
        if (!current() || s.selected !== id) return;
        const extra = { confirm: true }; if (action === "override") extra.until = new Date(Date.now() + 15 * 60 * 1000).toISOString();
        perform(action, extra);
      }
    }
    drawKeys(); draw(); load();
  }
  window.magpieQuotaGuard = { mount, dirty, discard };
})();
