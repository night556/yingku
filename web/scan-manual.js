const fileOrder=new Intl.Collator('zh-CN',{numeric:true,sensitivity:'base'});

export function buildManualFiles(session,plan,ids,{season=1,numbering='filename',start=1,forceSeason=false}={}) {
  if(!Number.isInteger(season)||season<0||season>999)throw new Error('季数应为 0–999，特别篇填 0。');
  if(!['filename','sequence'].includes(numbering))throw new Error('请选择集号填写方式。');
  if(numbering==='sequence'&&(!Number.isInteger(start)||start<1||start>9999))throw new Error('起始集号应为 1–9999。');
  const selected=new Set(ids),pending=new Set(plan.unassigned.map(f=>f.id));
  if(!selected.size)throw new Error('请先勾选要归类的待确认文件。');
  const assigned=new Set(plan.groups.flatMap(g=>g.files.map(f=>f.id)));
  const originals=new Map(session.files.map(f=>[f.id,f]));
  for(const id of selected)if(!pending.has(id)||assigned.has(id)||!originals.has(id))throw new Error('待确认文件已变化，请重新选择。');
  const files=[...selected].map(id=>originals.get(id)).sort((a,b)=>fileOrder.compare(a.relative||a.key,b.relative||b.key)||a.id-b.id);
  if(numbering==='sequence'&&start+files.length-1>9999)throw new Error('连续编号超出了第 9999 集，请分批归类。');
  return files.map((f,index)=>{
    const hint=f.episodeHint||{};
    const episode=numbering==='sequence'?start+index:(Number.isInteger(hint.episode)&&hint.episode>0&&hint.episode<=9999?hint.episode:0);
    const end=numbering==='filename'&&episode&&Number.isInteger(hint.episodeEnd)&&hint.episodeEnd>episode&&hint.episodeEnd<=9999&&hint.episodeEnd-episode<100?hint.episodeEnd:0;
    const fileSeason=numbering==='filename'&&!forceSeason&&Number.isInteger(hint.season)&&hint.season>=0&&hint.season<=999?hint.season:season;
    return {id:f.id,key:f.key,name:f.entry.name,season:fileSeason,episode,episodeEnd:end,selected:true};
  });
}

export function manualFileIssues(files,existing=[]) {
  const seen=new Set(),duplicates=new Set(),incoming=new Set(files);let unknown=0;
  for(const f of [...existing.filter(f=>f.selected),...files]){
    if(!f.episode){if(incoming.has(f))unknown++;continue;}
    let repeated=false;
    const last=f.episodeEnd>=f.episode&&f.episodeEnd-f.episode<100?f.episodeEnd:f.episode;
    for(let ep=f.episode;ep<=last;ep++){
      const key=f.season+':'+ep;if(seen.has(key))repeated=true;seen.add(key);
    }
    if(repeated&&incoming.has(f))duplicates.add(f.id);
  }
  return {unknown,duplicates};
}

export function applyManualFiles(session,plan,ids,options) {
  const target=options.target?plan.groups.find(g=>g.id===options.target&&g.type==='tv'):null;
  if(options.target&&!target)throw new Error('要追加的剧集分组已变化，请重新选择。');
  const title=String(options.title||'').trim(),year=String(options.year||'').trim();
  if(!target&&(!title||[...title].length>150))throw new Error('请填写作品名称，最多 150 字。');
  if(!target&&year&&!/^(18|19|20|21)\d{2}$|^2200$/.test(year))throw new Error('年份请填写 1800–2200，或留空。');
  const files=buildManualFiles(session,plan,ids,options);
  const group=target||{id:'manual-'+files[0].id,type:'tv',title,year,confidence:0,selected:true,manual:false,note:'手动批量归类；作品只需匹配一次，季集请核对下表。',files:[]};
  if(!target&&plan.groups.some(g=>g.id===group.id))throw new Error('作品分组编号已存在，请重新扫描。');
  group.files.push(...files);group.selected=true;
  if(!target)plan.groups.push(group);
  const added=new Set(files.map(f=>f.id));plan.unassigned=plan.unassigned.filter(f=>!added.has(f.id));
  return group;
}

