export function readMediaRoute(hash) {
  const raw=hash.replace(/^#/,'');
  const [path,query='']=raw.split('?');
  const params=new URLSearchParams(query);
  if(path.startsWith('item/')){
    let id='';try{id=decodeURIComponent(path.slice(5));}catch{}
    const season=params.has('season')&&/^\d{1,3}$/.test(params.get('season'))?Number(params.get('season')):null;
    return {page:'detail',id,season,view:['episodes','info','people'].includes(params.get('view'))?params.get('view'):null};
  }
  return {page:['library','sources','tasks','settings','cast'].includes(path)?path:'library',
    filter:['movie','tv'].includes(params.get('type'))?params.get('type'):'all',
    query:params.get('q')||'',sort:['title','year'].includes(params.get('sort'))?params.get('sort'):'added'};
}
export function itemLink(id,season=null,view=null) {
  const params=new URLSearchParams();
  if(season!==null)params.set('season',String(season));
  if(view)params.set('view',view);
  return '#item/'+encodeURIComponent(id)+(params.size?'?'+params:'');
}
export function libraryLink(filter='all',query='',sort='added') {
  const params=new URLSearchParams();
  if(filter!=='all')params.set('type',filter);
  if(query)params.set('q',query);
  if(sort!=='added')params.set('sort',sort);
  return '#library'+(params.size?'?'+params:'');
}

export function episodeNeighbors(state,fileID) {
  const current=state.files.find(file=>file.id===fileID);
  if(!current||!state.items.some(item=>item.id===current.itemId&&item.type==='tv'))return null;
  const files=state.files.filter(file=>file.itemId===current.itemId).sort((a,b)=>a.season-b.season||a.episode-b.episode||(a.episodeEnd||a.episode)-(b.episodeEnd||b.episode)||a.id.localeCompare(b.id));
  const index=files.findIndex(file=>file.id===fileID);
  return {previous:files[index-1]||null,next:files[index+1]||null};
}

export const fileUnavailable=file=>['missing','unreadable'].includes(file?.availability?.status);
export function preferredPlaybackFile(files){
  const ready=files.filter(f=>f.status==='completed'&&!fileUnavailable(f));
  const dated=ready.filter(f=>f.lastPlayed).sort((a,b)=>String(b.lastPlayed).localeCompare(String(a.lastPlayed)));
  return dated[0]||ready.filter(f=>f.position>5&&(!f.duration||f.position<f.duration-15)).at(-1)||ready[0];
}
export function castResumeTarget(state,originalID,cast){
  const original=state.files.find(f=>f.id===originalID),target=state.files.find(f=>f.id===cast?.fileId);
  if(target&&target.itemId===original?.itemId&&target.status==='completed'&&!fileUnavailable(target))return {file:target,position:Math.max(0,cast.position||0)};
  return original&&original.status==='completed'&&!fileUnavailable(original)?{file:original,position:original.position||0}:null;
}

export function createCinema({esc,icon,art,duration,bytes,detailInfo,detailPeople,sourceStatus,taskBadge}) {
  const filesFor=(state,id)=>state.files.filter(file=>file.itemId===id).sort((a,b)=>a.season-b.season||a.episode-b.episode);
  const resumable=file=>file.status==='completed'&&!fileUnavailable(file)&&file.position>5&&(!file.duration||file.position<file.duration-15);
  const preferred=preferredPlaybackFile;
  const episodeNumber=file=>String(file.episode).padStart(2,'0')+(file.episodeEnd>file.episode?'–'+String(file.episodeEnd).padStart(2,'0'):'');
  const episodeLabel=file=>'第 '+file.season+' 季 · 第 '+episodeNumber(file)+' 集';
  const progress=file=>file.duration?Math.min(100,file.position/file.duration*100):0;
  const image=(path,cls='',alt='',lazy=true)=>path?`<img class="${cls}" src="${esc(art(path,true))}" alt="${esc(alt)}" ${lazy?'loading="lazy"':'fetchpriority="high"'}>`:'';
  const meta=(item,files)=>[item.year,item.type==='tv'?new Set(files.map(file=>file.season)).size+' 季':'电影',item.details?.runtime?item.details.runtime+' 分钟'+(item.type==='tv'?' / 集':''):'',item.details?.contentRating].filter(Boolean);
  function poster(item,files) {
    const colors=['#38372d','#253a43','#44302f','#293448','#3b2d3e'];
    const index=[...item.id].reduce((n,c)=>n+c.charCodeAt(0),0)%colors.length;
    const file=preferred(files),missing=files.filter(fileUnavailable).length;
    return `<article class="wall-card"><a class="wall-card-link" href="${esc(itemLink(item.id))}" aria-label="查看${esc(item.title)}详情">
      <div class="wall-poster" style="--poster-color:${colors[index]}">${item.poster?image(item.poster,'',item.title+'海报'):`<div class="wall-placeholder"><span>${item.type==='tv'?'电视剧':'电影'}</span><strong>${esc(item.title)}</strong>${item.year?'<small>'+esc(item.year)+'</small>':''}</div>`}
      ${item.rating?'<span class="wall-rating">★ '+Number(item.rating).toFixed(1)+'</span>':''}
      ${item.type==='tv'&&files.length?'<span class="wall-seasons">'+new Set(files.map(file=>file.season)).size+' 季</span>':''}
      ${missing?`<span class="wall-unavailable">${missing===files.length?'文件不可用':missing+' 个文件不可用'}</span>`:''}<span class="wall-card-hover">查看详情</span>
      ${file&&resumable(file)?'<span class="wall-progress"><i style="width:'+progress(file)+'%"></i></span>':''}</div>
      <h3>${esc(item.title)}</h3><p>${esc([item.year,item.type==='tv'?'电视剧':'电影'].filter(Boolean).join(' · '))}</p></a>
      ${file?`<button class="wall-play" data-action="play" data-id="${esc(file.id)}" aria-label="播放${esc(item.title)}" title="播放">${icon('play')}</button>`:''}</article>`;
  }
  function libraryShell(state,filter,query,sort) {
    return `<div class="cinema-library"><section id="library-feature"></section>
      <section class="library-collection"><div id="continue-section"></div>
      <header class="wall-toolbar"><div class="wall-heading-line"><h1 id="wall-heading">我的媒体库</h1><span id="wall-count"></span></div>
      <div class="wall-tools"><label class="search-box">${icon('search')}<input id="library-search" aria-label="搜索媒体库" placeholder="搜索片名、原名或年份" value="${esc(query)}"></label>
      <label class="wall-sort"><span class="sr-label">排序方式</span><select id="library-sort" aria-label="排序方式">${[['added','最近加入'],['title','名称排序'],['year','年份排序']].map(([value,label])=>`<option value="${value}" ${sort===value?'selected':''}>${label}</option>`).join('')}</select></label>
      <button class="button quiet" data-action="check-files">${icon('refresh')}检查文件</button><button class="button quiet wall-import" data-action="go-sources">${icon('plus')}导入作品</button></div></header>
      <div id="library-content"></div></section></div>`;
  }
  function libraryContent(state,filter,query,sort) {
    const q=query.trim().toLowerCase();
    const items=state.items.filter(item=>(filter==='all'||item.type===filter)&&[item.title,item.originalTitle,item.year,...(item.genres||[])].join(' ').toLowerCase().includes(q));
    items.sort(sort==='title'?(a,b)=>a.title.localeCompare(b.title,'zh-CN',{numeric:true}):sort==='year'?(a,b)=>(Number(b.year)||0)-(Number(a.year)||0):(a,b)=>String(b.created||'').localeCompare(String(a.created||''))||state.items.indexOf(b)-state.items.indexOf(a));
    const first=items[0],firstFiles=first?filesFor(state,first.id):[],file=preferred(firstFiles);
    const feature=first&&filter==='all'&&!q&&!state.files.some(resumable)?`<section class="cinema-hero ${first.backdrop?'':'poster-backdrop'}">
      ${image(first.backdrop||first.poster,'cinema-backdrop','',false)}<div class="cinema-shade"></div><div class="cinema-hero-copy">
      <span class="feature-kicker">${file&&resumable(file)?'继续你的故事':'我的私人放映室'}</span><h2>${esc(first.title)}</h2>
      <div class="cinema-meta">${meta(first,firstFiles).map(value=>'<span>'+esc(value)+'</span>').join('')}${first.rating?'<span class="cinema-rating">★ '+Number(first.rating).toFixed(1)+'</span>':''}</div>
      <p>${esc(first.overview||'收藏你的电影和剧集，在这里开始下一段故事。')}</p>
      <div class="cinema-actions">${file?`<button class="button primary" data-action="play" data-id="${esc(file.id)}">${icon('play')}${resumable(file)?'继续播放':'开始播放'}</button>`:''}<a class="button hero-details" href="${esc(itemLink(first.id))}">${icon('info')}查看详情</a></div>
      ${file&&resumable(file)?'<small class="feature-resume">'+(first.type==='tv'?episodeLabel(file)+' · ':'')+'已观看 '+duration(file.position)+'</small>':''}</div></section>`:'';
    const continuing=state.items.map(item=>({item,file:preferred(filesFor(state,item.id))})).filter(entry=>entry.file&&resumable(entry.file)&&(filter==='all'||entry.item.type===filter)&&(!q||items.includes(entry.item))).slice(-8).reverse();
    const resume=continuing.length?`<section class="continue-collection"><h2>继续观看<span>接着上次的进度</span></h2><div class="continue-rail">${continuing.map(({item,file})=>{
      const episode=(item.episodes||[]).find(ep=>ep.season===file.season&&ep.number===file.episode);
      return `<button class="resume-tile" data-action="play" data-id="${esc(file.id)}" aria-label="继续观看${esc(item.title)}"><div class="resume-image">${image(episode?.still||item.backdrop||item.poster)}<span class="resume-play">${icon('play')}</span><span class="resume-time">${duration(file.position)}${file.duration?' / '+duration(file.duration):''}</span><span class="wall-progress"><i style="width:${progress(file)}%"></i></span></div><strong>${esc(item.title)}</strong><small>${item.type==='tv'?episodeLabel(file):esc(item.year||'电影')}</small></button>`;
    }).join('')}</div></section>`:'';
    const grid=items.length?'<div class="poster-wall">'+items.map(item=>poster(item,filesFor(state,item.id))).join('')+'</div>':
      `<div class="cinema-empty">${icon(state.items.length?'search':'film')}<h2>${state.items.length?'没有找到匹配的作品':'你的影视墙，从第一部收藏开始'}</h2><p>${state.items.length?'试试其他名称，或切换电影和电视剧。':'导入已有电影或剧集，海报和作品资料会在这里展示。'}</p><button class="button" data-action="${state.items.length?'clear-library-search':'go-sources'}">${state.items.length?'查看全部作品':'导入第一部作品'}</button></div>`;
    return {feature,resume,grid,count:items.length,title:q?'搜索结果':filter==='movie'?'电影':filter==='tv'?'电视剧':'我的媒体库'};
  }
  function episodes(item,files,season) {
    const localSeasons=[...new Set(files.map(file=>file.season))],d=item.details||{};
    const visible=item.type==='tv'?files.filter(file=>file.season===season):files;
    const current=(d.seasons||[]).find(value=>value.number===season);
    return `${item.type==='tv'?`<div class="season-collection"><h2>季</h2><div class="season-rail">${localSeasons.map(number=>{
      const info=(d.seasons||[]).find(value=>value.number===number),count=files.filter(file=>file.season===number).length;
      return `<a class="season-cover ${number===season?'active':''}" href="${esc(itemLink(item.id,number,'episodes'))}" ${number===season?'aria-current="page"':''}>${image(info?.poster||item.poster)}<span><strong>${number===0?'特别篇':'第 '+number+' 季'}</strong><small>${[info?.year||info?.premiereDate?.slice(0,4),count+' 个视频'].filter(Boolean).map(esc).join(' · ')}</small></span></a>`;
    }).join('')}</div></div><header class="episode-section-heading"><div><h2>${season===0?'特别篇':'第 '+season+' 季'}</h2>${current?.premiereDate?'<p>'+esc(current.premiereDate)+' 首播</p>':''}</div><div class="season-actions"><span>${visible.length} 个视频</span><button class="button small quiet" data-action="remove-library-season" data-id="${esc(item.id)}" data-season="${season}">${icon('trash')}移出本季</button></div></header>${current?.overview?'<p class="season-intro">'+esc(current.overview)+'</p>':''}`:'<h2 class="media-section-heading">视频文件</h2>'}
      <div class="episode-grid">${visible.map(file=>{
        const ep=(item.episodes||[]).find(value=>value.season===file.season&&value.number===file.episode);
        const ready=file.status==='completed'&&!fileUnavailable(file);
        return `<article class="episode-card"><button class="episode-art" data-action="play" data-id="${esc(file.id)}" ${ready?'':'disabled'} aria-label="播放${item.type==='tv'?'第 '+episodeNumber(file)+' 集':esc(item.title)}">
          ${image(ep?.still||item.backdrop||item.poster)}<span class="episode-play">${icon('play')}</span>${item.type==='tv'?'<span class="episode-label">'+episodeNumber(file)+'</span>':''}
          ${file.duration>0?'<span class="episode-length">'+duration(file.duration)+'</span>':ep?.runtime?'<span class="episode-length">'+ep.runtime+' 分钟</span>':''}
          ${resumable(file)?'<span class="wall-progress"><i style="width:'+progress(file)+'%"></i></span>':''}</button>
          <h3>${esc(ep?.name||(item.type==='tv'?'第 '+episodeNumber(file)+' 集':item.title))}</h3><div class="episode-caption">${[ep?.airDate,ep?.rating?'★ '+Number(ep.rating).toFixed(1):'',file.position>5?'已看到 '+duration(file.position):''].filter(Boolean).map(esc).join(' · ')}${fileUnavailable(file)?'<span class="badge red">文件不可用</span>':!ready?taskBadge(file.status):''}</div>
          ${ep?.overview?'<p class="episode-overview">'+esc(ep.overview)+'</p>':''}
          ${fileUnavailable(file)?`<div class="episode-unavailable"><p>${esc(file.availability.message||'文件暂不可用')}</p><div><button class="button small" data-action="recover-file" data-id="${esc(file.id)}">重新定位</button><button class="button small quiet" data-action="check-file" data-id="${esc(file.id)}">重新检查</button><button class="button small quiet" data-action="remove-library-file" data-id="${esc(file.id)}">移除记录</button></div></div>`:''}<details class="episode-file"><summary>文件与字幕</summary><p>${esc(file.name)}<br>${bytes(file.size)}${file.reference?' · 使用原文件':''}${file.videoWidth>0&&file.videoHeight>0?`<br>原画 · ${file.videoWidth} × ${file.videoHeight}`:''}</p><div class="episode-file-actions">${ready?'<button class="button small quiet" data-action="upload-subtitle" data-id="'+esc(file.id)+'">'+icon('subtitle')+'添加字幕</button>':''}<button class="button small quiet library-remove" data-action="remove-library-file" data-id="${esc(file.id)}">${icon('trash')}${item.type==='tv'?'移出本集':'移出此视频'}</button></div></details></article>`;
      }).join('')}</div>`;
  }
  function detail(state,item,season,view,back) {
    const files=filesFor(state,item.id),visible=item.type==='tv'?files.filter(file=>file.season===season):files;
    const file=preferred(visible),d=item.details||{};
    const tabs=[['episodes',item.type==='tv'?'剧集':'视频文件'],['info','作品资料'],['people','演职人员']];
    return `<article class="item-page"><div class="item-backdrop ${item.backdrop?'':'poster-backdrop'}">${image(item.backdrop||item.poster,'','',false)}</div>
      <div class="item-page-content"><a class="back-to-library" href="${esc(back)}">${icon('back')}返回媒体库</a>
      <header class="item-hero"><div class="item-poster">${item.poster?image(item.poster,'',item.title+'海报',false):'<div class="wall-placeholder"><strong>'+esc(item.title)+'</strong></div>'}</div>
      <div class="item-heading"><span class="feature-kicker">${item.type==='tv'?'电视剧':'电影'}${d.status?' · '+esc(sourceStatus(d.status)):''}</span><h1>${esc(item.title)}</h1>
      ${item.originalTitle&&item.originalTitle!==item.title?'<p class="item-original-title">'+esc(item.originalTitle)+'</p>':''}
      <div class="cinema-meta">${meta(item,files).map(value=>'<span>'+esc(value)+'</span>').join('')}${item.rating?'<span class="cinema-rating" title="'+esc(d.ratingSource||'资料源评分')+'">★ '+Number(item.rating).toFixed(1)+'</span>':''}</div>
      ${item.genres?.length?'<div class="item-genres">'+item.genres.slice(0,5).map(genre=>'<span>'+esc(genre)+'</span>').join('')+'</div>':''}
      ${d.tagline?'<p class="item-tagline">'+esc(d.tagline)+'</p>':''}
      <p class="item-overview">${esc(item.overview||'影片资料尚未补齐，你仍可播放已经入库的视频。')}</p>
      <div class="cinema-actions">${file?`<button class="button primary" data-action="play" data-id="${esc(file.id)}">${icon('play')}${resumable(file)?'继续播放':'开始播放'}</button><button class="button item-cast" data-action="cast" data-id="${esc(file.id)}">${icon('cast')}投屏到电视</button>`:''}
      ${!file&&files.some(fileUnavailable)?`<button class="button" data-action="recover-file" data-id="${esc(files.find(fileUnavailable).id)}">处理失效文件</button>`:''}<details class="item-manage"><summary aria-label="更多作品操作">•••</summary><div><button data-action="rescrape" data-id="${esc(item.id)}">${icon('refresh')}补全资料</button><button data-action="match-item" data-id="${esc(item.id)}">${icon('search')}匹配作品</button><button class="library-remove" data-action="remove-library-item" data-id="${esc(item.id)}">${icon('trash')}${item.type==='tv'?'移出整部剧':'移出媒体库'}</button></div></details></div>
      ${file&&resumable(file)?'<p class="item-resume">'+(item.type==='tv'?episodeLabel(file)+' · ':'')+'从 '+duration(file.position)+' 继续</p>':''}</div></header>
      <nav class="item-tabs" aria-label="作品详情导航">${tabs.map(([key,label])=>`<a href="${esc(itemLink(item.id,season,key))}" class="${view===key?'active':''}" ${view===key?'aria-current="page"':''}>${label}</a>`).join('')}</nav>
      <section class="item-content" aria-label="${tabs.find(([key])=>key===view)?.[1]||'作品资料'}">
      ${item.metadataError?'<details class="metadata-warning"><summary>部分资料尚未完成</summary><p>'+esc(item.metadataError)+'</p></details>':''}
      ${view==='episodes'?episodes(item,files,season):view==='people'?detailPeople(item):detailInfo(item,files)}
      </section>${d.sources?.length?'<footer class="item-attribution">资料来源：'+d.sources.map(source=>/^https?:\/\//i.test(source.url||'')?'<a href="'+esc(source.url)+'" target="_blank" rel="noopener noreferrer">'+esc(source.name)+' ↗</a>':esc(source.name)).join(' · ')+'</footer>':''}</div></article>`;
  }
  return {libraryShell,libraryContent,detail,episodes};
}
