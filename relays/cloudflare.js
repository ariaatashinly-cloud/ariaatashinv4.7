const ORIGIN=__ORIGIN__;
export default {async fetch(req){
 const input=new URL(req.url);
 if ((input.pathname!=='/live'&&!/^\/connect\/[A-Za-z0-9_-]+\//.test(input.pathname)&&!/^\/dns-query\/[a-f0-9]{48}$/.test(input.pathname))||/%2e|%2f|%5c/i.test(input.pathname)||input.pathname.includes('..'))return new Response('',{status:404});
 const target=new URL(ORIGIN);target.pathname=input.pathname;target.search=input.search;
 if(req.headers.get('upgrade')&&!input.pathname.startsWith('/connect/'))return new Response('',{status:404});
 if(!['GET','POST','HEAD'].includes(req.method))return new Response('',{status:405});
 const h=new Headers();for(const key of ['content-type','accept','user-agent','upgrade','sec-websocket-protocol','sec-websocket-key','sec-websocket-version']){const v=req.headers.get(key);if(v)h.set(key,v);}h.set('cache-control','no-store');
 try{const up=await fetch(target,{method:req.method,headers:h,body:req.method==='POST'?req.body:undefined,redirect:'manual'});if(up.status===101)return up;const out=new Headers();out.set('cache-control','no-store, no-transform');const ct=up.headers.get('content-type');if(ct)out.set('content-type',ct);return new Response(up.body,{status:up.status,headers:out});}catch{return new Response('',{status:502});}
}};
