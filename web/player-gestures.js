export function gestureSeekTarget(start,delta,width,duration) {
  if(!Number.isFinite(duration)||duration<=0)return start;
  const span=Math.min(120,Math.max(10,duration*0.02));
  return Math.max(0,Math.min(duration,start+delta/Math.max(1,width)*span));
}

export function gestureLevelTarget(start,delta,height,min=0,max=1) {
  return Math.max(min,Math.min(max,start-delta/Math.max(1,height)*(max-min)*1.5));
}

export function applyGestureVolume(video,target) {
  try {
    video.volume=target;
    if(Math.abs(video.volume-target)>0.01)return {value:video.muted?0:video.volume,supported:false};
    video.muted=target===0;
    return {value:video.muted?0:video.volume,supported:true};
  }catch{return {value:video.muted?0:video.volume,supported:false};}
}

export function mountVideoGestures({video,signal,onTap,onActivity,onStart,onPreview,onCommit,onHold,onFinish,isControlsHidden,getBrightness=()=>1,onBrightness=()=>{},onVolume=()=>{},schedule=setTimeout,unschedule=clearTimeout,now=Date.now}) {
  let pointer=null,holdTimer=null,suppressUntil=0,lastTouch=-Infinity;
  const listen=(type,callback)=>video.addEventListener(type,callback,{signal});
  function restoreRate() {if(pointer?.kind==='hold'&&pointer.originalRate!==undefined)video.playbackRate=pointer.originalRate;}
  function finish(commit=false) {
    if(!pointer)return;
    const current=pointer;unschedule(holdTimer);holdTimer=null;
    restoreRate();pointer=null;suppressUntil=now()+800;
    if(commit&&current.kind==='seek'){
      video.currentTime=current.target;onCommit();
    }else if(commit&&current.kind==='tap')onTap(current.revealOnly);
    try{if(video.hasPointerCapture?.(current.id))video.releasePointerCapture(current.id);}catch{}
    onFinish();onActivity();
  }
  listen('pointerdown',event=>{
    if(event.pointerType!=='touch'&&event.pointerType!=='pen')return;
    if(event.isPrimary===false||pointer){finish();return;}
    lastTouch=now();
    const bounds=video.getBoundingClientRect();
    pointer={id:event.pointerId,x:event.clientX,y:event.clientY,width:bounds.width,height:bounds.height,start:video.currentTime,target:video.currentTime,kind:'tap',level:event.clientX<bounds.left+bounds.width/2?'brightness':'volume',brightness:getBrightness(),volume:video.muted?0:video.volume,revealOnly:isControlsHidden()};
    try{video.setPointerCapture(event.pointerId);}catch{}
    onStart();onActivity();
    holdTimer=schedule(()=>{
      if(!pointer||pointer.kind!=='tap')return;
      pointer.kind='hold';
      if(video.paused||video.ended){onHold(null,null);return;}
      pointer.originalRate=video.playbackRate;video.playbackRate=Math.max(2,pointer.originalRate);
      onHold(video.playbackRate,pointer.originalRate);
    },550);
  });
  listen('pointermove',event=>{
    if(!pointer||event.pointerId!==pointer.id)return;
    if(pointer.kind==='hold'||pointer.kind==='cancelled')return;
    const dx=event.clientX-pointer.x,dy=event.clientY-pointer.y;
    if(pointer.kind==='tap'||pointer.kind==='pending'){
      if(Math.max(Math.abs(dx),Math.abs(dy))<12)return;
      unschedule(holdTimer);holdTimer=null;
      if(Math.abs(dy)>Math.abs(dx)*1.25)pointer.kind=pointer.level;
      else if(Math.abs(dx)>Math.abs(dy)*1.25){
        if(!Number.isFinite(video.duration)||video.duration<=0){pointer.kind='cancelled';return;}
        pointer.kind='seek';
      }else{pointer.kind='pending';return;}
    }
    event.preventDefault();
    if(pointer.kind==='brightness')onBrightness(gestureLevelTarget(pointer.brightness,dy,pointer.height,0.2,1.5));
    else if(pointer.kind==='volume'){
      const result=applyGestureVolume(video,gestureLevelTarget(pointer.volume,dy,pointer.height));
      onVolume(result.value,result.supported);
    }else{
      pointer.target=gestureSeekTarget(pointer.start,dx,pointer.width,video.duration);
      onPreview(pointer.target,pointer.target-pointer.start);
    }
    onActivity();
  });
  listen('pointerup',event=>{if(pointer&&event.pointerId===pointer.id)finish(true);});
  listen('pointercancel',event=>{if(pointer&&event.pointerId===pointer.id)finish();});
  listen('lostpointercapture',event=>{if(pointer&&event.pointerId===pointer.id)finish();});
  listen('click',event=>{
    if(now()<suppressUntil){event.preventDefault();event.stopPropagation();return;}
    onTap(false);
  });
  listen('contextmenu',event=>{if(pointer||now()-lastTouch<1500)event.preventDefault();});
  signal.addEventListener('abort',()=>{unschedule(holdTimer);restoreRate();pointer=null;onFinish();},{once:true});
  return {active:()=>!!pointer,cancel:()=>finish()};
}
