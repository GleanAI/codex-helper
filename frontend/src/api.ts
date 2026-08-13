export async function api<T>(path:string,init:RequestInit={}):Promise<T>{const r=await fetch('/api/v1/'+path,{credentials:'same-origin',...init,headers:{'Content-Type':'application/json','X-Requested-With':'codex-helper',...(init.headers||{})}});const j=await r.json().catch(()=>({}));if(!r.ok)throw new Error(j.error||`HTTP ${r.status}`);return j}
export const post=<T>(p:string,v:unknown={})=>api<T>(p,{method:'POST',body:JSON.stringify(v)})
export const put=<T>(p:string,v:unknown)=>api<T>(p,{method:'PUT',body:JSON.stringify(v)})
export const del=<T>(p:string)=>api<T>(p,{method:'DELETE'})
