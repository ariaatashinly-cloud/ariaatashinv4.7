"use strict";

function botReadinessChecks(t){
 return [['توکن ربات',!!t.tokenConfigured],['شناسهٔ مدیر',Number(t.chatId)>0],['دریافت پیام',!!t.enabled],['فروشگاه',!!t.shopEnabled],['پلن فروش',Number(t.activePlans)>0]];
}
window.renderBotStatus=function(t){
 const box=$('#telegram-readiness');if(!box)return;
 const h=t.health||{},checks=botReadinessChecks(t),configured=checks.every(([,ok])=>ok);
 const title=!t.enabled?'ربات هنوز پاسخ‌گویی را شروع نکرده است.':!t.shopEnabled?'ربات فعال است؛ فروشگاه خاموش است.':!t.activePlans?'برای خرید، یک پلن فعال بساز.':h.pollError?'دریافت پیام نیاز به بررسی دارد.':'خرید مستقیم در تلگرام تنظیم شده است.';
 box.innerHTML=`<div class="bot-readiness-head"><div><span class="mini-label">TELEGRAM / LIVE STATUS</span><h2>${esc(title)}</h2><p>${configured?'در چت همین ربات /start بزن؛ خرید، پرداخت، رسید و دریافت کانفیگ با دکمه‌ها انجام می‌شود.':'توکن و شناسهٔ مدیر را وارد کن و «فعال‌کردن خرید در تلگرام» را بزن؛ سپس یک پلن فعال بساز.'}</p></div><span class="sale-status ${configured&&!h.pollError?'fulfilled':'receipt'}">${configured&&!h.pollError?'تنظیم شده':'نیازمند تنظیم'}</span></div><div class="bot-check-grid">${checks.map(([label,ok])=>`<div class="bot-check ${ok?'ok':'warn'}"><b>${ok?'✓':'○'} ${esc(label)}</b><small>${ok?'آماده':'تکمیل کن'}</small></div>`).join('')}</div>${h.username?`<p class="field-note">ربات متصل: <a href="https://t.me/${encodeURIComponent(h.username)}" target="_blank" rel="noopener" dir="ltr">@${esc(h.username)}</a></p>`:''}${h.pollError?`<p class="form-error">${esc(h.pollError)}</p>`:''}${h.deliveryError?`<p class="form-error">آخرین ارسال ناموفق: ${esc(h.deliveryError)}</p>`:''}<p class="field-note">آخرین دریافت: ${h.lastUpdate?new Date(h.lastUpdate).toLocaleTimeString('fa-IR'):'هنوز پیامی دریافت نشده'} · پیام‌های در صف: ${fa(t.pendingNotices||0)}</p>`;
};

$('#telegram-activate').addEventListener('click',async e=>{
 const button=e.currentTarget,form=$('#telegram-form');
 const data={token:form.elements.token.value,chatId:Number(form.elements.chatId.value),notify:form.elements.notify.checked,replaceWebhook:false};
 try{
  await busy(button,async()=>{
   let next;
   try{next=await api('/api/telegram/activate',{method:'POST',body:data})}
   catch(err){
    if(err.code!=='webhook_active')throw err;
    if(!await confirmAction('اختصاص ربات به همین پنل','این ربات اتصال Webhook فعال دارد. اتصال قبلی جایگزین شود و همین پنل پاسخ‌گویی را انجام بدهد؟ پیام‌های در انتظار حذف نمی‌شوند.'))return;
    next=await api('/api/telegram/activate',{method:'POST',body:{...data,replaceWebhook:true}});
   }
   telegramDraft=false;form.elements.token.value='';renderState(next);
   if(window.openCommerce&&currentView==='sales')await loadSales();
   toast('ربات و فروشگاه فعال شدند؛ در چت ربات /start بزن.');
  });
 }catch(err){toast(err.message,true)}
});

$('#telegram-check').addEventListener('click',async e=>{
 const button=e.currentTarget;
 try{await busy(button,async()=>{
  const d=await api('/api/telegram/diagnostics');
  $('#telegram-check-results').innerHTML=`<section class="panel bot-diagnostics"><h3>${d.ready?'✓ تنظیمات خرید در ربات آماده است.':'این موارد را تکمیل کن.'}</h3>${d.checks.map(c=>`<div class="bot-diagnostic ${c.ok?'ok':'warn'}"><b>${c.ok?'✓':'○'} ${esc(c.label)}</b><p>${esc(c.detail)}</p></div>`).join('')}</section>`;
  if(state){state.telegram=d.status;renderBotStatus(d.status)}
 })}catch(err){toast(err.message,true)}
});

document.addEventListener('click',e=>{const b=e.target.closest('[data-bot-setup]');if(!b)return;e.preventDefault();showView('telegram')});
