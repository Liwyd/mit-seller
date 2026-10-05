#!/usr/bin/env python3
"""Differential test: the PHP bot and the Go bot process the same updates
against identical databases; every Telegram call and every DB change is
compared after each step.

    python3 tests/diff/run.py [scenario-name ...]

Needs: a MariaDB/MySQL server reachable on /var/run/mysqld/mysqld.sock as
root without password (e.g. started with --skip-grant-tables), php-cli with
mysqli/pdo_mysql/gd, and the mitseller binary at $MITSELLER (default
/tmp/mitseller).
"""
import json
import os
import re
import shutil
import signal
import subprocess
import sys
import time
import urllib.request

import pymysql

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
WORK = os.environ.get("DIFF_WORK", "/tmp/mitseller-diff")
BIN = os.environ.get("MITSELLER", "/tmp/mitseller")
PHP_PORT, GO_PORT = 9101, 9102
MOCK_PHP, MOCK_GO = 9201, 9202
TOKEN_PHP, TOKEN_GO = "111:PHP", "222:GO"
ADMIN = "5000000001"
SECRET = "sekret"
procs = []


def sh(cmd, **kw):
    return subprocess.run(cmd, shell=True, check=True, **kw)


def http(url, data=None, method=None):
    req = urllib.request.Request(url, data=data, method=method)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(req, timeout=60) as r:
        return r.read()


def wait_port(port, path="/"):
    for _ in range(100):
        try:
            urllib.request.urlopen("http://127.0.0.1:%d%s" % (port, path), timeout=2)
            return
        except urllib.error.HTTPError:
            return
        except Exception:
            time.sleep(0.1)
    raise RuntimeError("port %d did not come up" % port)


def db(name):
    return pymysql.connect(unix_socket="/var/run/mysqld/mysqld.sock", user="root", database=name, charset="utf8mb4", autocommit=True)


def setup():
    for port in (PHP_PORT, GO_PORT, MOCK_PHP, MOCK_GO):
        subprocess.run("fuser -k %d/tcp >/dev/null 2>&1" % port, shell=True)
    shutil.rmtree(WORK, ignore_errors=True)
    os.makedirs(WORK)
    env = dict(os.environ, NO_PROXY="*", no_proxy="*")
    for k in ("HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy"):
        env.pop(k, None)
    for port in (MOCK_PHP, MOCK_GO):
        procs.append(subprocess.Popen([sys.executable, os.path.join(ROOT, "tests/diff/mock.py"), str(port)], env=env))
    c = pymysql.connect(unix_socket="/var/run/mysqld/mysqld.sock", user="root", charset="utf8mb4", autocommit=True)
    cur = c.cursor()
    for n in ("difphp", "difgo"):
        cur.execute("DROP DATABASE IF EXISTS " + n)
        cur.execute("CREATE DATABASE %s CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci" % n)
    c.close()

    # ---- PHP bot copy, pointed at the mock
    phpdir = os.path.join(WORK, "php")
    shutil.copytree(os.path.join(ROOT, "legacy-php"), phpdir)
    cfg = open(os.path.join(phpdir, "config.php")).read()
    for a, b in {"{DATABASE_NAME}": "difphp", "{DATABASE_USERNAME}": "root", "{DATABASE_PASSOWRD}": "",
                 "{BOT_TOKEN}": TOKEN_PHP, "{ADMIN_#ID}": ADMIN, "{DOMAIN.COM/PATH/BOT}": "bot.test",
                 "{BOT_USERNAME}": "testbot", "{MIT_SECRET}": SECRET}.items():
        cfg = cfg.replace(a, b)
    open(os.path.join(phpdir, "config.php"), "w").write(cfg)
    api = open(os.path.join(phpdir, "botapi.php")).read()
    api = api.replace('"https://api.telegram.org/bot"', '"http://127.0.0.1:%d/bot"' % MOCK_PHP)
    open(os.path.join(phpdir, "botapi.php"), "w").write(api)
    fn = open(os.path.join(phpdir, "functions.php")).read()
    fn = fn.replace("function checktelegramip()\n{", "function checktelegramip()\n{ return true;")
    open(os.path.join(phpdir, "functions.php"), "w").write(fn)
    shutil.rmtree(os.path.join(phpdir, "installer"), ignore_errors=True)
    procs.append(subprocess.Popen(["php", "-d", "disable_functions=shell_exec", "-d", "display_errors=0", "-d", "log_errors=1",
                                   "-d", "error_log=" + os.path.join(WORK, "php_errors.log"),
                                   "-S", "127.0.0.1:%d" % PHP_PORT, "-t", phpdir], env=env, cwd=phpdir,
                                  stdout=open(os.path.join(WORK, "php_server.log"), "w"), stderr=subprocess.STDOUT))
    wait_port(PHP_PORT, "/cron/index.php")
    http("http://127.0.0.1:%d/table.php" % PHP_PORT)
    seed("difphp", MOCK_PHP)

    # ---- same data for the Go bot, through the migration tool
    dump = os.path.join(WORK, "dump.sql")
    sh("mariadb-dump -uroot difphp > " + dump)
    gocfg = os.path.join(WORK, "go.env")
    open(gocfg, "w").write("\n".join([
        "BOT_TOKEN=" + TOKEN_GO, "ADMIN_ID=" + ADMIN, "DOMAIN=bot.test", "BOT_USERNAME=testbot",
        "MIT_SECRET=" + SECRET, "DB_NAME=difgo", "DB_USER=root", "DB_PASS=", "DB_SOCKET=/var/run/mysqld/mysqld.sock",
        "LISTEN=127.0.0.1:%d" % GO_PORT, "TELEGRAM_API=http://127.0.0.1:%d" % MOCK_GO, "SYNC_UPDATES=1",
        "CHECK_TELEGRAM_IP=0", "DISABLE_CRONS=1", "API_OWNER_KEY=owner", "API_MANAGER_KEY=manager", ""]))
    sh("%s import-sql -c %s --file %s > %s 2>&1" % (BIN, gocfg, dump, os.path.join(WORK, "import.log")))
    g = db("difgo").cursor()
    g.execute("UPDATE marzban_panel SET url_panel = REPLACE(url_panel, '%d', '%d'), marzban_url_direct = REPLACE(marzban_url_direct, '%d', '%d')" % (MOCK_PHP, MOCK_GO, MOCK_PHP, MOCK_GO))
    procs.append(subprocess.Popen([BIN, "serve", "-c", gocfg], env=env, stdout=open(os.path.join(WORK, "go.log"), "w"), stderr=subprocess.STDOUT))
    wait_port(GO_PORT, "/healthz")


