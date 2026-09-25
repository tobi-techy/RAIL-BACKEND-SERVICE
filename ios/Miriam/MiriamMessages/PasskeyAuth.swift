// PasskeyAuth.swift — system passkey sheet for transaction approvals.
//
// Wire shapes (options, submission, base64url) live in ConfirmAPI.swift so
// the unit-test target can compile them without AuthenticationServices UI.

import AuthenticationServices
import Foundation

// MARK: - Passkey sheet (platform authenticator, Face ID)

enum PasskeyError: Error {
    case noAnchor
    case cancelled
    case failed(String)
}

// Non-Sendable delegate hop for the Swift 6 boundary.
private final class AuthDelegateBox: NSObject, ASAuthorizationControllerDelegate {
    var continuation: CheckedContinuation<ASAuthorizationPlatformPublicKeyCredentialAssertion, Error>?

    func authorizationController(
        controller: ASAuthorizationController,
        didCompleteWithAuthorization authorization: ASAuthorization
    ) {
        if let assertion = authorization.credential as? ASAuthorizationPlatformPublicKeyCredentialAssertion {
            continuation?.resume(returning: assertion)
        } else {
            continuation?.resume(throwing: PasskeyError.failed("unexpected credential type"))
        }
        continuation = nil
    }

    func authorizationController(controller: ASAuthorizationController, didCompleteWithError error: Error) {
        if let authError = error as? ASAuthorizationError, authError.code == .canceled {
            continuation?.resume(throwing: PasskeyError.cancelled)
        } else {
            continuation?.resume(throwing: PasskeyError.failed(error.localizedDescription))
        }
        continuation = nil
    }
}

struct PasskeyAuth: Sendable {
    /// Present the system passkey sheet and return the assertion ready to
    /// submit. Throws `cancelled` when the user dismisses the sheet.
    /// MainActor: the ASAuthorization delegate protocols are main-actor
    /// isolated in this SDK, and the caller (MessagesViewController) is too.
    @MainActor
    static func authorize(
        options: AssertionOptions,
        anchor: ASPresentationAnchor
    ) async throws -> AssertionSubmission {
        guard let challenge = Base64URL.decode(options.challenge) else {
            throw PasskeyError.failed("bad challenge")
        }
        let provider = ASAuthorizationPlatformPublicKeyCredentialProvider(
            relyingPartyIdentifier: options.rpId
        )
        let request = provider.createCredentialAssertionRequest(challenge: challenge)
        request.userVerificationPreference = .required
        if let allowed = options.allowCredentials, !allowed.isEmpty {
            var descriptors: [ASAuthorizationPlatformPublicKeyCredentialDescriptor] = []
            for cred in allowed {
                if let id = Base64URL.decode(cred.id) {
                    descriptors.append(
                        ASAuthorizationPlatformPublicKeyCredentialDescriptor(credentialID: id)
                    )
                }
            }
            request.allowedCredentials = descriptors
        }

        let box = AuthDelegateBox()
        let controller = ASAuthorizationController(authorizationRequests: [request])
        controller.delegate = box
        controller.presentationContextProvider = PresentationProvider(anchor: anchor)
        return try await withCheckedThrowingContinuation { continuation in
            box.continuation = continuation
            controller.performRequests()
        }.submitJSON()
    }
}

private final class PresentationProvider: NSObject, ASAuthorizationControllerPresentationContextProviding {
    private let anchor: ASPresentationAnchor
    init(anchor: ASPresentationAnchor) { self.anchor = anchor }
    func presentationAnchor(for controller: ASAuthorizationController) -> ASPresentationAnchor {
        anchor
    }
}

private extension ASAuthorizationPlatformPublicKeyCredentialAssertion {
    /// WebAuthn assertion the Go endpoint parses
    /// (protocol.ParseCredentialRequestResponseBody). Member names follow the
    /// SDK: rawClientDataJSON / rawAuthenticatorData — not the wire names.
    func submitJSON() -> AssertionSubmission {
        AssertionSubmission(
            id: Base64URL.encode(credentialID),
            rawId: Base64URL.encode(credentialID),
            type: "public-key",
            response: AssertionResponse(
                clientDataJSON: Base64URL.encode(rawClientDataJSON),
                authenticatorData: Base64URL.encode(rawAuthenticatorData),
                signature: Base64URL.encode(signature)
            )
        )
    }
}
