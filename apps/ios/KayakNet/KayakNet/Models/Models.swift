import Foundation

struct Listing: Codable, Identifiable {
    let id: String
    let title: String
    let description: String
    let price: Double
    let currency: String
    let category: String
    let seller: String
    let sellerName: String?
    let sellerXmrAddress: String?
    let sellerZecAddress: String?
    let image: String?
    let stock: Int?
    let createdAt: String?
    
    enum CodingKeys: String, CodingKey {
        case id, title, description, price, currency, category, seller, image, stock
        case sellerName = "seller_name"
        case sellerXmrAddress = "seller_xmr_address"
        case sellerZecAddress = "seller_zec_address"
        case createdAt = "created_at"
    }
}

struct Escrow: Codable, Identifiable {
    var id: String { escrowId }
    let escrowId: String
    let orderId: String
    let listingId: String
    let listingTitle: String
    let buyerId: String
    let sellerId: String
    let amount: Double
    let currency: String
    let state: String
    let paymentAddress: String
    let txId: String?
    let trackingInfo: String?
    let createdAt: String?
    let expiresAt: String?
    let autoReleaseAt: String?
    let feePercent: Double?
    let feeAmount: Double?
    var isBuyer: Bool = false
    var isSeller: Bool = false
    
    enum CodingKeys: String, CodingKey {
        case escrowId = "escrow_id"
        case orderId = "order_id"
        case listingId = "listing_id"
        case listingTitle = "listing_title"
        case buyerId = "buyer_id"
        case sellerId = "seller_id"
        case amount, currency, state
        case paymentAddress = "payment_address"
        case txId = "tx_id"
        case trackingInfo = "tracking_info"
        case createdAt = "created_at"
        case expiresAt = "expires_at"
        case autoReleaseAt = "auto_release_at"
        case feePercent = "fee_percent"
        case feeAmount = "fee_amount"
        case isBuyer = "is_buyer"
        case isSeller = "is_seller"
    }
}

struct ChatRoom: Codable, Identifiable {
    var id: String { name }
    let name: String
    let description: String?
    let memberCount: Int?
    
    enum CodingKeys: String, CodingKey {
        case name, description
        case memberCount = "member_count"
    }
}

struct ChatMessage: Codable, Identifiable {
    let id: String
    let senderId: String
    let nick: String
    let message: String
    let timestamp: String
    let room: String?
    
    enum CodingKeys: String, CodingKey {
        case id
        case senderId = "sender_id"
        case nick, message, timestamp, room
    }
}

struct Domain: Codable, Identifiable {
    var id: String { name }
    let name: String
    let target: String
    let owner: String
    let expiresAt: String?
    
    enum CodingKeys: String, CodingKey {
        case name, target, owner
        case expiresAt = "expires_at"
    }
}

struct Peer: Codable, Identifiable {
    var id: String { nodeId }
    let nodeId: String
    let address: String
    let connected: Bool?
    
    enum CodingKeys: String, CodingKey {
        case nodeId = "node_id"
        case address, connected
    }
}


