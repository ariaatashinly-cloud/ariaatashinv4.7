"use strict";

const $ = (s, root = document) => root.querySelector(s);
const $$ = (s, root = document) => [...root.querySelectorAll(s)];
const icons = {
  grid: '<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>',
  users: '<path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2m20 0v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75"/><circle cx="9" cy="7" r="4"/>',
  layers: '<path d="m12 3 10 6-10 6L2 9l10-6Zm-10 12 10 6 10-6M2 12l10 6 10-6"/>',
  pulse: '<path d="M2 12h4l3-8 6 16 3-8h4"/>',
  settings: '<path d="M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8Zm-2-5h4l.7 3 2.1 1.2 3-.8 2 3.5-2.3 2.1v2.4l2.3 2.1-2 3.5-3-.8-2.1 1.2-.7 3h-4l-.7-3L7.2 19l-3 .8-2-3.5 2.3-2.1v-2.4L2.2 9.7l2-3.5 3 .8L9.3 6l.7-3Z"/>',
  "arrow-left": '<path d="M20 12H4m6-6-6 6 6 6"/>', plus: '<path d="M12 5v14M5 12h14"/>',
  shield: '<path d="M12 3 3 7v6c0 5 9 9 9 9s9-4 9-9V7l-9-4Z"/><path d="m8 12 3 3 5-6"/>',
  lock: '<rect x="5" y="10" width="14" height="11" rx="2"/><path d="M8 10V7a4 4 0 0 1 8 0v3M12 14v3"/>',
  logout: '<path d="M9 21H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5m7 14 5-5-5-5m5 5H9"/>',
  refresh: '<path d="M20 7v5h-5M4 17v-5h5m-4-4a8 8 0 0 1 13-3l2 3M4 16l2 3a8 8 0 0 0 13-3"/>',
  "check-circle": '<circle cx="12" cy="12" r="9"/><path d="m8 12 3 3 5-6"/>',
  transfer: '<path d="M8 3v17m-4-4 4 4 4-4M16 21V4m-4 4 4-4 4 4"/>',
  clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
  alert: '<path d="m12 3 10 18H2L12 3ZM12 9v4m0 4h.01"/>', info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v6m0-10h.01"/>',
  search: '<circle cx="10" cy="10" r="7"/><path d="m15 15 6 6"/>', x: '<path d="m6 6 12 12M6 18 18 6"/>',
  zap: '<path d="m13 2-10 12h8l-1 8 11-13h-8l1-7Z"/>',
  sparkles: '<path d="m12 3 2.5 6.5L21 12l-6.5 2.5L12 21l-2.5-6.5L3 12l6.5-2.5L12 3Zm7-1v4m-2-2h4"/>',
  globe: '<circle cx="12" cy="12" r="9"/><ellipse cx="12" cy="12" rx="4" ry="9"/><path d="M3 12h18"/>',
  download: '<path d="M12 3v12m-5-5 5 5 5-5M3 15v5a1 1 0 0 0 1 1h16a1 1 0 0 0 1-1v-5"/>',
  upload: '<path d="M12 15V3m-5 5 5-5 5 5M3 15v5a1 1 0 0 0 1 1h16a1 1 0 0 0 1-1v-5"/>',
  copy: '<rect x="8" y="8" width="13" height="13" rx="2"/><path d="M16 8V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h3"/>',
  edit: '<path d="m15 5 4 4M3 21l5-1L20 8a3 3 0 0 0-4-4L4 16l-1 5Z"/>',
  trash: '<path d="M3 6h18M9 6V3h6v3M5 6l1 15h12l1-15M10 10v7m4-7v7"/>',
  key: '<circle cx="7" cy="8" r="4"/><path d="m10 11 10 10m-4-4 3-3m-7-1 3-3"/>',
  link: '<path d="M10 14a5 5 0 0 0 7 0l3-3a5 5 0 0 0-7-7l-2 2m2 4a5 5 0 0 0-7 0l-3 3a5 5 0 0 0 7 7l2-2"/>',
  power: '<path d="M12 2v10M6 5a9 9 0 1 0 12 0"/>'
};
const svg = key => `<svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.65" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${icons[key] || icons.zap}</svg>`;
function paintIcons(root = document) { $$('[data-icon]', root).forEach(el => { el.innerHTML = svg(el.dataset.icon); }); }
function esc(value) { return String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])); }
const fa = n => new Intl.NumberFormat('fa-IR').format(n);
const bytes = n => { if (!n) return '۰ B'; const unit = Math.min(4, Math.floor(Math.log(n) / Math.log(1024))); return `${new Intl.NumberFormat('fa-IR',{maximumFractionDigits:unit<2?0:2}).format(n/1024**unit)} ${['B','KB','MB','GB','TB'][unit]}`; };
const statuses = {active:'فعال', disabled:'غیرفعال', expired:'منقضی', limited:'اتمام حجم'};
const descriptions = {'vless-ws':'انتخاب ساده و سازگار برای شروع. لینک کامل با Host، SNI و ALPN مناسب WebSocket.', 'vless-xhttp':'انتقال جدید Xray. نیازمند نسخهٔ جدید کلاینت و پشتیبانی واقعی میزبان از XHTTP.', 'vmess-ws':'برای کلاینت‌های سازگار با VMess؛ شناسهٔ مستقل و لینک آمادهٔ واردکردن.', 'trojan-ws':'اتصال با رمز اختصاصی؛ WebSocket داخلی و TLS روی دامنهٔ عمومی.','vmess-xhttp':'VMess روی XHTTP؛ برای واردکردن دقیق تنظیمات از Xray JSON استفاده کن.','trojan-xhttp':'رمز مستقل Trojan روی XHTTP؛ کلاینت جدید Xray لازم است.','shadowsocks-ws':'Shadowsocks روی WebSocket؛ TCP و TLS عمومی. خروجی کامل Xray همین اتصال را وارد کن.','shadowsocks-xhttp':'Shadowsocks روی XHTTP؛ TCP و TLS عمومی. خروجی کامل Xray و میزبان سازگار لازم است.'};
const profileIcons = {'vless-ws':'zap','vless-xhttp':'pulse','vmess-ws':'layers','trojan-ws':'shield','vmess-xhttp':'layers','trojan-xhttp':'shield','shadowsocks-ws':'lock','shadowsocks-xhttp':'lock'};
let state = null, csrf = '', currentView = 'dashboard', editing = null, linkUser = null, selectedLink = '', loading = false, toastTimer;

