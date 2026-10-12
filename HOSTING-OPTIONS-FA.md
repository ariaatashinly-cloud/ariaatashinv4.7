# گزینه‌های میزبانی برای این پروژه

بررسی مستندات رسمی: ۹ اکتبر ۲۰۲۶. این گزینه‌ها محل دیپلوی هستند؛ نصب موفق این پروژه روی حساب واقعی هر سرویس در این بررسی انجام نشده است. منطقهٔ نزدیک‌تر به‌تنهایی پینگ کمتر را تضمین نمی‌کند؛ مسیر اپراتور ایران و ورودی CDN هم اثر دارند.

| سرویس | منطقهٔ مرتبط | وضعیت رایگان و محدودیت اصلی | سازگاری این پروژه |
|---|---|---|---|
| [Koyeb](https://www.koyeb.com) | Frankfurt | یک Free Instance؛ ۵۱۲MB رم و ۰٫۱ vCPU؛ خواب بعد از یک ساعت بدون ترافیک؛ بدون Volume | Docker/Web Service قابل انتخاب است. برای آزمون WS مناسب‌تر از نگه‌داری دائمی SQLite است؛ XHTTP به بررسی streaming پراکسی نیاز دارد. |
| [Render](https://render.com) | Frankfurt | Free Web Service؛ خواب بعد از ۱۵ دقیقه؛ دیسک موقت و حذف SQLite در خواب، ری‌استارت یا ری‌دیپلوی | Docker و WebSocket دارد؛ برای پنل پایدار بدون بکاپ مناسب نیست. ترافیک خروجی غیرعادی ممکن است موجب تعلیق شود. |
| [Oracle Cloud](https://www.oracle.com/cloud/free/) | Dubai، Abu Dhabi یا Mumbai، در صورت امکان انتخاب منطقه و وجود ظرفیت | Always Free جدا از اعتبار آزمایشی ۳۰روزه است؛ اغلب کارت و شمارهٔ تلفن لازم دارد. ظرفیت Compute رایگان ممکن است پر باشد. | ماشین Linux **AMD/x86_64** با Docker و دیسک پایدار، با نسخهٔ فعلی سازگارتر است. A1 نوع ARM با Dockerfile و باینری فعلی سازگار نیست. |

منابع: [Koyeb Free Instance](https://www.koyeb.com/docs/reference/instances)، [Koyeb Services](https://www.koyeb.com/docs/reference/services)، [Render Free](https://render.com/docs/free)، [Render Regions](https://render.com/docs/regions)، [Render WebSocket](https://render.com/docs/websocket)، [Oracle Free Tier](https://docs.oracle.com/en-us/iaas/Content/FreeTier/freetier.htm)، [Oracle Always Free](https://docs.oracle.com/en-us/iaas/Content/FreeTier/freetier_topic-Always_Free_Resources.htm)، [Oracle Regions](https://www.oracle.com/cloud/public-cloud-regions/).

برای آزمایشِ یک هاست وب، Koyeb Frankfurt گزینهٔ قابل بررسی است؛ اما پلن رایگانش محل نگه‌داری دائمی کاربران نیست. برای سرویس با دیتابیس پایدار، Oracle AMD در منطقهٔ نزدیک، مشروط به امکان ثبت‌نام و ظرفیت، امکانات مناسب‌تری دارد. دسترسی به ثبت‌نام از ایران یا آزادبودن ظرفیت این مناطق تأیید نشده است. روی سرویس فعلی هم اگر Volume واقعی داری، تعویض میزبان به‌خاطر نام «رایگان» الزاماً بهتر نیست.

## راه‌اندازی Koyeb یا Render

در پنل پروژه، «میزبان و سرور مجازی»، گزینهٔ Koyeb یا Render راهنمای کوتاه دارد؛ انتخاب این گزینه فقط راهنما و تشخیص میزبان است و دیپلوی انجام نمی‌دهد.

1. ریپازیتوری همین پروژه را وصل کن؛ شاخهٔ اصلی و Dockerfile ریشه.
2. نوع سرویس Web Service / Docker و منطقهٔ Frankfurt را انتخاب کن.
3. `ARIA_ADMIN_PASSWORD` را به رمز خصوصی حداقل ۱۲کاراکتری و `ARIA_ADMIN_USER` را به نام مدیر تنظیم کن.
4. پورت HTTP داخلی 8080، `PORT=8080` و مسیر عمومی `/`؛ Health check روی `/health`.
5. یک Instance/Replica. در پلن بدون Volume، قبل از هر بازسازی بکاپ بگیر و انتظار حفظ خودکار SQLite نداشته باش.
6. دامنهٔ عمومی HTTPS میزبان را باز کن؛ ورود مدیر دامنه را خودکار یاد می‌گیرد. در حالت خودکار، `ARIA_PUBLIC_URL` را تنظیم نکن.

## فقط WS و XHTTP؟

در **حالت Cloud این پروژه**، عبور از ورودی وب با WS و XHTTP انجام می‌شود و هر دو برای VLESS، VMess و Trojan در فرم دستی قابل انتخاب‌اند. WebSocket باید از طرف میزبان فعال باشد؛ XHTTP علاوه بر نسخهٔ کلاینت، به streaming و محدودیت زمان درخواستِ پراکسی وابسته است.

این محدودیتِ حالت فعلی برنامه است، نه قانون همهٔ سرویس‌های ابری: بعضی میزبان‌ها gRPC یا TCP عمومی هم دارند. برای مثال [TCP Proxy در Koyeb](https://www.koyeb.com/docs/run-and-scale/tcp-proxy) یک قابلیت جدا با دامنه و پورت اختصاص‌یافته است؛ صرف انتخاب الگوی TCP در پنل این ورودی را فراهم نمی‌کند.

برای REALITY/TCP، gRPC مستقیم، TUIC یا Hysteria2 باید زیرساخت، پورت عمومی TCP/UDP لازم را بدهد. الگوهای آمادهٔ حالت VPS در این پروژه برای همین شرایط‌اند. ارائه‌دهندهٔ وب فقط با HTTPS روی 443، جای VPS با پورت UDP باز را نمی‌گیرد. قبل از انتقال، محدودیت ترافیک و مقررات همان پلن را بررسی کن.

## Address، Host و SNI

Address مقصد اتصال شبکه است؛ Host مسیر وب و SNI هویت TLS را تعیین می‌کند. [مستندات SNI در Cloudflare](https://www.cloudflare.com/learning/ssl/what-is-sni/) و [تنظیم Host در WebSocket Xray](https://xtls.github.io/en/config/transports/websocket.html) این جدایی را توضیح می‌دهند. نتیجهٔ عملی: IP معتبر همان ورودی را می‌توان جدا وارد کرد، ولی جایگزینی IP به‌تنهایی مسدودشدن SNI یا توقف سرویس را برطرف نمی‌کند.
