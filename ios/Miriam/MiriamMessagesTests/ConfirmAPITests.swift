import XCTest

// Pure-logic tests: URL parsing, payload decoding, signed-message format.
// No keychain, no biometrics, no network — all runnable in Simulator.
// NOTE: ConfirmAPI.swift is compiled directly into this test bundle (see the
// MiriamMessagesTests target), so no @testable import — an .appex is not a
// linkable module.

final class ConfirmAPITests: XCTestCase {

    func testParseValidLink() throws {
        // Dev host: simulator-safe. Production hosts are refused in
        // simulator builds (software keys must never approve real money).
        let url = URL(string: "https://dev.userail.money/confirm/11112222-3333-4444-5555-666677778888?t=1893456000.abcdef0123456789")!
        let link = try parseConfirmLink(url)
        XCTAssertEqual(link.actionID, "11112222-3333-4444-5555-666677778888")
        XCTAssertEqual(link.token, "1893456000.abcdef0123456789")
        XCTAssertEqual(link.expiryUnix, 1893456000)
        XCTAssertEqual(link.baseURL.absoluteString, "https://dev.userail.money")
    }

    func testParseRefusesProdHostInSimulator() {
        // Simulator builds must never open production confirm links.
        #if targetEnvironment(simulator)
        let url = URL(string: "https://api.userail.money/confirm/11112222-3333-4444-5555-666677778888?t=1893456000.abcdef0123456789")!
        XCTAssertThrowsError(try parseConfirmLink(url))
        #endif
    }

    func testParseRejectsNonConfirmURL() {
        XCTAssertThrowsError(try parseConfirmLink(URL(string: "https://example.com/other/123")!))
        XCTAssertThrowsError(try parseConfirmLink(URL(string: "https://example.com/confirm/")!))
    }

    func testParseRejectsMissingOrMalformedToken() {
        let noToken = URL(string: "https://dev.userail.money/confirm/abc")!
        XCTAssertThrowsError(try parseConfirmLink(noToken))
        let badToken = URL(string: "https://dev.userail.money/confirm/abc?t=garbage")!
        XCTAssertThrowsError(try parseConfirmLink(badToken))
    }

    func testTokenExpiry() {
        XCTAssertEqual(tokenExpiry("1893456000.sig"), 1893456000)
        XCTAssertNil(tokenExpiry("garbage"))
        XCTAssertNil(tokenExpiry(""))
    }

    func testBase64URLRoundTrip() {
        let data = Data([0, 1, 2, 250, 255, 16, 32])
        let encoded = Base64URL.encode(data)
        XCTAssertFalse(encoded.contains("+"))
        XCTAssertFalse(encoded.contains("/"))
        XCTAssertFalse(encoded.contains("="))
        XCTAssertEqual(Base64URL.decode(encoded), data)
        XCTAssertNil(Base64URL.decode("!!!"))
    }

    func testOptionsDecode() throws {
        let json = """
        {"publicKey":{"challenge":"dGVzdA","rpId":"userail.money",
        "allowCredentials":[{"id":"Y3JlZA","transports":["internal"]}],
        "userVerification":"required","timeout":60000}}
        """.data(using: .utf8)!
        let opts = try JSONDecoder().decode(OptionsEnvelope.self, from: json).publicKey
        XCTAssertEqual(opts.rpId, "userail.money")
        XCTAssertEqual(opts.userVerification, "required")
        XCTAssertEqual(opts.allowCredentials?.first?.id, "Y3JlZA")
        XCTAssertEqual(Base64URL.decode(opts.challenge), Data("test".utf8))
    }

    func testNoPasskeyRefusal() throws {
        let json = """
        {"error":"no passkey enrolled","passkey_setup":true}
        """.data(using: .utf8)!
        let refusal = try JSONDecoder().decode(Refusal.self, from: json)
        XCTAssertEqual(refusal.passkeySetup, true)
    }

    func testPayloadDecodeLive() throws {
        let json = """
        {"confirmation":{"action_id":"a1","action":"transfer.send","state":"pending",
        "title":"Send ₦20,000","subtitle":"To Funsho","amount":"₦20,000",
        "expires_at":"2030-01-01T00:00:00Z","result":"","dead":false,"live":true,
        "assurance":"","enrolled_key_id":""}}
        """.data(using: .utf8)!
        let p = try JSONDecoder().decode(APIEnvelope.self, from: json).confirmation
        XCTAssertEqual(p.title, "Send ₦20,000")
        XCTAssertFalse(p.isTerminal)
        XCTAssertFalse(p.dead)
    }

    func testPayloadDecodeDead() throws {
        let json = """
        {"confirmation":{"action_id":"a1","action":"invest.buy","state":"expired",
        "title":"Buy GOOGL","expires_at":"2020-01-01T00:00:00Z","result":"",
        "dead":true,"live":false}}
        """.data(using: .utf8)!
        let p = try JSONDecoder().decode(APIEnvelope.self, from: json).confirmation
        XCTAssertTrue(p.isTerminal)
        XCTAssertNil(p.subtitle)
        XCTAssertEqual(deadCopy(for: p), "Expired — ask Miriam again.")
    }

    func testDeadCopyMatrix() {
        func payload(_ state: String, result: String? = nil) -> ConfirmationPayload {
            ConfirmationPayload(actionID: "x", action: "transfer.send", state: state,
                                title: "T", subtitle: nil, amount: nil, asset: nil,
                                destination: nil, fee: nil, riskLine: nil,
                                expiresAt: nil, result: result, dead: true, live: false,
                                assurance: nil, enrolledKeyID: nil)
        }
        XCTAssertEqual(deadCopy(for: payload("rejected")), "Cancelled — nothing moved.")
        XCTAssertEqual(deadCopy(for: payload("completed", result: "Sent!")), "Sent!")
        XCTAssertEqual(deadCopy(for: payload("failed", result: "boom")), "Couldn't complete: boom")
        XCTAssertEqual(deadCopy(for: payload("weird")), "This request is no longer live.")
    }

    func testSubmissionEncodesWireShape() throws {
        let sub = AssertionSubmission(
            id: "a", rawId: "a", type: "public-key",
            response: AssertionResponse(clientDataJSON: "c", authenticatorData: "d", signature: "s")
        )
        let data = try JSONEncoder().encode(sub)
        let dict = try JSONSerialization.jsonObject(with: data) as! [String: Any]
        XCTAssertEqual(dict["type"] as? String, "public-key")
        let resp = dict["response"] as! [String: Any]
        XCTAssertEqual(resp["signature"] as? String, "s")
        XCTAssertEqual(resp["authenticatorData"] as? String, "d")
    }
}
