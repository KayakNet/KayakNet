import SwiftUI

struct DomainsView: View {
    @EnvironmentObject var client: KayakNetClient
    @State private var searchText = ""
    @State private var showRegister = false
    
    var filteredDomains: [Domain] {
        if searchText.isEmpty {
            return client.domains
        }
        return client.domains.filter {
            $0.name.localizedCaseInsensitiveContains(searchText)
        }
    }
    
    var body: some View {
        NavigationView {
            ZStack {
                Color.black.ignoresSafeArea()
                
                VStack(spacing: 0) {
                    // Header
                    HStack {
                        Text("DOMAINS")
                            .font(.system(size: 20, weight: .bold, design: .monospaced))
                            .foregroundColor(.green)
                        
                        Spacer()
                        
                        Button(action: { showRegister = true }) {
                            Image(systemName: "plus")
                                .foregroundColor(.green)
                        }
                    }
                    .padding()
                    
                    // Search
                    HStack {
                        Image(systemName: "magnifyingglass")
                            .foregroundColor(.green.opacity(0.5))
                        TextField("Search domains...", text: $searchText)
                            .font(.system(size: 14, design: .monospaced))
                            .foregroundColor(.green)
                    }
                    .padding(12)
                    .background(Color.green.opacity(0.05))
                    .cornerRadius(8)
                    .overlay(
                        RoundedRectangle(cornerRadius: 8)
                            .stroke(Color.green.opacity(0.3), lineWidth: 1)
                    )
                    .padding(.horizontal)
                    
                    // Domains List
                    ScrollView {
                        LazyVStack(spacing: 12) {
                            ForEach(filteredDomains) { domain in
                                DomainCard(domain: domain)
                            }
                        }
                        .padding()
                    }
                }
            }
            .navigationBarHidden(true)
            .sheet(isPresented: $showRegister) {
                RegisterDomainView()
                    .environmentObject(client)
            }
        }
    }
}

struct DomainCard: View {
    let domain: Domain
    
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Image(systemName: "globe")
                    .foregroundColor(.green)
                
                Text(domain.name)
                    .font(.system(size: 16, weight: .bold, design: .monospaced))
                    .foregroundColor(.green)
                
                Text(".kyk")
                    .font(.system(size: 16, design: .monospaced))
                    .foregroundColor(.green.opacity(0.5))
                
                Spacer()
            }
            
            HStack {
                Text("→")
                    .foregroundColor(.cyan)
                Text(domain.target)
                    .font(.system(size: 12, design: .monospaced))
                    .foregroundColor(.cyan)
                    .lineLimit(1)
            }
            
            HStack {
                Text("Owner: \(domain.owner.prefix(12))...")
                    .font(.system(size: 10, design: .monospaced))
                    .foregroundColor(.green.opacity(0.5))
                
                Spacer()
                
                if let expires = domain.expiresAt {
                    Text("Expires: \(expires.prefix(10))")
                        .font(.system(size: 10, design: .monospaced))
                        .foregroundColor(.green.opacity(0.5))
                }
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

struct RegisterDomainView: View {
    @EnvironmentObject var client: KayakNetClient
    @Environment(\.dismiss) var dismiss
    
    @State private var name = ""
    @State private var target = ""
    @State private var isRegistering = false
    @State private var errorMessage: String?
    
    var body: some View {
        NavigationView {
            ZStack {
                Color.black.ignoresSafeArea()
                
                VStack(spacing: 20) {
                    VStack(alignment: .leading, spacing: 4) {
                        Text("DOMAIN NAME")
                            .font(.system(size: 10, design: .monospaced))
                            .foregroundColor(.green.opacity(0.7))
                        
                        HStack {
                            TextField("mydomain", text: $name)
                                .font(.system(size: 16, design: .monospaced))
                                .foregroundColor(.green)
                                .textInputAutocapitalization(.never)
                                .autocorrectionDisabled()
                            
                            Text(".kyk")
                                .font(.system(size: 16, design: .monospaced))
                                .foregroundColor(.green.opacity(0.5))
                        }
                        .padding()
                        .background(Color.green.opacity(0.05))
                        .cornerRadius(8)
                        .overlay(
                            RoundedRectangle(cornerRadius: 8)
                                .stroke(Color.green.opacity(0.3), lineWidth: 1)
                        )
                    }
                    
                    VStack(alignment: .leading, spacing: 4) {
                        Text("TARGET (Node ID or URL)")
                            .font(.system(size: 10, design: .monospaced))
                            .foregroundColor(.green.opacity(0.7))
                        
                        TextField("node_id or URL", text: $target)
                            .font(.system(size: 14, design: .monospaced))
                            .foregroundColor(.green)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                            .padding()
                            .background(Color.green.opacity(0.05))
                            .cornerRadius(8)
                            .overlay(
                                RoundedRectangle(cornerRadius: 8)
                                    .stroke(Color.green.opacity(0.3), lineWidth: 1)
                            )
                    }
                    
                    if let error = errorMessage {
                        Text(error)
                            .font(.system(size: 12, design: .monospaced))
                            .foregroundColor(.red)
                    }
                    
                    Button(action: registerDomain) {
                        if isRegistering {
                            ProgressView()
                                .progressViewStyle(CircularProgressViewStyle(tint: .black))
                        } else {
                            Text("REGISTER DOMAIN")
                                .font(.system(size: 16, weight: .bold, design: .monospaced))
                        }
                    }
                    .foregroundColor(.black)
                    .frame(maxWidth: .infinity)
                    .padding()
                    .background(canRegister ? Color.green : Color.gray)
                    .cornerRadius(8)
                    .disabled(!canRegister || isRegistering)
                    
                    Spacer()
                }
                .padding()
            }
            .navigationTitle("Register Domain")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .navigationBarLeading) {
                    Button("Cancel") { dismiss() }
                        .foregroundColor(.green)
                }
            }
        }
    }
    
    var canRegister: Bool {
        !name.isEmpty && !target.isEmpty
    }
    
    func registerDomain() {
        isRegistering = true
        errorMessage = nil
        
        Task {
            let success = await client.registerDomain(name: name, target: target)
            
            await MainActor.run {
                isRegistering = false
                if success {
                    dismiss()
                } else {
                    errorMessage = "Failed to register domain"
                }
            }
        }
    }
}

#Preview {
    DomainsView()
        .environmentObject(KayakNetClient())
}


