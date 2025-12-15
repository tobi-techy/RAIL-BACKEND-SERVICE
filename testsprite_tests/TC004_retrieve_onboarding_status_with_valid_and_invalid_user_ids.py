import requests
import uuid
import time

BASE_URL = "http://localhost:8080"
TIMEOUT = 30

def test_retrieve_onboarding_status_with_valid_and_invalid_user_ids():
    # Step 1: Create a new user onboarding to get a valid userId and auth token
    email = f"testuser+{uuid.uuid4()}@example.com"
    signup_payload = {
        "email": email
    }

    headers = {
        "Content-Type": "application/json"
    }

    # Start onboarding
    resp_start = requests.post(f"{BASE_URL}/api/v1/onboarding/start", json=signup_payload, headers=headers, timeout=TIMEOUT)
    assert resp_start.status_code == 201, f"Onboarding start failed: {resp_start.text}"
    data_start = resp_start.json()

    user_id = data_start.get("userId")
    assert user_id is not None, "userId missing in onboarding start response"
    session_token = data_start.get("sessionToken")
    # sessionToken may be nullable, fallback to login if needed

    # Step 2: Authenticate to get a bearer token
    # Since KYC verification and OTP/email verification is described as required before continuing,
    # but no direct login or verification endpoint is described for continued steps,
    # we will assume for test purposes that onboarding start returns a sessionToken usable as bearer token.
    # If sessionToken is None, will try login (not mandatory in PRD for onboarding) - here we skip that due to test scope

    assert session_token is not None, "Session token is required for authenticated requests"

    auth_headers = {
        "Authorization": f"Bearer {session_token}",
        "Content-Type": "application/json"
    }

    try:
        # Wait some seconds to simulate any processing delays for provisioning or KYC (optional, can be skipped)
        time.sleep(2)

        # Step 3: Retrieve onboarding status with valid user ID
        params_valid = {"user_id": user_id}
        resp_status_valid = requests.get(f"{BASE_URL}/api/v1/onboarding/status", headers=auth_headers, params=params_valid, timeout=TIMEOUT)
        assert resp_status_valid.status_code == 200, f"Failed to get onboarding status for valid user_id: {resp_status_valid.text}"
        data_status_valid = resp_status_valid.json()

        # Validate required fields in response for valid user ID
        assert data_status_valid.get("userId") == user_id, "Returned userId does not match"
        # onboardingStatus, kycStatus should be present and strings
        assert isinstance(data_status_valid.get("onboardingStatus"), str), "onboardingStatus missing or invalid"
        assert isinstance(data_status_valid.get("kycStatus"), str), "kycStatus missing or invalid"
        # walletStatus may be null or object if provisioning done
        wallet_status = data_status_valid.get("walletStatus")
        if wallet_status is not None:
            assert isinstance(wallet_status, dict), "walletStatus should be an object or null"
        # canProceed boolean
        assert isinstance(data_status_valid.get("canProceed"), bool), "canProceed missing or not boolean"
        # completedSteps is list of strings
        completed_steps = data_status_valid.get("completedSteps")
        assert isinstance(completed_steps, list), "completedSteps missing or not list"
        for step in completed_steps:
            assert isinstance(step, str), "completedSteps items must be strings"
        # requiredActions is list of strings
        required_actions = data_status_valid.get("requiredActions")
        assert isinstance(required_actions, list), "requiredActions missing or not list"
        for action in required_actions:
            assert isinstance(action, str), "requiredActions items must be strings or empty list"

        # Step 4: Test invalid user IDs for error responses
        invalid_user_ids = [
            "not-a-uuid",
            str(uuid.uuid4()),  # random UUID not associated with any user
            "",  # empty string
            "00000000-0000-0000-0000-000000000000"  # all zeros UUID
        ]

        for invalid_id in invalid_user_ids:
            params_invalid = {"user_id": invalid_id}
            resp_invalid = requests.get(f"{BASE_URL}/api/v1/onboarding/status", headers=auth_headers, params=params_invalid, timeout=TIMEOUT)
            if invalid_id == "not-a-uuid" or invalid_id == "":
                # Expect 400 Bad Request for invalid format
                assert resp_invalid.status_code == 400, f"Expected 400 for invalid user_id format '{invalid_id}', got {resp_invalid.status_code}"
            else:
                # Expect 404 Not Found for valid UUID format but nonexistent user
                # The zero UUID may be considered invalid or not found, test both cases
                assert resp_invalid.status_code in (400, 404), f"Expected 400 or 404 for user_id '{invalid_id}', got {resp_invalid.status_code}"

    finally:
        # Clean up: API does not provide explicit user delete, best effort skip
        pass

test_retrieve_onboarding_status_with_valid_and_invalid_user_ids()