#!/usr/bin/env python3
"""Nexra Panel "Bot" section against a real nexrabot.

Needs the Go database run.py leaves behind, and a Nexra Panel checkout
(PANEL_DIR, default ../nexra-panel next to this repo) with `uv sync` done,
its .env (ADMIN_USERNAME=super, ADMIN_PASSWORD=superpw, PORT=8765) and
`alembic upgrade head` run. Starts the mock, the bot and the panel, then
drives the panel's /bots API as the superadmin, as the assigned admin and as
an admin the bot does not belong to.
"""
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(os.path.dirname(HERE))
PANEL = os.environ.get("PANEL_DIR", os.path.join(os.path.dirname(ROOT), "nexra-panel"))
WORK = os.environ.get("DIFF_WORK", "/tmp/nexrabot-diff")
BIN = os.environ.get("NEXRABOT", "/tmp/nexrabot")
P = "http://127.0.0.1:8765/dashboard"
FAILS = []


def req(method, path, token=None, body=None, form=None, raw=False, files=None):
    headers = {}
    data = None
    if body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    if form is not None:
        data = urllib.parse.urlencode(form).encode()
        headers["Content-Type"] = "application/x-www-form-urlencoded"
    if files is not None:
        boundary = "nxboundary"
        name, fname, content = files
        data = (("--%s\r\nContent-Disposition: form-data; name=\"%s\"; filename=\"%s\"\r\nContent-Type: application/octet-stream\r\n\r\n" % (boundary, name, fname)).encode()
                + content + ("\r\n--%s--\r\n" % boundary).encode())
        headers["Content-Type"] = "multipart/form-data; boundary=" + boundary
    if token:
        headers["Authorization"] = "Bearer " + token
    r = urllib.request.Request(P + path, data=data, method=method, headers=headers)
    try:
        with urllib.request.urlopen(r, timeout=30) as res:
            code, payload, ctype = res.status, res.read(), res.headers.get("content-type", "")
    except urllib.error.HTTPError as e:
        code, payload, ctype = e.code, e.read(), e.headers.get("content-type", "")
    if raw:
        return code, payload, ctype
    try:
        return code, json.loads(payload)
    except ValueError:
        return code, {"raw": payload[:300]}


def check(name, cond, extra=""):
    print(("[ok]   " if cond else "[FAIL] ") + name + ("" if cond else "  " + str(extra)[:500]))
    if not cond:
        FAILS.append(name)


def expect(name, method, path, code, token, body=None):
    c, j = req(method, path, token, body)
    check("%s (%s %s -> %d)" % (name, method, path, code), c == code, (c, j))
    return j.get("data") if isinstance(j, dict) else None


def login(user, pw):
    c, j = req("POST", "/login", form={"username": user, "password": pw})
    assert c == 200, (c, j)
    return j["data"]["access_token"]


def wait(url):
    for _ in range(200):
        try:
            urllib.request.urlopen(url, timeout=1)
            return
        except urllib.error.HTTPError:
            return
        except Exception:
            time.sleep(0.1)
    raise SystemExit("did not start: " + url)


def main():
    env = dict(os.environ, WALPANEL_DATA_DIR=os.path.join(PANEL, "data"))
    procs = [
        subprocess.Popen([sys.executable, os.path.join(HERE, "mock.py"), "9202"]),
        subprocess.Popen([BIN, "serve", "-c", os.path.join(WORK, "go.env")],
                         stdout=open(os.path.join(WORK, "panel_bot.log"), "w"), stderr=subprocess.STDOUT),
        subprocess.Popen(["uv", "run", "python", "main.py"], cwd=PANEL, env=env,
                         stdout=open(os.path.join(WORK, "panel.log"), "w"), stderr=subprocess.STDOUT),
    ]
    try:
        wait("http://127.0.0.1:9102/healthz")
        wait(P + "/login")
        run()
    finally:
        for p in procs:
            p.terminate()
    print("\n%d failures" % len(FAILS))
    sys.exit(1 if FAILS else 0)


