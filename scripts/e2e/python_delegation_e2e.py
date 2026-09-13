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

import base64
import hashlib
import hmac
import json
import os
import re
import subprocess
import sys
import time
import urllib.error
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
STASH_MSG = os.getenv("E2E_STASH_SCRIPT", "move 1 dollar from spending to stash")
JWT_SECRET = os.getenv("E2E_JWT_SECRET") or os.getenv("JWT_SECRET", "")
BRIDGE_URL = os.getenv("MOCK_BRIDGE_URL", "http://127.0.0.1:3100").rstrip("/")
INBOUND_PATH = "/api/v1/platform/inbound"

OTP_RE = re.compile(r"(?<!\d)(\d{6})(?!\d)")
# Go's known failure copy when Python execution fails after OTP (must not pass).
EXECUTION_FAILURE_MARKERS = (
    "hit an error",
    "couldn't reach my finance brain",
    "i can't complete that right now",
)

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


def _b64url(raw: bytes) -> str:
    return base64.urlsafe_b64encode(raw).rstrip(b"=").decode("ascii")


def mint_agent_token(user_id: str, email: str, role: str, secret: str, ttl: int = 120) -> str:
    """Mint a Go-compatible HS256 agent JWT (stdlib only)."""
    now = int(time.time())
    header = _b64url(json.dumps({"alg": "HS256", "typ": "JWT"}, separators=(",", ":")).encode())
    payload = {
        "user_id": user_id,
        "email": email,
        "role": role,
        "token_type": "agent",
        "exp": now + ttl,
        "iat": now,
        "nbf": now,
        "iss": "rail_service",
        "sub": user_id,
        "jti": str(uuid.uuid4()),
    }
    body = _b64url(json.dumps(payload, separators=(",", ":")).encode())
    sig = hmac.new(secret.encode(), f"{header}.{body}".encode(), hashlib.sha256).digest()
    return f"{header}.{body}.{_b64url(sig)}"


def fetch_balances(token: str) -> dict:
    req = urllib.request.Request(
        GO_URL + "/api/v1/balances",
        headers={
            "Authorization": f"Bearer {token}",
            "X-Requested-With": "RailApp",
        },
        method="GET",
    )
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            return json.loads(resp.read().decode())
    except urllib.error.HTTPError as e:
        raw = e.read().decode()
        raise RuntimeError(f"GET /api/v1/balances -> {e.code}: {raw[:300]}") from e


def _money(value) -> float:
    try:
        return float(value)
    except (TypeError, ValueError):
        return 0.0


@dataclass
class SeededUser:
    platform_user_id: str
    user_id: str = ""
    email: str = ""
    role: str = "user"


def seed_identity() -> SeededUser:
    """Create the linked platform identity."""
    seeded = SeededUser(platform_user_id=PLATFORM_USER_ID, user_id=USER_ID)
    if not DB_URL:
        print("  [skip] E2E_DATABASE_URL not set — assuming identity already seeded")
        return seeded

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

    meta = subprocess.run(
        psql + [f"select email, kyc_status from users where id='{uid}';"],
        capture_output=True,
        text=True,
    ).stdout.strip()
    email, kyc = "", ""
    if meta:
        parts = meta.split("|")
        email = parts[0].strip() if parts else ""
        kyc = parts[1].strip() if len(parts) > 1 else ""
    role = "verified" if kyc == "approved" else "user"

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
    return SeededUser(platform_user_id=PLATFORM_USER_ID, user_id=uid, email=email, role=role)


def wait_for_otp() -> str | None:
    otp = None
    for _ in range(6):
        otp = scrape_otp() or otp
        if otp:
            break
        time.sleep(1.5)
    return otp


def assert_execution_reply(label: str, text: str) -> bool:
    """Fail loudly on Go's known execution-failure copy (previously a false pass)."""
    lowered = (text or "").lower()
    if not (text or "").strip():
        fail(label, "empty outbound text")
        return False
    for marker in EXECUTION_FAILURE_MARKERS:
        if marker in lowered:
            fail(label, f"Go reported an execution failure: {text!r}")
            return False
    ok(label, "final outbound is a real completion, not the failure copy")
    return True