def seed(name, mock):
    c = db(name).cursor()
    base = "http://127.0.0.1:%d" % mock
    c.execute("UPDATE setting SET Channel_Report = '-1001', Extra_volume = '5000', time_usertest = '2', val_usertest = '200', limit_usertest_all = '1'")
    c.execute("UPDATE user SET limit_usertest = 1")
    c.execute("INSERT INTO marzban_panel (name_panel,url_panel,username_panel,password_panel,type,inboundid,sublink,configManual,MethodUsername,statusTest,status,onholdstatus,proxies,inbounds) VALUES "
              "('Germany', %s, 'admin', 'pass', 'marzban', '0', 'onsublink', 'offconfig', 'آیدی عددی + حروف و عدد رندوم', 'ontestshowpanel', 'activepanel', 'offonhold', '{\"vless\":{}}', '{\"vless\":[\"VLESS TCP\"]}')", (base,))
    c.execute("INSERT INTO marzban_panel (name_panel,url_panel,username_panel,password_panel,type,inboundid,sublink,configManual,MethodUsername,statusTest,status,onholdstatus,marzban_url_direct,marzban_username_direct,marzban_password_direct) VALUES "
              "('MitDE', %s, 'reseller', 'pass', 'mit', '0', 'onsublink', 'offconfig', 'دلخواه یا رندوم (انتخاب با کاربر)', 'ontestshowpanel', 'activepanel', 'offonhold', %s, 'sudo', 'pass')", (base + "/dashboard", base))
    c.execute("INSERT INTO category (remark) VALUES ('ماهانه'), ('سه ماهه')")
    c.execute("INSERT INTO product (code_product,name_product,price_product,Volume_constraint,Location,Service_time,Category) VALUES "
              "('aa01','۳۰ گیگ یک ماهه','50000','30','Germany','30','1'),"
              "('aa02','۶۰ گیگ سه ماهه','120000','60','/all','90','2'),"
              "('aa03','MIT ۵۰ گیگ','80000','50','MitDE','30','1')")
    c.execute("INSERT INTO DiscountSell (codeDiscount, price, limitDiscount, usedDiscount, usefirst) VALUES ('OFF20','20','10','0','0')")
    c.execute("INSERT INTO Discount (code, price) VALUES ('GIFT', '15000')")


