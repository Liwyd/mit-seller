#!/usr/bin/env bash
# Sandbox test for the provisioner and panel-creds: the panel installs bots
# over this API, so the token file and the certificate must be root:mitseller
# 0640, the unit must point at the real installer, and panel-creds must store
# the panel's password without ever letting a shell see it.
#
#   bash tests/install/provisioner_test.sh
#
# No root, no MySQL, no network, no systemd: everything happens under a mktemp
# dir. The provisioner binary itself is tested in Go (internal/provisioner).
set -uo pipefail

REPO=$(cd "$(dirname "$0")/../.." && pwd)
SB=$(readlink -f "$(mktemp -d "${TMPDIR:-/tmp}/mit-provisioner.XXXXXX")")
trap 'rm -rf "${SB:?}"' EXIT
FAILS=0

ok()   { printf '  [ok]   %s\n' "$1"; }
fail() { printf '  [FAIL] %s\n' "$1"; FAILS=$((FAILS+1)); }

fresh() {
    rm -rf "${SB:?}"
    mkdir -p "$SB/bin" "$SB/state" "$SB/etc/mitseller" \
             "$SB/usr/local/bin" "$SB/etc/systemd/system" "$SB/root"
    : > "$SB/state/systemctl"
    make_stubs
    make_script
    make_fake_binary
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
    stub install <<'EOF'
#!/bin/bash
# just enough of install(1) for this script: copy, and make directories
args=()
while [ $# -gt 0 ]; do
    case "$1" in
        -d|-m|-o|-g) shift 2 ;;
        -*) shift ;;
        *) args+=("$1"); shift ;;
    esac
done
src=${args[0]:-}; dst=${args[1]:-}
if [ -n "${src:-}" ] && [ -f "$src" ] && [ -n "${dst:-}" ]; then
    cp "$src" "$dst"
fi
exit 0
EOF
    stub systemctl <<'EOF'
#!/bin/bash
echo "systemctl $*" >> @@SB@@/state/systemctl
exit 0
EOF
    # the port must look free and this server needs an address for the output
    stub ss <<'EOF'
#!/bin/bash
exit 0
EOF
    stub hostname <<'EOF'
#!/bin/bash
case "${1:-}" in -I) echo "176.124.199.78 " ;; *) echo sandbox ;; esac
EOF
    # openssl: only "give me a token" and "make me a certificate" matter here
    stub openssl <<'EOF'
#!/bin/bash
case "${1:-}" in
    rand) echo "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899" ;;
    req)
        out=""; key=""
        while [ $# -gt 0 ]; do
            case "$1" in -out) out=$2; shift 2 ;; -keyout) key=$2; shift 2 ;; *) shift ;; esac
        done
        [ -n "$key" ] && echo SANDBOX-KEY > "$key"
        [ -n "$out" ] && echo SANDBOX-CERT > "$out"
        ;;
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
 ('PROVISIONER_UNIT=/etc/systemd/system/mitseller-provisioner.service',
  'PROVISIONER_UNIT=%s/etc/systemd/system/mitseller-provisioner.service' % sb),
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

run() { PATH="$SB/bin:$PATH" MITSELLER_BIN="$SB/usr/local/bin/mitseller" bash "$SB/install.sh" "$@"; }

ENVF="$SB/etc/mitseller/provisioner.env"
CRT="$SB/etc/mitseller/provisioner.crt"
KEY="$SB/etc/mitseller/provisioner.key"
UNIT="$SB/etc/systemd/system/mitseller-provisioner.service"
PANEL="$SB/etc/mitseller/panel.env"

echo "provisioner-setup / panel-creds"

# ---- setup must produce a token, a certificate and a unit
fresh
out=$(run provisioner-setup 2>&1) || fail "provisioner-setup exited non-zero: $out"
[ -f "$ENVF" ] && ok "the token file is written" || fail "no $ENVF: $out"
grep -q '^MIT_PROVISION_TOKEN=[0-9a-f]\{64\}$' "$ENVF" &&
    ok "the token is 64 hex characters" || fail "token line: $(grep TOKEN "$ENVF" 2>/dev/null)"
grep -q "^MIT_PROVISION_CERT=$SB/etc/mitseller/provisioner.crt$" "$ENVF" &&
    ok "the token file names the certificate" || fail "cert line: $(grep CERT "$ENVF" 2>/dev/null)"
[ "$(stat -c %a "$ENVF")" = 640 ] && ok "the token file is mode 0640" || fail "mode is $(stat -c %a "$ENVF")"
[ -f "$CRT" ] && [ -f "$KEY" ] && ok "the certificate and key exist" || fail "missing cert or key"
[ "$(stat -c %a "$KEY")" = 640 ] && ok "the key is mode 0640" || fail "key mode is $(stat -c %a "$KEY")"
grep -q 'chown root:mitseller' "$SB/state/chown" && ok "files are chown'd to root:mitseller" || fail "no chown recorded"
[ -f "$UNIT" ] && ok "the unit is written" || fail "no unit file"
grep -q 'provisioner -env' "$UNIT" && ok "the unit runs the provisioner" || fail "unit: $(cat "$UNIT" 2>/dev/null)"
grep -q -- "-installer $SB/install.sh" "$UNIT" &&
    ok "the unit points at this installer, not a hard-coded /root path" || fail "installer line: $(grep installer "$UNIT")"
# ProtectHome mounts an empty tmpfs over /root, so the service cannot even
# exec the installer it just named; ProtectSystem=full makes /etc read-only,
# which is where install.sh new writes every config. Both of them take every
# install down, so neither may come back.
if grep -q '^ProtectHome=' "$UNIT"; then
    fail "the unit hides /root, so exec of the installer fails: $(grep '^ProtectHome=' "$UNIT")"
