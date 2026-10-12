# محدودیت‌های اتصال هنگام اختلال اینترنت ایران

بررسی منابع اولیه در ۱۲ اکتبر ۲۰۲۶ انجام شد. گزارش‌های Cloudflare دربارهٔ ژوئن ۲۰۲۵ تفاوت زمان و شدت اختلال میان اپراتورها و محدودشدن به شبکهٔ داخلی را نشان می‌دهند. گزارش ژانویهٔ ۲۰۲۶ نیز افت شدید ترافیک و تغییر دسترسی شبکه را شرح می‌دهد. گزارش OONI دربارهٔ ۲۰۲۲، مسدودسازی سرویس‌ها و DNS رمزگذاری‌شده را از خاموشی‌های اپراتوری تفکیک می‌کند.

از این اندازه‌گیری‌ها نمی‌توان نتیجه گرفت که هر روایت «کانفیگ CDN در قطعی وصل بود» به معنی عبور از قطع کامل تمام مسیرهای خارجی است. نتیجهٔ فنی محتمل این است که در زمان، اپراتور یا مقصد موردنظر یک مسیر باقی مانده بود. صحت کانفیگ‌های مشخص افراد در این بررسی آزمایش نشده است.

CDN در شرایطی کمک می‌کند که ورودی آن از خط کاربر قابل دسترسی باشد و به مبدأ دسترسی داشته باشد. تغییر Address همراه با Host/SNI صحیح، یا دو ورودی برای آپلود و دانلود XHTTP، می‌تواند تنوع مسیر بدهد. مسدودشدن SNI، IP، انتقال، مقصد CDN یا مسیر بین‌الملل عوامل جدا هستند. مقدار پینگ از سرور خارج، کیفیت همان ورودی از همراه‌اول یا ایرانسل را تعیین نمی‌کند.

قابلیت‌های اجرایی این بسته: ثبت چند ورودی، آزمون TLS/مسیریابی به همین پنل، تنظیم مستقل Address/Host/SNI، مسیر دانلود جدا XHTTP، فایل انتخاب خودکار مسیر در کلاینت و دو موتور انتقال مستقل. این‌ها پروتکل ناشناخته یا اتصال غیرقابل فیلتر نیستند. توکن Cloudflare برای مدیریت رکورد DNS است و پهنای باند یا اجازهٔ عبور از قطعی ایجاد نمی‌کند. توکن یک سرویس دیگر نیز به‌تنهایی مسیر داده را فراهم نمی‌کند.

منابع:
- Cloudflare، اختلال‌های فصل دوم ۲۰۲۵: https://blog.cloudflare.com/q2-2025-internet-disruption-summary/
- Cloudflare، خاموشی ایران در ژانویهٔ ۲۰۲۶: https://blog.cloudflare.com/iran-protests-internet-shutdown/
- OONI، اندازه‌گیری مسدودسازی و اختلال ایران ۲۰۲۲: https://ooni.org/post/2022-iran-blocks-social-media-mahsa-amini-protests/
- Project X، توضیح رسمی XHTTP و downloadSettings: https://github.com/XTLS/Xray-core/discussions/4113
- Cloudflare، پشتیبانی WebSocket: https://developers.cloudflare.com/network/websockets/
- Cloudflare، وضعیت Proxy رکورد DNS: https://developers.cloudflare.com/dns/proxy-status/
- Deplexo، دامنهٔ اختصاصی: https://docs.deplexo.com/guides/domains/
- Vercel، محدودیت‌های اجرای تابع: https://vercel.com/docs/functions/limitations
- sing-box، URLTest: https://sing-box.sagernet.org/configuration/outbound/urltest/
