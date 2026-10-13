#!/usr/bin/env bash
# Sandbox test for the migrate path of install.sh: runs it against a throwaway
# tree with systemctl/ss/nginx/curl/crontab stubbed and a fake mitseller binary,
# and checks that it finds a mirzapanel main bot, a mirzapanel "additional bot"
# and a mirza_pro install, gives each a free number, mints a MIT_SECRET and
# stops the PHP cron lines of both layouts.
#
#   bash tests/install/migrate_mirza_test.sh
#
# No root, no systemd, no MySQL, no network: everything happens under a mktemp
# dir and the bot binary is a shell stub.
set -uo pipefail

REPO=$(cd "$(dirname "$0")/../.." && pwd)
# resolved, so the sandbox paths in the nginx sites match the ones
# php_bot_dirs prints after readlink -f
SB=$(readlink -f "$(mktemp -d "${TMPDIR:-/tmp}/mit-migrate.XXXXXX")")
trap 'rm -rf "${SB:?}"' EXIT
FAILS=0

ok()   { printf '  [ok]   %s\n' "$1"; }
fail() { printf '  [FAIL] %s\n' "$1"; FAILS=$((FAILS+1)); }

fresh() {
    rm -rf "${SB:?}"
    mkdir -p "$SB/bin" "$SB/state" \
             "$SB/etc/mitseller" \
             "$SB/etc/nginx/sites-enabled" "$SB/etc/nginx/sites-available" \
             "$SB/etc/nginx/snippets" "$SB/etc/nginx/conf.d" \
             "$SB/etc/systemd" "$SB/usr/local/bin" "$SB/var/lib" "$SB/var/www/html" \
             "$SB/root"
    : > "$SB/state/units"
    : > "$SB/state/crontab"
    make_stubs
    make_script
    make_fake_binary
}

stub() { # name  (template on stdin, @@SB@@ = sandbox root)
    sed "s|@@SB@@|$SB|g" > "$SB/bin/$1"
    chmod +x "$SB/bin/$1"
}

make_stubs() {
    stub systemctl <<'EOF'
#!/bin/bash
S=@@SB@@/state/units
norm() { echo "${1%.service}"; }
cmd=${1:-}; shift || true
case "$cmd" in
  list-units)
    pat=""
    for a in "$@"; do case "$a" in --*) ;; *) pat=$a ;; esac; done
    pat=${pat%.service}
    while IFS='|' read -r u en ac; do
      case "$u" in
        $pat)
          state=inactive; [ "$ac" = 1 ] && state=active
          printf '%s.service loaded %s running - sandbox\n' "$u" "$state" ;;
      esac
    done < "$S" ;;
  is-active)
    [ "${1:-}" = --quiet ] && shift
    u=$(norm "$1")
    awk -F'|' -v u="$u" '$1==u && $3==1 {f=1} END{exit !f}' "$S" ;;
  is-enabled)
    [ "${1:-}" = --quiet ] && shift
    u=$(norm "$1")
    awk -F'|' -v u="$u" '$1==u && $2==1 {f=1} END{exit !f}' "$S" ;;
  enable|disable)
    [ "${1:-}" = --now ] && shift
    u=$(norm "$1")
    v=1; [ "$cmd" = disable ] && v=0
    awk -F'|' -v u="$u" -v v="$v" '$1==u {print u"|"v"|"v; f=1; next} {print}
        END{if(!f) print u"|"v"|"v}' "$S" > "$S.t"
    mv "$S.t" "$S" ;;
  *) exit 0 ;;
esac
EOF
    stub ss <<'EOF'
#!/bin/bash
while IFS='|' read -r u en ac; do
  [ "$ac" = 1 ] || continue
  case "$u" in
    *@*) n=${u##*@}; printf 'LISTEN 0 128 127.0.0.1:%d 0.0.0.0:*\n' $((18000+n)) ;;
  esac
done < @@SB@@/state/units
exit 0
EOF
    stub nginx <<'EOF'
#!/bin/bash
exit 0
EOF
    stub curl <<'EOF'
#!/bin/bash
url=""
for a in "$@"; do case "$a" in http*) url=$a ;; esac; done
case "$url" in
  *healthz*) echo "ok mitseller 7.0.0-go"; exit 0 ;;
esac
exit 0
EOF
    stub id <<'EOF'
#!/bin/bash
[ "${1:-}" = "-u" ] && { echo 0; exit 0; }
exit 1
EOF
    stub useradd <<'EOF'
#!/bin/bash
exit 0
EOF
    stub chown <<'EOF'
