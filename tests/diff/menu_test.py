#!/usr/bin/env python3
"""Inline ("glass") main menu and the premium emoji endpoints.

Run after run.py (uses the Go database it leaves behind). A tap on an inline
menu button must give exactly what typing that button's label gives.
"""
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
WORK = os.environ.get("DIFF_WORK", "/tmp/nexrabot-diff")
BIN = os.environ.get("NEXRABOT", "/tmp/nexrabot")
BOT = "http://127.0.0.1:9102"
MOCK = "http://127.0.0.1:9202"
TOKEN = "222:GO"
FAILS = []
U = 7000000001


def http(method, url, body=None, headers=None, raw=False):
    data = None if body is None else json.dumps(body).encode()
    h = dict(headers or {})
    if data is not None:
        h["Content-Type"] = "application/json"
    try:
        with urllib.request.urlopen(urllib.request.Request(url, data=data, method=method, headers=h), timeout=20) as r:
            b, code, ct = r.read(), r.status, r.headers.get("content-type", "")
    except urllib.error.HTTPError as e:
        b, code, ct = e.read(), e.code, e.headers.get("content-type", "")
    if raw:
        return code, b, ct
    try:
        return code, json.loads(b)
    except ValueError:
        return code, b


def api(method, path, body=None):
    return http(method, BOT + "/api/v1" + path, body, {"Authorization": "Bearer manager"})


def check(name, cond, extra=""):
    print(("[ok]   " if cond else "[FAIL] ") + name + ("" if cond else "  " + str(extra)[:600]))
    if not cond:
        FAILS.append(name)


UID = [100]


def update(msg=None, data=None):
    UID[0] += 1
    frm = {"id": U, "is_bot": False, "first_name": "a", "username": "ali"}
    if data is not None:
        u = {"update_id": UID[0], "callback_query": {"id": "cq%d" % UID[0], "from": frm, "data": data,
             "message": {"message_id": 777, "date": 1, "chat": {"id": U, "type": "private"}, "text": "menu"}}}
    else:
        u = {"update_id": UID[0], "message": {"message_id": 5, "date": 1, "chat": {"id": U, "type": "private"}, "from": frm, "text": msg}}
    http("POST", MOCK + "/__clear/" + TOKEN)
    http("POST", BOT + "/index.php", u)
    time.sleep(0.3)
    return http("GET", MOCK + "/__log/" + TOKEN)[1]


def calls(log, method):
    return [c["params"] for c in log if c["method"] == method]


