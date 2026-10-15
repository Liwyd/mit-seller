#!/bin/bash
# =============================================================================
# Mit bot (mitseller, the Go rewrite of the Mirza bot) — install / migrate
#
#   New bot (same variables as the old PHP installer):
#     N=7 TOKEN='123:abc' DOMAIN='bot7.example.com' ADMIN='123456789' \
#     MIT_SECRET='secret' CERT_EMAIL='you@example.com' bash install.sh
#
#   Move a running PHP bot to Go (same database, same webhook, no downtime):
#     N=7 bash install.sh migrate          # PHP_DIR=/var/www/html/botmirzapanel7 by default
#     PHP_DIR=/var/www/html/mybot bash install.sh migrate   # N is then picked for you
#
#   It accepts a bot written by nexra, by mirzapanel or by mirza_pro. The bot
#   must already be served by its own nginx site with "root <bot dir>;" — the
#   mirzapanel and mirza_pro installers put every bot behind one Apache site
#   on a path, which mit-seller does not proxy, so those installs migrate by
#   hand (see docs/MIGRATION.md).
#
#   Undo a migration (back to PHP exactly as it was):
#     N=7 bash install.sh rollback
#
#   Update every Go bot on this server to the latest release:
#     bash install.sh update
#
#   Show the keys Mit Panel needs:
#     N=7 bash install.sh keys
#
#   Everything on one server, nothing to type: update the Mit Panel that
#   runs here (found by itself, credentials read from it), then move every
#   PHP bot to Go and connect each to the panel under its owner:
#     bash /root/mitseller-install.sh all
#
#   Move EVERY PHP bot on this server (one level under /var/www/html, plus
#   whatever MIGRATE_DIRS names) to Go, one by one, and connect each to Mit
#   Panel under its owner:
#     curl -sLo /root/mitseller-install.sh <raw url of this file>
#     MIT_PANEL_URL=https://panel.example.com/dashboard MIT_PANEL_USER=admin \
#     MIT_PANEL_PASS='...' bash /root/mitseller-install.sh migrate-all
#   A bot that fails is put back on PHP by itself and the others carry on.
#   Without the MIT_PANEL_* variables the Mit Panel on this server is
#   used; with none, the bots are migrated but not connected (on the panel's
#   server, "bash install.sh panel-link" prints the command for this one).
#   A bot in another directory is added with MIGRATE_DIRS, colon separated:
#     MIGRATE_DIRS=/srv/bots/extra1:/srv/bots/extra2 bash install.sh migrate-all
#
#   Convert a bot installed by the PRE-REBRAND installer (nexrabot@N) to this
#   one — same number, port, database, domain, nginx site and panel keys, the
#   names change, and a bot that doesn't answer comes back by itself:
#     bash install.sh legacy-adopt
#
#   Update only the Mit Panel running here (data backed up first, the old
#   version comes back by itself if the new one doesn't start):
#     bash install.sh panel-update
#
# Optional: MITSELLER_BIN=/path/to/mitseller uses a local binary instead of
# downloading the latest release; RELEASE=v7.0.0 pins a release.
# =============================================================================
set -Eeuo pipefail

# ----------------------------------------------------------------- rebrand
# The names this script used before the rebrand are still accepted, so an
# existing setup keeps its variables: NEXRA_SECRET -> MIT_SECRET,
# NEXRA_PANEL_* -> MIT_PANEL_*, NEXRABOT_* -> MITSELLER_*. The current names
# win when both are set.
MIT_SECRET="${MIT_SECRET:-${NEXRA_SECRET:-}}"
MIT_PANEL_URL="${MIT_PANEL_URL:-${NEXRA_PANEL_URL:-}}"
MIT_PANEL_USER="${MIT_PANEL_USER:-${NEXRA_PANEL_USER:-}}"
MIT_PANEL_PASS="${MIT_PANEL_PASS:-${NEXRA_PANEL_PASS:-}}"
MIT_PANEL_INSECURE="${MIT_PANEL_INSECURE:-${NEXRA_PANEL_INSECURE:-}}"
MIT_PANEL_IGNORE_IDS="${MIT_PANEL_IGNORE_IDS:-${NEXRA_PANEL_IGNORE_IDS:-}}"
MIT_OWNER_ID="${MIT_OWNER_ID:-${NEXRA_OWNER_ID:-}}"
MITSELLER_BIN="${MITSELLER_BIN:-${NEXRABOT_BIN:-}}"
MITSELLER_TELEGRAM_API="${MITSELLER_TELEGRAM_API:-${NEXRABOT_TELEGRAM_API:-}}"
# Only what the bot itself reads is exported: an exported-but-empty variable
# would beat the bot's own default (its Telegram API base URL, for one).
# MIT_SECRET is exported too, so "migrate" can hand the same value to the Go
# bot when the PHP bot never wrote one into its config.php.
export MIT_SECRET MIT_PANEL_URL MIT_PANEL_USER MIT_PANEL_PASS MIT_PANEL_INSECURE \
       MIT_PANEL_IGNORE_IDS MITSELLER_BIN

# legacy_units: the numbers of the pre-rebrand bots that are enabled or
# running here, if any. They belong to the other product and this script never
# touches them — except when you ask for it with "install.sh legacy-adopt".
legacy_units() {
    local u n
    for u in $(systemctl list-units --type=service --all --plain --no-legend 'nexrabot@*.service' 2>/dev/null | awk '{print $1}'); do
        n=${u#nexrabot@}; n=${n%.service}
        case "$n" in ''|*[!0-9]*) continue ;; esac
        if systemctl is-active --quiet "$u" || systemctl is-enabled --quiet "$u" 2>/dev/null; then
            echo "$n"
        fi
    done
}

# legacy_warn points out pre-rebrand bots without ever touching them.
legacy_warn() {
    local olds
    olds=$(legacy_units | tr '\n' ' ')
    [ -n "${olds// /}" ] || return 0
    warn "pre-rebrand bots run on this server too (nexrabot@${olds% }): they are left untouched; convert them with: bash install.sh legacy-adopt"
}

REPO="${REPO:-Liwyd/mit-seller}"
RELEASE="${RELEASE:-latest}"
RAW_URL="https://raw.githubusercontent.com/$REPO/main/install.sh"
BIN=/usr/local/bin/mitseller
ETC=/etc/mitseller
BACKUPS=/root/mitseller-backups
UNIT=/etc/systemd/system/mitseller@.service

say()  { printf '\033[1;34m>>>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!!\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31mxxx\033[0m %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" = 0 ] || die "run as root"

port_for() { echo $((18000 + $1)); }

# ----------------------------------------------------------------- binary

install_binary() {
    if [ -n "${MITSELLER_BIN:-}" ]; then
        install -m 0755 "$MITSELLER_BIN" "$BIN.new"
    else
        local arch
        case "$(uname -m)" in
            x86_64|amd64) arch=amd64 ;;
            aarch64|arm64) arch=arm64 ;;
            *) die "unsupported CPU $(uname -m)" ;;
        esac
        local base="https://github.com/$REPO/releases/latest/download"
        [ "$RELEASE" != latest ] && base="https://github.com/$REPO/releases/download/$RELEASE"
        say "downloading mitseller ($arch) from $base"
        if ! curl -fsSL "$base/mitseller-linux-$arch" -o "$BIN.new"; then
            # no stable release yet: take the newest one (pre-releases too)
            local tag
            tag=$(curl -fsSL "https://api.github.com/repos/$REPO/releases?per_page=1" | grep -m1 '"tag_name"' | cut -d'"' -f4 || true)
            [ "$RELEASE" = latest ] && [ -n "$tag" ] || die "download failed (is there a release on github.com/$REPO?)"
            base="https://github.com/$REPO/releases/download/$tag"
            say "no stable release; using $tag"
            curl -fsSL "$base/mitseller-linux-$arch" -o "$BIN.new" || die "download failed"
        fi
        if curl -fsSL "$base/SHA256SUMS" -o /tmp/mitseller.sums 2>/dev/null; then
            local want got
            want=$(grep " mitseller-linux-$arch\$" /tmp/mitseller.sums | awk '{print $1}')
            got=$(sha256sum "$BIN.new" | awk '{print $1}')
            [ -n "$want" ] && [ "$want" != "$got" ] && { rm -f "$BIN.new"; die "checksum mismatch"; }
            rm -f /tmp/mitseller.sums
        fi
        chmod 0755 "$BIN.new"
    fi
    "$BIN.new" version >/dev/null || { rm -f "$BIN.new"; die "the downloaded binary does not run"; }
    mv -f "$BIN.new" "$BIN"
    say "mitseller $("$BIN" version) installed at $BIN"
}

install_unit() {
    id mitseller >/dev/null 2>&1 || useradd --system --home-dir /var/lib/mitseller --shell /usr/sbin/nologin mitseller
    mkdir -p "$ETC" "$BACKUPS" /var/lib/mitseller
    chown mitseller:mitseller /var/lib/mitseller
    chmod 700 "$BACKUPS"
    cat > "$UNIT" <<'EOF'
[Unit]
Description=Mit Telegram bot %i
After=network-online.target mysql.service mariadb.service
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/mitseller serve -c /etc/mitseller/bot%i.env
User=mitseller
Group=mitseller
StateDirectory=mitseller
Restart=always
RestartSec=2
TimeoutStopSec=30
LimitNOFILE=65536
NoNewPrivileges=yes
ProtectSystem=full
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
}

secure_config() {
    chown root:mitseller "$1"
    chmod 0640 "$1"
}

wait_healthy() { # url, seconds — passes only when the Go bot itself answers
    local i
    for i in $(seq 1 "$2"); do
        curl -fsS -m 3 "$1" 2>/dev/null | grep -q '^ok ' && return 0
        sleep 1
    done
    return 1
}

