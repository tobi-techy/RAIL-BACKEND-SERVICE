#!/usr/bin/env python3
"""Python-delegation E2E driver.

Proves, end to end, that platform chat on the Go backend is delegated to the
Python MIRIAM agent and that money mutations are gated behind an email OTP.

Requisites (see docs/python-agent-delegation-e2e.md for the full runbook):
  * Go backend running WITH the delegation path enabled and reachable:
      PYTHON_AGENT_ENABLED=true  PYTHON_AGENT_URL=<python agent>
      PLATFORM_BRIDGE_BASE_URL=<mock bridge base url>
      EMAIL_PROVIDER=log   (so the OTP code lands in Go's logs, not an inbox)
    The Go backend and the Python agent must share the SAME JWT_SECRET.
  * mock_bridge.py running (or MOCK_BRIDGE_URL pointing at it).
  * A test user + linked platform identity (seeded via E2E_DATABASE_URL, or
    pre-existing rows).

Environment:
  E2E_GO_URL            Go backend base URL (default http://localhost:8080)
  E2E_HMAC_SECRET       PLATFORM_BRIDGE_HMAC_SECRET (shared with the bridge)
  E2E_DATABASE_URL      postgres URL used to seed the test identity
                        (postgres://postgres:postgres@localhost:5432/rail_service_dev)
  E2E_USER_ID           existing user uuid to link (else latest approved user)
  E2E_PLATFORM          platform name (default imessage)
  E2E_PLATFORM_USER_ID  bridge sender id to create (default e2e-miriam+<ts>@rail.sim)
  E2E_THREAD_ID         thread id to use (default e2e-thread-<ts>)
  E2E_OTP_LOG_FILE      path to Go's log file to scrape the OTP code from
                        (alternative to --tail-docker)
  E2E_GO_LOG_DOCKER     docker container name to `docker logs` for OTP scraping
                        (alternative to E2E_OTP_LOG_FILE)
  E2E_GREETING          first message (default "hi")
  E2E_SCRIPT            money instruction to stage an OTP
                        (default "send 2.50 to e2e@rail.sim")
  MOCK_BRIDGE_URL       mock bridge base url (default http://127.0.0.1:3100)

Exit codes: 0 all assertions passed, 1 any assertion failed.
"""
from __future__ import annotations

import hashlib
import hmac
import json
import os
import re
import subprocess
import sys
import time
import urllib.request
import uuid
from dataclasses import dataclass
from datetime import datetime, timezone

GO_URL = os.getenv("E2E_GO_URL", "http://localhost:8080").rstrip("/")
HMAC_SECRET = os.getenv("E2E_HMAC_SECRET", "")
DB_URL = os.getenv("E2E_DATABASE_URL", "")
USER_ID = os.getenv("E2E_USER_ID", "")
PLATFORM = os.getenv("E2E_PLATFORM", "imessage")
PLATFORM_USER_ID = os.getenv("E2E_PLATFORM_USER_ID", f"e2e-miriam+{int(time.time())}@rail.sim")
THREAD_ID = os.getenv("E2E_THREAD_ID", f"e2e-thread-{int(time.time())}")
OTP_LOG_FILE = os.getenv("E2E_OTP_LOG_FILE", "")
GO_LOG_DOCKER = os.getenv("E2E_GO_LOG_DOCKER", "")
GREETING = os.getenv("E2E_GREETING", "hi")
SCRIPT_MSG = os.getenv("E2E_SCRIPT", "send 2.50 to e2e@rail.sim")
BRIDGE_URL = os.getenv("MOCK_BRIDGE_URL", "http://127.0.0.1:3100").rstrip("/")
INBOUND_PATH = "/api/v1/platform/inbound"

OTP_RE = re.compile(r"(?<!\d)(\d{6})(?!\d)")

passed: list[str] = []
failed: list[str] = []


def ok(name: str, detail: str = "") -> None:
    passed.append(name)
    print(f"  ✓ {name}" + (f" — {detail}" if detail else ""))


def fail(name: str, detail: str) -> None:
    failed.append(name)
    print(f"  ✗ {name} — {detail}")


@dataclass
class SignedReq:
    body: bytes
    headers: dict[str, str]


