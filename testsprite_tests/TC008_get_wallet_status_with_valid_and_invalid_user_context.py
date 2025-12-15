import requests
import uuid
import time

BASE_URL = "http://localhost:8080"
TIMEOUT = 30

def test_wallet_status_with_valid_and_invalid_user_context():
    # Helper to register and onboard a user, returning tokens and userId
    def register_and_onboard_user(email):
        # Register User
        register_payload = {
            "email": email,
            "password": "StrongPassw0rd!"
        }
        r = requests.post(f"{BASE_URL}/api/v1/auth/register", json=register_payload, timeout=TIMEOUT)
        if r.status_code == 409:
            # User exists - continue with login instead
            pass
        else:
            assert r.status_code == 201, f"Unexpected register status: {r.status_code}"

        # Login User
        login_payload = {
            "email": email,
            "password": "StrongPassw0rd!"
        }
        r = requests.post(f"{BASE_URL}/api/v1/auth/login", json=login_payload, timeout=TIMEOUT)
        assert r.status_code == 200, f"Login failed with status {r.status_code}"
        token = r.json().get("token") or r.json().get("accessToken") or r.json().get("sessionToken")
        assert token, "No token received in login response"

        # Start onboarding (KYC onboarding start)
        onboard_payload = {
            "email": email
        }
        headers = {"Authorization": f"Bearer {token}"}
        r = requests.post(f"{BASE_URL}/api/v1/onboarding/start", json=onboard_payload, timeout=TIMEOUT)
        # 201 if onboarding started successfully, 409 if onboarding exists
        assert r.status_code in (201, 409), f"Onboarding start failed: {r.status_code}"
        if r.status_code == 201:
            user_id = r.json().get("userId")
        else:
            # Duplicate onboard => get userId from onboarding status API
            r2 = requests.get(f"{BASE_URL}/api/v1/onboarding/status", headers=headers, timeout=TIMEOUT)
            assert r2.status_code == 200, f"Failed to get onboarding status: {r2.status_code}"
            user_id = r2.json().get("userId")
        assert user_id

        return user_id, token

    # Helper to submit KYC documents
    def submit_kyc(token):
        headers = {"Authorization": f"Bearer {token}"}
        kyc_payload = {
            "documentType": "passport",
            "documents": [
                {
                    "type": "passport",
                    "fileUrl": "https://example.com/kyc/passport.jpg",
                    "contentType": "image/jpeg"
                }
            ],
            "personalInfo": {
                "firstName": "John",
                "lastName": "Doe",
                "dateOfBirth": "1990-01-01T00:00:00Z",
                "country": "US"
            }
        }
        r = requests.post(f"{BASE_URL}/api/v1/onboarding/kyc/submit", json=kyc_payload, headers=headers, timeout=TIMEOUT)
        assert r.status_code == 202, f"KYC submission failed with status {r.status_code}"

    # Helper to poll onboarding status until KYC approved or timeout (max wait ~2 minutes)
    def wait_for_kyc_approved(token, user_id, max_wait=120, interval=5):
        headers = {"Authorization": f"Bearer {token}"}
        elapsed = 0
        while elapsed < max_wait:
            r = requests.get(f"{BASE_URL}/api/v1/onboarding/status", headers=headers, timeout=TIMEOUT)
            assert r.status_code == 200, f"Onboarding status failed: {r.status_code}"
            data = r.json()
            assert data.get("userId") == user_id

            kyc_status = data.get("kycStatus")
            wallet_status = data.get("walletStatus")
            can_proceed = data.get("canProceed")
            required_actions = data.get("requiredActions")

            if kyc_status == "Approved":
                # Wallet provisioning should start or be done
                return data
            elif kyc_status == "Failed":
                raise Exception(f"KYC failed as per onboarding status. Required actions: {required_actions}")
            # Pending or other: wait and retry
            time.sleep(interval)
            elapsed += interval

        raise TimeoutError("Timed out waiting for KYC approval")

    # Helper to get wallet status with token
    def get_wallet_status(token):
        headers = {"Authorization": f"Bearer {token}"}
        r = requests.get(f"{BASE_URL}/api/v1/wallet/status", headers=headers, timeout=TIMEOUT)
        return r

    # Generate a unique email for testing
    test_email = f"testuser+{uuid.uuid4()}@example.com"

    user_id = None
    token = None

    try:
        # Register, login, start onboarding
        user_id, token = register_and_onboard_user(test_email)

        # Submit KYC documents
        submit_kyc(token)

        # Wait for KYC approval and wallet provisioning status
        onboarding_data = wait_for_kyc_approved(token, user_id)

        # Validate wallet provisioning info in onboarding status
        wallet_status = onboarding_data.get("walletStatus")
        assert wallet_status is not None, "Wallet status should be present after KYC approval"

        # Get wallet status endpoint response (happy path)
        r = get_wallet_status(token)
        assert r.status_code == 200, f"Wallet status endpoint failed: {r.status_code}"
        data = r.json()
        assert data.get("userId") == user_id
        # Validate keys presence and types
        for key in ["totalWallets", "readyWallets", "pendingWallets", "failedWallets", "walletsByChain"]:
            assert key in data, f"{key} missing in wallet status response"
        assert isinstance(data.get("totalWallets"), int)
        assert isinstance(data.get("readyWallets"), int)
        assert isinstance(data.get("pendingWallets"), int)
        assert isinstance(data.get("failedWallets"), int)
        wallets_by_chain = data.get("walletsByChain")
        assert isinstance(wallets_by_chain, dict)

        # --------------------------
        # Test unauthorized access (missing / invalid token)
        r = requests.get(f"{BASE_URL}/api/v1/wallet/status", timeout=TIMEOUT)
        # Expect 401 Unauthorized or 403 Forbidden
        assert r.status_code in (401, 403), "Expected unauthorized error for missing token"

        r = requests.get(f"{BASE_URL}/api/v1/wallet/status", headers={"Authorization": "Bearer invalidtoken"}, timeout=TIMEOUT)
        assert r.status_code in (401, 403), "Expected unauthorized error for invalid token"

        # --------------------------
        # Test access with random non-existent user JWT or invalid user context
        # If the system supports user_id query param for admin, test invalid user_id param (should 400 or 404)
        # But wallet/status does not specify user_id query param so test only through token
        # We can simulate a token with no valid user, but here just test with token for non-registered user.

        # Create a dummy token format (may not work as real JWT but testing API error)
        dummy_token = "Bearer " + str(uuid.uuid4())
        r = requests.get(f"{BASE_URL}/api/v1/wallet/status", headers={"Authorization": dummy_token}, timeout=TIMEOUT)
        assert r.status_code in (401, 403, 404), "Expected unauthorized or not found for invalid user token"

    finally:
        # Cleanup would be here - no direct user delete API specified in PRD,
        # assuming test env auto-cleans or alternate approach.
        pass


test_wallet_status_with_valid_and_invalid_user_context()