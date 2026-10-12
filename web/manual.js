"use strict";

let manualCreateMode = false;
let connectionDraft = false;

window.setCreateMode = function (manual) {
  manualCreateMode = !!manual;
  $('#create-modes').hidden = false;
  $('#manual-fields').hidden = !manualCreateMode;
  $('#manual-fields').disabled = !manualCreateMode;
  $('#connection-choices').hidden = manualCreateMode;
  $('#count-label').hidden = manualCreateMode;
  $('#create-auto-note').hidden = manualCreateMode;
  $$('#create-modes button').forEach(b => {
    const selected = (b.dataset.createMode === 'manual') === manualCreateMode;
    b.classList.toggle('active', selected);
    b.setAttribute('aria-pressed', String(selected));
  });
  $('#create-title').textContent = manualCreateMode ? 'ساخت کانفیگ دستی' : 'یک اتصال تازه بساز.';
  updateManualFields();
};

window.hideManualCreate = function () {
  manualCreateMode = false;
  $('#create-modes').hidden = true;
  $('#manual-fields').hidden = true;
  $('#manual-fields').disabled = true;
  $('#create-auto-note').hidden = true;
};

function updateManualFields() {
  const f = $('#create-form');
  const shadowsocks = f.elements.manualProtocol.value === 'shadowsocks';
  const passwordProtocol = shadowsocks || f.elements.manualProtocol.value === 'trojan';
  const xhttp = f.elements.manualNetwork.value === 'xhttp';
  $('#manual-uuid-label').hidden = passwordProtocol;
  f.elements.manualUUID.disabled = !manualCreateMode || passwordProtocol;
  $('#manual-password-label').hidden = !passwordProtocol;
  f.elements.manualPassword.disabled = !manualCreateMode || !passwordProtocol;
  $('#manual-method-label').hidden = !shadowsocks;
  f.elements.manualMethod.disabled = !manualCreateMode || !shadowsocks;
  $('#manual-mode-label').hidden = !xhttp;
  f.elements.manualMode.disabled = !manualCreateMode || !xhttp;
}

window.manualCreateBody = function () {
  if (!manualCreateMode) return null;
  const f = $('#create-form').elements;
  const shadowsocks = f.manualProtocol.value === 'shadowsocks';
  const passwordProtocol = shadowsocks || f.manualProtocol.value === 'trojan';
  return {
    protocol: f.manualProtocol.value,
    network: f.manualNetwork.value,
    path: f.manualPath.value.trim(),
    mode: f.manualNetwork.value === 'xhttp' ? f.manualMode.value : '',
    method: shadowsocks ? f.manualMethod.value : '',
    endpoint: {
      address: f.manualAddress.value.trim(),
      port: Number(f.manualPort.value || 0),
      host: f.manualHost.value.trim(),
      sni: f.manualSNI.value.trim()
    },
    uuid: passwordProtocol ? '' : f.manualUUID.value.trim(),
    password: passwordProtocol ? f.manualPassword.value : ''
  };
};

window.renderManual = function (s) {
  const e = s.settings.connection || {};
  const origin = new URL(s.settings.publicURL || window.location.origin);
  const f = $('#connection-settings-form').elements;
  if (!connectionDraft) {
    f.address.value = e.address || '';
    f.port.value = e.port || '';
    f.host.value = e.host || '';
    f.sni.value = e.sni || '';
  }
  f.address.placeholder = origin.hostname;
  f.port.placeholder = origin.port || (origin.protocol === 'https:' ? '443' : '80');
  f.host.placeholder = f.sni.placeholder = origin.hostname;
  const m = $('#create-form').elements;
  m.manualAddress.placeholder = e.address || origin.hostname;
  m.manualPort.placeholder = String(e.port || origin.port || (origin.protocol === 'https:' ? 443 : 80));
  m.manualHost.placeholder = e.host || origin.hostname;
  m.manualSNI.placeholder = e.sni || origin.hostname;
  $('#connection-endpoint-note').textContent = e.address
    ? 'Address پیش‌فرض کانفیگ‌های وب: ' + e.address + '؛ اشتراک‌ها را در کلاینت به‌روزرسانی کن.'
    : 'Address از دامنهٔ پنل گرفته می‌شود. فیلد خالی یعنی تنظیم خودکار.';
};

