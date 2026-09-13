#!/usr/bin/env python3
"""Mock Spectrum bridge for the Python-delegation E2E harness.

Playacts the real cmd/spectrum-bridge over HTTP only (no iMessage/Telegram):
  * POST /send            — receives outbound replies from Go (HMAC-verified),
                            records them for the harness to assert on.
  * GET  /outbound        — returns every recorded outbound message.
  * GET  /health          — readiness probe.

Auth uses the same scheme as the real bridge: HMAC-SHA256 over
"timestamp.nonce.body" with the shared PLATFORM_BRIDGE_HMAC_SECRET
(RAIL_HMAC_SECRET on the bridge side), carried in
X-HMAC-SHA256 / X-HMAC-Timestamp / X-HMAC-Nonce.

Environment:
  MOCK_BRIDGE_PORT     listen port (default 3100)
  BRIDGE_HMAC_SECRET   shared secret; empty = accept unsigned (dev only)
"""

import hashlib
import hmac
import json
import os
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse

PORT = int(os.getenv("MOCK_BRIDGE_PORT", "3100"))
SECRET = os.getenv("BRIDGE_HMAC_SECRET", "")

# In-memory + on-disk journal of received outbound messages.
JOURNAL = os.getenv("MOCK_BRIDGE_JOURNAL", "/tmp/e2e_mock_bridge_outbound.jsonl")
_outbound: list[dict] = []
_missing_hmac_warned = False


def _verify(payload: bytes, sig: str, ts: str, nonce: str) -> bool:
    if not SECRET:
        global _missing_hmac_warned
        if not _missing_hmac_warned:
            print("[mock_bridge] WARN: no BRIDGE_HMAC_SECRET set — skipping HMAC verify")
            _missing_hmac_warned = True
        return True
    try:
        now = int(time.time())
        if abs(now - int(ts)) > 600:
            return False
    except (TypeError, ValueError):
        return False
    mac = hmac.new(SECRET.encode(), f"{ts}.{nonce}.".encode() + payload, hashlib.sha256)
    return hmac.compare_digest(mac.hexdigest(), sig or "")


def _record(msg: dict) -> None:
    _outbound.append(msg)
    try:
        with open(JOURNAL, "a") as fh:
            fh.write(json.dumps(msg) + "\n")
    except OSError as exc:
        print(f"[mock_bridge] journal write failed: {exc}")


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_args):  # silence default stderr logging
        pass

    def _read_body(self) -> bytes:
        length = int(self.headers.get("Content-Length", "0") or "0")
        return self.rfile.read(length)

    def _json(self, code: int, obj: dict) -> None:
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        path = urlparse(self.path).path
        if path != "/send":
            return self._json(404, {"error": "not found"})

        body = self._read_body()
        sig = self.headers.get("X-HMAC-SHA256", "")
        ts = self.headers.get("X-HMAC-Timestamp", "")
        nonce = self.headers.get("X-HMAC-Nonce", "")
        if not _verify(body, sig, ts, nonce):
            return self._json(401, {"error": "invalid or missing HMAC signature"})

        try:
            msg = json.loads(body.decode("utf-8"))
        except ValueError:
            return self._json(400, {"error": "invalid json"})

        _record(msg)
        print(f"[mock_bridge] recorded outbound: thread={msg.get('thread_id')} text={msg.get('text')!r}")
        return self._json(200, {"status": "queued"})

    def do_GET(self):
        path = urlparse(self.path).path
        if path == "/health":
            return self._json(200, {"status": "ok"})
        if path == "/outbound":
            return self._json(200, {"messages": _outbound, "count": len(_outbound)})
        return self._json(404, {"error": "not found"})


def main() -> None:
    # Replay any prior journal so a restarted bridge can be queried.
    if os.path.exists(JOURNAL):
        try:
            with open(JOURNAL) as fh:
                for line in fh:
                    line = line.strip()
                    if line:
                        _outbound.append(json.loads(line))
        except (OSError, ValueError):
            pass
    server = ThreadingHTTPServer(("127.0.0.1", PORT), Handler)
    print(f"[mock_bridge] listening on 127.0.0.1:{PORT}, journal={JOURNAL}")
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()