#!/usr/bin/env python3
"""Drive one shop page through the whole local stack, no Docker.

Reads a fixture page, links the platform identity of an approved user in the
local Postgres, then POSTs the bridge-shaped inbound body (same URL, same HMAC
scheme the bridge signs with) to the Go backend and reports every outbound
message the mock bridge received. That is the chain a real text takes, with the
Spectrum SDK and Photon replaced by this signed POST at one end and the mock
bridge's /send at the other.

    # reader (Postgres store), mock bridge, Go backend: see
    # docs/miriam-shop-read.md, "Full stack without Docker"
    python3 scripts/e2e/shop_inbound_local.py ../MIRIAM/tests/fixtures/wigmore_invoice_WGT-2610-0417.pdf

Environment: GO_URL (default http://127.0.0.1:8081), BRIDGE_URL (default
http://127.0.0.1:3100), E2E_DATABASE_URL, BRIDGE_HMAC_SECRET, E2E_HANDLE,
E2E_MSG_ID. A PDF is sent as a document, anything else as a photo.
"""

from __future__ import annotations

import base64
import hashlib
import hmac
import json
import os
import secrets
import subprocess
import sys
import time
import urllib.error
import urllib.request

GO_URL = os.getenv("GO_URL", "http://127.0.0.1:8081")
BRIDGE_URL = os.getenv("BRIDGE_URL", "http://127.0.0.1:3100")
SECRET = os.getenv("BRIDGE_HMAC_SECRET", "")
DB = os.getenv("E2E_DATABASE_URL", "postgres://postgres:postgres@localhost:5432/rail_sim?sslmode=disable")
PLATFORM = os.getenv("E2E_PLATFORM", "imessage")
HANDLE = os.getenv("E2E_HANDLE", "shop-e2e@rail.sim")
MSG_ID = os.getenv("E2E_MSG_ID", f"shop-e2e-{int(time.time())}")
# One read is tens of seconds on the big tier; two messages means done.
WAIT_SECONDS = int(os.getenv("E2E_WAIT_SECONDS", "150"))


def psql(sql: str) -> str:
    if not DB:
        return ""
    return subprocess.run(
        ["psql", DB, "-tA", "-c", sql], capture_output=True, text=True
    ).stdout.strip()


def link_user() -> str:
    """Return the handle the inbound message arrives as, linked to a real user.

    A user holds one identity per platform (uq_user_platform), so an identity
    that already exists is used as it is rather than inserted over.
    """
    if not DB:
        print("  [skip] E2E_DATABASE_URL not set, assuming the identity is linked")
        return HANDLE
    uid = psql(
        "select id from users where kyc_status='approved' order by created_at desc limit 1;"
    )
    if not uid:
        raise SystemExit("no approved user in the database to link")
    existing = psql(
        "select platform_user_id from platform_identities "
        f"where user_id='{uid}' and platform='{PLATFORM}';"
    )
    if existing:
        print(f"using the already-linked {PLATFORM}:{existing} (user {uid})")
        return existing
    psql(
        "insert into platform_identities (user_id, platform, platform_user_id, linked_at) "
        f"values ('{uid}', '{PLATFORM}', '{HANDLE}', now()) "
        "on conflict (platform, platform_user_id) do update set linked_at=now();"
    )
    print(f"linked {PLATFORM}:{HANDLE} -> user {uid}")
    return HANDLE


def post_inbound(body: dict) -> tuple[int, str]:
    raw = json.dumps(body).encode()
    ts = str(int(time.time()))
    nonce = secrets.token_hex(16)
    mac = hmac.new(
        SECRET.encode(), f"{ts}.{nonce}.".encode() + raw, hashlib.sha256
    ).hexdigest()
    request = urllib.request.Request(
        f"{GO_URL}/api/v1/platform/inbound",
        data=raw,
        method="POST",
        headers={
            "Content-Type": "application/json",
            "X-HMAC-SHA256": mac,
            "X-HMAC-Timestamp": ts,
            "X-HMAC-Nonce": nonce,
        },
    )
    try:
        with urllib.request.urlopen(request, timeout=180) as response:
            return response.status, response.read().decode()
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode()


def outbound() -> list[dict]:
    with urllib.request.urlopen(f"{BRIDGE_URL}/outbound", timeout=15) as response:
        payload = json.loads(response.read().decode())
    return payload["messages"] if isinstance(payload, dict) else payload


def main() -> int:
    if not SECRET:
        raise SystemExit("BRIDGE_HMAC_SECRET is required (same value as PLATFORM_BRIDGE_HMAC_SECRET)")
    page = sys.argv[1] if len(sys.argv) > 1 else "../MIRIAM/tests/fixtures/stock_book_ledger_v2.png"
    as_document = page.lower().endswith(".pdf")
    owner = link_user()

    with open(page, "rb") as page_file:
        data = page_file.read()
    mime = "application/pdf" if as_document else "image/png"
    body: dict = {
        "platform": PLATFORM,
        "user_id": owner,
        "thread_id": owner,
        "msg_id": MSG_ID,
        "turn_id": MSG_ID,
        "text": "stock book",
    }
    if as_document:
        body.update(
            is_document=True,
            document_b64=base64.b64encode(data).decode(),
            document_mime=mime,
            document_name=os.path.basename(page),
        )
    else:
        body.update(
            is_image=True, image_b64=base64.b64encode(data).decode(), image_mime=mime
        )
    print(f"page {page}: {len(data)} bytes as {'document' if as_document else 'photo'}")

    already = len(outbound())
    started = time.time()
    status, text = post_inbound(body)
    print(f"POST /api/v1/platform/inbound -> {status} {text} ({time.time() - started:.2f}s)")
    if status != 200:
        return 1

    seen = already
    deadline = time.time() + WAIT_SECONDS
    while time.time() < deadline:
        messages = outbound()
        for message in messages[seen:]:
            seen += 1
            print(f"\n--- outbound #{seen} at {time.time() - started:.1f}s ---")
            print(message.get("text", ""))
        if seen - already >= 2 and time.time() - started > 2:
            break
        time.sleep(2)

    print(f"\noutbound messages: {seen - already} after {time.time() - started:.1f}s")
    return 0


if __name__ == "__main__":
    sys.exit(main())
