// Local descriptor search. No model call, telemetry, input-value indexing or
// automatic setting changes. Exported for isolated tests.
(function(root, factory) {
  const core = factory();
  if (typeof module === "object" && module.exports) module.exports = core;
  else root.MagpieNavigationCore = core;
})(typeof globalThis === "object" ? globalThis : this, function() {
  "use strict";
  const aliases = [
    ["使用倍率", "price rate"], ["价格倍率", "price rate"], ["计费倍率", "price rate"],
    ["费用倍率", "price rate"], ["倍率", "price rate"], ["price multiplier", "price rate"],
    ["额度保护", "quota protection"], ["额度熔断", "quota protection"], ["预留额度", "quota protection"],
    ["并发", "concurrency"], ["排队上限", "queue size"], ["排队等待", "queue wait"],
    ["每分钟请求", "requests per minute"], ["余额", "balance"], ["请求头", "headers"],
  ];
  function normalize(value) {
    return String(value || "").normalize("NFKC").toLowerCase()
      .replace(/[的:：\/·>›]+/g," ").replace(/\s+/g," ").trim();
  }
  function words(query) {return [...new Set(normalize(query).split(" ").filter(Boolean))];}
  function variants(value) {
    const text = normalize(value), result = [text];
    for (const [from, to] of aliases) {
      if (text.includes(from)) result.push(to);
      if (text.includes(to)) result.push(from);
    }
    return result;
  }
  // Quotes are exact phrases, spaces AND, | OR, ! excludes. These fields
  // describe navigation targets; no query is evaluated as code or sent away.
  function parse(query) {
    const groups = [[]];
    for (let token of String(query).match(/(?:[^\s"|]|"[^"]*")+|\|/g) || []) {
      if (token === "|") { if (groups.at(-1).length) groups.push([]); continue; }
      const exclude = token.startsWith("!");
      if (exclude) token = token.slice(1);
      const field = /^(page|object|type):/i.exec(token);
      if (field) token = token.slice(field[0].length);
      const exact = token.startsWith('"') && token.endsWith('"');
      const text = normalize(exact ? token.slice(1, -1) : token);
      if (text) groups.at(-1).push({ field: field?.[1].toLowerCase() || "", text, exact, exclude });
    }
    return groups.filter(group => group.length);
  }
  function match(text, term) {
    if (text === term.text) return 100;
    if (text.startsWith(term.text)) return 80;
    if (text.includes(term.text)) return 60;
    if (term.exact || !/^[a-z0-9_-]{2,}$/i.test(term.text)) return -1;
    let at = 0, first = -1, last = -1;
    for (const ch of term.text) {
      at = text.indexOf(ch, at);
      if (at < 0) return -1;
      if (first < 0) first = at;
      last = at++;
    }
    return 20 + term.text.length / (last - first + 1);
  }
  function rank(entry, groups) {
    if (!groups.length) return 0;
    const fields = {
      page: variants([entry.page || entry.path, entry.pageId].filter(Boolean).join(" ")), object: variants(entry.object), type: [normalize(entry.type)],
      "": [entry.title, entry.object, entry.path, ...(entry.keywords || [])].flatMap(variants),
    };
    let best = -1;
    for (const group of groups) {
      let sum = 0, valid = true;
      for (const term of group) {
        const n = Math.max(...fields[term.field].map(value => match(value, term)));
        if (term.exclude ? n >= 0 : n < 0) { valid = false; break; }
        if (!term.exclude) sum += n;
      }
      if (valid) best = Math.max(best, sum);
    }
    return best;
  }
  function score(entry, query) { return rank(entry, parse(query)); }
  class Registry {
    constructor() {this.groups = new Map();}
    replace(group, entries) {
      const ids = new Set();
      for (const e of entries) {
        if (!e.id || !e.title || typeof e.run !== "function" || ids.has(e.id)) throw new Error("invalid navigation descriptor");
        ids.add(e.id);
      }
      this.groups.set(group, entries.slice());
    }
    remove(group) {this.groups.delete(group);}
    entries() {
      const unique = new Map();
      for (const es of this.groups.values()) for (const e of es) unique.set(e.id,e);
      return [...unique.values()];
    }
    search(query, scope, limit = 200, recent = []) {
      const groups = parse(query), history = new Map(recent.map((id, i) => [id, recent.length - i]));
      return this.entries().filter(e => !scope || e.scope === scope).map(e => ({entry:e,rank:rank(e,groups),recent:history.get(e.id)||0}))
        .filter(x => x.rank >= 0).sort((a,b) => b.rank-a.rank || b.recent-a.recent || a.entry.id.localeCompare(b.entry.id))
        .slice(0,limit).map(x => x.entry);
    }
  }
  // Pre-render descriptors for high-frequency provider fields. Other editor
  // fields join from labels when rendered; registry coverage is explicit.
  const providerFields = [
    {id:"priceRate",title:"Price rate",selector:"input.price-rate",advanced:true,keywords:["使用倍率","价格倍率","计费倍率","price multiplier"]},
    {id:"concurrency",title:"Concurrency",selector:"input.concurrency",advanced:true,keywords:["并发","请求数量"]},
    {id:"maxRPM",title:"Requests per minute",selector:"input.rpm",advanced:true,keywords:["每分钟请求","rpm","频率"]},
    {id:"queueLimit",title:"Queue size",selector:"input.queue-limit",advanced:true,keywords:["排队上限","队列"]},
    {id:"queueWait",title:"Queue wait",selector:"input.queue-wait",advanced:true,keywords:["排队等待","超时"]},
  ];
  function providerDescriptors(providers, translate, reveal) {
    const result=[];
    for (const p of providers || []) {
      const object = p.name || p.id;
      result.push({id:`provider:${p.id}`,scope:"global",type:"object",pageId:"providers",page:translate("Providers"),title:object,object:p.id,path:translate("Providers"),keywords:["提供商","provider"],run:e=>reveal(p.id,null,e)});
      if (p.account) continue; // subscription editors do not expose these same fields
      for (const f of providerFields) result.push({id:`provider:${p.id}:${f.id}`,scope:"global",type:"input",pageId:"providers",page:translate("Providers"),title:translate(f.title),object,
        path:`${translate("Providers")} › ${object} › ${translate("Advanced")}`,
        keywords:[p.id,f.title,...f.keywords],run:e=>reveal(p.id,f,e)});
    }
    return result;
  }
  return {normalize,words,parse,score,Registry,providerFields,providerDescriptors};
});