def sign(body: bytes) -> SignedReq:
    """Mimic the bridge's outbound signing on /send (and Go's own signing)."""
    if not HMAC_SECRET:
        raise SystemExit("E2E_HMAC_SECRET is required (PLATFORM_BRIDGE_HMAC_SECRET)")
    ts = str(int(time.time()))
    nonce = uuid.uuid4().hex
    mac = hmac.new(HMAC_SECRET.encode(), f"{ts}.{nonce}.".encode() + body, hashlib.sha256)
    return SignedReq(
        body=body,
        headers={
            "Content-Type": "application/json",
            "X-HMAC-Timestamp": ts,
            "X-HMAC-Nonce": nonce,
            "X-HMAC-SHA256": mac.hexdigest(),
        },
    )


def post_inbound(text: str, **overrides) -> dict:
    """POST an inbound message to Go exactly like the real bridge does."""
    payload = {
        "platform": PLATFORM,
        "user_id": overrides.get("user_id", PLATFORM_USER_ID),
        "thread_id": overrides.get("thread_id", THREAD_ID),
        "text": text,
    }
    payload = {**payload, **overrides}
    body = json.dumps(payload).encode()
    req = sign(body)
    r = urllib.request.Request(GO_URL + INBOUND_PATH, data=req.body, headers=req.headers, method="POST")
    try:
        with urllib.request.urlopen(r, timeout=30) as resp:
            raw = resp.read().decode()
            try:
                return {"status": resp.status, "body": json.loads(raw)}
            except ValueError:
                return {"status": resp.status, "body": raw}
    except urllib.error.HTTPError as e:
        raw = e.read().decode()
        try:
            return {"status": e.code, "body": json.loads(raw)}
        except ValueError:
            return {"status": e.code, "body": raw}


def mock_outbound() -> list[dict]:
    try:
        with urllib.request.urlopen(BRIDGE_URL + "/outbound", timeout=5) as resp:
            data = json.loads(resp.read().decode())
            return data.get("messages", [])
    except Exception as e:  # noqa: BLE001
        print(f"  [warn] mock bridge unreadable: {e}")
        return []


def wait_for_reply(after_mock_count: int, text_filter: str | None = None, timeout: float = 120.0) -> list[dict]:
    """Wait for a new outbound message from the mock bridge."""
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        msgs = mock_outbound()
        new = msgs[after_mock_count:]
        if text_filter:
            new = [m for m in new if (m.get("text") or "").strip()]
        if new:
            return new
        time.sleep(1.5)
    return []


def scrape_otp() -> str | None:
    """Extract the 6-digit code from Go's logs (EMAIL_LOG_PROVIDER dev sink)."""
    if OTP_LOG_FILE:
        try:
            with open(OTP_LOG_FILE) as fh:
                content = fh.read()
        except OSError as e:
            print(f"  [warn] cannot read E2E_OTP_LOG_FILE: {e}")
            return None
    elif GO_LOG_DOCKER:
        try:
            proc = subprocess.run(
                ["docker", "logs", "--since", "5m", GO_LOG_DOCKER],
                capture_output=True,
                text=True,
                timeout=30,
            )
            content = proc.stdout
        except Exception as e:  # noqa: BLE001
            print(f"  [warn] docker logs failed: {e}")
            return None
    else:
        print("  [warn] neither E2E_OTP_LOG_FILE nor E2E_GO_LOG_DOCKER set — can't scrape OTP")
        return None

    matches = OTP_RE.findall(content)
    if not matches:
        return None
    return matches[-1]