#!/bin/bash
exit 0
EOF
    stub getent <<'EOF'
#!/bin/bash
exit 1
EOF
    stub docker <<'EOF'
#!/bin/bash
exit 0
EOF
    stub runuser <<'EOF'
#!/bin/bash
while [ $# -gt 0 ]; do case "$1" in --) shift; break ;; *) shift ;; esac; done
exec "$@"
EOF
    stub crontab <<'EOF'
#!/bin/bash
T=@@SB@@/state/crontab
case "${1:-}" in
  -l) cat "$T" ;;
  -)  cat > "$T" ;;
esac
exit 0
EOF
}

make_script() {
    python3 - "$REPO/install.sh" "$SB/install.sh" "$SB" <<'PY'
import re, sys
src, dst, sb = sys.argv[1], sys.argv[2], sys.argv[3]
s = open(src).read()
pairs = [
 ('BIN=/usr/local/bin/mitseller', 'BIN=%s/usr/local/bin/mitseller' % sb),
 ('ETC=/etc/mitseller', 'ETC=%s/etc/mitseller' % sb),
 ('BACKUPS=/root/mitseller-backups', 'BACKUPS=%s/root/mitseller-backups' % sb),
 ('UNIT=/etc/systemd/system/mitseller@.service', 'UNIT=%s/etc/systemd/mitseller@.service' % sb),
 ('/usr/local/bin/nexrabot', '%s/usr/local/bin/nexrabot' % sb),
 ('/usr/local/bin/mitseller', '%s/usr/local/bin/mitseller' % sb),
 ('/etc/nexrabot', '%s/etc/nexrabot' % sb),
 ('/etc/nginx', '%s/etc/nginx' % sb),
 ('/var/lib/mitseller', '%s/var/lib/mitseller' % sb),
 ('/var/lib/nexrabot', '%s/var/lib/nexrabot' % sb),
 ('/var/www/html', '%s/var/www/html' % sb),
 ('/root/mitseller', '%s/root/mitseller' % sb),
]
# one pass only, so an already-rewritten path is never rewritten again
table = {a: b for a, b in pairs}
s = re.sub('|'.join(sorted(map(re.escape, table), key=len, reverse=True)),
           lambda m: table[m.group(0)], s)
open(dst, 'w').write(s)
PY
    chmod +x "$SB/install.sh"
}

# The bot binary the installer drives. It reads config.php for the domain and
# writes the config/report files the installer expects, so no MySQL is needed.
make_fake_binary() {
    cat > "$SB/bin/fake-mitseller" <<'EOF'
#!/bin/bash
cmd=${1:-}; shift || true
out="" phpdir="" report=""
while [ $# -gt 0 ]; do
  case "$1" in
    --out) out=$2; shift 2 ;;
    --php-dir) phpdir=$2; shift 2 ;;
    --report) report=$2; shift 2 ;;
    *) shift ;;
  esac
done
domain_of() { sed -n "s/.*domainhosts[[:space:]]*=[[:space:]]*[\"']\([^\"']*\)[\"'].*/\1/p" "$1/config.php" | head -1; }
case "$cmd" in
  version) echo "7.0.0-go" ;;
  migrate-php)
    [ -n "$out" ] || exit 1
    {
      printf 'DOMAIN=%s\n' "$(domain_of "$phpdir")"
      printf 'BOT_TOKEN=1:x\nADMIN_ID=5\nBOT_USERNAME=fake\n'
      printf 'MIT_SECRET=%s\n' "${MIT_SECRET:-minted-by-the-binary}"
      printf 'API_OWNER_KEY=owner\nAPI_MANAGER_KEY=manager\nWEBHOOK_SECRET=hook\n'
    } > "$out"
    [ -n "$report" ] && printf 'what the migration changed\n  - schema: added column setting.help_Status\n' > "$report"
    exit 0 ;;
  db-dump) [ -n "$out" ] && : > "$out"; exit 0 ;;
  check) exit 0 ;;
  serve) exec sleep 20 ;;
  *) exit 0 ;;
esac
EOF
    chmod +x "$SB/bin/fake-mitseller"
}

seed_php_bot() { # dir domain [flavour=legacy]
    local dir=$1 domain=$2 flavour=${3:-legacy}
    mkdir -p "$dir"
    cat > "$dir/config.php" <<EOF
<?php
\$APIKEY = '1:x';
\$adminnumber = '5';
\$domainhosts = '$domain';
\$usernamebot = '@fake';
\$dbname = 'mirzabot';
\$usernamedb = 'u';
\$passworddb = 'p';
EOF
    [ "$flavour" = mirza ] && mkdir -p "$dir/cronbot"
    true
}

