import Foundation
import Combine

enum ConnectionState {
    case disconnected
    case connecting
    case connected
}

class KayakNetClient: ObservableObject {
    @Published var connectionState: ConnectionState = .disconnected
    @Published var listings: [Listing] = []
    @Published var myEscrows: [Escrow] = []
    @Published var chatMessages: [String: [ChatMessage]] = [:]
    @Published var chatRooms: [ChatRoom] = []
    @Published var domains: [Domain] = []
    @Published var peers: [Peer] = []
    @Published var localNick: String = "Anonymous"
    
    private let bootstrapHost = "203.161.33.237"
    private let bootstrapPort = 8080
    private var nodeId: String = ""
    
    private var baseURL: String {
        "http://\(bootstrapHost):\(bootstrapPort)"
    }
    
    init() {
        loadOrCreateNodeId()
        connect()
    }
    
    private func loadOrCreateNodeId() {
        if let savedId = UserDefaults.standard.string(forKey: "node_id") {
            nodeId = savedId
        } else {
            // Generate random node ID
            nodeId = UUID().uuidString.replacingOccurrences(of: "-", with: "").lowercased()
            UserDefaults.standard.set(nodeId, forKey: "node_id")
        }
    }
    
    func connect() {
        connectionState = .connecting
        
        // Start periodic sync
        Task {
            while true {
                await fetchAll()
                try? await Task.sleep(nanoseconds: 10_000_000_000) // 10 seconds
            }
        }
    }
    
    func fetchAll() async {
        await fetchListings()
        await fetchChatRooms()
        await fetchMyEscrows()
        await fetchDomains()
        
        await MainActor.run {
            connectionState = .connected
        }
    }
    
    // MARK: - Listings
    
    func fetchListings() async {
        guard let url = URL(string: "\(baseURL)/api/listings") else { return }
        
        do {
            let (data, _) = try await URLSession.shared.data(from: url)
            let decoded = try JSONDecoder().decode([Listing].self, from: data)
            await MainActor.run {
                self.listings = decoded
            }
        } catch {
            print("fetchListings error: \(error)")
        }
    }
    
    func createListing(title: String, description: String, price: Double, currency: String, category: String, xmrAddress: String?, zecAddress: String?) async -> Bool {
        guard let url = URL(string: "\(baseURL)/api/create-listing") else { return false }
        
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/x-www-form-urlencoded", forHTTPHeaderField: "Content-Type")
        
        var params = "title=\(title.urlEncoded)&description=\(description.urlEncoded)&price=\(price)&currency=\(currency)&category=\(category)&seller_id=\(nodeId)"
        if let xmr = xmrAddress { params += "&seller_xmr_address=\(xmr.urlEncoded)" }
        if let zec = zecAddress { params += "&seller_zec_address=\(zec.urlEncoded)" }
        
        request.httpBody = params.data(using: .utf8)
        
        do {
            let (_, response) = try await URLSession.shared.data(for: request)
            if let httpResponse = response as? HTTPURLResponse, httpResponse.statusCode == 200 {
                await fetchListings()
                return true
            }
        } catch {
            print("createListing error: \(error)")
        }
        return false
    }
    
    // MARK: - Escrow
    
    func createEscrow(listingId: String, currency: String, refundAddress: String, deliveryInfo: String) async -> Escrow? {
        guard let url = URL(string: "\(baseURL)/api/escrow/create") else { return nil }
        guard let listing = listings.first(where: { $0.id == listingId }) else { return nil }
        
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/x-www-form-urlencoded", forHTTPHeaderField: "Content-Type")
        
        let sellerAddress = currency == "ZEC" ? (listing.sellerZecAddress ?? "") : (listing.sellerXmrAddress ?? "")
        
        let params = "listing_id=\(listingId)&currency=\(currency)&buyer=\(nodeId)&buyer_address=\(refundAddress.urlEncoded)&delivery_info=\(deliveryInfo.urlEncoded)&seller_id=\(listing.seller)&listing_title=\(listing.title.urlEncoded)&amount=\(listing.price)&seller_address=\(sellerAddress.urlEncoded)"
        
        request.httpBody = params.data(using: .utf8)
        
        do {
            let (data, response) = try await URLSession.shared.data(for: request)
            if let httpResponse = response as? HTTPURLResponse, httpResponse.statusCode == 200 {
                let escrow = try JSONDecoder().decode(Escrow.self, from: data)
                await fetchMyEscrows()
                return escrow
            }
        } catch {
            print("createEscrow error: \(error)")
        }
        return nil
    }
    
    func fetchMyEscrows() async {
        // Fetch buyer escrows
        var allEscrows: [Escrow] = []
        
        if let url = URL(string: "\(baseURL)/api/escrow/my?node_id=\(nodeId)&role=buyer") {
            do {
                let (data, _) = try await URLSession.shared.data(from: url)
                var escrows = try JSONDecoder().decode([Escrow].self, from: data)
                escrows = escrows.map { var e = $0; e.isBuyer = true; return e }
                allEscrows.append(contentsOf: escrows)
            } catch {
                print("fetchMyEscrows buyer error: \(error)")
            }
        }
        
        // Fetch seller escrows
        if let url = URL(string: "\(baseURL)/api/escrow/my?node_id=\(nodeId)&role=seller") {
            do {
                let (data, _) = try await URLSession.shared.data(from: url)
                var escrows = try JSONDecoder().decode([Escrow].self, from: data)
                escrows = escrows.map { var e = $0; e.isSeller = true; return e }
                for escrow in escrows {
                    if !allEscrows.contains(where: { $0.escrowId == escrow.escrowId }) {
                        allEscrows.append(escrow)
                    }
                }
            } catch {
                print("fetchMyEscrows seller error: \(error)")
            }
        }
        
        await MainActor.run {
            self.myEscrows = allEscrows
        }
    }
    