def stop():
    for p in procs:
        try:
            p.send_signal(signal.SIGTERM)
        except Exception:
            pass
    time.sleep(0.3)
    for p in procs:
        try:
            p.kill()
        except Exception:
            pass


# ---------------------------------------------------------------- updates
UPD = [100]
MID = [500]


def msg(uid, text=None, photo=False, contact=None, username=None, first="کاربر", caption=None, entities=None):
    UPD[0] += 1
    MID[0] += 1
    m = {"message_id": MID[0], "from": {"id": int(uid), "is_bot": False, "first_name": first},
         "chat": {"id": int(uid), "type": "private"}, "date": int(time.time())}
    if username:
        m["from"]["username"] = username
    if text is not None:
        m["text"] = text
    if entities:
        m["entities"] = entities
    if photo:
        m["photo"] = [{"file_id": "small-id", "width": 90, "height": 90}, {"file_id": "PHOTO-%d" % MID[0], "width": 800, "height": 800}]
        if caption:
            m["caption"] = caption
    if contact:
        m["contact"] = {"phone_number": contact[0], "user_id": int(contact[1])}
    return {"update_id": UPD[0], "message": m}


def cb(uid, data, text="پیام قبلی", username=None, caption=None):
    UPD[0] += 1
    frm = {"id": int(uid), "is_bot": False, "first_name": "کاربر"}
    if username:
        frm["username"] = username
    m = {"message_id": 777, "chat": {"id": int(uid), "type": "private"}, "date": int(time.time()), "text": text}
    if caption:
        m["caption"] = caption
    return {"update_id": UPD[0], "callback_query": {"id": "cbq-%d" % UPD[0], "from": frm, "message": m, "data": data}}


# ---------------------------------------------------------------- normalisation
HEXRUN = re.compile(r"[0-9a-f]{4,}")
FA_TIME = re.compile(r"[۰-۹]{2}:[۰-۹]{2}:[۰-۹]{2}")
TIME = re.compile(r"\d{4}[/-]\d{2}[/-]\d{2}[ T]\d{2}:\d{2}:\d{2}")
RAND_USER = re.compile(r"(\d{5,}|mit)_[0-9a-f]{4}(?![0-9a-f])")
MOCKPORT = re.compile(r":92\d\d\b")
VERSION = re.compile(r"Version: [0-9][^\n]*")


class Tokens:
    """Maps one bot's random ids to placeholders in order of first use."""

    def __init__(self, dbname):
        self.db = dbname
        self.map = {}
        self.known = set()

    def refresh(self):
        c = db(self.db).cursor()
        vals = set()
        for q in ("SELECT ref_code FROM user", "SELECT id_invoice FROM invoice", "SELECT username FROM invoice",
                  "SELECT id_order FROM Payment_report", "SELECT code_product FROM product", "SELECT CAST(amount AS CHAR) FROM autopay_order",
                  "SELECT device_key FROM autopay"):
            try:
                c.execute(q)
                vals.update(str(r[0]) for r in c.fetchall() if r[0])
            except Exception:
                pass
        for s in ("aa01", "aa02", "aa03"):
            vals.discard(s)
        try:
            c.execute("SELECT CAST(amount AS CHAR) FROM autopay_order")
            for (a,) in c.fetchall():
                vals.add("{:,}".format(int(a)))
                vals.add("{:,}".format(int(a) * 10))
        except Exception:
            pass
        self.known = vals

    def norm(self, s):
        if not isinstance(s, str) or not s:
            return s
        for tok in sorted(self.known, key=len, reverse=True):
            if tok in s:
                if tok not in self.map:
                    self.map[tok] = "<R%d>" % (len(self.map) + 1)
                s = s.replace(tok, self.map[tok])
        s = FA_TIME.sub("<T>", s)
        s = TIME.sub("<TS>", s)
        s = RAND_USER.sub(r"\1_<rnd>", s)
        s = MOCKPORT.sub(":MOCK", s)
        return s


IGNORE_PARAMS = {"disable_web_page_preview", "cache_time", "show_alert", "max_connections", "allowed_updates", "callback_query_id"}


