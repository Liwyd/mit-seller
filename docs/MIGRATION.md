# انتقال ربات PHP به Go

خلاصه: ربات Go **همان دیتابیس** را می‌خواند و می‌نویسد و **همان آدرس‌ها** را جواب می‌دهد (`/index.php` برای وبهوک، `/autopay.php` برای
اپ تأیید خودکار، `/payment/...` برای درگاه‌ها). پس انتقال فقط یعنی nginx به‌جای php-fpm درخواست‌ها را به ربات Go بدهد. داده‌ای منتقل
نمی‌شود، وبهوک عوض نمی‌شود و کاربران چیزی حس نمی‌کنند.

## همه‌چیز با یک دستور

```bash
curl -sLo /root/mitseller-install.sh https://raw.githubusercontent.com/Liwyd/mit-seller/main/install.sh
bash /root/mitseller-install.sh all
```

هیچ اطلاعاتی لازم نیست؛ به ترتیب:

1. **Mit Panel روی همین سرور** پیدا می‌شود (کانتینر docker با نام `mit-panel`، هر پوشه‌ای که باشد). از پوشه‌ی
   `data` آن بکاپ گرفته می‌شود (`/root/mitseller-backups/panel-data-<تاریخ>.tar.gz`)، نسخه‌ی تازه از GitHub ساخته و پنل با همان `.env`
   و همان داده‌ها دوباره بالا می‌آید. اگر ساختن نسخه‌ی تازه خطا بدهد، پنل دست نمی‌خورد؛ اگر پنل تازه بالا نیاید، خودکار نسخه‌ی قبلی
   برمی‌گردد (نسخه‌ی قبلی با نام `mit-panel-previous:<تاریخ>` نگه داشته می‌شود).
2. آدرس، نام کاربری و رمز سوپرادمین پنل از تنظیمات خود پنل (`.env` آن) خوانده می‌شود.
3. هر ربات PHP در `/var/www/html/botmirzapanel*` جداگانه و با همه‌ی مراحل پایین منتقل می‌شود. اگر یکی خطا بدهد، فقط همان خودکار به
   PHP برمی‌گردد و بقیه ادامه می‌دهند.
4. وقتی همه منتقل شدند، هر ربات به Mit Panel وصل و به صاحبش داده می‌شود: ادمینی که نام کاربری‌اش همان ریسلرِ پنل Mit ربات است،
   وگرنه ادمینی که آیدی عددی تلگرامش (همان که در پنل برای ادمین ثبت شده) جزو ادمین‌های ربات است. آیدی‌ای که در **همه‌ی** ربات‌ها ادمین
   است (آیدی خود مالک، مثلاً ادمین اصلی همه‌ی ربات‌ها) کنار گذاشته می‌شود و پنل آن را برای ربات‌های بعدی هم به خاطر می‌سپارد. اگر
   بیشتر از یک ادمین پنل بخورد یا هیچ‌کدام، ربات وصل می‌شود ولی به کسی داده نمی‌شود تا خودتان در «اتصال ربات‌ها» انتخاب کنید. (فقط
   یک ربات دارید؟ آیدی خودتان را بدهید: `MIT_OWNER_ID=123456 bash /root/mitseller-install.sh all`)

دوباره اجرا کردن ضرری ندارد (ربات‌های منتقل‌شده فقط دوباره به پنل وصل می‌شوند). خلاصه‌ی آخر کار می‌گوید کدام منتقل شد و کدام نه؛ لاگ هر
ربات در `/root/mitseller-backups/migrate-botN.log`.

**پنل و ربات‌ها روی دو سرور جدا؟** روی سرور پنل همان `all` را بزنید: پنل به‌روز می‌شود و چون رباتی آن‌جا نیست، دستور آماده (با آدرس و
رمز پنل) برای سرور ربات‌ها چاپ می‌شود؛ همان را روی سرور ربات‌ها بزنید. (بعداً هم: `bash /root/mitseller-install.sh panel-link`)

فقط به‌روزکردن پنل: `bash /root/mitseller-install.sh panel-update` — فقط ربات‌ها با پنلی دیگر:
`MIT_PANEL_URL=https://panel.example.com/dashboard MIT_PANEL_USER=admin MIT_PANEL_PASS='رمز' bash /root/mitseller-install.sh migrate-all`

## انتقال خودکار یک ربات

```bash
curl -sLo install.sh https://raw.githubusercontent.com/Liwyd/mit-seller/main/install.sh
N=7 bash install.sh migrate
# پوشه‌ی دیگری؟  N=7 PHP_DIR=/var/www/html/mybot bash install.sh migrate
```

مراحل، به ترتیب:

1. **بررسی‌ها، بدون هیچ تغییری:** `config.php` پیدا می‌شود؛ سایت nginx که این پوشه را با php-fpm سرو می‌کند پیدا می‌شود و تغییرش روی یک کپی
   امتحان می‌شود. اگر چیدمان nginx ناآشنا باشد، همین‌جا متوقف می‌شود و چیزی دست نمی‌خورد.
