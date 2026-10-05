#!/usr/bin/env python3
"""Smoke test of the management API (/api/v1) that Mit Panel uses.

Run after run.py (it reuses the Go database the diff test leaves behind):
    python3 api_smoke.py
Checks every endpoint with the owner and the manager key, that managers can
never touch or even see panel credentials, and that writes round-trip.
"""
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
WORK = os.environ.get("DIFF_WORK", "/tmp/mitseller-diff")
BIN = os.environ.get("MITSELLER", "/tmp/mitseller")
BASE = "http://127.0.0.1:9102/api/v1"
FAILS = []


def call(method, path, key="owner", body=None, raw=False):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(BASE + path, data=data, method=method)
    if key:
        req.add_header("Authorization", "Bearer " + key)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=20) as r:
            payload = r.read()
            code = r.status
    except urllib.error.HTTPError as e:
        payload = e.read()
        code = e.code
    if raw:
        return code, payload
    try:
        return code, json.loads(payload)
    except ValueError:
        return code, {"raw": payload[:200].decode(errors="replace")}


def check(name, cond, extra=""):
    print(("[ok]   " if cond else "[FAIL] ") + name + ("" if cond else "  " + str(extra)[:400]))
    if not cond:
        FAILS.append(name)


def find(lst, key, val):
    for x in lst or []:
        if x.get(key) == val:
            return x
    return None


def expect(name, method, path, code=200, key="owner", body=None):
    c, j = call(method, path, key, body)
    check("%s (%s %s as %s -> %d)" % (name, method, path, key, code), c == code, (c, j))
    return j.get("data") if isinstance(j, dict) else None


def main():
    procs = [subprocess.Popen([sys.executable, os.path.join(HERE, "mock.py"), "9202"])]
    procs.append(subprocess.Popen([BIN, "serve", "-c", os.path.join(WORK, "go.env")],
                                  stdout=open(os.path.join(WORK, "api.log"), "w"), stderr=subprocess.STDOUT))
    try:
        for _ in range(100):
            try:
                urllib.request.urlopen("http://127.0.0.1:9102/healthz", timeout=1)
                break
            except Exception:
                time.sleep(0.1)
        run()
    finally:
        for p in procs:
            p.terminate()
    print("\n%d failures" % len(FAILS))
    sys.exit(1 if FAILS else 0)


