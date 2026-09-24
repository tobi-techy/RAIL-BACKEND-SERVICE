import SwiftUI

struct ContentView: View {
    var body: some View {
        NavigationStack {
            List {
                Section("Miriam") {
                    Text("Money approvals live in iMessage. When Miriam stages a payment, investment order, or save-rule change, a live card appears in your transcript — tap it and approve with Face ID.")
                }
                Section("Status") {
                    LabeledContent("Extension", value: Bundle.main.object(forInfoDictionaryKey: "MiriamExtensionBundleID") as? String ?? "—")
                    LabeledContent("Confirm server", value: Bundle.main.object(forInfoDictionaryKey: "MiriamConfirmBaseURL") as? String ?? "—")
                }
                Section("Security") {
                    Text("Approvals are signed in the Secure Enclave. The server never sees your face — only a signature that proves your device authenticated you.")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
            }
            .navigationTitle("Miriam")
        }
    }
}

#Preview {
    ContentView()
}
