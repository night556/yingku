import {createScanMatcher} from './scan-matching.js';
import {createManualScan} from './scan-manual.js';
export function aiSettingsPanel(ai,esc,icon) {
  return `<section class="settings-panel" id="ai-settings"><h2>${icon('star')}AI 文件识别 <span class="badge ${ai.enabled?'green':''}">${ai.enabled?'已启用':'可选'}</span></h2><label class="checkbox-label"><input type="checkbox" name="aiEnabled" ${ai.enabled?'checked':''}>启用 OpenAI 兼容的 AI 识别</label><p class="section-note">让 AI 根据目录名和视频文件名判断作品、季数与集号，结果会先放进导入预览。所选目录的名称与视频相对路径会发送给你配置的服务，调用可能使用服务商额度。</p><label class="field"><span>AI 端点</span><input name="aiEndpoint" type="url" autocomplete="off" value="${esc(ai.endpoint||'')}" placeholder="https://api.openai.com/v1"><small>填写基础地址，或完整的 /chat/completions 地址。本机与内网服务可使用 HTTP。</small></label><div class="fields"><label class="field"><span>AI Key</span><input name="aiKey" type="password" autocomplete="new-password" placeholder="${ai.keyConfigured?'已保存，留空保留':'本机服务无需鉴权时可留空'}"><small>加密保存在本机。更换服务商地址后，请重新填写 Key。</small></label><label class="field"><span>AI 模型</span><input name="aiModel" autocomplete="off" value="${esc(ai.model||'')}" placeholder="服务商提供的模型名称"></label></div><div class="button-row"><button type="button" class="button small" data-action="test-ai">${icon('refresh')}测试 AI 连接</button><span class="section-note">先保存设置，再测试。</span></div>${ai.keyConfigured?'<label class="checkbox-label" style="margin-top:16px"><input type="checkbox" name="aiClearKey">清除已保存的 AI Key</label>':''}</section>`;
}

