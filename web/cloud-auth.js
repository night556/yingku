export function createSourceLogin({api,esc,icon,getEditing}) {
  let current=null,timer=null,generation=0,type='',sourceId='',starting=false;
  const $=selector=>document.querySelector(selector);
  const providerName=()=>type==='baidu'?'百度网盘':'夸克网盘';
  function stopTimer(){if(timer){clearTimeout(timer);timer=null;}}
  function pause(){generation++;starting=false;stopTimer();if(current?.id)api('/cloud-auth/'+current.id+'/cancel',{}).catch(()=>{});current=null;}
  function showError(message,preserve=false){const box=$('#cloud-auth-panel');if(!box)return;const html=`<div class="cloud-login-error" data-login-error role="alert">${esc(message)}</div>`;if(preserve){box.querySelector('[data-login-error]')?.remove();box.insertAdjacentHTML('beforeend',html);}else box.innerHTML=html;}
  function fields(provider,source={}) {
    pause();type=provider;sourceId=getEditing()?.id||'';
    const epoch=generation;
    if(type==='baidu')queueMicrotask(()=>loadOptions(epoch));
    const saved=!!sourceId;
    const intro=type==='baidu'?'打开百度官方授权页或扫码，确认后会自动获取授权，无需复制授权码。':'使用夸克手机 App 扫码确认，程序自动取得并加密保存登录态，无需打开开发者工具。';
    const basic=`<section class="cloud-login"><div class="cloud-login-heading">${icon('cloud')}<strong>${type==='baidu'?'登录并授权':'扫码登录'}${providerName()}</strong></div><p>${intro}</p>${saved?'<p class="cloud-login-saved">已有连接已保存。需要更换账号或重新登录时，点击下方按钮。</p>':''}<button type="button" class="button primary" data-action="cloud-login-start">${icon(''+(type==='baidu'?'arrow':'play'))}${saved?'重新登录':type==='baidu'?'登录并授权百度网盘':'获取夸克登录二维码'}</button><input type="hidden" name="authSession" value=""><div id="cloud-auth-panel" aria-live="polite"></div></section>`;
    const folder=`<details class="cloud-folder-options"><summary>限制可浏览的目录（可选）</summary><label class="field"><span>${type==='baidu'?'网盘根目录':'起始文件夹 ID'}</span><input name="root" value="${esc(source.root||(type==='baidu'?'/':'0'))}" placeholder="${type==='baidu'?'/':'0'}"><small>${type==='baidu'?'默认 / 表示整个账号允许访问的网盘。':'默认 0 表示整个网盘。仅连接特定目录时，可填写夸克网页地址中的文件夹 ID。'}连接后可分别打开 movie、tv 等目录扫描。</small></label></details>`;
    if(type==='quark')return basic+folder+`<details class="cloud-advanced"><summary>高级：手动使用 Cookie 连接</summary><p class="section-note">扫码接口暂不可用时，可以使用之前的手动方式。扫码登录底层使用网页登录态，过期后可重新扫码。</p><label class="field"><span>网页登录态 Cookie${saved?'（留空保留）':''}</span><input name="cookie" type="password" autocomplete="new-password" spellcheck="false" placeholder="粘贴完整 Cookie 值"><small>加密保存在本机，不会回显。扫码登录完成后无需填写。</small></label><details><summary>如何获取 Cookie</summary><ol class="section-note"><li>用 Chrome 或 Edge 登录 <a href="https://pan.quark.cn/" target="_blank" rel="noopener noreferrer">夸克网盘</a>。</li><li>按 F12，选择 Network / 网络，打开一个网盘文件夹。</li><li>在发往 drive.quark.cn 的目录请求 Headers 中复制 Cookie 值。</li></ol></details></details>`;
    return basic+`<div id="cloud-app-message" class="section-note">正在检查百度应用配置…</div><details class="cloud-advanced" id="baidu-app-options"><summary>部署者设置：百度应用（仅需一次）</summary><p class="section-note">这是映库应用的配置，不是网盘账号密码。已有来源或项目配置可复用时无需再次填写。新部署需要自己的、已开通网盘及设备授权权限的百度应用。</p><div class="fields"><label class="field"><span>App Key / Client ID</span><input name="clientId" autocomplete="off" value="${esc(source.clientId||'')}" placeholder="百度应用 App Key"></label><label class="field"><span>Secret Key</span><input name="clientSecret" type="password" autocomplete="new-password" placeholder="已配置时留空保留"></label></div><button type="button" class="button small" data-action="cloud-save-baidu-app">保存应用配置</button><a class="button small quiet" href="https://yun.baidu.com/union/home" target="_blank" rel="noopener noreferrer">百度网盘开放平台 ↗</a><div id="cloud-app-error"></div></details>`+folder+`<details class="cloud-advanced"><summary>高级：手动授权码或令牌连接</summary><p class="section-note">仅在设备授权不可用时使用。手动授权码模式需要应用允许 oob 回调。</p><label class="field"><span>授权码</span><div class="inline-input"><input name="authCode" autocomplete="off" placeholder="手动授权码"><button type="button" class="button small" data-action="baidu-authorize">打开授权页</button></div></label><div class="fields"><label class="field"><span>Access Token</span><input name="accessToken" type="password" autocomplete="new-password"></label><label class="field"><span>Refresh Token</span><input name="refreshToken" type="password" autocomplete="new-password"></label></div><p class="section-note">已有连接留空保留，自动刷新需要同一应用的 App Key 和 Secret Key。</p></details>`;
  }
  async function loadOptions(epoch=generation){
    try {
      const result=await api('/cloud-auth/options?sourceId='+encodeURIComponent(sourceId));
      if(epoch!==generation||type!=='baidu')return;
      const message=$('#cloud-app-message');if(message){message.textContent=result.message;message.classList.toggle('cloud-needs-setup',!result.baiduConfigured);}
      const details=$('#baidu-app-options');if(details&&!result.baiduConfigured)details.open=true;
      const login=$('.cloud-login > button[data-action="cloud-login-start"]');if(login){login.disabled=!result.baiduConfigured;login.textContent=result.baiduConfigured?(sourceId?'重新登录':'登录并授权百度网盘'):'配置百度应用后即可登录';}
      const input=$('#source-form [name=clientId]');if(input&&!input.value&&result.baiduClientId)input.value=result.baiduClientId;
    }catch(error){if(epoch===generation){const message=$('#cloud-app-message');if(message)message.textContent=error.message;}}
  }
  function draw(view){
    current=view;
    const root=$('#cloud-auth-panel');if(!root)return;
    const token=$('#source-form [name=authSession]');if(token)token.value=view.status==='ready'?view.id:'';
    if(view.status==='ready'){
      for(const name of ['cookie','authCode','accessToken','refreshToken']){const field=$('#source-form [name='+name+']');if(field)field.value='';}
      root.innerHTML=`<div class="cloud-login-ready" role="status">${icon('check')}<div><strong>${type==='baidu'?'百度授权已完成':'夸克登录已完成'}</strong><p>凭证已由服务端接收，点击「保存并连接」测试目录并完成连接。</p></div></div>`;
      return;
    }
    if(view.status==='pending'){
      root.innerHTML=`<div class="cloud-login-challenge">${view.qrUrl?`<figure><img src="${esc(view.qrUrl)}" alt="${providerName()}登录二维码"><figcaption>请用${type==='baidu'?'百度网盘':'夸克'}手机 App 扫码并确认</figcaption></figure>`:''}<div class="cloud-login-instructions"><p><span class="spinner"></span> 等待你完成${type==='baidu'?'授权':'扫码确认'}…</p>${view.userCode?`<p>在授权页提示时输入此设备码：</p><code class="cloud-device-code">${esc(view.userCode)}</code>`:''}${view.loginUrl?`<a class="button small" href="${esc(view.loginUrl)}" target="_blank" rel="noopener noreferrer">${type==='baidu'?'在此设备打开百度授权页':'在此设备打开夸克登录页'} ↗</a>`:''}<p class="section-note">二维码过期后可重新获取。请核对官方页面所显示的账号和授权内容。</p></div></div><div class="button-row"><button type="button" class="button small quiet" data-action="cloud-login-cancel">取消本次登录</button><button type="button" class="button small quiet" data-action="cloud-login-start">重新获取二维码</button></div>`;
      return;
    }
    showError(view.error||({expired:'登录二维码已过期，请重新获取。',denied:'登录已取消，可重新开始。',cancelled:'本次登录已取消。'})[view.status]||'登录尚未完成，请重试。');
    root.insertAdjacentHTML('beforeend','<button type="button" class="button small quiet" data-action="cloud-login-cancel">取消本次登录</button>');
  }
  async function poll(epoch){
    if(epoch!==generation||!current?.id||!$('#cloud-auth-panel'))return;
    try{
      const view=await api('/cloud-auth/'+current.id+'/poll',{});
      if(epoch!==generation)return;
      // Keep the QR image stable while waiting, rather than reloading it on every poll.
      if(view.status!=='pending'||current.status!=='pending')draw(view);else current=view;
      if(view.status==='pending')timer=setTimeout(()=>poll(epoch),Math.max(1,view.interval||3)*1000);
    }catch(error){if(epoch===generation)showError(error.message);}
  }
  async function action(name,el){
    if(!['cloud-login-start','cloud-login-cancel','cloud-save-baidu-app'].includes(name))return false;
    if(name==='cloud-login-cancel'){pause();const token=$('#source-form [name=authSession]');if(token)token.value='';showError('本次登录已取消，可重新开始。');return true;}
    if(name==='cloud-save-baidu-app'){
      pause();const token=$('#source-form [name=authSession]');if(token)token.value='';
      const panel=$('#cloud-auth-panel');if(panel)panel.innerHTML='';
      el.disabled=true;
      try{
        const form=$('#source-form');await api('/config/cloud-apps',{sourceId,clientId:form.elements.clientId.value.trim(),clientSecret:form.elements.clientSecret.value.trim()});form.elements.clientSecret.value='';const box=$('#cloud-app-error');if(box)box.innerHTML='';await loadOptions();
      }catch(error){const box=$('#cloud-app-error');if(box)box.innerHTML=`<div class="cloud-login-error">${esc(error.message)}</div>`;}finally{if(el.isConnected)el.disabled=false;}
      return true;
    }
    pause();starting=true;const epoch=generation;const token=$('#source-form [name=authSession]');if(token)token.value='';el.disabled=true;
    const root=$('#cloud-auth-panel');if(root)root.innerHTML='<p class="section-note"><span class="spinner"></span> 正在连接官方登录服务…</p>';
    try{
      const view=await api('/cloud-auth',{provider:type,sourceId});
      if(epoch!==generation||!$('#cloud-auth-panel')){api('/cloud-auth/'+view.id+'/cancel',{}).catch(()=>{});return true;}
      draw(view);timer=setTimeout(()=>poll(epoch),Math.max(1,view.interval||3)*1000);
    }catch(error){if(epoch===generation){showError(error.message);if(type==='baidu')await loadOptions(epoch);}}finally{if(epoch===generation)starting=false;if(el.isConnected)el.disabled=false;}
    return true;
  }
  function beforeSave(){
    const form=$('#source-form');if(!form||!['baidu','quark'].includes(form.elements.type.value))return true;
    if(starting||current&&current.status!=='ready'){showError('本次登录尚未完成，请先确认登录或取消本次登录。',true);return false;}
    if(getEditing()||form.elements.authSession?.value)return true;
    if(type==='quark'&&form.elements.cookie?.value.trim())return true;
    if(type==='baidu'&&['authCode','accessToken','refreshToken'].some(name=>form.elements[name]?.value.trim()))return true;
    showError('请先完成登录，再点击「保存并连接」。如需手动连接，可展开高级选项。');return false;
  }
  function manualChanged(el){if(!['cookie','authCode','accessToken','refreshToken'].includes(el.name)||!el.value||!current&&!starting)return;pause();const token=$('#source-form [name=authSession]');if(token)token.value='';showError('已切换为手动连接，将使用你填写的凭证。');}
  return {fields,action,beforeSave,pause,manualChanged};
}
