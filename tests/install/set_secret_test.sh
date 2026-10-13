#!/usr/bin/env bash
# Sandbox test for set-secret: changing the panel-management code has to leave
# the config readable by the service. The file is root:mitseller mode 0640, and
# the bot runs as mitseller — edit it by hand as 0600 and the service cannot
# open its own config and restarts forever, taking the bot down.
#
#   bash tests/install/set_secret_test.sh
#
# No root, no MySQL, no network: everything happens under a mktemp dir.
set -uo pipefail

REPO=$(cd "$(dirname "$0")/../.." && pwd)
SB=$(readlink -f "$(mktemp -d "${TMPDIR:-/tmp}/mit-secret.XXXXXX")")
trap 'rm -rf "${SB:?}"' EXIT
FAILS=0

ok()   { printf '  [ok]   %s\n' "$1"; }
fail() { printf '  [FAIL] %s\n' "$1"; FAILS=$((FAILS+1)); }

fresh() {
    rm -rf "${SB:?}"
    mkdir -p "$SB/bin" "$SB/state" "$SB/etc/mitseller" \
             "$SB/usr/local/bin" "$SB/root/mitseller-backups"
    : > "$SB/state/units"
    make_stubs
    make_script
    make_fake_binary
    # the shape a real install leaves behind: readable by the mitseller group
    printf 'DOMAIN=one.example.com\nBOT_TOKEN=1:x\nADMIN_ID=5\nBOT_USERNAME=one\nDB_NAME=db1\nMIT_SECRET=old-secret\n' \
        > "$SB/etc/mitseller/bot1.env"
    chmod 0640 "$SB/etc/mitseller/bot1.env"
    HEALTH=ok
}

stub() { sed "s|@@SB@@|$SB|g" > "$SB/bin/$1"; chmod +x "$SB/bin/$1"; }

make_stubs() {
    stub id <<'EOF'
#!/bin/bash
case "${1:-}" in -u) echo 0 ;; *) echo sandbox ;; esac
EOF
    stub chown <<'EOF'
#!/bin/bash
echo "chown $*" >> @@SB@@/state/chown
exit 0
EOF
    stub systemctl <<'EOF'
#!/bin/bash
echo "systemctl $*" >> @@SB@@/state/systemctl
exit 0
EOF
    # wait_healthy asks the bot's own port; HEALTH decides whether it answers
    stub curl <<'EOF'
#!/bin/bash
[ -f @@SB@@/state/healthy ] && { echo "ok mitseller sandbox"; exit 0; }
exit 7
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

make_fake_binary() {
    # install.sh drives $BIN, which the sandbox rewrites to this path
    cat > "$SB/usr/local/bin/mitseller" <<'EOF'
#!/bin/bash
case "${1:-}" in
  check) exit 0 ;;
  keys)  echo "owner key: owner"; echo "manager key: manager" ;;
  *) exit 0 ;;
esac
EOF
    chmod +x "$SB/usr/local/bin/mitseller"
}

run() { PATH="$SB/bin:$PATH" bash "$SB/install.sh" "$@"; }
CFG="$SB/etc/mitseller/bot1.env"
secret_of() { sed -n 's/^MIT_SECRET=//p' "$CFG"; }

echo "set-secret"

# ---- the happy path must leave the config readable by the service
fresh
touch "$SB/state/healthy"
out=$(N=1 SECRET='86428642Mit""' run set-secret 2>&1) || fail "set-secret exited non-zero: $out"
[ "$(secret_of)" = '86428642Mit""' ] && ok "the new code is stored exactly as given" || fail "stored $(secret_of)"
[ "$(stat -c %a "$CFG")" = 640 ] && ok "the config stays mode 0640 (the service can read it)" || fail "mode is $(stat -c %a "$CFG"), want 640"
grep -q 'chown root:mitseller' "$SB/state/chown" && ok "ownership is put back to root:mitseller" || fail "no chown: $(cat "$SB/state/chown" 2>/dev/null)"
grep -q 'restart mitseller@1' "$SB/state/systemctl" && ok "the bot is restarted" || fail "not restarted: $(cat "$SB/state/systemctl")"
echo "$out" | grep -q '86428642Mit' && ok "it prints the code to type in Telegram" || fail "no code printed: $out"
ls "$SB/root/mitseller-backups"/bot1-config-*.env.bak >/dev/null 2>&1 && ok "the old config is backed up" || fail "no backup written"
grep -q '^MIT_SECRET=old-secret$' "$SB/root/mitseller-backups"/bot1-config-*.env.bak 2>/dev/null &&
    ok "the backup holds the old code" || fail "backup does not hold the old code"

# ---- if the bot does not come back, the old config must be restored
fresh
: > "$SB/state/units"        # no healthy answer: curl stub has no marker file
: > "$SB/state/systemctl"
out=$(N=1 SECRET='another-code' run set-secret 2>&1) && fail "set-secret claimed success while the bot was down" || true
echo "$out" | grep -qi 'did not come back' && ok "it reports the failure" || fail "no failure reported: $out"
[ "$(secret_of)" = 'old-secret' ] && ok "the old code is restored" || fail "left as $(secret_of)"
[ "$(stat -c %a "$CFG")" = 640 ] && ok "the restored config is still readable" || fail "mode is $(stat -c %a "$CFG")"

# ---- a bot that is not installed must say so, not write anything
fresh
out=$(N=9 SECRET='x' run set-secret 2>&1) && fail "set-secret ran for a bot that is not installed" || true
echo "$out" | grep -q 'not installed' && ok "it refuses a bot that is not installed" || fail "wrong message: $out"

# ---- an override left in the environment would hide the real value
fresh
touch "$SB/state/healthy"
out=$(N=1 SECRET='x' MITSELLER_MIT_SECRET=override run set-secret 2>&1) && fail "it ran with an override set" || true
echo "$out" | grep -q 'override' && ok "it refuses while an override is set" || fail "wrong message: $out"

echo
if [ "$FAILS" -eq 0 ]; then
    echo "set-secret sandbox tests: all passed"
else
    echo "set-secret sandbox tests: $FAILS FAILED"
    exit 1
fi
