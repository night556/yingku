const form=document.querySelector('#login-form'),error=document.querySelector('#login-error');
form.addEventListener('submit',async event=>{
  event.preventDefault();const button=form.querySelector('button');button.disabled=true;error.textContent='';
  try {
    const response=await fetch('/api/session',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({password:new FormData(form).get('password')})});
    const result=await response.json();
    if(!response.ok)throw new Error(result.error||'暂时无法登录');
    location.replace('/');
  }catch(e){error.textContent=e.message;}
  finally{button.disabled=false;}
});
