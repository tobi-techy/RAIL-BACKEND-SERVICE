import requests
import time

BASE_URL = "http://localhost:8080"
TIMEOUT = 30

def test_get_wallet_addresses_filtered_by_blockchain_chain():
    session = requests.Session()

    # Common headers
    headers = {
        "Content-Type": "application/json"
    }

    # 1. Register a new user with a unique email
    import uuid
    unique_email = f"testuser+{uuid.uuid4().hex[:8]}@example.com"
    password = "StrongPassword!123"

    register_payload = {
        "email": unique_email,
        "password": password
    }

    resp = session.post(f"{BASE_URL}/api/v1/auth/register", json=register_payload, headers=headers, timeout=TIMEOUT)
    assert resp.status_code == 201, f"Unexpected register status code: {resp.status_code} {resp.text}"

    # 2. Login with the new user to get JWT token
    login_payload = {
        "email": unique_email,
        "password": password
    }
    resp = session.post(f"{BASE_URL}/api/v1/auth/login", json=login_payload, headers=headers, timeout=TIMEOUT)
    assert resp.status_code == 200, f"Login failed: {resp.status_code} {resp.text}"
    login_data = resp.json()
    assert "token" in login_data or "accessToken" in login_data or "access_token" in login_data, "No token found on login"
    # Try different key variants for token
    token = login_data.get("token") or login_data.get("accessToken") or login_data.get("access_token")
    assert token, "Empty token returned"

    auth_headers = {"Authorization": f"Bearer {token}"}

    # 3. Start onboarding process (required to begin KYC and wallet provisioning)
    onboarding_start_payload = {
        "email": unique_email
    }
    resp = session.post(f"{BASE_URL}/api/v1/onboarding/start",
                        json=onboarding_start_payload,
                        headers={**headers, **auth_headers},
                        timeout=TIMEOUT)
    # Onboarding start might fail if duplicate or something else; allow 201 or 409
    assert resp.status_code in (201, 409), f"Onboarding start unexpected status: {resp.status_code} {resp.text}"

    # 4. Submit minimal KYC documents to get approved status (simulate approved after waiting)
    # Submit KYC documents with dummy data
    kyc_payload = {
        "documentType": "IDENTITY",
        "documents": [
            {
                "type": "id_front",
                "fileUrl": "http://example.com/id_front.jpg",
                "contentType": "image/jpeg"
            },
            {
                "type": "id_back",
                "fileUrl": "http://example.com/id_back.jpg",
                "contentType": "image/jpeg"
            }
        ]
    }
    resp = session.post(f"{BASE_URL}/api/v1/onboarding/kyc/submit",
                        json=kyc_payload,
                        headers={**headers, **auth_headers},
                        timeout=TIMEOUT)
    assert resp.status_code == 202, f"KYC submit failed: {resp.status_code} {resp.text}"

    # 5. Poll onboarding/status endpoint until KYC is Approved and wallets created or timeout 120s
    kyc_approved = False
    max_wait = 120
    interval = 5
    user_id = None

    for _ in range(0, max_wait, interval):
        resp = session.get(f"{BASE_URL}/api/v1/onboarding/status", headers={**headers, **auth_headers}, timeout=TIMEOUT)
        if resp.status_code == 200:
            data = resp.json()
            kyc_status = data.get("kycStatus", "").lower()
            user_id = data.get("userId")
            wallet_status = data.get("walletStatus")
            if kyc_status == "approved":
                # Check if walletStatus exists and some wallets present (ready or pending)
                if wallet_status and isinstance(wallet_status, dict):
                    # Assuming walletStatus contains data on wallets readiness
                    kyc_approved = True
                    break
        time.sleep(interval)

    assert kyc_approved, "KYC approval or wallet provisioning did not complete in time"

    # 6. Define supported chains to test filtering
    supported_chains = ["ETH", "SOL", "APTOS", "ETH-SEPOLIA", "SOL-DEVNET", "APTOS-TESTNET"]

    # 7. Test fetching wallet addresses filtered by each supported chain - happy path
    for chain in supported_chains:
        params = {"chain": chain}
        resp = session.get(f"{BASE_URL}/api/v1/wallet/addresses",
                           headers={**headers, **auth_headers},
                           params=params,
                           timeout=TIMEOUT)
        assert resp.status_code == 200, f"Failed to get wallet address for chain {chain}: {resp.status_code} {resp.text}"
        resp_json = resp.json()
        assert isinstance(resp_json, dict), f"Response is not a dict for chain {chain}"
        wallets = resp_json.get("wallets")
        assert isinstance(wallets, list), f"wallets not a list for chain {chain}"
        for wallet in wallets:
            assert "chain" in wallet, "Wallet missing 'chain' field"
            assert wallet["chain"].upper() == chain.upper(), f"Returned wallet chain mismatch: expected {chain}, got {wallet['chain']}"
            assert "address" in wallet and wallet["address"], "Wallet missing or empty 'address'"
            assert "status" in wallet and wallet["status"], "Wallet missing or empty 'status'"

    # 8. Test fetching wallet addresses without chain filter returns all wallets
    resp = session.get(f"{BASE_URL}/api/v1/wallet/addresses",
                       headers={**headers, **auth_headers},
                       timeout=TIMEOUT)
    assert resp.status_code == 200, f"Failed to get all wallet addresses: {resp.status_code} {resp.text}"
    resp_json = resp.json()
    wallets = resp_json.get("wallets")
    assert isinstance(wallets, list) and len(wallets) > 0, "No wallets returned when no chain filter applied"

    # 9. Test invalid chain parameter returns 400 error
    invalid_chains = ["INVALIDCHAIN", "123", "", "ethereummainnet"]
    for bad_chain in invalid_chains:
        params = {"chain": bad_chain}
        resp = session.get(f"{BASE_URL}/api/v1/wallet/addresses",
                           headers={**headers, **auth_headers},
                           params=params,
                           timeout=TIMEOUT)
        assert resp.status_code == 400, f"Invalid chain '{bad_chain}' did not return 400, got {resp.status_code}"

    # 10. Test unauthorized request returns 401
    resp = session.get(f"{BASE_URL}/api/v1/wallet/addresses",
                       headers={**headers},  # no auth header
                       timeout=TIMEOUT)
    assert resp.status_code == 401 or resp.status_code == 403, f"Unauthorized request did not return 401/403, got {resp.status_code}"


test_get_wallet_addresses_filtered_by_blockchain_chain()