import requests
import uuid
import time

BASE_URL = "http://localhost:8080"
TIMEOUT = 30

# Admin user credentials for authentication (should have admin privileges)
ADMIN_EMAIL = "admin@example.com"
ADMIN_PASSWORD = "AdminPass123!"

def test_admin_create_wallets_for_user_valid_invalid_inputs():
    headers = {"Content-Type": "application/json"}
    session = requests.Session()

    def register_user(email):
        payload = {
            "email": email,
            "password": "TestPassword123!"
        }
        resp = session.post(f"{BASE_URL}/api/v1/auth/register", json=payload, headers=headers, timeout=TIMEOUT)
        # Accept both success and if user already exists (409)
        assert resp.status_code in (201, 409), f"Unexpected status code on registration: {resp.status_code}"
        return email

    def login_user(email, password):
        payload = {"email": email, "password": password}
        resp = session.post(f"{BASE_URL}/api/v1/auth/login", json=payload, headers=headers, timeout=TIMEOUT)
        assert resp.status_code == 200, f"Login failed for {email} with status {resp.status_code}"
        return resp.json().get("token") or resp.json().get("accessToken") or resp.json().get("access_token")

    def start_onboarding(token):
        hdr = headers.copy()
        hdr["Authorization"] = f"Bearer {token}"
        payload = {
            "email": test_email
        }
        resp = session.post(f"{BASE_URL}/api/v1/onboarding/start", json=payload, headers=hdr, timeout=TIMEOUT)
        assert resp.status_code == 201, f"Onboarding start failed with status {resp.status_code}"
        data = resp.json()
        assert "userId" in data, "userId missing in onboarding start response"
        return data["userId"], data.get("sessionToken")

    def submit_kyc(token, user_id, session_token=None):
        hdr = headers.copy()
        hdr["Authorization"] = f"Bearer {token}"
        # Minimal valid KYC submission with dummy doc URL
        payload = {
            "documentType": "passport",
            "documents": [
                {
                    "type": "passport",
                    "fileUrl": "https://example.com/dummy-passport.jpg",
                    "contentType": "image/jpeg"
                }
            ],
            "personalInfo": {
                "firstName": "Test",
                "lastName": "User",
                "country": "US"
            }
        }
        resp = session.post(f"{BASE_URL}/api/v1/onboarding/kyc/submit", json=payload, headers=hdr, timeout=TIMEOUT)
        assert resp.status_code == 202, f"KYC submission failed with status {resp.status_code}"

    def wait_for_kyc_approval(token, user_id, max_wait_sec=120, poll_interval=5):
        hdr = headers.copy()
        hdr["Authorization"] = f"Bearer {token}"
        start_time = time.time()
        while time.time() - start_time < max_wait_sec:
            resp = session.get(f"{BASE_URL}/api/v1/onboarding/status", params={"user_id": user_id}, headers=hdr, timeout=TIMEOUT)
            if resp.status_code == 200:
                data = resp.json()
                kyc_status = data.get("kycStatus")
                if kyc_status == "Approved":
                    return True
                elif kyc_status == "Failed":
                    raise Exception("KYC verification failed")
            time.sleep(poll_interval)
        raise TimeoutError("Timed out waiting for KYC approval")

    def admin_login():
        payload = {"email": ADMIN_EMAIL, "password": ADMIN_PASSWORD}
        resp = session.post(f"{BASE_URL}/api/v1/auth/login", json=payload, headers=headers, timeout=TIMEOUT)
        assert resp.status_code == 200, "Admin login failed"
        token = resp.json().get("token") or resp.json().get("accessToken") or resp.json().get("access_token")
        assert token is not None, "No token received for admin"
        return token

    def admin_create_wallets(token, user_id, chains):
        hdr = headers.copy()
        hdr["Authorization"] = f"Bearer {token}"
        payload = {
            "user_id": user_id,
            "chains": chains
        }
        resp = session.post(f"{BASE_URL}/api/v1/admin/wallet/create", json=payload, headers=hdr, timeout=TIMEOUT)
        return resp

    def admin_create_wallets_no_auth(user_id, chains):
        payload = {
            "user_id": user_id,
            "chains": chains
        }
        resp = session.post(f"{BASE_URL}/api/v1/admin/wallet/create", json=payload, headers=headers, timeout=TIMEOUT)
        return resp

    # Create test user with unique email
    test_email = f"testuser_{uuid.uuid4()}@example.com"
    register_user(test_email)
    user_token = login_user(test_email, "TestPassword123!")
    user_id, session_token = start_onboarding(user_token)
    submit_kyc(user_token, user_id, session_token)
    wait_for_kyc_approval(user_token, user_id)

    # Admin login for privileged actions
    admin_token = admin_login()

    # Happy path: Admin creates wallets with valid user_id and chains
    valid_chains = ["ETH", "SOL", "APTOS"]
    resp = admin_create_wallets(admin_token, user_id, valid_chains)
    assert resp.status_code == 202, f"Expected 202 for valid wallet creation, got {resp.status_code}"

    # Invalid user ID format
    invalid_user_id = "not-a-uuid"
    resp = admin_create_wallets(admin_token, invalid_user_id, valid_chains)
    assert resp.status_code == 400, f"Expected 400 for invalid user_id, got {resp.status_code}"

    # Invalid chain in list
    invalid_chains = ["INVALIDCHAIN"]
    resp = admin_create_wallets(admin_token, user_id, invalid_chains)
    assert resp.status_code == 400, f"Expected 400 for invalid chains, got {resp.status_code}"

    # Empty chains list
    resp = admin_create_wallets(admin_token, user_id, [])
    assert resp.status_code == 400, f"Expected 400 for empty chains list, got {resp.status_code}"

    # Missing user_id
    hdr = headers.copy()
    hdr["Authorization"] = f"Bearer {admin_token}"
    payload_missing_user = {"chains": valid_chains}
    resp = session.post(f"{BASE_URL}/api/v1/admin/wallet/create", json=payload_missing_user, headers=hdr, timeout=TIMEOUT)
    assert resp.status_code == 400, f"Expected 400 when missing user_id, got {resp.status_code}"

    # Missing chains
    payload_missing_chains = {"user_id": user_id}
    resp = session.post(f"{BASE_URL}/api/v1/admin/wallet/create", json=payload_missing_chains, headers=hdr, timeout=TIMEOUT)
    assert resp.status_code == 400, f"Expected 400 when missing chains, got {resp.status_code}"

    # Insufficient permissions: call without auth
    resp = admin_create_wallets_no_auth(user_id, valid_chains)
    assert resp.status_code in (401,403), f"Expected 401 or 403 for unauthenticated request, got {resp.status_code}"

    # Insufficient permissions: call with non-admin user token
    resp = admin_create_wallets(user_token, user_id, valid_chains)
    assert resp.status_code == 403, f"Expected 403 for non-admin user, got {resp.status_code}"

test_admin_create_wallets_for_user_valid_invalid_inputs()