seed_site() { # site-file php-dir
    cat > "$1" <<EOF
server {
    listen 80;
    server_name x;
    root $2;
    index index.php;
    location / {
        try_files \$uri \$uri/ /index.php?\$query_string;
    }
    location ~ \\.php\$ {
        include fastcgi_params;
        fastcgi_pass unix:/run/php/php8.2-fpm.sock;
        fastcgi_param SCRIPT_FILENAME \$document_root\$fastcgi_script_name;
    }
}
EOF
    ln -sf "$1" "$SB/etc/nginx/sites-enabled/$(basename "$1")"
}

seed_crontab() { # domain
    local domain=$1
    {
        echo "0 * * * * bash /root/someones_backup.sh"
        echo "*/15 * * * * curl https://$domain/cron/configtest.php"
        echo "*/1 * * * * curl https://$domain/cron/cronvolume.php"
        echo "*/1 * * * * curl https://$domain/cronbot/statusday.php"
        echo "*/1 * * * * curl https://$domain/cronbot/NoticationsService.php"
        echo "*/5 * * * * curl https://$domain/cronbot/uptime_panel.php"
        echo "*/1 * * * * curl https://unrelated.example.com/cron/cronday.php"
    } >> "$SB/state/crontab"
}

run() { PATH="$SB/bin:$PATH" MITSELLER_BIN="$SB/bin/fake-mitseller" bash "$SB/install.sh" "$@"; }

# Three bots, named so the sorted order of php_bot_dirs is the order of the
# numbers they are given: aaa (nexra/mirzapanel main), bbb (a mirzapanel
# "additional bot" under the operator's own name), ccc (mirza_pro).
seed_three() {
    seed_php_bot "$SB/var/www/html/aaa_main"        "bot1.example.com"
    seed_php_bot "$SB/var/www/html/bbb_shop"        "bot2.example.com"
    seed_php_bot "$SB/var/www/html/ccc_mirza_pro"   "bot3.example.com" mirza
    seed_site "$SB/etc/nginx/sites-available/bot1" "$SB/var/www/html/aaa_main"
    seed_site "$SB/etc/nginx/sites-available/bot2" "$SB/var/www/html/bbb_shop"
    seed_site "$SB/etc/nginx/sites-available/bot3" "$SB/var/www/html/ccc_mirza_pro"
}

# ---------------------------------------------------------------------------

echo "== A: mirzapanel main bot, an additional bot and mirza_pro, all migrated =="
fresh
seed_three
seed_crontab "bot1.example.com"
out=$(run migrate-all 2>&1); rc=$?
[ $rc -eq 0 ] && ok "exit 0" || { fail "exit $rc"; echo "$out" | tail -20; }
grep -q "bot 1 ($SB/var/www/html/aaa_main)" <<<"$out" && ok "main bot found" || fail "main bot: $out"
grep -q "bot 2 ($SB/var/www/html/bbb_shop)" <<<"$out" && ok "additional bot found under its own name" || fail "additional bot: $out"
grep -q "bot 3 ($SB/var/www/html/ccc_mirza_pro)" <<<"$out" && ok "mirza_pro found" || fail "mirza_pro: $out"
grep -q "moved to Go:        1 2 3" <<<"$out" && ok "three free numbers handed out in order" || fail "numbers: $out"
for n in 1 2 3; do
    [ -f "$SB/etc/mitseller/bot$n.env" ] && ok "bot$n.env written" || fail "bot$n.env missing"
    [ -f "$SB/etc/mitseller/bot$n.phpdir" ] && ok "bot$n.phpdir records the source" || fail "bot$n.phpdir missing"
done
grep -q "^$SB/var/www/html/bbb_shop$" "$SB/etc/mitseller/bot2.phpdir" && ok "phpdir holds the directory" || fail "phpdir content"
grep -q "secret:" <<<"$out" && ok "the secret is printed with the keys" || fail "secret not printed: $out"
[ ! -f "$SB/etc/mitseller/bot4.env" ] && ok "no fourth bot invented" || fail "extra bot created"

