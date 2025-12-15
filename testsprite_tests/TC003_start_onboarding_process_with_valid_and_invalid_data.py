import requests
import uuid

BASE_URL = "http://localhost:8080"
TIMEOUT = 30

def test_start_onboarding_process_with_valid_and_invalid_data():
    """
    Test the onboarding start endpoint by submitting valid email and optional phone number to initiate onboarding.
    Validate error responses for missing required fields and duplicate user onboarding attempts.
    """

    headers = {"Content-Type": "application/json"}

    # Generate a unique email for testing to avoid duplication conflicts on first onboarding
    unique_email = f"testuser_{uuid.uuid4()}@example.com"
    phone_number = "+1234567890"

    # Helper function to start onboarding with given payload
    def start_onboarding(payload):
        try:
            resp = requests.post(
                f"{BASE_URL}/api/v1/onboarding/start",
                json=payload,
                headers=headers,
                timeout=TIMEOUT
            )
            return resp
        except requests.RequestException as e:
            raise AssertionError(f"Request failed: {e}")

    # 1. Happy Path: Valid email only
    payload_valid_email_only = {"email": unique_email}
    response = start_onboarding(payload_valid_email_only)
    assert response.status_code == 201, f"Expected 201, got {response.status_code} with body {response.text}"
    json_resp = response.json()
    assert "userId" in json_resp and isinstance(json_resp["userId"], str) and json_resp["userId"]
    assert "onboardingStatus" in json_resp and isinstance(json_resp["onboardingStatus"], str)
    # nextStep can be any string
    assert "nextStep" in json_resp and isinstance(json_resp["nextStep"], str)
    # sessionToken could be null or string
    assert "sessionToken" in json_resp

    user_id = json_resp["userId"]

    # 2. Happy Path: Valid email with optional phone number
    unique_email_2 = f"testuser_{uuid.uuid4()}@example.com"
    payload_valid_email_phone = {"email": unique_email_2, "phone": phone_number}
    response = start_onboarding(payload_valid_email_phone)
    assert response.status_code == 201, f"Expected 201, got {response.status_code} with body {response.text}"
    json_resp = response.json()
    assert "userId" in json_resp
    assert isinstance(json_resp["userId"], str)
    assert json_resp["userId"]
    assert "onboardingStatus" in json_resp and isinstance(json_resp["onboardingStatus"], str)
    assert "nextStep" in json_resp
    assert "sessionToken" in json_resp

    # 3. Error Case: Missing required field email
    payload_missing_email = {"phone": phone_number}
    response = start_onboarding(payload_missing_email)
    assert response.status_code == 400, f"Expected 400 for missing email, got {response.status_code}"

    # 4. Error Case: Duplicate onboarding attempt with same email
    # Using the first valid email to trigger conflict
    response = start_onboarding(payload_valid_email_only)
    assert response.status_code == 409, f"Expected 409 for duplicate user, got {response.status_code}"

    # 5. Error Case: Invalid email format
    payload_invalid_email = {"email": "not-an-email"}
    response = start_onboarding(payload_invalid_email)
    assert response.status_code == 400, f"Expected 400 for invalid email format, got {response.status_code}"

    # 6. Error Case: Empty email string
    payload_empty_email = {"email": ""}
    response = start_onboarding(payload_empty_email)
    assert response.status_code == 400, f"Expected 400 for empty email, got {response.status_code}"

    # 7. Edge Case: Null phone explicitly sent (should pass because phone is optional and nullable)
    payload_null_phone = {"email": f"testuser_{uuid.uuid4()}@example.com", "phone": None}
    response = start_onboarding(payload_null_phone)
    assert response.status_code == 201, f"Expected 201 when phone is null, got {response.status_code}"

test_start_onboarding_process_with_valid_and_invalid_data()