proxy_snippet() { # N
    cat > "/etc/nginx/snippets/mitseller$1.conf" <<EOF
# mitseller $1 (written by install.sh)
location / {
    proxy_pass http://127.0.0.1:$(port_for "$1");
    proxy_http_version 1.1;
    proxy_set_header Host \$host;
    proxy_set_header X-Real-IP \$remote_addr;
    proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto \$scheme;
    proxy_read_timeout 120s;
    client_max_body_size 20m;
}
EOF
}

print_keys() { # N
    echo
    "$BIN" keys -c "$ETC/bot$1.env"
    echo "secret:       $(sed -n 's/^MIT_SECRET=//p' "$ETC/bot$1.env" | tr -d '"')"
    echo
    echo "Mit Panel → Bot → افزودن ربات: address https://$(grep '^DOMAIN=' "$ETC/bot$1.env" | cut -d= -f2), and the two keys above."
}

# set-secret: change the code that unlocks panel management inside Telegram.
#
# Editing the config by hand is how a bot gets left unable to read its own
# config: the file has to stay root:mitseller and mode 0640, or the service
# (which runs as mitseller) cannot open it and restarts forever. This keeps
# the ownership, backs the config up first, and only reports success once the
# bot is answering again.
#
#   N=3 SECRET='new code' bash install.sh set-secret
cmd_set_secret() {
    local N="${N:?set N= (bot number)}"
    local NEW="${SECRET:?set SECRET= (the new panel-management code)}"
    local CFG="$ETC/bot${N}.env" BAK

    [ -f "$CFG" ] || die "$CFG not found; bot $N is not installed"
    # these are read as overrides of the file, so leaving one set would make
    # "check" pass while the bot still starts on the old value
    [ -n "${MITSELLER_MIT_SECRET:-}${NEXRABOT_NEXRA_SECRET:-}" ] &&
        die "unset MITSELLER_MIT_SECRET/NEXRABOT_NEXRA_SECRET first; they override the config file"
    [ -n "$NEW" ] || die "SECRET= is empty"

    BAK="$BACKUPS/bot${N}-config-$(date +%Y%m%d_%H%M%S).env.bak"
    cp -a "$CFG" "$BAK"
    chmod 0640 "$BAK"
    say "config backed up to $BAK"

    python3 - "$CFG" "$NEW" <<'PY'
import sys
path, secret = sys.argv[1], sys.argv[2]
lines = open(path).read().splitlines(True)
out, seen = [], False
for ln in lines:
    if ln.startswith("MIT_SECRET="):
        out.append("MIT_SECRET=%s\n" % secret)
        seen = True
    else:
        out.append(ln)
if not seen:
    out.append("MIT_SECRET=%s\n" % secret)
open(path, "w").writelines(out)
PY
    # back to the ownership and mode the service needs, whatever the rewrite did
    secure_config "$CFG"

    "$BIN" check -c "$CFG" >/dev/null || {
        cp -a "$BAK" "$CFG"; secure_config "$CFG"
        die "the new value does not parse; the old config has been restored from $BAK"
    }

    say "restarting mitseller@$N"
    systemctl restart "mitseller@$N"
    if ! wait_healthy "http://127.0.0.1:$(port_for "$N")/healthz" 30; then
        cp -a "$BAK" "$CFG" && secure_config "$CFG"
        systemctl restart "mitseller@$N" || true
        die "the bot did not come back; the old config has been restored from $BAK"
    fi
    say "mitseller@$N is up again"
    echo
    echo "the code to type in Telegram is now:"
    echo
    echo "    $NEW"
    echo
    echo "(it gates panel management; customers are not affected by changing it)"
}

# ----------------------------------------------------------------- Mit Panel

PANEL_REPO="${PANEL_REPO:-https://github.com/Liwyd/mit-panel}"
PANEL_BRANCH="${PANEL_BRANCH:-main}"
PANEL_SRC="${PANEL_SRC:-/opt/mit-panel-src}"

# find_panel: the Mit Panel container on this server, if there is one. Only
# this product's panel is matched — a pre-rebrand panel running next to it
# must never be updated from this repository.
find_panel() {
    command -v docker >/dev/null 2>&1 || return 1
    PANEL_C=$(docker ps --format '{{.Names}} {{.Image}}' 2>/dev/null |
        awk '$1 == "mit-panel" || $2 ~ /(^|\/)liwyd\/mit-panel/ {print $1; exit}')
    [ -n "$PANEL_C" ] || return 1
    local label='{{ index .Config.Labels "%s" }}'
    PANEL_DIR=$(docker inspect -f "$(printf "$label" com.docker.compose.project.working_dir)" "$PANEL_C")
    PANEL_FILES=$(docker inspect -f "$(printf "$label" com.docker.compose.project.config_files)" "$PANEL_C")
    PANEL_SERVICE=$(docker inspect -f "$(printf "$label" com.docker.compose.service)" "$PANEL_C")
    PANEL_IMAGE=$(docker inspect -f '{{.Config.Image}}' "$PANEL_C")
    PANEL_DATA=$(docker inspect -f '{{range .Mounts}}{{if eq .Destination "/app/data"}}{{.Source}}{{end}}{{end}}' "$PANEL_C")
}

panel_var() { # NAME [default] — from the panel container's environment (its .env)
    local v
    v=$(docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$PANEL_C" | sed -n "s/^$1=//p" | head -1)
    v="${v%\"}"; v="${v#\"}"; v="${v%\'}"; v="${v#\'}"
    echo "${v:-${2:-}}"
}

# panel_env: fill MIT_PANEL_* from the local panel unless they were given
# panel.env holds the Mit Panel address and the superadmin's credentials on a
# server that is not the panel's own, so `register` works there too. It is read
# (never sourced: this file is root-owned and may hold a password, and nothing
# here needs to execute it) before the local-panel autodiscovery below.
PANEL_CREDS="$ETC/panel.env"

panel_creds_load() {
    [ -n "${MIT_PANEL_URL:-}" ] && return 0
    [ -f "$PANEL_CREDS" ] || return 0
    local line k v
    while IFS= read -r line || [ -n "$line" ]; do
        case "$line" in ''|\#*) continue ;; esac
        k=${line%%=*}; v=${line#*=}
        v=${v%\"}; v=${v#\"}
        case "$k" in
            MIT_PANEL_URL) MIT_PANEL_URL=$v ;;
            MIT_PANEL_USER) MIT_PANEL_USER=$v ;;
            MIT_PANEL_PASS) MIT_PANEL_PASS=$v ;;
            MIT_PANEL_INSECURE) MIT_PANEL_INSECURE=$v ;;
        esac
    done < "$PANEL_CREDS"
    export MIT_PANEL_URL MIT_PANEL_USER MIT_PANEL_PASS MIT_PANEL_INSECURE
}

panel_env() {
    panel_creds_load
    [ -n "${MIT_PANEL_URL:-}" ] && return 0
    find_panel || return 0
    local scheme=http
    [ -n "$(panel_var SSL_CERTFILE)" ] && { scheme=https; export MIT_PANEL_INSECURE=1; }
    export MIT_PANEL_URL="$scheme://127.0.0.1:$(panel_var PORT 8000)/$(panel_var URLPATH dashboard)"
    export MIT_PANEL_USER="$(panel_var ADMIN_USERNAME)"
    export MIT_PANEL_PASS="$(panel_var ADMIN_PASSWORD)"
    say "using the Mit Panel on this server ($PANEL_C, $MIT_PANEL_URL)"
}

panel_up() { # wait for the panel's login page
    local i
    for i in $(seq 1 "${PANEL_WAIT:-90}"); do
        curl -fsk -o /dev/null -m 3 "$1/login" && return 0
        sleep 2
    done
    return 1
}

cmd_panel_update() {
    if ! find_panel; then
        warn "no Mit Panel container runs on this server; update it on its own server with: bash install.sh panel-update"
        return 0
    fi
    [ -n "$PANEL_DIR" ] && [ -n "$PANEL_SERVICE" ] || die "$PANEL_C was not started with docker compose; update it by hand"
    local ts old url
    ts=$(date +%Y%m%d_%H%M%S)
    old="mit-panel-previous:$ts" # kept so a failed update can go back
    mkdir -p "$BACKUPS"; chmod 700 "$BACKUPS"
    if [ -n "$PANEL_DATA" ] && [ -d "$PANEL_DATA" ]; then
        tar -czf "$BACKUPS/panel-data-$ts.tar.gz" -C "$PANEL_DATA" .
        say "panel data backed up to $BACKUPS/panel-data-$ts.tar.gz"
    fi
    say "getting Mit Panel ($PANEL_BRANCH)"
    if [ -d "$PANEL_SRC/.git" ]; then
        git -C "$PANEL_SRC" fetch -q --depth 1 origin "$PANEL_BRANCH" && git -C "$PANEL_SRC" checkout -q -f FETCH_HEAD
    else
        rm -rf "$PANEL_SRC"
        git clone -q --depth 1 -b "$PANEL_BRANCH" "$PANEL_REPO" "$PANEL_SRC"
    fi
    say "building the panel image $PANEL_IMAGE (a few minutes)"
    docker tag "$(docker inspect -f '{{.Image}}' "$PANEL_C")" "$old" 2>/dev/null ||
        docker tag "$PANEL_IMAGE" "$old"
    if ! docker build -q -t "$PANEL_IMAGE" "$PANEL_SRC" >/dev/null; then
        docker rmi "$old" >/dev/null 2>&1 || true
        die "building the new panel failed; the panel was not touched"
    fi
    # keep a clone of the panel in its own folder on the same version, so a
    # later "docker compose up --build" there doesn't bring the old one back
    if [ -d "$PANEL_DIR/.git" ]; then
        git -C "$PANEL_DIR" fetch -q origin "$PANEL_BRANCH" 2>/dev/null &&
            git -C "$PANEL_DIR" checkout -q "$PANEL_BRANCH" 2>/dev/null &&
            git -C "$PANEL_DIR" merge -q --ff-only FETCH_HEAD 2>/dev/null ||
            warn "$PANEL_DIR has local changes; left it on its current version"
    fi
    say "restarting the panel"
    local files=() f
    IFS=, read -ra f <<< "$PANEL_FILES"
    for x in "${f[@]}"; do files+=(-f "$x"); done
    (cd "$PANEL_DIR" && docker compose "${files[@]}" up -d --no-build --force-recreate "$PANEL_SERVICE")
    find_panel || true # still the same container name if it already stopped
    url="http://127.0.0.1:$(panel_var PORT 8000)/$(panel_var URLPATH dashboard)"
    [ -n "$(panel_var SSL_CERTFILE)" ] && url="https${url#http}"
    if ! panel_up "$url"; then
        warn "the new panel did not come up — going back to the previous one"
        docker tag "$old" "$PANEL_IMAGE"
        (cd "$PANEL_DIR" && docker compose "${files[@]}" up -d --no-build --force-recreate "$PANEL_SERVICE")
        die "panel update failed; the old panel runs again (logs: docker logs $PANEL_C)"
    fi
    say "Mit Panel updated (previous image kept as $old)"
}