echo "== B: cron lines Go reimplements are gone, the rest keep running =="
# the four the Go bot runs itself are dropped
grep -q "bot1.example.com/cron/configtest.php" "$SB/state/crontab" && fail "configtest.php left" || ok "configtest.php removed (Go runs it)"
grep -q "bot1.example.com/cron/cronvolume.php" "$SB/state/crontab" && fail "cronvolume.php left" || ok "cronvolume.php removed (Go runs it)"
grep -q "bot1.example.com/cronbot/NoticationsService.php" "$SB/state/crontab" && fail "NoticationsService.php left" || ok "NoticationsService.php removed (Go runs it)"
# the Go bot has no job for these; dropping them would stop them silently
grep -q "bot1.example.com/cronbot/statusday.php" "$SB/state/crontab" && ok "statusday.php left running (no Go job)" || fail "statusday.php was dropped"
grep -q "bot1.example.com/cronbot/uptime_panel.php" "$SB/state/crontab" && ok "uptime_panel.php left running (no Go job)" || fail "uptime_panel.php was dropped"
grep -q "unrelated.example.com/cron/" "$SB/state/crontab" && ok "another domain's cron left alone" || fail "unrelated cron removed"
grep -q "someones_backup.sh" "$SB/state/crontab" && ok "a plain backup cron left alone" || fail "backup cron removed"
[ -f "$SB/etc/mitseller/bot1.crontab.bak" ] && ok "old crontab kept for rollback" || fail "no crontab backup"

echo "== C: a second run migrates nothing twice =="
out=$(run migrate-all 2>&1); rc=$?
[ $rc -eq 0 ] && ok "exit 0" || fail "exit $rc"
grep -q "already runs on Go" <<<"$out" && ok "says the bots are already on Go" || fail "message: $out"
grep -q "moved to Go:        1 2 3" <<<"$out" && fail "tried to migrate them again" || ok "nothing new moved"

echo "== D: MIT_SECRET is generated when the PHP bot never wrote one =="
fresh
seed_php_bot "$SB/var/www/html/botmirzapanel7" "bot7.example.com"
seed_site "$SB/etc/nginx/sites-available/bot7" "$SB/var/www/html/botmirzapanel7"
out=$(PHP_DIR="$SB/var/www/html/botmirzapanel7" run migrate 2>&1); rc=$?
[ $rc -eq 0 ] && ok "exit 0" || { fail "exit $rc"; echo "$out" | tail -10; }
grep -q "using bot number 1" <<<"$out" && ok "N was picked when PHP_DIR was given" || fail "number: $out"
grep -q "^MIT_SECRET=" "$SB/etc/mitseller/bot1.env" && ok "a secret landed in the config" || fail "no MIT_SECRET"
sec=$(sed -n 's/^MIT_SECRET=//p' "$SB/etc/mitseller/bot1.env")
[ -n "$sec" ] && ok "the secret is not empty" || fail "empty MIT_SECRET"
ls "$SB"/root/mitseller-backups/bot1-*.migrate-report.txt >/dev/null 2>&1 && ok "the migration report was written" || fail "no report file"
grep -q "what changed:" <<<"$out" && ok "the report path is pointed out" || fail "report path not mentioned: $out"

echo "== E: MIT_SECRET from the environment is used as it is =="
fresh
seed_php_bot "$SB/var/www/html/botmirzapanel7" "bot7.example.com"
seed_site "$SB/etc/nginx/sites-available/bot7" "$SB/var/www/html/botmirzapanel7"
MIT_SECRET='operator-chosen' PHP_DIR="$SB/var/www/html/botmirzapanel7" run migrate >/dev/null 2>&1
grep -q "^MIT_SECRET=operator-chosen$" "$SB/etc/mitseller/bot1.env" && ok "the given secret is kept" || fail "config: $(cat "$SB/etc/mitseller/bot1.env" 2>/dev/null)"

echo "== F: an already-running number is never reused =="
fresh
seed_php_bot "$SB/var/www/html/botmirzapanel1" "bot1.example.com"
seed_site "$SB/etc/nginx/sites-available/bot1" "$SB/var/www/html/botmirzapanel1"
echo "mitseller@1|1|1" >> "$SB/state/units"
seed_php_bot "$SB/var/www/html/botmirzapanel2" "bot2.example.com"
seed_site "$SB/etc/nginx/sites-available/bot2" "$SB/var/www/html/botmirzapanel2"
out=$(run migrate-all 2>&1)
grep -q "bot 1 ($SB/var/www/html/botmirzapanel1) already runs on Go" <<<"$out" && ok "the running bot is skipped" || fail "message: $out"
grep -q "moved to Go:        2" <<<"$out" && ok "bot 2 was given a free number" || fail "summary: $out"

