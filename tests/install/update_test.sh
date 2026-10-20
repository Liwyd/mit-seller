#!/usr/bin/env bash
# Sandbox test for `install.sh update`: one binary is shared by every bot on
# the server, so an update that takes a bot down has to be able to put the
# binary that was running before it back, and it has to bring every bot with
# it rather than leave the server on two versions.
#
#   bash tests/install/update_test.sh
#
# No root, no MySQL, no network, no systemd: everything happens under a mktemp
# dir. A bot answers on a version the way it would after a real restart — the
# health stub checks the binary that is on disk right now.
set -uo pipefail

REPO=$(cd "$(dirname "$0")/../.." && pwd)
SB=$(readlink -f "$(mktemp -d "${TMPDIR:-/tmp}/mit-update.XXXXXX")")
trap 'rm -rf "${SB:?}"' EXIT
FAILS=0

ok()   { printf '  [ok]   %s\n' "$1"; }
fail() { printf '  [FAIL] %s\n' "$1"; FAILS=$((FAILS+1)); }

fresh() {
    rm -rf "${SB:?}"
    mkdir -p "$SB/bin" "$SB/state" "$SB/etc/mitseller" "$SB/usr/local/bin" \
             "$SB/etc/systemd" "$SB/var/lib" "$SB/root/mitseller-backups"
    : > "$SB/state/systemctl"
    make_stubs
    make_script
    make_fake_binary
    printf '1\n2\n3\n' > "$SB/state/installed"   # three bots are installed here
    printf '1\n2\n'    > "$SB/state/active"      # bot 3 is not running
}

stub() { sed "s|@@SB@@|$SB|g" > "$SB/bin/$1"; chmod +x "$SB/bin/$1"; }

make_stubs() {
    stub id <<'EOF'
#!/bin/bash
case "${1:-}" in -u) echo 0 ;; *) exit 0 ;; esac
EOF
    stub chown <<'EOF'
#!/bin/bash
exit 0
EOF
    stub useradd <<'EOF'
#!/bin/bash
exit 0
EOF
    stub sleep <<'EOF'
#!/bin/bash
# wait_healthy retries for 30s; in here that is 30 passes, not 30 seconds
exit 0
EOF
    stub systemctl <<'EOF'
#!/bin/bash
echo "systemctl $*" >> @@SB@@/state/systemctl
case "${1:-}" in
  list-units)
    case "${@: -1}" in *nexrabot*) exit 0 ;; esac
    for n in $(cat @@SB@@/state/installed); do
      if grep -qx "$n" @@SB@@/state/active 2>/dev/null; then
        echo "mitseller@$n.service loaded active running Mit Telegram bot $n"
      else
        echo "mitseller@$n.service loaded inactive dead Mit Telegram bot $n"
      fi
    done
    exit 0 ;;
  is-active)
    u="${@: -1}"; n=${u#mitseller@}; n=${n%.service}
    if grep -qx "$n" @@SB@@/state/active 2>/dev/null; then exit 0; fi
    exit 1 ;;
esac
exit 0
EOF
    # curl is asked for two things here: the release binary, and /healthz on
    # every bot. A bot answers only on the version listed for it in state/health.
    stub curl <<'EOF'
#!/bin/bash
url="" out="" next=""
for a in "$@"; do
  case "$a" in
    http*) url=$a ;;
    -o) next=out ;;
    *) if [ "$next" = out ]; then out=$a; next=""; fi ;;
  esac
done
case "$url" in
  *SHA256SUMS*) exit 6 ;;   # no checksums in the sandbox; install.sh skips them
  *healthz*)
    port=${url##*:}; port=${port%%/*}
    n=$((port - 18000))
    v=$(@@SB@@/usr/local/bin/mitseller version 2>/dev/null)
    grep -qx "$n|$v" @@SB@@/state/health && { echo "ok mitseller $v"; exit 0; }
    exit 7 ;;
  *)
    if [ -n "$out" ]; then
      printf '#!/bin/bash\ncase "${1:-}" in version) echo v9.9.9 ;; *) exit 0 ;; esac\n' > "$out"
    fi
    exit 0 ;;