document.addEventListener('click', async event => {
  const b = event.target.closest('button');
  if (!b) return;
  if (b.hasAttribute('data-select-cloud')) {
    const keys = new Set(state.profiles.filter(p => !p.direct && !p.manual && p.available !== false).map(p => p.key));
    $$('input[name="profiles"]', $('#create-form')).forEach(input => {
      input.checked = keys.has(input.value);
      input.closest('.choice').classList.toggle('selected', input.checked);
    });
  }
  if (b.dataset.createMode) setCreateMode(b.dataset.createMode === 'manual');
  if (b.classList.contains('manual-create-trigger')) {
    await openCreate();
    if ($('#create-dialog').open) setCreateMode(true);
  }
});

for (const name of ['manualProtocol', 'manualNetwork']) {
  $('#create-form').elements[name].addEventListener('change', updateManualFields);
}

$('#connection-settings-form').addEventListener('input', () => { connectionDraft = true; });
$('#connection-settings-form').addEventListener('submit', async event => {
  event.preventDefault();
  const form = event.currentTarget;
  const f = form.elements;
  const s = state.settings;
  const connection = { address: f.address.value.trim(), port: Number(f.port.value || 0), host: f.host.value.trim(), sni: f.sni.value.trim() };
  $('#connection-settings-error').textContent = '';
  try {
    await busy($('button[type="submit"]', form), async () => {
      const next = await api('/api/settings', { method: 'POST', body: { publicURL: s.publicURL, automaticDomain: s.automaticDomain, hostPreset: s.hostPreset || 'auto', connection } });
      connectionDraft = false;
      renderState(next);
    });
    toast('آدرس اتصال ذخیره شد؛ اشتراک را در برنامهٔ کلاینت به‌روزرسانی کن.');
  } catch (error) {
    $('#connection-settings-error').textContent = error.message;
  }
});

$('#reset-connection-address').addEventListener('click', () => {
  const f = $('#connection-settings-form');
  for (const name of ['address', 'port', 'host', 'sni']) f.elements[name].value = '';
  connectionDraft = true;
  f.requestSubmit();
});

const xrayButton = document.createElement('button');
xrayButton.type = 'button';
xrayButton.className = 'button secondary';
xrayButton.id = 'download-xray-outbounds';
xrayButton.textContent = 'Xray JSON · WS / XHTTP';
$('#download-links').after(xrayButton);
xrayButton.addEventListener('click', async event => {
  if (!linkUser) return;
  try {
    await busy(event.currentTarget, async () => {
      const data = await api('/api/client-export?email=' + encodeURIComponent(linkUser.email));
      if (!data.xray?.length) throw new Error('این خروجی برای کانفیگ‌های وب WS/XHTTP است.');
      download(JSON.stringify({outbounds:data.xray}, null, 2), 'ariaatashin-xray-outbounds.json', 'application/json');
    });
    toast('خروجی Xray دانلود شد؛ هر outbound یک اتصال مستقل است.');
  } catch (error) { toast(error.message, true); }
});

// A complete configuration for the selected connection also works when a
// client's ss:// importer discards WS/XHTTP parameters.
const fullXrayButton = document.createElement('button');
fullXrayButton.type = 'button';
fullXrayButton.className = 'button secondary';
fullXrayButton.id = 'download-xray-config';
fullXrayButton.textContent = 'Xray همین اتصال';
xrayButton.after(fullXrayButton);
fullXrayButton.addEventListener('click', async event => {
  if (!linkUser) return;
  try {
    await busy(event.currentTarget, async () => {
      const data = await api('/api/client-export?email=' + encodeURIComponent(linkUser.email));
      const outbound = data.xray?.find(o => o.tag === selectedLink);
      if (!outbound) throw new Error('ابتدا یک اتصال وب WS یا XHTTP را از تب‌های بالا انتخاب کن.');
      const config = {
        log: {loglevel:'warning'},
        inbounds: [{tag:'socks-in',listen:'127.0.0.1',port:10808,protocol:'socks',settings:{auth:'noauth',udp:false}}],
        outbounds: [outbound]
      };
      download(JSON.stringify(config, null, 2), 'ariaatashin-' + outbound.tag + '.xray.json', 'application/json');
    });
    toast('فایل کامل Xray برای اتصال انتخاب‌شده دانلود شد؛ آن را به‌عنوان Custom config وارد کن.');
  } catch (error) { toast(error.message, true); }
});
