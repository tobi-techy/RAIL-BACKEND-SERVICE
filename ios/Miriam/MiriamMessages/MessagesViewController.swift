// MessagesViewController.swift — the actual product half of the Miriam card.
//
// Tapping the live card (customizedMiniApp, live: true) opens THIS extension,
// not Safari, not a webview. Flow: parse url → fetch payload (dead renders
// dead) → compact UI → Face ID via the Secure Enclave key → approve POST →
// render the SERVER verdict. The extension never shows "filled" unless the
// API says completed.
//
// What this file never does: custom PIN sheets (biometrics-only in v1),
// Safari for Face ID, executing on card open, or a labeled web button with
// no LocalAuthentication.

import Messages
import UIKit

@MainActor
final class MessagesViewController: MSMessagesAppViewController {

    private enum Screen {
        case loading
        case pending(ConfirmationPayload, Bool) // payload, simulatorSoftwareKey
        case status(String) // working line, buttons disabled
        case done(String)
        case dead(String)
    }

    private var link: ConfirmLink?
    private var payload: ConfirmationPayload?
    private var retriedReenroll = false

    private let api = ConfirmAPI()
    private let gate = BiometricGate()

    private let titleLabel = UILabel()
    private let detailLabel = UILabel()
    private let riskLabel = UILabel()
    private let statusLabel = UILabel()
    private let approveButton = UIButton(type: .system)
    private let cancelButton = UIButton(type: .system)
    private let stack = UIStackView()

    // MARK: - Lifecycle

    override func viewDidLoad() {
        super.viewDidLoad()
        buildUI()
    }

    override func willBecomeActive(with conversation: MSConversation) {
        super.willBecomeActive(with: conversation)
        retriedReenroll = false
        guard let url = conversation.selectedMessage?.url else {
            render(.dead("No action link."))
            return
        }
        do {
            link = try parseConfirmLink(url)
        } catch {
            render(.dead("This link is invalid."))
            return
        }
        render(.loading)
        Task { await load() }
    }

    // MARK: - UI (programmatic, compact presentation)

