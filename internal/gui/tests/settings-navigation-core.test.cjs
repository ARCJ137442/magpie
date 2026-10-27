const {test}=require('node:test');
const assert=require('node:assert/strict');
const core=require('../assets/settings-navigation-core.js');
const noop=()=>{};
test('Chinese object plus multiplier resolves the exact provider field',()=>{
 const registry=new core.Registry();
 registry.replace('providers',core.providerDescriptors([{id:'a',name:'MiniMax 拼车 2026-10-27'},{id:'b',name:'MiniMax 直连'}],s=>s,noop));
 const got=registry.search('MiniMax 拼车 2026-10-27 的使用倍率','global');
 assert.equal(got.length,1);assert.equal(got[0].id,'provider:a:priceRate');
});
test('duplicate names retain distinct identities',()=>{
 const registry=new core.Registry();registry.replace('p',core.providerDescriptors([{id:'a',name:'MiniMax'},{id:'b',name:'MiniMax'}],s=>s,noop));
 assert.equal(registry.search('MiniMax price rate','global').length,2);
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