def run():
    su = login("super", "superpw")
    # two admins: one gets the bot, one does not
    admins = {}
    for name in ("botadmin", "otheradmin"):
        c, j = req("GET", "/superadmin/admins", su)
        found = [a for a in j["data"] if a["username"] == name]
        if not found:
            c, j = req("POST", "/superadmin/admin", su, body={"username": name, "password": "pw123456", "panel": "none", "traffic": 0, "expiry_date": None})
            check("create admin " + name, c == 200, j)
            c, j = req("GET", "/superadmin/admins", su)
            found = [a for a in j["data"] if a["username"] == name]
        admins[name] = found[0]["id"]
    ad = login("botadmin", "pw123456")
    other = login("otheradmin", "pw123456")

    # clean slate
    for b in req("GET", "/sales-bots", su)[1]["data"]:
        req("DELETE", "/sales-bots/manage/%d" % b["id"], su)

    base = {"name": "Bot7", "url": "http://127.0.0.1:9102/api/v1/", "owner_key": "owner", "manager_key": "manager", "admin_id": admins["botadmin"]}
    expect("admin cannot connect bots", "POST", "/sales-bots/manage", 403, ad, base)
    expect("swapped keys refused", "POST", "/sales-bots/manage", 400, su, dict(base, owner_key="manager", manager_key="owner"))
    expect("wrong key refused", "POST", "/sales-bots/manage", 400, su, dict(base, owner_key="nope"))
    expect("unreachable refused", "POST", "/sales-bots/manage", 400, su, dict(base, url="http://127.0.0.1:1"))
    bot = expect("connect", "POST", "/sales-bots/manage", 200, su, base)
    check("url normalised, username read", bot and bot["url"] == "http://127.0.0.1:9102" and bot["bot_username"] == "testbot", bot)
    check("no keys in output", bot and "owner_key" not in bot and "manager_key" not in bot, bot)
    expect("duplicate name", "POST", "/sales-bots/manage", 409, su, base)
    bid = bot["id"]

    lst = expect("admin sees own bot", "GET", "/sales-bots", 200, ad)
    check("exactly the assigned bot, without the address", lst and len(lst) == 1 and lst[0]["id"] == bid and lst[0]["url"] is None, lst)
    lst = expect("other admin sees none", "GET", "/sales-bots", 200, other)
    check("other admin list empty", lst == [], lst)
    expect("other admin cannot reach it", "GET", "/sales-bots/%d/api/info" % bid, 404, other)

    info = expect("superadmin -> owner key", "GET", "/sales-bots/%d/api/info" % bid, 200, su)
    check("superadmin is owner", info and info["role"] == "owner", info)
    info = expect("admin -> manager key", "GET", "/sales-bots/%d/api/info" % bid, 200, ad)
    check("admin is manager", info and info["role"] == "manager", info)

    pan = expect("admin panel names", "GET", "/sales-bots/%d/api/panels" % bid, 200, ad)
    check("admin sees no panel credentials", pan and all("url_panel" not in p for p in pan), pan)
    expect("admin cannot add panels", "POST", "/sales-bots/%d/api/panels" % bid, 403, ad, {"name": "x"})
    expect("admin cannot delete panels", "DELETE", "/sales-bots/%d/api/panels/1" % bid, 403, ad)
    expect("admin cannot test panels", "POST", "/sales-bots/%d/api/panels/1/test" % bid, 403, ad)
    spn = expect("superadmin panel details", "GET", "/sales-bots/%d/api/panels" % bid, 200, su)
    check("superadmin sees addresses", spn and "url_panel" in spn[0], spn)

    expect("unknown resource", "GET", "/sales-bots/%d/api/secret" % bid, 404, ad)
    expect("path traversal", "GET", "/sales-bots/%d/api/users/../panels" % bid, 400, ad)

    # shop work through the proxy
    loc = pan[0]["name_panel"]
    p = expect("add product", "POST", "/sales-bots/%d/api/products" % bid, 200, ad,
               {"name": "پنل تست", "location": loc, "volume": 10, "days": 30, "price": 40000})
    expect("bad product -> bot message", "POST", "/sales-bots/%d/api/products" % bid, 400, ad, {"name": "x", "location": loc, "volume": "x", "days": 1, "price": 1})
    c, j = req("POST", "/sales-bots/%d/api/products" % bid, ad, body={"name": "x", "location": loc, "volume": "x", "days": 1, "price": 1})
    check("bot error surfaces as message", j.get("message", "").startswith("volume must be"), j)
    if p:
        expect("delete product", "DELETE", "/sales-bots/%d/api/products/%s" % (bid, p["id"]), 200, ad)
    users = expect("users with query", "GET", "/sales-bots/%d/api/users?q=ali&limit=3" % bid, 200, ad)
    check("query string passed through", users and users["total"] >= 1 and len(users["items"]) <= 3, users)
    expect("buttons", "PUT", "/sales-bots/%d/api/buttons" % bid, 200, ad, {"buttons": {"text_sell": {"style": "danger"}}})
    expect("bad button style", "PUT", "/sales-bots/%d/api/buttons" % bid, 400, ad, {"buttons": {"text_sell": {"style": "x"}}})
    expect("unknown payment", "POST", "/sales-bots/%d/api/payments/none/approve" % bid, 409, ad, {})
    c, payload, ctype = req("GET", "/sales-bots/%d/api/payments/none/receipt" % bid, ad, raw=True)
    check("missing receipt 404", c == 404, (c, payload[:100]))

    # bot key revoked -> must not log the panel user out
    req("PUT", "/sales-bots/manage/%d" % bid, su, body={"name": "Bot7"})
    import sqlite3
    cn = sqlite3.connect(os.path.join(PANEL, "data", "walpanel.db"))
    cn.execute("UPDATE telegram_bots SET manager_key = 'revoked' WHERE id = ?", (bid,))
    cn.commit()
    expect("revoked key -> 502 not 401", "GET", "/sales-bots/%d/api/info" % bid, 502, ad)
    expect("check reports it", "POST", "/sales-bots/manage/%d/check" % bid, 502, su)
    expect("re-key", "PUT", "/sales-bots/manage/%d" % bid, 200, su, {"manager_key": "manager"})
    expect("works again", "GET", "/sales-bots/%d/api/info" % bid, 200, ad)

    # deactivate / unassign / reassign
    expect("deactivate", "PUT", "/sales-bots/manage/%d" % bid, 200, su, {"is_active": False})
    expect("inactive hidden from admin", "GET", "/sales-bots/%d/api/info" % bid, 404, ad)
    expect("superadmin still reaches it", "GET", "/sales-bots/%d/api/info" % bid, 200, su)
    expect("activate", "PUT", "/sales-bots/manage/%d" % bid, 200, su, {"is_active": True})
    expect("reassign to other", "PUT", "/sales-bots/manage/%d" % bid, 200, su, {"admin_id": admins["otheradmin"]})
    expect("old admin lost it", "GET", "/sales-bots/%d/api/info" % bid, 404, ad)
    expect("new admin has it", "GET", "/sales-bots/%d/api/info" % bid, 200, other)
    expect("unassign", "PUT", "/sales-bots/manage/%d" % bid, 200, su, {"unassign": True})
    expect("nobody but superadmin", "GET", "/sales-bots/%d/api/info" % bid, 404, other)
    expect("assign back", "PUT", "/sales-bots/manage/%d" % bid, 200, su, {"admin_id": admins["botadmin"]})

    # auto-confirm app
    expect("app info", "GET", "/sales-bots/autopay-app/info", 200, ad)
    c, j = req("POST", "/sales-bots/autopay-app/upload", ad, files=("file", "a.apk", b"PK\x03\x04fake"))
    check("admin cannot upload app", c == 403, (c, j))
    c, j = req("POST", "/sales-bots/autopay-app/upload", su, files=("file", "a.txt", b"PK"))
    check("non-apk refused", c == 400, (c, j))
    c, j = req("POST", "/sales-bots/autopay-app/upload", su, files=("file", "a.apk", b"not a zip"))
    check("non-zip refused", c == 400, (c, j))
    c, j = req("POST", "/sales-bots/autopay-app/upload", su, files=("file", "nexra.apk", b"PK\x03\x04fake apk"))
    check("superadmin uploads app", c == 200 and j["data"]["available"], (c, j))
    c, payload, ctype = req("GET", "/sales-bots/autopay-app/download", ad, raw=True)
    check("admin downloads app", c == 200 and payload == b"PK\x03\x04fake apk" and "android" in ctype, (c, ctype))
    c, payload, ctype = req("GET", "/sales-bots/autopay-app/download", None, raw=True)
    check("download needs login", c == 401, c)

    # premium emoji packs: the superadmin's list is what the bots allow
    packs_file = os.path.join(PANEL, "data", "emoji-packs.json")
    if os.path.exists(packs_file):
        os.remove(packs_file)
    p = expect("no packs yet", "GET", "/sales-bots/emoji-packs", 200, ad)
    check("not configured", p and p["configured"] is False and p["packs"] == [], p)
    expect("admin cannot add packs", "POST", "/sales-bots/emoji-packs", 403, ad, {"link": "https://t.me/addemoji/NexraPack"})
    expect("unknown pack", "POST", "/sales-bots/emoji-packs", 404, su, {"link": "https://t.me/addemoji/Missing"})
    r = expect("add pack by link", "POST", "/sales-bots/emoji-packs", 200, su, {"link": "https://t.me/addemoji/NexraPack"})
    check("pack stored and pushed", r and len(r["packs"][0]["emojis"]) == 2 and r["pushed"] and r["pushed"][0]["ok"], r)
    expect("same pack twice", "POST", "/sales-bots/emoji-packs", 409, su, {"link": "NexraPack"})
    al = expect("bot got the allow-list", "GET", "/sales-bots/%d/api/emoji-allow" % bid, 200, ad)
    check("bot restricted to the pack", al and al["restricted"] and sorted(al["ids"]) == ["5368324170671202286", "5368324170671202287"], al)
    expect("admin cannot change the allow-list", "PUT", "/sales-bots/%d/api/emoji-allow" % bid, 403, ad, {"restricted": False})
    expect("admin browses an allowed pack", "GET", "/sales-bots/%d/api/emoji-pack/NexraPack" % bid, 200, ad)
    expect("admin cannot browse other packs", "GET", "/sales-bots/%d/api/emoji-pack/OtherPack" % bid, 403, ad)
    expect("icon outside the packs refused", "PUT", "/sales-bots/%d/api/buttons" % bid, 400, ad, {"buttons": {"text_sell": {"emoji": "5368324170671202299"}}})
    expect("icon from the pack accepted", "PUT", "/sales-bots/%d/api/buttons" % bid, 200, ad, {"buttons": {"text_sell": {"emoji": "5368324170671202286"}}})
    r = expect("remove pack", "DELETE", "/sales-bots/emoji-packs/NexraPack", 200, su)
    al = expect("allow-list after removal", "GET", "/sales-bots/%d/api/emoji-allow" % bid, 200, su)
    check("nothing allowed now", al and al["restricted"] and al["ids"] == [], al)
    os.remove(packs_file)
    expect("lift restriction (owner)", "PUT", "/sales-bots/%d/api/emoji-allow" % bid, 403, su, {"restricted": False})
    import urllib.request as ur
    ur.urlopen(ur.Request("http://127.0.0.1:9102/api/v1/emoji-allow", data=b'{"restricted": false}', method="PUT",
                          headers={"Authorization": "Bearer owner", "Content-Type": "application/json"}))

    # auto-confirm mode through the panel
    m = expect("autopay no_review", "PUT", "/sales-bots/%d/api/autopay" % bid, 200, ad, {"mode": "no_review"})
    check("mode saved", m and m["mode"] == "no_review" and not m["enabled"], m)
    m = expect("autopay sms", "PUT", "/sales-bots/%d/api/autopay" % bid, 200, ad, {"mode": "sms"})
    check("sms mode excludes the unchecked one", m and m["mode"] == "sms" and m["enabled"], m)
    expect("autopay off", "PUT", "/sales-bots/%d/api/autopay" % bid, 200, ad, {"mode": "off"})

    # deleting the admin leaves the bot, unassigned
    expect("disconnect bot", "DELETE", "/sales-bots/manage/%d" % bid, 200, su)
    expect("gone", "GET", "/sales-bots/%d/api/info" % bid, 404, su)


if __name__ == "__main__":
    main()