    private func buildUI() {
        view.backgroundColor = .systemBackground
        stack.axis = .vertical
        stack.spacing = 10
        stack.alignment = .fill
        stack.translatesAutoresizingMaskIntoConstraints = false

        titleLabel.font = .preferredFont(forTextStyle: .headline)
        titleLabel.numberOfLines = 2
        detailLabel.font = .preferredFont(forTextStyle: .subheadline)
        detailLabel.numberOfLines = 0
        riskLabel.font = .preferredFont(forTextStyle: .footnote)
        riskLabel.textColor = .secondaryLabel
        riskLabel.numberOfLines = 0
        statusLabel.font = .preferredFont(forTextStyle: .footnote)
        statusLabel.textColor = .secondaryLabel
        statusLabel.numberOfLines = 0

        approveButton.setTitle("Approve with Face ID", for: .normal)
        approveButton.titleLabel?.font = .preferredFont(forTextStyle: .headline)
        approveButton.backgroundColor = .systemBlue
        approveButton.setTitleColor(.white, for: .normal)
        approveButton.setTitleColor(.systemGray, for: .disabled)
        approveButton.layer.cornerRadius = 12
        approveButton.contentEdgeInsets = UIEdgeInsets(top: 12, left: 16, bottom: 12, right: 16)
        approveButton.addTarget(self, action: #selector(didTapApprove), for: .touchUpInside)
        approveButton.accessibilityLabel = "Approve with Face ID"

        cancelButton.setTitle("Cancel", for: .normal)
        cancelButton.addTarget(self, action: #selector(didTapCancel), for: .touchUpInside)

        for v in [titleLabel, detailLabel, riskLabel, approveButton, cancelButton, statusLabel] as [UIView] {
            stack.addArrangedSubview(v)
        }
        view.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: 16),
            stack.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -16),
            stack.topAnchor.constraint(equalTo: view.topAnchor, constant: 12),
            stack.bottomAnchor.constraint(lessThanOrEqualTo: view.bottomAnchor, constant: -12),
        ])
    }

    private func render(_ screen: Screen) {
        statusLabel.text = nil
        switch screen {
        case .loading:
            titleLabel.text = "Loading…"
            detailLabel.text = nil
            riskLabel.text = nil
            approveButton.isEnabled = false
            cancelButton.isEnabled = false
        case let .pending(p, softwareKey):
            payload = p
            titleLabel.text = p.title
            var lines: [String] = []
            if let s = p.subtitle, !s.isEmpty { lines.append(s) }
            if let a = p.amount, !a.isEmpty { lines.append("Amount: \(a)") }
            if let d = p.destination, !d.isEmpty { lines.append("Where: \(d)") }
            if let f = p.fee, !f.isEmpty { lines.append("Fee: \(f)") }
            detailLabel.text = lines.joined(separator: "\n")
            riskLabel.text = p.riskLine
            if softwareKey {
                statusLabel.text = "Simulator build — software key, never for real money."
            }
            approveButton.isEnabled = true
            cancelButton.isEnabled = true
        case let .status(line):
            statusLabel.text = line
            approveButton.isEnabled = false
            cancelButton.isEnabled = true
        case let .done(line):
            statusLabel.text = line
            approveButton.isEnabled = false
            cancelButton.isEnabled = false
        case let .dead(line):
            titleLabel.text = "No longer live"
            detailLabel.text = nil
            riskLabel.text = nil
            statusLabel.text = line
            approveButton.isEnabled = false
            cancelButton.isEnabled = false
        }
    }

    // MARK: - Load

    private func load() async {
        guard let link else { return }
        do {
            let p = try await api.fetch(link: link)
            if p.dead || !p.live || p.isTerminal {
                render(.dead(deadCopy(for: p)))
            } else {
                render(.pending(p, !DeviceKey.isSecureEnclaveAvailable))
            }
        } catch {
            render(.dead("Couldn't load this request. Check connection and retry."))
        }
    }

    // MARK: - Approve (the real Face ID call)

    @objc private func didTapApprove() {
        guard link != nil else { return }
        render(.status("Confirming…"))
        Task { await approveOnce() }
    }

    @objc private func didTapCancel() {
        guard let link else { return }
        render(.status("Cancelling…"))
        Task {
            do {
                let p = try await api.decide(
                    link: link, approved: false, biometric: "cancel",
                    device: ApproveBody(t: "", biometric: "", deviceKeyID: nil, signature: nil, enrollDeviceKey: nil)
                )
                render(.dead(deadCopy(for: p)))
            } catch {
                render(.dead("Cancelled — nothing moved."))
            }
        }
    }

    private func approveOnce() async {
        guard let link else { return }
        do {
            let device = try await biometricProof(for: link)
            let p = try await api.decide(
                link: link, approved: true, biometric: "pass", device: device
            )
            if let enrolled = p.enrolledKeyID, !enrolled.isEmpty {
                KeychainRef.set(enrolled, forKey: Self.enrolledKeyIDDefaultsKey)
            }
            switch p.state {
            case "completed":
                render(.done(p.result ?? "Done."))
            case "failed":
                render(.dead("Couldn't complete: \(p.result ?? "error")"))
            case "rejected", "expired":
                render(.dead(deadCopy(for: p)))
            default:
                // Server accepted but still working: poll once via fetch.
                render(.status("Approved — working on it…"))
                try? await Task.sleep(nanoseconds: 2_000_000_000)
                await load()
            }
        } catch ConfirmAPIError.refused(let msg) {
            // Unknown device key (biometry changed) → delete, clear, re-enroll ONCE.
            if !retriedReenroll, msg.lowercased().contains("unknown device key") {
                retriedReenroll = true
                DeviceKey.deleteKey()
                KeychainRef.remove(forKey: Self.enrolledKeyIDDefaultsKey)
                render(.status("Face changed — confirming again…"))
                await approveOnce()
            } else {
                render(.dead(msg))
            }
        } catch {
            render(.dead("Something went wrong. Nothing moved unless the card says Done."))
            approveButton.isEnabled = true
        }
    }

    // The single Face ID prompt per approval lives inside DeviceKey.sign
    // (Enclave access control). The explicit prompt below runs ONLY on first
    // enrollment, where no key exists to sign with yet.
    private func biometricProof(for link: ConfirmLink) async throws -> ApproveBody {
        guard gate.canEvaluateBiometrics() || !DeviceKey.isSecureEnclaveAvailable else {
            throw ConfirmAPIError.refused("Face ID isn't available on this device.")
        }
        let knownKeyID = KeychainRef.string(forKey: Self.enrolledKeyIDDefaultsKey)
        if let knownKeyID, (try? DeviceKey.existing()) != nil {
            let message = signedMessage(actionID: link.actionID, expiryUnix: link.expiryUnix)
            let sig = try DeviceKey.sign(message: message)
            return ApproveBody(
                // "pass": Face ID just ran — via Enclave access control inside
                // sign(). The server rejects an empty biometric whenever
                // device material is attached (fail-closed).
                t: "", biometric: "pass",
                deviceKeyID: knownKeyID,
                signature: sig.base64EncodedString(),
                enrollDeviceKey: nil
            )
        }
        // First approval from this device: explicit Face ID, then enroll.
        // The biometric verdict here is client-asserted; the server binds the
        // key to the single-use token and audits the enrollment.
        let title = payload?.title ?? "Approve this action"
        let ok = try await gate.authenticate(reason: title)
        guard ok else { throw ConfirmAPIError.refused("Face ID was cancelled.") }
        let spki = try DeviceKey.publicKeySPKI()
        return ApproveBody(
            t: "", biometric: "pass",
            deviceKeyID: nil, signature: nil,
            enrollDeviceKey: spki.base64EncodedString()
        )
    }

    private static let enrolledKeyIDDefaultsKey = "miriam.enrolledDeviceKeyID"
}
