import requests
import uuid
import time

BASE_URL = "http://localhost:8080"
TIMEOUT = 30

def test_process_kyc_callback_with_valid_and_invalid_payloads():
    # Step 1: Register a new user
    register_url = f"{BASE_URL}/api/v1/auth/register"
    email = f"testuser+{uuid.uuid4().hex[:8]}@example.com"
    password = "TestPass123!"
    register_payload = {
        "email": email,
        "password": password
    }
    headers = {"Content-Type": "application/json"}
    r = requests.post(register_url, json=register_payload, headers=headers, timeout=TIMEOUT)
    assert r.status_code == 201, f"User registration failed: {r.text}"

    # Step 2: Login the user to get token
    login_url = f"{BASE_URL}/api/v1/auth/login"
    login_payload = {
        "email": email,
        "password": password
    }
    r = requests.post(login_url, json=login_payload, headers=headers, timeout=TIMEOUT)
    assert r.status_code == 200, f"User login failed: {r.text}"
    token = r.json().get("token")
    assert token, "No token received upon login"
    auth_headers = {
        "Authorization": f"Bearer {token}",
        "Content-Type": "application/json"
    }

    # Step 3: Start onboarding with email (no phone)
    onboarding_start_url = f"{BASE_URL}/api/v1/onboarding/start"
    onboarding_payload = {
        "email": email
    }
    r = requests.post(onboarding_start_url, json=onboarding_payload, headers={"Content-Type": "application/json"}, timeout=TIMEOUT)
    # It might return 409 if user onboarding already exists, allow that case but get userId and token from response if 201
    if r.status_code == 201:
        onboarding_data = r.json()
        user_id = onboarding_data.get("userId")
        onboarding_token = onboarding_data.get("sessionToken")
        # Use onboarding_token if provided for KYC submit
        if onboarding_token:
            auth_headers["Authorization"] = f"Bearer {onboarding_token}"
    elif r.status_code == 409:
        # User already exists, get user id from onboarding status later
        # We'll try to get user_id by fetching onboarding status
        user_id = None
    else:
        assert False, f"Unexpected onboarding start response: {r.status_code} {r.text}"

    # If user_id is None, get from onboarding status (some endpoints require auth)
    if not user_id:
        onboarding_status_url = f"{BASE_URL}/api/v1/onboarding/status"
        # This endpoint requires Authorization bearer token
        r = requests.get(onboarding_status_url, headers=auth_headers, timeout=TIMEOUT)
        assert r.status_code == 200, f"Failed to fetch onboarding status: {r.text}"
        user_id = r.json().get("userId")
    assert user_id, "User ID not obtained after onboarding start"

    # Prepare to submit KYC documents to initiate a KYC flow to get a provider_ref for the callback
    kyc_submit_url = f"{BASE_URL}/api/v1/onboarding/kyc/submit"
    kyc_payload = {
        "documentType": "passport",
        "documents": [
            {
                "type": "passport",
                "fileUrl": "https://example.com/passport.jpg",
                "contentType": "image/jpeg"
            }
        ],
        "personalInfo": {
            "firstName": "Test",
            "lastName": "User",
            "dateOfBirth": "1990-01-01T00:00:00Z",
            "country": "US"
        },
        "metadata": {
            "test": "callback"
        }
    }

    r = requests.post(kyc_submit_url, headers=auth_headers, json=kyc_payload, timeout=TIMEOUT)
    assert r.status_code == 202, f"KYC submit should be accepted (202), got {r.status_code}: {r.text}"

    # Since the provider_ref is required for callback, we must fetch it.
    # It should be logged in audit_logs or part of onboarding status or wallet provisioning logs.
    # The PRD doesn't specify direct API to get provider_ref, assume onboarding status or wallet info contains it.
    # We'll poll onboarding status until KYC Pending or provider_ref appears.

    kyc_provider_ref = None
    max_retries = 10
    for _ in range(max_retries):
        r = requests.get(f"{BASE_URL}/api/v1/onboarding/status", headers=auth_headers, timeout=TIMEOUT)
        if r.status_code != 200:
            time.sleep(1)
            continue
        status_data = r.json()
        # Attempt to extract provider_ref from walletStatus or other fields if available
        # This is heuristic - no direct field in PRD - fallback to fake provider_ref for testing invalid case
        # We'll assume status_data has "requiredActions" that might contain provider_ref or the userId can serve as provider_ref for testing
        # For demonstration, use user_id as provider_ref for the valid callback test
        kyc_provider_ref = user_id
        break
    assert kyc_provider_ref, "Unable to obtain provider_ref for KYC callback"

    kyc_callback_url = f"{BASE_URL}/api/v1/kyc/callback/{kyc_provider_ref}"

    # Happy Path: Valid callback payload - simulate Approved KYC status
    valid_callback_payload = {
        "status": "approved",
        "providerReference": kyc_provider_ref,
        "details": {
            "verifiedAt": "2025-09-29T12:00:00Z",
            "documentsReviewed": True
        }
    }

    r = requests.post(kyc_callback_url, headers={"Content-Type": "application/json"}, json=valid_callback_payload, timeout=TIMEOUT)
    assert r.status_code == 200, f"Valid KYC callback failed: {r.status_code} {r.text}"

    # Verify onboarding status reflects approved KYC
    r = requests.get(f"{BASE_URL}/api/v1/onboarding/status", headers=auth_headers, timeout=TIMEOUT)
    assert r.status_code == 200, f"Failed to get onboarding status after callback: {r.text}"
    kyc_status = r.json().get("kycStatus")
    assert kyc_status and kyc_status.lower() == "approved", f"KYC status not approved after valid callback, got: {kyc_status}"

    # Sad Path: Invalid callback payload - missing required fields
    invalid_callback_payloads = [
        {},  # empty payload
        {"status": "unknown"},  # unknown status values
        {"providerReference": kyc_provider_ref},  # missing status
        {"status": "approved", "providerReference": ""},  # empty providerReference
        "not-a-json",  # invalid type payload
    ]

    for payload in invalid_callback_payloads:
        if isinstance(payload, str):
            r = requests.post(kyc_callback_url, headers={"Content-Type": "application/json"}, data=payload, timeout=TIMEOUT)
        else:
            r = requests.post(kyc_callback_url, headers={"Content-Type": "application/json"}, json=payload, timeout=TIMEOUT)
        assert r.status_code == 400, f"Invalid callback payload should return 400, got {r.status_code} for payload: {payload}"

    # Sad Path: Callback with invalid provider_ref in URL (not existing user/provider_ref)
    fake_provider_ref = "nonexistent-provider-ref-12345"
    fake_callback_url = f"{BASE_URL}/api/v1/kyc/callback/{fake_provider_ref}"
    r = requests.post(fake_callback_url, headers={"Content-Type": "application/json"}, json=valid_callback_payload, timeout=TIMEOUT)
    # The PRD states 400 for invalid callback, or 500 on server error.
    # We expect a 400 Bad Request or 404 Not Found, but 400 is the documented error for invalid callback.
    assert r.status_code == 400 or r.status_code == 404, f"Invalid provider_ref callback should return 400 or 404, got {r.status_code}"

# Call the test function
test_process_kyc_callback_with_valid_and_invalid_payloads()