2. فایل اجرایی در `/usr/local/bin/mitseller` و سرویس `mitseller@.service` نصب می‌شوند (کاربر سیستمی `mitseller`).
3. تنظیمات از `config.php` خوانده می‌شود و در `/etc/mitseller/bot7.env` نوشته می‌شود (توکن، دیتابیس، دامنه، کد مخفی، به‌علاوه‌ی کلیدهای
   API و توکن مخفی وبهوک که تازه ساخته می‌شوند).
4. **بکاپ کامل دیتابیس** در `/root/mitseller-backups/bot7-<تاریخ>.sql.gz`، قبل از هر تغییری در دیتابیس.
5. اتصال به دیتابیس و توکن ربات آزمایش می‌شود.
6. جدول‌ها به‌روز می‌شوند: فقط جدول/ستون‌های جاافتاده اضافه می‌شوند (همان کار `table.php`) به‌علاوه‌ی دو جدول کوچک `mit_kv` و
   `mit_broadcast`. جدول‌های هم‌نامِ قبل از بازنام‌گذاری (`nexra_kv`، `nexra_broadcast`) خودکار به نام تازه منتقل می‌شوند.
   چیزی پاک یا عوض نمی‌شود و ربات PHP همچنان کار می‌کند. وضعیت روشن/خاموش کرون‌ها از crontab خوانده و منتقل می‌شود.
7. ربات Go یک بار آزمایشی (با کرون‌های خاموش) بالا می‌آید. اگر بالا نیاید، کار همین‌جا تمام می‌شود و ربات PHP دست‌نخورده است.
8. **جابه‌جایی** (از این‌جا هر خطایی، همه‌چیز را خودکار به PHP برمی‌گرداند):
   - از سایت nginx و crontab نسخه‌ی پشتیبان در `/etc/mitseller/` گرفته می‌شود؛
   - خط‌های `https://دامنه/cron/*.php` از crontab برداشته می‌شوند (بقیه‌ی خط‌ها، مثل بکاپ ساعتی، می‌مانند)؛
   - سرویس `mitseller@7` روشن می‌شود؛
   - در سایت nginx، بلوک `location /` با `include /etc/nginx/snippets/mitseller7.conf` (proxy به ‎127.0.0.1:18007) جایگزین و بلوک
     `location ~ \.php$` برداشته می‌شود. `nginx -t` و reload انجام می‌شود (بدون قطعی؛ درخواست‌های در جریان PHP تمام می‌شوند)؛
   - `https://دامنه/healthz` باید جواب ربات Go را بدهد.
9. وبهوک با توکن مخفی دوباره ثبت می‌شود (آدرس همان است). از این به بعد فقط درخواست‌های واقعی تلگرام پذیرفته می‌شوند.

اولین کار زمان‌بندی‌شده‌ی ربات Go یک دقیقه بعد از روشن‌شدنش اجرا می‌شود. تا آن موقع درخواست‌های در جریان PHP تمام شده‌اند و
کرون‌های PHP هم برداشته شده‌اند، پس هیچ کاری (مثلاً تأیید یک رسید) دو بار انجام نمی‌شود.

### بعد از انتقال

- لاگ: `journalctl -u mitseller@7 -f`
- کلیدهای Mit Panel: `N=7 bash install.sh keys`
- گوشی تأیید خودکار **همان‌طور که بود کار می‌کند** (همان کلید و همان آدرس `/autopay.php`).
- اسکریپت بکاپ ساعتی همان‌طور کار می‌کند: برای این انتقال همان `/root/bot7_backup.sh` قبلی می‌ماند، و ربات تازه‌نصب
  `/root/mitseller7_backup.sh` می‌گیرد.
- پوشه‌ی PHP (`/var/www/html/botmirzapanel7`) پاک نمی‌شود؛ برای بازگشت لازم است.

## ربات‌های نصب‌شده قبل از بازنام‌گذاری

اگر روی همین سرور ربات‌هایی با نام‌های قبلی هست (`nexrabot@7`، تنظیمات در `/etc/nexrabot`، فایل اجرایی
`/usr/local/bin/nexrabot`)، این اسکریپت دست به‌شان نمی‌زند: هشدار می‌دهد و راهش را می‌رود. برای منتقل‌کردنشان به نام‌های تازه:

```bash
bash install.sh legacy-adopt
```

- شماره، پورت، دیتابیس، دامنه، سایت nginx، کرون‌ها و کلیدهای API همان می‌مانند؛ فقط نام‌ها عوض می‌شوند
  (`nexrabot@N` → `mitseller@N`، `/etc/nexrabot` → `/etc/mitseller`، اسنیپت `nexrabotN.conf` → `mitsellerN.conf`).
- پیش از دست‌زدن همه‌چیز در `/root/mitseller-backups/adopt-<تاریخ>` کپی می‌شود؛ اگر رباتِ منتقل‌شده جواب ندهد، خودش همان ربات را
  به نصب قبلی برمی‌گرداند.
