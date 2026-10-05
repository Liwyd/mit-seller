#!/usr/bin/env python3
"""Fake Telegram Bot API + Marzban + Mit Panel for the PHP-vs-Go diff test.

One process serves one "world": Telegram calls are logged per bot token and
the panel state (users) lives in memory. Control endpoints:
  GET  /__log/<token>      -> JSON list of recorded Telegram calls
  POST /__clear/<token>    -> forget that token's calls
  POST /__fail/<n>         -> make the next n panel user-creations fail
"""
import cgi
import io
import json
import sys
import threading
import time
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

LOCK = threading.Lock()
CALLS = {}           # token -> [ {method, params} ]
MSG_ID = [1000]
USERS = {}           # marzban username -> dict
FAIL = [0]
MIT_TRAFFIC = [500 * 1024 ** 3]


def now():
    return int(time.time())


def marzban_user(name, data):
    u = {
        "username": name,
        "status": data.get("status", "active"),
        "data_limit": data.get("data_limit"),
        "expire": data.get("expire", 0),
        "used_traffic": data.get("used_traffic", 0),
        "online_at": None,
        "links": ["vless://%s@host:443?type=tcp#%s" % ("00000000-0000-4000-8000-000000000000", name)],
        "subscription_url": "/sub/%s/token" % name,
        "proxies": {"vless": {"id": "uuid-x", "flow": ""}},
        "inbounds": {"vless": ["VLESS TCP"]},
    }
    return u


