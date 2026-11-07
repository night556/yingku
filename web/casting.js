export function createCaster({api,toast,esc,icon,getState,getPlayerPosition,awaitProgress,navigateRemote,returnFromRemote,getReturnLabel}) {
  let current=null,selectedFile='',serial=0,commandSerial=0,busy=false,interacting=false,volume=50;
  let autoNext=true;try{autoNext=localStorage.getItem('yingku_cast_auto_next')!=='0';}catch{}
  let layer=null,panelReturn=null,suspended=[],savedOverflow='',savedAppInert=false;
  const dock=document.createElement('div');dock.id='cast-dock';dock.hidden=true;document.querySelector('.workspace').append(dock);
  function panel(title,subtitle,body,footer) {
    if(!layer){
      const host=document.querySelector('#modal-root'),app=document.querySelector('.app-shell');
      panelReturn=document.activeElement;savedOverflow=document.body.style.overflow;savedAppInert=app.inert;
      suspended=[...host.children].map(element=>({element,inert:element.inert}));
      suspended.forEach(({element})=>{element.inert=true;});
      app.inert=true;document.body.style.overflow='hidden';
      layer=document.createElement('div');layer.id='cast-layer';layer.className='overlay cast-overlay';host.append(layer);
    }
    layer.innerHTML=`<section class="modal" role="dialog" aria-modal="true" aria-labelledby="cast-dialog-title"><div class="modal-head"><div><h2 id="cast-dialog-title">${title}</h2><p>${subtitle}</p></div><button class="icon-btn" data-cast="close" aria-label="关闭投屏面板">${icon('close')}</button></div><div class="modal-body">${body}</div><div class="modal-foot">${footer}</div></section>`;
    const activeLayer=layer;
    requestAnimationFrame(()=>{if(layer===activeLayer)layer.querySelector('button')?.focus({preventScroll:true});});
  }
  function close({restoreFocus=true}={}) {
    if(!layer)return false;
    ++serial;layer.remove();layer=null;interacting=false;
    suspended.forEach(({element,inert})=>{if(element.isConnected)element.inert=inert;});suspended=[];
    document.body.style.overflow=savedOverflow;document.querySelector('.app-shell').inert=savedAppInert;
    if(restoreFocus&&panelReturn?.isConnected)panelReturn.focus({preventScroll:true});
    panelReturn=null;return true;
  }
  const labels={PLAYING:'正在播放',PAUSED_PLAYBACK:'已暂停',STOPPED:'已停止',TRANSITIONING:'正在连接',NO_MEDIA_PRESENT:'播放已结束'};
  const time=value=>{const n=Math.max(0,Math.floor(value||0));return Math.floor(n/60)+':'+String(n%60).padStart(2,'0');};
  const episodeName=file=>`${file.season===0?'特别篇':'第 '+file.season+' 季'} · 第 ${file.episode}${file.episodeEnd>file.episode?'–'+file.episodeEnd:''} 集`;
  function dockUpdate() {
    dock.hidden=location.hash==='#cast'||!current||['STOPPED','NO_MEDIA_PRESENT'].includes(current.status);
    dock.innerHTML=current?`<button class="button" data-cast="remote">${icon('cast')}${esc(current.deviceName)} · ${esc(labels[current.status]||'正在投屏')}<span>打开遥控</span></button>`:'';
  }
  function remoteHTML() {
    const s=current;if(!s)return '';
    const ended=['STOPPED','NO_MEDIA_PRESENT'].includes(s.status);
    return `<div id="cast-remote"><div class="cast-screen">${icon('cast')}<h3>${esc(s.deviceName)}</h3><p>${s.current?esc(episodeName(s.current))+' · ':''}${esc(s.name)}</p><span class="badge green">${esc(labels[s.status]||s.status)}</span></div>
      ${s.warning?'<div class="notice neutral">'+esc(s.warning)+'</div>':''}
      <div class="cast-time"><span>${time(s.position)}</span><span>${s.duration?time(s.duration):'总时长未知'}</span></div>
      <label class="field"><span>播放进度</span><input id="cast-seek" type="range" min="0" max="${Math.max(s.duration||0,s.position||0,1)}" step="1" value="${s.position||0}" ${ended?'disabled':''}></label>
      <div class="cast-controls">${s.series?`<button id="cast-previous" class="button" data-cast="previous" title="${s.previous?esc(episodeName(s.previous)+(s.previous.ready?'':' · 未入库')):'已经是第一集'}" ${ended||!s.previous?.ready?'disabled':''}>${icon('previous')}上一集</button>`:''}<button class="button primary" id="cast-play" data-cast="${s.status==='PLAYING'?'pause':'play'}" ${ended?'disabled':''}>${icon(s.status==='PLAYING'?'pause':'play')}${s.status==='PLAYING'?'暂停':'播放'}</button>${s.series?`<button id="cast-next" class="button" data-cast="next" title="${s.next?esc(episodeName(s.next)+(s.next.ready?'':' · 未入库')):'已经是最后一集'}" ${ended||!s.next?.ready?'disabled':''}>下一集${icon('next')}</button>`:''}</div>
      ${s.series?`<div class="cast-continuation"><label class="checkbox-field"><input type="checkbox" id="cast-auto-next" ${s.autoNext?'checked':''} ${ended?'disabled':''}>自动播放下一集</label><p class="section-note">${s.next?esc(episodeName(s.next))+(s.next.ready?'':' 尚未入库，连播将在本集结束后暂停。'):'已是本剧最后一个视频。'}</p><p class="section-note">手机关屏后仍可连播，映库服务和电视需保持运行。</p></div>`:''}
      <div class="cast-controls" style="margin-top:16px"><button class="button" data-cast="stop" ${ended?'disabled':''}>停止投屏</button><button class="button quiet" data-cast="devices">选择其他电视</button></div>
      <label class="field" style="margin-top:22px"><span>设置电视音量</span><input id="cast-volume" type="range" min="0" max="100" value="50" step="1" ${ended?'disabled':''}><small>仅适用于支持音量控制的设备。</small></label><div id="cast-error" role="status"></div></div>`;
  }
  function showRemote(){
    if(!current)return;
    close({restoreFocus:false});navigateRemote();
  }
  function renderPage(root) {
    root.innerHTML=`<section class="cast-page" id="cast-page"><header class="cast-page-heading"><div><div class="eyebrow">正在投屏</div><h1>电视遥控</h1><p>离开这个页面不会停止电视播放或自动连播。</p></div><button class="button" data-cast="return">${icon('back')}${esc(getReturnLabel())}</button></header><article class="cast-page-card">${current?remoteHTML():`<div class="cast-empty">${icon('cast')}<h2>${sessionStorage.getItem('yingku_cast_id')?'正在恢复投屏会话…':'暂无正在投屏的内容'}</h2><p>从作品详情或播放器选择电视，即可在这里控制播放。</p></div>`}</article></section>`;
    const slider=document.querySelector('#cast-volume');if(slider)slider.value=volume;
    dockUpdate();
  }
  function updateRemote(){
    const root=document.querySelector('#cast-remote');
    if(root&&current&&!interacting&&!busy){
      const focusID=root.contains(document.activeElement)?document.activeElement.id:'';
      root.outerHTML=remoteHTML();
      if(focusID)document.getElementById(focusID)?.focus({preventScroll:true});
      const slider=document.querySelector('#cast-volume');if(slider)slider.value=volume;
    }
    if((!root||!current)&&document.querySelector('#cast-page'))renderPage(document.querySelector('#main'));
    dockUpdate();
  }
  async function open(fileID,force=false){
    selectedFile=fileID||current?.fileId||'';
    if(!selectedFile)return;
    const mine=++serial;
    if(!force&&current?.fileId===selectedFile&&!['STOPPED','NO_MEDIA_PRESENT'].includes(current.status)){showRemote();return;}
    panel('投屏到电视','手机、运行映库的电脑与电视需在同一局域网。',
      '<div id="cast-devices"><p class="section-note">正在检查局域网设置…</p></div>',
      `<button class="button quiet" data-cast="close">${document.querySelector('#player-video')?'返回播放器':'返回'}</button>`);
    try {
      const network=await api('/network');
      if(mine!==serial||!document.querySelector('#cast-devices'))return;
      if(!network.active){
        document.querySelector('#cast-devices').innerHTML='<div class="notice neutral">服务当前仅监听本机，请用 0.0.0.0 的监听地址重启，再搜索电视。</div>';
        return;
      }
      await discover(mine);
    }catch(error){if(mine===serial&&layer)showError(error,'#cast-devices');}
  }
  async function discover(mine=serial){
    const root=document.querySelector('#cast-devices');if(!root)return;
    root.innerHTML='<div class="loading-screen"><span class="spinner"></span> 正在搜索电视…</div>';
    try {
      const result=await api('/dlna/discover',{});
      if(mine!==serial||!root.isConnected)return;
      root.innerHTML=result.devices.length?
        '<div class="cast-device-list">'+result.devices.map(device=>`<button class="cast-device" data-cast="start" data-device="${esc(device.id)}">${icon('cast')}<span><strong>${esc(device.name)}</strong><small>${esc([device.manufacturer,device.model].filter(Boolean).join(' · '))}</small></span>${icon('arrow')}</button>`).join('')+'</div>':
        '<div class="notice neutral">没有找到可接收视频的 DLNA 设备。请开启电视的媒体投屏功能，并确认它和电脑在同一个网络；只支持屏幕镜像的设备可能不会出现。</div>';
      root.insertAdjacentHTML('beforeend','<button class="button small" data-cast="rescan" style="margin-top:18px">重新搜索电视</button><p class="section-note">直接使用原视频，能否播放取决于电视的格式支持；外置字幕暂不随投屏发送。</p><div id="cast-error" role="status"></div>');
    }catch(error){if(root.isConnected)root.innerHTML='<div class="error-box">'+esc(error.message)+'</div><button class="button" data-cast="rescan">重新搜索电视</button>';}
  }
  function showError(error,selector='#cast-error'){
    const root=document.querySelector(selector);
    if(root)root.innerHTML='<div class="error-box">'+esc(error.message)+'</div>';
    toast(error.message,true);
  }
  document.addEventListener('click',async event=>{
    const button=event.target.closest('[data-cast]');if(!button)return;
    const action=button.dataset.cast;
    if(action==='close'){close();return;}
    if(action==='return'){returnFromRemote();return;}
    if(action==='remote'){await open(current?.fileId);return;}
    if(action==='devices'){await open(current?.fileId,true);return;}
    if(action==='rescan'){await discover();return;}
    if(busy)return;
    ++commandSerial;busy=true;button.disabled=true;
    try {
      if(action==='start'){
        const mine=serial,fileID=selectedFile;
        const file=getState().files.find(file=>file.id===fileID);
        const position=getPlayerPosition(fileID)??file?.position??0;
        await awaitProgress();
        if(mine!==serial||!layer)return;
        const previous=current;
        current=await api('/dlna/cast',{deviceId:button.dataset.device,fileId:fileID,position,autoNext});
        if(previous&&previous.deviceId!==current.deviceId&&!['STOPPED','NO_MEDIA_PRESENT'].includes(previous.status)){
          try{await api('/dlna/sessions/'+previous.id,{action:'stop'});}catch{toast('新电视已开始播放，请检查原电视是否需要手动停止');}
        }
        sessionStorage.setItem('yingku_cast_id',current.id);
        if(getPlayerPosition(fileID)!==undefined)document.querySelector('#player-video')?.pause();
        if(mine===serial&&layer)showRemote();
        dockUpdate();
      }else if(current){
        current=await api('/dlna/sessions/'+current.id,{action});
        updateRemote();
      }
    }catch(error){showError(error);}
    finally{busy=false;button.disabled=false;updateRemote();}
  });
  document.addEventListener('change',async event=>{
    if(!['cast-seek','cast-volume','cast-auto-next'].includes(event.target.id)||!current||busy)return;
    ++commandSerial;busy=true;interacting=false;
    if(event.target.id==='cast-volume')volume=Number(event.target.value);
    try {
      const automatic=event.target.id==='cast-auto-next';
      current=await api('/dlna/sessions/'+current.id,{action:automatic?'auto-next':event.target.id==='cast-seek'?'seek':'volume',value:automatic?(event.target.checked?1:0):Number(event.target.value)});
      if(automatic){autoNext=current.autoNext;try{localStorage.setItem('yingku_cast_auto_next',autoNext?'1':'0');}catch{}}
    }catch(error){showError(error);}
    finally{busy=false;updateRemote();}
  });
  document.addEventListener('pointerdown',event=>{if(['cast-seek','cast-volume'].includes(event.target.id))interacting=true;});
  document.addEventListener('pointerup',()=>{interacting=false;});
  async function poll(){
    const mine=commandSerial;
    const id=current?.id||sessionStorage.getItem('yingku_cast_id');
    try{
      if(id&&!busy){
        const result=await api('/dlna/sessions/'+encodeURIComponent(id));
        if(mine!==commandSerial||busy)return;
        current=result;
        if(['STOPPED','NO_MEDIA_PRESENT'].includes(current.status))sessionStorage.removeItem('yingku_cast_id');
        updateRemote();
      }
    }catch(error){
      if(mine===commandSerial&&!busy&&error.message.includes('投屏已结束')){current=null;sessionStorage.removeItem('yingku_cast_id');updateRemote();}
    }finally{setTimeout(poll,4000);}
  }
  poll();
  async function sync(){
    const id=current?.id||sessionStorage.getItem('yingku_cast_id');if(!id)return null;
    try{current=await api('/dlna/sessions/'+encodeURIComponent(id)+'/sync',{});updateRemote();return current;}
    catch(error){if(error.message.includes('投屏已结束'))return null;throw error;}
  }
  return {open,close,renderPage,sync,dialog:()=>layer?.querySelector('.modal')};
}