    func markEscrowShipped(escrowId: String, trackingInfo: String) async -> Bool {
        guard let url = URL(string: "\(baseURL)/api/escrow/ship") else { return false }
        
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/x-www-form-urlencoded", forHTTPHeaderField: "Content-Type")
        request.httpBody = "escrow_id=\(escrowId)&tracking_info=\(trackingInfo.urlEncoded)".data(using: .utf8)
        
        do {
            let (_, response) = try await URLSession.shared.data(for: request)
            if let httpResponse = response as? HTTPURLResponse, httpResponse.statusCode == 200 {
                await fetchMyEscrows()
                return true
            }
        } catch {
            print("markEscrowShipped error: \(error)")
        }
        return false
    }
    
    func confirmEscrowReceived(escrowId: String) async -> Bool {
        guard let url = URL(string: "\(baseURL)/api/escrow/release") else { return false }
        
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/x-www-form-urlencoded", forHTTPHeaderField: "Content-Type")
        request.httpBody = "escrow_id=\(escrowId)".data(using: .utf8)
        
        do {
            let (_, response) = try await URLSession.shared.data(for: request)
            if let httpResponse = response as? HTTPURLResponse, httpResponse.statusCode == 200 {
                await fetchMyEscrows()
                return true
            }
        } catch {
            print("confirmEscrowReceived error: \(error)")
        }
        return false
    }
    
    func manualConfirmPayment(escrowId: String, txId: String) async -> Bool {
        guard let url = URL(string: "\(baseURL)/api/escrow/manual-confirm") else { return false }
        
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/x-www-form-urlencoded", forHTTPHeaderField: "Content-Type")
        request.httpBody = "escrow_id=\(escrowId)&tx_id=\(txId.urlEncoded)".data(using: .utf8)
        
        do {
            let (_, response) = try await URLSession.shared.data(for: request)
            if let httpResponse = response as? HTTPURLResponse, httpResponse.statusCode == 200 {
                await fetchMyEscrows()
                return true
            }
        } catch {
            print("manualConfirmPayment error: \(error)")
        }
        return false
    }
    
    // MARK: - Chat
    
    func fetchChatRooms() async {
        guard let url = URL(string: "\(baseURL)/api/chat/rooms") else { return }
        
        do {
            let (data, _) = try await URLSession.shared.data(from: url)
            let decoded = try JSONDecoder().decode([ChatRoom].self, from: data)
            await MainActor.run {
                self.chatRooms = decoded
            }
        } catch {
            print("fetchChatRooms error: \(error)")
        }
    }
    
    func fetchChatMessages(room: String) async {
        guard let url = URL(string: "\(baseURL)/api/chat?room=\(room.urlEncoded)") else { return }
        
        do {
            let (data, _) = try await URLSession.shared.data(from: url)
            let decoded = try JSONDecoder().decode([ChatMessage].self, from: data)
            await MainActor.run {
                self.chatMessages[room] = decoded
            }
        } catch {
            print("fetchChatMessages error: \(error)")
        }
    }
    
    func sendChatMessage(room: String, message: String) async -> Bool {
        guard let url = URL(string: "\(baseURL)/api/chat") else { return false }
        
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/x-www-form-urlencoded", forHTTPHeaderField: "Content-Type")
        request.httpBody = "room=\(room.urlEncoded)&message=\(message.urlEncoded)&nick=\(localNick.urlEncoded)&sender_id=\(nodeId)".data(using: .utf8)
        
        do {
            let (_, response) = try await URLSession.shared.data(for: request)
            if let httpResponse = response as? HTTPURLResponse, httpResponse.statusCode == 200 {
                await fetchChatMessages(room: room)
                return true
            }
        } catch {
            print("sendChatMessage error: \(error)")
        }
        return false
    }
    
    // MARK: - Domains
    
    func fetchDomains() async {
        guard let url = URL(string: "\(baseURL)/api/domains") else { return }
        
        do {
            let (data, _) = try await URLSession.shared.data(from: url)
            let decoded = try JSONDecoder().decode([Domain].self, from: data)
            await MainActor.run {
                self.domains = decoded
            }
        } catch {
            print("fetchDomains error: \(error)")
        }
    }
    
    func registerDomain(name: String, target: String) async -> Bool {
        guard let url = URL(string: "\(baseURL)/api/domains/register") else { return false }
        
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/x-www-form-urlencoded", forHTTPHeaderField: "Content-Type")
        request.httpBody = "name=\(name.urlEncoded)&target=\(target.urlEncoded)&owner=\(nodeId)".data(using: .utf8)
        
        do {
            let (_, response) = try await URLSession.shared.data(for: request)
            if let httpResponse = response as? HTTPURLResponse, httpResponse.statusCode == 200 {
                await fetchDomains()
                return true
            }
        } catch {
            print("registerDomain error: \(error)")
        }
        return false
    }
    
    func setLocalNick(_ nick: String) {
        localNick = nick
        UserDefaults.standard.set(nick, forKey: "local_nick")
    }
}

// MARK: - String Extension
extension String {
    var urlEncoded: String {
        addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? self
    }
}

