# nexra-mirzabot

ربات فروش تلگرام Nexra — بازنویسی کامل میرزابات (PHP) به **Go**.

- یک فایل اجرایی، بدون PHP و php-fpm و crontab؛ کارهای زمان‌بندی‌شده داخل خود ربات اجرا می‌شوند.
- **همان دیتابیس** نسخه‌ی PHP: ربات‌های فعلی بدون انتقال داده، بدون عوض‌کردن وبهوک و بدون قطعی به Go منتقل می‌شوند و هر وقت بخواهید با یک دستور به PHP برمی‌گردند.
- همه‌ی قابلیت‌های نسخه‌ی شما: پنل Nexra (ساخت کاربر از طریق Nexra، خواندن مستقیم از مرزبان)، قفل مدیریت پنل با کد مخفی، انتخاب نام کاربری دلخواه/تصادفی، و **تأیید خودکار پرداخت** با اپ اندروید (همان کلید اتصال قبلی کار می‌کند).
- دکمه‌های منوی اصلی قابل تنظیم: جا، رنگ و آیکون ایموجی پریمیوم.
- API مدیریت برای **Nexra Panel** (بخش «Bot»): ادمین هر ربات محصولات، پرداخت‌ها، کاربران، متن‌ها و دکمه‌ها را از پنل مدیریت می‌کند؛ سرورها (پنل‌های VPN) فقط دست مالک است.

کد PHP قبلی در `legacy-php/` نگه داشته شده (مرجع و مسیر بازگشت).

## نصب ربات جدید

```bash
N=7 TOKEN='123456:abc-token' DOMAIN='bot7.example.com' ADMIN='123456789' \
NEXRA_SECRET='your-secret-code' CERT_EMAIL='you@example.com' \
bash <(curl -sL https://raw.githubusercontent.com/MHBehzadian/nexra-mirzabot/main/install.sh)
```

پیش‌نیاز: رکورد DNS دامنه به همین سرور، و nginx + MySQL/MariaDB + certbot نصب باشند (PHP لازم نیست).
`N` شماره‌ی ربات روی این سرور است؛ ربات روی پورت `18000+N` اجرا می‌شود و nginx جلویش است.
آخر کار، آدرس و دو کلید API برای اتصال در Nexra Panel چاپ می‌شود.

## انتقال ربات‌های PHP فعلی به Go

همه‌چیز با یک دستور، بدون واردکردن هیچ اطلاعاتی (Nexra Panel روی همین سرور به‌روز می‌شود، همه‌ی ربات‌ها منتقل و هر کدام به صاحبش در
پنل داده می‌شود):

```bash
curl -sLo /root/nexrabot-install.sh https://raw.githubusercontent.com/MHBehzadian/nexra-mirzabot/main/install.sh
bash /root/nexrabot-install.sh all
```

فقط یک ربات:

```bash
curl -sLo install.sh https://raw.githubusercontent.com/MHBehzadian/nexra-mirzabot/main/install.sh
N=7 bash install.sh migrate        # برای ربات /var/www/html/botmirzapanel7
```

اسکریپت قبل از هر تغییری از دیتابیس بکاپ می‌گیرد، ربات Go را آزمایشی بالا می‌آورد، و فقط اگر همه‌چیز سالم بود nginx را از php-fpm به Go
می‌برد. اگر هر مرحله‌ای خطا بدهد، خودش همه‌چیز را به حالت PHP برمی‌گرداند. جزئیات و نکته‌ها: [docs/MIGRATION.md](docs/MIGRATION.md).

بازگشت به PHP در هر زمان:

```bash
N=7 bash install.sh rollback
```

به‌روزرسانی همه‌ی ربات‌های Go روی سرور به آخرین نسخه:

```bash
bash install.sh update
```

## اتصال به Nexra Panel

```bash
N=7 bash install.sh keys
```

در Nexra Panel (با حساب مالک) → **Bot** → «افزودن ربات»: آدرس `https://bot7.example.com` و دو کلید را وارد کنید و ربات را به ادمینش بدهید.
آن ادمین از منوی Bot همه‌ی کارهای ربات را انجام می‌دهد، جز افزودن/ویرایش/حذف سرورها که فقط برای مالک است.

## تفاوت‌ها با نسخه‌ی PHP

رفتار ربات برای کاربر و ادمین همان است؛ این را یک آزمون تفاضلی نشان می‌دهد که نسخه‌ی PHP و Go را کنار هم اجرا می‌کند، ۲۹۳ پیام یکسان به
هر دو می‌فرستد و تک‌تک درخواست‌های تلگرام و ردیف‌های دیتابیس را مقایسه می‌کند (`tests/diff`). چند باگ نسخه‌ی PHP عمداً درست شده‌اند؛ فهرست
کامل: [docs/CHANGES.md](docs/CHANGES.md).

## دستورات

```
nexrabot serve        -c /etc/nexrabot/bot7.env    اجرای ربات (systemd: nexrabot@7)
nexrabot migrate-php  --php-dir … --out …          ساخت تنظیمات از config.php نسخه‌ی PHP
nexrabot import-sql   -c … --file backup.sql.gz    بارگذاری بکاپ mysqldump در دیتابیس
nexrabot db-dump      -c … --out bot7.sql.gz       بکاپ دیتابیس
nexrabot schema       -c …                         ساخت/به‌روزرسانی جدول‌ها (تکرارپذیر)
nexrabot check        -c …                         آزمایش دیتابیس و توکن
nexrabot set-webhook  -c …                         تنظیم وبهوک (با توکن مخفی)
nexrabot keys         -c …                         کلیدهای API برای Nexra Panel
nexrabot autopay-test -c … parse|status|send …     آزمایش تشخیص پیامک بانک
```

لاگ: `journalctl -u nexrabot@7 -f`

## ساخت از سورس و آزمون‌ها

```bash
go build -o nexrabot ./cmd/nexrabot
go test ./...
cd tests/diff && python3 run.py        # آزمون تفاضلی PHP/Go (نیاز به MariaDB و php)
python3 api_smoke.py                   # API مدیریت
python3 panel_e2e.py                   # بخش Bot در Nexra Panel
```

### انتشار نسخه‌ی تازه

عدد نسخه را در `.github/RELEASE_VERSION` بالا ببرید (مثلاً `v6.0.6`) و push کنید. GitHub Actions همان نسخه را می‌سازد و منتشر می‌کند. نصب
یک نسخه‌ی مشخص: `RELEASE=v6.0.5 bash install.sh update`.

با هر تگ `v*` (مثلاً `v6.0.0`)، GitHub Actions فایل‌های اجرایی لینوکس (amd64/arm64) و اپ اندروید تأیید خودکار را می‌سازد و در Release
می‌گذارد؛ `install.sh` و دکمه‌ی «گرفتن آخرین نسخه از GitHub» در Nexra Panel از همان‌جا برمی‌دارند.

## License

GPLv3, like the upstream [mahdiMGF2/botmirzapanel](https://github.com/mahdiMGF2/botmirzapanel).