export function createManualScan({getScan,readParentEdits,onApply,esc}) {
  let selected=new Set(),draft=null,error='';
  const root=()=>document.querySelector('.scan-unassigned');
  function reset(){selected=new Set();draft=null;error='';}
  function sync(){const pending=new Set(getScan().plan?.unassigned.map(f=>f.id)||[]);selected=new Set([...selected].filter(id=>pending.has(id)));}
  function readEdits(){
    const panel=root();if(!panel)return;
    selected=new Set([...panel.querySelectorAll('[data-manual-selected]:checked')].map(el=>Number(el.dataset.manualSelected)));
    if(draft)for(const el of panel.querySelectorAll('[data-manual-field]'))draft[el.dataset.manualField]=el.type==='checkbox'?el.checked:['season','start'].includes(el.dataset.manualField)?(el.value===''?NaN:Number(el.value)):el.value;
  }
  function preview(){
    if(!draft)return {files:[],unknown:0,duplicates:new Set(),error:''};
    try{
      const {session,plan}=getScan(),files=buildManualFiles(session,plan,selected,draft),target=plan.groups.find(g=>g.id===draft.target);
      return {files,...manualFileIssues(files,target?.files),error:''};
    }catch(e){return {files:[],unknown:0,duplicates:new Set(),error:e.message};}
  }
  function markup(){
    const {session,plan}=getScan();if(!plan?.unassigned.length)return '';sync();
    const result=preview(),numbers=new Map(result.files.map(f=>[f.id,f]));
    const groups=plan.groups.filter(g=>g.type==='tv');
    const groupName=g=>g.title||'未命名剧集（作品 '+(plan.groups.indexOf(g)+1)+'）';
    return `<section class="scan-unassigned"><h3>待确认文件 · ${plan.unassigned.length}</h3><p class="section-note">同一部剧可以勾选后批量归类，作品只需确认一次；不同作品请分批选择。</p><div class="scan-pending-tools"><label class="checkbox-label"><input type="checkbox" data-manual-all ${selected.size===plan.unassigned.length?'checked':''}>全选待确认文件</label><span>已选 ${selected.size} 个</span><button class="button small" data-action="scan-manual-batch" ${!selected.size?'disabled':''}>批量归类为同一部剧</button></div>${draft?`<div class="scan-manual-editor" id="scan-manual-editor"><h4>归类这 ${selected.size} 个文件</h4><div class="scan-manual-fields"><label class="field scan-manual-target"><span>归入作品</span><select data-manual-field="target"><option value="">新建同一部剧</option>${groups.map(g=>`<option value="${esc(g.id)}" ${draft.target===g.id?'selected':''}>追加到：${esc(groupName(g))}${g.matchToken?' · 已确认匹配':''}</option>`).join('')}</select></label>${!draft.target?`<label class="field"><span>作品名称</span><input data-manual-field="title" maxlength="150" value="${esc(draft.title)}" placeholder="填写整部剧的名称"></label><label class="field"><span>年份（可空）</span><input data-manual-field="year" maxlength="4" value="${esc(draft.year)}" placeholder="匹配后补齐"></label>`:''}<label class="field"><span>${draft.numbering==='sequence'?'统一季数':'默认季数'}</span><input type="number" min="0" max="999" data-manual-field="season" value="${Number.isFinite(draft.season)?draft.season:''}"><small>${draft.numbering==='sequence'?'全部选中文件使用此季数。':'文件已标注季数时保留原值；特别篇填 0。'}</small></label><label class="field"><span>集号填写方式</span><select data-manual-field="numbering"><option value="filename" ${draft.numbering==='filename'?'selected':''}>从文件名提取集号（推荐）</option><option value="sequence" ${draft.numbering==='sequence'?'selected':''}>按文件名排序，连续编号</option></select></label>${draft.numbering==='sequence'?`<label class="field"><span>起始集号</span><input type="number" min="1" max="9999" data-manual-field="start" value="${Number.isFinite(draft.start)?draft.start:''}"></label>`:`<label class="checkbox-label scan-manual-force"><input type="checkbox" data-manual-field="forceSeason" ${draft.forceSeason?'checked':''}>全部使用默认季数</label>`}</div><p class="section-note">${draft.target?'追加后沿用这个分组的作品信息和匹配结果。':'填写一次作品名称，应用后统一检索并确认作品。'} ${draft.numbering==='sequence'?'按下方文件顺序连续编号，请核对顺序与起始集号。':'保留文件名里的实际集号，07 不会重新编号为 01；无法确定的集号留空，应用后可逐集补填。'}</p>${result.unknown?`<p class="scan-manual-warning">${result.unknown} 个文件缺少集号，归类后仍需填写。</p>`:''}${result.duplicates.size?`<p class="scan-manual-warning">${result.duplicates.size} 个文件存在重复或重叠集号，请在入库前核对。</p>`:''}${error||result.error?`<div class="error-box" role="alert">${esc(error||result.error)}</div>`:''}<div class="button-row"><button class="button small primary" data-action="scan-manual-apply" ${!selected.size||result.error?'disabled':''}>应用归类 · ${selected.size} 个文件</button><button class="button small quiet" data-action="scan-manual-cancel">取消</button></div></div>`:''}${[...plan.unassigned].sort((a,b)=>fileOrder.compare(a.name,b.name)||a.id-b.id).map(f=>{const number=numbers.get(f.id),label=number?(number.episode?`第 ${number.season} 季 · 第 ${number.episode}${number.episodeEnd?'–'+number.episodeEnd:''} 集${result.duplicates.has(f.id)?' · 集号冲突，待核对':''}`:`第 ${number.season} 季 · 集号待填写`):f.reason;return `<div class="scan-pending-file"><label class="scan-pending-choice"><input type="checkbox" data-manual-selected="${f.id}" aria-label="归类${esc(f.name)}" ${selected.has(f.id)?'checked':''}><span><strong>${esc(f.name)}</strong><small>${esc(label)}</small></span></label><button class="button small" data-action="scan-manual" data-file="${f.id}">手动归类</button></div>`;}).join('')}</section>`;
  }
  function redraw(focus){
    const panel=root();if(!panel)return;
    const active=document.activeElement,field=active?.dataset.manualField,file=active?.dataset.manualSelected,wasAll=active?.hasAttribute('data-manual-all');
    const body=document.querySelector('.modal-body'),scroll=body?.scrollTop||0;
    panel.outerHTML=markup();
    const next=focus||(field?`[data-manual-field="${field}"]`:file?`[data-manual-selected="${file}"]`:wasAll?'[data-manual-all]':null);
    if(next)root()?.querySelector(next)?.focus({preventScroll:true});
    if(body)body.scrollTop=scroll;
    const all=root()?.querySelector('[data-manual-all]');if(all)all.indeterminate=selected.size>0&&selected.size<getScan().plan.unassigned.length;
  }
  function open(){
    const groups=getScan().plan.groups.filter(g=>g.type==='tv');
    draft={target:groups.length===1?groups[0].id:'',title:'',year:'',season:1,numbering:'filename',start:1,forceSeason:false};error='';
    redraw('[data-manual-field="'+(draft.target?'target':'title')+'"]');root()?.querySelector('.scan-manual-editor')?.scrollIntoView({block:'nearest'});
  }
  function action(name,el){
    if(!['scan-manual','scan-manual-batch','scan-manual-cancel','scan-manual-apply'].includes(name))return false;
    readParentEdits();readEdits();
    if(name==='scan-manual'){selected=new Set([Number(el.dataset.file)]);open();}
    else if(name==='scan-manual-batch')open();
    else if(name==='scan-manual-cancel'){draft=null;error='';redraw();}
    else {
      try{const {session,plan}=getScan(),group=applyManualFiles(session,plan,selected,draft);selected.clear();draft=null;error='';onApply(group);}
      catch(e){error=e.message;redraw();}
    }
    return true;
  }
  function changed(el){
    if(!el.closest('.scan-unassigned'))return false;
    readEdits();error='';
    if(el.hasAttribute('data-manual-all'))selected=el.checked?new Set(getScan().plan.unassigned.map(f=>f.id)):new Set();
    redraw();return true;
  }
  return {markup,action,changed,readEdits,reset};
}