class H(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def body(self):
        n = int(self.headers.get("Content-Length") or 0)
        return self.rfile.read(n) if n else b""

    def send(self, code, obj, ctype="application/json"):
        raw = obj if isinstance(obj, (bytes, bytearray)) else json.dumps(obj, ensure_ascii=False).encode()
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def params(self, raw):
        ct = self.headers.get("Content-Type", "")
        if ct.startswith("application/json"):
            try:
                return json.loads(raw or b"{}")
            except Exception:
                return {}
        if ct.startswith("multipart/form-data"):
            env = {"REQUEST_METHOD": "POST", "CONTENT_TYPE": ct, "CONTENT_LENGTH": str(len(raw))}
            fs = cgi.FieldStorage(fp=io.BytesIO(raw), environ=env, keep_blank_values=True)
            out = {}
            for k in fs.keys():
                item = fs[k]
                if isinstance(item, list):
                    item = item[0]
                if item.filename:
                    out[k] = "<file:%d bytes>" % len(item.value)
                else:
                    v = item.value
                    out[k] = v.decode() if isinstance(v, bytes) else v
            return out
        if raw:
            return {k: v[0] for k, v in urllib.parse.parse_qs(raw.decode(), keep_blank_values=True).items()}
        return {}

    def do_GET(self):
        self.route("GET")

    def do_POST(self):
        self.route("POST")

    def do_PUT(self):
        self.route("PUT")

    def do_DELETE(self):
        self.route("DELETE")

    # ------------------------------------------------------------------
    def route(self, method):
        raw = self.body()
        path = urllib.parse.urlparse(self.path)
        p = path.path
        if p.startswith("/__log/"):
            with LOCK:
                return self.send(200, CALLS.get(p[7:], []))
        if p.startswith("/__clear/"):
            with LOCK:
                CALLS[p[9:]] = []
            return self.send(200, {"ok": True})
        if p.startswith("/__fail/"):
            FAIL[0] = int(p[8:])
            return self.send(200, {"ok": True})
        if p.startswith("/__users"):
            with LOCK:
                return self.send(200, USERS)
        if p.startswith("/bot"):
            return self.telegram(p, raw)
        if p.startswith("/file/bot"):
            return self.send(200, b"\x89PNG fake receipt", "image/png")
        return self.panel(method, p, path.query, raw)

    def telegram(self, p, raw):
        rest = p[4:]
        token, _, meth = rest.partition("/")
        params = self.params(raw)
        m = meth.lower()
        with LOCK:
            CALLS.setdefault(token, []).append({"method": m, "params": params})
            MSG_ID[0] += 1
            mid = MSG_ID[0]
        if m == "getchatmember":
            return self.send(200, {"ok": True, "result": {"status": "member"}})
        if m == "getme":
            return self.send(200, {"ok": True, "result": {"id": 1, "is_bot": True, "first_name": "t", "username": "testbot"}})
        if m == "getfile":
            return self.send(200, {"ok": True, "result": {"file_id": params.get("file_id"), "file_path": "photos/x.jpg"}})
        if m == "getcustomemojistickers":
            ids = params.get("custom_emoji_ids") or []
            return self.send(200, {"ok": True, "result": [
                {"file_id": "sticker-" + i, "emoji": "⭐", "custom_emoji_id": i, "set_name": "MitPack", "is_animated": True, "is_video": False,
                 "thumbnail": {"file_id": "thumb-" + i}} for i in ids if not i.startswith("404")]})
        if m == "getstickerset":
            if params.get("name") != "MitPack":
                return self.send(400, {"ok": False, "error_code": 400, "description": "Bad Request: STICKERSET_INVALID"})
            return self.send(200, {"ok": True, "result": {"name": "MitPack", "title": "Mit", "sticker_type": "custom_emoji", "stickers": [
                {"file_id": "s1", "emoji": "🛒", "custom_emoji_id": "5368324170671202286", "is_animated": False, "is_video": False},
                {"file_id": "s2", "emoji": "💎", "custom_emoji_id": "5368324170671202287", "is_animated": True, "is_video": False,
                 "thumbnail": {"file_id": "t2"}}]}})
        if m in ("answercallbackquery", "deletemessage", "setwebhook"):
            return self.send(200, {"ok": True, "result": True})
        return self.send(200, {"ok": True, "result": {"message_id": mid, "chat": {"id": params.get("chat_id")}, "date": now()}})

    # ------------------------------------------------------------------ panels
    def auth_ok(self):
        return self.headers.get("Authorization", "").startswith("Bearer ")

    def panel(self, method, p, query, raw):
        ct = self.headers.get("Content-Type", "")
        data = {}
        if raw and ct.startswith("application/json"):
            data = json.loads(raw)
        elif raw:
            data = {k: v[0] for k, v in urllib.parse.parse_qs(raw.decode()).items()}

        # Marzban
        if p == "/api/admin/token" and method == "POST":
            if data.get("password") == "wrong":
                return self.send(401, {"detail": "Incorrect username or password"})
            return self.send(200, {"access_token": "tok-" + data.get("username", ""), "token_type": "bearer"})
        if p == "/api/system":
            return self.send(200, {"version": "0.8.4", "total_user": len(USERS), "users_active": len(USERS),
                                   "mem_total": 4 * 1024 ** 3, "mem_used": 1024 ** 3,
                                   "incoming_bandwidth": 10 * 1024 ** 3, "outgoing_bandwidth": 20 * 1024 ** 3})
        if p == "/api/user" and method == "POST":
            name = data.get("username")
            with LOCK:
                if FAIL[0] > 0:
                    FAIL[0] -= 1
                    return self.send(500, {"detail": "simulated failure"})
                if name in USERS:
                    return self.send(409, {"detail": "User already exists"})
                USERS[name] = marzban_user(name, data)
                return self.send(200, USERS[name])
        if p.startswith("/api/user/"):
            parts = p[len("/api/user/"):].split("/")
            name = parts[0]
            with LOCK:
                u = USERS.get(name)
                if u is None:
                    return self.send(404, {"detail": "User not found"})
                if len(parts) == 1 and method == "GET":
                    return self.send(200, u)
                if len(parts) == 1 and method == "PUT":
                    for k in ("expire", "data_limit", "status"):
                        if k in data:
                            u[k] = data[k]
                    return self.send(200, u)
                if len(parts) == 1 and method == "DELETE":
                    del USERS[name]
                    return self.send(200, {})
                if parts[1] == "reset":
                    u["used_traffic"] = 0
                    return self.send(200, u)
                if parts[1] == "revoke_sub":
                    u["subscription_url"] = "/sub/%s/new" % name
                    return self.send(200, u)
        if p == "/api/groups":
            return self.send(200, {"groups": []})

        # Mit Panel (mounted under /dashboard in real life; any prefix works here)
        if p.endswith("/login") and method == "POST":
            if data.get("password") == "wrong":
                return self.send(401, {"success": False, "message": "Incorrect username or password"})
            return self.send(200, {"success": True, "data": {"access_token": "nx-" + data.get("username", "")}})
        if p.endswith("/dashboard"):
            return self.send(200, {"success": True, "data": {"remaining_traffic": MIT_TRAFFIC[0]}})
        if "/admin/user" in p:
            tail = p.split("/admin/user", 1)[1].strip("/")
            parts = [urllib.parse.unquote(x) for x in tail.split("/")] if tail else []
            with LOCK:
                if method == "POST" and not parts:
                    name = data.get("email")
                    if FAIL[0] > 0:
                        FAIL[0] -= 1
                        return self.send(400, {"success": False, "message": "Insufficient traffic for this admin"})
                    if name in USERS:
                        return self.send(400, {"success": False, "message": "User already exists"})
                    exp = data.get("expiry_time", 0)
                    USERS[name] = marzban_user(name, {"data_limit": data.get("total"), "expire": int(exp / 1000) if exp else 0})
                    return self.send(200, {"success": True, "data": {}})
                name = parts[0] if parts else ""
                if name not in USERS:
                    return self.send(404, {"success": False, "message": "User not found"})
                if method == "PUT" and len(parts) == 1:
                    if FAIL[0] > 0:
                        FAIL[0] -= 1
                        return self.send(400, {"success": False, "message": "Insufficient traffic for this admin"})
                    u = USERS[name]
                    exp = data.get("expiry_time", 0)
                    u["expire"] = int(exp / 1000) if exp else 0
                    u["data_limit"] = data.get("total")
                    u["status"] = "active" if data.get("enable", True) else "disabled"
                    return self.send(200, {"success": True})
                if method == "DELETE":
                    del USERS[name]
                    return self.send(200, {"success": True})
                if method == "PUT" and len(parts) == 2 and parts[1] == "reset":
                    USERS[name]["used_traffic"] = 0
                    return self.send(200, {"success": True})
        return self.send(404, {"detail": "not found: " + p})


def main():
    port = int(sys.argv[1])
    srv = ThreadingHTTPServer(("127.0.0.1", port), H)
    srv.daemon_threads = True
    srv.serve_forever()


if __name__ == "__main__":
    main()
