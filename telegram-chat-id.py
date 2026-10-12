#!/usr/bin/env python3
import getpass, json, re, urllib.request, urllib.error
class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None
token = getpass.getpass("Private bot token: ").strip()
if not re.fullmatch(r"[0-9]{6,16}:[A-Za-z0-9_-]{30,80}", token):
    raise SystemExit("Invalid token format.")
opener = urllib.request.build_opener(NoRedirect())
try:
    request = urllib.request.Request(
        "https://api.telegram.org/bot" + token + "/getUpdates",
        data=json.dumps({"timeout": 0, "limit": 100, "allowed_updates": ["message"]}).encode(),
        headers={"Content-Type": "application/json"}, method="POST")
    with opener.open(request, timeout=15) as response:
        payload = json.load(response)
    if not payload.get("ok"):
        raise ValueError()
    found = set()
    for item in payload.get("result", []):
        m = item.get("message", {}); c = m.get("chat", {}); u = m.get("from", {})
        if c.get("type") == "private" and c.get("id", 0) > 0 and c.get("id") == u.get("id") and not u.get("is_bot"):
            found.add(c["id"])
    for chat_id in sorted(found):
        print("Private Chat ID:", chat_id)
    if not found:
        print("No private message found. Stop panel polling, send your bot a new message, then retry.")
except (urllib.error.URLError, ValueError, KeyError):
    raise SystemExit("Could not read Bot API. Check your token and HTTPS connection; no token is printed.")
