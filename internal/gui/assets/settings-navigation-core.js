// Local descriptor search. No model call, telemetry, input-value indexing or
// fuzzy selection that silently edits a setting. Exported for isolated tests.
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
    let s = String(value || "").normalize("NFKC").toLocaleLowerCase();
    for (const [from,to] of aliases) s = s.split(from).join(to);
    return s.replace(/[的:：\/·>›]+/g," ").replace(/\s+/g," ").trim();
  }
  function words(query) {return [...new Set(normalize(query).split(" ").filter(Boolean))];}
  function score(entry, query) {
    const terms = words(query), title = normalize(entry.title), object = normalize(entry.object);
    const text = normalize([entry.title,entry.object,entry.path,...(entry.keywords || [])].join(" "));
    if (!terms.every(term => text.includes(term))) return -1;
    let n = 0;
    for (const term of terms) n += object === term ? 12 : title === term ? 10 : title.includes(term) ? 5 : object.includes(term) ? 4 : 1;
    if (normalize(query) === normalize([entry.object,entry.title].filter(Boolean).join(" "))) n += 100;
    return n;
  }
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
    search(query, scope, limit = 60) {
      return this.entries().filter(e => !scope || e.scope === scope).map(e => ({entry:e,rank:score(e,query)}))
        .filter(x => x.rank >= 0).sort((a,b) => b.rank-a.rank || a.entry.id.localeCompare(b.entry.id))
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
      result.push({id:`provider:${p.id}`,scope:"global",title:object,object:p.id,path:translate("Providers"),keywords:["提供商","provider"],run:e=>reveal(p.id,null,e)});
      if (p.account) continue; // subscription editors do not expose these same fields
      for (const f of providerFields) result.push({id:`provider:${p.id}:${f.id}`,scope:"global",title:translate(f.title),object,
        path:`${translate("Providers")} › ${object} › ${translate("Advanced")} › ${translate(f.title)}`,
        keywords:[p.id,f.title,...f.keywords],run:e=>reveal(p.id,f,e)});
    }
    return result;
  }
  return {normalize,words,score,Registry,providerFields,providerDescriptors};
});