def canon_markup(v, tk):
    if v in (None, "", "null"):
        return None
    if isinstance(v, str):
        try:
            v = json.loads(v)
        except Exception:
            return tk.norm(v)
    if isinstance(v, dict):
        v = dict(v)
        if "inline_keyboard" in v:
            v.pop("resize_keyboard", None)
        rows = v.get("inline_keyboard") or v.get("keyboard")
        if rows is not None:
            out = []
            for r in rows:
                nr = []
                for b in r:
                    if isinstance(b, str):
                        b = {"text": b}
                    b = {k: (tk.norm(unesc(x)) if isinstance(x, str) else x) for k, x in b.items() if x not in (None, False, "")}
                    b.pop("style", None)
                    b.pop("icon_custom_emoji_id", None)
                    if b.get("text") in DROP_BUTTONS:
                        continue
                    nr.append(b)
                if nr or not r:
                    out.append(nr)
            key = "inline_keyboard" if "inline_keyboard" in v else "keyboard"
            v[key] = out
    return v


DROP_BUTTONS = {"🆔 شناسه ایموجی پریمیوم"}


def unesc(s):
    # PHP wrote "\u270d\ufe0f" in a double-quoted string, which PHP does not
    # decode; the Go bot sends the actual emoji
    return s.replace("\\u270d\\ufe0f ", "✍️ ").replace("\\ud83c\\udfb2 ", "🎲 ")


def canon_call(call, tk):
    m = call["method"].lower()
    p = {}
    for k, v in call["params"].items():
        if k in IGNORE_PARAMS:
            continue
        if k == "reply_markup":
            v = canon_markup(v, tk)
            if v is None:
                continue
        elif k == "parse_mode":
            v = str(v).lower()
        elif k in ("photo", "document") and str(v).startswith("<file"):
            v = "<upload>"
        elif isinstance(v, str):
            v = tk.norm(VERSION.sub("Version: X", unesc(v)))
        elif isinstance(v, (int, float)) and not isinstance(v, bool):
            v = str(v)
        p[k] = v
    if m == "sendphoto" and p.get("photo") == "<upload>":
        p.pop("parse_mode", None)
    return m, p


MASK_COLS = {
    "user": {"ref_code", "last_message_time"},
    "invoice": {"time_sell"},
    "Payment_report": {"time"},
    "marzban_panel": {"datelogin"},
    "autopay": {"last_seen", "created_at", "device_key"},
    "autopay_order": {"created_at", "closed_at", "id"},
    "autopay_sms": {"received_at", "id", "hash"},
}
SKIP_TABLES = {"mit_kv", "mit_broadcast"}


def snapshot(dbname, tk):
    c = db(dbname).cursor()
    c.execute("SHOW TABLES")
    out = {}
    for (t,) in c.fetchall():
        if t in SKIP_TABLES:
            continue
        c.execute("SELECT * FROM `%s`" % t)
        cols = [d[0] for d in c.description]
        rows = []
        for r in c.fetchall():
            row = {}
            for col, val in zip(cols, r):
                if col in MASK_COLS.get(t, ()):
                    val = "<masked>" if val not in (None, "") else val
                elif t == "marzban_panel" and col in ("url_panel", "marzban_url_direct") and val:
                    val = re.sub(r":92\d\d", ":MOCK", str(val))
                elif val is not None:
                    val = tk.norm(str(val))
                row[col] = val
            rows.append(json.dumps(row, ensure_ascii=False, sort_keys=True))
        out[t] = sorted(rows)
    return out


def diff_snap(a, b):
    lines = []
    for t in sorted(set(a) | set(b)):
        ra, rb = a.get(t, []), b.get(t, [])
        if ra != rb:
            onlya = [x for x in ra if x not in rb]
            onlyb = [x for x in rb if x not in ra]
            lines.append("  table %s:" % t)
            for x in onlya[:6]:
                lines.append("    PHP: " + x)
            for x in onlyb[:6]:
                lines.append("    GO : " + x)
    return lines


