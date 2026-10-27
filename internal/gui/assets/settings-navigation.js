// Native navigation only: results focus existing controls, never activate a
// destructive button, submit a form, grant quota, or call a settings write API.
(() => {
  "use strict";
  const core=window.MagpieNavigationCore, registry=new core.Registry();
  let panel=null, query=null, results=null, notice=null, entries=[], cursor=0;
  let owner=0, scope=null, returnFocus=null, currentProvider=null, composing=false;
  const tr=(s,args)=>t(s,args);
  const make=(tag,cls,text)=>{const e=document.createElement(tag);if(cls)e.className=cls;if(text!==undefined)e.textContent=text;return e;};
  const interactive='input:not([type=hidden]),textarea,button,[tabindex]:not([tabindex="-1"])';
  const isHiddenFeature=n=>typeof featureHides==='function'&&featureHides(n);
  function focusTarget(control,event) {
    if(!control||!control.isConnected||control.disabled||isHiddenFeature(control)) throw new Error(tr("This setting is not available in the current context"));
    // Reveal disclosure, never a manually disabled feature or restricted row.
    for(let p=control.parentElement;p;p=p.parentElement)if(p.tagName==='DETAILS')p.open=true;
    if(!control.getClientRects().length)throw new Error(tr("This setting is not available in the current context"));
    if(event && typeof scrollOnPurpose==='function' && scrollOnPurpose(event,2500))control.scrollIntoView({block:'center',behavior:'auto'});
    control.focus({preventScroll:true});
    control.classList.add('nav-focus');
    setTimeout(()=>control.classList.remove('nav-focus'),1500);
  }
  function waitFor(find,signal,within=1800) {
    return new Promise((resolve,reject)=>{
      const now=find();if(now){resolve(now);return;}
      let timer;
      const observer=new MutationObserver(()=>{const n=find();if(n){finish();resolve(n);}});
      const abort=()=>{finish();reject(new DOMException('Canceled','AbortError'));};
      function finish(){observer.disconnect();clearTimeout(timer);signal?.removeEventListener('abort',abort);}
      if(signal?.aborted){abort();return;}
      observer.observe(document.body,{childList:true,subtree:true});
      signal?.addEventListener('abort',abort,{once:true});
      timer=setTimeout(()=>{finish();reject(new Error(tr("This setting is not available in the current context")));},within);
    });
  }
  let navigation=null;
  async function revealProvider(id,field,event) {
    navigation?.abort();const run=new AbortController();navigation=run;
    // Reusing the current editor preserves draft identity and pending inputs.
    if(!(view==='providers'&&editing===id))await openProvider(id);
    if(run.signal.aborted||editing!==id||view!=='providers')return;
    if(field?.advanced){advanced={of:editing,open:true};renderProviders();}
    const control=await waitFor(()=>{
      if(editing!==id)return null;
      const editor=document.querySelector('#modal .editor');
      return field ? editor?.querySelector(field.selector) : editor?.querySelector(interactive);
    },run.signal);
    if(!run.signal.aborted&&editing===id)focusTarget(control,event);
  }
  function collectLabels(editor,p) {
    const items=[],seen=new Set();
    for(const label of editor.querySelectorAll('label[for]')) {
      const target=editor.querySelector('#'+CSS.escape(label.htmlFor));
      if(!target||!editor.contains(target)||target.type==='hidden'||isHiddenFeature(target))continue;
      const title=label.textContent.trim();if(!title||seen.has(label.htmlFor))continue;
      seen.add(label.htmlFor);
      // Labels only. No input.value, placeholder secrets, URL, headers or raw key.
      const n=items.length,id=`local:${p?.id || 'editor'}:${n}`;
      items.push({id,scope:'editor',title,object:p?.name||'',path:tr("Current editor"),keywords:[],run:event=>focusTarget(target,event)});
    }
    return items;
  }
  function mountEditor(editor,p) {
    currentProvider=p?.id||null;
    registry.replace('current-editor',collectLabels(editor,p));
    if(panel?.open && scope==='editor')render();
    if(editor.querySelector('.settings-find-editor'))return;
    const b=make('button','text settings-find-editor',tr("Find a setting in this editor"));b.type='button';
    b.onclick=e=>{registry.replace('current-editor',collectLabels(editor,p));open(e,'editor');};
    editor.querySelector('.ehead')?.append(b);
    // Ctrl/Cmd+F belongs to this editor; ordinary text editing/IME is untouched.
    editor.addEventListener('keydown',e=>{
      if(e.isComposing||e.altKey||e.shiftKey||!(e.ctrlKey||e.metaKey)||e.key.toLowerCase()!=='f')return;
      e.preventDefault();e.stopPropagation();registry.replace('current-editor',collectLabels(editor,p));open(e,'editor');
    },true);
  }
  const settingTabs=['general','usage','network','models','privacy','otel','sync','plugins','about'];
  function addPageEntries() {
    const names={agents:'Agents',providers:'Providers',gateway:'Gateway',routing:'Routing',usage:'Usage',sessions:'Sessions',library:'Library',plugins:'Plugins',settings:'Settings'};
    const pages=[];
    for(const [id,name] of Object.entries(names)) {
      const page=document.getElementById('view-'+id);if(!page||isHiddenFeature(page))continue;
      if(typeof gatewayMode!=='undefined'&&gatewayMode&&GATEWAY_HIDES.includes(id))continue;
      pages.push({id:'page:'+id,scope:'global',title:tr(name),object:'',path:tr("Pages"),keywords:[id],run:()=>show(id)});
    }
    registry.replace('pages',pages);
    const sections=[];
    for(const tab of settingTabs){
      const page=document.getElementById('setPage-'+tab);if(!page||isHiddenFeature(page))continue;
      const button=document.getElementById('setTab-'+tab),title=button?.textContent.trim()||tab;
      sections.push({id:'settings-tab:'+tab,scope:'global',title,object:tr("Settings"),path:tr("Settings"),keywords:[tab],run:async()=>{
        if(!await show('settings'))return;
        const q=document.getElementById('setQ');if(q)q.value='';searchSettings('');setSetTab(tab,true);
      }});
    }
    registry.replace('settings-sections',sections);
  }
  function collectSettings() {
    const root=document.getElementById('view-settings');if(!root)return;
    const found=[];
    for(const row of root.querySelectorAll('.row.pref')) {
      if(isHiddenFeature(row))continue;
      const section=row.closest('.set-page');if(!section)continue;
      const label=row.querySelector('.name');const title=label?.textContent.trim();if(!title)continue;
      const tab=section.id.replace('setPage-','');
      const stable=row.id||row.querySelector('[id]')?.id;if(!stable)continue;
      const id='setting:'+stable;
      found.push({id,scope:'global',title,object:tr("Settings"),path:`${tr("Settings")} › ${tab} › ${title}`,
        keywords:[tab,label.dataset.en||''],run:async event=>{
          if(!await show('settings'))return;
          const q=document.getElementById('setQ');if(q)q.value='';searchSettings('');setSetTab(tab,true);
          const anchor=document.getElementById(stable);const live=anchor?.matches('.row.pref')?anchor:anchor?.closest('.row.pref');
          if(!live)throw new Error(tr("This setting is not available in the current context"));
          focusTarget(live.querySelector(interactive),event);
        }});
    }
    registry.replace('settings-fields',found);
  }
  function ensurePanel() {
    if(panel)return;
    panel=make('dialog','settings-command');panel.setAttribute('aria-labelledby','settings-command-title');
    const title=make('h2','',tr("Find settings and navigate"));title.id='settings-command-title';
    query=make('input','words');query.type='search';query.autocomplete='off';query.spellcheck=false;
    query.setAttribute('role','combobox');query.setAttribute('aria-autocomplete','list');query.setAttribute('aria-controls','settings-command-results');query.setAttribute('aria-expanded','true');
    query.setAttribute('aria-label',tr("Search objects and settings"));query.placeholder=tr("Provider name + setting, for example MiniMax price rate");
    results=make('div','settings-command-results');results.id='settings-command-results';results.setAttribute('role','listbox');
    notice=make('p','hint');notice.setAttribute('role','status');
    const close=make('button','text',tr("Close"));close.type='button';close.onclick=()=>dismiss(true);
    panel.append(title,query,results,notice,close);document.body.append(panel);
    query.addEventListener('compositionstart',()=>{composing=true;});
    query.addEventListener('compositionend',()=>{composing=false;render();});
    query.addEventListener('input',()=>{if(!composing)render();});
    panel.addEventListener('keydown',e=>{
      e.stopPropagation();
      if(e.isComposing||composing)return;
      if(e.key==='Escape'){e.preventDefault();dismiss(true);return;}
      if(e.target!==query)return;
      if(e.key==='ArrowDown'||e.key==='ArrowUp'){e.preventDefault();cursor=(cursor+(e.key==='ArrowDown'?1:-1)+entries.length)%Math.max(1,entries.length);select();}
      if(e.key==='Enter'){e.preventDefault();activate(e);}
    });
    panel.addEventListener('cancel',e=>{e.preventDefault();dismiss(true);});
  }
  function select() {
    [...results.children].forEach((n,i)=>n.setAttribute('aria-selected',String(i===cursor)));
    const active=results.children[cursor];if(active){query.setAttribute('aria-activedescendant',active.id);active.scrollIntoView({block:'nearest'});}
    else query.removeAttribute('aria-activedescendant');
  }
  function render() {
    if(!panel?.open)return;
    entries=registry.search(query.value,scope==='editor'?'editor':'global');cursor=0;results.replaceChildren();
    entries.forEach((entry,i)=>{
      const option=make('div','settings-command-result');option.id='settings-command-option-'+i;option.setAttribute('role','option');
      option.append(make('div','',entry.object?`${entry.object} · ${entry.title}`:entry.title),make('div','hint',entry.path));
      option.onmousedown=e=>e.preventDefault();option.onclick=e=>{cursor=i;activate(e);};results.append(option);
    });
    notice.textContent=entries.length?tr("Enter opens the setting; it does not change its value"):tr("No matching setting; try an object name or a shorter term");select();
  }
  function dismiss(restore) {
    // The host marks existing body children inert when it opens an editor.
    // A fresh palette can open above it without changing that editor's guard.
    owner++;panel?.close();panel?.remove();
    panel=query=results=notice=null;
    if(restore&&returnFocus?.isConnected)returnFocus.focus({preventScroll:true});
  }
  async function activate(event) {
    if(composing||!entries[cursor])return;
    const entry=entries[cursor];dismiss(false);
    try{await entry.run(event);}catch(e){if(e.name!=='AbortError')status(e.message,'err');}
  }
  async function open(event,nextScope='global') {
    ensurePanel();returnFocus=document.activeElement;scope=nextScope;
    addPageEntries();collectSettings();
    const editor=document.querySelector('#modal .editor');
    if(nextScope==='editor'&&editor)registry.replace('current-editor',collectLabels(editor,providers?.providers?.find(p=>p.id===editing)));
    const token=++owner;query.value='';
    if(!panel.open)panel.showModal();query.focus();render();
    if(nextScope==='editor')return;
    // Read-only local API. Never transmit the search text or read raw secrets.
    try{
      const current=await api('providers');if(token!==owner||!panel.open)return;
      registry.replace('providers',core.providerDescriptors(current.providers,tr,revealProvider));render();
    }catch(e){if(token===owner&&panel.open)notice.textContent=tr("Provider list unavailable; existing navigation results are still shown");}
  }
  function boot() {
    if(typeof mode!=='undefined'&&mode==='panel')return;
    const b=make('button','text settings-command-open',tr("Find settings"));b.type='button';b.title=tr("Find settings (Ctrl/Cmd+Shift+P)");b.onclick=e=>open(e);
    const top=document.querySelector('header.top');if(top)top.append(b);
    document.addEventListener('keydown',e=>{
      if(e.defaultPrevented||e.isComposing||confirmationPending||!(e.ctrlKey||e.metaKey)||!e.shiftKey||e.altKey||e.key.toLowerCase()!=='p')return;
      e.preventDefault();e.stopPropagation();if(panel?.open)dismiss(true);else open(e);
    },true);
  }
  window.magpieSettingsNav={mountEditor,open,register:(name,descriptors)=>registry.replace('extension:'+name,descriptors),remove:name=>registry.remove('extension:'+name),coverage:()=>registry.entries().map(({id,path,scope})=>({id,path,scope}))};
  if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',boot,{once:true});else queueMicrotask(boot);
})();