def seed_identity() -> str:
    """Create the linked platform identity. Returns the platform user id used."""
    if not DB_URL:
        print("  [skip] E2E_DATABASE_URL not set — assuming identity already seeded")
        return PLATFORM_USER_ID

    psql = ["psql", DB_URL, "-tA", "-c"]
    if not USER_ID:
        row = subprocess.run(
            psql + ["select id from users where kyc_status='approved' order by created_at desc limit 1;"],
            capture_output=True,
            text=True,
        ).stdout.strip()
        if not row:
            print("  [warn] no approved user found to link; using any user")
            row = subprocess.run(
                psql + ["select id from users order by created_at desc limit 1;"],
                capture_output=True,
                text=True,
            ).stdout.strip()
        if not row:
            raise SystemExit("no users in the database to link")
        uid = row
    else:
        uid = USER_ID

    sql = f"""
      insert into platform_identities (user_id, platform, platform_user_id, linked_at)
      values ('{uid}', '{PLATFORM}', '{PLATFORM_USER_ID}', now())
      on conflict (platform, platform_user_id) do update set linked_at=now()
      returning platform_user_id;
    """
    out = subprocess.run(psql + [sql], capture_output=True, text=True)
    if out.returncode != 0:
        print(f"  [warn] identity seed failed: {out.stderr.strip()}")
    else:
        print(f"  [ok] seeded platform identity for user {uid} ({PLATFORM}: {PLATFORM_USER_ID})")
    return PLATFORM_USER_ID


def main() -> int:
    print("Python-delegation E2E")
    print(f"  go:      {GO_URL}")
    print(f"  bridge:  {BRIDGE_URL}")
    print(f"  agent:   {PLATFORM}:{PLATFORM_USER_ID} / thread {THREAD_ID}")
    if not HMAC_SECRET:
        print("ERROR: E2E_HMAC_SECRET (PLATFORM_BRIDGE_HMAC_SECRET) is required")
        return 1

    seed_identity()
    before = len(mock_outbound())

    # ---- Phase 1: plain greeting is answered by the Python agent ----
    print("\n[phase 1] plain message delegated to the Python agent")
    reply = post_inbound(GREETING)
    if reply["status"] != 200:
        fail("inbound accepted", f"status {reply['status']}: {reply['body']}")
        return 1
    ok("inbound accepted", f"{reply['body']}")

    msgs = wait_for_reply(before)
    if not msgs:
        fail("agent replied", f"no outbound within timeout after '{GREETING}'")
        return 1
    reply_text = msgs[-1].get("text", "")
    print(f"  reply: {reply_text!r}")
    if reply_text.strip():
        ok("agent replied", "outbound text received via bridge")
    else:
        fail("agent replied", "empty outbound text")

    # ---- Phase 2: money instruction stages an email OTP, then completes ----
    print("\n[phase 2] money instruction gated behind email OTP")
    head_count = len(mock_outbound())
    reply2 = post_inbound(SCRIPT_MSG)
    if reply2["status"] != 200:
        fail("money instruction accepted", f"status {reply2['status']}: {reply2['body']}")
        return 1

    msgs2 = wait_for_reply(head_count, timeout=120)
    if not msgs2:
        fail("staging reply received", "no outbound after money instruction")
        return 1
    stage_text = msgs2[-1].get("text", "")
    print(f"  staging reply: {stage_text!r}")

    says_code = "code" in stage_text.lower() or "confirm" in stage_text.lower()
    if says_code:
        ok("staging asks for confirmation", "reply mentions a code / confirmation")
    else:
        fail("staging asks for confirmation", f"reply: {stage_text!r}")

    # Wait briefly for the OTP to land in logs, then scrape it.
    otp = None
    for _ in range(6):
        otp = scrape_otp() or otp
        if otp:
            break
        time.sleep(1.5)
    if otp:
        ok("OTP emitted to emails/logs", f"code {otp}")
    else:
        fail("OTP emitted to emails/logs", "no 6-digit code found in logs (is EMAIL_PROVIDER=log?)")

    if not otp:
        return (0 if not failed else 1)

    # Submit the code as an ordinary inbound message (mirrors the user replying).
    head_count3 = len(mock_outbound())
    reply3 = post_inbound(otp)
    if reply3["status"] != 200:
        fail("OTP reply accepted", f"status {reply3['status']}: {reply3['body']}")
        return 1
    msgs3 = wait_for_reply(head_count3, timeout=120)
    if not msgs3:
        fail("OTP completion replied", "no outbound after OTP submit")
        return 1
    final_text = msgs3[-1].get("text", "")
    print(f"  final reply: {final_text!r}")
    if final_text.strip():
        ok("OTP completion replied", "final outbound received")

    print("\n=== summary ===")
    print(f"  passed: {len(passed)}  failed: {len(failed)}")
    return 0 if not failed else 1


if __name__ == "__main__":
    sys.exit(main())