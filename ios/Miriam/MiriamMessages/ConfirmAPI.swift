// ConfirmAPI.swift — pure Foundation logic for the Miriam confirmation card.
// No UIKit, no Messages imports: this file is compiled into BOTH the
// MiriamMessages extension target and the MiriamMessagesTests target.

import Foundation

// MARK: - Confirm link (?t=expiry.sig)

// The extension opens https://<base>/confirm/{actionId}?t={expiryUnix}.{sig}.
struct ConfirmLink: Sendable {
    let actionID: String
    let token: String
    let expiryUnix: Int64
    let baseURL: URL
}

enum ConfirmLinkError: Error {
    case notAConfirmURL
    case missingToken
    case malformedToken
}

func parseConfirmLink(_ url: URL) throws -> ConfirmLink {
    let parts = url.pathComponents.filter { $0 != "/" }
    guard parts.count >= 2, parts[parts.count - 2] == "confirm",
          let actionID = parts.last, !actionID.isEmpty
    else { throw ConfirmLinkError.notAConfirmURL }
    guard let items = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems,
          let token = items.first(where: { $0.name == "t" })?.value, !token.isEmpty
    else { throw ConfirmLinkError.missingToken }
    let sides = token.split(separator: ".", maxSplits: 1).map(String.init)
    guard sides.count == 2, let expiry = Int64(sides[0]), !sides[1].isEmpty
    else { throw ConfirmLinkError.malformedToken }
    var comps = URLComponents()
    comps.scheme = url.scheme ?? "https"
    comps.host = url.host
    comps.port = url.port
    guard let base = comps.url else { throw ConfirmLinkError.notAConfirmURL }
    try guardSimulatorHost(base)
    return ConfirmLink(actionID: actionID, token: token, expiryUnix: expiry, baseURL: base)
}

func tokenExpiry(_ token: String) -> Int64? {
    let sides = token.split(separator: ".", maxSplits: 1).map(String.init)
    guard sides.count == 2 else { return nil }
    return Int64(sides[0])
}

// MARK: - Signed message (mirrors Go SignedMessage: actionId.expiryUnix)

// The extension signs exactly the bytes the URL token covers, so a signature
// can never be replayed onto a different card or past expiry.
func signedMessage(actionID: String, expiryUnix: Int64) -> Data {
    Data("\(actionID).\(expiryUnix)".utf8)
}

// MARK: - Payloads (mirror Go publicView + cardPayload)

// swiftlint:disable:next type_name
struct ConfirmationPayload: Decodable, Sendable {
    let actionID: String
    let action: String
    let state: String
    let title: String
    let subtitle: String?
    let amount: String?
    let asset: String?
    let destination: String?
    let fee: String?
    let riskLine: String?
    let expiresAt: String?
    let result: String?
    let dead: Bool
    let live: Bool
    let assurance: String?
    let enrolledKeyID: String?

    enum CodingKeys: String, CodingKey {
        case actionID = "action_id"
        case action, state, title, subtitle, amount, asset, destination, fee
        case riskLine = "risk_line"
        case expiresAt = "expires_at"
        case result, dead, live, assurance
        case enrolledKeyID = "enrolled_key_id"
    }

    var isTerminal: Bool {
        ["completed", "rejected", "failed", "expired"].contains(state)
    }
}

struct APIEnvelope: Decodable, Sendable {
    let confirmation: ConfirmationPayload
}

struct ApproveBody: Encodable, Sendable {
    let t: String
    let biometric: String
    let deviceKeyID: String?
    let signature: String?
    let enrollDeviceKey: String?

    enum CodingKeys: String, CodingKey {
        case t, biometric
        case deviceKeyID = "device_key_id"
        case signature
        case enrollDeviceKey = "enroll_device_key"
    }
}

// MARK: - HTTP client (URLSession async/await, no third party)

/// Simulator builds use a software key (no Secure Enclave): they must NEVER
/// talk to the production backend, where a software signature is
/// server-indistinguishable from a real Face ID proof. Refuse prod hosts
/// at parse time so a misconfigured simulator scheme fails loudly.
func guardSimulatorHost(_ url: URL) throws {
    #if targetEnvironment(simulator)
    let host = (url.host ?? "").lowercased()
    if host == "api.userail.money" || host == "userail.money" {
        throw ConfirmLinkError.notAConfirmURL
    }
    #endif
}

struct ConfirmAPI: Sendable {
    let session: URLSession

    init(session: URLSession = .shared) {
        self.session = session
    }

    func fetch(link: ConfirmLink) async throws -> ConfirmationPayload {
        var comps = URLComponents(
            url: link.baseURL.appendingPathComponent("confirm/\(link.actionID)"),
            resolvingAgainstBaseURL: false
        )!
        comps.queryItems = [URLQueryItem(name: "t", value: link.token)]
        var req = URLRequest(url: comps.url!)
        req.httpMethod = "GET"
        let (data, response) = try await session.data(for: req)
        guard (response as? HTTPURLResponse)?.statusCode == 200 else {
            throw ConfirmAPIError.badStatus((response as? HTTPURLResponse)?.statusCode ?? -1)
        }
        return try JSONDecoder().decode(APIEnvelope.self, from: data).confirmation
    }

    func decide(link: ConfirmLink, approved: Bool, biometric: String, device: ApproveBody) async throws -> ConfirmationPayload {
        let verb = approved ? "approve" : "reject"
        var req = URLRequest(url: link.baseURL.appendingPathComponent("confirm/\(link.actionID)/\(verb)"))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        let payload = ApproveBody(
            t: link.token,
            biometric: biometric,
            deviceKeyID: device.deviceKeyID,
            signature: device.signature,
            enrollDeviceKey: device.enrollDeviceKey
        )
        req.httpBody = try JSONEncoder().encode(payload)
        let (data, response) = try await session.data(for: req)
        let status = (response as? HTTPURLResponse)?.statusCode ?? -1
        // 200 + 422 both carry JSON: 200 = settled/terminal, 422 = refused
        // (bad signature, expired, strict mode). Anything else is transport.
        guard status == 200 || status == 422 else {
            throw ConfirmAPIError.badStatus(status)
        }
        // A 422 body is {"error": ...} — surface it as a typed refusal.
        if status == 422 {
            let msg = (try? JSONDecoder().decode(Refusal.self, from: data).error) ?? "refused"
            throw ConfirmAPIError.refused(msg)
        }
        return try JSONDecoder().decode(APIEnvelope.self, from: data).confirmation
    }
}

struct Refusal: Decodable, Sendable {
    let error: String
}

enum ConfirmAPIError: Error {
    case badStatus(Int)
    case refused(String)
}

// MARK: - Dead-state copy (single source for terminal rendering)

func deadCopy(for payload: ConfirmationPayload) -> String {
    switch payload.state {
    case "expired": return "Expired — ask Miriam again."
    case "rejected": return "Cancelled — nothing moved."
    case "completed": return payload.result ?? "Done."
    case "failed": return "Couldn't complete: \(payload.result ?? "error")"
    default: return "This request is no longer live."
    }
}
