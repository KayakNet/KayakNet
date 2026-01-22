import SwiftUI

struct SettingsView: View {
    @EnvironmentObject var client: KayakNetClient
    @State private var nickname = ""
    @State private var showAbout = false
    
    var body: some View {
        NavigationView {
            ZStack {
                Color.black.ignoresSafeArea()
                
                ScrollView {
                    VStack(spacing: 20) {
                        // Header
                        HStack {
                            Text("SETTINGS")
                                .font(.system(size: 20, weight: .bold, design: .monospaced))
                                .foregroundColor(.green)
                            Spacer()
                        }
                        .padding(.horizontal)
                        
                        // Profile Section
                        VStack(alignment: .leading, spacing: 12) {
                            Text("// PROFILE")
                                .font(.system(size: 12, weight: .bold, design: .monospaced))
                                .foregroundColor(.green.opacity(0.5))
                            
                            VStack(alignment: .leading, spacing: 4) {
                                Text("NICKNAME")
                                    .font(.system(size: 10, design: .monospaced))
                                    .foregroundColor(.green.opacity(0.7))
                                
                                HStack {
                                    TextField("Anonymous", text: $nickname)
                                        .font(.system(size: 14, design: .monospaced))
                                        .foregroundColor(.green)
                                    
                                    Button(action: saveNickname) {
                                        Text("SAVE")
                                            .font(.system(size: 12, weight: .bold, design: .monospaced))
                                            .foregroundColor(.black)
                                            .padding(.horizontal, 12)
                                            .padding(.vertical, 8)
                                            .background(Color.green)
                                            .cornerRadius(4)
                                    }
                                }
                                .padding()
                                .background(Color.green.opacity(0.05))
                                .cornerRadius(8)
                                .overlay(
                                    RoundedRectangle(cornerRadius: 8)
                                        .stroke(Color.green.opacity(0.3), lineWidth: 1)
                                )
                            }
                        }
                        .padding()
                        .background(Color.green.opacity(0.02))
                        .cornerRadius(8)
                        
                        // Connection Section
                        VStack(alignment: .leading, spacing: 12) {
                            Text("// CONNECTION")
                                .font(.system(size: 12, weight: .bold, design: .monospaced))
                                .foregroundColor(.green.opacity(0.5))
                            
                            HStack {
                                Text("Status")
                                    .font(.system(size: 14, design: .monospaced))
                                    .foregroundColor(.green)
                                
                                Spacer()
                                
                                HStack(spacing: 8) {
                                    Circle()
                                        .fill(client.connectionState == .connected ? Color.green : Color.yellow)
                                        .frame(width: 8, height: 8)
                                    
                                    Text(connectionStatusText)
                                        .font(.system(size: 14, design: .monospaced))
                                        .foregroundColor(client.connectionState == .connected ? .green : .yellow)
                                }
                            }
                            .padding()
                            .background(Color.green.opacity(0.05))
                            .cornerRadius(8)
                            
                            HStack {
                                Text("Bootstrap")
                                    .font(.system(size: 14, design: .monospaced))
                                    .foregroundColor(.green)
                                
                                Spacer()
                                
                                Text("203.161.33.237:8080")
                                    .font(.system(size: 12, design: .monospaced))
                                    .foregroundColor(.cyan)
                            }
                            .padding()
                            .background(Color.green.opacity(0.05))
                            .cornerRadius(8)
                        }
                        .padding()
                        .background(Color.green.opacity(0.02))
                        .cornerRadius(8)
                        
                        // Stats Section
                        VStack(alignment: .leading, spacing: 12) {
                            Text("// STATISTICS")
                                .font(.system(size: 12, weight: .bold, design: .monospaced))
                                .foregroundColor(.green.opacity(0.5))
                            
                            StatRow(label: "Listings", value: "\(client.listings.count)")
                            StatRow(label: "Chat Rooms", value: "\(client.chatRooms.count)")
                            StatRow(label: "Domains", value: "\(client.domains.count)")
                            StatRow(label: "Active Orders", value: "\(client.myEscrows.count)")
                        }
                        .padding()
                        .background(Color.green.opacity(0.02))
                        .cornerRadius(8)
                        
                        // About Section
                        Button(action: { showAbout = true }) {
                            HStack {
                                Text("About KayakNet")
                                    .font(.system(size: 14, design: .monospaced))
                                    .foregroundColor(.green)
                                
                                Spacer()
                                
                                Image(systemName: "chevron.right")
                                    .foregroundColor(.green.opacity(0.5))
                            }
                            .padding()
                            .background(Color.green.opacity(0.05))
                            .cornerRadius(8)
                        }
                        
                        Spacer()
                    }
                    .padding()
                }
            }
            .navigationBarHidden(true)
            .onAppear {
                nickname = client.localNick
            }
            .sheet(isPresented: $showAbout) {
                AboutView()
            }
        }
    }
    