# php_bot_dirs: every PHP bot directory this server holds — the ones named in
# MIGRATE_DIRS (colon separated) first, then one level under /var/www/html. A
# directory counts as a bot when its own config.php exists, which is how
# mirzapanel's "additional bot" folders are found under whatever the operator
# named them, and how a mirza_pro install is found next to them.
php_bot_dirs() {
    {
        local d IFS=:
        for d in ${MIGRATE_DIRS:-}; do
            d=${d%/}
            [ -f "$d/config.php" ] && readlink -f "$d"
        done
        for d in /var/www/html/*/; do
            [ -f "$d/config.php" ] || continue
            readlink -f "${d%/}"
        done
    } | awk '!seen[$0]++'
}

# next_free_N: the lowest bot number nothing here is using. A number counts as
# taken when a config, an nginx backup, a running unit or a listening port
# already answers for it, so a new source never lands on an existing bot.
next_free_N() {
    local n=1 taken
    while [ "$n" -le 200 ]; do
        taken=0
        [ -f "$ETC/bot$n.env" ] && taken=1
        [ -f "$ETC/bot$n.nginx.path" ] && taken=1
        systemctl is-active --quiet "mitseller@$n" 2>/dev/null && taken=1
        ss -ltn 2>/dev/null | grep -q ":$(port_for "$n") " && taken=1
        [ "$taken" = 0 ] && { echo "$n"; return 0; }
        n=$((n+1))
    done
    return 1
}

# already_migrated php-dir — reports whether this directory was moved over by a
# previous run (cmd_migrate records it in botN.phpdir).
already_migrated() {
    local f
    for f in "$ETC"/bot*.phpdir; do
        [ -f "$f" ] || continue
        [ "$(readlink -f "$(cat "$f")" 2>/dev/null)" = "$(readlink -f "$1")" ] && return 0
    done
    return 1
}

# panel_link: the command that connects bots on ANOTHER server to the panel
# running here, with its address and superadmin login filled in
cmd_panel_link() {
    find_panel || die "no Mit Panel container runs on this server"
    local host port path cert
    port=$(panel_var PORT 8000); path=$(panel_var URLPATH dashboard); cert=$(panel_var SSL_CERTFILE)
    if [ -n "$cert" ]; then
        host=$(docker exec "$PANEL_C" cat "$cert" 2>/dev/null | openssl x509 -noout -ext subjectAltName 2>/dev/null |
            grep -o 'DNS:[^,]*' | head -1 | cut -d: -f2 | tr -d ' ')
        host="https://${host:-$(hostname -f)}"
    else
        host="http://$(ip -4 route get 1.1.1.1 2>/dev/null | grep -o 'src [0-9.]*' | cut -d' ' -f2)"
    fi
    echo
    echo "On the server with the bots, run:"
    echo
    echo "curl -sLo /root/mitseller-install.sh $RAW_URL"
    printf "MIT_PANEL_URL=%q MIT_PANEL_USER=%q MIT_PANEL_PASS=%q bash /root/mitseller-install.sh migrate-all\n" \
        "$host:$port/$path" "$(panel_var ADMIN_USERNAME)" "$(panel_var ADMIN_PASSWORD)"
    echo
}

cmd_all() {
    cmd_panel_update
    if [ -z "$(php_bot_dirs)" ] && ! ls "$ETC"/bot*.env >/dev/null 2>&1; then
        say "no PHP bot on this server"
        find_panel && cmd_panel_link
        return 0
    fi
    cmd_migrate_all
}

register_with_panel() { # N — MIT_PANEL_* given, or a Mit Panel on this server
    [ "${MITSELLER_DEFER_REGISTER:-}" = 1 ] && return 0 # migrate-all connects them all at the end
    panel_env
    shared_ids_env
    if [ -z "${MIT_PANEL_URL:-}" ] || [ -z "${MIT_PANEL_USER:-}" ] || [ -z "${MIT_PANEL_PASS:-}" ]; then
        warn "bot $1 is not connected to Mit Panel (no panel on this server); see: bash install.sh panel-link on the panel's server"
        return 0
    fi
    say "connecting bot $1 to Mit Panel"
    if ! "$BIN" panel-register -c "$ETC/bot$1.env"; then
        warn "bot $1 runs, but connecting it to Mit Panel failed; retry with: N=$1 bash install.sh register"
        return 1
    fi
}

cmd_register() {
    N="${N:?set N= (bot number)}"
    [ -f "$ETC/bot$N.env" ] || die "$ETC/bot$N.env not found"
    panel_env
    [ -n "${MIT_PANEL_URL:-}" ] && [ -n "${MIT_PANEL_USER:-}" ] && [ -n "${MIT_PANEL_PASS:-}" ] ||
        die "set MIT_PANEL_URL, MIT_PANEL_USER and MIT_PANEL_PASS"
    register_with_panel "$N"
}

# panel-creds: remember the Mit Panel this server's bots belong to, so
# `register` and the provisioner can connect them without the credentials being
# typed again. Run it on the bot server, not on the panel's.
#
#   URL=https://panel.example.com USER=admin PASS=... bash install.sh panel-creds
cmd_panel_creds() {
    local URL="${URL:?set URL= (Mit Panel address, with its path)}"
    local USER="${USER:?set USER= (Mit Panel superadmin username)}"
    local PASS="${PASS:?set PASS= (Mit Panel superadmin password)}"
    case "$URL" in http://*|https://*) ;; *) die "URL must start with http:// or https://" ;; esac
    install -d -m 0750 -o root -g mitseller "$ETC"
    umask 0077
    {
        printf 'MIT_PANEL_URL=%s\n' "$URL"
        printf 'MIT_PANEL_USER=%s\n' "$USER"
        printf 'MIT_PANEL_PASS=%s\n' "$PASS"
    } > "$PANEL_CREDS.tmp"
    chown root:mitseller "$PANEL_CREDS.tmp"
    chmod 0640 "$PANEL_CREDS.tmp"
    mv -f "$PANEL_CREDS.tmp" "$PANEL_CREDS"
    say "saved to $PANEL_CREDS (root:mitseller 0640)"
    say "register a bot with: N=<n> bash install.sh register"
}

# ------------------------------------------- the provisioner (what the panel calls)
#
# provisioner-setup starts a small TLS API that lets Mit Panel install a new bot
# on this server. The panel runs in a container with no shell and no SSH, so
# this is the only way it can ask. It is a privileged endpoint by design — it
# runs install.sh new as root — so it is guarded three ways: a bearer token the
# panel holds, its own self-signed certificate the panel pins, and a rate limit.
#
#   bash install.sh provisioner-setup
#   SERVER_IP=176.124.199.78 bash install.sh provisioner-setup   # shown in the output
PROVISIONER_ENV="$ETC/provisioner.env"
PROVISIONER_CRT="$ETC/provisioner.crt"
PROVISIONER_KEY="$ETC/provisioner.key"
PROVISIONER_UNIT=/etc/systemd/system/mitseller-provisioner.service

cmd_provisioner_setup() {
    local PORT=18100 SELF
    SELF=$(readlink -f "${BASH_SOURCE[0]}")
    if ss -ltn 2>/dev/null | grep -q ":$PORT "; then
        die "port $PORT is already in use"
    fi
    install_binary
    install -d -m 0750 -o root -g mitseller "$ETC"

    local SERVER_ADDR="${SERVER_IP:-$(hostname -I 2>/dev/null | awk '{print $1}')}"
    if [ ! -f "$PROVISIONER_CRT" ] || [ ! -f "$PROVISIONER_KEY" ]; then
        say "generating the provisioner certificate"
        umask 0077
        # The address goes in as a SAN so ordinary TLS clients accept it; the
        # panel trusts the certificate itself and does not care about the name,
        # which is what lets it address this server by IP or by hostname.
        #
        # This one certificate is both the trust anchor and the server's own
        # certificate, so it has to say both things at once. CA:TRUE is what
        # makes it an anchor at all; digitalSignature and keyEncipherment are
        # what make it usable as a TLS server certificate. Leaving out either
        # half and clients refuse it — as "self-signed" or as "unsuitable
        # certificate purpose" — which is exactly the failure this setup
        # re-checks for below.
        openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \
            -keyout "$PROVISIONER_KEY" -out "$PROVISIONER_CRT" \
            -subj "/CN=mit-seller-provisioner" \
            -addext "basicConstraints=critical,CA:TRUE" \
            -addext "keyUsage=critical,digitalSignature,keyEncipherment,keyCertSign,cRLSign" \
            -addext "subjectAltName=DNS:mit-seller-provisioner${SERVER_ADDR:+,IP:$SERVER_ADDR}" >/dev/null 2>&1 ||
            die "openssl could not make a certificate"
        chown root:mitseller "$PROVISIONER_KEY" "$PROVISIONER_CRT"
        chmod 0640 "$PROVISIONER_KEY" "$PROVISIONER_CRT"
    fi

    local TOKEN=""
    if [ -f "$PROVISIONER_ENV" ]; then
        TOKEN=$(sed -n 's/^MIT_PROVISION_TOKEN=//p' "$PROVISIONER_ENV" | tr -d '"')
    fi
    if [ -z "$TOKEN" ]; then
        TOKEN=$(openssl rand -hex 32)
        umask 0077
        {
            printf 'MIT_PROVISION_TOKEN=%s\n' "$TOKEN"
            printf 'MIT_PROVISION_CERT=%s\n' "$PROVISIONER_CRT"
            printf 'MIT_PROVISION_KEY=%s\n' "$PROVISIONER_KEY"
        } > "$PROVISIONER_ENV.tmp"
        chown root:mitseller "$PROVISIONER_ENV.tmp"
        chmod 0640 "$PROVISIONER_ENV.tmp"
        mv -f "$PROVISIONER_ENV.tmp" "$PROVISIONER_ENV"
        say "token generated in $PROVISIONER_ENV"
    else
        say "keeping the existing token in $PROVISIONER_ENV"
    fi

    umask 022
    cat > "$PROVISIONER_UNIT" <<UNIT
[Unit]
Description=Mit Panel bot provisioner (lets the panel install a bot here)
After=network.target mysql.service
Wants=mysql.service

[Service]
Type=simple
ExecStart=$BIN provisioner -env $PROVISIONER_ENV -installer $SELF -config-dir $ETC
Restart=always
RestartSec=5
User=root
Group=mitseller
UMask=0027
# install.sh new needs to write nginx, certificates and mysql, so this service
# is root by design. The bearer token in $PROVISIONER_ENV is the only way in.
NoNewPrivileges=false
PrivateTmp=true
ProtectHome=true
ProtectSystem=full

[Install]
WantedBy=multi-user.target
UNIT
    systemctl daemon-reload
    systemctl enable --now mitseller-provisioner.service >/dev/null 2>&1
    sleep 1
    systemctl is-active --quiet mitseller-provisioner.service ||
        die "the provisioner did not start: journalctl -u mitseller-provisioner"
    # A certificate clients will not accept as a trust anchor is worse than no
    # certificate at all: the panel will quietly send nothing and the reason
    # will be three servers away. Check it here, once, at setup.
    if ! printf '' | openssl s_client -connect "127.0.0.1:$PORT" -CAfile "$PROVISIONER_CRT" \
        -servername mit-seller-provisioner >/dev/null 2>&1; then
        die "the certificate in $PROVISIONER_CRT is not accepted as a trust anchor; remove the .crt and .key and run provisioner-setup again"
    fi
    cmd_provisioner_print
}

cmd_provisioner_print() {
    [ -f "$PROVISIONER_ENV" ] || die "no provisioner yet; run: bash install.sh provisioner-setup"
    local TOKEN="" CRT="" IP=""
    TOKEN=$(sed -n 's/^MIT_PROVISION_TOKEN=//p' "$PROVISIONER_ENV" | tr -d '"')
    CRT=$(sed -n 's/^MIT_PROVISION_CERT=//p' "$PROVISIONER_ENV" | tr -d '"')
    IP="${SERVER_IP:-$(hostname -I 2>/dev/null | awk '{print $1}')}"
    echo
    echo "Mit Panel → تنظیمات → سرور mit-seller — paste these three:"
    echo
    printf '  mitseller_server          = %s\n' "$IP"
    printf '  mitseller_provision_token = %s\n' "$TOKEN"
    echo
    echo "  mitseller_server_cert ="
    sed 's/^/  /' "$CRT"
    echo
    echo "  The panel reaches this server at https://$IP:18100/provision"
}

cmd_provisioner_teardown() {
    systemctl disable --now mitseller-provisioner.service >/dev/null 2>&1 || true
    rm -f "$PROVISIONER_UNIT"
    systemctl daemon-reload
    rm -f "$PROVISIONER_ENV"
    warn "removed the unit and $PROVISIONER_ENV; the certificate and key are left in $ETC"
}

# shared_admin_ids N... — the Telegram ids that are admin on every one of these
# bots (with two or more bots): the server owner's own id, which says nothing
# about whose bot it is. MIT_OWNER_ID adds ids by hand.
shared_admin_ids() {
    local common="" n ids
    if [ $# -ge 2 ]; then
        for n in "$@"; do
            ids=$("$BIN" admin-ids -c "$ETC/bot$n.env" 2>/dev/null | sort -u) && [ -n "$ids" ] || continue # unreadable: left out
            if [ -z "$common" ]; then common="$ids"; else common=$(comm -12 <(echo "$common") <(echo "$ids")); fi
            [ -n "$common" ] || break
        done
    fi
    local extra="${MIT_OWNER_ID:-}"
    printf '%s\n' $common ${extra//,/ } | grep -E '^[0-9]+$' | sort -u | paste -sd, - || true
}

running_bots() { # numbers of the Go bots running here
    local f n
    for f in "$ETC"/bot*.env; do
        [ -f "$f" ] || continue
        n=${f##*/bot}; n=${n%.env}
        case "$n" in ''|*[!0-9]*) continue ;; esac
        systemctl is-active --quiet "mitseller@$n" && echo "$n"
    done
}

