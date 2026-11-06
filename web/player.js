import {mountVideoGestures} from './player-gestures.js';
import {mountPlayerKeyboard} from './player-keyboard.js';

const preferences={volume:1,muted:false,brightness:1,rate:1,subtitle:0,autoNext:true};
try{preferences.autoNext=localStorage.getItem('yingku_auto_next')!=='0';}catch{}

export function createPlayerIdle({setHidden,canHide,signal,schedule=setTimeout,unschedule=clearTimeout,delay=2800}) {
  let timer=null,disposed=false;
  function activity(){
    if(disposed)return;
    unschedule(timer);timer=null;setHidden(false);
    if(canHide())timer=schedule(()=>{timer=null;if(!disposed&&canHide())setHidden(true);},delay);
  }
  signal.addEventListener('abort',()=>{disposed=true;unschedule(timer);timer=null;},{once:true});
  return {activity};
}

export function playbackTime(value) {
  const seconds=Math.max(0,Math.floor(Number.isFinite(value)?value:0));
  const hours=Math.floor(seconds/3600),minutes=Math.floor(seconds/60)%60,remainder=seconds%60;
  return (hours?hours+':'+String(minutes).padStart(2,'0'):String(minutes))+':'+String(remainder).padStart(2,'0');
}

export function mountPlayerControls({root,video,signal,icon,onSeek,getNextEpisode,onNextEpisode,beforePlay=async()=>{}}) {
  const $=selector=>root.querySelector(selector);
  const stage=$('.player-stage'),seek=$('#player-seek'),volume=$('#player-volume');
  const play=$('[data-player-action="play"]'),mute=$('[data-player-action="mute"]'),fullscreen=$('[data-player-action="fullscreen"]');
  const clock=$('#player-clock'),menu=$('#player-settings'),rate=$('#player-rate'),subtitles=$('#player-subtitles');
  const nextPanel=$('#player-auto-next-panel'),autoNext=$('#player-auto-next'),gestureHint=$('#player-gesture');
  const brightness=$('#player-brightness'),brightnessValue=$('#player-brightness-value');
  const fullscreenRoot=document.getElementById('modal-root');
  const positionedCues=new WeakSet();
  let noticeTimer,nextTimer,seeking=false,dragging=false,waiting=false,gestures,keyboard,nextID='';
  const listen=(target,event,callback)=>target.addEventListener(event,callback,{signal});
  const isFullscreen=()=>document.fullscreenElement===fullscreenRoot||document.webkitFullscreenElement===fullscreenRoot;
  const announce=message=>{const notice=$('#player-notice');notice.textContent=message;clearTimeout(noticeTimer);noticeTimer=setTimeout(()=>{notice.textContent='';},3500);};
  function layoutCaptions() {
    const height=video.clientHeight;if(!height)return;
    const reserve=root.classList.contains('controls-hidden')?24:$('.player-controls').offsetHeight+20;
    const line=Math.max(5,Math.min(95,(height-reserve)/height*100));
    for(const track of video.textTracks)for(const cue of track.cues||[]){
      if(cue.line!=='auto'&&!positionedCues.has(cue))continue;
      positionedCues.add(cue);cue.snapToLines=false;
      if('lineAlign' in cue)cue.lineAlign='end';
      cue.line=line;
    }
  }
  const idle=createPlayerIdle({signal,
    canHide:()=>root.isConnected&&!root.closest('[inert]')&&!video.paused&&!video.ended&&!waiting&&menu.hidden&&!seeking&&!dragging&&!gestures?.active(),
    setHidden:hidden=>{if(root.classList.contains('controls-hidden')===hidden)return;root.classList.toggle('controls-hidden',hidden);layoutCaptions();}
  });
  function showControls(){idle.activity();}
  function updateClock(value=video.currentTime) {
    const duration=Number.isFinite(video.duration)?video.duration:0;
    seek.disabled=duration<=0;seek.max=String(duration||1);
    if(!seeking)seek.value=String(Math.min(duration,Number.isFinite(value)?value:0));
    const position=seeking?Number(seek.value):value;
    clock.textContent=playbackTime(position)+' / '+playbackTime(duration);
    seek.setAttribute('aria-valuetext',clock.textContent);
    seek.style.setProperty('--played',duration?Math.min(100,position/duration*100)+'%':'0%');
    let buffered=0;
    if(duration&&video.buffered.length)buffered=Math.min(100,video.buffered.end(video.buffered.length-1)/duration*100);
    seek.style.setProperty('--buffered',buffered+'%');
  }
  function updatePlay() {
    const paused=video.paused||video.ended;
    play.innerHTML=icon(paused?'play':'pause');play.setAttribute('aria-label',paused?'播放':'暂停');play.title=paused?'播放（空格）':'暂停（空格）';
    showControls();
  }
  function updateVolume() {
    preferences.volume=video.volume;preferences.muted=video.muted;
    mute.innerHTML=icon(video.muted||video.volume===0?'muted':'volume');
    mute.setAttribute('aria-label',video.muted||video.volume===0?'取消静音':'静音');mute.title=mute.getAttribute('aria-label');
    volume.value=String(video.muted?0:video.volume);
  }
  function updateBrightness(value=preferences.brightness) {
    preferences.brightness=Math.max(0.2,Math.min(1.5,value));
    video.style.filter=preferences.brightness===1?'':`brightness(${preferences.brightness})`;
    const percent=Math.round(preferences.brightness*100);
    brightness.value=String(percent);brightness.setAttribute('aria-valuetext',percent+'%');brightnessValue.textContent=percent+'%';
  }
  function showGesture(title,detail,kind='gesture') {
    gestureHint.dataset.kind=kind;gestureHint.hidden=false;$('#player-gesture-title').textContent=title;$('#player-gesture-detail').textContent=detail;
  }
  function updateFullscreen() {
    const full=isFullscreen();fullscreen.innerHTML=icon(full?'exitFullscreen':'fullscreen');
    fullscreen.setAttribute('aria-label',full?'退出全屏':'进入全屏');fullscreen.title=full?'退出全屏':'进入全屏';showControls();
  }
  function updateSubtitles() {
    const tracks=video.textTracks;
    const selected=preferences.subtitle>=tracks.length?0:preferences.subtitle;
    for(let i=0;i<tracks.length;i++)tracks[i].mode=i===selected?'showing':'disabled';
    subtitles.value=String(tracks.length?selected:-1);
    layoutCaptions();
  }
  function setWaiting(value) {
    waiting=value;$('#player-buffering').hidden=!value;showControls();
  }
  async function togglePlay() {
    if(video.paused||video.ended){try{await beforePlay();if(!signal.aborted)await video.play();}catch(error){announce(error.message||'暂时无法播放，请重试或检查视频格式。');}}
    else video.pause();
  }
  async function toggleFullscreen() {
    try {
      if(isFullscreen()){
        if(document.exitFullscreen)await document.exitFullscreen();else document.webkitExitFullscreen?.();
      }else if(fullscreenRoot.requestFullscreen)await fullscreenRoot.requestFullscreen();
      else fullscreenRoot.webkitRequestFullscreen?.();
    }catch{announce('当前浏览器暂时无法进入全屏。');}
  }
  function closeMenu() {menu.hidden=true;$('[data-player-action="settings"]').setAttribute('aria-expanded','false');showControls();}
  function cancelAutoNext() {clearTimeout(nextTimer);nextTimer=null;nextID='';if(nextPanel)nextPanel.hidden=true;}
  function episodeName(file) {return `${file.season===0?'特别篇':'第 '+file.season+' 季'} · 第 ${file.episode}${file.episodeEnd>file.episode?'–'+file.episodeEnd:''} 集`;}
  async function advanceEpisode() {
    clearTimeout(nextTimer);nextTimer=null;
    const next=getNextEpisode?.();
    if(!next||next.id!==nextID){cancelAutoNext();announce('剧集列表已变化，请手动选择下一集。');return;}
    if(next.status!=='completed'){startAutoNext();return;}
    cancelAutoNext();
    try{await onNextEpisode(next.id);}catch{announce('下一集暂时无法打开，请手动重试。');}
  }
  function startAutoNext() {
    cancelAutoNext();if(!nextPanel||!preferences.autoNext||!video.ended||root.closest('[inert]'))return;
    const next=getNextEpisode?.();if(!next)return;
    nextID=next.id;nextPanel.hidden=false;
    $('#player-next-description').textContent=episodeName(next);
    const ready=next.status==='completed';
    $('#player-next-title').textContent=ready?'即将播放下一集':'下一集尚未入库';
    $('[data-player-action="next-now"]').hidden=!ready;
    $('[data-player-action="cancel-next"]').textContent=ready?'取消连播':'关闭';
    if(!ready){$('#player-next-countdown').textContent='下载完成后，可以点击下一集继续播放。';showControls();return;}
    const deadline=Date.now()+5000;
    const tick=()=>{
      if(signal.aborted||!video.ended||!preferences.autoNext||root.closest('[inert]')){cancelAutoNext();return;}
      const seconds=Math.max(0,Math.ceil((deadline-Date.now())/1000));
      $('#player-next-countdown').textContent=`${seconds} 秒后自动播放`;
      if(seconds===0){void advanceEpisode();return;}
      nextTimer=setTimeout(tick,250);
    };
    tick();showControls();
  }
  listen(root,'click',event=>{
    const button=event.target.closest('[data-player-action]');
    if(!button){if(!event.target.closest('#player-settings,#player-seek'))closeMenu();return;}
    event.preventDefault();
    const action=button.dataset.playerAction;
    if(action==='play')void togglePlay();
    else if(action==='mute'){const silent=video.muted||video.volume===0;if(silent&&video.volume===0)video.volume=1;video.muted=!silent;}
    else if(action==='fullscreen')void toggleFullscreen();
    else if(action==='cancel-next')cancelAutoNext();
    else if(action==='next-now')void advanceEpisode();
    else if(action==='settings'){
      menu.hidden=!menu.hidden;button.setAttribute('aria-expanded',String(!menu.hidden));showControls();
    }
  });
  gestures=mountVideoGestures({video,signal,isControlsHidden:()=>root.classList.contains('controls-hidden'),onActivity:showControls,onStart:()=>{keyboard?.cancel();cancelAutoNext();},
    getBrightness:()=>preferences.brightness,
    onBrightness:value=>{updateBrightness(value);showGesture('画面亮度 '+Math.round(preferences.brightness*100)+'%','仅调整视频画面');},
    onVolume:(value,supported)=>{updateVolume();showGesture(supported?'音量 '+Math.round(value*100)+'%':'请用设备音量键',supported?'上下滑动调整音量':'当前浏览器不支持网页调音量');},
    onTap:revealOnly=>{stage.focus({preventScroll:true});if(!revealOnly)void togglePlay();},
    onPreview:(target,delta)=>{seeking=true;seek.value=String(target);updateClock();showGesture((delta<0?'快退 ':'快进 ')+playbackTime(Math.abs(delta)),playbackTime(target)+' / '+playbackTime(video.duration));},
    onCommit:()=>{seeking=false;updateClock();onSeek();},
    onHold:(speed,original)=>showGesture(speed?`${speed}× 倍速播放`:'播放时长按可临时加速',speed?`松手恢复 ${original}×`:'松手返回','speed'),
    onFinish:()=>{seeking=false;gestureHint.hidden=true;updateClock();}
  });
  listen(video,'dblclick',()=>void toggleFullscreen());
  listen(window,'blur',gestures.cancel);listen(document,'visibilitychange',()=>{if(document.hidden)gestures.cancel();});
  listen(root,'pointermove',event=>{if(event.pointerType!=='touch')showControls();});
  listen(root,'pointerdown',event=>{if(event.target.closest('input[type="range"]'))dragging=true;showControls();});
  const finishDrag=()=>{if(dragging){dragging=false;showControls();}};
  listen(window,'pointerup',finishDrag);listen(window,'pointercancel',finishDrag);listen(window,'blur',finishDrag);
  listen(root,'focusin',showControls);listen(root,'focusout',showControls);
  keyboard=mountPlayerKeyboard({root,video,signal,onSeek:()=>{updateClock();onSeek();},onActivity:showControls,
    onStart:()=>{gestures?.cancel();cancelAutoNext();},
    onHold:(speed,original)=>showGesture(speed?`${speed}× 倍速播放`:'播放时长按可临时加速',speed?`松开恢复 ${original}×`:'松开返回','speed'),
    onRelease:()=>{gestureHint.hidden=true;},
    onVolume:(value,supported)=>{updateVolume();announce(supported?'音量 '+Math.round(value*100)+'%':'当前浏览器不支持网页调音量，请用设备音量键。');},
    onTogglePlay:()=>void togglePlay(),onMute:()=>{video.muted=!video.muted;},onFullscreen:()=>void toggleFullscreen(),
    onEscape:()=>{if(!menu.hidden){closeMenu();return true;}if(isFullscreen()){void toggleFullscreen();return true;}return false;}
  });
  listen(seek,'input',()=>{seeking=true;updateClock();showControls();});
  listen(seek,'change',()=>{
    const duration=Number.isFinite(video.duration)?video.duration:0;
    if(duration>0)video.currentTime=Math.max(0,Math.min(duration,Number(seek.value)));
    seeking=false;updateClock();onSeek();showControls();
  });
  listen(seek,'pointercancel',()=>{seeking=false;updateClock();});
  listen(volume,'input',()=>{try{video.volume=Number(volume.value);video.muted=video.volume===0;}catch{}showControls();});
  listen(brightness,'input',()=>{updateBrightness(Number(brightness.value)/100);showControls();});
  listen(rate,'change',()=>{preferences.rate=Number(rate.value);video.playbackRate=preferences.rate;showControls();});
  listen(subtitles,'change',()=>{preferences.subtitle=Number(subtitles.value);updateSubtitles();showControls();});
  listen(video,'play',()=>{cancelAutoNext();updatePlay();});listen(video,'pause',updatePlay);
  listen(video,'seeking',cancelAutoNext);
  listen(video,'ended',()=>{setWaiting(false);updatePlay();startAutoNext();});
  listen(video,'timeupdate',()=>{if(!seeking)updateClock();});
  listen(video,'progress',()=>{if(!seeking)updateClock();});
  listen(video,'durationchange',()=>updateClock());
  listen(video,'loadedmetadata',()=>{updateClock();updateSubtitles();});
  listen(video,'volumechange',updateVolume);
  listen(video,'waiting',()=>{if(!video.paused)setWaiting(true);});
  listen(video,'playing',()=>setWaiting(false));
  listen(video,'canplay',()=>setWaiting(false));
  listen(video,'error',()=>setWaiting(false));
  listen(video.textTracks,'addtrack',updateSubtitles);
  listen(video.textTracks,'change',layoutCaptions);
  root.querySelectorAll('track').forEach(track=>listen(track,'load',layoutCaptions));
  listen(video,'resize',layoutCaptions);listen(window,'resize',layoutCaptions);
  listen(document,'fullscreenchange',updateFullscreen);listen(document,'webkitfullscreenchange',updateFullscreen);
  signal.addEventListener('abort',()=>{clearTimeout(noticeTimer);clearTimeout(nextTimer);},{once:true});
  if(autoNext){autoNext.checked=preferences.autoNext;listen(autoNext,'change',()=>{preferences.autoNext=autoNext.checked;try{localStorage.setItem('yingku_auto_next',preferences.autoNext?'1':'0');}catch{}if(preferences.autoNext)startAutoNext();else cancelAutoNext();});}
  fullscreen.hidden=!fullscreenRoot.requestFullscreen&&!fullscreenRoot.webkitRequestFullscreen;
  rate.value=String(preferences.rate);video.playbackRate=preferences.rate;
  try{video.volume=preferences.volume;}catch{}video.muted=preferences.muted;
  updatePlay();updateVolume();updateBrightness();updateClock();updateSubtitles();updateFullscreen();
  return {canAutoSkip:()=>menu.hidden&&!seeking&&!dragging&&!gestures.active()&&!root.closest('[inert]')};
}
