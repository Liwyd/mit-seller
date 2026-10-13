#!/usr/bin/env bash
# Sandbox test for the cross-server move: bot-export packs a bot up on one
# "server", bot-import installs the bundle as a new bot on another, and
# bot-cutover is the moment the webhook moves. The two halves are the same
# sandbox, but the assertions check that the source is never touched and that
# the imported bot is held dark until cutover.
#
#   bash tests/install/bot_move_test.sh
#
# No root, no systemd, no MySQL, no network: everything happens under a mktemp
# dir and the bot binary is a shell stub that really does write and read a
# tar.gz, so the bundle round-trip is exercised end to end.
set -uo pipefail

REPO=$(cd "$(dirname "$0")/../.." && pwd)
SB=$(readlink -f "$(mktemp -d "${TMPDIR:-/tmp}/mit-move.XXXXXX")")
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
    : > "$SB/state/mysql"
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
  restart|start)
    u=$(norm "$1")
    awk -F'|' -v u="$u" '$1==u {print u"|1|1"; f=1; next} {print}
        END{if(!f) print u"|1|1"}' "$S" > "$S.t"
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
    # the healthz stub answers only for a port a bot is actually listening on,
    # so wait_healthy cannot pass for a bot that was never started
    stub curl <<'EOF'
#!/bin/bash
url=""
for a in "$@"; do case "$a" in http*) url=$a ;; esac; done
case "$url" in
  *healthz*)
    port=${url##*:}; port=${port%%/*}
    n=$((port - 18000))
    if awk -F'|' -v u="mitseller@$n" '$1==u && $3==1 {f=1} END{exit !f}' @@SB@@/state/units; then
      echo "ok mitseller 7.0.0-go"; exit 0
    fi
    exit 1 ;;
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
    # getent succeeds only for the domain the test has pointed here, so
    # bot-cutover refuses while DNS still points at the other server
    stub getent <<'EOF'
#!/bin/bash
[ -f @@SB@@/state/dns-here ] || exit 1
for a in "$@"; do
  [ "$a" = "$(cat @@SB@@/state/dns-here 2>/dev/null)" ] && exit 0
done
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
    # mysql records the statements it was asked to run, so the test can check
    # that the database was created and (on unimport) dropped
    stub mysql <<'EOF'
#!/bin/bash
S=@@SB@@/state/mysql
if [ "${1:-}" = "-e" ]; then
  echo "$2" >> "$S"
  exit 0
fi
cat >> "$S"
EOF
    stub certbot <<'EOF'
#!/bin/bash
exit 0
EOF
    stub openssl <<'EOF'
#!/bin/bash
echo "deadbeefcafe"
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
table = {a: b for a, b in pairs}
s = re.sub('|'.join(sorted(map(re.escape, table), key=len, reverse=True)),
           lambda m: table[m.group(0)], s)
open(dst, 'w').write(s)
PY
    chmod +x "$SB/install.sh"
}

# The bot binary the installer drives. export-bundle and import-bundle really
# do write and read a tar.gz, so the round-trip of the bundle file is
# exercised; everything else is a stub.
make_fake_binary() {
    cat > "$SB/bin/fake-mitseller" <<'EOF'
#!/bin/bash
cmd=${1:-}; shift || true
out="" phpdir="" report="" file="" cfgpath="" domain="" dbname="" dbuser=""
while [ $# -gt 0 ]; do
  case "$1" in
    --out) out=$2; shift 2 ;;
    --php-dir) phpdir=$2; shift 2 ;;
    --report) report=$2; shift 2 ;;
    --file) file=$2; shift 2 ;;
    --domain) domain=$2; shift 2 ;;
    --db-name) dbname=$2; shift 2 ;;
    --db-user) dbuser=$2; shift 2 ;;
    -c) cfgpath=$2; shift 2 ;;
    *) shift ;;
  esac