async function api(path, options = {}) {
  const method = options.method || 'GET';
  const headers = {'Accept':'application/json','X-Requested-With':'ariaatashin','X-ARIA-Origin':window.location.origin};
  if (method !== 'GET') { headers['Content-Type'] = 'application/json'; if (csrf) headers['X-ARIA-CSRF'] = csrf; }
  const res = await fetch(path,{method,headers,body:options.body === undefined ? undefined : JSON.stringify(options.body),credentials:'same-origin',cache:'no-store',signal:AbortSignal.timeout(45000)});
  const data = await res.json().catch(() => ({}));
  if (!res.ok) { if (res.status===401 && path!=='/api/login') showLogin(); const e = new Error(data.error || `خطای سرویس (${res.status})`); e.status=res.status;e.code=data.code||''; throw e; }
  return data;
}
function toast(message, bad=false) {
  clearTimeout(toastTimer); const region=$('#toast-region'); region.replaceChildren(); const el=document.createElement('div'); el.className=`toast ${bad?'error':''}`; el.innerHTML=svg(bad?'alert':'check-circle'); const span=document.createElement('span');span.textContent=message;el.append(span);region.append(el);toastTimer=setTimeout(()=>el.remove(),5500);
}
const busyJobs=new WeakMap();
async function busy(button, job) { if(!button)throw new Error('فرم کامل بارگیری نشده؛ صفحه را تازه کن.');if(busyJobs.has(button))return busyJobs.get(button);const original=button.innerHTML;button.disabled=true;button.classList.add('busy');const running=(async()=>job())();busyJobs.set(button,running);try{return await running;}finally{busyJobs.delete(button);button.disabled=false;button.classList.remove('busy');button.innerHTML=original;} }
function showLogin() { csrf='';state=null;$('#app-screen').hidden=true;$('#login-screen').hidden=false;$$('dialog[open]').forEach(d=>d.close()); }
function showApp(username) { $('#login-screen').hidden=true;$('#app-screen').hidden=false;$('#admin-name').textContent=username;$('#admin-avatar').textContent=username.charAt(0).toUpperCase(); }
function showView(view) {
  if(view==='sales'&&window.openCommerce)window.openCommerce();
  if (!$('#view-'+view)) return; currentView=view;$$('.view').forEach(el=>el.hidden=el.id!==`view-${view}`);$$('.nav-item,.mobile-nav button').forEach(el=>el.classList.toggle('active',el.dataset.view===view));
  const item=$(`.nav-item[data-view="${view}"]`);$('#breadcrumb-current').textContent=item?.children[1]?.textContent||'';window.scrollTo({top:0,behavior:'instant'});
}
function badge(status) { return `<span class="status-badge ${esc(status)}"><i></i>${statuses[status]||'نامشخص'}</span>`; }
function expiryText(u) { return u.expiry ? new Date(u.expiry).toLocaleDateString('fa-IR') : 'بدون انقضا'; }
function userRows(users, compact=false) {
  if (!users.length) return `<div class="empty-state">${svg('users')}<h3>${state?.users?.length?'نتیجه‌ای پیدا نشد.':'اولین شعله را روشن کن.'}</h3><p>${state?.users?.length?'جست‌وجو یا فیلتر را تغییر بده.':'یک کاربر بساز؛ لینک و QR همین‌جا آماده می‌شود.'}</p>${state?.users?.length?'':'<button class="button secondary create-trigger">ساخت اولین کاربر</button>'}</div>`;
  return `<div class="table-scroll"><table><thead><tr><th>کاربر</th><th>وضعیت</th><th>مصرف / سهمیه</th>${compact?'':'<th>اعتبار</th>'}<th>مدیریت</th></tr></thead><tbody>${users.map(u=>{
    const usage=u.up+u.down, percentage=u.quota?Math.min(100,usage/u.quota*100):0;
    return `<tr><td><div class="user-cell"><span class="user-avatar">${esc(u.name.slice(0,1))}</span><div><b>${esc(u.name)}</b><small>${fa(u.links.length)} نوع اتصال</small></div></div></td><td>${badge(u.status)}</td><td><div class="traffic-cell"><span dir="ltr">${bytes(usage)} <i>/</i> ${u.quota?bytes(u.quota):'∞'}</span><progress max="100" value="${percentage}" aria-label="مصرف سهمیه"></progress></div></td>${compact?'':`<td><span class="expiry-value">${expiryText(u)}</span></td>`}<td><div class="row-actions"><button class="button link-button" data-user="${esc(u.email)}" data-action="links">${svg('link')} لینک‌ها</button>${compact?'':`<button class="icon-button" title="ویرایش حجم و اعتبار" aria-label="ویرایش ${esc(u.name)}" data-user="${esc(u.email)}" data-action="edit">${svg('edit')}</button><button class="icon-button ${u.enabled?'':'orange'}" title="${u.enabled?'غیرفعال کردن':'فعال کردن'}" aria-label="تغییر وضعیت ${esc(u.name)}" data-user="${esc(u.email)}" data-action="toggle">${svg('power')}</button><button class="icon-button" title="تعویض کلیدها" aria-label="تعویض کلیدها" data-user="${esc(u.email)}" data-action="rotate">${svg('key')}</button><button class="icon-button" title="ریست مصرف" aria-label="ریست مصرف" data-user="${esc(u.email)}" data-action="reset">${svg('refresh')}</button><button class="icon-button danger" title="حذف کاربر" aria-label="حذف ${esc(u.name)}" data-user="${esc(u.email)}" data-action="delete">${svg('trash')}</button>`}</div></td></tr>`;
  }).join('')}</tbody></table></div>`;
}
function renderUsers() { if(!state)return; const search=$('#user-search').value.trim().toLocaleLowerCase(), filter=$('#status-filter').value;const users=state.users.filter(u=>(filter==='all'||u.status===filter)&&(u.name.toLocaleLowerCase().includes(search)||u.email.includes(search)));$('#all-users').innerHTML=userRows(users);$('#filter-count').textContent=`${fa(users.length)} کاربر`;$('#recent-users').innerHTML=userRows(state.users.slice(-5).reverse(),true);$('#recent-count').textContent=fa(Math.min(5,state.users.length)); }
function renderState(s) {
  state=s;s.users??=[];s.profiles??=[];s.audit??=[];
  if($('#core-warning')){$('#core-warning').hidden=!!s.ready;$('#core-message').textContent=s.core?.lastError||'داده‌ها محفوظ‌اند؛ بازیابی خودکار ادامه دارد.'}
  const xr=s.engine?.xray?.state;const engineOK=s.ready&&xr==='running';const pill=$('#engine-pill');pill.classList.toggle('offline',!engineOK);$('span',pill).textContent=engineOK?'هسته آماده است':(s.ready&&!xr?'در حال بررسی Xray':'هسته نیازمند بررسی است');
  const publicURL=new URL(s.settings.publicURL||window.location.origin);$('#hero-domain').textContent=publicURL.host;$('.lock-label', $('.hero-card')).innerHTML=svg('lock')+(publicURL.protocol==='https:'?' TLS AT THE EDGE':' HTTP / NO TLS');
  $('#storage-warning').hidden=!!s.persistent;$('#storage-status').textContent=s.persistent?'Volume داده؛ حفظ شود':'موقت؛ بکاپ ضروری';
  $('#stat-users').textContent=fa(s.users.length);$('#nav-user-count').textContent=fa(s.users.length);$('#stat-configs').textContent=`${fa(s.users.reduce((n,u)=>n+u.links.length,0))} لینک آمادهٔ اتصال`;
  $('#stat-active').textContent=fa(s.users.filter(u=>u.status==='active').length);$('#stat-expired').textContent=fa(s.users.filter(u=>u.status!=='active').length);$('#stat-traffic').textContent=bytes(s.users.reduce((n,u)=>n+u.up+u.down,0));
  $('#last-synced').textContent=s.syncedAt?`همگام‌سازی ${new Date(s.syncedAt).toLocaleTimeString('fa-IR',{hour:'2-digit',minute:'2-digit'})}`:'در انتظار هسته';
  if(document.activeElement!==$('#public-url'))$('#public-url').value=s.settings.publicURL;
  if(document.activeElement!==$('#automatic-domain'))$('#automatic-domain').checked=!!s.settings.automaticDomain;
  $('#public-url').readOnly=$('#automatic-domain').checked;
  $('#domain-mode-note').textContent=s.settings.automaticDomain?'آدرس همین مرورگر خودکار انتخاب شده است؛ لینک‌ها آماده‌اند.':'حالت دستی؛ فقط برای انتخاب آدرس عمومی متفاوت.';
  $('#diag-public-port').textContent=publicURL.port||(publicURL.protocol==='https:'?'443':'80');
  $('#activity-list').innerHTML=s.audit.length?s.audit.slice(0,5).map(a=>`<div class="activity-item"><span class="activity-icon">${svg(a.kind==='create'?'plus':a.kind==='restore'?'upload':'settings')}</span><div><b>${esc(a.message)}</b><small>${new Date(a.time).toLocaleString('fa-IR',{month:'short',day:'numeric',hour:'2-digit',minute:'2-digit'})}</small></div></div>`).join(''):'<div class="empty-activity">فعلاً رویدادی ثبت نشده است.<br>اولین اتصال، شروع داستان توست.</div>';
  $('#dashboard-profiles').innerHTML=s.profiles.filter(p=>!p.direct && p.available!==false).map(p=>`<button class="profile-mini" data-create-profile="${esc(p.key)}"><span class="profile-icon">${svg(profileIcons[p.key]||'layers')}</span><span><b dir="ltr">${esc(p.name)}</b><small>${p.ready?'اینباند آماده':'ساخت خودکار در اولین استفاده'}</small></span>${svg('arrow-left')}</button>`).join('');
  $('#profiles-grid').innerHTML=s.profiles.map((p,i)=>`<article class="panel profile-card"><div class="profile-top"><span class="profile-icon">${svg(profileIcons[p.key]||'layers')}</span><span class="profile-index">0${i+1}</span></div><h2 dir="ltr">${esc(p.name)}</h2><p>${esc(descriptions[p.key]||p.reason||'پورت مستقیم، هویت مستقل و لینک خودکار برای سرور مجازی.')}</p><div class="profile-specs"><div><span>نوع انتقال</span><b dir="ltr">${p.network.toUpperCase()}</b></div><div><span>امنیت عمومی</span><b>${publicURL.protocol==='https:'?'TLS':'بدون TLS'}</b></div><div><span>پورت داخلی</span><b dir="ltr">${p.ready?p.internalPort:'Auto'}</b></div><div><span>وضعیت</span><b class="${p.ready?'orange':'muted'}">${p.ready?'آماده':'هنوز ساخته نشده'}</b></div></div><button class="button secondary wide" data-create-profile="${esc(p.key)}">ساخت با این الگو ${svg('plus')}</button></article>`).join('');
  if(window.renderAdvanced) window.renderAdvanced(s); if(window.renderManual) window.renderManual(s); renderUsers();if(linkUser){const u=s.users.find(u=>u.email===linkUser.email);if(u){linkUser=u;renderLink();}else{$('#links-dialog').close();linkUser=null;}}
}
async function refresh() { if(loading||!csrf)return;loading=true;try{renderState(await api('/api/state'));}catch(e){if(e.status!==401)toast(e.message,true);}finally{loading=false;} }
async function openCreate(profile='vless-ws') {
  if(!state?.ready||Date.now()-(state?.syncedAt||0)>45000){try{renderState(await api('/api/core/reconnect',{method:'POST',body:{}}));}catch(e){toast(e.message,true);return;}}if(window.prepareChoices)window.prepareChoices(profile); editing=null;const f=$('#create-form');f.reset();f.elements.quotaGB.step='0.1';$('#create-title').textContent='یک اتصال تازه بساز.';$('#create-submit').innerHTML=svg('plus')+' بساز و آماده کن';$('#create-error').textContent='';$('#count-label').hidden=false;$('#connection-choices').hidden=false;
  $$('input[name="profiles"]',f).forEach(i=>{i.checked=i.value===profile;i.closest('.choice').classList.toggle('selected',i.checked);});if(window.setCreateMode)window.setCreateMode(false);$('#create-dialog').showModal();setTimeout(()=>f.elements.name.focus(),50);
}
function openEdit(u) { editing=u.email;const f=$('#create-form');f.reset();f.elements.name.value=u.name;f.elements.quotaGB.value=(u.quota/1024**3).toFixed(2);f.elements.quotaGB.step='0.01';f.elements.days.value=u.expiry?Math.max(1,Math.ceil((u.expiry-Date.now())/86400000)):0;$('#create-title').textContent='ویرایش سهمیه و اعتبار';$('#create-submit').innerHTML=svg('check-circle')+' ذخیرهٔ تغییرات';$('#create-error').textContent='';$('#count-label').hidden=true;$('#connection-choices').hidden=true;if(window.hideManualCreate)window.hideManualCreate();$('#create-dialog').showModal(); }
function openLinks(u) {linkUser=u;selectedLink=u.links[0]?.profile||'subscription';$('#links-title').textContent=`اتصال‌های ${u.name}`;renderLink();$('#links-dialog').showModal();}
function renderLink() {
  const u=linkUser;if(!u)return;$('#link-user-summary').innerHTML=badge(u.status)+`<span>${u.quota?bytes(u.quota):'نامحدود'} · ${expiryText(u)}</span>`;
  const list=[...u.links,{profile:'subscription',name:'اشتراک همهٔ لینک‌ها',link:u.subscription}].filter(l=>l.link);if(!list.some(l=>l.profile===selectedLink))selectedLink=list[0]?.profile;
  $('#link-tabs').innerHTML=list.map(l=>`<button class="link-tab ${l.profile===selectedLink?'active':''}" data-link-profile="${esc(l.profile)}">${esc(l.name)}</button>`).join('');
  const link=list.find(l=>l.profile===selectedLink)?.link||'';$('#current-link').value=link;const holder=$('#qr-holder');holder.replaceChildren();
  try{if(!window.qrcode)throw new Error('QR unavailable');const qr=qrcode(0,'M');qr.addData(link);qr.make();holder.innerHTML=qr.createSvgTag({cellSize:4,margin:16,scalable:true});$('svg',holder).setAttribute('role','img');$('svg',holder).setAttribute('aria-label','QR اتصال خصوصی');}catch{holder.textContent='ساخت QR ممکن نشد؛ لینک را کپی کن.';}
  const profile=state?.profiles.find(p=>p.key===selectedLink);
  const needsJSON=profile&&!profile.direct&&(profile.protocol==='shadowsocks'||(profile.protocol==='vmess'&&profile.network==='xhttp'));
  $('#client-format-note').hidden=!needsJSON;
  $('#client-format-note').textContent=needsJSON?'برای حفظ نوع انتقال، «Xray همین اتصال» را دانلود و به‌عنوان Custom config وارد کلاینت Xray کن. بعضی کلاینت‌ها لینک یا QR این نوع را کامل وارد نمی‌کنند.':'';
  $('.qr-caption').textContent=selectedLink==='subscription'?'این لینک را به‌عنوان Subscription وارد کلاینت کن.':'این QR را با کلاینت گوشی اسکن کن.';
}
function confirmAction(title,message) { const dialog=$('#confirm-dialog');$('#confirm-title').textContent=title;$('#confirm-message').textContent=message;return new Promise(resolve=>{let result=false;const yes=()=>{result=true;dialog.close();};const no=()=>dialog.close();const closed=()=>{$('#confirm-yes').removeEventListener('click',yes);$('#confirm-cancel').removeEventListener('click',no);dialog.removeEventListener('close',closed);resolve(result);};$('#confirm-yes').addEventListener('click',yes);$('#confirm-cancel').addEventListener('click',no);dialog.addEventListener('close',closed);dialog.showModal();}); }
async function userAction(button) {
  const u=state?.users.find(u=>u.email===button.dataset.user);if(!u)return;const action=button.dataset.action;
  if(action==='links'){openLinks(u);return;}if(action==='edit'){openEdit(u);return;}
  const messages={delete:['حذف کاربر',`«${u.name}» و همهٔ اتصال‌هایش حذف شود؟ این عملیات برگشت‌پذیر نیست.`],rotate:['تعویض کلید اتصال','همهٔ لینک‌ها و اشتراک قبلی این کاربر باطل می‌شود. لینک‌های جدید را باید دوباره به صاحب حساب بدهی.'],reset:['ریست مصرف','مصرف ثبت‌شده صفر می‌شود و سهمیه دوباره از ابتدا محاسبه می‌شود.']};
  if(messages[action]&&!await confirmAction(...messages[action]))return;
  try{await busy(button,async()=>renderState(await api(`/api/users/${encodeURIComponent(u.email)}/${action}`,{method:'POST',body:{}})));toast('تغییرات کاربر ذخیره شد.');}catch(e){toast(e.message,true);}
}
async function copy(value) { try{await navigator.clipboard.writeText(value);toast('لینک کپی شد.');}catch{const input=$('#current-link');input.focus();input.select();toast('لینک انتخاب شد؛ از گزینهٔ کپی گوشی استفاده کن.');} }
function download(content,name,type='text/plain;charset=utf-8') {const a=document.createElement('a'),url=URL.createObjectURL(new Blob([content],{type}));a.href=url;a.download=name;document.body.append(a);a.click();a.remove();setTimeout(()=>URL.revokeObjectURL(url),10000);}

