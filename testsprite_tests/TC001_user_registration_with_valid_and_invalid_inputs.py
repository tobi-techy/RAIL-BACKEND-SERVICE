import requests
import uuid
import time

BASE_URL = "http://localhost:8080"
TIMEOUT = 30
HEADERS_JSON = {'Content-Type': 'application/json'}

def test_user_registration_with_valid_and_invalid_inputs():
    created_users = []
    try:
        # 1. Valid user registration (email + password)
        valid_email = f"testuser_{uuid.uuid4()}@example.com"
        valid_password = "TestPass123!"
        payload = {"email": valid_email, "password": valid_password}
        resp = requests.post(f"{BASE_URL}/api/v1/auth/register", json=payload, headers=HEADERS_JSON, timeout=TIMEOUT)
        assert resp.status_code == 201, f"Expected 201 Created, got {resp.status_code}, content: {resp.text}"
        created_users.append(valid_email)

        # 2. Attempt duplicate registration with the same email (should return 409)
        resp_dup = requests.post(f"{BASE_URL}/api/v1/auth/register", json=payload, headers=HEADERS_JSON, timeout=TIMEOUT)
        assert resp_dup.status_code == 409, f"Expected 409 Conflict on duplicate registration, got {resp_dup.status_code}"

        # 3. Registration missing required field: no password
        payload_missing_password = {"email": f"nopass_{uuid.uuid4()}@example.com"}
        resp_missing_pw = requests.post(f"{BASE_URL}/api/v1/auth/register", json=payload_missing_password, headers=HEADERS_JSON, timeout=TIMEOUT)
        assert resp_missing_pw.status_code == 400, f"Expected 400 Bad Request for missing password, got {resp_missing_pw.status_code}"

        # 4. Registration missing required field: no email
        payload_missing_email = {"password": "SomePassword123"}
        resp_missing_email = requests.post(f"{BASE_URL}/api/v1/auth/register", json=payload_missing_email, headers=HEADERS_JSON, timeout=TIMEOUT)
        assert resp_missing_email.status_code == 400, f"Expected 400 Bad Request for missing email, got {resp_missing_email.status_code}"

        # 5. Registration with invalid email format
        payload_invalid_email = {"email": "invalid-email-format", "password": "Password123"}
        resp_invalid_email = requests.post(f"{BASE_URL}/api/v1/auth/register", json=payload_invalid_email, headers=HEADERS_JSON, timeout=TIMEOUT)
        # Accept either 400 or 422 depending on schema validation
        assert resp_invalid_email.status_code in [400, 422], f"Expected 400 or 422 for invalid email format, got {resp_invalid_email.status_code}"

        # 6. Registration with phone only (email still required so expect 400)
        payload_phone_only = {"phone": "+1234567890"}
        resp_phone_only = requests.post(f"{BASE_URL}/api/v1/auth/register", json=payload_phone_only, headers=HEADERS_JSON, timeout=TIMEOUT)
        assert resp_phone_only.status_code == 400, f"Expected 400 Bad Request when email missing, got {resp_phone_only.status_code}"

        # 7. Registration with email and phone (phone nullable)
        valid_email2 = f"testuser2_{uuid.uuid4()}@example.com"
        payload_email_phone = {"email": valid_email2, "password": "AnotherPass123", "phone": "+1234567890"}
        resp_email_phone = requests.post(f"{BASE_URL}/api/v1/auth/register", json=payload_email_phone, headers=HEADERS_JSON, timeout=TIMEOUT)
        assert resp_email_phone.status_code == 201, f"Expected 201 Created with email and phone, got {resp_email_phone.status_code}"
        created_users.append(valid_email2)

    finally:
        # Clean up created users to maintain environment hygiene if API supports delete (not documented here)
        # Since no delete user endpoint was documented, we skip deletion.
        # Typically, cleanup or test isolation should be handled externally or via test DB.
        pass

test_user_registration_with_valid_and_invalid_inputs()
