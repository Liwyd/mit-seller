#!/usr/bin/env bash
# Sandbox test for install.sh: runs "legacy-adopt" against a throwaway tree with
# systemctl/ss/nginx/curl stubbed, and checks that it really migrates a
# pre-rebrand nexrabot@N install (and rolls back when it must).
#
#   bash tests/install/legacy_adopt_test.sh
#
# No root, no systemd, no network: everything happens under a mktemp dir.
set -uo pipefail

REPO=$(cd "$(dirname "$0")/../.." && pwd)
SB=$(mktemp -d "${TMPDIR:-/tmp}/mit-adopt.XXXXXX")
trap 'rm -rf "${SB:?}"' EXIT
FAILS=0

ok()   { printf '  [ok]   %s\n' "$1"; }
fail() { printf '  [FAIL] %s\n' "$1"; FAILS=$((FAILS+1)); }

fresh() {
    rm -rf "${SB:?}"
    mkdir -p "$SB/bin" "$SB/state" \
             "$SB/etc/mitseller" "$SB/etc/nexrabot" \
             "$SB/etc/nginx/sites-enabled" "$SB/etc/nginx/sites-available" \
             "$SB/etc/nginx/snippets" "$SB/etc/nginx/conf.d" \
             "$SB/etc/systemd" "$SB/usr/local/bin" "$SB/var/lib" "$SB/root"
    make_stubs
    make_script
    printf '#!/bin/bash\necho 7.0.0-go\n' > "$SB/dummy.bin"
    chmod +x "$SB/dummy.bin"
}

stub() { # name  (template on stdin, @@SB@@ = sandbox root)
    sed "s|@@SB@@|$SB|g" > "$SB/bin/$1"
    chmod +x "$SB/bin/$1"
}

make_stubs() {
    stub systemctl <<'EOF'
#!/bin/bash
S=@@SB@@/state/units
touch "$S"
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
[ -f @@SB@@/state/rogue ] && cat @@SB@@/state/rogue
exit 0
EOF
    stub nginx <<'EOF'
#!/bin/bash
if [ "${1:-}" = "-t" ] && [ -f @@SB@@/state/nginx_bad ]; then
  echo "nginx: [emerg] sandbox failure" >&2
  exit 1
fi
exit 0
EOF
    stub curl <<'EOF'
#!/bin/bash
url=""
for a in "$@"; do case "$a" in http*) url=$a ;; esac; done
case "$url" in
  *healthz*)
    [ -f @@SB@@/state/unhealthy ] && exit 7
    echo "ok mitseller 7.0.0-go"; exit 0 ;;
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
    stub crontab <<'EOF'
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

seed_legacy() { # number  [no-symlink]
    local n=$1
    printf '%s\n' \
      "BOT_TOKEN=1:x" "ADMIN_ID=5" "DOMAIN=bot$n.example.com" "NEXRA_SECRET=legacysecret" \
      "DB_NAME=mirzabot$n" "DB_USER=u" "DB_PASS=p" \
      "LISTEN=127.0.0.1:$((18000+n))" "DATA_DIR=$SB/var/lib/nexrabot" > "$SB/etc/nexrabot/bot$n.env"
    chmod 640 "$SB/etc/nexrabot/bot$n.env"
    echo "$SB/etc/nginx/sites-available/bot$n" > "$SB/etc/nexrabot/bot$n.nginx.path"
    cat > "$SB/etc/nginx/sites-available/bot$n" <<EOF
server {
    listen 443 ssl;
    include $SB/etc/nginx/snippets/nexrabot$n.conf;
}
EOF
    if [ "${2:-}" != "no-symlink" ]; then
        ln -sf "$SB/etc/nginx/sites-available/bot$n" "$SB/etc/nginx/sites-enabled/bot$n"
    fi
    printf '#!/bin/bash\necho backup\n' > "$SB/root/bot${n}_backup.sh"
    echo "nexrabot@$n|1|1" >> "$SB/state/units"
}

run() { PATH="$SB/bin:$PATH" MITSELLER_BIN="$SB/dummy.bin" bash "$SB/install.sh" "$@"; }