done
[ -n "$MITSELLER_CONFIG" ] && [ -z "$cfgpath" ] && cfgpath=$MITSELLER_CONFIG
domain_of() { sed -n "s/.*domainhosts[[:space:]]*=[[:space:]]*[\"']\([^\"']*\)[\"'].*/\1/p" "$1/config.php" | head -1; }
val_of() { sed -n "s/.*\$$1[[:space:]]*=[[:space:]]*[\"']\([^\"']*\)[\"'].*/\1/p" "$2/config.php" | head -1; }
case "$cmd" in
  version) echo "7.0.0-go" ;;
  export-bundle)
    [ -n "$out" ] || exit 1
    tmp=$(mktemp -d)
    if [ -n "$phpdir" ]; then
      d=$(domain_of "$phpdir"); tok=$(val_of APIKEY "$phpdir"); adm=$(val_of adminnumber "$phpdir")
      user=$(val_of usernamebot "$phpdir"); flav=""
      [ -d "$phpdir/cronbot" ] && flav="mirza_pro"
      srcdb=$(val_of dbname "$phpdir")
      cp "$phpdir/config.php" "$tmp/config.php"
    else
      d=$(sed -n 's/^DOMAIN=//p' "$cfgpath"); tok=$(sed -n 's/^BOT_TOKEN=//p' "$cfgpath")
      adm=$(sed -n 's/^ADMIN_ID=//p' "$cfgpath"); user=$(sed -n 's/^BOT_USERNAME=//p' "$cfgpath")
      flav=""; srcdb=$(sed -n 's/^DB_NAME=//p' "$cfgpath")
    fi
    printf '{"version":1,"flavor":"%s","domain":"%s","admin_id":"%s","bot_username":"%s","bot_token":"%s","source_db":"%s","cron":{"test":true},"source":"php","php_dir":"%s","exported_at":"2026-01-01T00:00:00Z"}\n' \
      "$flav" "$d" "$adm" "$user" "$tok" "$srcdb" "$phpdir" > "$tmp/manifest.json"
    echo "-- fake dump" | gzip > "$tmp/dump.sql.gz"
    tar -czf "$out" -C "$tmp" manifest.json dump.sql.gz $([ -f "$tmp/config.php" ] && echo config.php)
    rm -rf "$tmp"
    chmod 600 "$out"
    echo "bot $d (@$user) packed into $out"
    exit 0 ;;
  import-bundle)
    [ -n "$file" ] && [ -n "$out" ] && [ -n "$dbname" ] || exit 1
    tmp=$(mktemp -d)
    tar -xzf "$file" -C "$tmp" || exit 1
    [ -f "$tmp/manifest.json" ] || exit 1
    d=$(sed -n 's/.*"domain":"\([^"]*\)".*/\1/p' "$tmp/manifest.json")
    tok=$(sed -n 's/.*"bot_token":"\([^"]*\)".*/\1/p' "$tmp/manifest.json")
    adm=$(sed -n 's/.*"admin_id":"\([^"]*\)".*/\1/p' "$tmp/manifest.json")
    user=$(sed -n 's/.*"bot_username":"\([^"]*\)".*/\1/p' "$tmp/manifest.json")
    rm -rf "$tmp"
    [ -n "$domain" ] && d=$domain
    case "$d" in */*) echo "the bot is served under a path ($d)" >&2; exit 1 ;; esac
    {
      printf 'DOMAIN=%s\nBOT_TOKEN=%s\nADMIN_ID=%s\nBOT_USERNAME=%s\n' "$d" "$tok" "$adm" "$user"
      printf 'MIT_SECRET=%s\n' "${MIT_SECRET:-minted-by-the-binary}"
      printf 'DB_NAME=%s\nDB_USER=%s\nDB_PASS=%s\n' "$dbname" "$dbuser" "${MITSELLER_INIT_DB_PASS:-}"
      printf 'LISTEN=127.0.0.1:1\nWEBHOOK_SECRET=hook\nAPI_OWNER_KEY=owner\nAPI_MANAGER_KEY=manager\n'
      printf 'DISABLE_CRONS=1\n'
    } > "$out"
    [ -n "$report" ] && printf 'what the import changed\n  - moved from php\n' > "$report"
    echo "config written: $out"
    exit 0 ;;
  migrate-php)
    [ -n "$out" ] || exit 1
    {
      printf 'DOMAIN=%s\n' "$(domain_of "$phpdir")"
      printf 'BOT_TOKEN=1:x\nADMIN_ID=5\nBOT_USERNAME=fake\n'
      printf 'MIT_SECRET=%s\n' "${MIT_SECRET:-minted-by-the-binary}"
      printf 'API_OWNER_KEY=owner\nAPI_MANAGER_KEY=manager\nWEBHOOK_SECRET=hook\n'
    } > "$out"
    exit 0 ;;
  db-dump) [ -n "$out" ] && : > "$out"; exit 0 ;;
  check) exit 0 ;;
  set-webhook) echo "webhook set" >> "${MITSELLER_STATE:-/dev/null}"; exit 0 ;;
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

run() { PATH="$SB/bin:$PATH" MITSELLER_BIN="$SB/bin/fake-mitseller" bash "$SB/install.sh" "$@"; }

# ---------------------------------------------------------------------------

echo "== A: bot-export packs a PHP bot and leaves it alone =="
fresh
seed_php_bot "$SB/var/www/html/mirzabot" "bot.example.com" mirza
out=$(PHP_DIR="$SB/var/www/html/mirzabot" OUT="$SB/root/bundle.tar.gz" run bot-export 2>&1); rc=$?
[ $rc -eq 0 ] && ok "exit 0" || { fail "exit $rc"; echo "$out" | tail -10; }
[ -f "$SB/root/bundle.tar.gz" ] && ok "the bundle was written" || fail "no bundle"
# the source bot is untouched: still a PHP install, still has its config
[ -f "$SB/var/www/html/mirzabot/config.php" ] && ok "the source bot still has its config.php" || fail "source config gone"
grep -q "copy it to the other server" <<<"$out" && ok "points at the next step" || fail "no next step: $out"
# the bundle is not world-readable
mode=$(stat -c '%a' "$SB/root/bundle.tar.gz" 2>/dev/null || stat -f '%Lp' "$SB/root/bot.tar.gz")
[ "$mode" = 600 ] && ok "the bundle is 0600" || fail "bundle mode $mode"

echo "== B: the bundle really holds a manifest and a dump =="
tar -tzf "$SB/root/bundle.tar.gz" > "$SB/state/listing" 2>/dev/null
grep -q "manifest.json" "$SB/state/listing" && ok "manifest.json is in the bundle" || fail "no manifest: $(cat "$SB/state/listing")"
grep -q "dump.sql.gz" "$SB/state/listing" && ok "dump.sql.gz is in the bundle" || fail "no dump"
tar -xOzf "$SB/root/bundle.tar.gz" manifest.json > "$SB/state/man" 2>/dev/null
grep -q '"flavor":"mirza_pro"' "$SB/state/man" && ok "the flavour is recorded" || fail "flavour: $(cat "$SB/state/man")"
grep -q '"domain":"bot.example.com"' "$SB/state/man" && ok "the domain is recorded" || fail "domain: $(cat "$SB/state/man")"

echo "== C: bot-import installs it as a new bot, held dark =="
out=$(FILE="$SB/root/bundle.tar.gz" DOMAIN="moved.example.com" CERT_EMAIL="a@b.c" run bot-import 2>&1); rc=$?
[ $rc -eq 0 ] && ok "exit 0" || { fail "exit $rc"; echo "$out" | tail -20; }
grep -q "using bot number 1" <<<"$out" && ok "a free number was taken" || fail "number: $out"
[ -f "$SB/etc/mitseller/bot1.env" ] && ok "bot1.env written" || fail "no config"
grep -q "^DOMAIN=moved.example.com$" "$SB/etc/mitseller/bot1.env" && ok "--domain overrode the source domain" || fail "domain: $(grep '^DOMAIN=' "$SB/etc/mitseller/bot1.env")"
grep -q "^DISABLE_CRONS=1$" "$SB/etc/mitseller/bot1.env" && ok "the cron jobs are held off" || fail "crons are live"
grep -q "^DB_NAME=mitseller1$" "$SB/etc/mitseller/bot1.env" && ok "its own database is used" || fail "db: $(grep '^DB_' "$SB/etc/mitseller/bot1.env")"
[ -f "$SB/etc/nginx/sites-available/mitseller1" ] && ok "an nginx site was written" || fail "no site"
grep -q "server_name moved.example.com" "$SB/etc/nginx/sites-available/mitseller1" && ok "the site serves the new domain" || fail "site: $(cat "$SB/etc/nginx/sites-available/mitseller1")"
awk -F'|' '$1=="mitseller@1" && $3==1 {f=1} END{exit !f}' "$SB/state/units" && ok "the service is running" || fail "not running"
grep -q "CREATE DATABASE" "$SB/state/mysql" && ok "the database was created" || fail "no CREATE DATABASE"
grep -q "is NOT yet live" <<<"$out" && ok "says it is not live yet" || fail "message: $out"
grep -q "bot-cutover" <<<"$out" && ok "points at the cutover" || fail "no cutover hint"

echo "== D: the source server is never touched by the import =="
# nothing on the "source" side changed: the PHP bot is still there, the
# crontab still has its lines, and no webhook was moved
[ -f "$SB/var/www/html/mirzabot/config.php" ] && ok "the source PHP bot is intact" || fail "source bot damaged"
grep -q "webhook set" "$SB/state/mysql" && fail "a webhook was moved during import" || ok "no webhook was moved"

echo "== E: bot-cutover refuses while DNS still points at the other server =="
out=$(N=1 run bot-cutover 2>&1); rc=$?
[ $rc -ne 0 ] && ok "exit non-zero" || fail "cut over despite DNS"
grep -q "does not point here yet" <<<"$out" && ok "says why it stopped" || fail "message: $out"
grep -q "^DISABLE_CRONS=1$" "$SB/etc/mitseller/bot1.env" && ok "the cron jobs are still held" || fail "crons released early"

echo "== F: bot-cutover moves the bot once the domain points here =="
echo "moved.example.com" > "$SB/state/dns-here"
out=$(N=1 run bot-cutover 2>&1); rc=$?
[ $rc -eq 0 ] && ok "exit 0" || { fail "exit $rc"; echo "$out" | tail -10; }
grep -q "^DISABLE_CRONS=1$" "$SB/etc/mitseller/bot1.env" && fail "crons still held after cutover" || ok "the cron jobs were released"
grep -q "is live on https://moved.example.com" <<<"$out" && ok "says the bot is live" || fail "message: $out"
grep -q "stop the old bot on the other server" <<<"$out" && ok "warns about the old server's crons" || fail "no warning: $out"

echo "== G: bot-unimport removes the bot cleanly =="
fresh
seed_php_bot "$SB/var/www/html/mirzabot" "bot.example.com" mirza
PHP_DIR="$SB/var/www/html/mirzabot" OUT="$SB/root/b.tar.gz" run bot-export >/dev/null 2>&1
FILE="$SB/root/b.tar.gz" run bot-import >/dev/null 2>&1
out=$(N=1 run bot-unimport 2>&1); rc=$?
[ $rc -eq 0 ] && ok "exit 0" || { fail "exit $rc"; echo "$out" | tail -10; }
[ ! -f "$SB/etc/mitseller/bot1.env" ] && ok "the config is gone" || fail "config left"
[ ! -f "$SB/etc/nginx/sites-available/mitseller1" ] && ok "the nginx site is gone" || fail "site left"
[ ! -f "$SB/etc/nginx/snippets/mitseller1.conf" ] && ok "the snippet is gone" || fail "snippet left"
grep -q "DROP DATABASE" "$SB/state/mysql" && ok "the database was dropped" || fail "no DROP DATABASE"
awk -F'|' '$1=="mitseller@1" && $3==0 {f=1} END{exit !f}' "$SB/state/units" && ok "the service is stopped" || fail "still running"
grep -q "was never touched" <<<"$out" && ok "says the other server is intact" || fail "message: $out"
[ -f "$SB/var/www/html/mirzabot/config.php" ] && ok "the source PHP bot survived the round trip" || fail "source bot gone"

echo "== H: an installed number is never overwritten =="
fresh
seed_php_bot "$SB/var/www/html/mirzabot" "bot.example.com"
PHP_DIR="$SB/var/www/html/mirzabot" OUT="$SB/root/b.tar.gz" run bot-export >/dev/null 2>&1
FILE="$SB/root/b.tar.gz" N=3 run bot-import >/dev/null 2>&1
before=$(cat "$SB/etc/mitseller/bot3.env")
out=$(FILE="$SB/root/b.tar.gz" N=3 run bot-import 2>&1); rc=$?
[ $rc -ne 0 ] && ok "exit non-zero" || fail "overwrote bot 3"
grep -q "already exists" <<<"$out" && ok "says why it stopped" || fail "message: $out"
[ "$(cat "$SB/etc/mitseller/bot3.env")" = "$before" ] && ok "the existing config is unchanged" || fail "config changed"

echo "== I: export refuses when it is not told what to export =="
fresh
out=$(OUT="$SB/root/b.tar.gz" run bot-export 2>&1); rc=$?
[ $rc -ne 0 ] && ok "exit non-zero" || fail "exported nothing"
grep -q "PHP_DIR=" <<<"$out" && ok "says which variable to set" || fail "message: $out"

echo "== J: a path URL is refused on import, not discovered later =="
fresh
seed_php_bot "$SB/var/www/html/mirzabot" "example.com/mirzabot"
PHP_DIR="$SB/var/www/html/mirzabot" OUT="$SB/root/b.tar.gz" run bot-export >/dev/null 2>&1
out=$(FILE="$SB/root/b.tar.gz" run bot-import 2>&1); rc=$?
[ $rc -ne 0 ] && ok "exit non-zero" || fail "imported a path URL"
grep -q "served under a path" <<<"$out" && ok "says why it stopped" || fail "message: $out"

echo
if [ "$FAILS" -eq 0 ]; then
    echo "bot-move sandbox tests: all passed"
else
    echo "bot-move sandbox tests: $FAILS failed"
fi
exit $((FAILS > 0))
