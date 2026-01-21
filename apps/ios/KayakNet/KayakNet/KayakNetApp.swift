import SwiftUI

@main
struct KayakNetApp: App {
    @StateObject private var networkClient = KayakNetClient()
    
    var body: some Scene {
        WindowGroup {
            ContentView()
                .environmentObject(networkClient)
                .preferredColorScheme(.dark)
        }
    }
}


