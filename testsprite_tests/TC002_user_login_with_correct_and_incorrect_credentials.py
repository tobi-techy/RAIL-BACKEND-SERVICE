import requests
import uuid

BASE_URL = "http://localhost:8080"
TIMEOUT = 30
HEADERS_JSON = {"Content-Type": "application/json"}

def test_user_login_with_correct_and_incorrect_credentials():
    # We will create a unique user for testing login happy path
    test_email = f"testuser_{uuid.uuid4()}@example.com"
    test_password = "StrongP@ssw0rd!"
    register_url = f"{BASE_URL}/api/v1/auth/register"
    login_url = f"{BASE_URL}/api/v1/auth/login"

    # Register the user first to ensure it's in the system
    register_payload = {
        "email": test_email,
        "password": test_password
    }

    # Cleanup variable to delete user is not provided by API, so no cleanup here
    try:
        # Register user
        r_register = requests.post(register_url, json=register_payload, headers=HEADERS_JSON, timeout=TIMEOUT)
        # Registration could return 201 (success) or 409 (user exists, unlikely here with unique email)
        assert r_register.status_code in (201, 409), f"Unexpected register status {r_register.status_code}, body: {r_register.text}"

        # Test successful login with correct credentials
        login_payload = {
            "email": test_email,
            "password": test_password
        }
        r_login_success = requests.post(login_url, json=login_payload, headers=HEADERS_JSON, timeout=TIMEOUT)
        assert r_login_success.status_code == 200, f"Expected 200 OK on valid login, got {r_login_success.status_code}"
        json_resp = r_login_success.json()
        assert "token" in json_resp or "accessToken" in json_resp or "sessionToken" in json_resp, \
            f"Login success response missing token field: {json_resp}"

        # Test login with incorrect password (invalid credentials)
        bad_password_payload = {
            "email": test_email,
            "password": "WrongPassword123!"
        }
        r_login_fail = requests.post(login_url, json=bad_password_payload, headers=HEADERS_JSON, timeout=TIMEOUT)
        assert r_login_fail.status_code == 401, f"Expected 401 Unauthorized for bad password, got {r_login_fail.status_code}"

        # Test login with non-existent email
        non_exist_email_payload = {
            "email": f"nonexistent_{uuid.uuid4()}@example.com",
            "password": "AnyPass123!"
        }
        r_login_non_exist = requests.post(login_url, json=non_exist_email_payload, headers=HEADERS_JSON, timeout=TIMEOUT)
        # API doc only states 401 for invalid credentials, so expect 401 here too
        assert r_login_non_exist.status_code == 401, f"Expected 401 Unauthorized for non-existent user, got {r_login_non_exist.status_code}"

        # Test login with missing email field
        missing_email_payload = {
            # "email" omitted
            "password": "AnyPass123!"
        }
        r_login_missing_email = requests.post(login_url, json=missing_email_payload, headers=HEADERS_JSON, timeout=TIMEOUT)
        # Spec not explicit, but likely to return 400 Bad Request for missing required fields
        assert r_login_missing_email.status_code in (400, 422), f"Expected 400 or 422 Bad Request for missing email, got {r_login_missing_email.status_code}"

        # Test login with missing password field
        missing_password_payload = {
            "email": test_email
            # "password" omitted
        }
        r_login_missing_password = requests.post(login_url, json=missing_password_payload, headers=HEADERS_JSON, timeout=TIMEOUT)
        assert r_login_missing_password.status_code in (400, 422), f"Expected 400 or 422 Bad Request for missing password, got {r_login_missing_password.status_code}"

        # Test login with invalid email format
        invalid_email_payload = {
            "email": "not-an-email",
            "password": "AnyPass123!"
        }
        r_login_invalid_email = requests.post(login_url, json=invalid_email_payload, headers=HEADERS_JSON, timeout=TIMEOUT)
        # API spec may respond 400 for invalid format
        assert r_login_invalid_email.status_code == 400, f"Expected 400 Bad Request for invalid email, got {r_login_invalid_email.status_code}"

    except requests.RequestException as e:
        assert False, f"HTTP request failed: {e}"

test_user_login_with_correct_and_incorrect_credentials()