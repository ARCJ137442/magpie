const {test}=require('node:test');
const assert=require('node:assert/strict');
const core=require(process.env.MAGPIE_NAV_CORE || '../assets/settings-navigation-core.js');
const noop=()=>{};
test('Chinese object plus multiplier resolves the exact provider field',()=>{
 const registry=new core.Registry();
 registry.replace('providers',core.providerDescriptors([{id:'a',name:'MiniMax 拼车 2026-10-27'},{id:'b',name:'MiniMax 直连'}],s=>s,noop));
 const got=registry.search('MiniMax 拼车 2026-10-27 的使用倍率','global');
 assert.equal(got.length,1);assert.equal(got[0].id,'provider:a:priceRate');
});
test('duplicate names retain distinct identities',()=>{
 const registry=new core.Registry();registry.replace('p',core.providerDescriptors([{id:'a',name:'MiniMax'},{id:'b',name:'MiniMax'}],s=>s,noop));
 assert.equal(registry.search('MiniMax "price rate"','global').length,2);
});
test('short Chinese queue queries keep the original labels alongside aliases',()=>{
 const r=new core.Registry();r.replace('p',core.providerDescriptors([{id:'deepseek',name:'DeepSeek'}],s=>s,noop));
 assert.deepEqual(r.search('DeepSeek 排队').map(e=>e.id).sort(),['provider:deepseek:queueLimit','provider:deepseek:queueWait']);
 assert.equal(r.search('dpsk "queue wait"')[0].id,'provider:deepseek:queueWait');
});
test('scopes, quoted phrases, OR and exclusions combine without running a target',()=>{
 const r=new core.Registry();r.replace('p',core.providerDescriptors([{id:'deepseek',name:'DeepSeek'},{id:'other',name:'Other'}],s=>s,noop));
 assert.deepEqual(r.search('page:providers object:DeepSeek 排队 !等待').map(e=>e.id),['provider:deepseek:queueLimit']);
 assert.deepEqual(r.search('object:DeepSeek "queue wait" | object:Other "price rate"').map(e=>e.id).sort(),['provider:deepseek:queueWait','provider:other:priceRate']);
 assert.equal(r.search('type:object').length,2);
});
test('history sorts matching destinations but cannot outrank a stronger text match',()=>{
 const r=new core.Registry();r.replace('p',[{id:'a',title:'Queue wait',scope:'global',run:noop},{id:'b',title:'Queue size',scope:'global',run:noop}]);
 assert.equal(r.search('',null,200,['b'])[0].id,'b');
 assert.equal(r.search('Queue wait',null,200,['b'])[0].id,'a');
 assert.equal(r.search('absent',null,200,['b']).length,0);
});
test('search only descriptors; credential values never become searchable',()=>{
 const registry=new core.Registry();registry.replace('p',core.providerDescriptors([{id:'a',name:'MiniMax',key:'sk-not-searchable',headers:{Authorization:'secret'}}],s=>s,noop));
 assert.equal(registry.search('sk-not-searchable','global').length,0);
 assert.equal(registry.search('secret','global').length,0);
});
test('provider list replacement removes deleted objects',()=>{
 const r=new core.Registry();r.replace('p',core.providerDescriptors([{id:'gone',name:'Old'}],s=>s,noop));r.replace('p',[]);assert.equal(r.search('Old').length,0);
});
test('registry search never invokes actions',()=>{
 let invoked=0;const r=new core.Registry();r.replace('x',[{id:'a',title:'Save',scope:'global',run:()=>invoked++}]);r.search('Save');assert.equal(invoked,0);
});
test('scope, Unicode normalization and invalid descriptors',()=>{
 const r=new core.Registry();r.replace('x',[{id:'a',title:'ＡＢＣ',scope:'editor',run:noop}]);assert.equal(r.search('abc','editor').length,1);assert.equal(r.search('abc','global').length,0);
 assert.throws(()=>r.replace('x',[{id:'broken',title:'Broken'}]));
});