echo "== A: happy path =="
fresh; seed_legacy 7
out=$(run legacy-adopt 2>&1); rc=$?
[ $rc -eq 0 ] && ok "exit 0" || { fail "exit $rc"; echo "$out" | tail -5; }
grep -q "adopted nexrabot@7 -> mitseller@7" <<<"$out" && ok "summary printed" || fail "summary: $out"
[ -f "$SB/etc/mitseller/bot7.env" ] && ok "config moved" || fail "config not moved"
grep -q "^MIT_SECRET=legacysecret" "$SB/etc/mitseller/bot7.env" && ok "secret key renamed" || fail "secret key"
grep -Eq "^DATA_DIR=.*/var/lib/mitseller$" "$SB/etc/mitseller/bot7.env" && ok "DATA_DIR rewritten" || fail "datadir"
[ ! -f "$SB/etc/nexrabot/bot7.env" ] && ok "old config gone" || fail "old config still there"
grep -q "snippets/mitseller7.conf" "$SB/etc/nginx/sites-available/bot7" && ok "site include rewritten" || fail "site include"
[ -f "$SB/etc/nginx/snippets/mitseller7.conf" ] && ok "snippet written" || fail "snippet missing"
grep -q "127.0.0.1:18007" "$SB/etc/nginx/snippets/mitseller7.conf" && ok "snippet proxies to 18007" || fail "snippet port"
grep -q "^mitseller@7|1|1" "$SB/state/units" && ok "new unit enabled+active" || fail "unit state"
grep -q "^nexrabot@7|0|0" "$SB/state/units" && ok "legacy unit disabled" || fail "legacy state"
ls -d "$SB"/root/mitseller-backups/adopt-* >/dev/null 2>&1 && ok "backup made" || fail "no backup"
grep -rq "NEXRA_SECRET=legacysecret" "$SB"/root/mitseller-backups/adopt-*/etc/ && ok "backup has original" || fail "backup config"
[ -f "$SB/etc/systemd/mitseller@.service" ] && ok "unit file installed" || fail "unit file"

echo "== B: bot does not answer -> rollback =="
fresh; seed_legacy 4; touch "$SB/state/unhealthy"
out=$(run legacy-adopt 2>&1); rc=$?
[ $rc -ne 0 ] && ok "exit non-zero" || fail "exit 0 despite failure"
grep -q "did not answer" <<<"$out" && ok "failure message" || fail "message: $out"
[ -f "$SB/etc/nexrabot/bot4.env" ] && ok "config back in old etc" || fail "config not restored"
[ ! -f "$SB/etc/mitseller/bot4.env" ] && ok "new config removed" || fail "new config left"
grep -q "snippets/nexrabot4.conf" "$SB/etc/nginx/sites-available/bot4" && ok "site restored" || fail "site not restored"
grep -q "^nexrabot@4|1|1" "$SB/state/units" && ok "legacy running again" || fail "legacy state"
grep -q "^mitseller@4|0|0" "$SB/state/units" && ok "new unit stopped" || fail "new state"

echo "== C: port still busy -> abort before touching anything =="
fresh; seed_legacy 7
echo "LISTEN 0 128 127.0.0.1:18007 0.0.0.0:*" > "$SB/state/rogue"
out=$(run legacy-adopt 2>&1); rc=$?
[ $rc -ne 0 ] && ok "exit non-zero" || fail "exit 0"
grep -q "still in use" <<<"$out" && ok "port message" || fail "message: $out"
[ -f "$SB/etc/nexrabot/bot7.env" ] && ok "config untouched" || fail "config moved"
grep -q "^nexrabot@7|1|1" "$SB/state/units" && ok "legacy left running" || fail "legacy state"

echo "== D: nothing legacy -> no-op =="
fresh
out=$(run legacy-adopt 2>&1); rc=$?
[ $rc -eq 0 ] && ok "exit 0" || fail "exit $rc"
grep -q "nothing to adopt" <<<"$out" && ok "said nothing to do" || fail "message: $out"

echo "== E: config already migrated -> refuse =="
fresh; seed_legacy 7
echo "x" > "$SB/etc/mitseller/bot7.env"
out=$(run legacy-adopt 2>&1); rc=$?
[ $rc -ne 0 ] && ok "exit non-zero" || fail "exit 0"
grep -q "already exists" <<<"$out" && ok "refusal message" || fail "message: $out"
[ -f "$SB/etc/nexrabot/bot7.env" ] && ok "old config untouched" || fail "moved anyway"

echo "== F: two bots, both unhealthy -> both rolled back =="
fresh; seed_legacy 3; seed_legacy 5
touch "$SB/state/unhealthy"
out=$(run legacy-adopt 2>&1); rc=$?
[ $rc -ne 0 ] && ok "exit non-zero" || fail "exit 0"
grep -q "3 5" <<<"$out" && ok "both numbers reported" || fail "message: $out"
[ -f "$SB/etc/nexrabot/bot3.env" ] && [ -f "$SB/etc/nexrabot/bot5.env" ] && ok "both configs restored" || fail "configs restored"
grep -q "^nexrabot@3|1|1" "$SB/state/units" && grep -q "^nexrabot@5|1|1" "$SB/state/units" && ok "both legacy running" || fail "legacy state"

echo "== G: site found only via botN.nginx.path (readlink regression) =="
fresh; seed_legacy 9 no-symlink
out=$(run legacy-adopt 2>&1); rc=$?
[ $rc -eq 0 ] && ok "exit 0" || { fail "exit $rc"; echo "$out" | tail -3; }
grep -q "snippets/mitseller9.conf" "$SB/etc/nginx/sites-available/bot9" && ok "site include rewritten" || fail "site include (readlink path)"
grep -q "no nginx site includes" <<<"$out" && fail "fell through to warning: $out" || ok "site resolved from bot9.nginx.path"

echo
if [ "$FAILS" -eq 0 ]; then
    echo "installer sandbox tests: all passed"
else
    echo "installer sandbox tests: $FAILS failed"
fi
exit $((FAILS > 0))
