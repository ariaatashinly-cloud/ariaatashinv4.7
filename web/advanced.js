"use strict";
let advancedFilter='all', networkHistory=[], telegramDraft=false;
icons.send='<path d="m22 2-7 20-4-9-9-4L22 2ZM11 13l11-11"/>';
icons.server='<rect x="3" y="3" width="18" height="7" rx="2"/><rect x="3" y="14" width="18" height="7" rx="2"/><path d="M7 6.5h.01M7 17.5h.01M12 6.5h5M12 17.5h5"/>';
paintIcons();
function profileHint(p){
 if(p.available===false)return p.reason;
 if(p.manual)return 'ساخت دستی · '+p.path+(p.endpoint?.address?' · '+p.endpoint.address:'');
 if(!p.direct)return descriptions[p.key]||'ورودی HTTP پشت دامنهٔ عمومی.';
 const hints={'vless-reality':'کلید X25519 پایدار، Short ID و Vision خودکار. بدون گواهی محلی TLS.',
 'vless-xhttp-reality':'XHTTP همراه REALITY؛ به نسخهٔ سازگار Xray در کلاینت نیاز دارد.',
 'vless-tls':'اتصال مستقیم TLS با Vision و گواهی معتبر سرور.',
 'tuic':'TUIC نسخهٔ ۵، UDP مستقیم، TLS معتبر و کنترل ازدحام BBR.',
 'hysteria2':'Hysteria نسخهٔ ۲، QUIC و UDP مستقیم؛ رمز Auth مخصوص کاربر.',
 'shadowsocks':'رمز کاربر مستقل؛ روش chacha20-ietf-poly1305 و TCP/UDP.',
 'vless-grpc':'انتقال gRPC با سرویس اختصاصی و TLS مستقیم.',
 'trojan-grpc':'Trojan روی gRPC با TLS معتبر و رمز کاربر.',
 'trojan-tls':'Trojan با TLS مستقیم و رمز مستقل.',
 'vmess-tls':'VMess روی TLS مستقیم؛ UUID کاربر خودکار.'};
 return hints[p.key]||'پورت مستقیم VPS با لینک آماده.';
}
function renderProfileCatalog(s){
 const list=s.profiles.filter(p=>advancedFilter==='all'||(advancedFilter==='cloud'&&!p.direct)||(advancedFilter==='vps'&&p.direct)||(advancedFilter==='available'&&p.available!==false));
 $('#profiles-grid').innerHTML=list.map((p,i)=>'<article class="panel profile-card '+(p.available===false?'locked-profile':'')+'"><div class="profile-top"><span class="profile-icon">'+svg(profileIcons[p.key]||'layers')+'</span><span class="mini-label">'+(p.manual?'MANUAL / HTTP':p.direct?'VPS / DIRECT':'CLOUD / HTTP')+'</span></div><h2 dir="ltr">'+esc(p.name)+'</h2><p>'+esc(profileHint(p))+'</p><div class="profile-specs"><div><span>نوع انتقال</span><b dir="ltr">'+esc(p.network.toUpperCase())+'</b></div><div><span>امنیت</span><b dir="ltr">'+esc(p.direct?p.security.toUpperCase():'TLS EDGE')+'</b></div><div><span>'+(p.direct?'پورت مستقیم':'پورت داخلی')+'</span><b dir="ltr">'+(p.direct||p.ready?p.internalPort:'Auto')+(p.udp?' / UDP':'')+'</b></div><div><span>وضعیت</span><b class="orange">'+(p.available===false?'نیازمند VPS / TLS':p.ready?'آماده':'ساخت خودکار')+'</b></div></div><button class="button secondary wide" data-create-profile="'+esc(p.key)+'" '+(p.available===false?'disabled':'')+'>'+svg(p.available===false?'lock':'plus')+(p.available===false?'در این حالت فعال نیست':'ساخت با این الگو')+'</button></article>').join('');
 $$('.profile-filters button').forEach(b=>b.classList.toggle('active',b.dataset.profileFilter===advancedFilter));
}
window.prepareChoices=function(profile){
 if(!state)return;
 $('#connection-choices').innerHTML='<legend>نوع اتصال <span>چند گزینه را هم‌زمان انتخاب کن.</span></legend><button type="button" class="button secondary wide" data-select-cloud>انتخاب همهٔ ۸ نوع وب</button>'+state.profiles.map(p=>'<label class="choice '+(p.key===profile?'selected':'')+' '+(p.available===false?'unavailable':'')+'"><input type="checkbox" name="profiles" value="'+esc(p.key)+'" '+(p.key===profile?'checked':'')+' '+(p.available===false?'disabled':'')+'><span class="choice-icon">'+svg(profileIcons[p.key]||'layers')+'</span><span><b dir="ltr">'+esc(p.name)+'</b><small>'+esc(p.available===false?p.reason:p.direct?'پورت مستقیم '+p.internalPort:'ساخت خودکار؛ TLS روی دامنه')+'</small></span><i></i></label>').join('');
};
function gauge(label,percent,detail){
 const value=Math.min(100,Math.max(0,Number(percent)||0)),dash=251.3,offset=dash*(1-value/100);
 return '<div class="resource-gauge"><svg viewBox="0 0 100 100" aria-label="'+esc(label)+'"><circle cx="50" cy="50" r="40" fill="none" stroke="#2c252b" stroke-width="6"/><circle class="gauge-arc" cx="50" cy="50" r="40" fill="none" stroke-width="6" stroke-dasharray="'+dash+'" stroke-dashoffset="'+offset+'" transform="rotate(-90 50 50)" stroke-linecap="round"/><text x="50" y="55" text-anchor="middle">'+Math.round(value)+'%</text></svg><b>'+esc(label)+'</b><small dir="auto">'+esc(detail)+'</small></div>';
}
function renderTelemetry(m){
 if(!m||!m.available){$('#resource-note').textContent='اطلاعات منابع فعلاً در دسترس نیست.';return}
 $('#resource-gauges').innerHTML=gauge('CPU',m.cpu,'مصرف پردازنده')+gauge('RAM',m.memoryTotal?m.memoryUsed/m.memoryTotal*100:0,bytes(m.memoryUsed)+' / '+bytes(m.memoryTotal))+gauge('دیسک',m.diskTotal?m.diskUsed/m.diskTotal*100:0,bytes(m.diskUsed)+' / '+bytes(m.diskTotal));
 $('#resource-note').textContent='منابع محیط اجرا · زمان روشن‌بودن سیستم '+fa(Math.floor(m.uptime/3600))+' ساعت';
 $('#network-down').textContent='↓ '+bytes(m.downBPS)+' / s';$('#network-up').textContent='↑ '+bytes(m.upBPS)+' / s';
 if(!networkHistory.length||networkHistory.at(-1).timestamp!==m.timestamp){networkHistory.push(m);if(networkHistory.length>32)networkHistory.shift()}
 if(networkHistory.length<2){$('#network-chart').innerHTML='<p class="chart-wait">برای رسم نمودار، منتظر نمونهٔ بعدی هستیم…</p>';return}
 const max=Math.max(1024,...networkHistory.flatMap(x=>[x.downBPS,x.upBPS]));
 const path=key=>networkHistory.map((m,i)=>(i?'L':'M')+(16+i*608/(networkHistory.length-1)).toFixed(1)+' '+(147-m[key]/max*117).toFixed(1)).join(' ');
 $('#network-chart').innerHTML='<svg viewBox="0 0 640 170" role="img" aria-label="نمودار واقعی دریافت و ارسال شبکه"><path class="chart-grid" d="M16 30H624M16 69H624M16 108H624M16 147H624"/><path class="chart-down" d="'+path('downBPS')+'"/><path class="chart-up" d="'+path('upBPS')+'"/><text class="chart-label" x="16" y="163">'+(networkHistory.length*15)+'s</text><text class="chart-label" x="598" y="163">NOW</text></svg>';
}
window.renderAdvanced=function(s){
 renderProfileCatalog(s);renderTelemetry(s.metrics);
 const h=s.hosting||{},selected=h.selected||'auto';
 $('#hosting-mode').textContent=h.mode==='vps'?'VPS · DIRECT ACCESS':'CLOUD · HTTP EDGE';
 $('#hosting-cards').innerHTML=(h.presets||[]).map(p=>'<button class="hosting-card '+(selected===p.key?'selected':'')+'" data-host-preset="'+esc(p.key)+'" '+(p.key==='vps'&&h.mode!=='vps'?'disabled':'')+'><span class="hosting-logo">'+(p.key==='auto'?'✦':p.name.charAt(0))+'</span><b>'+esc(p.name)+'</b><p>'+esc(p.description)+'</p><small>'+(selected===p.key?'انتخاب شده':p.key==='vps'&&h.mode!=='vps'?'روی سرور مجازی فعال می‌شود':'انتخاب پروفایل')+'</small></button>').join('');
 const guide=(h.presets||[]).find(x=>x.key===selected)||(h.presets||[])[0];
 if(guide){$('#hosting-guide-title').textContent='راه‌اندازی '+guide.name;$('#hosting-steps').innerHTML=guide.steps.map(x=>'<li>'+esc(x)+'</li>').join('')}
 $('#hosting-detected').textContent='تشخیص خودکار: '+(h.detected||'auto');
 const v=s.vps||{},f=$('#vps-form'),focus=document.activeElement;
 ['serverName','portBase','certFile','keyFile','realityTarget'].forEach(k=>{if(f.elements[k]!==focus)f.elements[k].value=v[k]??''});
 const disabled=v.mode!=='vps';$$('input,button',f).forEach(x=>x.disabled=disabled);
 $('#vps-mode-note').textContent=disabled?'این برنامه در حالت Cloud اجرا شده؛ برای پورت مستقیم از راهنمای سرور مجازی استفاده کن.':(v.certificateReady?'گواهی TLS معتبر است؛ تمام الگوها قابل ساخت‌اند.':v.certificateNote||'REALITY و Shadowsocks آماده‌اند؛ برای الگوهای TLS گواهی لازم است.');
 $('#vps-public-key').textContent=v.publicKey||'—';$('#vps-short-id').textContent=v.shortId||'—';
 const t=s.telegram||{},tf=$('#telegram-form');
 if(!telegramDraft){['enabled','notify'].forEach(k=>{if(tf.elements[k]!==focus)tf.elements[k].checked=!!t[k]});if(tf.elements.chatId!==focus)tf.elements.chatId.value=t.chatId||'';}
 $('#telegram-status').textContent=t.enabled?'ربات فعال · مدیر '+t.chatId+' · '+(t.mode||'poll'):(t.tokenConfigured?'توکن ذخیره شده؛ ربات غیرفعال است.':'توکن هنوز تنظیم نشده است.');
 if(window.renderBotStatus)window.renderBotStatus(t);
 document.body.classList.toggle('signed-in',!$('#app-screen').hidden);
};
document.addEventListener('change',e=>{if(e.target.matches('input[name="profiles"]'))e.target.closest('.choice').classList.toggle('selected',e.target.checked)});
document.addEventListener('click',async e=>{
 const b=e.target.closest('button');if(!b)return;
 if(b.dataset.profileFilter){advancedFilter=b.dataset.profileFilter;if(state)renderProfileCatalog(state)}
 if(b.dataset.hostPreset){
  try{await busy(b,async()=>{const s=state.settings;renderState(await api('/api/settings',{method:'POST',body:{publicURL:s.publicURL||window.location.origin,automaticDomain:s.automaticDomain,hostPreset:b.dataset.hostPreset}}))});toast('پروفایل میزبان و لینک‌ها هماهنگ شدند.')}catch(err){toast(err.message,true)}
 }
});
$('#vps-form').addEventListener('submit',async e=>{
 e.preventDefault();const f=e.currentTarget;try{await busy($('button',f),async()=>renderState(await api('/api/vps',{method:'POST',body:{serverName:f.elements.serverName.value,portBase:Number(f.elements.portBase.value),certFile:f.elements.certFile.value,keyFile:f.elements.keyFile.value,realityTarget:f.elements.realityTarget.value}})));toast('تنظیمات سرور مجازی ذخیره شد.')}catch(err){toast(err.message,true)}
});
$('#telegram-form').addEventListener('submit',async e=>{
 e.preventDefault();const f=e.currentTarget,b=e.submitter||$('button[type="submit"]',f),data={token:f.elements.token.value,chatId:Number(f.elements.chatId.value),enabled:f.elements.enabled.checked,notify:f.elements.notify.checked};try{await busy(b,async()=>{const next=await api('/api/telegram',{method:'POST',body:data});telegramDraft=false;f.elements.token.value='';renderState(next)});toast(data.enabled?'ربات فعال شد؛ در چت ربات /start بزن.':'اتصال ذخیره شد؛ دریافت پیام ربات خاموش است.')}catch(err){toast(err.message,true)}
});
for(const kind of ['input','change'])$('#telegram-form').addEventListener(kind,()=>{telegramDraft=true});
$('#telegram-test').addEventListener('click',async e=>{try{await busy(e.currentTarget,async()=>{const d=await api('/api/telegram/test',{method:'POST',body:{}});toast('پیام آزمایشی به چت خصوصی ارسال شد؛ @'+d.username)})}catch(err){toast(err.message,true)}});
const singButton=document.createElement('button');singButton.id='download-singbox';singButton.className='button secondary';singButton.textContent='خروجی sing-box';$('#download-links').after(singButton);
singButton.addEventListener('click',async e=>{if(!linkUser)return;try{await busy(e.currentTarget,async()=>{const d=await api('/api/client-export?email='+encodeURIComponent(linkUser.email));if(!d.singBox?.length)throw new Error('برای این اتصال از خروجی Xray استفاده کن.');download(JSON.stringify({outbounds:d.singBox},null,2),'ariaatashin-singbox-outbounds.json','application/json')});toast('قطعه‌های outbound دانلود شد؛ XHTTP و Shadowsocks وب را از خروجی Xray بگیر.')}catch(err){toast(err.message,true)}});
const originalShowLogin=showLogin;showLogin=function(){originalShowLogin();document.body.classList.remove('signed-in');networkHistory=[]};

$('#mobile-logout').addEventListener('click',()=>$('#logout').click());

for(const [id,mode] of [['bot-webhook','webhook'],['bot-poll','poll']])$('#'+id).addEventListener('click',async e=>{const button=e.currentTarget;if(!await confirmAction('تغییر اتصال ربات','اتصال webhook فعلی این ربات جایگزین می‌شود. این ربات را برای همین پنل اختصاص داده‌ای؟'))return;try{await busy(button,async()=>renderState(await api('/api/telegram/webhook',{method:'POST',body:{mode}})));toast('حالت اتصال ذخیره شد.')}catch(err){toast(err.message,true)}});
