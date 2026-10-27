// The real application assets with local API fixtures only; no user settings.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");
const provider = (id, name) => ({
  id, name, icon: "generic", chat: "https://relay.example.invalid/v1", responses: "", anthropic: "", catalog: "",
  models: [{ id: "model-a", name: "Model A", on: true }], agents: [], fallback: [], headers: {},
  key: { set: true, masked: "fixture-only" }, keyList: [], balanceToken: { takes: false, set: false },
  proxy: "", maxConcurrency: null,
});

async function open(t, engine, lang, width = 900) {
  const theme = width === 560 ? "dark" : "light";
  const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
  t.after(() => browser.close());
  const page = await browser.newPage({ viewport: { width, height: 760 }, reducedMotion: "reduce" });
  page.setDefaultTimeout(5000);
  const errors = [], writes = [];
  page.on("pageerror", e => errors.push(e.message));
  const providers = {
    providers: [provider("shared", "MiniMax 拼车 2026-10-27"), provider("direct", "MiniMax 直连")],
    presets: [], excluded: [], gateway: { running: true, window: true },
  };
  await page.route("**/*", async route => {
    const request = route.request(), url = new URL(request.url());
    const json = data => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs=${JSON.stringify({ lang, theme, web: true })};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname.startsWith("/api/")) {
      if (request.method() !== "GET") writes.push({ path: url.pathname, method: request.method() });
      if (url.pathname === "/api/state") return json({ agents: [], profiles: [], settings: { lang, theme } });
      if (url.pathname === "/api/providers") return json(providers);
      if (url.pathname === "/api/groups") return json({ groups: [] });
      return json({});
    }
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    await route.fulfill({ body: await fs.readFile(file), contentType });
  });
  await page.goto("http://magpie.test/?view=providers");
  await page.locator('.row.provider[data-id="shared"]').waitFor();
  // Boot may persist presentation preferences; only navigation is measured.
  writes.length = 0;
  return { page, errors, writes };
}

async function find(page, query) {
  await page.keyboard.press("Control+Shift+P");
  const search = page.locator(".settings-command input");
  await search.fill(query);
  await page.locator(".settings-command-result").first().waitFor();
  return search;
}

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of ["en", "zh", "zh-TW", "ja", "de"]) {
    test(`${engine} ${lang}: exact provider field navigation is read only and preserves a draft`, async t => {
      const { page, errors, writes } = await open(t, engine, lang);
      await page.evaluate(() => show("agents"));
      const search = await find(page, "MiniMax 拼车 2026-10-27 的使用倍率");
      await search.press("Enter");
      const rate = page.locator("#modal .editor input.price-rate");
      await rate.waitFor({ state: "visible" });
      assert.equal(await page.evaluate(() => editing), "shared");
      assert.equal(await rate.evaluate(e => document.activeElement === e), true);
      assert.equal(await rate.inputValue(), "");
      await rate.fill("1.5");
      const again = await find(page, "MiniMax 拼车 2026-10-27 的使用倍率");
      await again.press("Enter");
      assert.equal(await rate.inputValue(), "1.5", "same editor keeps its unsaved value");
      assert.equal(await page.evaluate(() => draft.priceRate), "1.5");
      if (process.env.ARTIFACT_DIR) {
        await fs.mkdir(process.env.ARTIFACT_DIR, { recursive: true });
        await page.screenshot({ path: path.join(process.env.ARTIFACT_DIR, `${engine}-${lang}-provider-field.png`) });
      }
      assert.deepEqual(writes, [], "finding and focusing never saves a setting");
      assert.deepEqual(errors, []);
    });

    test(`${engine} ${lang}: editor search restores focus and ignores IME Enter`, async t => {
      const { page, errors, writes } = await open(t, engine, lang, 560);
      await page.locator('.row.provider[data-id="shared"]').click();
      const editor = page.locator("#modal .editor");
      const initial = editor.locator("input").first();
      await initial.focus();
      await initial.press("Control+f");
      const search = page.locator(".settings-command input");
      await search.waitFor({ state: "visible" });
      assert.ok(await page.locator(".settings-command-result").count(), "the current editor has field descriptors");
      const colors = await page.locator(".settings-command").evaluate(e => ({
        actual: getComputedStyle(e).color,
        expected: getComputedStyle(document.body).color,
      }));
      assert.equal(colors.actual, colors.expected, "the palette uses the host's foreground token");
      if (process.env.ARTIFACT_DIR) {
        await fs.mkdir(process.env.ARTIFACT_DIR, { recursive: true });
        await page.screenshot({ path: path.join(process.env.ARTIFACT_DIR, `${engine}-${lang}-editor-search.png`) });
      }
      await search.dispatchEvent("compositionstart");
      await search.press("Enter");
      assert.equal(await page.locator(".settings-command").evaluate(e => e.open), true);
      await search.dispatchEvent("compositionend");
      await search.press("Escape");
      assert.equal(await initial.evaluate(e => document.activeElement === e), true);
      assert.equal(await page.locator("#modal").isVisible(), true, "Escape leaves the editor open");
      assert.deepEqual(writes, []);
      assert.deepEqual(errors, []);
    });

    test(`${engine} ${lang}: canceling a different provider keeps the original draft`, async t => {
      const { page, errors, writes } = await open(t, engine, lang);
      await page.locator('.row.provider[data-id="shared"]').click();
      const advanced = page.locator(".editor details.advanced");
      if (!await advanced.evaluate(e => e.open)) await advanced.locator(":scope > summary").click();
      await page.locator(".editor input.price-rate").fill("1.5");
      const search = await find(page, "MiniMax 直连 使用倍率");
      await search.press("Enter");
      const confirm = page.locator("dialog.action-confirm");
      await confirm.waitFor({ state: "visible" });
      const cancel = await page.evaluate(() => t("Cancel"));
      await confirm.getByRole("button", { name: cancel, exact: true }).click();
      assert.equal(await page.evaluate(() => editing), "shared");
      assert.equal(await page.locator(".editor input.price-rate").inputValue(), "1.5");
      assert.deepEqual(writes, []);
      assert.deepEqual(errors, []);
    });
  }
}
