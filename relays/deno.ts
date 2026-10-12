const ORIGIN = __ORIGIN__;
Deno.serve(async(req)=>{
 const input=new URL(req.url);
 if ((input.pathname!=="/live"&&!/^\/connect\/[A-Za-z0-9_-]+\//.test(input.pathname)&&!/^\/dns-query\/[a-f0-9]{48}$/.test(input.pathname))||/%2e|%2f|%5c/i.test(input.pathname)||input.pathname.includes('..'))return new Response('',{status:404});
 const target=new URL(ORIGIN);target.pathname=input.pathname;target.search=input.search;
 if(req.headers.get('upgrade')?.toLowerCase()==='websocket'&&input.pathname.startsWith('/connect/')){
  target.protocol='wss:';
  const protocols=(req.headers.get('sec-websocket-protocol')||'').split(',').map(v=>v.trim()).filter(Boolean);
  const remote=new WebSocket(target,protocols);remote.binaryType='arraybuffer';
  try{await new Promise((resolve,reject)=>{const timer=setTimeout(()=>{remote.close();reject(new Error('timeout'));},10000);remote.onopen=()=>{clearTimeout(timer);resolve(null);};remote.onerror=()=>{clearTimeout(timer);reject(new Error('upstream'));};});}catch{return new Response('',{status:502});}
  const {socket,response}=Deno.upgradeWebSocket(req,remote.protocol?{protocol:remote.protocol}:{});socket.binaryType='arraybuffer';
  const pending:(string|ArrayBuffer)[]=[];let size=0;
  const forward=(peer:WebSocket,data:string|ArrayBuffer)=>{if(peer.readyState===WebSocket.OPEN){if(peer.bufferedAmount>4*1024*1024){peer.close(1013);return;}peer.send(data);}};
  remote.onmessage=e=>{if(socket.readyState===WebSocket.OPEN)forward(socket,e.data);else{size+=typeof e.data==='string'?e.data.length:e.data.byteLength;if(size>4*1024*1024){remote.close(1013);return;}pending.push(e.data);}};
  socket.onopen=()=>{for(const data of pending)forward(socket,data);pending.length=0;};socket.onmessage=e=>forward(remote,e.data);
  socket.onclose=()=>remote.close();remote.onclose=()=>socket.close();socket.onerror=()=>remote.close();remote.onerror=()=>socket.close(1011);
  return response;
 }
 if(!['GET','POST','HEAD'].includes(req.method))return new Response('',{status:405});
 const headers=new Headers();for(const key of ['content-type','accept','user-agent']){const v=req.headers.get(key);if(v)headers.set(key,v);}
 headers.set('cache-control','no-store');
 try{const upstream=await fetch(target,{method:req.method,headers,body:req.method==='POST'?req.body:undefined,redirect:'manual'});const out=new Headers();out.set('cache-control','no-store, no-transform');const ct=upstream.headers.get('content-type');if(ct)out.set('content-type',ct);return new Response(upstream.body,{status:upstream.status,headers:out});}catch{return new Response('',{status:502});}
});
