export async function mountNetworkPanel({root,api,esc,icon,toast}) {
  if(!root)return;
  root.className='settings-panel network-panel';
  root.innerHTML='<h2>手机访问与 DLNA 投屏</h2><p class="section-note">正在读取设置…</p>';
  try {
    const config=await api('/network');
    if(!root.isConnected)return;
    root.innerHTML=`<h2>${icon('disk')}手机访问与 DLNA 投屏</h2>
      <p class="section-note">手机、电脑和电视共用同一个服务端口，启动后即可在家中网络访问。访问密码可选。</p>
      <p class="section-note">服务监听：${esc(config.listenAddress)}</p>
      <form id="network-form">
      <label class="checkbox-label" style="margin-top:16px"><input type="checkbox" name="requirePassword" ${config.passwordRequired?'checked':''} ${config.local?'':'disabled'}>需要访问密码（可选）</label>
      <p class="section-note" id="network-access-note">${config.passwordRequired?'手机输入密码后才能使用映库，电视投屏无需输入此密码。':'免登录时，同一局域网内能访问这个地址的设备都可以浏览、播放和管理媒体库。'}</p>
      <div class="password-settings" ${config.passwordRequired?'':'hidden'}><label class="field"><span>手机访问密码</span><input type="password" name="password" minlength="10" maxlength="128" autocomplete="new-password" placeholder="${config.passwordConfigured?'已设置，留空保留':'至少 10 个字符'}" ${config.local&&config.passwordRequired?'':'disabled'}><small>仅保存密码校验值。更换密码后，其他设备需要重新登录。</small></label></div>
      <div id="network-error" role="status">${config.error?'<div class="error-box">'+esc(config.error)+'</div>':''}</div>
      ${config.local?'<button class="button" type="submit">保存访问设置</button>':'<p class="section-note">请在运行映库的电脑上修改这些设置。</p>'}
      </form>
      <div class="rule"></div><h3>访问与投屏地址（可选）</h3>
      <form id="address-form">
      <label class="field"><span>访问地址</span><input type="url" name="publicURL" value="${esc(config.publicURL||'')}" ${config.publicURLManaged?'readonly':''} placeholder="例如 https://media.example.com"><small>${config.publicURLManaged?'由部署配置固定；仅用于展示，不影响其他地址访问。':'仅用于页面展示，留空也可通过 IP 或反向代理域名访问。'}</small></label>
      <label class="field"><span>投屏回连地址</span><input type="url" name="castBaseURL" value="${esc(config.castBaseURL||'')}" placeholder="留空自动选择，例如 http://192.168.1.100:8787"><small>电视读取视频时使用的映库地址。一般留空；容器映射端口或多网卡导致电视无法连接时，填写电视可访问的局域网地址和端口。不要带子路径。</small></label>
      <div id="address-error" role="status"></div>
      <button class="button" type="submit">保存地址设置</button>
      </form>
      ${config.active?`<div class="network-addresses"><strong>手机打开以下地址之一</strong>${config.addresses.map(address=>`<a href="${esc(address)}" target="_blank" rel="noopener noreferrer">${esc(address)}</a>`).join('')}<p class="section-note">多个地址对应不同网络，以手机能打开的为准。若无法访问，请检查电脑防火墙是否允许映库在专用网络中通信。</p></div>`:'<p class="section-note">当前监听地址仅允许本机访问；如需手机访问，请用 0.0.0.0 的监听地址启动服务。</p>'}
      <p class="section-note">DLNA 要求电视开启媒体投屏接收。支持播放、暂停、停止、拖动进度和设备支持的音量控制。视频格式取决于电视，暂不转码，外置字幕暂不随投屏发送。此模式用于家中网络。</p>
      ${!config.local&&config.passwordRequired?'<button type="button" class="button small" id="network-logout">退出登录</button>':''}`;
    const passwordMode=root.querySelector('[name=requirePassword]');
    passwordMode.addEventListener('change',()=>{
      root.querySelector('.password-settings').hidden=!passwordMode.checked;
      root.querySelector('[name=password]').disabled=!config.local||!passwordMode.checked;
      root.querySelector('[name=password]').required=config.local&&passwordMode.checked&&!config.passwordConfigured;
      root.querySelector('#network-access-note').textContent=passwordMode.checked?'手机输入密码后才能使用映库，电视投屏无需输入此密码。':'免登录时，同一局域网内能访问这个地址的设备都可以浏览、播放和管理媒体库。';
    });
    root.querySelector('form').addEventListener('submit',async event=>{
      event.preventDefault();event.stopPropagation();
      const form=event.currentTarget,button=form.querySelector('button[type=submit]');
      const data=new FormData(form);button.disabled=true;
      root.querySelector('#network-error').textContent='';
      try {
        await api('/network',{requirePassword:data.has('requirePassword'),password:data.get('password')||''});
        toast('访问设置已保存');
        await mountNetworkPanel({root,api,esc,icon,toast});
      } catch(error) {
        const message=document.createElement('div');message.className='error-box';message.textContent=error.message;
        root.querySelector('#network-error').replaceChildren(message);
      } finally {button.disabled=false;}
    });
    root.querySelector('#network-logout')?.addEventListener('click',async()=>{
      await api('/session',undefined,'DELETE');location.href='/login';
    });
    root.querySelector('#address-form').addEventListener('submit',async event=>{
      event.preventDefault();event.stopPropagation();
      const form=event.currentTarget,button=form.querySelector('button[type=submit]'),errorBox=root.querySelector('#address-error');
      const data=new FormData(form);button.disabled=true;errorBox.textContent='';
      try {
        await api('/config/addresses',{publicURL:data.get('publicURL')||'',castBaseURL:data.get('castBaseURL')||''});
        toast('地址已保存；新的投屏会话将使用新设置');
        await mountNetworkPanel({root,api,esc,icon,toast});
      }catch(error){
        const message=document.createElement('div');message.className='error-box';message.textContent=error.message;errorBox.replaceChildren(message);
      }finally{button.disabled=false;}
    });
  }catch(error){if(root.isConnected)root.innerHTML='<h2>手机访问与 DLNA 投屏</h2><div class="error-box">'+esc(error.message)+'</div>';}
}
