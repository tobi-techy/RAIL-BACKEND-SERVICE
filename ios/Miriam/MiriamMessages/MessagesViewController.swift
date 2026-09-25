// MessagesViewController.swift — the actual product half of the Miriam card.
//
// Tapping the live card (customizedMiniApp, live: true) opens THIS extension,
// not Safari, not a webview. Flow: parse url → fetch payload (dead renders
// dead) → compact UI → system passkey sheet (Face ID) → submit assertion →
// render the SERVER verdict. The extension never shows "filled" unless the
// API says completed. The login passkey (same RP ID) is the approval passkey:
// no separate enrollment, iCloud-synced, survives Face ID re-enrollment.
//
// What this file never does: custom PIN sheets, Safari for Face ID, executing
// on card open, or a labeled web button with no passkey ceremony.

import Messages
import UIKit

@MainActor
final class MessagesViewController: MSMessagesAppViewController {

    private enum Screen {
        case loading
        case pending(ConfirmationPayload)
        case setup(String) // no passkey: in-app setup state, approve hidden
        case status(String) // working line, buttons disabled
        case done(String)
        case dead(String)
    }

    private var link: ConfirmLink?
    private var payload: ConfirmationPayload?

    private let api = ConfirmAPI()

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
        case let .pending(p):
            payload = p
            titleLabel.text = p.title
            var lines: [String] = []
            if let s = p.subtitle, !s.isEmpty { lines.append(s) }
            if let a = p.amount, !a.isEmpty { lines.append("Amount: \(a)") }
            if let d = p.destination, !d.isEmpty { lines.append("Where: \(d)") }
            if let f = p.fee, !f.isEmpty { lines.append("Fee: \(f)") }
            detailLabel.text = lines.joined(separator: "\n")
            riskLabel.text = p.riskLine
            approveButton.isEnabled = true
            cancelButton.isEnabled = true
        case let .setup(line):
            titleLabel.text = "Passkey needed"
            detailLabel.text = nil
            riskLabel.text = nil
            statusLabel.text = line
            approveButton.isEnabled = false
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
                render(.pending(p))
            }
        } catch {
            render(.dead("Couldn't load this request. Check connection and retry."))
        }
    }

    // MARK: - Approve (passkey ceremony + Face ID)

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
                    link: link, approved: false, biometric: "cancel"
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
            // 1. Ceremony bound server-side to this card (never consumes it).
            let options: AssertionOptions
            do {
                options = try await api.fetchOptions(link: link)
            } catch ConfirmAPIError.noPasskey {
                render(.setup("This approval needs your Rail passkey — the same one you log in with. Set it up in the Rail app, then come back."))
                return
            }
            // 2. System passkey sheet: one tap + Face ID, userVerification=required.
            guard let anchor = view.window else {
                render(.dead("No window to present the passkey sheet."))
                approveButton.isEnabled = true
                return
            }
            let assertion = try await PasskeyAuth.authorize(options: options, anchor: anchor)
            // 3. Submit: server verifies, consumes the token, executes.
            render(.status("Approved — working on it…"))
            let p = try await api.submitAssertion(link: link, assertion: assertion)
            switch p.state {
            case "completed":
                render(.done(p.result ?? "Done."))
            case "failed":
                render(.dead("Couldn’t complete: \(p.result ?? "error")"))
            case "rejected", "expired":
                render(.dead(deadCopy(for: p)))
            default:
                // Server accepted but still working: poll once via fetch.
                try? await Task.sleep(nanoseconds: 2_000_000_000)
                await load()
            }
        } catch PasskeyError.cancelled {
            render(.status("Ready — tap Approve with Face ID when ready."))
            approveButton.isEnabled = true
        } catch ConfirmAPIError.refused(let msg) {
            render(.dead(msg))
        } catch {
            render(.dead("Something went wrong. Nothing moved unless the card says Done."))
            approveButton.isEnabled = true
        }
    }
}