export function createScanner({api,getState,modal,closeModal,toast,esc,icon,refresh,navigate}) {
  let session=null,formPlan=null,lastSource='',lastID='',pollTimer=null,lastSignature='',mode='reference',outputRoot='',checked=false,previews=[],viewOpen=false;
  let aiInstructions='',feedbackOpen=false,recognitionBackup=null,recognitionRequest=false;
  const $=(s,root=document)=>root.querySelector(s),$$=(s,root=document)=>[...root.querySelectorAll(s)];
  const matcher=createScanMatcher({api,esc,icon,getScan:()=>({id:lastID,plan:formPlan,session}),readEdits,isOpen:()=>viewOpen&&session?.status==='ready',onChange:()=>{checked=false;previews=[];render();}});
  const manual=createManualScan({getScan:()=>({session,plan:formPlan}),readParentEdits:readEdits,esc,onApply:group=>{
    checked=false;previews=[];render();
    requestAnimationFrame(()=>{const section=$$('.scan-group').find(el=>el.dataset.group===group.id);section?.scrollIntoView({block:'start'});$('input[name=title]',section||document)?.focus({preventScroll:true});});
  }});
  const busy=s=>['scanning','recognizing','checking','importing'].includes(s);
  function pause(){viewOpen=false;if(pollTimer){clearTimeout(pollTimer);pollTimer=null;}}
  function fail(error){toast(error.message||String(error),true);}
  function setup(sourceId,path){
    pause();viewOpen=true;const state=getState(),source=state.sources.find(s=>s.id===sourceId);if(!source)return;
    modal('扫描整个文件夹','自动查找当前目录和所有子目录中的视频。',`<form id="scan-start"><div class="notice neutral">${icon('folder')}<span>${esc(source.name)} · ${esc(path)}</span></div><p class="section-note">按作品归组并识别季集，确认后再入库。一个目录里可以包含不同作品和多个季。</p><label class="field"><span>识别方式</span><select name="method"><option value="local" ${!state.config.ai?.enabled?'selected':''}>本地识别 · 无需 AI</option><option value="ai" ${state.config.ai?.enabled?'selected':''} ${state.config.ai?.enabled?'':'disabled'}>AI 识别${state.config.ai?.model?' · '+esc(state.config.ai.model):' · 请先配置'}</option></select><small>AI 接收目录名和视频文件的相对路径，视频原文件留在原位置。</small></label>${!state.config.ai?.enabled?'<button type="button" class="button small" data-action="scan-configure-ai">配置 AI</button>':'<p class="section-note">AI 调用会使用你配置的服务。文件较多时分批识别，可查看进度。</p>'}<div id="scan-start-error"></div></form>`,`<button class="button quiet" data-action="close-modal">返回</button><button class="button primary" data-action="scan-start" data-source="${esc(sourceId)}" data-path="${esc(path)}">${icon('search')}开始扫描</button>`);
  }
  async function start(el){
    const method=$('#scan-start [name=method]')?.value||'local';el.disabled=true;
    try{const result=await api('/scans',{sourceId:el.dataset.source,path:el.dataset.path,method});matcher.reset();manual.reset();lastID=result.id;lastSource=el.dataset.source;session=null;formPlan=null;checked=false;previews=[];aiInstructions='';feedbackOpen=false;recognitionBackup=null;mode=getState().sources.find(s=>s.id===lastSource)?.type==='local'?'reference':'download';outputRoot=getState().config.mediaRoot;lastSignature='';await poll();}catch(e){el.disabled=false;$('#scan-start-error').innerHTML=`<div class="error-box">${esc(e.message)}</div>`;}
  }
  async function poll(){
    if(!viewOpen)return;if(pollTimer){clearTimeout(pollTimer);pollTimer=null;}try{const next=await api('/scans/'+lastID);if(!viewOpen)return;const signature=JSON.stringify([next.status,next.folders,next.videoCount,next.processed,next.tokens,next.error,next.results,next.plan.method,next.aiInstructions]);
      const previous=session;if(!session)aiInstructions=next.aiInstructions||'';session=next;
      if(signature!==lastSignature){
        lastSignature=signature;
        if(!busy(next.status)){
          if(recognitionBackup){
            const saved=recognitionBackup;recognitionBackup=null;
            if(next.status==='ready'&&next.error){formPlan=saved.plan;previews=saved.previews;checked=saved.checked;feedbackOpen=saved.method==='ai';}
            else{formPlan=structuredClone(next.plan);previews=next.previews||[];checked=false;matcher.reset();manual.reset();feedbackOpen=false;}
          }else{if(!(next.status==='ready'&&previous?.status==='ready'&&formPlan))formPlan=structuredClone(next.plan);checked=false;previews=next.previews||[];}
        }
        render();
      }
      if(busy(next.status))pollTimer=setTimeout(poll,1000);
    }catch(e){fail(e);}
  }
  function counts(){const groups=(formPlan?.groups||[]).filter(g=>g.selected);return {groups:groups.length,files:groups.reduce((n,g)=>n+g.files.filter(f=>f.selected).length,0),unmatched:groups.filter(g=>!g.matchToken&&!g.manual&&g.files.some(f=>f.selected)).length};}
  function editBody(){readEdits();return {mode,outputRoot,groups:formPlan.groups};}
  function readEdits(){
    if(!formPlan||!$('#scan-plan'))return;
    readAIInstructions();
    manual.readEdits();
    const modeInput=$('#scan-mode');if(modeInput)mode=modeInput.value;const rootInput=$('#scan-output');if(rootInput)outputRoot=rootInput.value.trim();
    $$('.scan-group').forEach(section=>{const g=formPlan.groups.find(g=>g.id===section.dataset.group);if(!g)return;const before=matcher.identity(g);g.selected=!!$('[name=groupSelected]',section)?.checked;g.title=$('[name=title]',section).value.trim();g.type=$('[name=type]',section).value;g.year=$('[name=year]',section).value.trim();g.itemId=$('[name=itemId]',section)?.value||'';matcher.identityChanged(g,before);
      $$('[data-scan-file]',section).forEach(row=>{const f=g.files.find(f=>f.id===Number(row.dataset.scanFile));if(!f)return;f.selected=!!$('[name=fileSelected]',row)?.checked;f.season=g.type==='tv'?Number($('[name=season]',row)?.value||0):0;f.episode=g.type==='tv'?Number($('[name=episode]',row)?.value||0):0;f.episodeEnd=g.type==='tv'?Number($('[name=episodeEnd]',row)?.value||0):0;});
    });
  }
  function markDirty(){if(document.activeElement?.matches('#scan-ai-instructions')){readAIInstructions();return;}if(document.activeElement?.matches('[data-match-query]'))return;readEdits();checked=false;const selected=$('#scan-selected-count');if(selected)selected.textContent='已选 '+counts().files+' 个视频';const button=$('[data-action="scan-confirm"]');if(button){button.textContent='检查导入预览';button.disabled=!counts().files||!!counts().unmatched;const hint=$('#scan-match-hint');if(hint)hint.textContent=counts().unmatched?'还有 '+counts().unmatched+' 部作品待确认匹配，也可选择暂不匹配。':'已确认的作品将沿用资料编号补全详情。';}}
  function readAIInstructions(){const input=$('#scan-ai-instructions');if(input)aiInstructions=input.value;const count=$('#scan-ai-instructions-count');if(count)count.textContent=Array.from(aiInstructions).length+' / 2000';}
  function feedbackHTML(){
    if(!feedbackOpen)return session.aiInstructions&&formPlan.method==='ai'&&!session.error?`<p class="scan-feedback-summary">本次识别说明：${esc(session.aiInstructions)}</p>`:'';
    return `<section class="scan-ai-feedback" id="scan-ai-feedback"><label class="field"><span>告诉 AI 应该如何识别（可选）</span><textarea id="scan-ai-instructions" rows="4" maxlength="2000" placeholder="例如：这些都是《某某剧》第一季，文件名前两位是集号；彩蛋目录归特别篇，按文件名中的编号填写。">${esc(aiInstructions)}</textarea></label><div class="scan-feedback-meta"><p class="section-note">说明仅用于本次扫描，会随目录名、文件名发送给已配置的 AI。重新整理全部 ${session.videoCount} 个视频；成功后替换当前预览，需重新确认作品，失败时保留原预览。</p><span id="scan-ai-instructions-count">${Array.from(aiInstructions).length} / 2000</span></div><div class="button-row"><button class="button small primary" data-action="scan-ai-run">${icon('star')}按说明重新识别</button><button class="button small quiet" data-action="scan-ai-close">收起</button></div></section>`;
  }
  async function reidentify(method,el){
    if(recognitionRequest||busy(session?.status))return;
    readEdits();if(method==='ai'&&!getState().config.ai?.enabled)return;
    if(method==='ai'&&Array.from(aiInstructions.trim()).length>2000)throw new Error('补充说明最多 2000 字。');
    const saved={plan:structuredClone(formPlan),previews:structuredClone(previews),checked,method};
    recognitionRequest=true;el.disabled=true;
    try{
      await api('/scans/'+lastID+'/recognize',{method,...(method==='ai'?{instructions:aiInstructions.trim()}:{})});
      recognitionBackup=saved;lastSignature='';await poll();
    }finally{recognitionRequest=false;if(el.isConnected)el.disabled=false;}
  }
  function planHTML(){
    const state=getState(),count=counts(),issues=new Map(),groupErrors=new Map();for(const p of previews){if(p.error)groupErrors.set(p.groupId,p.error);for(const row of p.preview?.rows||[])issues.set(row.entry.key,{text:row.issue||'',duplicate:row.duplicate,destination:row.destination});}
    return `<div id="scan-plan"><div class="scan-summary"><span>${formPlan.method==='ai'?'AI 识别':'本地识别'} · ${formPlan.groups.length} 部作品</span><span><b id="scan-selected-count">已选 ${count.files} 个视频</b></span>${session.model&&formPlan.method==='ai'?`<span>${esc(session.model)}</span>`:''}${session.tokens?`<span>本次记录用量 ${session.tokens} tokens</span>`:''}</div><div class="scan-options">${session.sourceType==='local'?`<label class="field"><span>本地视频如何入库</span><select id="scan-mode"><option value="reference" ${mode==='reference'?'selected':''}>直接入库 · 保留原位置（推荐）</option><option value="copy" ${mode==='copy'?'selected':''}>复制整理 · 另外保存一份</option></select></label>`:''}${mode!=='reference'?`<label class="field"><span>${mode==='copy'?'复制到':'下载到'}</span><input id="scan-output" value="${esc(outputRoot)}" required></label>`:''}</div><div class="scan-match-intro">${icon('info')}<span>名称与季集最初来自文件名或 AI 推测。请在下方确认在线作品；找到候选不代表已匹配。</span></div><div class="scan-tools"><button class="button small" data-action="scan-search-unmatched">${icon('search')}重新检索未确认作品</button><button class="button small" data-action="scan-ai" ${!state.config.ai?.enabled?'disabled':''}>${icon('star')}${formPlan.method==='ai'?'用 AI 重新识别':'用 AI 识别'}</button><button class="button small quiet" data-action="scan-local">使用本地识别结果</button></div>${formPlan.groups.map((g,index)=>`<section class="scan-group" data-group="${esc(g.id)}"><header><label class="checkbox-label"><input type="checkbox" name="groupSelected" ${g.selected?'checked':''}>作品 ${index+1}</label><span data-match-status>${matcher.badge(g)}</span><span>${g.files.length} 个视频</span></header><div class="scan-group-fields"><label class="field"><span>作品名称</span><input name="title" maxlength="150" value="${esc(g.title)}" required></label><label class="field"><span>类型</span><select name="type" data-scan-kind><option value="tv" ${g.type==='tv'?'selected':''}>电视剧</option><option value="movie" ${g.type==='movie'?'selected':''}>电影</option></select></label><label class="field"><span>年份（可空）</span><input name="year" maxlength="4" value="${esc(g.year)}" placeholder="自动补齐"></label>${g.type==='tv'?`<label class="field scan-existing"><span>所属剧集</span><select name="itemId" data-scan-series><option value="">自动识别归属</option>${state.items.filter(it=>it.type==='tv').map(it=>`<option value="${it.id}" ${g.itemId===it.id?'selected':''}>${esc(it.title)}${it.year?' ('+esc(it.year)+')':''}</option>`).join('')}</select></label>`:''}</div>${g.note?`<p class="scan-note">初始识别：${esc(g.note)}</p>`:''}<div class="scan-match-panel" data-match-panel>${matcher.markup(g)}</div>${groupErrors.has(g.id)?`<div class="error-box">${esc(groupErrors.get(g.id))}</div>`:''}<div class="table-scroll"><table class="scan-table"><thead><tr><th></th><th>视频文件</th>${g.type==='tv'?'<th>季</th><th>起始集</th><th>结束集</th>':''}</tr></thead><tbody>${g.files.map(f=>{const issue=issues.get(f.key);const rel=session.files.find(v=>v.id===f.id)?.relative||f.key;return `<tr data-scan-file="${f.id}"><td><input type="checkbox" name="fileSelected" aria-label="导入${esc(rel)}" ${f.selected?'checked':''}></td><td><div>${esc(rel)}</div>${issue?.text?`<small class="${issue.duplicate?'scan-duplicate':'row-issue'}">${esc(issue.text)}</small>`:''}${checked&&mode!=='reference'&&issue?.destination?`<small class="scan-target">${esc(issue.destination)}</small>`:''}</td>${g.type==='tv'?`<td><input type="number" name="season" min="0" max="999" value="${f.season}" aria-label="${esc(rel)}的季数"></td><td><input type="number" name="episode" min="1" max="9999" value="${f.episode||''}" aria-label="${esc(rel)}的起始集"></td><td><input type="number" name="episodeEnd" min="0" max="9999" value="${f.episodeEnd||''}" placeholder="单集留空" aria-label="${esc(rel)}的结束集"></td>`:''}</tr>`}).join('')}</tbody></table></div></section>`).join('')}${manual.markup()}</div>`;
  }
  function render(){
    if(!session)return;
    const priorScroll=$('.modal-body')?.scrollTop||0;
    if(busy(session.status)){
      const title=({scanning:'正在扫描目录',recognizing:'AI 正在识别',checking:'正在检查导入内容',importing:'正在加入媒体库'})[session.status];
      const progress=session.status==='recognizing'?`已处理 ${session.processed} / ${session.videoCount} 个视频`:`已找到 ${session.videoCount} 个视频 · ${session.folders} 个目录`;
      modal(title,esc(session.folder),`<div class="scan-progress"><span class="spinner"></span><h3>${title}</h3><p>${progress}</p>${session.status==='recognizing'?'<p>AI 根据目录和文件名整理作品与季集，请稍等。</p>':''}${session.results?.length?`<p>已完成 ${session.results.length} 个作品分组</p>`:''}</div>`,`<button class="button quiet" data-action="close-modal">后台继续</button><button class="button" data-action="scan-cancel">取消本次操作</button>`,true);return;
    }
    if(session.status==='complete'){
      const added=session.results.reduce((n,r)=>n+r.count,0),failed=session.results.filter(r=>r.status==='failed').length;
      modal(failed?'部分作品需要处理':'本次入库已完成',`${added} 个新视频 · ${session.results.length} 个作品分组`,`<div class="scan-results">${session.results.map(r=>`<div><span class="badge ${r.status==='failed'?'red':r.status==='skipped'?'':'green'}">${r.status==='completed'?'已添加':r.status==='skipped'?'已在库中':'需要处理'}</span><span><strong>${esc(r.title)}</strong><small>${r.error?esc(r.error):r.count+' 个新增视频'+(r.skipped?' · 跳过 '+r.skipped+' 个重复文件':'')}</small></span></div>`).join('')}</div>`,`<button class="button quiet" data-action="scan-edit">返回修改</button><button class="button primary" data-action="scan-library">打开媒体库 ${icon('arrow')}</button>`,true);return;
    }
    if(session.status==='failed'||session.status==='cancelled'){
      modal(session.status==='cancelled'?'本次操作已取消':'目录扫描未完成',esc(session.folder),`<div class="notice">${icon('info')}<span>${esc(session.error||'可以重新扫描目录。已经入库的作品会保留。')}</span></div>`,`<button class="button" data-action="close-modal">关闭</button>`,true);return;
    }
    modal('确认作品与季集',`${esc(session.folder)} · ${session.videoCount} 个视频`,`${session.error?`<div class="notice">${icon('info')}<span>${esc(session.error)}</span></div>`:''}${planHTML()}<div id="scan-error"></div>`,`<span class="section-note" id="scan-match-hint">${counts().unmatched?'还有 '+counts().unmatched+' 部作品待确认匹配，也可选择暂不匹配。':'已确认的作品将沿用资料编号补全详情。'}</span><button class="button primary" data-action="scan-confirm" ${!formPlan.groups.length||!counts().files||counts().unmatched?'disabled':''}>${checked?(mode==='reference'?'确认入库':mode==='copy'?'开始复制并入库':'开始下载并入库'):'检查导入预览'} ${icon('arrow')}</button>`,true);
    $('.scan-tools')?.insertAdjacentHTML('beforeend','<button class="button small quiet" data-action="scan-select-all">全选已识别视频</button><button class="button small quiet" data-action="scan-select-none">取消全选</button>');
    $('.scan-tools')?.insertAdjacentHTML('afterend',feedbackHTML());
    const aiButton=$('[data-action="scan-ai"]');if(aiButton){aiButton.setAttribute('aria-expanded',String(feedbackOpen));aiButton.setAttribute('aria-controls','scan-ai-feedback');}
    requestAnimationFrame(()=>{const body=$('.modal-body');if(body)body.scrollTop=priorScroll;});
    queueMicrotask(()=>matcher.auto());
  }
  async function action(name,el){
    try{
      if(manual.action(name,el))return;
      if(matcher.action(name,el))return;
      if(name==='scan-select-all'||name==='scan-select-none'){
        readEdits();const selected=name==='scan-select-all';
        for(const group of formPlan.groups){group.selected=selected;for(const file of group.files)file.selected=selected;}
        checked=false;previews=[];render();return;
      }
      if(name==='scan-open'){setup(el.dataset.source,el.dataset.path);return;}
      if(name==='scan-start'){await start(el);return;}
      if(name==='scan-configure-ai'){closeModal();navigate('settings');$('#ai-settings')?.scrollIntoView({block:'start'});return;}
      if(name==='scan-resume'){if(!lastID)return;viewOpen=true;lastSignature='';await poll();return;}
      if(name==='scan-cancel'){await api('/scans/'+lastID+'/cancel',{});lastSignature='';await poll();return;}
      if(name==='scan-library'){closeModal();await refresh();navigate('library');return;}
      if(name==='scan-edit'){manual.reset();session.status='ready';formPlan=structuredClone(session.plan);checked=false;previews=[];render();return;}
      if(name==='scan-ai'){readEdits();if(!getState().config.ai?.enabled)return;feedbackOpen=true;render();requestAnimationFrame(()=>{$('#scan-ai-instructions')?.focus();$('#scan-ai-feedback')?.scrollIntoView({block:'nearest'});});return;}
      if(name==='scan-ai-close'){readAIInstructions();feedbackOpen=false;render();return;}
      if(name==='scan-ai-run'||name==='scan-local'){await reidentify(name==='scan-local'?'local':'ai',el);return;}
      if(name==='scan-confirm'){
        el.disabled=true;const body=editBody();if(checked){await api('/scans/'+lastID+'/import',body);lastSignature='';await poll();return;}
        const result=await api('/scans/'+lastID+'/preview',body);previews=result.previews;checked=!!result.valid;render();if(!checked)toast('请核对标出的文件，修改或取消勾选后重新检查',true);return;
      }
    }catch(e){if(el?.isConnected)el.disabled=false;const box=$('#scan-error');if(box)box.innerHTML=`<div class="error-box">${esc(e.message)}</div>`;else fail(e);}
  }
  function changed(el){
    if(el.id==='scan-ai-instructions'){readAIInstructions();return;}
    if(manual.changed(el))return;
    if(!el.closest('#scan-plan')||el.matches('[data-match-query]'))return;readEdits();markDirty();
    if(el.id==='scan-mode'||el.matches('[data-scan-kind]')){render();return;}
    if(el.matches('[name=groupSelected]'))matcher.auto();
    if(el.matches('[data-scan-series]')){const g=formPlan.groups.find(g=>g.id===el.closest('.scan-group').dataset.group),it=getState().items.find(it=>it.id===el.value);if(g&&it){const before=matcher.identity(g);g.title=it.title;g.year=it.year;matcher.identityChanged(g,before);}render();}
  }
  return {open:setup,action,changed,markDirty,pause,hasPrevious:source=>!!lastID&&lastSource===source};
}