    var connectionStatusText: String {
        switch client.connectionState {
        case .disconnected: return "Disconnected"
        case .connecting: return "Connecting..."
        case .connected: return "Connected"
        }
    }
    
    func saveNickname() {
        client.setLocalNick(nickname)
    }
}

struct StatRow: View {
    let label: String
    let value: String
    
    var body: some View {
        HStack {
            Text(label)
                .font(.system(size: 14, design: .monospaced))
                .foregroundColor(.green)
            
            Spacer()
            
            Text(value)
                .font(.system(size: 14, weight: .bold, design: .monospaced))
                .foregroundColor(.cyan)
        }
        .padding()
        .background(Color.green.opacity(0.05))
        .cornerRadius(8)
    }
}

struct AboutView: View {
    @Environment(\.dismiss) var dismiss
    
    var body: some View {
        NavigationView {
            ZStack {
                Color.black.ignoresSafeArea()
                
                ScrollView {
                    VStack(spacing: 20) {
                        Image(systemName: "network")
                            .font(.system(size: 64))
                            .foregroundColor(.green)
                        
                        Text("KAYAKNET")
                            .font(.system(size: 28, weight: .bold, design: .monospaced))
                            .foregroundColor(.green)
                        
                        Text("Privacy-First Anonymous P2P Network")
                            .font(.system(size: 14, design: .monospaced))
                            .foregroundColor(.green.opacity(0.7))
                        
                        Text("v1.0.0")
                            .font(.system(size: 12, design: .monospaced))
                            .foregroundColor(.green.opacity(0.5))
                        
                        VStack(alignment: .leading, spacing: 12) {
                            FeatureItem(icon: "shield.fill", title: "End-to-End Encryption", description: "All communications are encrypted")
                            FeatureItem(icon: "arrow.triangle.branch", title: "Onion Routing", description: "3-hop routing for anonymity")
                            FeatureItem(icon: "cart.fill", title: "Marketplace", description: "Escrow with Monero & Zcash")
                            FeatureItem(icon: "message.fill", title: "Anonymous Chat", description: "Encrypted chat rooms")
                            FeatureItem(icon: "globe", title: "Domain System", description: ".kyk decentralized domains")
                        }
                        .padding()
                        .background(Color.green.opacity(0.05))
                        .cornerRadius(8)
                        
                        Text("Built for privacy. Built for freedom.")
                            .font(.system(size: 12, design: .monospaced))
                            .foregroundColor(.green.opacity(0.5))
                            .padding(.top)
                    }
                    .padding()
                }
            }
            .navigationTitle("About")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .navigationBarTrailing) {
                    Button("Done") { dismiss() }
                        .foregroundColor(.green)
                }
            }
        }
    }
}

struct FeatureItem: View {
    let icon: String
    let title: String
    let description: String
    
    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: icon)
                .font(.system(size: 20))
                .foregroundColor(.green)
                .frame(width: 24)
            
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(.system(size: 14, weight: .bold, design: .monospaced))
                    .foregroundColor(.green)
                Text(description)
                    .font(.system(size: 12, design: .monospaced))
                    .foregroundColor(.green.opacity(0.7))
            }
        }
    }
}

#Preview {
    SettingsView()
        .environmentObject(KayakNetClient())
}