esac
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
 ('PROVISIONER_UNIT=/etc/systemd/system/mitseller-provisioner.service',
  'PROVISIONER_UNIT=%s/etc/systemd/mitseller-provisioner.service' % sb),
 ('/usr/local/bin/mitseller', '%s/usr/local/bin/mitseller' % sb),
 ('/etc/mitseller', '%s/etc/mitseller' % sb),
 ('/root/mitseller', '%s/root/mitseller' % sb),
 ('/var/lib/mitseller', '%s/var/lib/mitseller' % sb),
]
table = {a: b for a, b in pairs}
s = re.sub('|'.join(sorted(map(re.escape, table), key=len, reverse=True)),
           lambda m: table[m.group(0)], s)
open(dst, 'w').write(s)
PY
    chmod +x "$SB/install.sh"
}

make_fake_binary() {
    cat > "$SB/usr/local/bin/mitseller" <<'EOF'
#!/bin/bash
case "${1:-}" in
  version) echo v7.2.5 ;;
  *) exit 0 ;;
esac
EOF
    chmod +x "$SB/usr/local/bin/mitseller"
}

run() { PATH="$SB/bin:$PATH" bash "$SB/install.sh" "$@"; }
version_of() { "$SB/usr/local/bin/mitseller" version; }
restarts_of() { grep -c "restart mitseller@$1" "$SB/state/systemctl"; }

echo "update"

# ---- the happy path: every running bot is moved to the new version
fresh
echo -e "1|v9.9.9\n2|v9.9.9\n3|v7.2.5" > "$SB/state/health"
out=$(run update 2>&1) || fail "update exited non-zero: $out"
echo "$out" | grep -q 'v9.9.9' && ok "it installs and reports the new version" || fail "output: $out"
echo "$out" | grep -q 'was v7.2.5' && ok "it says what was running before" || fail "output: $out"
[ "$(restarts_of 1)" = 1 ] && [ "$(restarts_of 2)" = 1 ] &&
    ok "each running bot is restarted once" || fail "restarts: $(cat "$SB/state/systemctl")"
[ "$(restarts_of 3)" = 0 ] && ok "a bot that is not running is left alone" || fail "bot 3 was restarted"
echo "$out" | grep -q 'not running, so not restarted' && ok "it names the bot it skipped" || fail "output: $out"
[ "$(version_of)" = v9.9.9 ] && ok "the shared binary is the new one" || fail "version $(version_of)"
[ -x "$SB/usr/local/bin/mitseller.previous" ] && ok "the previous binary is kept" || fail "no $SB/usr/local/bin/mitseller.previous"

# ---- a bot that only answers on the old version: everything goes back
fresh
echo -e "1|v7.2.5\n2|v9.9.9\n3|v7.2.5" > "$SB/state/health"
: > "$SB/state/systemctl"
out=$(run update 2>&1) && fail "update claimed success with a bot down" || true
echo "$out" | grep -q 'did not answer on v9.9.9' && ok "it reports the bot that failed" || fail "output: $out"
echo "$out" | grep -q 'rolled back to v7.2.5' && ok "it says it rolled back" || fail "output: $out"
[ "$(version_of)" = v7.2.5 ] && ok "the old binary is back in place" || fail "version $(version_of)"
[ "$(restarts_of 1)" = 2 ] && [ "$(restarts_of 2)" = 2 ] &&
    ok "every running bot is restarted again on the old version" || fail "restarts: $(cat "$SB/state/systemctl")"
grep -q 'daemon-reload' "$SB/state/systemctl" && ok "the units are reloaded" || fail "no daemon-reload"

# ---- nothing running at all: the binary still goes up, and nothing is touched
fresh
: > "$SB/state/active"
: > "$SB/state/systemctl"
out=$(run update 2>&1) || fail "update exited non-zero with no bots running: $out"
echo "$out" | grep -q 'not running, so not restarted' && ok "it says so" || fail "output: $out"
grep -q "restart mitseller@" "$SB/state/systemctl" && fail "it restarted a bot that was not running" || ok "nothing was restarted"

echo
if [ "$FAILS" -eq 0 ]; then
    echo "update sandbox tests: all passed"
else
    echo "update sandbox tests: $FAILS FAILED"
    exit 1
fi
