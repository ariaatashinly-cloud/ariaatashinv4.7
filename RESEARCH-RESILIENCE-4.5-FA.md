# پژوهش پایداری اتصال — ۱۲ اکتبر ۲۰۲۶

هدف این بررسی پیدا کردن مسیرهای واقعاً قابل اجرا برای پنل است. نتیجهٔ قابل استفاده: تنوع ورودی وب، تنوع TCP/UDP، انتخاب مسیر از کلاینت و آماده‌کردن مبدأ مستقل، بهتر از نام‌گذاری یک پروتکل آزمایش‌نشده به‌عنوان «غیرقابل فیلتر» است.

## در قطعی‌های ایران چه چیزی مستند شده؟

گزارش OONI/CAIDA از آبان ۱۳۹۸ نشان می‌دهد قطع در شبکه‌ها یکسان نبود و بخشی از ارتباط خارجی باقی مانده بود. آزمون دستی آن گزارش امکان تونل DNS با ظرفیت پایین و استفاده از یک VPS داخلی با مسیر خارجی باقی‌مانده را شرح می‌دهد. این نتیجه مخصوص همان زمان و شبکه‌های اندازه‌گیری‌شده است؛ اثبات کارکرد همان مسیرها در یک قطعی آینده نیست.

منبع اولیه: https://ooni.org/post/2019-iran-internet-blackout/

گزارش Cloudflare از ژانویهٔ ۲۰۲۶، سقوط ترافیک به نزدیک صفر و پنجره‌های کوتاه بازگشت ارتباط در بعضی شبکه‌ها را ثبت می‌کند. مشاهدهٔ DNS در یک پنجرهٔ کوتاه به معنای بازبودن تونل همهٔ CDNها، TCP یا UDP نیست. این اندازه‌گیری نیز تعیین نمی‌کند کدام کانفیگ تجاری روی همهٔ اپراتورها کار کرده است.

منبع اولیه: https://blog.cloudflare.com/iran-protests-internet-shutdown/

استنباط مهندسی از این گزارش‌ها: وقتی شخصی در «قطعی ملی» وصل بوده، باید شبکه، ساعت، مقصد و مسیر خارجیِ واقعاً باقی‌مانده را بررسی کرد. این می‌تواند استثنای شبکه یا دسترسی کوتاه بوده باشد؛ نمی‌توان از آن ادعای اتصال بدون مسیر خارجی ساخت. در این پروژه کانفیگ موفق روی اپراتور ایران یا در قطعی واقعی اندازه‌گیری نشده است.

## تصمیم‌های پیاده‌سازی

| مشکل | ابزار قابل استفاده | آنچه همچنان باید آزمود |
|---|---|---|
| IP ورودی مسدود است | ورودی دوم متصل به همان مبدأ، Address جدا از Host/SNI | TLS، رسیدن به مبدأ، handshake و دسترسی از اپراتور مشتری |
| مسیر یک CDN بسته است | Vercel، Deno، Worker یا CDN مستقیمِ مستقل؛ خروجی انتخاب مسیر | دسترسی مشتری به ورودی جدید و محدودیت سرویس |
| UDP عبور نمی‌کند | TCP مستقیم VPS یا WS/TLS | کیفیت همان خط؛ QUIC روی UDP این مشکل را خودکار حل نمی‌کند |
| HTTP استریم محدود است | مسیر CDN مناسب، XHTTP packet-up با اندازهٔ ارسال مناسب؛ یا WS آزموده‌شده | حفظ استریم دانلود، timeout و نرخ درخواست |
| مبدأ خاموش است | سرور و حساب مستقل دوم | چند CDN به همان مبدأ کافی نیست |
| مسیر خارجی باقی نمانده | زیرساخت ارتباطی مستقل لازم است | تغییر TLS، توکن یا الگوریتم، مسیر فیزیکی ایجاد نمی‌کند |

## Vercel و Deno را با اطلاعات جدید بررسی کردیم

مستندات فعلی Vercel، WebSocket بومی در Functions/Fluid را در public beta شرح می‌دهند. اتصال با پایان حداکثر زمان اجرای تابع بسته می‌شود. قالب این پروژه Node.js با `ws` است؛ پروژهٔ تازه ساخته و Production دپلوی می‌شود. سقف قالب ۳۰۰ ثانیه است. این واسط یک سرور دائمی Xray، آمار مصرف یا پایگاه دادهٔ مستقل روی Vercel ایجاد نمی‌کند؛ فقط ورودی WS را به مبدأ مدیریت‌شده می‌رساند.

