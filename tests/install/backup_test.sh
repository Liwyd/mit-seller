#!/usr/bin/env bash
# Sandbox test for the hourly backup job: backup-setup installs one job that
# covers every bot on the server, backup-now runs it, and each backup is a
# bundle that bot-import can rebuild a bot from — not a bare database dump.
#
#   bash tests/install/backup_test.sh
#
# No root, no cron, no MySQL, no network: everything happens under a mktemp
# dir and the bot binary is a shell stub that really does write a tar.gz.
set -uo pipefail

REPO=$(cd "$(dirname "$0")/../.." && pwd)
SB=$(readlink -f "$(mktemp -d "${TMPDIR:-/tmp}/mit-backup.XXXXXX")")
trap 'rm -rf "${SB:?}"' EXIT
FAILS=0

ok()   { printf '  [ok]   %s\n' "$1"; }
fail() { printf '  [FAIL] %s\n' "$1"; FAILS=$((FAILS+1)); }

fresh() {
    rm -rf "${SB:?}"
    mkdir -p "$SB/bin" "$SB/state" "$SB/etc/mitseller" \
             "$SB/usr/local/bin" "$SB/usr/local/sbin" "$SB/root"
    : > "$SB/state/crontab"
    make_stubs
    make_script
    make_fake_binary
}

stub() { sed "s|@@SB@@|$SB|g" > "$SB/bin/$1"; chmod +x "$SB/bin/$1"; }

make_stubs() {
    # the sandbox is not root, and must not need to be
    stub id <<'EOF'
#!/bin/bash
case "${1:-}" in -u) echo 0 ;; *) echo sandbox ;; esac
EOF
    stub crontab <<'EOF'
#!/bin/bash
S=@@SB@@/state/crontab
case "${1:-}" in
  -l) [ -s "$S" ] && cat "$S" || exit 1 ;;
  *)  cat > "$S" ;;
esac
EOF
    stub curl <<'EOF'
#!/bin/bash
# record that an off-server copy was attempted, without any network
echo "$@" >> @@SB@@/state/curl
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
 ('/usr/local/sbin/mitseller-backup', '%s/usr/local/sbin/mitseller-backup' % sb),
 ('/usr/local/bin/mitseller', '%s/usr/local/bin/mitseller' % sb),
 ('/etc/mitseller', '%s/etc/mitseller' % sb),
 ('/root/mitseller', '%s/root/mitseller' % sb),
]
table = {a: b for a, b in pairs}
s = re.sub('|'.join(sorted(map(re.escape, table), key=len, reverse=True)),
           lambda m: table[m.group(0)], s)
open(dst, 'w').write(s)
PY
    chmod +x "$SB/install.sh"
}

# export-bundle really writes a tar.gz holding manifest.json and dump.sql.gz,
# so the test can open what the backup produced and check it is restorable
make_fake_binary() {
    cat > "$SB/bin/fake-mitseller" <<'EOF'
#!/bin/bash
cmd=${1:-}; shift || true
out="" cfgpath=""
while [ $# -gt 0 ]; do
  case "$1" in
    --out) out=$2; shift 2 ;;
    -c) cfgpath=$2; shift 2 ;;
    *) shift ;;
  esac
done
case "$cmd" in
  export-bundle)
    [ -n "$out" ] || { echo "no --out" >&2; exit 1; }
    # a bot whose config names a missing database fails, as a real dump would
    grep -q '^DB_NAME=$' "$cfgpath" 2>/dev/null && { echo "dump failed" >&2; exit 1; }
    tmp=$(mktemp -d)
    d=$(sed -n 's/^DOMAIN=//p' "$cfgpath")
    printf '{"version":1,"domain":"%s"}\n' "$d" > "$tmp/manifest.json"
    echo "-- dump of $d" | gzip > "$tmp/dump.sql.gz"
    tar -czf "$out" -C "$tmp" manifest.json dump.sql.gz
    rm -rf "$tmp"; chmod 600 "$out"
    exit 0 ;;
  *) exit 0 ;;
esac
EOF
    chmod +x "$SB/bin/fake-mitseller"
}

run() { PATH="$SB/bin:$PATH" MITSELLER_BIN="$SB/bin/fake-mitseller" bash "$SB/install.sh" "$@"; }
SCRIPT="$SB/usr/local/sbin/mitseller-backup"

mkbot() { printf 'DOMAIN=%s\nBOT_TOKEN=1:x\nADMIN_ID=5\nBOT_USERNAME=b%s\nDB_NAME=db%s\n' "$2" "$1" "$1" > "$SB/etc/mitseller/bot$1.env"; }

echo "backup job"