def run_confirmed_action(instruction: str, phase_label: str) -> str | None:
    """Send a money instruction, collect OTP, submit it. Returns final reply text."""
    head_count = len(mock_outbound())
    reply = post_inbound(instruction)
    if reply["status"] != 200:
        fail(f"{phase_label} instruction accepted", f"status {reply['status']}: {reply['body']}")
        return None

    msgs = wait_for_reply(head_count, timeout=120)
    if not msgs:
        fail(f"{phase_label} staging reply received", "no outbound after money instruction")
        return None
    stage_text = msgs[-1].get("text", "")
    print(f"  staging reply: {stage_text!r}")

    says_code = "code" in stage_text.lower() or "confirm" in stage_text.lower()
    if says_code:
        ok(f"{phase_label} staging asks for confirmation", "reply mentions a code / confirmation")
    else:
        fail(f"{phase_label} staging asks for confirmation", f"reply: {stage_text!r}")
        return None

    otp = wait_for_otp()
    if otp:
        ok(f"{phase_label} OTP emitted to emails/logs", f"code {otp}")
    else:
        fail(f"{phase_label} OTP emitted to emails/logs", "no 6-digit code found in logs (is EMAIL_PROVIDER=log?)")
        return None

    head_count2 = len(mock_outbound())
    reply2 = post_inbound(otp)
    if reply2["status"] != 200:
        fail(f"{phase_label} OTP reply accepted", f"status {reply2['status']}: {reply2['body']}")
        return None
    msgs2 = wait_for_reply(head_count2, timeout=120)
    if not msgs2:
        fail(f"{phase_label} OTP completion replied", "no outbound after OTP submit")
        return None
    final_text = msgs2[-1].get("text", "")
    print(f"  final reply: {final_text!r}")
    if not assert_execution_reply(f"{phase_label} OTP completion succeeded", final_text):
        return None
    return final_text


def main() -> int:
    print("Python-delegation E2E")
    print(f"  go:      {GO_URL}")
    print(f"  bridge:  {BRIDGE_URL}")
    print(f"  agent:   {PLATFORM}:{PLATFORM_USER_ID} / thread {THREAD_ID}")
    if not HMAC_SECRET:
        print("ERROR: E2E_HMAC_SECRET (PLATFORM_BRIDGE_HMAC_SECRET) is required")
        return 1

    seeded = seed_identity()
    before = len(mock_outbound())

    agent_token = ""
    if JWT_SECRET and seeded.user_id:
        agent_token = mint_agent_token(seeded.user_id, seeded.email, seeded.role, JWT_SECRET)
    elif not JWT_SECRET:
        fail("agent JWT available", "E2E_JWT_SECRET/JWT_SECRET is required to prove money moved")
    elif not seeded.user_id:
        fail("agent JWT available", "user id unknown; set E2E_USER_ID or E2E_DATABASE_URL")

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

    # ---- Phase 2: money instruction stages an email OTP, then actually moves money ----
    print("\n[phase 2] money instruction gated behind email OTP")
    before_balances = None
    if agent_token:
        try:
            before_balances = fetch_balances(agent_token)
            ok("pre-action balances readable", json.dumps(before_balances))
        except Exception as e:  # noqa: BLE001
            fail("pre-action balances readable", str(e))

    run_confirmed_action(SCRIPT_MSG, "p2p")

    if agent_token and before_balances is not None:
        try:
            after_balances = fetch_balances(agent_token)
            before_spend = _money(before_balances.get("spending_balance"))
            after_spend = _money(after_balances.get("spending_balance"))
            if after_spend != before_spend:
                ok(
                    "p2p changed spending balance",
                    f"{before_spend} -> {after_spend}",
                )
            else:
                fail(
                    "p2p changed spending balance",
                    f"spending_balance unchanged at {after_spend} (action did not move money)",
                )
        except Exception as e:  # noqa: BLE001
            fail("p2p changed spending balance", str(e))

    # ---- Phase 3: stash transfer via the new OTP-safe endpoints ----
    print("\n[phase 3] stash transfer via chat-channel endpoints")
    stash_before = None
    if agent_token:
        try:
            stash_before = fetch_balances(agent_token)
            ok("pre-stash balances readable", json.dumps(stash_before))
        except Exception as e:  # noqa: BLE001
            fail("pre-stash balances readable", str(e))

    run_confirmed_action(STASH_MSG, "stash")

    if agent_token and stash_before is not None:
        try:
            stash_after = fetch_balances(agent_token)
            before_spend = _money(stash_before.get("spending_balance"))
            after_spend = _money(stash_after.get("spending_balance"))
            before_stash = _money(stash_before.get("stash_balance"))
            after_stash = _money(stash_after.get("stash_balance"))
            moved = after_stash > before_stash and after_spend < before_spend
            if moved:
                ok(
                    "stash transfer moved money",
                    f"spend {before_spend}->{after_spend}, stash {before_stash}->{after_stash}",
                )
            else:
                fail(
                    "stash transfer moved money",
                    f"spend {before_spend}->{after_spend}, stash {before_stash}->{after_stash}",
                )
        except Exception as e:  # noqa: BLE001
            fail("stash transfer moved money", str(e))

    print("\n=== summary ===")
    print(f"  passed: {len(passed)}  failed: {len(failed)}")
    return 0 if not failed else 1


if __name__ == "__main__":
    sys.exit(main())