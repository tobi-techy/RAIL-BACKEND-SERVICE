#!/usr/bin/env python3
"""
Generate comprehensive Postman collection for RAIL Service API
"""
import json

def create_request(name, method, url, description="", body=None, headers=None, response_examples=None):
    """Create a Postman request item"""
    request = {
        "name": name,
        "request": {
            "method": method,
            "header": headers or [],
            "url": {
                "raw": f"{{{{base_url}}}}{url}",
                "host": ["{{base_url}}"],
                "path": url.strip("/").split("/")
            },
            "description": description
        },
        "response": response_examples or []
    }
    
    if body:
        request["request"]["body"] = {
            "mode": "raw",
            "raw": json.dumps(body, indent=2),
            "options": {
                "raw": {
                    "language": "json"
                }
            }
        }
    
    return request

def create_folder(name, description, items):
    """Create a Postman folder"""
    return {
        "name": name,
        "description": description,
        "item": items
    }

def create_example_response(name, status, body):
    """Create an example response"""
    return {
        "name": name,
        "originalRequest": {},
        "status": status,
        "code": int(status.split()[0]),
        "_postman_previewlanguage": "json",
        "header": [
            {
                "key": "Content-Type",
                "value": "application/json"
            }
        ],
        "body": json.dumps(body, indent=2)
    }

# Health & Monitoring endpoints
health_items = [
    create_request(
        "Health Check",
        "GET",
        "/health",
        "Basic health check endpoint",
        response_examples=[
            create_example_response("Success", "200 OK", {
                "status": "healthy",
                "timestamp": "2025-12-09T20:00:00Z"
            })
        ]
    ),
    create_request(
        "Readiness Check",
        "GET",
        "/ready",
        "Check if service is ready to accept traffic",
        response_examples=[
            create_example_response("Ready", "200 OK", {
                "status": "ready",
                "checks": {
                    "database": "ok",
                    "redis": "ok"
                }
            })
        ]
    ),
    create_request(
        "Liveness Check",
        "GET",
        "/live",
        "Check if service is alive",
        response_examples=[
            create_example_response("Alive", "200 OK", {
                "status": "alive"
            })
        ]
    ),
    create_request(
        "Version Info",
        "GET",
        "/version",
        "Get service version information",
        response_examples=[
            create_example_response("Version", "200 OK", {
                "version": "2.0.0",
                "build": "abc123",
                "commit": "def456"
            })
        ]
    )
]

# Authentication endpoints
auth_items = [
    create_request(
        "Register User",
        "POST",
        "/api/v1/auth/register",
        "Register a new user account",
        body={
            "email": "user@example.com",
            "password": "SecurePass123!",
            "first_name": "John",
            "last_name": "Doe",
            "phone": "+1234567890"
        },
        response_examples=[
            create_example_response("Success", "201 Created", {
                "user_id": "usr_123abc",
                "email": "user@example.com",
                "verification_required": True,
                "message": "Verification code sent to email"
            })
        ]
    ),
    create_request(
        "Verify Email Code",
        "POST",
        "/api/v1/auth/verify-code",
        "Verify email with code sent during registration",
        body={
            "email": "user@example.com",
            "code": "123456"
        },
        response_examples=[
            create_example_response("Success", "200 OK", {
                "verified": True,
                "message": "Email verified successfully"
            })
        ]
    ),
    create_request(
        "Resend Verification Code",
        "POST",
        "/api/v1/auth/resend-code",
        "Resend verification code to email",
        body={
            "email": "user@example.com"
        },
        response_examples=[
            create_example_response("Success", "200 OK", {
                "message": "Verification code sent"
            })
        ]
    ),
    create_request(
        "Login",
        "POST",
        "/api/v1/auth/login",
        "Login with email and password",
        body={
            "email": "user@example.com",
            "password": "SecurePass123!"
        },
        response_examples=[
            create_example_response("Success", "200 OK", {
                "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
                "refresh_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
                "token_type": "Bearer",
                "expires_in": 604800,
                "user": {
                    "id": "usr_123abc",
                    "email": "user@example.com",
                    "first_name": "John",
                    "last_name": "Doe"
                }
            })
        ]
    ),
    create_request(
        "Refresh Token",
        "POST",
        "/api/v1/auth/refresh",
        "Refresh access token using refresh token",
        body={
            "refresh_token": "{{refresh_token}}"
        },
        response_examples=[
            create_example_response("Success", "200 OK", {
                "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
                "token_type": "Bearer",
                "expires_in": 604800
            })
        ]
    ),
    create_request(
        "Logout",
        "POST",
        "/api/v1/auth/logout",
        "Logout and invalidate tokens",
        response_examples=[
            create_example_response("Success", "200 OK", {
                "message": "Logged out successfully"
            })
        ]
    ),
    create_request(
        "Forgot Password",
        "POST",
        "/api/v1/auth/forgot-password",
        "Request password reset",
        body={
            "email": "user@example.com"
        },
        response_examples=[
            create_example_response("Success", "200 OK", {
                "message": "Password reset link sent to email"
            })
        ]
    ),
    create_request(
        "Reset Password",
        "POST",
        "/api/v1/auth/reset-password",
        "Reset password with token",
        body={
            "token": "reset_token_here",
            "new_password": "NewSecurePass123!"
        },
        response_examples=[
            create_example_response("Success", "200 OK", {
                "message": "Password reset successfully"
            })
        ]
    )
]

# Save to file
collection = {
    "info": {
        "name": "RAIL Service API - Complete MVP",
        "description": "Complete API collection for RAIL - GenZ Web3 Investment Platform",
        "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json",
        "version": "2.0.0"
    },
    "auth": {
        "type": "bearer",
        "bearer": [{"key": "token", "value": "{{access_token}}", "type": "string"}]
    },
    "variable": [
        {"key": "base_url", "value": "http://localhost:8080", "type": "string"},
        {"key": "access_token", "value": "", "type": "string"},
        {"key": "refresh_token", "value": "", "type": "string"},
        {"key": "user_id", "value": "", "type": "string"}
    ],
    "item": [
        create_folder("🏥 Health & Monitoring", "Health check and monitoring endpoints", health_items),
        create_folder("🔐 Authentication", "User authentication and session management", auth_items)
    ]
}

print(json.dumps(collection, indent=2))