# ---- setup covers every bot with one job, and bakes the real paths in
fresh
mkbot 1 one.example.com
mkbot 2 two.example.com
out=$(run backup-setup 2>&1) || fail "backup-setup exited non-zero: $out"
[ -x "$SCRIPT" ] && ok "backup-setup writes an executable job" || fail "no job at $SCRIPT"
grep -q 'mitseller-backup' "$SB/state/crontab" && ok "one cron line is installed" || fail "no cron line"
[ "$(grep -c mitseller-backup "$SB/state/crontab")" = 1 ] && ok "exactly one cron line" || fail "duplicate cron lines"
grep -qE '^[0-9]+ \* \* \* \*' "$SB/state/crontab" && ok "the job runs hourly" || fail "not hourly: $(cat "$SB/state/crontab")"
grep -q '@@' "$SCRIPT" && fail "unsubstituted @@ placeholder left in the job" || ok "paths are baked in (no placeholders)"
grep -q "$SB/etc/mitseller/bot\*.env" "$SCRIPT" && ok "the job reads every bot config" || fail "wrong config dir in the job"
[ "$(stat -c %a "$SCRIPT")" = 700 ] && ok "the job is not world readable" || fail "mode $(stat -c %a "$SCRIPT")"

# ---- backup-now produces a restorable bundle per bot
run backup-now >/dev/null 2>&1 || fail "backup-now exited non-zero"
n=$(ls "$SB/root/mitseller-backups"/*-bundle-*.tar.gz 2>/dev/null | wc -l)
[ "$n" = 2 ] && ok "every bot got a bundle (2/2)" || fail "got $n bundles, want 2"
for b in bot1 bot2; do
    f=$(ls "$SB/root/mitseller-backups"/${b}-bundle-*.tar.gz 2>/dev/null | head -1)
    [ -n "$f" ] || continue
    # capture the listing once: "tar | grep -q" under pipefail is a coin
    # flip, because grep -q exits early and SIGPIPEs tar
    listing=$(tar -tzf "$f" 2>/dev/null) || fail "$b bundle is not a readable tar.gz"
    printf '%s\n' "$listing" | grep -q 'dump.sql.gz' && ok "$b bundle holds dump.sql.gz" || fail "$b bundle has no dump.sql.gz"
    printf '%s\n' "$listing" | grep -q 'manifest.json' && ok "$b bundle holds manifest.json" || fail "$b bundle has no manifest.json"
done
[ "$(stat -c %a "$SB/root/mitseller-backups")" = 700 ] && ok "the backup dir is not world readable" || fail "backup dir mode $(stat -c %a "$SB/root/mitseller-backups")"
grep -q 'sendDocument' "$SB/state/curl" && ok "an off-server copy was attempted" || fail "no Telegram copy attempted"

# ---- an off-server copy failing must not lose the local backup
fresh
mkbot 1 one.example.com
mkbot 2 two.example.com
run backup-setup >/dev/null 2>&1
: > "$SB/state/curl"
stub curl <<'EOF'
#!/bin/bash
exit 22
EOF
sleep 1
run backup-now >/dev/null 2>&1 || fail "a failed Telegram copy made backup-now fail"
n=$(ls "$SB/root/mitseller-backups"/*-bundle-*.tar.gz 2>/dev/null | wc -l)
[ "$n" = 2 ] && ok "local backups survive a failed Telegram copy" || fail "got $n bundles, want 2"

# ---- one broken bot must not stop the others
fresh
mkbot 1 good.example.com
mkbot 2 bad.example.com
# a config the fake binary refuses to dump
printf 'DOMAIN=bad.example.com\nDB_NAME=\n' > "$SB/etc/mitseller/bot2.env"
out=$(run backup-setup 2>&1) || fail "backup-setup exited non-zero: $out"
# a bot that cannot be dumped must be reported and must not lose the others;
# a non-zero exit is right here, because cron should flag a missed backup
run backup-now >/dev/null 2>&1
out=$(run backup-now 2>&1)
echo "$out" | grep -q 'bot2.*FAILED' && ok "the failing bot is reported" || fail "failure not reported: $out"
[ -f "$(ls "$SB/root/mitseller-backups"/bot1-bundle-*.tar.gz 2>/dev/null | head -1)" ] &&
    ok "the healthy bot is still backed up" || fail "the healthy bot was skipped"

# ---- old bundles are pruned, newest kept
fresh
mkbot 1 one.example.com
run backup-setup >/dev/null 2>&1
for i in 1 2 3 4 5; do
    sleep 1
    run backup-now >/dev/null 2>&1
done
KEEP_BACKUPS=2 run backup-now >/dev/null 2>&1
n=$(ls "$SB/root/mitseller-backups"/bot1-bundle-*.tar.gz 2>/dev/null | wc -l)
[ "$n" -le 2 ] && ok "old bundles are pruned (kept $n with KEEP_BACKUPS=2)" || fail "kept $n bundles, want <= 2"
[ "$n" -ge 1 ] && ok "the newest bundle is kept" || fail "everything was pruned"

# ---- backup-now refuses to run before setup
fresh
mkbot 1 one.example.com
run backup-now >/dev/null 2>&1 && fail "backup-now ran with no job installed" || ok "backup-now says to run backup-setup first"

echo
if [ "$FAILS" -eq 0 ]; then
    echo "backup sandbox tests: all passed"
else
    echo "backup sandbox tests: $FAILS FAILED"
    exit 1
fi
