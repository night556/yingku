import {createSourceLogin} from './cloud-auth.js';
import {createSourceMigration} from './source-migration.js';
import {createScanner,aiSettingsPanel} from './scan.js';
import {createCaster} from './casting.js';
import {mountNetworkPanel} from './network.js';
import {createCinema,readMediaRoute,itemLink,libraryLink,episodeNeighbors,fileUnavailable,castResumeTarget} from './cinema.js';
import {mountPlayerControls} from './player.js';
import {mountSkipSegments,skipEditorMarkup} from './player-skip.js';
import {createTaskPage} from './tasks.js';
const $ = (s, root = document) => root.querySelector(s);
const $$ = (s, root = document) => [...root.querySelectorAll(s)];
const esc = v => String(v ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const paths = {
  library: '<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>',
  cloud: '<path d="M7 18a5 5 0 1 1 1-9.9 6 6 0 0 1 11.5 2.4A3.8 3.8 0 0 1 18 18H7Z"/>',
  download: '<path d="M12 3v12m-5-5 5 5 5-5M4 16v4h16v-4"/>',
  settings: '<path d="m10 3-.7 2.2-2 .9L5 5.5 3 9l1.6 1.6v2.8L3 15l2 3.5 2.3-.6 2 .9L10 21h4l.7-2.2 2-.9 2.3.6 2-3.5-1.6-1.6v-2.8L21 9l-2-3.5-2.3.6-2-.9L14 3h-4Z"/><circle cx="12" cy="12" r="3"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  arrow: '<path d="M5 12h14m-5-5 5 5-5 5"/>',
  chevron: '<path d="m9 5 7 7-7 7"/>',
  back: '<path d="m14 5-7 7 7 7"/>',
  previous: '<path d="M5 5v14m13-14L8 12l10 7V5Z"/>',
  next: '<path d="M19 5v14M6 5l10 7-10 7V5Z"/>',
  volume: '<path d="m11 4-6 5H2v6h3l6 5V4Zm4 4a6 6 0 0 1 0 8m3-11a10 10 0 0 1 0 14"/>',
  muted: '<path d="m11 4-6 5H2v6h3l6 5V4Zm5 5 6 6m0-6-6 6"/>',
  fullscreen: '<path d="M9 3H3v6m12-6h6v6M3 15v6h6m12-6v6h-6"/>',
  exitFullscreen: '<path d="M3 9h6V3m6 0v6h6M9 21v-6H3m12 6v-6h6"/>',
  folder: '<path d="M3 7V5a1 1 0 0 1 1-1h5l3 3h8a1 1 0 0 1 1 1v11a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V7Z"/>',
  film: '<rect x="3" y="3" width="18" height="18" rx="2"/><path d="M7 3v18M17 3v18M3 8h4m10 0h4M3 16h4m10 0h4M7 12h10"/>',
  cast: '<path d="M3 8V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-7M3 12a9 9 0 0 1 9 9M3 16a5 5 0 0 1 5 5"/><circle cx="3" cy="21" r=".8" fill="currentColor" stroke="none"/>',
  search: '<circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 5 5"/>',
  close: '<path d="m6 6 12 12M6 18 18 6"/>',
  play: '<path d="m9 5 11 7-11 7V5Z"/>',
  pause: '<path d="M8 5v14M16 5v14"/>',
  check: '<path d="m5 12 4 4L19 6"/>',
  refresh: '<path d="M20 7v5h-5M4 17v-5h5M6 6a8 8 0 0 1 13 2M5 16a8 8 0 0 0 13 2"/>',
  clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
  info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v6M12 7v.1"/>',
  disk: '<rect x="3" y="5" width="18" height="14" rx="3"/><path d="M3 14h18M16 16.5h2"/>',
  edit: '<path d="m15 4 5 5M4 20l5-1L21 7l-5-5L4 14v6Z"/>',
  trash: '<path d="M3 6h18M9 6V3h6v3M5 6l1 15h12l1-15M10 10v7M14 10v7"/>',
  subtitle: '<rect x="2" y="5" width="20" height="14" rx="3"/><path d="M6 10h3M6 14h5M13 10h5M15 14h3"/>',
  star: '<path d="m12 3 2.8 5.7 6.2.9-4.5 4.4 1.1 6.2-5.6-3-5.6 3 1.1-6.2L3 9.6l6.2-.9L12 3Z"/>',
};
const icon = (name, cls = '') => `<svg class="${cls}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.65" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths[name] || paths.film}</svg>`;
let state = null;
let scanner = null;
let sourceLogin = null;
let caster = null;
let taskPage=null;
let diskWarningSignature='';
const initialRoute=readMediaRoute(location.hash);
let page=initialRoute.page;
let cinema=null,librarySignature='',libraryReturnURL='#library',libraryScroll=0,resetLibraryScroll=false;
try{const saved=sessionStorage.getItem('yingku_library_return');if(saved?.startsWith('#library'))libraryReturnURL=saved;libraryScroll=Math.max(0,Number(sessionStorage.getItem('yingku_library_scroll'))||0);}catch{}
function rememberLibrary(){try{sessionStorage.setItem('yingku_library_return',libraryReturnURL);sessionStorage.setItem('yingku_library_scroll',String(libraryScroll));}catch{}}
let libraryFilter=initialRoute.filter||'all',libraryQuery=initialRoute.query||'',librarySort=initialRoute.sort||'added';
let sourceID = '', sourcePath = '/', entries = [], selected = new Set(), sourceError = '', sourceBusy = false, browseSerial = 0;
let draft = null, preview = null, previewDirty = false, searchResults = [], sourceEditing = null, matchItemID = '';
let sourceMigration = null;
let modalReturn = null, detailSeason = null, detailItemID = '', playerFileID = '', progressAt = 0;
let libraryRemoval = null;
let episodeSwitchBusy=false;
let suspendedCastPlayer=null,castReturnURL='#library';
let castRestoreSerial=0;
try{const saved=sessionStorage.getItem('yingku_cast_return');if(saved?.startsWith('#')&&saved!=='#cast')castReturnURL=saved;}catch{}
let detailView = 'episodes';
let refreshBusy = false, playerAbort = null, progressPromise = Promise.resolve(), navSignature = '', detailSignature = '';
const kindLabel = t => ({ local: '本地目录', baidu: '百度网盘', quark: '夸克网盘', webdav: 'WebDAV' }[t] || t);
const activeStatus = s => ['queued', 'downloading', 'finalizing'].includes(s);
const taskLabels = { queued: '等待下载', downloading: '正在下载', paused: '已暂停', failed: '下载失败', finalizing: '正在入库', completed: '已入库', cancelled: '已取消' };
const metaLabels = { matched: '已匹配 · 待补全', manual: '待匹配资料', loading: '正在刮削', ready: '资料已就绪', failed: '刮削待重试' };
function bytes(n) { if (!n) return '0 B'; const u = ['B', 'KB', 'MB', 'GB', 'TB']; const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), 4); return `${(n / 1024 ** i).toFixed(i > 1 ? 2 : 0)} ${u[i]}`; }
function duration(s) { if (!Number.isFinite(s)) return '0:00'; s = Math.floor(s || 0); return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`; }
function badge(text, color = '') { return `<span class="badge ${color}">${esc(text)}</span>`; }
function taskBadge(s) { return badge(taskLabels[s] || s, s === 'completed' ? 'green' : s === 'failed' ? 'red' : s === 'downloading' || s === 'finalizing' ? 'orange' : ''); }
function metaBadge(s) { return badge(metaLabels[s] || s, s === 'ready' ? 'green' : s === 'failed' ? 'red' : s === 'loading' ? 'orange' : ''); }
function art(path, wide = false) { return `/api/art?path=${encodeURIComponent(path)}${wide ? '&wide=1' : ''}`; }
function toast(message, error = false) { const el = document.createElement('div'); el.className = `toast${error ? ' error' : ''}`; el.textContent = message; $('#toast-root').append(el); setTimeout(() => el.remove(), error ? 6500 : 3800); }
function notifyFileDeleteWarning(message){const warning=document.createElement('div');warning.className='toast error deletion-warning';warning.setAttribute('role','alert');warning.innerHTML=`<span>${esc(message)}</span><button class="button small" type="button">知道了</button>`;warning.querySelector('button').addEventListener('click',()=>warning.remove());$('#toast-root').append(warning);}
async function api(path, data, method = data === undefined ? 'GET' : 'POST', signal) {
  const options = { method, headers: {}, signal };
  if (method !== 'GET') options.headers['X-App-Token'] = state?.csrf || '';
  if (data !== undefined) { options.headers['Content-Type'] = 'application/json'; options.body = JSON.stringify(data); }
  let response;
  try { response = await fetch(`/api${path}`, options); } catch (error) { if(signal?.aborted)throw error;throw new Error('无法连接映库服务，请确认映库仍在运行'); }
  const result = await response.json().catch(() => ({}));
  if(response.status===401){location.assign('/login');throw new Error('请重新输入访问密码');}
  if (!response.ok) throw new Error(result.error || `请求失败（${response.status}）`);
  return result;
}
async function busy(button, fn) { if (button?.disabled) return; if (button) { button.disabled = true; button.classList.add('loading'); } try { return await fn(); } catch (e) { toast(e.message, true); } finally { if (button?.isConnected) { button.disabled = false; button.classList.remove('loading'); } } }
async function refresh(render = false) {
  if(refreshBusy)return;
  refreshBusy=true;
  try {
    state=await api('/state');
    const warnings=state.storage?.fileDeleteWarnings||[],signature=JSON.stringify(warnings);if(warnings.length&&signature!==diskWarningSignature)notifyFileDeleteWarning(warnings.join('；'));diskWarningSignature=signature;
    $('#connection-status').innerHTML='<span class="status-dot"></span>服务已连接';
    if(render)renderPage();
    else if(page==='tasks')renderTasks();
    else if(page==='library')renderLibraryContent();
    else if(page==='detail'&&!$('.modal')&&detailSignature!==detailKey(detailItemID)){
      const scroll=window.scrollY;
      renderDetailPage(detailItemID,detailSeason,detailView);
      window.scrollTo(0,scroll);
    }
    renderNav();
    updatePlayerNavigation();
  }catch(error){
    $('#connection-status').textContent='服务已断开';
    if(!state)$('#main').innerHTML='<div class="error-box">'+esc(error.message)+'</div>';
  }finally{refreshBusy=false;}
}
function renderNav() {
  const active=state.tasks.filter(task=>activeStatus(task.status)).length;
  $('.version').textContent='v'+state.version;
  const content=page==='library'||page==='detail';
  const selected=page==='detail'?(state.items.find(item=>item.id===detailItemID)?.type||'all'):libraryFilter;
  const signature=JSON.stringify([page,selected,libraryQuery,librarySort,active,state.items.length,state.sources]);
  if(signature===navSignature)return;
  navSignature=signature;
  $('#nav').innerHTML=[['sources','cloud','来源',state.sources.length],['tasks','download','下载任务',active],['settings','settings','设置',null]].map(([id,image,title,count])=>'<a class="nav-link '+(page===id?'active':'')+'" href="#'+id+'" aria-label="'+title+'" '+(page===id?'aria-current="page"':'')+'>'+icon(image)+'<span class="nav-title">'+title+'</span>'+(count?'<span class="nav-count" aria-hidden="true">'+count+'</span>':'')+'</a>').join('');
  $('#sidebar-sources').innerHTML=state.sources.length?state.sources.map(source=>'<button class="sidebar-source" data-action="choose-source" data-id="'+source.id+'" title="'+esc(source.name)+'">'+icon(source.type==='local'?'disk':'cloud')+'<span>'+esc(source.name)+'</span><i class="status-dot"></i></button>').join(''):'<div class="sidebar-source-empty">连接一个来源<br>开始整理你的收藏</div>';
  $('#top-label').innerHTML='<a class="cinema-brand" href="#library" aria-label="映库首页"><span>映</span><span>映库<small>YINGKU</small></span></a>';
  $('#cinema-nav').innerHTML=[['all','媒体库'],['movie','电影'],['tv','电视剧']].map(([id,title])=>'<a href="'+libraryLink(id,libraryQuery,librarySort)+'" class="'+(content&&selected===id?'active':'')+'" '+(content&&selected===id?'aria-current="page"':'')+'>'+title+'</a>').join('');
  $('#cinema-tools').innerHTML=[['sources','cloud','管理来源'],['tasks','download','下载任务'],['settings','settings','设置']].map(([id,image,title])=>'<a class="icon-btn" href="#'+id+'" title="'+title+'" aria-label="'+title+'" '+(page===id?'aria-current="page"':'')+'>'+icon(image)+'</a>').join('');
}
function heading(kicker, title, desc, action = '') { return `<div class="page-heading"><div><div class="eyebrow">${kicker}</div><h1>${title}</h1><p>${desc}</p></div>${action}</div>`; }
function empty(title, desc, action = '', image = 'film') { return `<div class="empty-library"><div class="empty-icon">${icon(image)}</div><h3>${title}</h3><p>${desc}</p>${action}</div>`; }
function renderPage() {
  if(!state)return;
  const route=readMediaRoute(location.hash);
  page=route.page;
  document.body.dataset.page=page;
  if(page==='detail')renderDetailPage(route.id,route.season,route.view);
  else if(page==='sources')renderSources();
  else if(page==='tasks')renderTasks();
  else if(page==='settings')renderSettings();
  else if(page==='cast')caster.renderPage($('#main'));
  else {
    libraryFilter=route.filter;libraryQuery=route.query;librarySort=route.sort;
    renderLibrary();
  }
  if(page!=='detail')document.title=({library:'媒体库',sources:'来源',tasks:'下载任务',settings:'设置',cast:'电视遥控'}[page]||'映库')+' · 映库';
  renderNav();
}
function syncLibraryURL(){
  const url=libraryLink(libraryFilter,libraryQuery,librarySort);
  history.replaceState(null,'',url);
  libraryReturnURL=url;
  rememberLibrary();
  renderNav();
}
function renderLibrary(){
  $('#main').innerHTML=cinema.libraryShell(state,libraryFilter,libraryQuery,librarySort);
  librarySignature='';
  renderLibraryContent();
}
function renderLibraryContent(){
  if(!$('#library-content'))return;
  const signature=JSON.stringify([state.items,state.files.map(file=>[file.id,file.itemId,file.status,file.position,file.duration,file.lastPlayed,file.availability?.status,file.destination]),libraryFilter,libraryQuery,librarySort]);
  if(signature===librarySignature)return;
  librarySignature=signature;
  const result=cinema.libraryContent(state,libraryFilter,libraryQuery,librarySort);
  $('#library-feature').innerHTML=result.feature;
  $('#continue-section').innerHTML=result.resume;
  $('#library-content').innerHTML=result.grid;
  $('#wall-heading').textContent=result.title;
  $('#wall-count').textContent=result.count+' 部作品';
}
function renderSources() {
  const src = state.sources.find(s => s.id === sourceID);
  if (!src && state.sources.length) { sourceID = state.sources[0].id; sourcePath = '/'; entries = []; selected.clear(); queueMicrotask(() => loadDirectory('/')); }
  $('#main').innerHTML = `${heading('CONNECT YOUR COLLECTION', '来源', '打开你的存储目录，选择想加入媒体库的作品。', `<button class="button primary" data-action="add-source">${icon('plus')}添加来源</button>`)}
    ${state.sources.length ? `<div class="source-grid">${state.sources.map(s => `<div class="source-card ${sourceID === s.id ? 'selected' : ''}" data-action="choose-source" data-id="${s.id}" role="button" tabindex="0"><span class="source-icon">${icon(s.type === 'local' ? 'disk' : 'cloud')}</span><div><strong>${esc(s.name)}</strong><small>${kindLabel(s.type)} · 已配置</small></div><button class="icon-btn" data-action="edit-source" data-id="${s.id}" title="编辑${esc(s.name)}" aria-label="编辑${esc(s.name)}">${icon('edit')}</button></div>`).join('')}</div><div id="source-browser"></div>` : `${empty('先连接一个来源', '可以连接百度网盘、夸克网盘、WebDAV，或添加本机上的影片目录。<br>映库会读取目录，下载时保留来源文件。', `<button class="button primary" data-action="add-source">${icon('plus')}添加来源</button>`, 'cloud')}<div class="notice neutral">${icon('info')}<span>首次使用也可以先添加本地测试目录，体验选片、确认集号、原地入库和播放的完整过程。</span></div>`}`;
  renderBrowser();
}
async function loadDirectory(path) {
  if (!sourceID) return;
  const serial = ++browseSerial, requestedSource = sourceID;
  sourceBusy = true; sourceError = ''; selected.clear(); renderBrowser();
  try { const result = await api(`/sources/${sourceID}/list?path=${encodeURIComponent(path)}`); if (serial !== browseSerial || requestedSource !== sourceID) return; sourcePath = result.path; entries = result.entries; }
  catch (e) { if (serial !== browseSerial) return; sourceError = e.message; }
  finally { if (serial === browseSerial) { sourceBusy = false; renderBrowser(); } }
}
function renderBrowser() {
  const root = $('#source-browser'); if (!root) return;
  const src = state.sources.find(s => s.id === sourceID); if (!src) return;
  const parts = sourcePath.split('/').filter(Boolean), videos = entries.filter(e => e.media && !e.dir);
  root.innerHTML = `<div class="panel"><div class="panel-head"><div class="pathbar">${icon('folder')}<button data-action="browse" data-path="/">${esc(src.name)}</button>${parts.map((p, i) => `<span>/</span><button data-action="browse" data-path="${esc('/' + parts.slice(0, i + 1).join('/'))}">${esc(p)}</button>`).join('')}</div><div class="button-row">${scanner?.hasPrevious(sourceID)?'<button class="button small quiet" data-action="scan-resume">上次扫描</button>':''}<button class="button small" data-action="scan-open" data-source="${esc(src.id)}" data-path="${esc(sourcePath)}" ${sourceBusy?'disabled':''}>${icon('search')}扫描整个目录</button><button class="icon-btn" data-action="refresh-source" aria-label="刷新目录" title="刷新目录">${icon('refresh')}</button><button class="button small primary" data-action="import-selected" ${!selected.size || sourceBusy ? 'disabled' : ''}>${icon('download')}导入所选${selected.size ? ` (${selected.size})` : ''}</button></div></div>
    ${sourceError ? `<div style="padding:4px 20px"><div class="error-box">${esc(sourceError)}</div><button class="button small" data-action="edit-source" data-id="${src.id}">检查来源设置</button></div>` : sourceBusy ? '<div class="loading-screen"><span class="spinner"></span> 正在读取目录…</div>' : !entries.length ? '<div class="no-results">这个目录是空的。</div>' : `<div class="table-scroll"><table class="file-table"><thead><tr><th class="check-col"><input type="checkbox" id="select-all" aria-label="选择本目录全部视频" ${videos.length && selected.size === videos.length ? 'checked' : ''} ${!videos.length ? 'disabled' : ''}></th><th>名称</th><th class="hide-mobile">大小</th><th class="hide-tablet">修改时间</th><th>类型</th></tr></thead><tbody>${entries.map(e => `<tr class="${selected.has(e.key) ? 'selected' : ''}"><td class="check-col">${e.media && !e.dir ? `<input type="checkbox" class="file-check" data-key="${esc(e.key)}" aria-label="选择${esc(e.name)}" ${selected.has(e.key) ? 'checked' : ''}>` : ''}</td><td><div class="filename">${icon(e.dir ? 'folder' : 'film')}${e.dir ? `<button class="folder-name" data-action="browse" data-path="${esc(e.key)}">${esc(e.name)}</button>` : `<span>${esc(e.name)}</span>`}</div></td><td class="hide-mobile">${e.dir ? '—' : bytes(e.size)}</td><td class="hide-tablet">${esc(e.modified ? new Date(e.modified).toLocaleDateString('zh-CN') : '—')}</td><td>${e.dir ? badge('文件夹') : e.media ? badge('视频', 'blue') : badge('其他')}</td></tr>`).join('')}</tbody></table></div>`}
    <div class="panel-foot"><span>${entries.filter(e => e.dir).length} 个文件夹 · ${videos.length} 个视频${selected.size ? ` · 已选 ${selected.size} 个` : ''}</span><span>扫描当前目录及子目录，或手动选择视频</span></div></div><p class="section-note">来源根目录：${esc(src.type === 'local' ? src.root : src.type === 'webdav' ? src.url : src.root)}。${src.type==='local'?'本地默认直接入库，使用原文件，不复制也不改名。':'网盘导入会下载到媒体目录，保留网盘中的原文件。'}</p>`;
}
function renderTasks() { taskPage.render(); }
function renderSettings() {
  const c=state.config,storage=state.storage||{};
  $('#main').innerHTML=`${heading('JUST THE WAY YOU LIKE IT','设置','默认即可使用。按需要调整保存位置和资料来源。')}
    <div class="settings-grid"><form id="settings-form"><section class="settings-panel"><h2>${icon('disk')}下载与复制整理</h2><label class="field"><span>默认保存位置</span><input name="mediaRoot" required value="${esc(c.mediaRoot)}" ${storage.mediaDirManaged?'readonly':''} placeholder="例如 /media 或 D:\\Media"><small>${storage.mediaDirManaged?'由部署配置固定。需要更改时，在 Dokploy 中调整 YINGKU_MEDIA_DIR 和对应挂载后重新部署。':'仅用于网盘下载或你主动选择的本地复制。本地“直接入库”使用原文件，无需另选保存位置。'}</small></label>${storage.dataDir?`<label class="field"><span>配置与资料目录</span><input readonly aria-label="配置与资料目录" value="${esc(storage.dataDir)}"><small>保存来源授权、作品资料、进度和缓存。容器部署时应挂载持久存储；备份时保留整个目录。</small></label>`:''}<div class="fields"><label class="field"><span>同时下载数量</span><select name="concurrency">${[1,2,3,4].map(n=>`<option value="${n}" ${n===c.concurrency?'selected':''}>${n} 个文件</option>`).join('')}</select></label></div><label class="checkbox-label"><input type="checkbox" name="exportNfo" ${c.exportNfo?'checked':''}>为下载或复制后的影片生成 NFO；直接入库不会写入原目录</label></section>
    <section class="settings-panel"><h2>${icon('film')}影片资料 ${badge(c.tmdbConfigured?'已启用 TMDb':'公开资料 · 无需 Key','green')}</h2><label class="checkbox-label"><input type="checkbox" name="autoMetadata" ${c.autoMetadata?'checked':''}>导入后自动补充影片资料</label><p class="section-note">优先读取本地已有的 NFO、海报与分集资料。在线默认使用维基百科、Wikidata 和 TVmaze；公开资料的覆盖范围、语言与评分信息可能不同于 TMDb。</p><button type="button" class="button small" data-action="test-tmdb">${icon('refresh')}测试资料连接</button>
    <div class="rule"></div><details><summary class="field-label" style="cursor:pointer">高级：使用 TMDb（可选）</summary><p class="section-note">需要 TMDb 的电影、评分和分集资料时，可以在这里配置你自己的凭据。未填写时仍可检索公开资料。</p><label class="field"><span>API Key 或 API Read Access Token</span><input type="password" name="tmdbToken" autocomplete="new-password" placeholder="${c.tmdbConfigured?'已保存，留空保留':'可选，日常使用不必填写'}"><small>凭据加密保存在本机。保存后再测试连接。</small></label><a class="button small quiet" href="https://www.themoviedb.org/settings/api" target="_blank" rel="noopener noreferrer">申请 TMDb 凭据 ${icon('arrow')}</a><label class="checkbox-label" style="margin-top:16px"><input type="checkbox" name="clearTmdb">清除 TMDb 凭据，改用公开资料</label></details></section>${aiSettingsPanel(c.ai||{},esc,icon)}<div id="settings-error"></div><button class="button primary" type="submit">${icon('check')}保存设置</button></form>
    <aside class="settings-aside"><div class="aside-block"><h3>本地影片直接入库</h3><p>视频保留原位置，确认名称与集数后即可播放。不复制、不改名、不移动；现有海报和 NFO 可以直接读取。</p></div><div class="aside-block"><h3>影片资料来源</h3><p>电影简介与图片：<a href="https://www.wikipedia.org" target="_blank" rel="noopener noreferrer">维基百科</a><br>结构化资料：<a href="https://www.wikidata.org" target="_blank" rel="noopener noreferrer">Wikidata</a><br>剧集与分集：<a href="https://www.tvmaze.com" target="_blank" rel="noopener noreferrer">TVmaze</a><br>作品详情保留原始资料链接。TVmaze 内容采用 CC BY-SA；维基条目及图片许可见原页面。</p></div><div class="aside-block"><h3>可选的 TMDb</h3><p>本产品使用 TMDb API 时，未经 TMDb 认可或认证。<a href="https://www.themoviedb.org" target="_blank" rel="noopener noreferrer">The Movie Database ↗</a></p></div><div class="aside-block"><h3>播放与备份</h3><p>浏览器直接播放，推荐 MP4 / H.264 / AAC。本版本尚未提供实时转码。备份完整 data 目录；直接入库的原视频需保留原路径。</p></div></aside></div>`;
  const form=$('#settings-form'),wrap=document.createElement('div'),root=document.createElement('section');
  form.before(wrap);wrap.append(form,root);
  mountNetworkPanel({root,api,esc,icon,toast});
}
function modal(title, subtitle, body, footer = '', wide = false) {
  caster?.close({restoreFocus:false});
  stopPlayer(); modalReturn = document.activeElement; $('#modal-root').innerHTML = `<div class="overlay"><section class="modal ${wide ? 'wide' : ''}" role="dialog" aria-modal="true" aria-labelledby="dialog-title"><div class="modal-head"><div><h2 id="dialog-title">${title}</h2>${subtitle ? `<p>${subtitle}</p>` : ''}</div><button class="icon-btn" data-action="close-modal" aria-label="关闭">${icon('close')}</button></div><div class="modal-body">${body}</div>${footer ? `<div class="modal-foot">${footer}</div>` : ''}</section></div>`; document.body.style.overflow = 'hidden'; $('.app-shell').inert = true; requestAnimationFrame(() => ($('.cinema-player .player-stage') || $('.modal input:not([type=hidden])') || $('.modal select') || $('.modal button'))?.focus());
}
function closeModal() { caster?.close({restoreFocus:false});sourceLogin?.pause();scanner?.pause();stopPlayer(); if(document.fullscreenElement===$('#modal-root'))document.exitFullscreen().catch(()=>{});else if(document.webkitFullscreenElement===$('#modal-root'))document.webkitExitFullscreen?.(); $('#modal-root').innerHTML = ''; document.body.style.overflow = ''; $('.app-shell').inert = false; if (page==='sources'&&state)renderBrowser();if (modalReturn?.isConnected) modalReturn.focus(); }
function sourceModal(id = '') {
  sourceEditing = state.sources.find(s => s.id === id) || null;
  const s = sourceEditing || { type: 'local', name: '', root: '', url: '', username: '', clientId: '' };
  modal(sourceEditing ? '编辑来源' : '连接一个来源', '连接后即可浏览目录，来源中的文件会保留。', `<form id="source-form"><label class="field"><span>来源类型</span><select name="type" id="source-type" ${sourceEditing ? 'disabled' : ''}>${[['baidu', '百度网盘'], ['quark', '夸克网盘'], ['webdav', 'WebDAV'], ['local', '本地目录']].map(([v, n]) => `<option value="${v}" ${s.type === v ? 'selected' : ''}>${n}</option>`).join('')}</select></label><label class="field"><span>来源名称</span><input name="name" required maxlength="80" value="${esc(s.name)}" placeholder="例如：我的百度网盘"></label><div id="source-fields"></div><div id="source-form-error"></div></form>`, `<small>保存前会测试目录是否可读取</small><div class="button-row">${sourceEditing ? `<button class="button small danger" data-action="remove-source" data-id="${id}">移除</button>` : ''}<button class="button primary" type="submit" form="source-form">${icon('check')}保存并连接</button></div>`);
  renderSourceFields(s.type, s);
  sourceMigration = sourceEditing?.type === 'local' ? createSourceMigration({
    form: $('#source-form'), button: $('button[form="source-form"]'), source: s, api, esc,
  }) : null;
}
function renderSourceFields(type, s = {}) {
  sourceLogin?.pause();
  const root = $('#source-fields'); if (!root) return;
  if (type === 'local') root.innerHTML = `<label class="field"><span>服务端目录的完整路径</span><input name="root" required value="${esc(s.type === 'local' ? s.root : '')}" placeholder="例如 /media 或 D:\\Videos"><small>填写运行映库的服务器上的路径。容器部署时填写挂载后的路径，例如 /media、/media/movie 或 /media/tv。</small></label>`;
  else if (type === 'baidu' || type === 'quark') root.innerHTML = sourceLogin.fields(type,s);
  else if (type === 'webdav') root.innerHTML = `<label class="field"><span>WebDAV 根地址</span><input name="url" type="url" required value="${esc(s.url || '')}" placeholder="https://example.com/dav/"></label><div class="fields"><label class="field"><span>用户名（可选）</span><input name="username" autocomplete="off" value="${esc(s.username || '')}"></label><label class="field"><span>密码${sourceEditing ? '（留空保留）' : ''}</span><input name="password" type="password" autocomplete="new-password"></label></div><p class="source-kind-hint">适用于已经提供 WebDAV 的存储服务。百度网盘自身需要使用“百度网盘”接入方式。</p>`;
}

function guess(name) {
  let title = name.replace(/\.(mp4|m4v|mkv|webm|mov|avi|ts)$/i, ''); const year = title.match(/(?:19|20)\d{2}/)?.[0] || '';
  title = title.split(/S\d{1,3}E\d{1,4}|(?:^|[ ._-])(?:Season[ ._-]*\d+|S\d{1,3})(?:[ ._-]|$)|\d{1,2}x\d{1,3}|第[\d零〇一二两三四五六七八九十百]+[季集]|(?:19|20)\d{2}|1080p|2160p|720p/i)[0].replace(/[._()[\]]/g, ' ').trim().replace(/[- ]+$/, '');
  return { title, year };
}
function guessSeason(name) {
  const m=name.match(/S(\d{1,3})[ ._-]*E\d|(?:^|[ ._-])(?:Season[ ._-]*|S)(\d{1,3})(?:[ ._-]|$)|(\d{1,2})x\d/i);
  if(m)return Number(m[1]??m[2]??m[3]);
  const chinese=name.match(/第\s*([\d零〇一二两三四五六七八九十百]+)\s*季/);if(!chinese)return null;
  if(/^\d+$/.test(chinese[1]))return Number(chinese[1]);
  const digits={'零':0,'〇':0,'一':1,'二':2,'两':2,'三':3,'四':4,'五':5,'六':6,'七':7,'八':8,'九':9};let sum=0,last=0;
  for(const c of chinese[1]){if(c==='十'||c==='百'){sum+=(last||1)*(c==='十'?10:100);last=0;}else last=digits[c]??0;}
  return sum+last;
}
function openImport() {
  const files=entries.filter(e=>selected.has(e.key));if(!files.length)return toast('请先选择视频文件');
  const src=state.sources.find(s=>s.id===sourceID);
  let name=sourcePath==='/'?files[0].name:sourcePath.split('/').at(-1);
  const folder=sourcePath.split('/').at(-1);
  if(/^(?:Season[ ._-]*\d+|S\d{1,3}|第[\d零〇一二两三四五六七八九十百]+季)$/i.test(name))name=sourcePath.split('/').at(-2)||files[0].name;
  const g=guess(name),tv=files.length>1||guessSeason(files[0].name)!==null||guessSeason(folder)!==null||/第.+[集话話]|(?:^|[ ._-])EP?\d+/i.test(files[0].name);
  const season=guessSeason(files[0].name)??guessSeason(folder)??1;
  draft={itemId:'',newItem:false,sourceId:sourceID,sourceType:src.type,mode:src.type==='local'?'reference':'download',type:tv?'tv':'movie',title:g.title,year:g.year,tmdbId:0,metadataProvider:'',metadataId:'',season,outputRoot:state.config.mediaRoot,files:files.map(e=>({key:e.key,episode:0})),matched:null};
  preview=null;previewDirty=false;searchResults=[];renderImportConfig();
}
function steps(current) {
  const ending=draft?.mode==='reference'?'直接入库':draft?.mode==='copy'?'复制入库':'下载入库';
  return `<div class="steps"><div class="step ${current===1?'active':''}"><span>1</span>确认作品</div><i></i><div class="step ${current===2?'active':''}"><span>2</span>预览与分集</div><i></i><div class="step"><span>3</span>${ending}</div></div>`;
}
function renderImportConfig() {
  const d=draft,isLocal=d.sourceType==='local',reference=d.mode==='reference';
  modal('导入作品',reference?'直接使用原目录的视频，确认作品和集数即可加入媒体库。':'选定作品与保存位置，下一步确认文件和集数。',`${steps(1)}
    <form id="import-config"><div class="segmented">${[['movie','电影'],['tv','电视剧']].map(([v,n])=>`<button type="button" class="${d.type===v?'active':''}" data-action="import-type" data-id="${v}">${n}</button>`).join('')}</div>
    ${d.type==='tv'?`<label class="field"><span>所属剧集</span><select name="itemId" id="import-series"><option value="" ${!d.itemId&&!d.newItem?'selected':''}>自动识别（推荐）</option>${state.items.filter(it=>it.type==='tv').map(it=>`<option value="${it.id}" ${d.itemId===it.id?'selected':''}>${esc(it.title)}${it.year?' ('+esc(it.year)+')':''}</option>`).join('')}<option value="new" ${d.newItem?'selected':''}>新建独立剧集</option></select><small>新季会归入同一部剧；同名作品可手动选择。年份请填写整部剧的首播年份。</small></label>`:''}
    ${d.matched?`<div class="selected-match">${icon('check')}<div><strong>${esc(d.matched.title)} <span class="match-year">${esc(d.matched.year)}</span></strong><small>已匹配 · ${esc(d.matched.providerName||'TMDb')}</small></div><button type="button" class="icon-btn" data-action="clear-match" aria-label="清除作品匹配" style="margin-left:auto">${icon('close')}</button></div>`:''}
    <div class="fields"><label class="field full"><span>作品名称</span><div class="inline-input"><input name="title" value="${esc(d.title)}" required maxlength="150" placeholder="输入片名或剧名"><button type="button" class="button" data-action="import-search">${icon('search')}搜索资料</button></div><small>${state.config.tmdbConfigured?'将使用已配置的 TMDb 搜索影片资料。':'可直接搜索公开资料，无需填写 Key。入库时也会优先读取已有 NFO 和海报。'}</small></label><div id="import-matches" class="full"></div>
    <label class="field"><span>年份（可选）</span><input name="year" inputmode="numeric" maxlength="4" value="${esc(d.year)}" placeholder="留空，匹配后自动补齐"><small>${d.type==='tv'?'这里是整部剧的首播年份；各季年份会单独获取。':'可留空；匹配作品后会自动获取上映年份。'}</small></label>
    ${d.type==='tv'?`<label class="field"><span>季数</span><input name="season" type="number" min="0" max="999" required value="${d.season}"><small>0 表示特别篇；下一步可单独调整每个文件。</small></label>`:''}
    ${isLocal?`<label class="field full"><span>本地视频如何入库</span><select name="mode" id="local-import-mode"><option value="reference" ${reference?'selected':''}>直接入库 · 保留原位置，不复制（推荐）</option><option value="copy" ${!reference?'selected':''}>复制整理 · 另外保存一份</option></select><small>${reference?'直接播放原文件，不改名、不移动，也不会在原目录写入刮削文件。':'只在你选择复制整理时使用下方的保存目录，原文件保留。'}</small></label>`:''}
    ${!reference?`<label class="field full"><span>${isLocal?'复制到':'下载到'}</span><input name="outputRoot" required value="${esc(d.outputRoot)}"><small>服务器上的完整路径，已有文件不会覆盖。</small></label>`:''}</div>
    <div class="notice neutral">${icon('info')}<span>已选 ${d.files.length} 个视频。${reference?'确认后立即加入媒体库，无需等待下载。':d.type==='tv'?'建议每次导入同一部剧的一季，集号可在下一步确认。':'电影每次选择一个视频文件。'}</span></div><div id="import-error"></div></form>`,
    `<small>${reference?'视频保留在原位置':'确认后才会开始传输'}</small><button class="button primary" type="submit" form="import-config">预览文件与分集 ${icon('arrow')}</button>`,true);
}
function readImportConfig() {
  const f=$('#import-config');if(!f)return;const form=new FormData(f),oldSeason=draft.season;
  Object.assign(draft,{title:String(form.get('title')||'').trim(),year:String(form.get('year')||'').trim(),outputRoot:form.has('outputRoot')?String(form.get('outputRoot')).trim():draft.outputRoot,mode:draft.sourceType==='local'?String(form.get('mode')||draft.mode):'download',season:draft.type==='tv'?Number(form.get('season')):0});
  const series=String(form.get('itemId')||'');draft.itemId=series==='new'?'':series;draft.newItem=series==='new';
  if(draft.matched&&(draft.title!==draft.matched.title||draft.year&&draft.matched.year&&draft.year!==draft.matched.year)){draft.matched=null;draft.tmdbId=0;draft.metadataProvider='';draft.metadataId='';}
  if(oldSeason!==draft.season)draft.files.forEach(f=>f.season=draft.season);
}
function importBody() { const { matched, sourceType, ...body } = draft; return body; }
async function getPreview() {
  if ($('#import-config')) readImportConfig();
  if ($('#preview-table')) readPreviewInputs();
  const result = await api('/import/preview', importBody()); preview = result; previewDirty = false;
  draft.files = result.rows.map(row => ({ key: row.entry.key, season: row.season, episode: row.episode, episodeEnd: row.episodeEnd||0 })); renderPreview();
}
function readPreviewInputs() { $$('#preview-table tbody tr').forEach((tr, i) => { if (draft.files[i]) { draft.files[i].season = Number($('[name=season]', tr)?.value ?? 0); draft.files[i].episode = Number($('[name=episode]', tr)?.value ?? 0); draft.files[i].episodeEnd=Number($('[name=episodeEnd]',tr)?.value||0); } }); }
function renderPreview() {
  const problems=preview.rows.filter(r=>r.issue&&!r.duplicate).length,reference=preview.mode==='reference',copy=preview.mode==='copy';
  modal('确认文件与分集',`${esc(preview.itemTitle||draft.title)}${preview.itemYear?' · '+esc(preview.itemYear):''} · ${draft.type==='tv'?'电视剧':'电影'}`,`${steps(2)}
    ${draft.type==='tv'&&preview.itemId?`<div class="notice neutral">${icon('check')}<span>归入已有剧集：<strong>${esc(preview.itemTitle)}</strong>${preview.itemYear?' ('+esc(preview.itemYear)+')':''}。在同一部作品内按季切换。</span></div>`:''}
    <div class="preview-summary"><span>${reference?'直接入库':copy?'准备复制':'准备下载'} <strong>${preview.count}</strong> 个文件</span><span>${reference?'原文件共':'共'} <strong>${bytes(preview.total)}</strong></span><span>跳过重复 <strong>${preview.rows.filter(r=>r.duplicate).length}</strong> 个</span></div>
    ${draft.type==='tv'?`<div class="button-row" style="margin-bottom:17px"><button class="button small" data-action="number-episodes">按当前顺序从第 1 集编号</button><span class="section-note">也可以直接修改下方季数与集号</span></div>`:''}
    <div class="panel table-scroll"><table class="file-table preview-table" id="preview-table"><thead><tr><th>${reference?'源文件与原位置':'源文件与保存位置'}</th>${draft.type==='tv'?'<th>季</th><th>起始集</th><th>结束集</th>':''}<th>状态</th></tr></thead><tbody>${preview.rows.map(row=>`<tr><td><div class="filename">${icon('film')}<span>${esc(row.entry.name)}</span></div><div class="target-path">${esc(row.destination)}</div>${row.issue?`<div class="row-issue">${esc(row.issue)}</div>`:''}</td>${draft.type==='tv'?`<td><input name="season" type="number" min="0" max="999" value="${row.season}" aria-label="${esc(row.entry.name)}的季数" ${row.duplicate?'disabled':''}></td><td><input name="episode" type="number" min="1" max="9999" value="${row.episode||''}" aria-label="${esc(row.entry.name)}的集号" ${row.duplicate?'disabled':''}></td><td><input name="episodeEnd" type="number" min="0" max="9999" value="${row.episodeEnd||''}" placeholder="单集留空" aria-label="${esc(row.entry.name)}的结束集" ${row.duplicate?'disabled':''}></td>`:''}<td>${row.duplicate?badge('跳过'):row.issue?badge('需确认','orange'):badge(reference?'原地入库':'准备就绪','green')}</td></tr>`).join('')}</tbody></table></div>
    ${problems?`<div class="notice">${icon('info')}<span>${problems} 个文件需要确认。修改集号后点击“更新预览”再继续。</span></div>`:`<p class="section-note">${reference?'原视频保持原位，确认后即可播放。':'完成传输的文件即可播放。'}${state.config.autoMetadata?'会优先读取已有资料，再在后台自动匹配在线资料；匹配不确定时可手动选择。':'已关闭自动补充资料，可在详情页单独搜索匹配。'}</p>`}<div id="import-error"></div>`,
    `<button class="button quiet" data-action="import-back">${icon('back')}返回配置</button><button class="button primary" id="import-confirm" data-action="import-confirm" ${!preview.count||problems?'disabled':''}>${icon(reference?'plus':'download')}${reference?'加入媒体库':copy?'复制并入库':'开始下载'}</button>`,true);
}
function dirtyPreview() { previewDirty = true; const b = $('#import-confirm'); if (b) { b.disabled = false; b.innerHTML = `${icon('refresh')}更新预览`; } }
async function searchImport(button) {
  readImportConfig(); if (!draft.title) return toast('先输入作品名称');
  const root = $('#import-matches'); root.innerHTML = '<p class="section-note"><span class="spinner"></span> 正在搜索影片资料…</p>';
  try { const result = await api(`/metadata/search?type=${draft.type}&query=${encodeURIComponent(draft.title)}`); searchResults = result.results; root.innerHTML = matchList('choose-import-match'); }
  catch (e) { root.innerHTML = `<div class="error-box">${esc(e.message)}</div>`; }
}
function matchList(action) {
  return searchResults.length?`<div class="matches">${searchResults.map((m,i)=>`<button type="button" class="match" data-action="${action}" data-index="${i}">${m.poster?`<img src="${art(m.poster)}" alt="" loading="lazy">`:'<span class="match-art"></span>'}<div class="match-text"><strong>${esc(m.title)}</strong><span class="match-year">${esc(m.year)} · ${esc(m.providerName||'TMDb')}</span><p>${esc(m.overview||'暂无简介')}</p></div>${icon('chevron')}</button>`).join('')}</div>`:'<div class="no-results">没有找到结果，试试作品的原名或其他译名。</div>';
}
function canonicalItemID(id) { for(let i=0;i<=Object.keys(state.itemAliases||{}).length;i++){const next=state.itemAliases?.[id];if(!next)break;id=next;}return id; }
function detailKey(id) { id=canonicalItemID(id);return JSON.stringify([state.items.find(it => it.id === id), state.files.filter(f => f.itemId === id).map(f => [f.id, f.status, f.subtitles.length, f.position, f.duration,f.videoWidth,f.videoHeight,f.lastPlayed,f.destination,f.name,f.availability?.status])]); }
function infoDate(value) { return value ? String(value).slice(0,10) : ''; }
function sourceStatus(value) { return ({Running:'播出中','Returning Series':'播出中',Ended:'已完结',Canceled:'已取消','In Development':'筹备中','To Be Determined':'待定',Released:'已上映','Post Production':'后期制作',Planned:'计划中'})[value] || value || ''; }
function localizedValue(value) { return ({English:'英语',Chinese:'中文',Mandarin:'普通话',Japanese:'日语',Korean:'韩语',French:'法语',Spanish:'西班牙语','United States':'美国','United States of America':'美国','United Kingdom':'英国',China:'中国',Japan:'日本','South Korea':'韩国',France:'法国',Canada:'加拿大',Germany:'德国',Australia:'澳大利亚'})[value] || value; }
function personType(value) { return ({Actor:'演员',Director:'导演',Writer:'编剧',Creator:'创作者',Producer:'制片人',Crew:'主创'})[value] || value || '演职人员'; }
function personRole(person) { if(!person.role)return personType(person.type);if(person.type==='Actor')return '饰 '+person.role;return person.role.split(' / ').map(role=>({Creator:'创作者',Director:'导演',Writer:'编剧',Screenplay:'编剧','Executive Producer':'执行制片人','Co-Executive Producer':'联合执行制片人','Supervising Producer':'监制',Producer:'制片人','Co-Producer':'联合制片人',Story:'故事创作'})[role]||role).join(' / '); }
function metadataLinks(it) {
  const d=it.details||{},ids=d.externalIds||{},out=[];
  for(const [key,id] of Object.entries(ids)) {
    let href='';if(key==='imdb'&&/^tt\d+$/.test(id))href='https://www.imdb.com/title/'+id+'/';
    else if(key==='tmdb'&&/^\d+$/.test(id))href='https://www.themoviedb.org/'+it.type+'/'+id;
    else if(key==='tvmaze'&&/^\d+$/.test(id))href='https://www.tvmaze.com/shows/'+id;
    else if(key==='wikidata'&&/^Q\d+$/.test(id))href='https://www.wikidata.org/wiki/'+id;
    const label=({imdb:'IMDb',tmdb:'TMDb',tvmaze:'TVmaze',tvdb:'TVDB',wikidata:'Wikidata'})[key]||key;
    out.push(href?`<a class="external-link" href="${esc(href)}" target="_blank" rel="noopener noreferrer">${esc(label)} ↗</a>`:`<span class="external-link">${esc(label)} · ${esc(id)}</span>`);
  }
  if(/^https?:\/\//i.test(d.homepage||''))out.push(`<a class="external-link" href="${esc(d.homepage)}" target="_blank" rel="noopener noreferrer">官方网站 ↗</a>`);
  return out.join('');
}
function detailInfo(it,files) {
  const d=it.details||{},localSeasons=[...new Set(files.map(f=>f.season))].length;
  const rows=[['原名',it.originalTitle], [it.type==='tv'?'整剧首播':'上映日期',infoDate(d.premiereDate)],['结束日期',infoDate(d.endDate)],['播出状态',sourceStatus(d.status)],['内容分级',d.contentRating],[it.type==='tv'?'每集参考时长':'片长',d.runtime?d.runtime+' 分钟':''],['地区',(d.countries||[]).map(localizedValue).join(' / ')],['语言',(d.languages||[]).map(localizedValue).join(' / ')],['导演',(d.directors||[]).join(' / ')],['编剧',(d.writers||[]).join(' / ')],['创作者',(d.creators||[]).join(' / ')],['制作 / 播出',(d.studios||[]).join(' / ')],['评分',it.rating?`${Number(it.rating).toFixed(1)} / 10${d.ratingSource?' · '+d.ratingSource:''}`:''],['全剧资料',it.type==='tv'&&d.totalEpisodes?`${d.totalSeasons||'—'} 季 · ${d.totalEpisodes} 集`: ''],['本机收藏',it.type==='tv'?`${localSeasons} 季 · ${files.length} 个视频`:`${files.length} 个视频`]];
  return `<h3 class="detail-section-title">作品资料</h3><dl class="metadata-grid">${rows.filter(([,value])=>value).map(([label,value])=>`<div><dt>${esc(label)}</dt><dd>${esc(value)}</dd></div>`).join('')}</dl>${it.overview?`<h3 class="detail-section-title">剧情简介</h3><p class="full-overview">${esc(it.overview)}</p>`:''}${d.tags?.length?`<div class="metadata-keywords">${d.tags.map(t=>`<span>${esc(t)}</span>`).join('')}</div>`:''}<div class="external-links">${metadataLinks(it)}</div>${it.metadataNote?`<p class="section-note metadata-attribution">${esc(it.metadataNote)}</p>`:''}${d.updated?`<p class="section-note">资料更新：${esc(new Date(d.updated).toLocaleString('zh-CN'))}</p>`:''}`;
}
function detailPeople(it) {
  const people=it.details?.people?.length?it.details.people:(it.cast||[]).map(name=>({name,type:'Actor'}));
  if(!people.length)return `<div class="detail-empty">当前资料源暂无演职人员信息，可点击“补全资料”重新获取。</div>`;
  const actors=people.filter(p=>p.type==='Actor'),crew=people.filter(p=>p.type!=='Actor');
  const cards=(title,list)=>list.length?`<h3 class="detail-section-title">${title}</h3><div class="people-grid">${list.map(p=>`<article class="person-card">${p.image?`<img src="${art(p.image)}" alt="${esc(p.name)}" loading="lazy">`:`<div class="person-placeholder">${esc(p.name.slice(0,1))}</div>`}<div><strong>${esc(p.name)}</strong>${p.originalName&&p.originalName!==p.name?`<small>${esc(p.originalName)}</small>`:''}<span>${esc(personRole(p))}</span></div></article>`).join('')}</div>`:'';
  return cards('主要演员',actors)+cards('主创人员',crew);
}
function renderDetailPage(id,season=null,view=null) {
  id=canonicalItemID(id);
  const item=state.items.find(item=>item.id===id);
  detailItemID=id;
  detailSignature=detailKey(id);
  if(!item){
    $('#main').innerHTML='<div class="cinema-empty"><h1>没有找到这部作品</h1><p>它可能已被合并或移出媒体库。</p><a class="button" href="#library">返回媒体库</a></div>';
    document.title='未找到作品 · 映库';return;
  }
  const seasons=[...new Set(state.files.filter(file=>file.itemId===id).map(file=>file.season))].sort((a,b)=>a-b);
  detailSeason=seasons.includes(season)?season:seasons[0]??1;
  detailView=view??(item.type==='tv'?'episodes':'info');
  $('#main').innerHTML=cinema.detail(state,item,detailSeason,detailView,libraryReturnURL);
  document.title=item.title+' · 映库';
}
function showDetail(id,season=null,view=null){
  id=canonicalItemID(id);
  if(page==='library'){libraryReturnURL=libraryLink(libraryFilter,libraryQuery,librarySort);libraryScroll=window.scrollY;rememberLibrary();}
  const same=page==='detail'&&detailItemID===id;
  const target=itemLink(id,season??(same?detailSeason:null),view??(same?detailView:null));
  closeModal();
  if(location.hash===target){renderDetailPage(id,season??detailSeason,view??detailView);renderNav();}
  else location.hash=target;
}

function confirmLibraryRemoval(scope,id,season=null) {
  const file=scope==='file'?state.files.find(file=>file.id===id):null;
  const item=state.items.find(item=>item.id===(file?.itemId||canonicalItemID(id)));
  if(!item)return toast('作品已移出媒体库，请刷新后查看',true);
  const files=state.files.filter(file=>file.itemId===item.id&&(scope!=='file'||file.id===id)&&(scope!=='season'||file.season===season));
  const taskFiles=new Set(files.map(file=>file.id)),tasks=state.tasks.filter(task=>taskFiles.has(task.fileId));
  const running=tasks.some(task=>['downloading','finalizing'].includes(task.status))||files.some(file=>['downloading','finalizing'].includes(file.status));
  const seasonName=number=>number===0?'特别篇':`第 ${number} 季`;
  const episodeName=file=>`第 ${file.episode}${file.episodeEnd>file.episode?`–${file.episodeEnd}`:''} 集`;
  const label=`《${item.title}》${scope==='season'?` · ${seasonName(season)}`:scope==='file'&&item.type==='tv'?` · ${seasonName(file.season)} · ${episodeName(file)}`:''}`;
  libraryRemoval={itemID:item.id,scope,path:scope==='file'?`/files/${id}`:scope==='season'?`/items/${item.id}/seasons/${season}`:`/items/${item.id}`,deleteFiles:false,deleteToken:''};
  modal('移出媒体库？','默认保留文件，来源配置始终保留。',`<div class="library-removal-summary"><strong>${esc(label)}</strong><p>将移出 ${files.length} 个视频记录${tasks.length?`，以及 ${tasks.length} 个相关下载任务记录`:''}。</p>${scope==='file'?`<p class="library-removal-filename">${esc(file.name)}</p>${file.episodeEnd>file.episode?'<p>这是包含多集的合并视频，所含集数会一并移出。</p>':''}`:''}<p>观看进度和字幕关联会随记录移除。</p><fieldset class="library-delete-options"><label class="checkbox-field"><input type="checkbox" id="library-delete-files" disabled>同时永久删除服务器上的视频文件</label><p>本地来源会删除原视频；网盘来源仅删除本机下载副本。百度、夸克网盘云端原文件始终保留。</p><p>字幕、海报和 NFO 文件保留。相关下载临时文件会列入删除清单。</p><div id="library-delete-preview" role="status">${running?'停止下载并等待收尾后，才可选择删除文件。':'正在检查服务器上的文件…'}</div><div class="library-delete-warning" id="library-delete-warning" hidden>删除无法恢复，请核对文件清单。只想隐藏影视时，请不要勾选。</div></fieldset>${running?'<div class="error-box">选中的视频正在下载或入库，请先在下载任务页暂停，等待收尾后再移出。</div>':''}<div id="library-removal-error" role="alert"></div></div>`,`<button class="button" data-action="close-modal">返回</button>${running?'<button class="button" data-action="remove-library-tasks">查看下载任务</button>':'<button class="button danger" data-action="confirm-remove-library">移出媒体库，保留文件</button>'}`);
  if(!running)loadLibraryDeletePreview(libraryRemoval);
}
async function loadLibraryDeletePreview(selection){
  try{
    const preview=await api(selection.path+'/removal-preview');
    if(libraryRemoval!==selection||!$('#library-delete-preview'))return;
    selection.deleteToken=preview.token;$('#library-delete-files').disabled=!preview.files.length;
    $('#library-delete-preview').innerHTML=preview.files.length?`<details><summary>查看 ${preview.files.length} 个可能删除的服务器文件</summary><ul>${preview.files.map(file=>`<li><span>${file.kind==='partial'?'下载临时文件':'视频'} · ${bytes(file.size)}</span><code>${esc(file.path)}</code></li>`).join('')}</ul></details>`:'服务器上未找到相应文件，仅移出记录。';
  }catch(error){if(libraryRemoval===selection&&$('#library-delete-preview'))$('#library-delete-preview').textContent='文件删除暂不可用：'+error.message+'。仍可只移出记录。';}
}
async function removeLibrarySelection() {
  const selection=libraryRemoval;if(!selection)return;
  try {
    const result=await api(selection.path,selection.deleteFiles?{deleteFiles:true,deleteToken:selection.deleteToken}:undefined,'DELETE');
    libraryRemoval=null;closeModal();
    await refresh();
    if(state.items.some(item=>item.id===selection.itemID)){
      const seasons=[...new Set(state.files.filter(file=>file.itemId===selection.itemID).map(file=>file.season))].sort((a,b)=>a-b);
      showDetail(selection.itemID,seasons.includes(detailSeason)?detailSeason:seasons[0]??null,'episodes');
    }
    else location.hash=libraryReturnURL||libraryLink();
    toast(`已移出${result.removedFiles?` ${result.removedFiles} 个视频记录`:''}${selection.deleteFiles?`，已删除 ${result.deletedDiskFiles||0} 个服务器文件`:'，磁盘文件保留'}`);
    if(result.fileDeleteWarning)notifyFileDeleteWarning(result.fileDeleteWarning);
  } catch(error) {
    const target=$('#library-removal-error');
    if(target)target.innerHTML='<div class="error-box">'+esc(error.message)+'</div>';
    else toast(error.message,true);
  }
}

function matchItem(id) {
  id=canonicalItemID(id);
  const it=state.items.find(it=>it.id===id);if(!it)return;matchItemID=id;searchResults=[];
  modal('匹配影片资料',`为“${esc(it.title)}”选择正确的作品，视频与观看进度会保留。`,
    `<form id="match-form"><label class="field"><span>作品名称</span><div class="inline-input"><input name="query" value="${esc(it.title)}" required><button class="button" type="submit">${icon('search')}搜索</button></div><small>${state.config.tmdbConfigured?'资料来源：TMDb。':'默认使用无需 Key 的公开资料，部分条目和分集信息可能不完整。'}</small></label><div id="item-matches"></div></form>`,
    `<button class="button quiet" data-action="detail" data-id="${id}">${icon('back')}返回作品</button><button class="button" data-action="rescrape" data-id="${id}">${icon('refresh')}${it.metadataId&&it.metadataProvider!=='local'?'按原匹配重新获取':'自动补全资料'}</button>`);
}
function showFileRecovery(id){
  const file=state.files.find(f=>f.id===id),item=state.items.find(it=>it.id===file?.itemId);if(!file||!item)return;
  modal('处理视频文件',esc(item.title),`<div class="file-recovery"><div class="notice neutral">${icon('info')}<span>${esc(file.availability?.message||'可重新检查文件，或更新服务器上的保存位置。')}</span></div><p>作品资料、季集和观看进度会保留。存储暂时离线时，请先恢复挂载后重新检查。</p><label class="field"><span>原保存位置</span><code class="file-location-old">${esc(file.destination)}</code></label><label class="field"><span>服务器上的新视频路径</span><input id="file-location-input" value="${esc(file.destination)}" autocomplete="off"><small>填写运行映库的服务器路径；容器部署时填写容器内路径。只更新关联，不移动或改名视频。</small></label><div class="button-row"><button class="button small" data-action="check-file" data-id="${esc(id)}">${icon('refresh')}重新检查原位置</button><button class="button small quiet" data-action="remove-library-file" data-id="${esc(id)}">${icon('trash')}移除记录</button></div><div id="file-location-error" role="alert"></div></div>`,`<button class="button" data-action="close-modal">返回</button><button class="button primary" data-action="save-file-location" data-id="${esc(id)}">更新文件位置</button>`);
}
async function checkFile(id){
  if(id){const result=await api(`/files/${id}/availability?refresh=1`);const file=state.files.find(f=>f.id===id);if(file)file.availability=result;await refresh();const message=result.status==='available'?'文件可以正常读取':result.message;const box=$('#file-location-error');if(box)box.textContent=message;else toast(message,result.status!=='available');}
  else{await api('/files/check',{});toast('已开始后台检查，失效文件会在影视墙和作品页标记。');}
}
function stopPlayer() { const video = $('#player-video'); if (video && playerFileID) saveProgress(video, playerFileID); playerAbort?.abort(); playerAbort = null; if (video) { video.pause(); video.removeAttribute('src'); video.load(); } playerFileID = ''; }
function navigateCastPage() {
  if(page==='cast'){caster.renderPage($('#main'));return;}
  castReturnURL=location.hash||'#library';try{sessionStorage.setItem('yingku_cast_return',castReturnURL);}catch{}
  const video=$('#player-video');
  if(video&&playerFileID){
    video.pause();saveProgress(video,playerFileID);
    suspendedCastPlayer={nodes:[...$('#modal-root').childNodes],video,fileID:playerFileID,abort:playerAbort};
    $('#modal-root').replaceChildren();playerAbort=null;playerFileID='';
    if(document.fullscreenElement===$('#modal-root'))document.exitFullscreen().catch(()=>{});
    else if(document.webkitFullscreenElement===$('#modal-root'))document.webkitExitFullscreen?.();
    document.body.style.overflow='';$('.app-shell').inert=false;
  }else if($('.modal'))closeModal();
  location.hash='cast';
}
function castReturnLabel(){if(suspendedCastPlayer)return '返回播放器';const route=readMediaRoute(castReturnURL);return {detail:'返回作品',tasks:'返回下载任务',sources:'返回来源',settings:'返回设置'}[route.page]||'返回媒体库';}
async function finishCastNavigation(restore) {
  const saved=suspendedCastPlayer;if(!saved)return;
  suspendedCastPlayer=null;
  saved.abort?.abort();saved.video.pause();saved.video.removeAttribute('src');saved.video.load();
  if(!restore)return;
  const mine=++castRestoreSerial,targetHash=location.hash;
  try{
    const cast=await caster.sync(),latest=await api('/state');
    if(mine!==castRestoreSerial||location.hash!==targetHash||page==='cast'||$('.modal'))return;
    state=latest;const target=castResumeTarget(state,saved.fileID,cast);
    if(!target){toast('原视频暂不可用，请在作品页重新定位文件。',true);return;}
    if(page==='detail'&&detailItemID===target.file.itemId){history.replaceState(null,'',itemLink(target.file.itemId,target.file.season,'episodes'));renderPage();}
    playFile(target.file.id,null,{autoplay:false,resumePosition:target.position});
  }catch(error){toast('暂时无法同步投屏进度：'+error.message+'。请从媒体库重新打开。',true);}
}
function saveProgress(video,id){
  if(!video.isConnected||$('#player-video')!==video||playerFileID!==id||!video.dataset.progressToken||!Number.isFinite(video.currentTime)||video.currentTime<0||video.readyState<1)return;
  const value={position:video.currentTime,duration:Number.isFinite(video.duration)?video.duration:0,token:video.dataset.progressToken};
  progressPromise=progressPromise.then(()=>api(`/files/${id}/progress`,value)).then(result=>{if(result.ignored){if(video.dataset.progressToken===value.token)delete video.dataset.progressToken;return;}const f=state.files.find(f=>f.id===id);if(f){f.position=value.position;f.duration=value.duration;}}).catch(()=>{});
}
function updatePlayerNavigation() {
  const root=$('#player-episode-nav');if(!root||!playerFileID)return;
  const neighbors=episodeNeighbors(state,playerFileID);if(!neighbors){root.innerHTML='';return;}
  const signature=JSON.stringify([playerFileID,episodeSwitchBusy,...[neighbors.previous,neighbors.next].map(file=>file&&[file.id,file.status,file.season,file.episode,file.episodeEnd])]);
  if(root.dataset.signature===signature)return;
  root.dataset.signature=signature;
  root.innerHTML=[['previous','上一集',neighbors.previous,'已经是第一集'],['next','下一集',neighbors.next,'已经是最后一集']].map(([direction,label,file,boundary])=>{
    const ready=file?.status==='completed';
    const number=file?`${file.episode}${file.episodeEnd>file.episode?'–'+file.episodeEnd:''}`:'';
    const note=file?`${file.season===0?'特别篇':'第 '+file.season+' 季'} · 第 ${number} 集${ready?'':' · 未入库'}`:boundary;
    return `<button class="player-control episode-control" data-action="player-step" data-id="${esc(file?.id||'')}" title="${label} · ${esc(note)}" aria-label="${label}：${esc(note)}" ${ready&&!episodeSwitchBusy?'':'disabled'}>${icon(direction)}</button>`;
  }).join('');
}
async function stepEpisode(id) {
  if(episodeSwitchBusy)return;
  const neighbors=episodeNeighbors(state,playerFileID),target=[neighbors?.previous,neighbors?.next].find(file=>file?.id===id);
  if(!target)return toast('剧集列表已变化，请重新打开作品',true);
  if(target.status!=='completed')return toast('这一集尚未入库完成，请先查看下载任务',true);
  const previousVideo=$('#player-video');
  episodeSwitchBusy=true;updatePlayerNavigation();
  try {
    stopPlayer();await progressPromise;
    if($('#player-video')!==previousVideo)return;
    playFile(id,previousVideo);
  }finally{episodeSwitchBusy=false;updatePlayerNavigation();}
}
function playFile(id,reuseVideo=null,{autoplay=true,resumePosition=null}={}) {
  const f = state.files.find(f => f.id === id), it = state.items.find(it => it.id === f?.itemId); if (!f || !it) return;
  if(f.status!=='completed')return toast('视频尚未入库完成，请先查看下载任务',true);
  if(fileUnavailable(f)){showFileRecovery(id);return;}
  const isTS = /\.ts$/i.test(f.destination);
  const canShareMarkers = it.type === 'tv' && f.season > 0 && f.episode > 0 && !(f.episodeEnd > f.episode);
  const hasResume = f.position > 5 && (!f.duration || f.position < f.duration - 15);
  let resumeAt=resumePosition!==null?resumePosition:hasResume?f.position:0,claimPending=null,preserveRequested=false;
  const originalQuality=f.videoWidth>0&&f.videoHeight>0?`原画 · ${f.videoWidth} × ${f.videoHeight}`:'原画 · 读取分辨率中…';
  modal(esc(it.title), `${it.type === 'tv' ? `第 ${f.season} 季 · 第 ${f.episode} 集 · ` : ''}${esc(f.name)}`, `<div class="player-stage" tabindex="0" aria-label="视频播放区域">
    <video id="player-video" playsinline preload="metadata" src="/api/files/${id}/stream">${f.subtitles.map((s, i) => `<track kind="subtitles" src="/api/files/${id}/subtitles/${i}" label="${esc(s.name)}" srclang="zh" ${i === 0 ? 'default' : ''}>`).join('')}</video>
    <div class="player-buffering" id="player-buffering" role="status" hidden><span class="spinner"></span>正在缓冲…</div><div class="player-notice" id="player-notice" role="status"></div><div class="player-error" id="player-error" role="alert"></div>
    ${isTS ? '<div class="player-buffering" id="player-prepare" role="status" hidden><span class="spinner"></span>正在准备 TS 兼容播放…</div>' : ''}
    <div class="player-gesture" id="player-gesture" aria-live="off" hidden><strong id="player-gesture-title"></strong><span id="player-gesture-detail"></span></div>
    <button class="button player-skip" type="button" hidden></button>
    ${it.type==='tv'?'<section class="player-auto-next-panel" id="player-auto-next-panel" aria-label="自动播放下一集" hidden><strong id="player-next-title">即将播放下一集</strong><p id="player-next-description"></p><p id="player-next-countdown" aria-live="polite"></p><div><button class="button primary" data-player-action="next-now">立即播放</button><button class="button quiet" data-player-action="cancel-next">取消连播</button></div></section>':''}
    <div class="player-controls" aria-label="播放控制">
      <input class="player-seek" id="player-seek" type="range" min="0" max="1" step="0.1" value="0" aria-label="播放进度" disabled>
      <div class="player-toolbar"><div class="player-controls-left">
        <button class="player-control" data-player-action="play" aria-label="播放">${icon('play')}</button>
        ${it.type==='tv'?'<div class="player-episode-nav" id="player-episode-nav" aria-label="剧集切换"></div>':''}
        <span class="player-clock" id="player-clock">0:00 / 0:00</span>
        <div class="player-volume-group"><button class="player-control" data-player-action="mute" aria-label="静音">${icon('volume')}</button><input class="player-volume" id="player-volume" type="range" min="0" max="1" step="0.05" value="1" aria-label="音量"></div>
      </div><div class="player-controls-right">
        <span class="player-quality" id="player-quality" role="status" aria-live="polite" title="直接播放原视频，没有转码">${originalQuality}</span>
        <button class="player-control" data-player-action="settings" aria-label="播放设置" aria-expanded="false" aria-controls="player-settings" title="播放设置">${icon('settings')}</button>
        <button class="player-control" data-player-action="fullscreen" aria-label="进入全屏" title="进入全屏">${icon('fullscreen')}</button>
      </div></div>
      <div class="player-settings" id="player-settings" hidden>
        ${it.type==='tv'?'<label class="player-checkbox"><input id="player-auto-next" type="checkbox" checked><span>看完自动播放下一集</span></label>':''}
        <label class="player-checkbox" title="此浏览器的所有视频共用"><input id="player-auto-skip" type="checkbox"><span>自动跳过片头片尾</span></label>
        <label>画面亮度<span class="player-brightness-control"><input id="player-brightness" aria-label="画面亮度" type="range" min="20" max="150" step="1" value="100"><output id="player-brightness-value" for="player-brightness">100%</output></span></label>
        <label>播放速度<select id="player-rate" aria-label="播放速度">${[0.5,0.75,1,1.25,1.5,2].map(rate=>`<option value="${rate}" ${rate===1?'selected':''}>${rate===1?'正常速度':rate+'×'}</option>`).join('')}</select></label>
        <label>字幕<select id="player-subtitles" aria-label="字幕"><option value="-1">关闭字幕</option>${f.subtitles.map((subtitle,index)=>`<option value="${index}" ${index===0?'selected':''}>${esc(subtitle.name)}</option>`).join('')}</select></label>
        <button class="button quiet" data-action="upload-subtitle" data-id="${esc(id)}">${icon('subtitle')}添加字幕</button>
        ${skipEditorMarkup(canShareMarkers)}
        <p class="player-keyboard-help">← 快退 5 秒 · → 短按快进 5 秒、长按临时 2×<br>↑ / ↓ 音量 ±5% · 空格 播放 / 暂停</p>
      </div>
    </div>
  </div>`,'',true);
  $('.modal').classList.add('cinema-player');
  if(reuseVideo){const replacement=$('#player-video');reuseVideo.innerHTML=replacement.innerHTML;reuseVideo.src=replacement.getAttribute('src');replacement.replaceWith(reuseVideo);}
  $('.modal-head').insertAdjacentHTML('afterbegin',`<button class="icon-btn player-back" data-action="detail" data-id="${esc(it.id)}" ${it.type==='tv'?`data-season="${f.season}" data-view="episodes"`:''} aria-label="返回作品" title="返回作品">${icon('back')}</button>`);
  $('.modal-head [data-action="close-modal"]').insertAdjacentHTML('beforebegin',`<button class="icon-btn player-cast" data-action="cast" data-id="${esc(id)}" aria-label="投屏到电视" title="投屏到电视">${icon('cast')}</button>`);
  playerFileID = id; progressAt = 0; const video = $('#player-video'); playerAbort = new AbortController(); const options = { signal: playerAbort.signal };
  delete video.dataset.progressToken;
  const playerSignal=playerAbort.signal;
  const root = $('.cinema-player');
  const prepareIndicator = $('#player-prepare');
  let compatibility = '', wantPlay = autoplay;
  let activityToken = '';
  const activityURL = `/api/files/${id}/playback/activity`;
  function releaseActivity(token = activityToken) {
    if (!token) return;
    if (token === activityToken) activityToken = '';
    fetch(activityURL, {
      method: 'POST', keepalive: true,
      headers: {'Content-Type': 'application/json', 'X-App-Token': state?.csrf || ''},
      body: JSON.stringify({token, ended: true}),
    }).catch(() => {});
  }
  const activityTimer = setInterval(() => {
    if (!video.isConnected) releaseActivity();
    else if (activityToken) api(`/files/${id}/playback/activity`, {token: activityToken}).catch(() => {});
  }, 15000);
  playerSignal.addEventListener('abort', () => {
    clearInterval(activityTimer);
    releaseActivity();
  }, {once: true});
  window.addEventListener('pagehide', () => releaseActivity(), options);
  window.addEventListener('pageshow', event => {
    if (event.persisted && video.isConnected && !video.paused) claimPlayback(true).catch(() => {});
  }, options);
  function applyResume(){if(video.readyState>=1&&Number.isFinite(resumeAt))video.currentTime=Math.max(0,Math.min(resumeAt,Number.isFinite(video.duration)?video.duration:resumeAt));}
  async function claimPlayback(preservePosition=false){
    if(preservePosition)preserveRequested=true;
    if(claimPending)return claimPending;
    const previousToken=video.dataset.progressToken||'';
    claimPending=(async()=>{
      const result=await api(`/files/${id}/playback`,{token:previousToken,trackActivity:true});
      if(playerSignal.aborted||$('#player-video')!==video){releaseActivity(result.activityToken);throw new Error('播放器已关闭');}
      releaseActivity();
      activityToken = result.activityToken || '';
      const keepPosition=preserveRequested||resumePosition!==null&&!previousToken||previousToken&&previousToken===result.token;
      resumeAt=keepPosition?video.currentTime:result.position>5&&(!result.duration||result.position<result.duration-15)?result.position:resumePosition!==null?result.position:0;
      if(!keepPosition)applyResume();video.dataset.progressToken=result.token;
    })().finally(()=>{claimPending=null;preserveRequested=false;});
    return claimPending;
  }
  updatePlayerNavigation();
  const updateVideoInfo=()=>{
    const width=video.videoWidth,height=video.videoHeight;
    if(width<1||height<1)return;
    const quality=$('#player-quality');if(quality)quality.textContent=`原画 · ${width} × ${height}`;
    if(f.videoWidth===width&&f.videoHeight===height)return;
    f.videoWidth=width;f.videoHeight=height;
    api(`/files/${id}/video-info`,{width,height}).catch(()=>{});
  };
  video.addEventListener('loadedmetadata', () => { updateVideoInfo();applyResume(); }, options);
  video.addEventListener('resize',updateVideoInfo,options);
  video.addEventListener('timeupdate', () => { if (Date.now() - progressAt > 5000) { progressAt = Date.now(); saveProgress(video, id); } }, options);
  video.addEventListener('pause', () => {
    if (!video.error && compatibility !== 'preparing') wantPlay = false;
    saveProgress(video, id);
  }, options);
  video.addEventListener('ended', () => { saveProgress(video, id); releaseActivity(); }, options);
  function showPlaybackError(message) {
    if (!f.videoWidth || !f.videoHeight) $('#player-quality').textContent = '原画 · 分辨率未知';
    $('#player-error').innerHTML = `<div class="error-box">${esc(message)}<button class="button small" data-action="recover-file" data-id="${esc(id)}">检查文件与位置</button></div>`;
  }
  async function prepareTSPlayback() {
    if (compatibility === 'preparing' || playerSignal.aborted) return;
    if (video.currentTime > 0) resumeAt = video.currentTime;
    compatibility = 'preparing';
    prepareIndicator.hidden = false;
    $('#player-error').textContent = '';
    $('#player-notice').textContent = '';
    const endpoint = `/files/${id}/ts-playback`;
    try {
      let result = await api(endpoint, undefined, 'POST', playerSignal);
      while (result.status === 'preparing') {
        await new Promise(resolve => setTimeout(resolve, 1200));
        if (playerSignal.aborted) return;
        result = await api(endpoint, undefined, 'GET', playerSignal);
      }
      if (result.status !== 'ready' || !result.key) throw new Error('无法取得 TS 播放缓存');
      if (wantPlay && !video.dataset.progressToken && !root.closest('[inert]')) await claimPlayback();
      if (playerSignal.aborted) return;
      compatibility = 'ready';
      $('#player-quality').title = 'TS 兼容播放，仅更换封装';
      video.src = `/api${endpoint}/${encodeURIComponent(result.key)}`;
    } catch (error) {
      compatibility = 'failed';
      if (!playerSignal.aborted) showPlaybackError(error.message || 'TS 兼容播放准备失败');
    } finally {
      prepareIndicator.hidden = true;
    }
    if (compatibility === 'ready' && wantPlay && !playerSignal.aborted && !root.closest('[inert]')) {
      await video.play().catch(error => {
        if (!playerSignal.aborted && !video.error) $('#player-notice').textContent = error.name === 'NotAllowedError' ? '兼容视频已就绪，点击播放。' : '兼容视频暂时无法播放，请重试。';
      });
    }
  }
  async function requestPlay() {
    wantPlay = true;
    if (compatibility === 'preparing') return;
    if (compatibility === 'failed') return prepareTSPlayback();
    await claimPlayback();
    if (playerSignal.aborted || compatibility === 'preparing') return;
    const source = video.src;
    try { await video.play(); }
    catch (error) {
      if (playerSignal.aborted || source !== video.src || compatibility === 'preparing') return;
      if (isTS && !compatibility && error.name === 'NotSupportedError') return prepareTSPlayback();
      if (!video.error) throw error;
    }
  }
  video.addEventListener('error', () => {
    if (compatibility === 'preparing' || compatibility === 'failed') return;
    const code = video.error?.code;
    if (isTS && !compatibility && (code === 3 || code === 4)) { void prepareTSPlayback();return; }
    showPlaybackError(compatibility === 'ready'
      ? 'TS 已换封装，浏览器仍无法播放。请重新打开视频或检查音视频编码。'
      : '视频暂时无法播放。文件可能无法读取，或浏览器不支持此格式。');
    void api(`/files/${id}/availability?refresh=1`).catch(()=>{});
  }, options);
  const onSeek = () => {
    void claimPlayback(true).then(() => {
      saveProgress(video, id);
      if (video.ended) releaseActivity();
    }).catch(error => { if (!playerSignal.aborted) toast(error.message, true); });
  };
  const {canAutoSkip} = mountPlayerControls({root,video,signal:playerSignal,icon,requestPlay,onSeek,getNextEpisode:()=>episodeNeighbors(state,id)?.next,onNextEpisode:stepEpisode});
  mountSkipSegments({root, video, signal: playerSignal, segments: f, canAutoSkip, onSeek,
    seasonSegments: canShareMarkers ? it.skipSeasons?.[f.season] || {} : null,
    saveSegment: async (kind, scope, input) => {
      const endpoint = `/files/${id}/skip-segments/${kind}${scope === 'season' ? '?scope=season' : ''}`;
      const result = input ? await api(endpoint, input) : await api(endpoint, undefined, 'DELETE');
      if (scope === 'season') {
        const latestFile = state.files.find(file => file.id === id);
        const series = state.items.find(item => item.id === (latestFile?.itemId || it.id));
        if (series) {
          const seasons = series.skipSeasons ||= {};
          const marks = seasons[f.season] ||= {};
          marks[kind === 'intro' ? 'intro' : 'outroFromEnd'] = result.segment;
        }
      } else {
        const latest = state.files.find(file => file.id === id);
        if (latest) {
          latest[kind] = result.segment;
          latest[`${kind}SkipDisabled`] = result.disabled;
        }
      }
      return result;
    },
  });
  if(autoplay)requestPlay().catch(error=>{if(!playerSignal.aborted)$('#player-notice').textContent=error.message||'视频已就绪，点击播放继续。';});
}
function uploadSubtitle(id) {
  const input = document.createElement('input'); input.type = 'file'; input.accept = '.srt,.vtt';
  input.addEventListener('change', async () => { const file = input.files[0]; if (!file) return; if (file.size > 8 * 1024 * 1024) return toast('字幕不能超过 8 MB', true); try { const r = await fetch(`/api/files/${id}/subtitles`, { method: 'POST', headers: { 'X-App-Token': state.csrf, 'X-Filename': encodeURIComponent(file.name) }, body: file }); const result = await r.json(); if (!r.ok) throw new Error(result.error); const wasPlaying = !!$('#player-video'); await refresh(); toast('字幕已添加，重新打开播放即可选择'); if (!wasPlaying) showDetail(state.files.find(f => f.id === id).itemId, detailSeason); } catch (e) { toast(e.message, true); } }); input.click();
}
document.addEventListener('click', async event => {
  if(event.target.closest('#cinema-nav a,.cinema-brand')){resetLibraryScroll=true;}
  const el = event.target.closest('[data-action]'); if (!el) return; event.preventDefault();
  const action = el.dataset.action, id = el.dataset.id;
  if(action.startsWith('tasks-')){await taskPage.action(action,el);return;}
  if (el.disabled) return;
  if(action.startsWith('cloud-')){await sourceLogin.action(action,el);return;}
  if(action.startsWith('scan-')){await scanner.action(action,el);return;}
  if(action==='recover-file'){showFileRecovery(id);return;}
  if(action==='check-files'||action==='check-file'){await busy(el,()=>checkFile(action==='check-file'?id:null));return;}
  if(action==='save-file-location'){await busy(el,async()=>{try{await api(`/files/${id}/relocate`,{path:$('#file-location-input').value});closeModal();await refresh(true);toast('文件位置已更新，作品资料和观看进度已保留。');}catch(error){$('#file-location-error').innerHTML=`<div class="error-box">${esc(error.message)}</div>`;}});return;}
  if (action === 'go-sources') { closeModal(); if (page === 'sources') renderSources(); else location.hash = 'sources'; }
  else if (action === 'add-source') sourceModal();
  else if (action === 'edit-source') { event.stopPropagation(); sourceModal(id); }
  else if (action === 'choose-source') { sourceID = id; sourcePath = '/'; entries = []; selected.clear(); sourceError = ''; if (page !== 'sources') { page = 'sources'; location.hash = 'sources'; } renderSources(); loadDirectory('/'); }
  else if (action === 'browse') loadDirectory(el.dataset.path);
  else if (action === 'refresh-source') loadDirectory(sourcePath);
  else if (action === 'close-modal') closeModal();
  else if (action === 'library-filter') { location.hash=libraryLink(id,libraryQuery,librarySort); }
  else if(action==='clear-library-search'){libraryFilter='all';libraryQuery='';librarySort='added';location.hash=libraryLink();}
  else if (action === 'task-control') await busy(el, async () => { const command = el.dataset.command; await api(`/tasks/${id}/${command}`, {}); await refresh(); if (command === 'remove') toast('任务记录已移除，原文件和临时文件保留'); });
  else if (action === 'test-tmdb') await busy(el, async () => { const result=await api('/config/test', {}); toast(result.provider==='public'?'公开资料服务可用，无需填写 Key':'TMDb 已连接，可以搜索并获取影片资料'); });
  else if (action === 'test-ai') await busy(el,async()=>{const result=await api('/config/ai/test',{});toast(result.message||'AI 连接成功');});
  else if (action === 'baidu-authorize') { const f = $('#source-form'), client = f.elements.clientId.value.trim(); if (!client) return toast('请先填写 App Key', true); const u = new URL('https://openapi.baidu.com/oauth/2.0/authorize'); u.search = new URLSearchParams({ response_type: 'code', client_id: client, redirect_uri: 'oob', scope: 'basic,netdisk', display: 'page' }); window.open(u, '_blank', 'noopener,noreferrer'); }
  else if (action === 'remove-source') {
    const s = state.sources.find(s => s.id === id); if(!s)return;
    const note=s.type==='local'
      ? `将移除“${esc(s.name)}”，并从媒体库移除该来源的视频记录。仍有其他来源视频的作品会保留。磁盘上的视频、字幕和资料文件全部保留。`
      : `将移除“${esc(s.name)}”的连接配置。已经下载的影片仍保留在媒体库中，网盘和本机中的文件都保留。`;
    modal('移除来源？', '', `<p class="section-note">${note}</p><div id="remove-error"></div>`, `<button class="button" data-action="close-modal">取消</button><button class="button danger" data-action="confirm-remove-source" data-id="${id}">移除来源</button>`);
  }
  else if (action === 'confirm-remove-source') await busy(el, async () => { const result=await api(`/sources/${id}`, undefined, 'DELETE'); closeModal(); if (sourceID === id) { sourceID = ''; sourcePath='/'; entries=[]; selected.clear(); } await refresh(true); toast(result.removedFiles?`来源已移除，已从媒体库移除 ${result.removedFiles} 个视频记录，原文件保留`:'来源已移除'); });
  else if (action === 'import-selected') openImport();
  else if (action === 'import-type') { readImportConfig(); draft.type = id; draft.tmdbId = 0; draft.metadataProvider=''; draft.metadataId=''; draft.matched = null; renderImportConfig(); }
  else if (action === 'import-search') await busy(el, () => searchImport(el));
  else if (action === 'choose-import-match') { readImportConfig(); const m = searchResults[Number(el.dataset.index)]; draft.matched = m; draft.tmdbId = m.provider==='tmdb'?Number(m.id):0; draft.metadataProvider=m.provider||'tmdb'; draft.metadataId=m.externalId||String(m.id); draft.title = m.title; draft.year = m.year; renderImportConfig(); }
  else if (action === 'clear-match') { readImportConfig(); draft.matched = null; draft.tmdbId = 0; draft.metadataProvider=''; draft.metadataId=''; renderImportConfig(); }
  else if (action === 'import-back') { readPreviewInputs(); renderImportConfig(); }
  else if (action === 'number-episodes') { $$('#preview-table tbody tr').forEach((tr, i) => { const ep = $('[name=episode]', tr); if (ep && !ep.disabled) ep.value = i + 1; }); dirtyPreview(); }
  else if (action === 'import-confirm') await busy(el, async () => { if (previewDirty) return getPreview(); const result = await api('/import', importBody()); closeModal(); await refresh(); page = result.mode==='reference'?'library':'tasks'; location.hash=page; renderPage(); toast(result.mode==='reference'?`已将 ${result.count} 个本地视频加入媒体库，原文件保持原位`:`已添加 ${result.count} 个${result.mode==='copy'?'复制':'下载'}任务${result.skipped ? `，跳过 ${result.skipped} 个重复文件` : ''}`); });
  else if (action === 'detail') showDetail(id,el.dataset.season!==undefined?Number(el.dataset.season):null,el.dataset.view||null);
  else if (action === 'player-step') await stepEpisode(id);
  else if (action === 'remove-library-item') confirmLibraryRemoval('item',id);
  else if (action === 'remove-library-season') confirmLibraryRemoval('season',id,Number(el.dataset.season));
  else if (action === 'remove-library-file') confirmLibraryRemoval('file',id);
  else if (action === 'confirm-remove-library') await busy(el,removeLibrarySelection);
  else if (action === 'remove-library-tasks') { taskPage.search(state.items.find(item=>item.id===libraryRemoval?.itemID)?.title||'');closeModal(); location.hash='tasks'; }
  else if (action === 'detail-season') showDetail(id, Number(el.dataset.season));
  else if (action === 'detail-view') showDetail(id, detailSeason, el.dataset.view);
  else if (action === 'match-item') matchItem(id);
  else if (action === 'choose-item-match') await busy(el, async () => { const m = searchResults[Number(el.dataset.index)]; await api(`/items/${matchItemID}/metadata`, { tmdbId:m.provider==='tmdb'?Number(m.id):0,provider:m.provider||'tmdb',externalId:m.externalId||String(m.id) }); await refresh(); showDetail(matchItemID); toast('已开始获取影片资料，可稍后重新打开查看'); });
  else if (action === 'rescrape') await busy(el, async () => { const it = state.items.find(it => it.id === id); await api(`/items/${id}/metadata`, it.metadataId&&it.metadataProvider&&it.metadataProvider!=='local'?{tmdbId:it.metadataProvider==='tmdb'?it.tmdbId:0,provider:it.metadataProvider,externalId:it.metadataId}:it.tmdbId&&state.config.tmdbConfigured?{tmdbId:it.tmdbId}:{auto:true}); await refresh(); showDetail(id); toast('已开始重新刮削，视频不会重新下载'); });
  else if (action === 'play') playFile(id);
  else if (action === 'cast') await caster.open(id);
  else if (action === 'upload-subtitle') uploadSubtitle(id);
});
document.addEventListener('change', event => {
  const el = event.target;
  if(el.id==='library-delete-files'&&libraryRemoval){libraryRemoval.deleteFiles=el.checked;$('#library-delete-warning').hidden=!el.checked;el.closest('fieldset').classList.toggle('deleting',el.checked);const button=$('[data-action="confirm-remove-library"]');if(button)button.textContent=el.checked?'永久删除文件并移出':'移出媒体库，保留文件';return;}
  if(el.closest('#tasks-page')){taskPage.changed(el);return;}
  if(el.closest('#scan-plan')){scanner.changed(el);return;}
  if(el.id==='library-sort'){librarySort=el.value;syncLibraryURL();renderLibraryContent();}
  else if (el.id === 'import-series') { readImportConfig();const it=state.items.find(it=>it.id===draft.itemId);if(it){draft.title=it.title;draft.year=it.year;draft.matched=null;draft.tmdbId=0;draft.metadataProvider='';draft.metadataId='';}renderImportConfig(); }
  else if (el.id === 'local-import-mode') { readImportConfig(); renderImportConfig(); }
  else if (el.id === 'source-type') { renderSourceFields(el.value); const n = $('#source-form').elements.name; if (!n.value || ['我的百度网盘', '我的夸克网盘', '我的 WebDAV', '本地影片'].includes(n.value)) n.value = { baidu: '我的百度网盘', quark: '我的夸克网盘', webdav: '我的 WebDAV', local: '本地影片' }[el.value]; }
  else if (el.id === 'select-all') { entries.filter(e => e.media && !e.dir).forEach(e => el.checked ? selected.add(e.key) : selected.delete(e.key)); renderBrowser(); }
  else if (el.classList.contains('file-check')) { el.checked ? selected.add(el.dataset.key) : selected.delete(el.dataset.key); renderBrowser(); }
});
document.addEventListener('input', event => {
  if(event.target.id==='tasks-query'){taskPage.input(event.target);return;}
	if(event.target.closest('#source-form')){sourceLogin?.manualChanged(event.target);return;}
  if(event.target.closest('#scan-plan')){scanner.markDirty();return;}
  if (event.target.id === 'library-search') { libraryQuery = event.target.value; syncLibraryURL(); renderLibraryContent(); }
  else if (event.target.closest('#preview-table')) dirtyPreview();
});
document.addEventListener('submit', async event => {
  const f = event.target; event.preventDefault();
  const button = $(`button[form="${f.id}"]`) || $('button[type=submit]', f);
  if (f.id === 'settings-form') await busy(button, async () => { const data = new FormData(f); await api('/config', { mediaRoot: data.get('mediaRoot'), concurrency: Number(data.get('concurrency')), tmdbToken: data.get('tmdbToken'), clearTmdb: data.has('clearTmdb'), exportNfo: data.has('exportNfo'), autoMetadata: data.has('autoMetadata'), ai:{enabled:data.has('aiEnabled'),endpoint:data.get('aiEndpoint'),model:data.get('aiModel'),key:data.get('aiKey'),clearKey:data.has('aiClearKey')} }); await refresh(true); toast('设置已保存'); });
  else if (f.id === 'source-form') {
    if (!sourceLogin.beforeSave()) return;
    await busy(button, async () => {
      const errorBox = $('#source-form-error', f);
      errorBox.innerHTML = '';
      const data = Object.fromEntries(new FormData(f));
      data.type = sourceEditing?.type || data.type;
      if (sourceEditing) data.id = sourceEditing.id;
      try {
        const migrating = sourceMigration?.rootChanged();
        const result = migrating ? await sourceMigration.submit(data) : await api('/sources', data);
        if (!result || !f.isConnected) return;
        closeModal();
        sourceID = result.id;
        sourcePath = '/';
        entries = [];
        selected.clear();
        await refresh();
        page = 'sources';
        location.hash = 'sources';
        renderPage();
        loadDirectory('/');
        toast(migrating ? `来源已更新，已重新定位 ${result.count} 个视频` : '来源已连接');
      } catch (error) {
        errorBox.innerHTML = `<div class="error-box">${esc(error.message)}</div>`;
      }
    });
  }
  else if (f.id === 'import-config') { await busy(button, async () => { try { await getPreview(); } catch (e) { $('#import-error').innerHTML = `<div class="error-box">${esc(e.message)}</div>`; } }); }
  else if (f.id === 'match-form') await busy(button, async () => { const it = state.items.find(it => it.id === matchItemID), q = new FormData(f).get('query'); const root = $('#item-matches'); root.innerHTML = '<p class="section-note"><span class="spinner"></span> 正在搜索…</p>'; try { const result = await api(`/metadata/search?type=${it.type}&query=${encodeURIComponent(q)}`); searchResults = result.results; root.innerHTML = matchList('choose-item-match'); } catch (e) { root.innerHTML = `<div class="error-box">${esc(e.message)}</div>`; } });
});
document.addEventListener('keydown', event => {
  if(event.defaultPrevented)return;
  if (event.key === 'Enter' && event.target.matches('.source-card')) { event.preventDefault(); event.target.click(); }
  const dialog = caster?.dialog()||$('.modal'); if (!dialog) return;
  if (event.key === 'Escape') { event.preventDefault();if(!caster?.close())closeModal(); }
  if (event.key === 'Tab') { const controls = $$('button:not([disabled]),a[href],input:not([disabled]),select:not([disabled]),video[controls]', dialog).filter(el => el.offsetParent !== null); const first = controls[0], last = controls.at(-1); if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); } else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); } }
});
$('#sidebar-add').addEventListener('click', () => sourceModal());
sourceLogin=createSourceLogin({api,esc,icon,getEditing:()=>sourceEditing});
cinema=createCinema({esc,icon,art,duration,bytes,detailInfo,detailPeople,sourceStatus,taskBadge});
taskPage=createTaskPage({api,getState:()=>state,esc,icon,bytes,heading,empty,taskBadge,toast,refresh,modal,closeModal});
scanner=createScanner({api,getState:()=>state,modal,closeModal,toast,esc,icon,refresh,navigate:target=>{page=target;location.hash=target;renderPage();}});
caster=createCaster({api,toast,esc,icon,getState:()=>state,getPlayerPosition:id=>playerFileID===id&&Number.isFinite($('#player-video')?.currentTime)?$('#player-video').currentTime:undefined,awaitProgress:()=>progressPromise,navigateRemote:navigateCastPage,returnFromRemote:()=>{location.hash=castReturnURL==='#cast'?'#library':castReturnURL;},getReturnLabel:castReturnLabel});
window.addEventListener('hashchange',()=>{
  const previousPage=page,previousItem=detailItemID,scroll=window.scrollY;
  const target=readMediaRoute(location.hash),fromNavigation=resetLibraryScroll;
  resetLibraryScroll=false;
  if(previousPage==='library'){libraryScroll=scroll;libraryReturnURL=libraryLink(libraryFilter,libraryQuery,librarySort);rememberLibrary();}
  if($('.modal'))closeModal();
  renderPage();
  if(previousPage==='cast'&&page!=='cast')finishCastNavigation(location.hash===castReturnURL);
  if(page==='library'&&previousPage==='detail'&&!fromNavigation&&libraryLink(target.filter,target.query,target.sort)===libraryReturnURL)window.scrollTo(0,libraryScroll);
  else if(page==='detail'&&previousPage==='detail'&&previousItem===detailItemID){
    const tabs=$('.item-tabs');
    const top=tabs?tabs.getBoundingClientRect().top+window.scrollY-parseFloat(getComputedStyle(document.body).getPropertyValue('--cinema-header-height')):scroll;
    window.scrollTo(0,Math.min(scroll,Math.max(0,top)));
  }else window.scrollTo(0,0);
  $('#main').focus({preventScroll:true});
});
window.addEventListener('pagehide', () => { const video = $('#player-video'); if (video && playerFileID) saveProgress(video, playerFileID); });
await refresh(true);
setInterval(() => refresh(), 2000);
