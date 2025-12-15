import requests
import time

BASE_URL = "http://localhost:8080"
TIMEOUT = 30

def test_generate_deposit_address_for_supported_blockchains():
    session = requests.Session()
    email = f"testuser_{int(time.time())}@example.com"
    password = "StrongPassw0rd!"
    headers = {"Content-Type": "application/json"}

    # 1. Register user
    register_payload = {
        "email": email,
        "password": password
    }
    r = session.post(f"{BASE_URL}/api/v1/auth/register", json=register_payload, headers=headers, timeout=TIMEOUT)
    assert r.status_code == 201, f"Registration failed: {r.text}"

    # 2. Login user to get auth token
    login_payload = {
        "email": email,
        "password": password
    }
    r = session.post(f"{BASE_URL}/api/v1/auth/login", json=login_payload, headers=headers, timeout=TIMEOUT)
    assert r.status_code == 200, f"Login failed: {r.text}"
    token = r.json().get("token") or r.json().get("accessToken")  # fallback key if any
    assert token, "No token received on login"
    auth_headers = {"Authorization": f"Bearer {token}", "Content-Type": "application/json"}

    # 3. Start onboarding
    onboarding_payload = {
        "email": email
    }
    r = session.post(f"{BASE_URL}/api/v1/onboarding/start", json=onboarding_payload, headers=auth_headers, timeout=TIMEOUT)
    assert r.status_code == 201, f"Onboarding start failed: {r.text}"
    user_id = r.json().get("userId")
    assert user_id, "No userId returned from onboarding start"

    # 4. Normally user has to verify email/OTP and submit KYC.
    # For testing, simulate KYC submission with required fields
    # Submit fake KYC documents (minimal valid)
    kyc_payload = {
        "documentType": "passport",
        "documents": [
            {
                "type": "passport_photo",
                "fileUrl": "https://example.com/fake-passport.jpg",
                "contentType": "image/jpeg"
            }
        ],
        "personalInfo": {
            "firstName": "Test",
            "lastName": "User",
            "country": "US"
        }
    }
    r = session.post(f"{BASE_URL}/api/v1/onboarding/kyc/submit", json=kyc_payload, headers=auth_headers, timeout=TIMEOUT)
    assert r.status_code == 202, f"KYC submit failed: {r.text}"

    # 5. Wait and poll onboarding status until KYC Approved or timeout (~90 seconds)
    kyc_approved = False
    for _ in range(18):
        r = session.get(f"{BASE_URL}/api/v1/onboarding/status?user_id={user_id}", headers=auth_headers, timeout=TIMEOUT)
        if r.status_code == 200:
            status = r.json()
            kyc_status = status.get("kycStatus", "").lower()
            if kyc_status == "approved":
                kyc_approved = True
                break
            elif kyc_status == "failed":
                assert False, f"KYC failed during test: {r.text}"
        else:
            assert False, f"Failed to get onboarding status: {r.text}"
        time.sleep(5)
    assert kyc_approved, "KYC not approved after waiting"

    # 6. Confirm wallet provisioning completed (ready wallets matches expected chains)
    wallet_ready = False
    for _ in range(12):
        r = session.get(f"{BASE_URL}/api/v1/wallet/status", headers=auth_headers, timeout=TIMEOUT)
        if r.status_code == 200:
            data = r.json()
            total_wallets = data.get("totalWallets", 0)
            ready_wallets = data.get("readyWallets", 0)
            # Expect at least ETH (EVM), SOL, APTOS wallets as per requirements
            if total_wallets >= 3 and ready_wallets >= 3:
                wallet_ready = True
                break
        else:
            assert False, f"Failed to get wallet status: {r.text}"
        time.sleep(5)
    assert wallet_ready, "Wallet provisioning not complete or insufficient wallets ready"

    supported_chains = ["Aptos", "Solana", "polygon", "starknet"]
    valid_chains_lower = {c.lower() for c in supported_chains}

    try:
        # 7. Test deposit address generation for each supported chain - expect 200 and valid address
        for chain in supported_chains:
            payload = {"chain": chain}
            r = session.post(f"{BASE_URL}/api/v1/funding/deposit-address", json=payload, headers=auth_headers, timeout=TIMEOUT)
            assert r.status_code == 200, f"Deposit address generation failed for chain {chain}: {r.text}"
            rsp_json = r.json()
            # Expect some address format in response; check at least address presence and chain key
            address = rsp_json.get("address") or rsp_json.get("walletAddress") or rsp_json.get("depositAddress")
            assert address and isinstance(address, str) and len(address) > 0, f"No valid address returned for chain {chain}"
        # 8. Test error handling for invalid chain (bad input)
        invalid_chain_payload = {"chain": "invalidchain"}
        r = session.post(f"{BASE_URL}/api/v1/funding/deposit-address", json=invalid_chain_payload, headers=auth_headers, timeout=TIMEOUT)
        assert r.status_code == 400, f"Invalid chain did not return 400 error, got {r.status_code}"

        # 9. Test unauthorized request (no token)
        r = session.post(f"{BASE_URL}/api/v1/funding/deposit-address", json={"chain": "Aptos"}, timeout=TIMEOUT)
        assert r.status_code == 401, f"Unauthorized request did not return 401, got {r.status_code}"

    finally:
        # (No explicit resource to delete; no cleanup endpoint specified)
        pass

test_generate_deposit_address_for_supported_blockchains()