def run():
    # auth
    expect("no key", "GET", "/info", 401, key=None)
    expect("bad key", "GET", "/info", 401, key="nope")
    info = expect("info owner", "GET", "/info")
    check("owner role", info and info["role"] == "owner", info)
    info = expect("info manager", "GET", "/info", key="manager")
    check("manager role", info and info["role"] == "manager", info)
    st = expect("stats", "GET", "/stats", key="manager")
    check("stats has users", st and st["users"] > 0, st)

    # panels: owner-only writes, managers see no secrets
    panels = expect("panels as owner", "GET", "/panels")
    mp = expect("panels as manager", "GET", "/panels", key="manager")
    secret_words = ("password", "username_panel", "url_panel", "marzban_", "token", "datelogin")
    leaked = [k for p in (mp or []) for k in p if any(w in k for w in secret_words)]
    check("manager panel list has no credentials", mp and not leaked, leaked)
    check("manager panel list is not blank", mp and all(p.get("name_panel") for p in mp), mp)
    pid = panels[0]["id"]
    for m, path, body in [("POST", "/panels", {"name": "x", "type": "marzban", "url": "http://a", "username": "u", "password": "p"}),
                          ("PUT", "/panels/%s" % pid, {"name": "y"}),
                          ("DELETE", "/panels/%s" % pid, None),
                          ("POST", "/panels/%s/test" % pid, None)]:
        expect("manager cannot change panels", m, path, 403, key="manager", body=body)
    created = expect("owner adds panel", "POST", "/panels", 200,
                     body={"name": "APITest", "type": "marzban", "url": "http://127.0.0.1:9202", "username": "admin", "password": "pw"})
    if created:
        expect("owner tests panel", "POST", "/panels/%s/test" % created["id"])
        expect("owner renames panel", "PUT", "/panels/%s" % created["id"], body={"name": "APITest2"})
        expect("owner deletes panel", "DELETE", "/panels/%s" % created["id"])

    # catalogue
    loc = mp[0]["name_panel"]
    cat = expect("category create", "POST", "/categories", key="manager", body={"name": "دسته API"})
    p = expect("product create", "POST", "/products", key="manager",
               body={"name": "محصول API", "location": loc, "volume": 20, "days": 30, "price": 70000, "category_id": cat and cat["id"]})
    expect("product bad volume", "POST", "/products", 400, key="manager", body={"name": "x", "location": loc, "volume": "a", "days": 1, "price": 1})
    expect("product bad location", "POST", "/products", 400, key="manager", body={"name": "x", "location": "nowhere", "volume": 1, "days": 1, "price": 1})
    if p:
        u = expect("product update", "PUT", "/products/%s" % p["id"], key="manager", body={"price": 75000, "name": "محصول API ۲"})
        check("product updated", u and u.get("price_product") == "75000" and u.get("name_product") == "محصول API ۲", u)
        lst = expect("product list", "GET", "/products", key="manager")
        check("product in list", any(x["id"] == p["id"] for x in lst or []))
        expect("product delete", "DELETE", "/products/%s" % p["id"], key="manager")
    if cat:
        expect("category rename", "PUT", "/categories/%s" % cat["id"], key="manager", body={"name": "دسته ۲"})
        expect("category delete", "DELETE", "/categories/%s" % cat["id"], key="manager")
    for x in call("GET", "/giftcodes", "manager")[1]["data"]:
        if x["code"] == "APIGIFT":
            call("DELETE", "/giftcodes/%s" % x["id"], "manager")
    g = find(expect("gift code create", "POST", "/giftcodes", key="manager", body={"code": "APIGIFT", "price": 5000}), "code", "APIGIFT")
    expect("gift code dup", "POST", "/giftcodes", 409, key="manager", body={"code": "APIGIFT", "price": 5000})
    expect("gift codes", "GET", "/giftcodes", key="manager")
    if g:
        expect("gift code delete", "DELETE", "/giftcodes/%s" % g["id"], key="manager")
    dsc = find(expect("discount create", "POST", "/discounts", key="manager", body={"code": "APIOFF", "percent": 15, "limit": 5}), "codeDiscount", "APIOFF")
    expect("discount bad percent", "POST", "/discounts", 400, key="manager", body={"code": "APIOFF2", "percent": 150, "limit": 5})
    expect("discounts", "GET", "/discounts", key="manager")
    if dsc:
        expect("discount delete", "DELETE", "/discounts/%s" % dsc["id"], key="manager")
    h = find(expect("help create", "POST", "/help", key="manager", body={"name": "آموزش API", "description": "متن"}), "name_os", "آموزش API")
    expect("help list", "GET", "/help", key="manager")
    if h:
        expect("help update", "PUT", "/help/%s" % h["id"], key="manager", body={"description": "متن ۲"})
        expect("help delete", "DELETE", "/help/%s" % h["id"], key="manager")

    # settings, texts, buttons
    s = expect("settings", "GET", "/settings", key="manager")
    s2 = expect("settings update", "PUT", "/settings", key="manager", body={"help_Status": not s["help_Status"], "time_usertest": "2", "crons": {"volume": False}})
    check("settings round-trip", s2 and s2["help_Status"] != s["help_Status"] and s2["time_usertest"] == "2" and s2["crons"]["volume"] is False, s2)
    expect("settings restore", "PUT", "/settings", key="manager", body={"help_Status": s["help_Status"], "crons": {"volume": s["crons"]["volume"]}})
    expect("settings bad value", "PUT", "/settings", 400, key="manager", body={"val_usertest": "10"})
    texts = expect("texts", "GET", "/texts", key="manager")
    check("texts listed", texts and any(t["id"] == "text_start" for t in texts), texts)
    t2 = expect("texts update", "PUT", "/texts", key="manager", body={"text_help": "📚 راهنما"})
    check("text saved", t2 and any(t["id"] == "text_help" and t["text"] == "📚 راهنما" for t in t2))
    expect("texts unknown id", "PUT", "/texts", 400, key="manager", body={"nope": "x"})
    expect("texts empty", "PUT", "/texts", 400, key="manager", body={"text_help": "  "})
    b = expect("buttons", "GET", "/buttons", key="manager")
    check("buttons have layout", b and b["layout"] and b["main_keys"], b)
    b2 = expect("buttons update", "PUT", "/buttons", key="manager",
                body={"layout": [["text_sell"], ["text_usertest", "text_Purchased_services"]],
                      "buttons": {"text_sell": {"style": "success", "emoji": "5368324170671202286"}, "text_help": {"hidden": True}}})
    check("button style saved", b2 and b2["buttons"].get("text_sell", {}).get("style") == "success", b2 and b2["buttons"])
    expect("buttons bad style", "PUT", "/buttons", 400, key="manager", body={"buttons": {"text_sell": {"style": "pink"}}})
    expect("buttons unknown key", "PUT", "/buttons", 400, key="manager", body={"layout": [["nope"]]})
    b3 = expect("buttons layout only", "PUT", "/buttons", key="manager", body={"layout": [["text_sell", "text_usertest"]]})
    check("styles kept when only the layout is sent", b3 and b3["buttons"].get("text_sell", {}).get("style") == "success", b3 and b3["buttons"])
    check("missing buttons appended", b3 and len(sum(b3["layout"], [])) == 9, b3 and b3["layout"])

    # customers
    users = expect("users", "GET", "/users?limit=5", key="manager")
    check("users paged", users is not None and len(users.get("items", users)) <= 5, users)
    expect("users search", "GET", "/users?q=ali", key="manager")
    uid = "7000000001"
    u = expect("user", "GET", "/users/" + uid, key="manager")
    before = float(u["user"]["Balance"]) if u else 0
    u2 = expect("balance add", "POST", "/users/%s/balance" % uid, key="manager", body={"amount": 1000, "mode": "add"})
    expect("balance bad", "POST", "/users/%s/balance" % uid, 400, key="manager", body={"amount": "x", "mode": "add"})
    u3 = expect("user after", "GET", "/users/" + uid, key="manager")
    check("balance increased", u3 and float(u3["user"]["Balance"]) == before + 1000, (before, u3 and u3["user"]["Balance"]))
    expect("balance sub", "POST", "/users/%s/balance" % uid, key="manager", body={"amount": 1000, "mode": "sub"})
    expect("block", "POST", "/users/%s/block" % uid, key="manager", body={"reason": "test"})
    expect("unblock", "POST", "/users/%s/unblock" % uid, key="manager", body={})
    expect("verify", "POST", "/users/%s/verify" % uid, key="manager", body={})
    expect("test limit", "POST", "/users/%s/test-limit" % uid, key="manager", body={"limit": 2})
    expect("message", "POST", "/users/%s/message" % uid, key="manager", body={"text": "سلام از پنل"})
    expect("unknown user", "GET", "/users/1", 404, key="manager")
    svcs = expect("services", "GET", "/services?limit=5", key="manager")
    items = (svcs or {}).get("items", svcs) or []
    if items:
        expect("service", "GET", "/services/" + items[0]["username"], key="manager")

    # money
    expect("payments", "GET", "/payments?status=waiting", key="manager")
    expect("payment settings", "GET", "/payment-settings", key="manager")
    expect("cancel requests", "GET", "/cancel-requests", key="manager")
    ap = expect("autopay", "GET", "/autopay", key="manager")
    check("autopay pairing string", ap and ap.get("pairing", "").startswith("NXP1"), ap)
    expect("autopay toggle", "PUT", "/autopay", key="manager", body={"enabled": not ap["enabled"]})
    expect("autopay restore", "PUT", "/autopay", key="manager", body={"enabled": ap["enabled"]})
    expect("affiliates", "GET", "/affiliates", key="manager")
    expect("affiliates update", "PUT", "/affiliates", key="manager", body={"percent": "12"})

    # unpaid card payment -> approve via API
    import pymysql
    cn = pymysql.connect(unix_socket="/var/run/mysqld/mysqld.sock", user="root", database="difgo", autocommit=True)
    cur = cn.cursor()
    o1, o2 = "api%d" % time.time_ns(), "api%db" % time.time_ns()
    cur.execute("INSERT INTO Payment_report (id_user, id_order, time, price, payment_Status, Payment_Method, invoice) VALUES ('7000000002',%s,'2026/10/03 12:00:00','30000','waiting','cart to cart','0')", (o1,))
    cur.execute("INSERT INTO Payment_report (id_user, id_order, time, price, payment_Status, Payment_Method, invoice) VALUES ('7000000002',%s,'2026/10/03 12:00:00','30000','waiting','cart to cart','0')", (o2,))
    cur.execute("SELECT Balance FROM user WHERE id = '7000000002'")
    bal = float(cur.fetchone()[0])
    pend = expect("payments waiting", "GET", "/payments?status=waiting", key="manager")
    check("new payment listed", any(x["id_order"] == o1 for x in (pend or {}).get("items", pend) or []), pend)
    expect("approve", "POST", "/payments/%s/approve" % o1, key="manager", body={})
    expect("approve twice", "POST", "/payments/%s/approve" % o1, 409, key="manager", body={})
    expect("reject", "POST", "/payments/%s/reject" % o2, key="manager", body={"reason": "رسید نامعتبر"})
    expect("reject twice", "POST", "/payments/%s/reject" % o2, 409, key="manager", body={})
    cur.execute("SELECT Balance FROM user WHERE id = '7000000002'")
    check("approved once", float(cur.fetchone()[0]) == bal + 30000)
    c, _ = call("GET", "/payments/%s/receipt" % o1, "manager", raw=True)
    check("receipt without photo is 404", c == 404, c)

    # broadcast and admins
    expect("broadcast status", "GET", "/broadcast", key="manager")
    expect("broadcast", "POST", "/broadcast", key="manager", body={"text": "اطلاعیه پنل"})
    expect("broadcast busy", "POST", "/broadcast", 409, key="manager", body={"text": "دوباره"})
    expect("broadcast cancel", "DELETE", "/broadcast", key="manager")
    expect("admins", "GET", "/admins", key="manager")
    expect("add admin", "POST", "/admins", key="manager", body={"id": "5000000077"})
    expect("remove admin", "DELETE", "/admins/5000000077", key="manager")
    expect("main admin stays", "DELETE", "/admins/5000000001", 400, key="manager")


if __name__ == "__main__":
    main()
