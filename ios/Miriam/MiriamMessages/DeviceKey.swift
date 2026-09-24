// DeviceKey.swift — the biometric-bound approval key.
//
// The private half lives in the Secure Enclave under biometryCurrentSet
// access control: every signature operation re-prompts Face ID, and changing
// the enrolled face invalidates the key (the approve flow re-enrolls once,
// audited server-side as a new enrollment).
//
// Simulator/dev fallback: no Secure Enclave exists in Simulator, so a
// software P-256 key is generated instead. The client banners this state and
// it must NEVER approve real money — the server cannot distinguish it, so
// keep simulator builds pointed at dev backends only.
//
// Keychain sharing: none needed. Trust-on-first-use keeps the key in the
// extension's own keychain; the server binds it to the user at enrollment.

import Foundation
import LocalAuthentication
import Security

enum DeviceKeyError: Error {
    case accessControlFailed
    case generationFailed(OSStatus)
    case lookupFailed(OSStatus)
    case publicKeyUnavailable
    case signFailed(OSStatus)
    case biometryChanged
}

// Sendable box for the non-Sendable LAContext across the continuation hop.
private struct Unchecked<T>: @unchecked Sendable {
    let value: T
}

enum DeviceKey {
    private static let tag = "com.railmoney.rail.confirm-approval-key"
    private static let label = "Miriam confirmation approval"

    static var isSecureEnclaveAvailable: Bool {
#if targetEnvironment(simulator)
        false
#else
        true
#endif
    }

    // MARK: - Lookup / create

    private static func query() -> [String: Any] {
        [
            kSecClass as String: kSecClassKey,
            kSecAttrApplicationTag as String: Data(tag.utf8),
            kSecAttrKeyType as String: kSecAttrKeyTypeECSECPrimeRandom,
            kSecReturnRef as String: true,
        ]
    }

    static func existing() throws -> SecKey? {
        var item: CFTypeRef?
        let status = SecItemCopyMatching(query() as CFDictionary, &item)
        switch status {
        case errSecSuccess:
            return (item as! SecKey)
        case errSecItemNotFound:
            return nil
        default:
            throw DeviceKeyError.lookupFailed(status)
        }
    }

    static func getOrCreate() throws -> SecKey {
        if let key = try existing() {
            return key
        }
        var attributes: [String: Any] = [
            kSecAttrKeyType as String: kSecAttrKeyTypeECSECPrimeRandom,
            kSecAttrKeySizeInBits as String: 256,
            kSecAttrApplicationTag as String: Data(tag.utf8),
            kSecAttrLabel as String: label,
        ]
        if isSecureEnclaveAvailable {
            guard let access = SecAccessControlCreateWithFlags(
                kCFAllocatorDefault,
                kSecAttrAccessibleWhenUnlockedThisDeviceOnly,
                [.privateKeyUsage, .biometryCurrentSet],
                nil
            ) else {
                throw DeviceKeyError.accessControlFailed
            }
            attributes[kSecAttrTokenID as String] = kSecAttrTokenIDSecureEnclave
            attributes[kSecAttrAccessControl as String] = access
        } else {
            attributes[kSecAttrAccessible as String] = kSecAttrAccessibleWhenUnlockedThisDeviceOnly
        }
        var error: Unmanaged<CFError>?
        guard let key = SecKeyCreateRandomKey(attributes as CFDictionary, &error) else {
            let code = (error?.takeRetainedValue() as? Error as NSError?)?.code ?? -1
            throw DeviceKeyError.generationFailed(OSStatus(code))
        }
        return key
    }

    static func publicKeySPKI() throws -> Data {
        let key = try getOrCreate()
        guard let pub = SecKeyCopyPublicKey(key),
              let data = SecKeyCopyExternalRepresentation(pub, nil) as Data?
        else {
            throw DeviceKeyError.publicKeyUnavailable
        }
        return data
    }

    // MARK: - Sign (prompts Face ID via the access control)

    static func sign(message: Data) throws -> Data {
        let key = try getOrCreate()
        var error: Unmanaged<CFError>?
        guard let sig = SecKeyCreateSignature(
            key,
            .ecdsaSignatureMessageX962SHA256,
            message as CFData,
            &error
        ) as Data? else {
            let nsErr = error?.takeRetainedValue() as? Error as NSError?
            // Biometry changed or key invalidated: caller deletes + re-enrolls once.
            let code = Int(nsErr?.code ?? 0)
            if nsErr?.domain == NSOSStatusErrorDomain as String,
               [Int(errSecAuthFailed), Int(errSecItemNotFound)].contains(code)
            {
                throw DeviceKeyError.biometryChanged
            }
            throw DeviceKeyError.signFailed(OSStatus(code))
        }
        return sig
    }

    static func deleteKey() {
        SecItemDelete(query() as CFDictionary)
    }
}

// MARK: - Explicit Face ID prompt (first-use enrollment UX)

/// The sign() call above prompts Face ID by itself (access control), so the
/// steady state needs no separate prompt. This gate exists for the two cases
/// that need an explicit verdict: availability checks (no UI) and the very
/// first enrollment, where no key exists to sign with yet.
@MainActor
final class BiometricGate {
    func canEvaluateBiometrics() -> Bool {
        let ctx = LAContext()
        var error: NSError?
        return ctx.canEvaluatePolicy(.deviceOwnerAuthenticationWithBiometrics, error: &error)
    }

    func authenticate(reason: String) async throws -> Bool {
        let box = Unchecked(value: LAContext())
        return try await withCheckedThrowingContinuation { continuation in
            box.value.evaluatePolicy(
                .deviceOwnerAuthenticationWithBiometrics,
                localizedReason: reason
            ) { success, error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume(returning: success)
                }
            }
        }
    }
}