def main():
    procs = [subprocess.Popen([sys.executable, os.path.join(HERE, "mock.py"), "9202"]),
             subprocess.Popen([BIN, "serve", "-c", os.path.join(WORK, "go.env")],
                              stdout=open(os.path.join(WORK, "menu.log"), "w"), stderr=subprocess.STDOUT)]
    try:
        for _ in range(100):
            try:
                urllib.request.urlopen(BOT + "/healthz", timeout=1)
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
    # start from the stock menu (earlier runs may have changed it)
    http("PUT", BOT + "/api/v1/emoji-allow", {"restricted": False}, {"Authorization": "Bearer owner"})
    api("PUT", "/buttons", {"mode": "reply", "buttons": {}, "layout": [["text_sell", "text_usertest"], ["text_Purchased_services", "text_Tariff_list"],
                                                                        ["text_account", "text_Add_Balance"], ["affiliates"], ["text_support", "text_help"]]})
    code, b = api("GET", "/buttons")
    check("mode defaults to reply", b["data"]["mode"] == "reply", b["data"].get("mode"))
    labels = b["data"]["labels"]

    # keyboard mode: replies the PHP way
    log = update("/start")
    sm = calls(log, "sendmessage")
    check("reply mode: keyboard under the text field", sm and "keyboard" in sm[-1]["reply_markup"], sm)
    check("reply mode: nothing removed", not calls(log, "deletemessage"), log)
    typed = update(labels["text_Purchased_services"])

    code, b = api("PUT", "/buttons", {"mode": "inline", "buttons": {"text_sell": {"style": "success", "emoji": "5368324170671202286"}}})
    check("switch to inline", code == 200 and b["data"]["mode"] == "inline", b)
    code, b = api("PUT", "/buttons", {"mode": "glass"})
    check("bad mode refused", code == 400, b)

    log = update("/start")
    sm = calls(log, "sendmessage")
    check("inline: old keyboard removed first", sm and sm[0]["reply_markup"] == {"remove_keyboard": True}, sm)
    check("inline: the throwaway message is deleted", len(calls(log, "deletemessage")) == 1, log)
    kb = sm[-1]["reply_markup"].get("inline_keyboard") if sm else None
    flat = [x for r in (kb or []) for x in r]
    check("inline: menu buttons on the welcome message", kb and any(x.get("callback_data") == "nxm_text_sell" for x in flat), sm[-1:] if sm else log)
    sell = [x for x in flat if x.get("callback_data") == "nxm_text_sell"]
    check("inline: style and premium icon kept", sell and sell[0].get("style") == "success" and sell[0].get("icon_custom_emoji_id") == "5368324170671202286", sell)
    check("inline: no admin row for a customer", not any(x.get("callback_data") == "nxm_admin" for x in flat), flat)

    tapped = update(data="nxm_text_Purchased_services")
    ans = calls(tapped, "answercallbackquery")
    check("tap is answered", len(ans) == 1, tapped)
    strip = lambda log: [c for c in log if c["method"] not in ("answercallbackquery",)]
    check("tap == typing the label", strip(tapped) == strip(typed), (strip(tapped), strip(typed)))

    log = update(data="nxm_admin")
    check("customer cannot open admin by tap", not calls(log, "sendmessage"), log)
    log = update(data="nxm_unknown")
    check("unknown tap ignored", not calls(log, "sendmessage"), log)

    log = update(labels["text_Purchased_services"])
    check("typing still works in inline mode", strip(log) == strip(typed), log)

    code, b = api("PUT", "/buttons", {"mode": "reply"})
    check("back to reply", b["data"]["mode"] == "reply", b)
    log = update("/start")
    check("reply again: keyboard, nothing removed", calls(log, "sendmessage")[-1]["reply_markup"].get("keyboard") and not calls(log, "deletemessage"), log)

    # premium emoji
    code, body, ct = http("GET", BOT + "/api/v1/emoji/5368324170671202286", headers={"Authorization": "Bearer manager"}, raw=True)
    check("emoji preview image", code == 200 and ct.startswith("image/") and body, (code, ct))
    code, body, ct = http("GET", BOT + "/api/v1/emoji/40400000000", headers={"Authorization": "Bearer manager"}, raw=True)
    check("unknown emoji 404", code == 404, code)
    code, b = api("GET", "/emoji/abc")
    check("bad emoji id 400", code == 400, code)
    code, b = api("GET", "/emoji-pack/NexraPack")
    check("emoji pack", code == 200 and len(b["data"]["emojis"]) == 2 and b["data"]["emojis"][0]["id"] == "5368324170671202286", b)
    code, b = api("GET", "/emoji-pack/" + urllib.request.quote("https:", safe="") + "nope")
    code, b = api("GET", "/emoji-pack/missing")
    check("missing pack 404", code == 404, b)

    # allow-list pushed by the panel owner
    ok_id, other = "5368324170671202286", "5368324170671202299"
    code, b = api("PUT", "/emoji-allow", {"ids": [ok_id]})
    check("manager cannot set the allow-list", code == 403, (code, b))
    code, b = http("PUT", BOT + "/api/v1/emoji-allow", {"ids": [ok_id, "x"]}, {"Authorization": "Bearer owner"})
    check("owner sets the allow-list", code == 200 and b["data"]["restricted"] and b["data"]["ids"] == [ok_id], b)
    code, b = api("PUT", "/buttons", {"buttons": {"text_sell": {"emoji": other}}})
    check("emoji outside the packs refused", code == 400, b)
    code, b = api("PUT", "/buttons", {"buttons": {"text_sell": {"emoji": ok_id}, "text_help": {"style": "danger"}}})
    check("allowed emoji accepted", code == 200 and b["data"]["buttons"]["text_sell"]["emoji"] == ok_id, b)
    code, b = api("PUT", "/texts", {"text_start": 'hi <tg-emoji emoji-id="%s">😀</tg-emoji>' % other})
    check("text with outside emoji refused", code == 400, b)
    code, b = api("PUT", "/texts", {"text_start": 'hi <tg-emoji emoji-id="%s">😀</tg-emoji>' % ok_id})
    check("text with allowed emoji accepted", code == 200, b)
    http("PUT", BOT + "/api/v1/emoji-allow", {"ids": [other]}, {"Authorization": "Bearer owner"})
    code, b = api("GET", "/buttons")
    check("icon no longer allowed is not used", "emoji" not in b["data"]["buttons"].get("text_sell", {}), b["data"]["buttons"])
    log = update("/start")
    flat = [x for r in calls(log, "sendmessage")[-1]["reply_markup"].get("keyboard", []) for x in r]
    check("menu without the removed icon", not any(x.get("icon_custom_emoji_id") for x in flat), flat)
    code, b = http("PUT", BOT + "/api/v1/emoji-allow", {"restricted": False}, {"Authorization": "Bearer owner"})
    check("allow-list lifted", code == 200 and not b["data"]["restricted"], b)


if __name__ == "__main__":
    main()