echo "== G: a bot behind a path URL is refused with the reason =="
fresh
mkdir -p "$SB/var/www/html/mirzabotconfig"
seed_php_bot "$SB/var/www/html/mirzabotconfig" "example.com/mirzabotconfig"
seed_site "$SB/etc/nginx/sites-available/panel" "$SB/var/www/html/mirzabotconfig"
out=$(PHP_DIR="$SB/var/www/html/mirzabotconfig" run migrate 2>&1); rc=$?
[ $rc -ne 0 ] && ok "exit non-zero" || fail "exit 0 despite the path URL"
grep -q "served under a path" <<<"$out" && ok "says why it stopped" || fail "message: $out"
grep -q "example.com/mirzabotconfig" <<<"$out" && ok "quotes the path it found" || fail "message: $out"
[ ! -f "$SB/etc/mitseller/bot1.env" ] && ok "no config was written" || fail "config written anyway"

echo "== H: no nginx site with this bot's root -> refused with the fix =="
fresh
seed_php_bot "$SB/var/www/html/myshop" "bot2.example.com"
out=$(PHP_DIR="$SB/var/www/html/myshop" run migrate 2>&1); rc=$?
[ $rc -ne 0 ] && ok "exit non-zero" || fail "exit 0 despite no site"
grep -q "no nginx site serves" <<<"$out" && ok "says the site is missing" || fail "message: $out"
grep -q "root $SB/var/www/html/myshop" <<<"$out" && ok "shows the root the site needs" || fail "message: $out"

echo "== I: MIGRATE_DIRS picks up a bot outside /var/www/html =="
fresh
mkdir -p "$SB/srv/bots"
seed_php_bot "$SB/srv/bots/elsewhere" "bot9.example.com"
seed_site "$SB/etc/nginx/sites-available/elsewhere" "$SB/srv/bots/elsewhere"
out=$(MIGRATE_DIRS="$SB/srv/bots/elsewhere" run migrate-all 2>&1); rc=$?
[ $rc -eq 0 ] && ok "exit 0" || { fail "exit $rc"; echo "$out" | tail -10; }
grep -q "moved to Go:        1" <<<"$out" && ok "the extra directory was migrated" || fail "summary: $out"
grep -q "^$SB/srv/bots/elsewhere$" "$SB/etc/mitseller/bot1.phpdir" && ok "phpdir records the extra directory" || fail "phpdir"

echo "== J: rollback restores what was removed without duplicating what was not =="
fresh
seed_php_bot "$SB/var/www/html/botmirzapanel1" "bot1.example.com"
seed_site "$SB/etc/nginx/sites-available/bot1" "$SB/var/www/html/botmirzapanel1"
seed_crontab "bot1.example.com"
PHP_DIR="$SB/var/www/html/botmirzapanel1" run migrate >/dev/null 2>&1
grep -q "bot1.example.com/cron/configtest.php" "$SB/state/crontab" && fail "cron lines were not removed" || ok "cron lines removed"
out=$(N=1 run rollback 2>&1); rc=$?
[ $rc -eq 0 ] && ok "rollback exit 0" || { fail "rollback exit $rc"; echo "$out" | tail -5; }
grep -q "bot1.example.com/cron/configtest.php" "$SB/state/crontab" && ok "/cron/ lines restored" || fail "/cron/ not restored"
grep -q "bot1.example.com/cronbot/NoticationsService.php" "$SB/state/crontab" && ok "/cronbot/ lines restored" || fail "/cronbot/ not restored"
grep -q "someones_backup.sh" "$SB/state/crontab" && ok "the unrelated backup cron survived the round trip" || fail "backup cron lost"
# the jobs Go never took over were never removed, so rollback must not stack
# a second copy of them
n=$(grep -c "bot1.example.com/cronbot/statusday.php" "$SB/state/crontab")
[ "$n" -eq 1 ] && ok "a job Go does not implement is not duplicated by rollback" || fail "statusday.php duplicated: $n copies"
n=$(grep -c "bot1.example.com/cronbot/uptime_panel.php" "$SB/state/crontab")
[ "$n" -eq 1 ] && ok "uptime_panel.php is not duplicated by rollback" || fail "uptime_panel.php duplicated: $n copies"
# rolling back twice must not stack the same lines
N=1 run rollback >/dev/null 2>&1
n=$(grep -c "bot1.example.com/cron/configtest.php" "$SB/state/crontab")
[ "$n" -eq 1 ] && ok "a second rollback leaves one copy of the lines" || fail "duplicated: $n copies"

echo
if [ "$FAILS" -eq 0 ]; then
    echo "migrate sandbox tests: all passed"
else
    echo "migrate sandbox tests: $FAILS failed"
fi
exit $((FAILS > 0))
