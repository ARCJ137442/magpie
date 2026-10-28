const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { test } = require("node:test");
const { strings } = require("./strings.cjs");

test("lazy navigation words are unique and translated with the same placeholders", () => {
  const { I18N } = strings(), seen = {};
  const context = vm.createContext({
    window: {}, navigator: { platform: "Win32" }, localStorage: { getItem: () => null },
    document: { readyState: "loading", addEventListener() {} },
    magpie: { strings(dict) {
      for (const [language, words] of Object.entries(dict)) {
        const target = I18N[language], keys = seen[language] ||= new Set();
        for (const [key, value] of Object.entries(words)) {
          assert.equal(key in target, false, `${language}: duplicate ${key}`);
          target[key] = value; keys.add(key);
          const marks = text => [...text.matchAll(/\{\w+\}/g)].map(match => match[0]).sort();
          assert.deepEqual(marks(value), marks(key), `${language}: ${key}`);
        }
      }
    } },
  });
  for (const file of ["settings-navigation-launcher.js", "settings-navigation-i18n.js"]) {
    vm.runInContext(fs.readFileSync(path.join(__dirname, "../assets", file), "utf8"), context);
  }
  for (const language of ["zh-TW", "ja", "de"]) assert.deepEqual([...seen[language]].sort(), [...seen.zh].sort());
});