- ربات‌هایی که منتقل نشده‌اند سر جایشان کار می‌کنند و فایل اجرایی و سرویس قبلی برای‌شان می‌ماند.

نام‌های قبلی همچنان پذیرفته می‌شوند، پس داده‌ها و اسکریپت‌های شما دست‌نخورده می‌مانند: متغیرهای محیطی `NEXRA_*` و `NEXRABOT_*`
(خوانده می‌شوند ولی `MIT_*` و `MITSELLER_*` مقدم‌اند)، جدول‌های `nexra_*`، ستون `type='nexra'` در پنل‌ها، و گام‌ها و دکمه‌هایی که
در دیتابیس با املای قبلی ذخیره شده‌اند.

## بازگشت به PHP

```bash
N=7 bash install.sh rollback
```

سایت nginx دقیقاً به فایل قبلی برمی‌گردد و reload می‌شود. وقتی PHP جواب داد، ربات Go خاموش می‌شود و خط‌های کرون PHP به crontab
برمی‌گردند. دیتابیس همان است، پس هر چه در این مدت در ربات Go اتفاق افتاده (خرید، شارژ، کاربر جدید) در PHP هم هست.

چیزهایی که فقط ربات Go دارد و PHP نادیده‌شان می‌گیرد: چیدمان/رنگ دکمه‌ها، صف پیام همگانی در جریان و تصویر رسیدها برای پنل. اگر در ربات
Go کرونی را روشن/خاموش کرده باشید، با بازگشت همان وضعیت crontab زمان انتقال برمی‌گردد.

## آزمون پیش از انتقال (اختیاری)

برای دیدن ربات Go روی کپی داده‌ها، بدون دست‌زدن به ربات اصلی:

```bash
mitseller db-dump -c /etc/mitseller/bot7.env --out /root/copy.sql.gz      # یا یک بکاپ قبلی
mysql -e "CREATE DATABASE bot7copy CHARACTER SET utf8mb4; GRANT ALL ON bot7copy.* TO '$(grep ^DB_USER= /etc/mitseller/bot7.env | cut -d= -f2)'@'localhost'"
cp /etc/mitseller/bot7.env /root/copy.env && sed -i 's/^DB_NAME=.*/DB_NAME=bot7copy/; s/^LISTEN=.*/LISTEN=127.0.0.1:18999/' /root/copy.env
mitseller import-sql -c /root/copy.env --file /root/copy.sql.gz
MITSELLER_DISABLE_CRONS=1 mitseller serve -c /root/copy.env &
curl -H "Authorization: Bearer $(grep ^API_OWNER_KEY /root/copy.env | cut -d= -f2)" http://127.0.0.1:18999/api/v1/stats
```

(این نسخه وبهوک نمی‌گیرد و فقط برای دیدن داده‌ها و API است؛ چون همان توکن را دارد، از آن پرداخت تأیید نکنید یا پیام نفرستید. بعد از آزمون: `kill %1` و `mysql -e "DROP DATABASE bot7copy"`.)

## انتقال دستی

اگر nginx شما چیدمان دیگری دارد (مثلاً ربات زیر یک مسیر مثل `domain/bot` است، یا Apache):

1. `mitseller migrate-php --php-dir /path/to/bot --out /etc/mitseller/bot7.env --listen 127.0.0.1:18007 --skip-db`
2. `mitseller db-dump -c /etc/mitseller/bot7.env --out /root/bot7.sql.gz`
3. `mitseller migrate-php --php-dir /path/to/bot --out /etc/mitseller/bot7.env --listen 127.0.0.1:18007 --clean-crontab=false`
4. `chown root:mitseller /etc/mitseller/bot7.env && chmod 640 /etc/mitseller/bot7.env`
5. خط‌های `/cron/` همین ربات را از `crontab -e` بردارید.
6. `systemctl enable --now mitseller@7`
7. در وب‌سرور، همه‌ی درخواست‌های این دامنه را به `http://127.0.0.1:18007` بفرستید (هدر `X-Real-IP` را بگذارید) و reload کنید.
8. `curl https://domain/healthz` باید `ok mitseller …` بدهد؛ بعد `mitseller set-webhook -c /etc/mitseller/bot7.env`.

ربات Go مسیرهای زیرشاخه را نمی‌شناسد؛ برای ربات زیر مسیر، در nginx پیشوند را حذف کنید (`proxy_pass http://127.0.0.1:18007/;` در
`location /bot/`) و آدرس وبهوک را با `setWebhook` خودتان تنظیم کنید.

## از یک بکاپ (سرور جدید)

```bash
N=7 TOKEN=… DOMAIN=… ADMIN=… MIT_SECRET=… CERT_EMAIL=… bash install.sh     # ربات تازه
mitseller import-sql -c /etc/mitseller/bot7.env --file mirzabot7_backup.sql.gz --force
systemctl restart mitseller@7
```

`import-sql` روی دیتابیسی که کاربر دارد بدون `--force` کار نمی‌کند، تا اشتباهی چیزی پاک نشود.