https://vercel.com/docs/functions/websockets
https://vercel.com/docs/functions/limitations
https://vercel.com/docs/rest-api/projects/create-a-new-project
https://vercel.com/docs/rest-api/deployments/create-a-new-deployment

Deno Deploy Classic و API قدیمی برای این اتصال انتخاب نشدند. مستندات مهاجرت، API v2، توکن `ddo_`، App/Revision و دامنهٔ Production را توضیح می‌دهند. قالب این پروژه از `Deno.serve` و WebSocket استفاده می‌کند؛ request/response مطابق OpenAPI v2 است. تکمیل build و آماده‌شدن نام دامنه ممکن است فوری نباشد، بنابراین وضعیت باید دریافت شود.

https://docs.deno.com/deploy/migration_guide/
https://docs.deno.com/subhosting/manual/api_migration_guide/
https://api.deno.com/v2/openapi.json

Cloudflare Worker برای WebSocket ورودی قابلیت رسمی دارد. Worker این پروژه مقصد ثابت دارد و درخواست‌های مدیریت پنل را عبور نمی‌دهد. CDN معمول Cloudflare، TCP/UDP دلخواه را مثل HTTP پروکسی نمی‌کند؛ پورت‌های مستقیم VPS و QUIC را نباید با یک رکورد DNS نارنجی قابل اجرا فرض کرد.

https://developers.cloudflare.com/workers/runtime-apis/websockets/
https://developers.cloudflare.com/api/resources/workers/subresources/scripts/methods/update/
https://developers.cloudflare.com/fundamentals/reference/network-ports/

## هسته و پروتکل جدید

این بسته موتور مدیریت و واسط اختصاصی دارد، در حالی که اجرای پروتکل به Xray و sing-box آزموده‌شده واگذار می‌شود. XHTTP، Reality/Vision، AnyTLS، Hysteria2 و TUIC به شیوه‌های متفاوت کار می‌کنند؛ هیچ‌کدام در تمام شبکه‌ها به‌طور ذاتی سریع‌تر نیست. جایگزینی رمزنگاری با الگوریتم تازهٔ بدون بررسی، مدرک سرعت یا مقاومت ایجاد نمی‌کند. تغییر sing-box این پروژه فقط سازگاری آمار و محیط محدود میزبان را بهبود داده است.

https://github.com/XTLS/Xray-core/discussions/4113
https://xtls.github.io/en/config/transports/reality.html
https://sing-box.sagernet.org/configuration/inbound/anytls/
https://sing-box.sagernet.org/configuration/inbound/hysteria2/
https://sing-box.sagernet.org/configuration/inbound/tuic/
https://sing-box.sagernet.org/configuration/outbound/urltest/

معیار مرحلهٔ بعد، ثبت موفقیت اتصال از هر اپراتور، تاخیر، سرعت پایدار، مدت اتصال و نوع خطاست. آزمون سرور، جای اندازه‌گیری روی خط مشتری را نمی‌گیرد. برای کاهش نقطهٔ شکست، چند مبدأ مستقل از چند ورودی روی یک مبدأ ارزش بیشتری دارد.

## کشور، QR و حریم خصوصی خروجی

تشخیص کشور بر HTTPS سرویس IPwho.is است و فقط خروجی اینترنت سرور را تشخیص می‌دهد. cache محلی جلوی درخواست مکرر را می‌گیرد. پرچم نمی‌تواند خروجی CDN را به کشور دلخواه تغییر دهد. QR از محتوای واقعی لینک ساخته می‌شود و از API تصویر مولد استفاده نمی‌کند؛ تولید تصویر QR با مدل، برای دقت اتصال مناسب نیست.

https://ipwhois.io/documentation
https://core.telegram.org/bots/api#sendphoto
https://github.com/skip2/go-qrcode
https://github.com/lipis/flag-icons

## اصلاح یک مانع واقعی استریم

آزمون HTTP/1 نشان داد لایهٔ پنل ممکن است تا تمام‌شدن بدنهٔ POST پاسخ را نگه دارد. برای مسیر تونل، EnableFullDuplex فعال شد تا درخواست و پاسخ هم‌زمان ادامه پیدا کنند؛ در HTTP/2 این رفتار پیش‌فرض است. این اصلاح مشکل لایهٔ پنل را برطرف می‌کند، اما محدودیت استریم یک CDN یا اپراتور را تغییر نمی‌دهد.

https://pkg.go.dev/net/http#ResponseController.EnableFullDuplex
