import SwiftUI

struct ContentView: View {
    @EnvironmentObject var client: KayakNetClient
    @State private var selectedTab = 0
    
    var body: some View {
        TabView(selection: $selectedTab) {
            HomeView()
                .tabItem {
                    Image(systemName: "house.fill")
                    Text("HOME")
                }
                .tag(0)
            
            ChatView()
                .tabItem {
                    Image(systemName: "message.fill")
                    Text("CHAT")
                }
                .tag(1)
            
            MarketView()
                .tabItem {
                    Image(systemName: "cart.fill")
                    Text("MARKET")
                }
                .tag(2)
            
            DomainsView()
                .tabItem {
                    Image(systemName: "globe")
                    Text("DOMAINS")
                }
                .tag(3)
            
            SettingsView()
                .tabItem {
                    Image(systemName: "gear")
                    Text("SETTINGS")
                }
                .tag(4)
        }
        .accentColor(Color.green)
        .onAppear {
            // Style tab bar
            let appearance = UITabBarAppearance()
            appearance.backgroundColor = UIColor.black
            appearance.stackedLayoutAppearance.normal.iconColor = UIColor(Color.green.opacity(0.5))
            appearance.stackedLayoutAppearance.normal.titleTextAttributes = [.foregroundColor: UIColor(Color.green.opacity(0.5))]
            appearance.stackedLayoutAppearance.selected.iconColor = UIColor(Color.green)
            appearance.stackedLayoutAppearance.selected.titleTextAttributes = [.foregroundColor: UIColor(Color.green)]
            UITabBar.appearance().standardAppearance = appearance
            UITabBar.appearance().scrollEdgeAppearance = appearance
        }
    }
}

#Preview {
    ContentView()
        .environmentObject(KayakNetClient())
}


