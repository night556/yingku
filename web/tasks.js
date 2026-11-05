export function filterTasks(state,filter,query='') {
  const files=new Map(state.files.map(file=>[file.id,file])),items=new Map(state.items.map(item=>[item.id,item]));
  const text=query.trim().toLocaleLowerCase();
  return state.tasks.filter(task=>{
    if(filter==='active'?!['downloading','finalizing'].includes(task.status):filter!=='all'&&task.status!==filter)return false;
    const file=files.get(task.fileId);return !text||[file?.name,items.get(file?.itemId)?.title].join(' ').toLocaleLowerCase().includes(text);
  }).toReversed();
}

export function createTaskPage({api,getState,esc,icon,bytes,heading,empty,taskBadge,toast,refresh,modal,closeModal}) {
  let filter='all',query='',selected=new Set(),busy=false,pending=null,lastResult=null;
  const $=selector=>document.querySelector(selector);
  const filters=[['all','全部任务'],['active','正在下载'],['queued','等待下载'],['paused','已暂停'],['completed','已完成'],['failed','失败'],['cancelled','已取消']];
  function visible(){return filterTasks(getState(),filter,query);}
  function eligible(task,action){return action==='pause'?['queued','downloading'].includes(task.status):action==='resume'?['paused','failed','cancelled'].includes(task.status):action==='cancel'?!['completed','finalizing'].includes(task.status):task.status!=='finalizing';}
  function render(){
    const state=getState();if(!$('#tasks-page'))document.querySelector('#main').innerHTML=`<section id="tasks-page">${heading('YOUR DOWNLOADS','下载任务','多选任务可批量暂停、继续或取消。移除任务记录会保留所有文件。',`<button class="button" data-action="go-sources">${icon('plus')}添加下载</button>`)}<div id="task-stats" class="task-stats"></div><div class="task-filter-row"><div class="task-filters" id="task-filters" aria-label="下载状态"></div><label class="task-search">${icon('search')}<input type="search" id="tasks-query" aria-label="搜索下载任务" placeholder="搜索作品或文件名" value="${esc(query)}"></label></div><div id="task-batch-bar" class="task-batch-bar"></div><div id="task-result" role="status"></div><div id="task-rows"></div></section>`;
    const rows=visible(),visibleIDs=new Set(rows.map(task=>task.id));selected=new Set([...selected].filter(id=>visibleIDs.has(id)));
    const focusID=document.activeElement?.closest('#tasks-page')?document.activeElement.id:'';
    const focusFilter=document.activeElement?.getAttribute('data-filter');
    const focusBatch=document.activeElement?.closest('#task-batch-bar')?document.activeElement.getAttribute('data-command'):null;
    const focused=document.activeElement?.closest('[data-task-id]')?.getAttribute('data-task-id');
    const focusAction=document.activeElement?.getAttribute('data-command');
    $('#task-stats').innerHTML=[['active','正在下载','download'],['queued','等待下载','clock'],['completed','已完成','check'],['failed','需要处理','info']].map(([id,title,image])=>`<button class="stat-card" data-action="tasks-filter" data-filter="${id}"><div><small>${title}</small><strong>${filterTasks(state,id).length}</strong></div>${icon(image)}</button>`).join('');
    $('#task-filters').innerHTML=filters.map(([id,title])=>`<button class="task-filter ${filter===id?'active':''}" data-action="tasks-filter" data-filter="${id}" aria-pressed="${filter===id}">${title}<span>${filterTasks(state,id).length}</span></button>`).join('');
    $('#tasks-query').disabled=busy;renderSelection();
    $('#task-result').innerHTML=lastResult?`<div class="notice neutral">${esc(lastResult)}</div>`:'';
    $('#task-rows').innerHTML=!rows.length?empty(state.tasks.length?'没有符合条件的任务':'还没有下载任务',query?'换个作品名称或文件名搜索。':'选择其他筛选项，或从来源添加视频。','','download'):`<div class="task-list">${rows.map(task=>{
      const file=state.files.find(file=>file.id===task.fileId),item=state.items.find(item=>item.id===file?.itemId),source=state.sources.find(source=>source.id===file?.sourceId);
      const percent=task.status==='completed'?100:task.total?Math.min(99.9,task.bytes/task.total*100):0;
      return `<article class="task-card task-selectable ${selected.has(task.id)?'selected':''}" data-task-id="${esc(task.id)}"><label class="task-select"><input type="checkbox" class="task-check" data-task-id="${esc(task.id)}" aria-label="选择${esc(file?.name||'视频任务')}" ${selected.has(task.id)?'checked':''} ${busy?'disabled':''}></label><div class="task-main"><h3>${esc(file?.name||'视频文件')}</h3><div class="task-meta"><span>${esc(item?.title||'')}${item?.type==='tv'?` · S${String(file.season).padStart(2,'0')}E${String(file.episode).padStart(2,'0')}`:''}</span><span>来自 ${esc(source?.name||'已移除的来源')}</span>${taskBadge(task.status)}</div><div class="progress-track"><span style="width:${percent}%;${task.status==='completed'?'background:var(--green)':''}"></span></div><div class="task-bytes"><span>${bytes(task.bytes)} / ${bytes(task.total)}${task.status==='downloading'&&task.speed?' · '+bytes(task.speed)+'/s':''}</span><span>${percent.toFixed(percent<100?1:0)}%</span></div>${task.error?`<p class="task-error">${esc(task.error)}</p>`:''}</div><div class="task-actions">${[['pause','暂停'],['resume',task.status==='failed'?'重试':'继续'],['cancel','取消下载']].filter(([action])=>eligible(task,action)).map(([action,title])=>`<button class="button small" data-action="tasks-control" data-command="${action}" data-task-id="${esc(task.id)}" ${busy?'disabled':''}>${title}</button>`).join('')}${task.status==='completed'?`<button class="button small" data-action="detail" data-id="${esc(item?.id)}">查看作品</button>`:''}<button class="button small quiet" data-action="tasks-control" data-command="remove" data-task-id="${esc(task.id)}" ${busy||!eligible(task,'remove')?'disabled':''}>移除记录</button></div></article>`;
    }).join('')}</div>`;
    if(focusID)document.getElementById(focusID)?.focus({preventScroll:true});
    else if(focused){const selector=focusAction?`[data-command="${focusAction}"]`:'.task-check';document.querySelector(`[data-task-id="${CSS.escape(focused)}"] ${selector}`)?.focus({preventScroll:true});}
    else if(focusFilter)document.querySelector(`#task-filters [data-filter="${CSS.escape(focusFilter)}"]`)?.focus({preventScroll:true});
    else if(focusBatch)document.querySelector(`#task-batch-bar [data-command="${CSS.escape(focusBatch)}"]`)?.focus({preventScroll:true});
  }
  function renderSelection(){
    const rows=visible(),picked=rows.filter(task=>selected.has(task.id));
    const bar=$('#task-batch-bar');if(!bar)return;
    bar.innerHTML=`<label class="checkbox-field"><input id="tasks-select-all" type="checkbox" ${rows.length&&picked.length===rows.length?'checked':''} ${!rows.length||busy?'disabled':''}>全选当前结果 <span>(${rows.length})</span></label><span class="task-selection-count">已选 ${picked.length} 项</span><div class="task-batch-actions">${[['pause','暂停所选'],['resume','继续所选'],['cancel','取消下载'],['remove','移除记录']].map(([action,label])=>`<button class="button small ${action==='remove'?'danger':''}" data-action="tasks-batch" data-command="${action}" ${busy||!picked.some(task=>eligible(task,action))?'disabled':''}>${label}</button>`).join('')}<button class="button small quiet" data-action="tasks-clear" ${busy||!picked.length?'disabled':''}>清空选择</button></div>${busy?'<span class="section-note">正在处理所选任务…</span>':''}`;
    $('#tasks-select-all').indeterminate=picked.length>0&&picked.length<rows.length;
  }
  async function run(action,ids){
    if(busy||!ids.length)return;busy=true;lastResult=null;render();
    try{
      const result=await api('/tasks/batch',{action,ids});
      for(const id of result.applied)selected.delete(id);
      lastResult=`已${{pause:'暂停',resume:'加入下载队列',cancel:'取消下载',remove:'移除任务记录'}[action]} ${result.applied.length} 项${result.skipped.length?'；'+result.skipped.length+' 项未处理：'+[...new Set(result.skipped.map(issue=>issue.error))].join('；'):''}。${action==='remove'?'所有磁盘文件、网盘原文件和已入库影视均保留。':''}`;
      toast(result.skipped.length?'批量操作已完成，部分任务未处理':'任务已更新');
    }catch(error){lastResult=error.message;toast(error.message,true);}
    finally{busy=false;await refresh();if($('#tasks-page'))render();}
  }
  async function action(name,button){
    if(name==='tasks-filter'){if(busy)return;filter=button.dataset.filter;selected.clear();lastResult=null;render();return;}
    if(name==='tasks-clear'){selected.clear();render();return;}
    if(name==='tasks-confirm-remove'){const ids=pending;pending=null;closeModal();await run('remove',ids||[]);return;}
    const ids=name==='tasks-control'?[button.dataset.taskId]:[...selected],command=button.dataset.command;
    if(command==='remove'){
      pending=ids;
      modal('移除下载任务记录？','所有视频和临时文件保留，网盘原文件保留。',`<p>将处理 ${ids.length} 个任务。未完成任务会先停止下载，再移除记录；已完成任务仅清理下载历史，影视仍在媒体库中。</p><p class="section-note">不会删除服务器上的视频、临时文件，也不会修改百度或夸克网盘中的原文件。正在入库的任务会跳过。</p>`,`<button class="button" data-action="close-modal">返回</button><button class="button danger" data-action="tasks-confirm-remove">停止下载并移除记录</button>`);return;
    }
    await run(command,ids);
  }
  function changed(input){
    if(input.id==='tasks-select-all'){for(const task of visible()){if(input.checked)selected.add(task.id);else selected.delete(task.id)}render();}
    else if(input.classList.contains('task-check')){if(input.checked)selected.add(input.dataset.taskId);else selected.delete(input.dataset.taskId);input.closest('.task-card').classList.toggle('selected',input.checked);renderSelection();}
  }
  function input(element){if(element.id==='tasks-query'){query=element.value;selected.clear();lastResult=null;render();}}
  function search(value){query=value;filter='all';selected.clear();lastResult=null;if($('#tasks-page'))$('#tasks-query').value=query;}
  return {render,action,changed,input,search};
}
