import {applyGestureVolume} from './player-gestures.js';

export function mountPlayerKeyboard({root,video,signal,onSeek,onVolume,onHold,onRelease,onActivity,onStart,onTogglePlay,onMute,onFullscreen,onEscape,keyboardTarget=window,visibilityTarget=document,schedule=setTimeout,unschedule=clearTimeout,isActive=()=>root.isConnected&&!root.closest('[inert]')}) {
  let right=null,holdTimer=null;
  const listen=(target,event,handler)=>target.addEventListener(event,handler,{signal});
  const nativeControl=target=>target?.isContentEditable||target?.closest?.('input,select,textarea,a,summary,[role="textbox"]');
  function finish(commit=false) {
    if(!right)return;
    const pressed=right;right=null;unschedule(holdTimer);holdTimer=null;
    if(pressed.originalRate!==undefined)video.playbackRate=pressed.originalRate;
    if(commit&&!pressed.held)seekBy(5);
    onRelease();onActivity();
  }
  function seekBy(delta) {
    const duration=video.duration;
    if(!Number.isFinite(duration)||duration<=0)return;
    video.currentTime=Math.max(0,Math.min(duration,video.currentTime+delta));onSeek();
  }
  listen(root,'keydown',event=>{
    const key=event.key.toLowerCase();
    if(key==='escape'){finish();if(onEscape())event.preventDefault();return;}
    if(event.ctrlKey||event.metaKey||event.altKey||event.shiftKey){finish();return;}
    if(!isActive()||nativeControl(event.target))return;
    const arrow=['arrowright','arrowleft','arrowup','arrowdown'].includes(key);
    if(!arrow&&event.target?.closest?.('button'))return;
    if(arrow){
      event.preventDefault();
      if(key==='arrowright'){
        if(right||event.repeat)return;
        right={held:false};onStart();onActivity();
        holdTimer=schedule(()=>{
          if(!right)return;
          if(!isActive()){finish();return;}
          right.held=true;
          if(video.paused||video.ended){onHold(null,null);return;}
          right.originalRate=video.playbackRate;video.playbackRate=Math.max(2,right.originalRate);
          onHold(video.playbackRate,right.originalRate);onActivity();
        },550);
      }else if(key==='arrowleft'){
        finish();onStart();seekBy(-5);onActivity();
      }else{
        onStart();
        const start=video.muted?0:video.volume;
        const value=Math.max(0,Math.min(1,Math.round(start*100+(key==='arrowup'?5:-5))/100));
        const result=applyGestureVolume(video,value);onVolume(result.value,result.supported);onActivity();
      }
      return;
    }
    if(![' ','k','m','f'].includes(key))return;
    event.preventDefault();if(event.repeat)return;
    if(key===' '||key==='k')onTogglePlay();else if(key==='m')onMute();else onFullscreen();
    onActivity();
  });
  listen(keyboardTarget,'keyup',event=>{
    if(event.key.toLowerCase()!=='arrowright'||!right)return;
    event.preventDefault();finish(isActive()&&root.contains(event.target)&&!nativeControl(event.target));
  });
  listen(root,'focusout',()=>finish());
  listen(keyboardTarget,'blur',()=>finish());
  listen(visibilityTarget,'visibilitychange',()=>{if(visibilityTarget.hidden)finish();});
  for(const event of ['pause','ended','error'])listen(video,event,()=>finish());
  signal.addEventListener('abort',()=>finish(),{once:true});
  return {cancel:()=>finish(),active:()=>!!right};
}
