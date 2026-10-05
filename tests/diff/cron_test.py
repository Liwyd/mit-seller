#!/usr/bin/env python3
"""Automatic confirmation without review: a receipt is accepted about a
minute after it arrives, the admins get it back with a warning, and it stands
aside while the SMS check (autopay) is on. Run after run.py."""
import json, os, subprocess, sys, time, urllib.error, urllib.request
from datetime import datetime, timedelta
from zoneinfo import ZoneInfo
import pymysql

HERE = os.path.dirname(os.path.abspath(__file__))
WORK = os.environ.get("DIFF_WORK", "/tmp/nexrabot-diff")
BIN = os.environ.get("NEXRABOT", "/tmp/nexrabot")
BOT, MOCK, TOKEN = "http://127.0.0.1:9102", "http://127.0.0.1:9202", "222:GO"
FAILS = []


def http(method, url, body=None, key="manager"):
    data = None if body is None else json.dumps(body).encode()
    h = {"Authorization": "Bearer " + key}
    if data:
        h["Content-Type"] = "application/json"
    try:
        with urllib.request.urlopen(urllib.request.Request(url, data=data, method=method, headers=h), timeout=20) as r:
            return r.status, json.loads(r.read() or b"null")
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read() or b"null")


def check(name, cond, extra=""):
    print(("[ok]   " if cond else "[FAIL] ") + name + ("" if cond else "  " + str(extra)[:500]))
    if not cond:
        FAILS.append(name)


def tehran(sec_ago):
    return (datetime.now(ZoneInfo("Asia/Tehran")) - timedelta(seconds=sec_ago)).strftime("%Y/%m/%d %H:%M:%S")


def main():
    db = pymysql.connect(unix_socket="/var/run/mysqld/mysqld.sock", user="root", database="difgo", autocommit=True)
    cur = db.cursor()
    q = lambda sql, *a: (cur.execute(sql, a), cur.fetchall())[1]
    env = dict(os.environ, NEXRABOT_DISABLE_CRONS="0")
    procs = [subprocess.Popen([sys.executable, os.path.join(HERE, "mock.py"), "9202"]),
             subprocess.Popen([BIN, "serve", "-c", os.path.join(WORK, "go.env")], env=env,
                              stdout=open(os.path.join(WORK, "cron.log"), "w"), stderr=subprocess.STDOUT)]
    try:
        for _ in range(100):
            try:
                urllib.request.urlopen(BOT + "/healthz", timeout=1); break
            except Exception:
                time.sleep(0.1)
        code, b = http("PUT", BOT + "/api/v1/autopay", {"mode": "no_review"})
        check("mode no_review", code == 200 and b["data"]["mode"] == "no_review", b)
        code, b = http("PUT", BOT + "/api/v1/autopay", {"mode": "nope"})
        check("bad mode refused", code == 400, b)
        bal0 = float(q("SELECT Balance FROM user WHERE id = '7000000003'")[0][0])
        stamp = int(time.time())
        old, new = "ac%da" % stamp, "ac%db" % stamp
        for oid, ago in ((old, 70), (new, 5)):
            cur.execute("INSERT INTO Payment_report (id_user, id_order, time, price, payment_Status, Payment_Method, invoice) VALUES ('7000000003', %s, %s, '25000', 'waiting', 'cart to cart', '0|0')", (oid, tehran(ago)))
        cur.execute("REPLACE INTO nexra_kv (k, v) VALUES (%s, 'receipt-photo-1')", ("receipt_" + old,))
        http("POST", MOCK + "/__clear/" + TOKEN)
        time.sleep(25)
        st = dict(q("SELECT id_order, payment_Status FROM Payment_report WHERE id_order IN (%s, %s)", old, new))
        check("a receipt older than a minute is accepted", st[old] == "paid", st)
        check("a fresh receipt waits", st[new] == "waiting", st)
        bal1 = float(q("SELECT Balance FROM user WHERE id = '7000000003'")[0][0])
        check("credited once", bal1 == bal0 + 25000, (bal0, bal1))
        log = json.loads(urllib.request.urlopen(MOCK + "/__log/" + TOKEN).read())
        photos = [c["params"] for c in log if c["method"] == "sendphoto"]
        check("admins get the receipt back with the warning", photos and "خودکار تأیید شد" in photos[0].get("caption", "") and photos[0].get("photo") == "receipt-photo-1", photos or log)
        check("the customer is told", any(c["method"] == "sendmessage" and str(c["params"].get("chat_id")) == "7000000003" for c in log), log)

        code, b = http("PUT", BOT + "/api/v1/autopay", {"mode": "sms"})
        check("mode sms turns the unchecked one off", code == 200 and b["data"]["mode"] == "sms", b)
        time.sleep(50)
        st = q("SELECT payment_Status FROM Payment_report WHERE id_order = %s", new)[0][0]
        check("with the SMS check on, nothing is accepted unchecked", st == "waiting", st)
        code, b = http("PUT", BOT + "/api/v1/autopay", {"mode": "off"})
        check("mode off", b["data"]["mode"] == "off" and not b["data"]["enabled"], b)
        cur.execute("UPDATE Payment_report SET payment_Status = 'reject' WHERE id_order = %s", (new,))
    finally:
        for p in procs:
            p.terminate()
    print("\n%d failures" % len(FAILS))
    sys.exit(1 if FAILS else 0)


main()