else
    ok "the unit can see the installer (no ProtectHome)"
fi
if grep -q '^ProtectSystem=' "$UNIT"; then
    fail "the unit makes /etc read-only, where install.sh new writes: $(grep '^ProtectSystem=' "$UNIT")"
else
    ok "the unit can write /etc (no ProtectSystem)"
fi
grep -q 'enable --now mitseller-provisioner' "$SB/state/systemctl" &&
    ok "the service is enabled and started" || fail "systemctl: $(cat "$SB/state/systemctl")"

# ---- print must hand over exactly what the panel needs
out=$(run provisioner-print 2>&1) || fail "provisioner-print exited non-zero: $out"
echo "$out" | grep -q 'mitseller_server          = 176.124.199.78' &&
    ok "it prints the server address" || fail "no address: $out"
echo "$out" | grep -q 'mitseller_provision_token' && ok "it prints the token label" || fail "no token: $out"
echo "$out" | grep -q 'mitseller_server_cert' && ok "it prints the certificate label" || fail "no cert: $out"
echo "$out" | grep -q 'SANDBOX-CERT' && ok "it prints the certificate itself" || fail "no cert body: $out"

# ---- setup twice must not rotate the token the panel is already holding
first=$(sed -n 's/^MIT_PROVISION_TOKEN=//p' "$ENVF")
out=$(run provisioner-setup 2>&1) || fail "second provisioner-setup exited non-zero: $out"
[ "$(sed -n 's/^MIT_PROVISION_TOKEN=//p' "$ENVF")" = "$first" ] &&
    ok "running setup again keeps the existing token" || fail "the token was rotated"
echo "$out" | grep -q 'keeping the existing token' && ok "it says it is keeping it" || fail "message: $out"

# ---- a busy port must stop it before it breaks anything
fresh
stub ss <<'EOF'
#!/bin/bash
echo "LISTEN 0 512 0.0.0.0:18100 0.0.0.0:*"
EOF
out=$(run provisioner-setup 2>&1) && fail "it ran with port 18100 taken" || true
echo "$out" | grep -q 'already in use' && ok "it refuses when the port is taken" || fail "message: $out"
# Re-running setup is how a fixed unit file gets installed, so the service we
# own has to be stopped first — otherwise its own listener is the port that is
# "already in use" and the fix can never be applied.
grep -q 'disable --now mitseller-provisioner' "$SB/state/systemctl" &&
    ok "it stops its own service before judging the port" || fail "systemctl: $(cat "$SB/state/systemctl")"

# ---- teardown removes the token and the unit, and leaves the key behind
fresh
out=$(run provisioner-teardown 2>&1) || fail "teardown exited non-zero: $out"
[ ! -f "$ENVF" ] && ok "the token file is removed" || fail "token file still there"
[ ! -f "$UNIT" ] && ok "the unit is removed" || fail "unit still there"
grep -q 'disable --now mitseller-provisioner' "$SB/state/systemctl" && ok "the service is stopped" || fail "not stopped"

# ---- panel-creds stores the address and the password, without a shell
fresh
out=$(URL='https://panel.example.com:4000/dashboard' USER=admin PASS='p@ss`id`"quoted"' run panel-creds 2>&1) ||
    fail "panel-creds exited non-zero: $out"
[ -f "$PANEL" ] && ok "the credentials file is written" || fail "no $PANEL: $out"
grep -q '^MIT_PANEL_URL=https://panel.example.com:4000/dashboard$' "$PANEL" &&
    ok "the URL is stored exactly" || fail "URL line: $(grep URL "$PANEL")"
grep -q '^MIT_PANEL_USER=admin$' "$PANEL" && ok "the username is stored" || fail "user line missing"
# the backtick and the quote must survive as data, never as shell
grep -qF 'MIT_PANEL_PASS=p@ss`id`"quoted"' "$PANEL" &&
    ok "a password with shell metacharacters survives intact" || fail "pass line: $(grep PASS "$PANEL")"
[ "$(stat -c %a "$PANEL")" = 640 ] && ok "the credentials are mode 0640" || fail "mode is $(stat -c %a "$PANEL")"

# ---- register must find them without MIT_PANEL_* in the environment
printf 'DOMAIN=one.example.com\nBOT_USERNAME=one\n' > "$SB/etc/mitseller/bot1.env"
out=$(N=1 run register 2>&1) || fail "register exited non-zero: $out"
echo "$out" | grep -q 'no panel on this server' && fail "register ignored panel.env: $out" || true
grep -q 'connecting bot 1' <<<"$out" && ok "register picks the credentials up from panel.env" || fail "message: $out"

# ---- panel-creds must refuse a URL that is not a URL
fresh
out=$(URL='not-a-url' USER=a PASS=b run panel-creds 2>&1) && fail "it accepted a non-URL" || true
echo "$out" | grep -q 'must start with http' && ok "it refuses a non-URL" || fail "message: $out"

# ---- and it must insist on all three
fresh
out=$(URL='https://x.example.com' USER=a run panel-creds 2>&1) && fail "it ran without a password" || true
echo "$out" | grep -q 'PASS=' && ok "it insists on a password" || fail "message: $out"

echo
if [ "$FAILS" -eq 0 ]; then
    echo "provisioner sandbox tests: all passed"
else
    echo "provisioner sandbox tests: $FAILS FAILED"
    exit 1
fi
