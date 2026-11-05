export function createScanMatcher({api,esc,icon,getScan,readEdits,onChange,isOpen}) {
  let states=new Map(),queue=[],active=0,generation=0;
  const identity=g=>JSON.stringify([g.type,g.title,g.year]);
  const state=g=>{if(!states.has(g.id))states.set(g.id,{status:'idle',query:g.title,results:[],serial:0});return states.get(g.id);};
  const section=g=>[...document.querySelectorAll('.scan-group')].find(el=>el.dataset.group===g.id);
  const art=path=>'/api/art?path='+encodeURIComponent(path);
  function reset(){generation++;states=new Map();queue=[];}
  function badge(g){
    const s=state(g);
    if(g.matchToken)return '<span class="badge green">已确认匹配</span>';
    if(g.manual)return '<span class="badge orange">未在线匹配 · 按当前名称入库</span>';
    if(s.status==='queued'||s.status==='loading')return '<span class="badge">正在检索资料</span>';
    if(s.results.length)return '<span class="badge orange">已找到候选 · 待确认</span>';
    return '<span class="badge orange">'+(getScan().plan?.method==='ai'?'AI 推测 · 未匹配':'文件名推测 · 未匹配')+'</span>';
  }
  function candidate(c,g,chosen=false){
    return `<article class="scan-candidate ${chosen?'confirmed':''}">${c.poster?`<img src="${art(c.poster)}" alt="${esc(c.title)}海报" loading="lazy">`:'<span class="scan-candidate-art">'+icon('film')+'</span>'}<div class="scan-candidate-text"><strong>${esc(c.title)}</strong><div class="scan-candidate-meta">${esc(c.year||'年份未知')} · ${esc(c.providerName)}${c.externalId?' · '+esc(c.externalId):''}</div><p>${esc(c.overview||'暂无简介，请打开资料来源核对。')}</p><a href="${esc(c.url)}" target="_blank" rel="noopener noreferrer">查看资料来源 ↗</a></div>${chosen?'<span class="scan-match-check">'+icon('check')+'</span>':`<button class="button small" data-action="scan-use-match" data-group="${esc(g.id)}" data-token="${esc(c.token)}">确认此作品</button>`}</article>`;
  }
  function markup(g){
    const s=state(g);
    if(g.matchToken){
      const c=s.selected||s.results.find(c=>c.token===g.matchToken);
      return `<div class="scan-match-caption">已确认在线作品；入库后沿用此资料编号补全详情。</div>${c?candidate(c,g,true):'<p class="scan-note">已保存确认的作品匹配。</p>'}<button class="button small quiet" data-action="scan-change-match" data-group="${esc(g.id)}">更换作品</button>`;
    }
    const loading=s.status==='queued'||s.status==='loading';
    return `<div class="scan-match-caption">${g.manual?'将使用当前填写的名称与年份入库，本次不自动猜测在线作品。':'请选择对应作品。检索到候选后，仍需你确认海报、名称和年份。'}</div><div class="scan-match-search"><input data-match-query aria-label="作品 ${esc(g.id)} 的资料搜索词" value="${esc(s.query)}" placeholder="输入片名、原名或其他译名"><button class="button small" data-action="scan-search-match" data-group="${esc(g.id)}" ${loading?'disabled':''}>${loading?'<span class="spinner"></span>检索中':icon('search')+'搜索资料'}</button></div>${s.error?`<div class="scan-match-error" role="status">${esc(s.error)}。可重试，或暂不匹配。</div>`:''}${s.status==='done'&&!s.results.length?'<p class="scan-match-empty">没有找到合适的影视条目，可换个片名检索，或暂不匹配。</p>':''}${s.results.length?`<div class="scan-candidates">${s.results.map(c=>candidate(c,g)).join('')}</div>`:''}<button class="button small quiet scan-manual-choice" data-action="scan-skip-match" data-group="${esc(g.id)}" ${g.manual?'disabled':''}>${g.manual?'已选择按当前名称入库':'暂不匹配，按当前名称入库'}</button>`;
  }
  function update(g){
    if(!isOpen())return;
    const root=section(g);if(!root)return;
    const panel=root.querySelector('[data-match-panel]'),status=root.querySelector('[data-match-status]');
    if(panel){const query=panel.querySelector('[data-match-query]');if(query)safeQuery(g,query.value);panel.innerHTML=markup(g);}
    if(status)status.innerHTML=badge(g);
  }
  function safeQuery(g,value){state(g).query=value;}
  function identityChanged(g,before){
    if(identity(g)===before)return;
    g.matchToken='';g.manual=false;
    const s=state(g);s.serial++;s.status='idle';s.query=g.title;s.results=[];s.selected=null;s.error='';
    const root=section(g);if(root?.querySelector('[data-match-panel]')){root.querySelector('[data-match-panel]').innerHTML=markup(g);root.querySelector('[data-match-status]').innerHTML=badge(g);}
  }
  function search(g,query){
    query=String(query??g.title).trim();if(!query)return;
    const s=state(g);s.query=query;s.status='queued';s.error='';const serial=++s.serial;
    queue.push({group:g,query,type:g.type,year:g.year,serial,generation,id:getScan().id});update(g);pump();
  }
  function pump(){
    while(active<2&&queue.length&&isOpen()){
      const job=queue.shift(),g=job.group,s=state(g);
      if(job.generation!==generation||job.serial!==s.serial)continue;
      active++;s.status='loading';update(g);
      api('/scans/'+job.id+'/matches',{groupId:g.id,type:job.type,query:job.query,year:job.year}).then(result=>{
        if(job.generation!==generation||job.serial!==s.serial)return;
        s.results=result.results;s.status='done';
      }).catch(error=>{if(job.generation===generation&&job.serial===s.serial){s.status='error';s.error=error.message;}}).finally(()=>{
        active--;if(job.generation===generation&&job.serial===s.serial)update(g);pump();
      });
    }
  }
  function auto(){
    if(!isOpen())return;
    for(const g of getScan().plan?.groups||[]){if(g.selected&&!g.matchToken&&!g.manual&&state(g).status==='idle')search(g,state(g).query);}
    pump();
  }
  function action(name,el){
    if(!['scan-search-match','scan-use-match','scan-change-match','scan-skip-match','scan-search-unmatched'].includes(name))return false;
    readEdits();
    if(name==='scan-search-unmatched'){
      for(const g of getScan().plan.groups){if(g.selected&&!g.matchToken&&!g.manual&&!['queued','loading'].includes(state(g).status))search(g,state(g).query);}
      return true;
    }
    const g=getScan().plan.groups.find(g=>g.id===el.dataset.group);if(!g)return true;
    const s=state(g);
    if(name==='scan-search-match'){search(g,section(g)?.querySelector('[data-match-query]')?.value);return true;}
    if(name==='scan-use-match'){
      const c=s.results.find(c=>c.token===el.dataset.token);if(!c||c.type!==g.type)return true;
      g.title=c.title;g.year=c.year;g.matchToken=c.token;g.manual=false;s.selected=c;
    }else if(name==='scan-skip-match'){g.matchToken='';g.manual=true;s.selected=null;}
    else {g.matchToken='';g.manual=false;s.selected=null;}
    onChange();return true;
  }
  return {reset,badge,markup,identity,identityChanged,auto,action};
}