document.addEventListener('click',event=>{
  const button=event.target.closest('button');if(!button)return;
  if(button.dataset.view)showView(button.dataset.view);
  if(button.dataset.goto)showView(button.dataset.goto);
  if(button.classList.contains('create-trigger')||button.id==='sidebar-create')openCreate();
  if(button.dataset.createProfile)openCreate(button.dataset.createProfile);
  if(button.dataset.action)userAction(button);
  if(button.dataset.linkProfile){selectedLink=button.dataset.linkProfile;renderLink();}
  if(button.classList.contains('close-dialog'))button.closest('dialog').close();
});
$('#login-form').addEventListener('submit',async event=>{event.preventDefault();const f=event.currentTarget;$('#login-error').textContent='';try{await busy($('button[type="submit"]',f),async()=>{const data=await api('/api/login',{method:'POST',body:{username:f.elements.username.value,password:f.elements.password.value,otp:f.elements.otp.value}});csrf=data.csrf;f.elements.password.value='';f.elements.otp.value='';showApp(data.username);await refresh();});}catch(e){$('#login-error').textContent=e.message;}});
$('#logout').addEventListener('click',async()=>{try{await api('/api/logout',{method:'POST',body:{}});}catch{}showLogin();});
$('#refresh').addEventListener('click',async event=>{await busy(event.currentTarget,refresh);toast('وضعیت تازه شد.');});
$('#user-search').addEventListener('input',renderUsers);$('#status-filter').addEventListener('change',renderUsers);
$$('input[name="profiles"]').forEach(i=>i.addEventListener('change',()=>i.closest('.choice').classList.toggle('selected',i.checked)));
$('#create-form').addEventListener('submit',async event=>{
  event.preventDefault();const f=event.currentTarget;$('#create-error').textContent='';const body={name:f.elements.name.value,quotaGB:Number(f.elements.quotaGB.value),days:Number(f.elements.days.value)};
  const manual=!editing&&window.manualCreateBody?window.manualCreateBody():null;if(manual){body.count=1;body.manual=manual;}else if(!editing){body.count=Number(f.elements.count.value);body.profiles=$$('input[name="profiles"]:checked',f).map(i=>i.value);if(!body.profiles.length){$('#create-error').textContent='حداقل یک نوع اتصال انتخاب کن.';return;}}
  try{await busy($('#create-submit'),async()=>{const data=await api(editing?`/api/users/${encodeURIComponent(editing)}/edit`:'/api/users',{method:'POST',body});const updated=editing?data:data.state;renderState(updated);$('#create-dialog').close();if(data.warning)toast(data.warning,true);else toast(editing?'سهمیه و اعتبار ذخیره شد.':`${fa(data.created.length)} کاربر و لینک‌هایشان ساخته شد.`);if(!editing&&data.created.length===1){const u=updated.users.find(u=>u.email===data.created[0]);if(u)openLinks(u);}});}catch(e){$('#create-error').textContent=e.message;}
});
$('#automatic-domain').addEventListener('change',event=>{const automatic=event.currentTarget.checked;$('#public-url').readOnly=automatic;if(automatic)$('#public-url').value=window.location.origin;$('#domain-mode-note').textContent=automatic?'آدرس همین مرورگر خودکار انتخاب می‌شود.':'آدرس دلخواه را بنویس و ذخیره کن.';});
$('#settings-form').addEventListener('submit',async event=>{event.preventDefault();try{await busy($('button',event.currentTarget),async()=>renderState(await api('/api/settings',{method:'POST',body:{publicURL:$('#public-url').value,automaticDomain:$('#automatic-domain').checked,hostPreset:state?.settings.hostPreset||'auto'}})));toast('آدرس و همهٔ لینک‌ها هماهنگ شدند.');}catch(e){toast(e.message,true);}});
$('#copy-current-link').addEventListener('click',()=>copy($('#current-link').value));
$('#download-links').addEventListener('click',()=>{if(linkUser)download(linkUser.links.map(l=>l.link).join('\n')+'\n\n# Subscription\n'+linkUser.subscription,'ariaatashin-links.txt');});
$('#download-backup').addEventListener('click',async event=>{try{await busy(event.currentTarget,async()=>{const b=await api('/api/backup');download(JSON.stringify(b,null,2),`ariaatashin-backup-${new Date().toISOString().slice(0,10)}.json`,'application/json');});toast('بکاپ دانلود شد؛ در جای خصوصی نگه‌دار.');}catch(e){toast(e.message,true);}});
$('#restore-backup').addEventListener('click',()=>$('#backup-file').click());
$('#backup-file').addEventListener('change',async event=>{const file=event.target.files?.[0];event.target.value='';if(!file)return;if(file.size>4*1024*1024){toast('فایل بکاپ بیش از ۴ مگابایت است.',true);return;}try{const body=JSON.parse(await file.text());if(!await confirmAction('بازیابی بکاپ','کاربران جدید اضافه می‌شوند؛ کاربران فعلی حذف یا بازنویسی نمی‌شوند. دامنهٔ عمومی همین پنل حفظ می‌شود؛ تنظیمات آی‌پی دستی ذخیره‌شده هم منتقل می‌شوند. پس از انتقال، آی‌پی را بررسی کن.'))return;await busy($('#restore-backup'),async()=>{const data=await api('/api/restore',{method:'POST',body});renderState(data.state);toast(data.warning||`${fa(data.restored)} کاربر بازیابی شد.`,!!data.warning);});}catch(e){toast(e.message==='Unexpected end of JSON input'?'فایل بکاپ معتبر نیست.':e.message,true);}});
$('#run-diagnostics').addEventListener('click',async event=>{try{await busy(event.currentTarget,async()=>{const data=await api('/api/diagnostics');$('#diagnostic-results').innerHTML=`<div class="diagnostic-list">${data.checks.map(c=>`<div class="diagnostic-item"><span class="diag-icon ${c.ok?'ok':'warn'}">${svg(c.ok?'check-circle':'alert')}</span><div><b>${esc(c.label)}</b><small dir="auto">${esc(c.detail)}</small></div><span class="mini-label">${c.ok?'OK':'CHECK'}</span></div>`).join('')}</div><p class="diagnostic-note">${esc(data.note)}</p>`;});}catch(e){toast(e.message,true);}});
$$('dialog').forEach(d=>d.addEventListener('click',e=>{if(e.target===d){const r=d.getBoundingClientRect();if(e.clientX<r.left||e.clientX>r.right||e.clientY<r.top||e.clientY>r.bottom)d.close();}}));
paintIcons();
(async()=>{try{const s=await api('/api/session');csrf=s.csrf;showApp(s.username);await refresh();}catch(e){if(e.status!==401)$('#login-error').textContent='ارتباط با پنل برقرار نشد؛ دوباره صفحه را باز کن.';}})();
setInterval(()=>{if(!document.hidden)refresh();},15000);

document.addEventListener('visibilitychange',()=>{if(!document.hidden&&csrf)refresh()});
window.addEventListener('online',()=>{if(csrf)refresh()});