shared_ids_env() { # once: MIT_PANEL_IGNORE_IDS from every bot running here
    [ -n "${MIT_PANEL_IGNORE_IDS+x}" ] && return 0
    MIT_PANEL_IGNORE_IDS=$(shared_admin_ids $(running_bots))
    export MIT_PANEL_IGNORE_IDS
    if [ -n "$MIT_PANEL_IGNORE_IDS" ]; then
        say "admin on every bot, so not used to find whose bot it is: $MIT_PANEL_IGNORE_IDS"
    fi
}

# register_all N... — connect these bots to Mit Panel, each to its owner
register_all() {
    [ $# -gt 0 ] || return 0
    panel_env
    local n
    shared_ids_env
    for n in "$@"; do
        register_with_panel "$n" || true
    done
}

cmd_migrate_all() {
    local self ok=() failed=() skipped=() dir n f
    legacy_warn
    self=$(readlink -f "$0")
    [ -f "$self" ] || die "save this script to a file first (curl -sLo /root/mitseller-install.sh …) and run that file"
    install_binary
    install_unit
    panel_env
    export MITSELLER_BIN="$BIN" # every bot below uses the binary just installed
    while read -r dir; do
        [ -n "$dir" ] || continue
        [ -f "$dir/config.php" ] || { warn "$dir has no config.php; skipped"; continue; }
        # a bot already on Go is skipped — whether it was recorded here or was
        # found by its old botmirzapanel<N> directory name
        n=""
        case "$dir" in
            */botmirzapanel[0-9]*)
                n=${dir##*botmirzapanel}
                case "$n" in ''|*[!0-9]*) n="" ;; esac ;;
        esac
        if [ -n "$n" ] && systemctl is-active --quiet "mitseller@$n"; then
            say "bot $n ($dir) already runs on Go"
            skipped+=("$n")
            continue
        fi
        if already_migrated "$dir"; then
            n=""
            for f in "$ETC"/bot*.phpdir; do
                [ -f "$f" ] || continue
                if [ "$(readlink -f "$(cat "$f")" 2>/dev/null)" = "$(readlink -f "$dir")" ]; then
                    n=${f##*/bot}; n=${n%.phpdir}; break
                fi
            done
            say "bot ${n:-?} ($dir) already runs on Go"
            [ -n "$n" ] && skipped+=("$n")
            continue
        fi
        n=$(next_free_N) || { warn "$dir: no free bot number below 200; skipped"; skipped+=("?"); continue; }
        echo
        say "======== bot $n ($dir) ========"
        set +e
        MITSELLER_DEFER_REGISTER=1 N="$n" PHP_DIR="$dir" bash "$self" migrate 2>&1 | tee "$BACKUPS/migrate-bot$n.log"
        local rc=${PIPESTATUS[0]}
        set -e
        if [ "$rc" = 0 ]; then ok+=("$n"); else failed+=("$n"); fi
    done < <(php_bot_dirs)
    register_all "${ok[@]}" "${skipped[@]}"
    echo
    echo "=========================================="
    echo "moved to Go:        ${ok[*]:-—}"
    echo "already on Go:      ${skipped[*]:-—}"
    echo "failed (still PHP): ${failed[*]:-—}"
    [ ${#failed[@]} -gt 0 ] && echo "logs:               $BACKUPS/migrate-bot<N>.log"
    echo "=========================================="
    [ ${#failed[@]} -eq 0 ]
}

# ----------------------------------------------------------------- new bot

cmd_new() {
    N="${N:?set N= (bot number, e.g. 7)}"
    TOKEN="${TOKEN:?set TOKEN= (bot token from @BotFather)}"
    DOMAIN="${DOMAIN:?set DOMAIN= (domain of this bot, A record must already point here)}"
    ADMIN="${ADMIN:?set ADMIN= (your numeric Telegram id)}"
    MIT_SECRET="${MIT_SECRET:?set MIT_SECRET= (secret code that gates panel management)}"
    CERT_EMAIL="${CERT_EMAIL:?set CERT_EMAIL= (email for the SSL certificate)}"
    DBPASS="${DBPASS:-$(openssl rand -hex 12)}"
    BACKUP_MINUTE="${BACKUP_MINUTE:-$((RANDOM % 60))}"
    local DBNAME="mitseller${N}" DBUSER="mitseller${N}user" CFG="$ETC/bot${N}.env" PORT
    PORT=$(port_for "$N")

    [ -e "$CFG" ] && die "$CFG already exists; bot $N is installed"
    legacy_warn
    if ss -ltn 2>/dev/null | grep -q ":$PORT "; then
        die "port $PORT is already in use (a pre-rebrand bot may be running as nexrabot@$N); pick another N"
    fi
    getent hosts "$DOMAIN" >/dev/null || die "DNS for $DOMAIN is not set yet; point the A record here first"
    for t in nginx certbot mysql; do command -v "$t" >/dev/null || die "$t is not installed"; done
    python3 -c "import certbot_nginx" 2>/dev/null ||
        die "certbot's nginx plugin is missing — install it first: apt install python3-certbot-nginx"

    install_binary
    install_unit

    say "resolving the bot username"
    local BOT_USER
    BOT_USER=$(curl -fsS "${MITSELLER_TELEGRAM_API:-https://api.telegram.org}/bot$TOKEN/getMe" | grep -oP '"username":\s*"\K[^"]+' || true)
    [ -n "$BOT_USER" ] || die "could not resolve the bot username; check the token"

    say "creating database $DBNAME"
    mysql <<EOF
CREATE DATABASE IF NOT EXISTS \`$DBNAME\` CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci;
CREATE USER IF NOT EXISTS '$DBUSER'@'localhost' IDENTIFIED BY '$DBPASS';
GRANT ALL PRIVILEGES ON \`$DBNAME\`.* TO '$DBUSER'@'localhost';
FLUSH PRIVILEGES;
EOF

    say "writing $CFG"
    MITSELLER_INIT_DB_PASS="$DBPASS" "$BIN" init-config --out "$CFG" --token "$TOKEN" --admin "$ADMIN" \
        --domain "$DOMAIN" --bot-username "$BOT_USER" --secret "$MIT_SECRET" \
        --db-name "$DBNAME" --db-user "$DBUSER" --listen "127.0.0.1:$PORT"
    secure_config "$CFG"
    "$BIN" schema -c "$CFG" >/dev/null
    "$BIN" check -c "$CFG"

    say "starting mitseller@$N"
    systemctl enable --now "mitseller@$N"
    wait_healthy "http://127.0.0.1:$PORT/healthz" 20 || die "the bot did not start; see: journalctl -u mitseller@$N"

    say "nginx site for $DOMAIN"
    proxy_snippet "$N"
    cat > "/etc/nginx/sites-available/mitseller${N}" <<EOF
server {
    listen 80;
    server_name $DOMAIN;
    include /etc/nginx/snippets/mitseller${N}.conf;
}
EOF
    ln -sf "/etc/nginx/sites-available/mitseller${N}" "/etc/nginx/sites-enabled/mitseller${N}"
    nginx -t && systemctl reload nginx

    say "SSL certificate"
    # certbot's own error for a missing plugin ("the requested nginx plugin
    # does not appear to be installed") arrives only after the bot, the
    # database and the site are already in place, which leaves a half-finished
    # install to unwind. Check first, and say how to fix it.
    python3 -c "import certbot_nginx" 2>/dev/null ||
        die "certbot's nginx plugin is missing — install it and rerun: apt install python3-certbot-nginx"
    certbot --nginx -d "$DOMAIN" --agree-tos --redirect --no-eff-email -m "$CERT_EMAIL"
    wait_healthy "https://$DOMAIN/healthz" 20 || die "https://$DOMAIN/healthz does not answer"

    say "webhook"
    "$BIN" set-webhook -c "$CFG"

    say "hourly backups (importable with: FILE=<bundle> bash install.sh bot-import)"
    backup_setup

    echo
    echo "=========================================="
    echo "done: @$BOT_USER on https://$DOMAIN"
    echo "database: $DBNAME / $DBUSER / $DBPASS"
    print_keys "$N"
    echo "=========================================="
    # into Mit Panel too, when it runs here or MIT_PANEL_* are given
    register_with_panel "$N" || true
}

# --------------------------------------------- hourly, importable backups

# backup_setup: one hourly job backing up every bot installed on this server.
#
# A backup is the same bundle bot-export writes, so it is not just a copy of
# the database — "FILE=<bundle> bash install.sh bot-import" rebuilds the whole
# bot from it on this server or any other. That is the difference between a
# backup you can restore and one you can only hope about.
#
# KEEP_BACKUPS= keeps that many hourly bundles per bot (default 48 = 2 days).
# It is safe to run at any time: export-bundle only reads a running bot.
backup_setup() {
    local SCRIPT=/usr/local/sbin/mitseller-backup
    mkdir -p "$BACKUPS"
    chmod 700 "$BACKUPS"

    cat > "$SCRIPT" <<'EOF'
#!/bin/bash
# Back up every bot on this server, once an hour.
#
# Each backup is the bundle install.sh bot-export writes, so it can be
# rebuilt on any server with:  FILE=<bundle> bash install.sh bot-import
# A running bot is only read, never touched, so this is safe to run by hand.
set -uo pipefail

BACKUP_DIR=@@BACKUPS@@
KEEP=${KEEP_BACKUPS:-48}                 # 48 hourly bundles = 2 days per bot
BIN=${MITSELLER_BIN:-@@BIN@@}
TG=${MITSELLER_TELEGRAM_API:-https://api.telegram.org}
mkdir -p "$BACKUP_DIR" && chmod 700 "$BACKUP_DIR"
ERR="$BACKUP_DIR/last-error.log"
TS=$(date +%Y%m%d_%H%M%S)
rc=0

for cfg in @@ETC@@/bot*.env; do
    [ -f "$cfg" ] || continue
    name=$(basename "$cfg" .env)
    out="$BACKUP_DIR/${name}-bundle-${TS}.tar.gz"
    if "$BIN" export-bundle -c "$cfg" --out "$out" >/dev/null 2>>"$ERR"; then
        printf '%s  %s  %s\n' "$name" "$(du -h "$out" | cut -f1)" "$(basename "$out")"
    else
        printf '%s  FAILED (see %s)\n' "$name" "$ERR" >&2
        rm -f "$out"; rc=1; continue
    fi
    # off the server too, so a lost server does not take the backups with it
    chat=$(sed -n 's/^ADMIN_ID=//p' "$cfg")
    token=$(sed -n 's/^BOT_TOKEN=//p' "$cfg")
    user=$(sed -n 's/^BOT_USERNAME=//p' "$cfg")
    if [ -n "$chat" ] && [ -n "$token" ]; then
        curl -s -m 60 -F chat_id="$chat" -F document=@"$out" \
            -F caption="بکاپ خودکار $user 🗄" "$TG/bot$token/sendDocument" >/dev/null || true
    fi
done

# drop the oldest bundles, keeping KEEP per bot
for cfg in @@ETC@@/bot*.env; do
    [ -f "$cfg" ] || continue
    name=$(basename "$cfg" .env)
    ls -1t "$BACKUP_DIR"/${name}-bundle-*.tar.gz 2>/dev/null | tail -n +$((KEEP + 1)) | xargs -r rm -f
done
exit $rc
EOF
    # bake the paths in: cron has no idea what install.sh's variables were
    sed -i -e "s|@@BACKUPS@@|$BACKUPS|g" -e "s|@@ETC@@|$ETC|g" -e "s|@@BIN@@|$BIN|g" "$SCRIPT"
    chmod 700 "$SCRIPT"

    # one line for every bot on the server, so a new bot is covered by the
    # same job without anyone having to remember to add one. A server with no
    # crontab yet is the normal case, so a failed "crontab -l" must not stop
    # the line being installed.
    { crontab -l 2>/dev/null | grep -v "$SCRIPT" || true
      echo "$(date +%-M) * * * * $SCRIPT >/dev/null 2>&1"; } | crontab -
    say "hourly backup job installed: $SCRIPT (keeping ${KEEP_BACKUPS:-48} bundles per bot)"
}

cmd_backup_setup() { backup_setup; }

# backup-now: run the hourly job now, to see what it produces or to take an
# extra backup before something you are about to change.
cmd_backup_now() {
    [ -x /usr/local/sbin/mitseller-backup ] ||
        die "no backup job yet; run: bash install.sh backup-setup"
    say "backing up every bot"
    /usr/local/sbin/mitseller-backup
}

# ------------------------------------------- move a bot from another server

# bot-export: pack a bot up on the server it currently runs on. Read-only —
# the bot keeps running, keeps its crontab, keeps its nginx site. PHP_DIR=
# names a PHP bot, N= names a bot already on Go here.
cmd_bot_export() {
    local OUT="${OUT:?set OUT= (bundle file to write, e.g. /root/bot7-bundle.tar.gz)}"
    case "$OUT" in /*) ;; *) OUT="$PWD/$OUT" ;; esac
    [ -e "$OUT" ] && die "$OUT already exists; pick another name"

    if [ -n "${PHP_DIR:-}" ] && [ -n "${N:-}" ]; then
        die "give PHP_DIR= (a PHP bot) or N= (a bot already on Go), not both"
    fi
    if [ -z "${PHP_DIR:-}" ] && [ -z "${N:-}" ]; then
        die "set PHP_DIR= (directory of the PHP bot) or N= (bot number on this server)"
    fi

    install_binary
    if [ -n "${PHP_DIR:-}" ]; then
        PHP_DIR="${PHP_DIR%/}"
        [ -f "$PHP_DIR/config.php" ] || die "$PHP_DIR/config.php not found"
        say "exporting the PHP bot in $PHP_DIR"
        "$BIN" export-bundle --php-dir "$PHP_DIR" --out "$OUT"
    else
        local CFG="$ETC/bot${N}.env"
        [ -f "$CFG" ] || die "$CFG not found"
        say "exporting bot $N"
        "$BIN" export-bundle -c "$CFG" --out "$OUT"
    fi
    say "bundle written: $OUT"
    say "copy it to the other server and run: FILE=$OUT bash install.sh bot-import"
}

# bot-import: install a bundle as a new bot on this server. It is loaded and
# answering locally, but held dark: no webhook, no crons, no panel. Nothing
# here can disturb the bot that is still running on the other server.
cmd_bot_import() {
    local FILE="${FILE:?set FILE= (the bundle made by bot-export)}"
    [ -f "$FILE" ] || die "$FILE not found"
    if [ -n "${PHP_DIR:-}" ]; then die "bot-import does not take PHP_DIR; it reads the bundle"; fi

    # read before the "local" below, which would shadow these with empties
    local IN_DOMAIN="${DOMAIN:-}" IN_CERT_EMAIL="${CERT_EMAIL:-}"
    local N="${N:-}" CFG PORT DBNAME DBUSER DBPASS TS
    if [ -z "$N" ]; then
        N=$(next_free_N) || die "no free bot number below 200"
        say "N is not set: using bot number $N"
    fi
    CFG="$ETC/bot${N}.env"; PORT=$(port_for "$N")
    DBNAME="mitseller${N}"; DBUSER="mitseller${N}user"; DBPASS=$(openssl rand -hex 12)
    TS=$(date +%Y%m%d_%H%M%S)
    local DOMAIN="$IN_DOMAIN" CERT_EMAIL="$IN_CERT_EMAIL"

    [ -e "$CFG" ] && die "$CFG already exists; bot $N is installed"
    if systemctl is-active --quiet "mitseller@$N"; then die "mitseller@$N is already running"; fi
    if ss -ltn 2>/dev/null | grep -q ":$PORT "; then die "port $PORT is already in use"; fi
    for t in nginx mysql; do command -v "$t" >/dev/null || die "$t is not installed"; done

    install_binary
    install_unit

    say "creating database $DBNAME"
    mysql <<EOF
CREATE DATABASE IF NOT EXISTS \`$DBNAME\` CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci;
CREATE USER IF NOT EXISTS '$DBUSER'@'localhost' IDENTIFIED BY '$DBPASS';
GRANT ALL PRIVILEGES ON \`$DBNAME\`.* TO '$DBUSER'@'localhost';
FLUSH PRIVILEGES;
EOF

    say "reading the bundle"
    if [ -n "$DOMAIN" ]; then
        MITSELLER_INIT_DB_PASS="$DBPASS" "$BIN" import-bundle --file "$FILE" --out "$CFG" \
            --listen "127.0.0.1:$PORT" --db-name "$DBNAME" --db-user "$DBUSER" \
            --report "$BACKUPS/bot${N}-${TS}.import-report.txt" --domain "$DOMAIN"
    else
        MITSELLER_INIT_DB_PASS="$DBPASS" "$BIN" import-bundle --file "$FILE" --out "$CFG" \
            --listen "127.0.0.1:$PORT" --db-name "$DBNAME" --db-user "$DBUSER" \
            --report "$BACKUPS/bot${N}-${TS}.import-report.txt"
    fi
    secure_config "$CFG"
    DOMAIN=$(grep '^DOMAIN=' "$CFG" | cut -d= -f2)

    say "checking the database and the token"
    "$BIN" check -c "$CFG"

    say "nginx site for $DOMAIN"
    proxy_snippet "$N"
    cat > "/etc/nginx/sites-available/mitseller${N}" <<EOF
server {
    listen 80;
    server_name $DOMAIN;
    include /etc/nginx/snippets/mitseller${N}.conf;
}
EOF
    ln -sf "/etc/nginx/sites-available/mitseller${N}" "/etc/nginx/sites-enabled/mitseller${N}"
    nginx -t && systemctl reload nginx

    if [ -n "$CERT_EMAIL" ]; then
        say "SSL certificate"
        certbot --nginx -d "$DOMAIN" --agree-tos --redirect --no-eff-email -m "$CERT_EMAIL" ||
            warn "certbot failed; the bot still answers on http://$DOMAIN — rerun certbot before cutting over"
    else
        warn "no CERT_EMAIL given: $DOMAIN has no certificate yet. Set one before cutting over (Telegram needs https)"
    fi

    say "starting mitseller@$N (held dark: no webhook, no crons)"
    systemctl enable --now "mitseller@$N"
    wait_healthy "http://127.0.0.1:$PORT/healthz" 20 ||
        die "the bot did not start; see: journalctl -u mitseller@$N"

    echo
    echo "=========================================="
    echo "bot $N is loaded here but is NOT yet live."
    echo "it is answering on http://$DOMAIN (and https, once it has a certificate)."
    echo "the bot on the other server still answers Telegram; nothing has been disturbed."
    echo
    echo "when the domain points here and you are ready to move the bot across:"
    echo "  N=$N bash install.sh bot-cutover"
    echo "undo this import (the other server is untouched):"
    echo "  N=$N bash install.sh bot-unimport"
    echo "logs: journalctl -u mitseller@$N -f"
    echo "=========================================="
}

# bot-cutover: the moment the bot moves. It takes the webhook from the other
# server and starts its own cron jobs. Run it only once the domain resolves
# here — until then Telegram would be told to send updates into a void.
cmd_bot_cutover() {
    N="${N:?set N= (bot number)}"
    local CFG="$ETC/bot${N}.env" PORT DOMAIN
    [ -f "$CFG" ] || die "$CFG not found"
    PORT=$(port_for "$N"); DOMAIN=$(grep '^DOMAIN=' "$CFG" | cut -d= -f2)

    install_binary
    getent hosts "$DOMAIN" >/dev/null ||
        die "DNS for $DOMAIN does not point here yet; cutting over now would stop the bot answering"
    "$BIN" check -c "$CFG" || die "the bot is not healthy enough to cut over"
    systemctl is-active --quiet "mitseller@$N" || { systemctl start "mitseller@$N"; wait_healthy "http://127.0.0.1:$PORT/healthz" 20; }

    say "taking the webhook — from here this server answers Telegram for @$(grep '^BOT_USERNAME=' "$CFG" | cut -d= -f2)"
    "$BIN" set-webhook -c "$CFG"

    say "starting the cron jobs"
    sed -i '/^DISABLE_CRONS=1$/d' "$CFG"
    systemctl restart "mitseller@$N"
    wait_healthy "http://127.0.0.1:$PORT/healthz" 20 || die "the bot did not restart; see: journalctl -u mitseller@$N"

    register_with_panel "$N" || true

    echo
    echo "=========================================="
    echo "bot $N is live on https://$DOMAIN"
    echo "stop the old bot on the other server now: its PHP cron jobs would"
    echo "otherwise keep messaging your users from a database that is no longer live."
    echo "undo (put the webhook back on the other server):"
    echo "  the other server still has its PHP bot; run there:"
    echo "  bash install.sh bot-uncutover DOMAIN=<its domain>"
    echo "logs: journalctl -u mitseller@$N -f"
    echo "=========================================="
}

# bot-unimport: undo an import that was never cut over. The other server was
# never touched, so this is safe at any time before bot-cutover.
cmd_bot_unimport() {
    N="${N:?set N= (bot number)}"
    local CFG="$ETC/bot${N}.env" PORT DOMAIN SITE
    [ -f "$CFG" ] || die "$CFG not found"
    PORT=$(port_for "$N"); DOMAIN=$(grep '^DOMAIN=' "$CFG" | cut -d= -f2)

    systemctl disable --now "mitseller@$N" 2>/dev/null || true
    rm -f "$CFG" "$ETC/bot${N}.nginx.path" "$ETC/bot${N}.phpdir" "$ETC/bot${N}.crontab.bak"
    SITE="/etc/nginx/sites-available/mitseller${N}"
    rm -f "$SITE" "/etc/nginx/sites-enabled/mitseller${N}" "/etc/nginx/snippets/mitseller${N}.conf"
    nginx -t && systemctl reload nginx

    local DBNAME="mitseller${N}" DBUSER="mitseller${N}user"
    if [ "${KEEP_DB:-0}" != 1 ]; then
        say "dropping database $DBNAME (set KEEP_DB=1 to keep it)"
        mysql -e "DROP DATABASE IF EXISTS \`$DBNAME\`; DROP USER IF EXISTS '$DBUSER'@'localhost'; FLUSH PRIVILEGES;" ||
            warn "could not drop $DBNAME; remove it by hand"
    else
        say "keeping database $DBNAME"
    fi
    say "bot $N removed; the bot on the other server was never touched"
}

# bot-uncutover: put the webhook back on the other server. Run it THERE, after
# its PHP bot is running again, to take the bot back from a Go server.
cmd_bot_uncutover() {
    DOMAIN="${DOMAIN:?set DOMAIN= (the domain the PHP bot is served on)}"
    local TOKEN="${TOKEN:?set TOKEN= (the bot token, from config.php \$APIKEY)}"
    command -v curl >/dev/null || die "curl is not installed"
    say "moving the webhook back to https://$DOMAIN/index.php"
    curl -fsS "https://api.telegram.org/bot$TOKEN/setWebhook?url=https%3A%2F%2F$DOMAIN%2Findex.php" >/dev/null ||
        die "setWebhook failed; check the token and the domain"
    say "the other server answers Telegram again; stop the Go bot there when you can"
}

# ----------------------------------------------------------------- migrate a PHP bot

nginx_site_for() { # php dir -> site file that serves it with php-fpm
    local f
    for f in /etc/nginx/sites-enabled/* /etc/nginx/conf.d/*.conf; do
        [ -f "$f" ] || continue
        if grep -Eq "^\s*root\s+${1%/}/?;" "$f" && grep -q "fastcgi_pass" "$f"; then
            readlink -f "$f"
            return 0
        fi
    done
    return 1
}

# Replace "location / {...}" with the proxy include and drop the php-fpm
# location, in every server block of the site file (certbot copies them into
# the 443 block). Only the flat blocks install.sh/certbot write are handled;
# anything else aborts before nginx is touched.
rewrite_nginx_site() { # file N
    local out="$1.mitseller.tmp"
    N_SNIPPET="/etc/nginx/snippets/mitseller$2.conf" perl -0777 -pe '
        my $inc = $ENV{N_SNIPPET};
        my $n = s{location\s+/\s*\{[^{}]*\}}{include $inc;}g;
        s{location\s+~\s+\\\.php\$\s*\{[^{}]*\}\s*}{}g;
        die "no \"location /\" block\n" unless $n;
    ' "$1" > "$out" || { rm -f "$out"; return 1; }
    grep -q "fastcgi_pass" "$out" && { rm -f "$out"; warn "fastcgi_pass left in the site file"; return 1; }
    mv -f "$out" "$1"
}

# The PHP cron scripts the Go bot reimplements itself. A crontab line calling
# one of these is dropped at the cutover, because the Go bot runs that job
# from its own scheduler. Anything else under /cron/ or /cronbot/ is left
# alone: the Go bot has no equivalent for it, and silently stopping somebody's
# backup, uptime or lottery job would be worse than leaving it running.
GO_CRON_SCRIPTS='configtest.php cronvolume.php cronday.php removeexpire.php croncard.php sendmessage.php NoticationsService.php'

# drop_cron_lines domain — reads a crontab on stdin and writes it back without
# the lines this domain ran for jobs the Go bot now runs itself. The domain is
# anchored behind "://" so that example.com never matches a line belonging to
# evil-example.com.
drop_cron_lines() {
    local domain=$1 script s
    awk -v d="$domain" -v keep="$GO_CRON_SCRIPTS" '
    {
        if (index($0, "://" d "/cron/") == 0 && index($0, "://" d "/cronbot/") == 0) { print; next }
        script = ""
        if (match($0, /[A-Za-z0-9_.-]+\.php/)) script = substr($0, RSTART, RLENGTH)
        n = split(keep, list, " ")
        for (i = 1; i <= n; i++) if (script == list[i]) next
        print
    }'
}

# drop_all_cron_lines domain — every cron line of this domain, whichever
# script it calls. Only used on rollback: the lines the Go bot did not take
# over were never removed, so restoring the backup on top of them would
# install them twice. Wiping the domain's lines first and then putting the
# whole backup back leaves the table exactly as it was.
drop_all_cron_lines() {
    grep -v -F -e "$1/cron/" -e "$1/cronbot/"
}

cmd_migrate() {
    # N is optional when PHP_DIR names the bot: a free number is then taken.
    if [ -n "${PHP_DIR:-}" ] && [ -z "${N:-}" ]; then
        N=$(next_free_N) || die "no free bot number below 200"
        say "PHP_DIR is set and N is not: using bot number $N"
    fi
    N="${N:?set N= (bot number)}"
    local PHP_DIR="${PHP_DIR:-/var/www/html/botmirzapanel${N}}"
    PHP_DIR="${PHP_DIR%/}"
    local CFG="$ETC/bot${N}.env" PORT TS SITE DOMAIN REPORT
    PORT=$(port_for "$N")
    TS=$(date +%Y%m%d_%H%M%S)
    REPORT="$BACKUPS/bot${N}-${TS}.migrate-report.txt"

    [ -f "$PHP_DIR/config.php" ] || die "$PHP_DIR/config.php not found (set PHP_DIR=)"
    command -v nginx >/dev/null || die "nginx is not installed"
    SITE=$(nginx_site_for "$PHP_DIR") || die "no nginx site serves $PHP_DIR with php-fpm. The mirzapanel and mirza_pro installers put every bot behind one Apache site on a path; on such a server put nginx in front first with \"root $PHP_DIR;\" (docs/MIGRATION.md, section «سروری که با Apache سرو می‌شود») and run this again"
    # dry-run the nginx rewrite on a copy, so an unusual layout stops us here
    cp "$SITE" "/tmp/mitseller-site-check.$$"
    rewrite_nginx_site "/tmp/mitseller-site-check.$$" "$N" || { rm -f "/tmp/mitseller-site-check.$$"; die "$SITE has a layout this script can't rewrite safely; migrate by hand (see docs/MIGRATION.md)"; }
    rm -f "/tmp/mitseller-site-check.$$"
    if systemctl is-active --quiet "mitseller@$N"; then die "mitseller@$N is already running; nothing to migrate"; fi
    if systemctl is-active --quiet "nexrabot@$N"; then die "nexrabot@$N (pre-rebrand) is already running on port $PORT; convert it first with: bash install.sh legacy-adopt"; fi
    if ss -ltn 2>/dev/null | grep -q ":$PORT "; then die "port $PORT is already in use"; fi

    install_binary
    install_unit

    say "reading $PHP_DIR/config.php"
    "$BIN" migrate-php --php-dir "$PHP_DIR" --out "$CFG" --listen "127.0.0.1:$PORT" --skip-db
    secure_config "$CFG"
    DOMAIN=$(grep '^DOMAIN=' "$CFG" | cut -d= -f2)
    case "$DOMAIN" in
    */*)
        # the config is taken back out, so a bot number is not left claimed
        # by a bot that was never migrated
        rm -f "$CFG"
        die "the PHP bot is served under a path ($PHP_DIR/config.php says \$domainhosts = \"$DOMAIN\"). mit-seller needs a site that serves this bot at its own domain root. On an Apache server (the usual mirzapanel layout) put nginx in front first — docs/MIGRATION.md, section «سروری که با Apache سرو می‌شود» — then run this again"
        ;;
    esac

    mkdir -p "$BACKUPS"
    say "backing up the database (before anything changes)"
    "$BIN" db-dump -c "$CFG" --out "$BACKUPS/bot${N}-${TS}.sql.gz"

    say "checking the database and the token"
    "$BIN" check -c "$CFG"

    say "upgrading the schema and carrying over what the PHP bot named differently"
    "$BIN" migrate-php --php-dir "$PHP_DIR" --out "$CFG" --listen "127.0.0.1:$PORT" \
        --clean-crontab=false --report "$REPORT"
    secure_config "$CFG"

    say "trial run on 127.0.0.1:$PORT (crons off)"
    runuser -u mitseller -- env MITSELLER_DISABLE_CRONS=1 "$BIN" serve -c "$CFG" > "$BACKUPS/bot${N}-${TS}.trial.log" 2>&1 &
    local TRIAL=$!
    if ! wait_healthy "http://127.0.0.1:$PORT/healthz" 20; then
        kill "$TRIAL" 2>/dev/null || true
        cat "$BACKUPS/bot${N}-${TS}.trial.log"
        die "the Go bot did not start; the PHP bot was not touched"
    fi
    kill "$TRIAL"; wait "$TRIAL" 2>/dev/null || true

    # ---- cut over: from here on, any failure puts PHP back
    cp -a "$SITE" "$ETC/bot${N}.nginx.bak"
    echo "$SITE" > "$ETC/bot${N}.nginx.path"
    echo "$PHP_DIR" > "$ETC/bot${N}.phpdir"
    crontab -l > "$ETC/bot${N}.crontab.bak" 2>/dev/null || true
    trap 'warn "migration failed — restoring the PHP bot"; restore_php "$N"; exit 1' ERR

    say "stopping the PHP bot's cron jobs (the Go bot runs them itself)"
    drop_cron_lines "$DOMAIN" < "$ETC/bot${N}.crontab.bak" | crontab - || true

    say "starting mitseller@$N"
    systemctl enable --now "mitseller@$N"
    wait_healthy "http://127.0.0.1:$PORT/healthz" 20

    say "switching nginx from php-fpm to the Go bot"
    proxy_snippet "$N"
    rewrite_nginx_site "$SITE" "$N"
    nginx -t
    systemctl reload nginx
    wait_healthy "https://$DOMAIN/healthz" 20

    trap - ERR
    say "securing the webhook with a secret token"
    "$BIN" set-webhook -c "$CFG" || warn "set-webhook failed; the bot still works (updates are accepted from Telegram's addresses)"

    register_with_panel "$N" || true

    # the hourly backup script of the PHP installer keeps working as it is
    echo
    echo "=========================================="
    echo "bot $N now runs on Go: https://$DOMAIN"
    echo "database backup: $BACKUPS/bot${N}-${TS}.sql.gz"
    if [ -f "$REPORT" ]; then echo "what changed:   $REPORT"; fi
    echo "logs:            journalctl -u mitseller@$N -f"
    echo "undo:            N=$N bash install.sh rollback"
    print_keys "$N"
    echo "=========================================="
}

restore_php() { # N
    local N="$1" SITE DOMAIN i cur
    DOMAIN=$(grep '^DOMAIN=' "$ETC/bot${N}.env" | cut -d= -f2)
    # nginx first, so requests go straight from Go to PHP with no gap
    if [ -f "$ETC/bot${N}.nginx.bak" ] && [ -f "$ETC/bot${N}.nginx.path" ]; then
        SITE=$(cat "$ETC/bot${N}.nginx.path")
        cp -a "$ETC/bot${N}.nginx.bak" "$SITE"
        nginx -t && systemctl reload nginx
        for i in $(seq 1 15); do
            curl -fsS -m 3 "https://$DOMAIN/healthz" 2>/dev/null | grep -q '^ok ' || break
            sleep 1
        done
    fi
    systemctl disable --now "mitseller@$N" 2>/dev/null || true
    if [ -f "$ETC/bot${N}.crontab.bak" ]; then
        # wipe this domain's cron lines, then put back only this domain's
        # lines from the backup. The jobs the Go bot did not take over were
        # never removed, so appending the whole backup would install them —
        # and every other bot's lines — a second time. Lines added since the
        # migration, and other bots' lines, stay exactly where they are.
        # the table is read into a variable first: crontab - truncates as soon
        # as the pipeline starts, and a crontab -l inside the same pipeline
        # would race it and see an empty table.
        cur=$(crontab -l 2>/dev/null || true)
        { [ -n "$cur" ] && printf '%s\n' "$cur" | drop_all_cron_lines "$DOMAIN" || true
          grep -e "://$DOMAIN/cron/" -e "://$DOMAIN/cronbot/" "$ETC/bot${N}.crontab.bak" || true; } | crontab -
    fi
}

cmd_rollback() {
    N="${N:?set N= (bot number)}"
    [ -f "$ETC/bot${N}.nginx.bak" ] || die "no migration backup for bot $N in $ETC"
    say "putting bot $N back on PHP"
    restore_php "$N"
    # Telegram keeps sending to the same URL; a webhook secret is simply ignored by PHP.
    say "done. The PHP bot serves https://$(grep '^DOMAIN=' "$ETC/bot${N}.env" | cut -d= -f2) again with the same database."
    echo "To try the migration again later: N=$N bash install.sh migrate"
}

# ------------------------------------------------------- pre-rebrand installs

# adopt_rollback: put the pre-rebrand bots passed as arguments back the way
# they were (config, nginx site, service). Used when an adopted bot does not
# answer after being switched over.
adopt_rollback() {
    local ts="$1" n f
    shift
    warn "putting the pre-rebrand bots back: $*"
    for n in "$@"; do
        systemctl disable --now "mitseller@$n" 2>/dev/null || true
        if [ -f "$BACKUPS/adopt-$ts/site-$n" ] && [ -f "$BACKUPS/adopt-$ts/site-$n.bak" ]; then
            cp -a "$BACKUPS/adopt-$ts/site-$n.bak" "$(cat "$BACKUPS/adopt-$ts/site-$n")" 2>/dev/null || true
        fi
        if [ -f "$ETC/bot$n.env" ]; then
            mv -f "$ETC/bot$n.env" "/etc/nexrabot/bot$n.env"
            if getent passwd nexrabot >/dev/null; then
                chown root:nexrabot "/etc/nexrabot/bot$n.env"
                chmod 0640 "/etc/nexrabot/bot$n.env"
            fi
        fi
        for f in nginx.bak nginx.path crontab.bak; do
            if [ -f "$ETC/bot$n.$f" ]; then
                mv -f "$ETC/bot$n.$f" "/etc/nexrabot/bot$n.$f" || true
            fi
        done
        systemctl enable --now "nexrabot@$n" 2>/dev/null || true
    done
    nginx -t >/dev/null 2>&1 && systemctl reload nginx 2>/dev/null || true
}

# legacy_adopt: convert the bots installed by the pre-rebrand installer
# (nexrabot@N, config in /etc/nexrabot, binary /usr/local/bin/nexrabot) to
# this one. A bot keeps its number, port, database, domain, nginx site, cron
# jobs and panel keys — only the names change. Everything that is rewritten is
# copied to $BACKUPS/adopt-<time> first, and a bot that does not answer after
# the switch is put back on the pre-rebrand install by itself.
#
# This never runs on its own: it only happens when you ask for it, because the
# converted bots then speak with this product's name and update from this repo.
# The pre-rebrand binary, unit and system user are left installed so the bots
# that were not converted keep running.
cmd_legacy_adopt() {
    local -a olds=()
    local -a failed=()
    local n f ts site
    while read -r n; do
        if [ -n "$n" ]; then olds+=("$n"); fi
    done < <(legacy_units)
    if [ ${#olds[@]} -eq 0 ]; then
        say "no pre-rebrand bot is enabled or running here; nothing to adopt"
        return 0
    fi
    [ -d /etc/nexrabot ] || die "nexrabot@${olds[*]} runs here but /etc/nexrabot does not exist"
    command -v nginx >/dev/null || die "nginx is not installed"
    nginx -t >/dev/null || die "the nginx configuration is broken; fix it before adopting"
    for n in "${olds[@]}"; do
        [ -f "/etc/nexrabot/bot$n.env" ] || die "/etc/nexrabot/bot$n.env not found for bot $n"
        if [ -e "$ETC/bot$n.env" ]; then
            die "bot $n already exists in $ETC; adopt it there first"
        fi
    done

    mkdir -p "$BACKUPS"; chmod 700 "$BACKUPS"
    ts=$(date +%Y%m%d_%H%M%S)
    mkdir -p "$BACKUPS/adopt-$ts"
    say "backing up /etc/nexrabot to $BACKUPS/adopt-$ts/etc"
    cp -a /etc/nexrabot "$BACKUPS/adopt-$ts/etc"

    install_binary
    install_unit

    say "stopping the pre-rebrand bots (they are back if anything goes wrong)"
    for n in "${olds[@]}"; do
        systemctl disable --now "nexrabot@$n" 2>/dev/null || true
    done
    for n in "${olds[@]}"; do
        if ss -ltn 2>/dev/null | grep -q ":$(port_for "$n") "; then
            for f in "${olds[@]}"; do systemctl enable --now "nexrabot@$f" 2>/dev/null || true; done
            die "port $(port_for "$n") is still in use after stopping nexrabot@$n; the pre-rebrand bots run again"
        fi
    done

    for n in "${olds[@]}"; do
        say "adopting bot $n (port $(port_for "$n"))"
        mv "/etc/nexrabot/bot$n.env" "$ETC/bot$n.env"
        # current key names, with the pre-rebrand ones still read
        sed -i -e 's/^NEXRA_SECRET=/MIT_SECRET=/' \
               -e 's#^DATA_DIR=/var/lib/nexrabot#DATA_DIR=/var/lib/mitseller#' "$ETC/bot$n.env"
        secure_config "$ETC/bot$n.env"
        for f in nginx.bak nginx.path crontab.bak; do
            if [ -f "/etc/nexrabot/bot$n.$f" ]; then
                mv "/etc/nexrabot/bot$n.$f" "$ETC/bot$n.$f"
            fi
        done

        site=""
        if [ -f "$ETC/bot$n.nginx.path" ]; then
            site=$(tr -d '\r\n' < "$ETC/bot$n.nginx.path")
            if [ -n "$site" ]; then
                site=$(readlink -f "$site")
            fi
        fi
        if [ -z "$site" ]; then
            for f in /etc/nginx/sites-enabled/* /etc/nginx/conf.d/*.conf; do
                [ -f "$f" ] || continue
                if grep -q "snippets/nexrabot$n\.conf" "$f"; then
                    site=$(readlink -f "$f")
                    break
                fi
            done
        fi
        if [ -n "$site" ] && [ -f "$site" ]; then
            cp -a "$site" "$BACKUPS/adopt-$ts/site-$n.bak"
            echo "$site" > "$BACKUPS/adopt-$ts/site-$n"
            sed -i "s|snippets/nexrabot$n\\.conf|snippets/mitseller$n.conf|" "$site"
            proxy_snippet "$n"
        else
            warn "bot $n: no nginx site includes snippets/nexrabot$n.conf; nginx was not changed for it"
        fi
    done

    for n in "${olds[@]}"; do
        say "starting mitseller@$n"
        systemctl enable --now "mitseller@$n"
        wait_healthy "http://127.0.0.1:$(port_for "$n")/healthz" 30 || failed+=("$n")
    done
    if [ ${#failed[@]} -gt 0 ]; then
        adopt_rollback "$ts" "${failed[@]}"
        die "bot ${failed[*]} did not answer after being adopted; it runs on the pre-rebrand install again"
    fi

    if ! nginx -t >/dev/null 2>&1; then
        adopt_rollback "$ts" "${olds[@]}"
        die "nginx rejected the new sites; the pre-rebrand install is back"
    fi
    systemctl reload nginx 2>/dev/null || warn "nginx did not reload; reload it by hand"

    rmdir /etc/nexrabot 2>/dev/null || true
    say "adopted nexrabot@${olds[*]} -> mitseller@${olds[*]}"
    say "each bot kept its port, database, domain, cron jobs and panel keys"
    say "everything that was rewritten is in $BACKUPS/adopt-$ts"
    warn "the pre-rebrand binary, unit and system user are still installed; remove them only when no nexrabot@* bot is left"
}

# ----------------------------------------------------------------- update

cmd_update() {
    local units
    legacy_warn
    units=$(systemctl list-units --type=service --all --plain --no-legend 'mitseller@*' | awk '{print $1}')
    install_binary
    install_unit
    for u in $units; do
        local n=${u#mitseller@}; n=${n%.service}
        say "restarting $u"
        systemctl restart "$u"
        wait_healthy "http://127.0.0.1:$(port_for "$n")/healthz" 30 || warn "$u is not healthy; see: journalctl -u $u"
    done
    say "all bots run $("$BIN" version)"
}

case "${1:-new}" in
    new) cmd_new ;;
    migrate) cmd_migrate ;;
    rollback) cmd_rollback ;;
    update) cmd_update ;;
    keys) N="${N:?set N=}"; print_keys "$N" ;;
    set-secret) cmd_set_secret ;;
    register) cmd_register ;;
    panel-creds) cmd_panel_creds ;;
    provisioner-setup) cmd_provisioner_setup ;;
    provisioner-print) cmd_provisioner_print ;;
    provisioner-teardown) cmd_provisioner_teardown ;;
    migrate-all) cmd_migrate_all ;;
    panel-update) cmd_panel_update ;;
    panel-link) cmd_panel_link ;;
    legacy-adopt) cmd_legacy_adopt ;;
    backup-setup) cmd_backup_setup ;;
    backup-now) cmd_backup_now ;;
    bot-export) cmd_bot_export ;;
    bot-import) cmd_bot_import ;;
    bot-cutover) cmd_bot_cutover ;;
    bot-unimport) cmd_bot_unimport ;;
    bot-uncutover) cmd_bot_uncutover ;;
    all) cmd_all ;;
    *) die "usage: install.sh [all|new|migrate|migrate-all|legacy-adopt|register|panel-creds|provisioner-setup|provisioner-print|provisioner-teardown|panel-update|panel-link|backup-setup|backup-now|set-secret|rollback|update|keys|bot-export|bot-import|bot-cutover|bot-unimport|bot-uncutover]" ;;
esac
