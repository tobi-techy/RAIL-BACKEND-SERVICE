import requests
import uuid
import time

BASE_URL = "http://localhost:8080"
TIMEOUT = 30


def test_submit_kyc_documents_complete_incomplete():
    """
    Test the KYC document submission endpoint with complete and incomplete data,
    including valid/invalid document URLs and unauthorized submissions.
    """

    # Helper function to register and start onboarding
    def register_and_start_onboarding(email, phone=None):
        # Register user
        register_payload = {
            "email": email,
            "password": "TestPass123!"
        }
        register_resp = requests.post(
            f"{BASE_URL}/api/v1/auth/register",
            json=register_payload,
            timeout=TIMEOUT,
        )
        # 409 if user exists, ignore for reruns
        assert register_resp.status_code in (201, 409), f"Registration failed: {register_resp.text}"

        # Start onboarding
        onboarding_payload = {
            "email": email,
        }
        if phone:
            onboarding_payload["phone"] = phone

        onboarding_resp = requests.post(
            f"{BASE_URL}/api/v1/onboarding/start",
            json=onboarding_payload,
            timeout=TIMEOUT,
        )
        assert onboarding_resp.status_code == 201, f"Onboarding start failed: {onboarding_resp.text}"
        onboarding_data = onboarding_resp.json()
        assert "userId" in onboarding_data
        assert "onboardingStatus" in onboarding_data

        return onboarding_data

    # Helper function to login and get bearer token
    def login_and_get_token(email):
        login_payload = {
            "email": email,
            "password": "TestPass123!"
        }
        login_resp = requests.post(
            f"{BASE_URL}/api/v1/auth/login",
            json=login_payload,
            timeout=TIMEOUT,
        )
        assert login_resp.status_code == 200, f"Login failed: {login_resp.text}"
        login_data = login_resp.json()
        assert "token" in login_data or "accessToken" in login_data or "sessionToken" in login_data or "jwt" in login_data or "bearer" in login_data
        # Try to find token key
        token = login_data.get("token") or login_data.get("accessToken") or login_data.get("sessionToken") or login_data.get("jwt") or login_data.get("bearer")
        # Some endpoints return sessionToken on onboarding, use that if login not present
        if not token:
            token = onboarding_data.get("sessionToken")
        assert token is not None, "No token found in login response"
        return token

    # Helper function to submit KYC documents
    def submit_kyc(token, payload):
        headers = {
            "Authorization": f"Bearer {token}",
            "Content-Type": "application/json"
        }
        return requests.post(
            f"{BASE_URL}/api/v1/onboarding/kyc/submit",
            json=payload,
            headers=headers,
            timeout=TIMEOUT,
        )

    # Generate a unique email for testing
    unique_email = f"testuser_{uuid.uuid4().hex[:8]}@example.com"

    # Register, start onboarding
    onboarding_data = register_and_start_onboarding(unique_email)

    # Wait briefly (simulate verification step - not described in API so we assume manual or auto complete)
    # For testing purposes, assume user is now authorized to submit KYC after onboarding start
    # If verification required, real flow would be more complex.

    # Login to get auth token
    token = login_and_get_token(unique_email)

    # Prepare valid KYC submission payload (complete data)
    valid_kyc_payload = {
        "documentType": "passport",
        "documents": [
            {
                "type": "id_front",
                "fileUrl": "https://example.com/docs/id_front.jpg",
                "contentType": "image/jpeg"
            },
            {
                "type": "selfie",
                "fileUrl": "https://example.com/docs/selfie.jpg",
                "contentType": "image/jpeg"
            }
        ],
        "personalInfo": {
            "firstName": "Test",
            "lastName": "User",
            "dateOfBirth": "1990-01-01T00:00:00Z",
            "country": "US",
            "address": {
                "street": "123 Test St",
                "city": "Testville",
                "postalCode": "12345",
                "country": "US"
            }
        },
        "metadata": {
            "note": "Test submission complete data"
        }
    }

    # Submit valid KYC documents - expect 202 Accepted
    resp_valid = submit_kyc(token, valid_kyc_payload)
    assert resp_valid.status_code == 202, f"Valid KYC submission failed: {resp_valid.text}"

    # Prepare incomplete KYC payloads (various cases)

    incomplete_payloads = [
        # Missing required 'documentType'
        {
            "documents": [
                {
                    "type": "id_front",
                    "fileUrl": "https://example.com/docs/id_front.jpg",
                    "contentType": "image/jpeg"
                }
            ]
        },
        # Missing required 'documents'
        {
            "documentType": "passport"
        },
        # Invalid document URL (non-URI format)
        {
            "documentType": "passport",
            "documents": [
                {
                    "type": "id_front",
                    "fileUrl": "not-a-valid-url",
                    "contentType": "image/jpeg"
                }
            ]
        },
        # Missing required fields within documents array (missing contentType)
        {
            "documentType": "passport",
            "documents": [
                {
                    "type": "id_front",
                    "fileUrl": "https://example.com/docs/id_front.jpg"
                }
            ]
        },
        # Empty documents array
        {
            "documentType": "driver_license",
            "documents": []
        }
    ]

    for idx, payload in enumerate(incomplete_payloads):
        resp = submit_kyc(token, payload)
        assert resp.status_code == 400, f"Incomplete KYC test case {idx} should return 400, got {resp.status_code}. Response: {resp.text}"

    # Test unauthorized submission (no token)
    resp_unauth = requests.post(
        f"{BASE_URL}/api/v1/onboarding/kyc/submit",
        json=valid_kyc_payload,
        timeout=TIMEOUT
    )
    assert resp_unauth.status_code == 401 or resp_unauth.status_code == 403, f"Unauthorized KYC submission should be rejected, got {resp_unauth.status_code}"


test_submit_kyc_documents_complete_incomplete()