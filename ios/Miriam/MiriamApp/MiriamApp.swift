import SwiftUI

// Miriam container app. iMessage extensions must live inside a host iOS app;
// this host is intentionally thin — the product surface is the Messages
// extension (Face ID approval cards in the iMessage transcript).

@main
struct MiriamApp: App {
    var body: some Scene {
        WindowGroup {
            ContentView()
        }
    }
}
