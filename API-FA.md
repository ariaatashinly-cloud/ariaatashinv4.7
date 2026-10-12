# اتصال ربات و پنل دیگر به ariaatashin 4.0

در «فروشگاه و سفارش‌ها → API و ربات دیگر» کلید بساز. توکن کامل فقط یک بار نشان داده می‌شود؛ سرور فقط SHA-256 آن را ذخیره می‌کند. کلیدهای منقضی یا باطل‌شده رد می‌شوند. برای یک ربات مدیریت سرویس، فقط `clients:read` و `clients:write` را انتخاب کن.

## ربات نمونهٔ developerAmira/telegram-bot

منبع: <https://github.com/developerAmira/telegram-bot>

در ariaatashin، «تنظیمات فروش → رابط Marzban کلاسیک» را فعال کن. در ربات نمونه، پنل با نوع **Marzban کلاسیک** اضافه کن:

- آدرس: دامنهٔ HTTPS همین ariaatashin، بدون `/shop` و بدون مسیر API؛ مثلاً `https://aria.example.com`.
- روش ترجیحی: Bearer token = API Key ساخته‌شده.
- اگر فرم نام کاربری و رمز دارد: نام کاربری `api` و رمز = همان API Key. `/api/admin/token` آن را به Bearer token تبدیل می‌کند؛ رمز مدیر پنل را وارد نکن.
- منابع را آزمایش کن و تگ‌ها را از `/api/inbounds` انتخاب کن؛ برای نمونه `aria-vless-ws` یا `aria-vless-xhttp`.
- شروع اعتبار را فوری و reset strategy را `no_reset` بگذار. first-use/on_hold، Marzban 1.x، Marzneshin و PasarGuard توسط این رابط شبیه‌سازی نمی‌شوند.

اتصال سرویس‌های ربات نمونه با Connector اصلی آن، بدون تغییر کد Connector، در آزمایش محلی برای health/resources/create/read/update/toggle/reset/revoke/delete بررسی شده است. این نتیجه به معنی استقرار Cloudflare، آزمون درگاه بانکی، پیام واقعی تلگرام یا پشتیبانی از همهٔ قابلیت‌های آن ربات نیست. ربات نمونه جداگانه نصب می‌شود؛ داخل ZIP ما کپی نشده است.

هر API Key فضای کاربران خودش را دارد: کلید دیگر، کاربران مدیر یا کاربران فروشگاه داخلی را مدیریت نمی‌کند. باطل‌کردن کلید، سرویس‌ها را حذف نمی‌کند. برای حفظ مدیریت خارجی، کلید معتبر همان اتصال را نگه دار؛ کلید تازه مالک کاربران کلید قبلی نمی‌شود.

## دسترسی‌ها

| Scope | کاربرد |
|---|---|
| `clients:read` | مشاهدهٔ الگوها و سرویس‌های ساخته‌شده با همین کلید |
| `clients:write` | ساخت، ویرایش، حذف، تغییر وضعیت و تعویض کلید همان کاربران |
| `sales:read` | تنظیمات و پلن‌های فروش؛ برای ابزار مورداعتماد |
| `orders:write` | ثبت سفارش برای شناسهٔ مشتری موجود؛ دسترسی ابزار مدیریتی مورداعتماد |
| `payments:approve` | تأیید تراکنش از بک‌اند درگاه؛ دسترسی مالی حساس |

API با `Authorization: Bearer API_KEY` کار می‌کند. قرار دادن کلید در query string یا URL پشتیبانی نمی‌شود. پاسخ خطا JSON با `error` است. کلید به‌طور پیش‌فرض ۹۰ روز اعتبار دارد؛ سقف انتخاب ۳۶۶ روز است. سقف درخواست معمول ۱۲۰ بار در دقیقه برای هر کلید است؛ retry بی‌وقفه انجام نده.

## رابط کلاسیک

