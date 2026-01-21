import SwiftUI

struct HomeView: View {
    @EnvironmentObject var client: KayakNetClient
    
    var body: some View {
        NavigationView {
            ZStack {
                Color.black.ignoresSafeArea()
                
                ScrollView {
                    VStack(spacing: 20) {
                        // Header
                        HStack {
                            Text("KAYAKNET")
                                .font(.system(size: 24, weight: .bold, design: .monospaced))
                                .foregroundColor(.green)
                            
                            Circle()
                                .fill(client.connectionState == .connected ? Color.green : Color.yellow)
                                .frame(width: 8, height: 8)
                            
                            Spacer()
                            
                            Text(client.connectionState == .connected ? "[ON]" : "[...]")
                                .font(.system(size: 14, design: .monospaced))
                                .foregroundColor(.green)
                        }
                        .padding()
                        
                        // Stats Cards
                        LazyVGrid(columns: [
                            GridItem(.flexible()),
                            GridItem(.flexible())
                        ], spacing: 16) {
                            StatCard(title: "LISTINGS", value: "\(client.listings.count)", icon: "cart.fill")
                            StatCard(title: "DOMAINS", value: "\(client.domains.count)", icon: "globe")
                            StatCard(title: "CHAT ROOMS", value: "\(client.chatRooms.count)", icon: "message.fill")
                            StatCard(title: "ORDERS", value: "\(client.myEscrows.count)", icon: "lock.fill")
                        }
                        .padding(.horizontal)
                        
                        // Welcome Box
                        VStack(alignment: .leading, spacing: 12) {
                            Text("// WELCOME TO KAYAKNET")
                                .font(.system(size: 16, weight: .bold, design: .monospaced))
                                .foregroundColor(.green)
                            
                            Text("A privacy-first anonymous P2P network with:")
                                .font(.system(size: 12, design: .monospaced))
                                .foregroundColor(.green.opacity(0.8))
                            
                            VStack(alignment: .leading, spacing: 8) {
                                FeatureRow(icon: "shield.fill", text: "End-to-end encryption")
                                FeatureRow(icon: "arrow.triangle.branch", text: "3-hop onion routing")
                                FeatureRow(icon: "cart.fill", text: "Marketplace with escrow")
                                FeatureRow(icon: "message.fill", text: "Anonymous chat")
                                FeatureRow(icon: "globe", text: ".kyk domain system")
                            }
                        }
                        .padding()
                        .background(Color.green.opacity(0.1))
                        .cornerRadius(8)
                        .overlay(
                            RoundedRectangle(cornerRadius: 8)
                                .stroke(Color.green.opacity(0.3), lineWidth: 1)
                        )
                        .padding(.horizontal)
                        
                        Spacer()
                    }
                }
            }
            .navigationBarHidden(true)
        }
    }
}

struct StatCard: View {
    let title: String
    let value: String
    let icon: String
    
    var body: some View {
        VStack(spacing: 8) {
            Image(systemName: icon)
                .font(.system(size: 24))
                .foregroundColor(.green)
            
            Text(value)
                .font(.system(size: 28, weight: .bold, design: .monospaced))
                .foregroundColor(.green)
            
            Text(title)
                .font(.system(size: 10, design: .monospaced))
                .foregroundColor(.green.opacity(0.6))
        }
        .frame(maxWidth: .infinity)
        .padding()
        .background(Color.green.opacity(0.05))
        .cornerRadius(8)
        .overlay(
            RoundedRectangle(cornerRadius: 8)
                .stroke(Color.green.opacity(0.3), lineWidth: 1)
        )
    }
}

struct FeatureRow: View {
    let icon: String
    let text: String
    
    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: icon)
                .font(.system(size: 12))
                .foregroundColor(.green)
            Text(text)
                .font(.system(size: 12, design: .monospaced))
                .foregroundColor(.green.opacity(0.8))
        }
    }
}

#Preview {
    HomeView()
        .environmentObject(KayakNetClient())
}