# ---------------------------------------------------------------- runner
class Runner:
    def __init__(self):
        self.tp, self.tg = Tokens("difphp"), Tokens("difgo")
        self.failures = 0
        self.steps = 0

    def logs(self, mock, token):
        data = json.loads(http("http://127.0.0.1:%d/__log/%s" % (mock, token)))
        http("http://127.0.0.1:%d/__clear/%s" % (mock, token), data=b"{}", method="POST")
        return data

    def q(self, side, sql, *args):
        c = db("difphp" if side == "php" else "difgo").cursor()
        c.execute(sql, args)
        r = c.fetchone()
        return None if r is None else r[0]

    def post(self, side, path, body, headers=None):
        port = PHP_PORT if side == "php" else GO_PORT
        req = urllib.request.Request("http://127.0.0.1:%d%s" % (port, path), data=body)
        for k, v in (headers or {}).items():
            req.add_header(k, v)
        try:
            with urllib.request.urlopen(req, timeout=60) as resp:
                return resp.status, resp.read()
        except urllib.error.HTTPError as e:
            return e.code, e.read()

    def step(self, name, upd, expect_diff=None, path="/index.php", headers=None, compare_body=False, reconcile=()):
        """upd is an update dict, or a function(side) -> update dict (or raw bytes).
        reconcile: SQL run on both databases after an intentional difference so
        later steps start from the same state again."""
        self.steps += 1
        if self.steps % 12 == 0:
            # keep the scripted users under the 35-messages-a-minute spam limit
            self.sql_both("UPDATE user SET message_count = '0'")
        bodies = {}
        for side in ("php", "go"):
            u = upd(side) if callable(upd) else upd
            body = u if isinstance(u, (bytes, bytearray)) else json.dumps(u, ensure_ascii=False).encode()
            h = headers(side, body) if callable(headers) else (headers or {})
            bodies[side] = self.post(side, path, body, h)
        lp = self.logs(MOCK_PHP, TOKEN_PHP)
        lg = self.logs(MOCK_GO, TOKEN_GO)
        self.tp.refresh()
        self.tg.refresh()
        # PHP sent the referral commission note to chat 0 (no referrer); Telegram rejects it
        lp = [x for x in lp if str(x["params"].get("chat_id")) != "0"]
        # the PHP copy runs without shell_exec, which makes it print crontab hints
        lp = [x for x in lp if "shell_exec" not in str(x["params"].get("text", ""))]
        # PHP's DirectPayment answered a callback even outside one (no id): Telegram rejects it
        lp = [x for x in lp if not (x["method"].lower() == "answercallbackquery" and not x["params"].get("callback_query_id"))]
        cp = [canon_call(x, self.tp) for x in lp]
        cg = [canon_call(x, self.tg) for x in lg]
        problems = []
        if cp != cg:
            problems.append("  telegram calls differ:")
            n = max(len(cp), len(cg))
            for i in range(n):
                a = cp[i] if i < len(cp) else None
                b = cg[i] if i < len(cg) else None
                if a != b:
                    problems.append("    #%d PHP: %s" % (i, json.dumps(a, ensure_ascii=False)[:6000]))
                    problems.append("    #%d GO : %s" % (i, json.dumps(b, ensure_ascii=False)[:6000]))
        sp, sg = snapshot("difphp", self.tp), snapshot("difgo", self.tg)
        problems += diff_snap(sp, sg)
        if compare_body:
            bp, bg = bodies["php"], bodies["go"]
            jp = json.loads(bp[1] or b"null")
            jg = json.loads(bg[1] or b"null")
            np_, ng = self.tp.norm(json.dumps(jp, ensure_ascii=False, sort_keys=True)), self.tg.norm(json.dumps(jg, ensure_ascii=False, sort_keys=True))
            if bp[0] != bg[0] or np_ != ng:
                problems.append("  response differs:\n    PHP: %s %s\n    GO : %s %s" % (bp[0], np_, bg[0], ng))
        if problems:
            tag = "EXPECTED" if expect_diff else "DIFF"
            print("[%s] step %d %s" % (tag, self.steps, name))
            if expect_diff:
                print("    reason: " + expect_diff)
            print("\n".join(problems))
            if not expect_diff:
                self.failures += 1
        else:
            print("[ok]   step %d %s  (%d calls)" % (self.steps, name, len(cp)))
        for q in reconcile:
            self.sql_both(q)
        return lp, lg

    def sql_both(self, q):
        for n in ("difphp", "difgo"):
            db(n).cursor().execute(q)


def main():
    import scenarios
    names = sys.argv[1:] or scenarios.ORDER
    setup()
    r = Runner()
    try:
        for n in names:
            print("=== scenario:", n)
            getattr(scenarios, n)(r)
    finally:
        stop()
    print("\n%d steps, %d with unexpected differences" % (r.steps, r.failures))
    errs = open(os.path.join(WORK, "go.log")).read()
    if "panic" in errs:
        print("GO PANIC in log:\n" + errs[-3000:])
        r.failures += 1
    sys.exit(1 if r.failures else 0)


if __name__ == "__main__":
    sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
    main()