| روش / مسیر | کاربرد |
|---|---|
| POST `/api/admin/token` | فرم URL-encoded با username=api و password=API_KEY |
| GET `/api/system` | وضعیت و شمار کاربران |
| GET `/api/inbounds` | تگ‌های الگوهای قابل ساخت |
| POST `/api/user` | username، data_limit بایت، expire ثانیهٔ Unix یا null، proxies و inbounds |
| GET `/api/user/username` | status، links، subscription_url، data_limit، used_traffic و expire |
| PUT `/api/user/username` | data_limit، expire و status=active/disabled |
| POST `/api/user/username/reset` | ریست مصرف با حفظ وضعیت خاموش کاربر |
| POST `/api/user/username/revoke_sub` | تعویض هویت و لینک اشتراک |
| DELETE `/api/user/username` | حذف سرویس متعلق به همان کلید |

`/api/nodes`، گروه‌بندی Marzban 1.x، scheduler reset دوره‌ای و first-use در این رابط پیاده‌سازی نشده‌اند. `/api/user` فقط ساخت کاربر است، نه فهرست همهٔ کاربران. برای فهرست سرویس‌های همین کلید از `/api/v1/clients` استفاده کن.

نمونهٔ بدنهٔ ساخت:

```json
{"username":"customer_123","data_limit":32212254720,"expire":1791000000,"data_limit_reset_strategy":"no_reset","note":"سرویس مشتری","proxies":{"vless":{}},"inbounds":{"vless":["aria-vless-ws"]}}
```

تاریخ مثال ثابت است؛ برای نصب خودت expire آینده بساز. مقدار صفر یا null برای expire و صفر برای data_limit یعنی نامحدود. نام کاربری ۳ تا ۳۲ حرف لاتین، عدد یا underscore است. نام تکراری با مقدار یکسان بازیابی می‌شود؛ برای تغییر سهمیه از PUT استفاده کن. `note` در پاسخ حفظ می‌شود تا ربات بتواند شناسهٔ عملیات خودش را تطبیق بدهد.

## API بومی

- GET `/api/v1/info`: نسخه، آماده‌بودن هسته، الگوها و قابلیت‌ها.
- GET `/api/v1/clients`: سرویس‌های همین کلید؛ برای دادهٔ تازه، GET مسیر سرویس را بزن.
- POST `/api/v1/clients`: ProvisionInput کامل با شناسه‌های ثابت؛ مناسب اتصال aria به aria.
- GET یا POST یا DELETE `/api/v1/clients/aria-email`: خواندن، ویرایش سهمیه/انقضا/enable یا حذف.
- GET `/api/v1/sales`: تنظیمات و پلن‌ها با scope مربوطه.
- POST `/api/v1/orders`: customerId، kind=buy/renew/topup، planId، serviceId در تمدید، method=manual/wallet/stars، amount برای topup، coupon و idempotency. شناسهٔ درخواست ۸ تا ۱۲۰ حرف است؛ برای retry همان سفارش، همان شناسه را بفرست. تغییر سفارش با شناسهٔ قبلی رد می‌شود. متد wallet موجودی را هنگام ثبت سفارش کسر می‌کند و تحویل در صف انجام می‌شود.
- POST `/api/v1/payments/o-ID`: فقط از بک‌اند دارای `payments:approve`، با `{"paymentId":"BANK_REFERENCE","amount":120000}`. مبلغ تومان، شناسهٔ تراکنش و سفارش باید مطابقت داشته باشند. قبل از فراخوانی، **بک‌اند باید تراکنش را با API واقعی درگاه تأیید کند**. callback مرورگر یا رسید مشتری به این مسیر وصل نشود.

ProvisionInput برای ابزارهای اختصاصی:

```json
{"operationId":"32-hex-characters","email":"aria-12hexchars","uuid":"valid-uuid","password":"40-hex-characters","auth":"40-hex-characters","subId":"32-hex-characters","name":"مشتری","quota":32212254720,"expiry":1791000000000,"profiles":["vless-ws"]}
```

placeholderهای این مثال معتبر نیستند؛ ابزار باید مقادیر تصادفی درست بسازد و پیش از اولین فراخوانی ذخیره کند. `expiry` در API بومی **میلی‌ثانیه** است؛ رابط کلاسیک **ثانیه** است. عملیات مبهم شبکه را با نام/شناسهٔ تازه تکرار نکن. پاسخ 503 ممکن است پس از ثبت درخواست رخ دهد؛ ابتدا همان حساب را بخوان و با شناسهٔ قبلی بازیابی کن.
