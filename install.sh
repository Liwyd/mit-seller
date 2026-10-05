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
#   Move EVERY PHP bot on this server (/var/www/html/botmirzapanel*) to Go,
#   one by one, and connect each to Mit Panel under its owner:
#     curl -sLo /root/mitseller-install.sh <raw url of this file>
#     MIT_PANEL_URL=https://panel.example.com/dashboard MIT_PANEL_USER=admin \
#     MIT_PANEL_PASS='...' bash /root/mitseller-install.sh migrate-all
#   A bot that fails is put back on PHP by itself and the others carry on.
#   Without the MIT_PANEL_* variables the Mit Panel on this server is
#   used; with none, the bots are migrated but not connected (on the panel's
#   server, "bash install.sh panel-link" prints the command for this one).
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
export MIT_PANEL_URL MIT_PANEL_USER MIT_PANEL_PASS MIT_PANEL_INSECURE \
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
    echo
    echo "Mit Panel → Bot → افزودن ربات: address https://$(grep '^DOMAIN=' "$ETC/bot$1.env" | cut -d= -f2), and the two keys above."
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
panel_env() {
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

php_bots() { # numbers of the PHP bots on this server
    local dir n
    for dir in /var/www/html/botmirzapanel*/; do
        n=${dir%/}; n=${n##*botmirzapanel}
        case "$n" in ''|*[!0-9]*) continue ;; esac
        [ -f "$dir/config.php" ] && echo "$n"
    done
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
    if [ -z "$(php_bots)" ] && ! ls "$ETC"/bot*.env >/dev/null 2>&1; then
        say "no MirzaBot on this server"
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
    local self ok=() failed=() skipped=() dir n
    legacy_warn
    self=$(readlink -f "$0")
    [ -f "$self" ] || die "save this script to a file first (curl -sLo /root/mitseller-install.sh …) and run that file"
    install_binary
    install_unit
    panel_env
    export MITSELLER_BIN="$BIN" # every bot below uses the binary just installed
    for dir in /var/www/html/botmirzapanel*/; do
        n=${dir%/}; n=${n##*botmirzapanel}
        case "$n" in ''|*[!0-9]*) continue ;; esac
        [ -f "$dir/config.php" ] || continue
        if systemctl is-active --quiet "mitseller@$n"; then
            say "bot $n already runs on Go"
            skipped+=("$n")
            continue
        fi
        echo
        say "======== bot $n ========"
        set +e
        MITSELLER_DEFER_REGISTER=1 N="$n" PHP_DIR="${dir%/}" bash "$self" migrate 2>&1 | tee "$BACKUPS/migrate-bot$n.log"
        local rc=${PIPESTATUS[0]}
        set -e
        if [ "$rc" = 0 ]; then ok+=("$n"); else failed+=("$n"); fi
    done
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
    certbot --nginx -d "$DOMAIN" --agree-tos --redirect --no-eff-email -m "$CERT_EMAIL"
    wait_healthy "https://$DOMAIN/healthz" 20 || die "https://$DOMAIN/healthz does not answer"

    say "webhook"
    "$BIN" set-webhook -c "$CFG"

    say "hourly database backup to your Telegram"
    cat > "/root/mitseller${N}_backup.sh" <<EOF
#!/bin/bash
FILE="/root/mitseller${N}_\$(date +%Y%m%d_%H%M%S).sql.gz"
$BIN db-dump -c $CFG --out "\$FILE" >/dev/null
curl -s -F chat_id="$ADMIN" -F document=@"\$FILE" -F caption="بکاپ خودکار $BOT_USER 🗄" \\
  "${MITSELLER_TELEGRAM_API:-https://api.telegram.org}/bot$TOKEN/sendDocument" >/dev/null
rm -f "\$FILE"
EOF
    chmod 700 "/root/mitseller${N}_backup.sh"
    (crontab -l 2>/dev/null | grep -v "mitseller${N}_backup"; echo "$BACKUP_MINUTE * * * * bash /root/mitseller${N}_backup.sh") | crontab -

    echo
    echo "=========================================="
    echo "done: @$BOT_USER on https://$DOMAIN"
    echo "database: $DBNAME / $DBUSER / $DBPASS"
    print_keys "$N"
    echo "=========================================="
    # into Mit Panel too, when it runs here or MIT_PANEL_* are given
    register_with_panel "$N" || true
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

cmd_migrate() {
    N="${N:?set N= (bot number)}"
    local PHP_DIR="${PHP_DIR:-/var/www/html/botmirzapanel${N}}"
    PHP_DIR="${PHP_DIR%/}"
    local CFG="$ETC/bot${N}.env" PORT TS SITE DOMAIN
    PORT=$(port_for "$N")
    TS=$(date +%Y%m%d_%H%M%S)

    [ -f "$PHP_DIR/config.php" ] || die "$PHP_DIR/config.php not found (set PHP_DIR=)"
    command -v nginx >/dev/null || die "nginx is not installed"
    SITE=$(nginx_site_for "$PHP_DIR") || die "no nginx site serves $PHP_DIR with php-fpm; migrate this one by hand (see docs/MIGRATION.md)"
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
    case "$DOMAIN" in */*) die "the PHP bot runs under a path ($DOMAIN); migrate it by hand (see docs/MIGRATION.md)";; esac

    mkdir -p "$BACKUPS"
    say "backing up the database (before anything changes)"
    "$BIN" db-dump -c "$CFG" --out "$BACKUPS/bot${N}-${TS}.sql.gz"

    say "checking the database and the token"
    "$BIN" check -c "$CFG"

    say "upgrading the schema (adds only what is missing; the PHP bot keeps working)"
    "$BIN" migrate-php --php-dir "$PHP_DIR" --out "$CFG" --listen "127.0.0.1:$PORT" --clean-crontab=false
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
    crontab -l > "$ETC/bot${N}.crontab.bak" 2>/dev/null || true
    trap 'warn "migration failed — restoring the PHP bot"; restore_php "$N"; exit 1' ERR

    say "stopping the PHP bot's cron jobs (the Go bot runs them itself)"
    grep -v -F "$DOMAIN/cron/" "$ETC/bot${N}.crontab.bak" | crontab - || true

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
    echo "logs:            journalctl -u mitseller@$N -f"
    echo "undo:            N=$N bash install.sh rollback"
    print_keys "$N"
    echo "=========================================="
}

restore_php() { # N
    local N="$1" SITE DOMAIN i
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
        # put back the PHP bot's cron lines, keep everything added since
        { crontab -l 2>/dev/null | grep -v -F "$DOMAIN/cron/" || true; grep -F "$DOMAIN/cron/" "$ETC/bot${N}.crontab.bak" || true; } | crontab -
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
    register) cmd_register ;;
    migrate-all) cmd_migrate_all ;;
    panel-update) cmd_panel_update ;;
    panel-link) cmd_panel_link ;;
    legacy-adopt) cmd_legacy_adopt ;;
    all) cmd_all ;;
    *) die "usage: install.sh [all|new|migrate|migrate-all|legacy-adopt|register|panel-update|panel-link|rollback|update|keys]" ;;
esac
