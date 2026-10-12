import http from 'node:http';
import https from 'node:https';
import { WebSocket, WebSocketServer } from 'ws';
const ORIGIN = __ORIGIN__;
function target(raw) {
 const u = new URL(raw, 'http://relay.invalid');
 const routed = u.searchParams.get('aria_path');
 if (routed !== null) { u.pathname = '/' + routed; u.searchParams.delete('aria_path'); }
 if (u.pathname !== '/live' && !/^\/connect\/[A-Za-z0-9_-]+\//.test(u.pathname) && !/^\/dns-query\/[a-f0-9]{48}$/.test(u.pathname)) return null;
 if (/%2e|%2f|%5c/i.test(u.pathname) || u.pathname.includes('..')) return null;
 const t = new URL(ORIGIN); t.pathname = u.pathname; t.search = u.search; return t;
}
function headers(input) {
 const out = {};
 for (const [key,v] of Object.entries(input)) {
  if (['content-type','content-length','accept','user-agent'].includes(key.toLowerCase())) out[key]=v;
 }
 out['cache-control']='no-store'; return out;
}
const server = http.createServer((req,res) => {
 const u = target(req.url); if (!u || !['GET','POST','HEAD'].includes(req.method)) {res.writeHead(404); res.end(); return;}
 const proxy = https.request(u,{method:req.method,headers:headers(req.headers)},up => {
  const h=headers(up.headers);delete h['content-length'];h['cache-control']='no-store, no-transform';
  res.writeHead(up.statusCode,h);up.pipe(res);
 });
 proxy.on('error',()=>{if (!res.headersSent) res.writeHead(502); res.end();});
 proxy.setTimeout(290000,()=>proxy.destroy());req.on('aborted',()=>proxy.destroy());res.on('close',()=>proxy.destroy());req.pipe(proxy);
});
const wss = new WebSocketServer({noServer:true,perMessageDeflate:false,maxPayload:1024*1024});
server.on('upgrade',(req,socket,head) => {
 const t=target(req.url);if (!t || !t.pathname.startsWith('/connect/')) {socket.destroy();return;}
 t.protocol='wss:';
 const protocols=String(req.headers['sec-websocket-protocol']||'').split(',').map(v=>v.trim()).filter(Boolean);
 const remote=new WebSocket(t,protocols,{perMessageDeflate:false,maxPayload:1024*1024,handshakeTimeout:10000});
 let attached=false;
 remote.once('open',()=>{
  if(socket.destroyed){remote.close();return;}
  wss.handleUpgrade(req,socket,head,client=>{
   attached=true;
   const send=(dest,data,binary)=>{if(dest.readyState===WebSocket.OPEN){if(dest.bufferedAmount>4*1024*1024){dest.close(1013);return;}dest.send(data,{binary});}};
   client.on('message',(data,binary)=>send(remote,data,binary));remote.on('message',(data,binary)=>send(client,data,binary));
   client.on('error',()=>remote.close());remote.on('error',()=>client.close(1011));
   client.on('close',()=>remote.close());remote.on('close',()=>client.close());
  });
 });
 remote.on('error',()=>{if(!attached)socket.destroy();});remote.on('close',()=>{if(!attached)socket.destroy();});socket.on('error',()=>remote.close());
});
export default server;
