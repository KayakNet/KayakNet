import SwiftUI

struct MarketView: View {
    @EnvironmentObject var client: KayakNetClient
    @State private var searchText = ""
    @State private var selectedListing: Listing?
    @State private var showCreateListing = false
    @State private var showOrders = false
    
    var filteredListings: [Listing] {
        if searchText.isEmpty {
            return client.listings
        }
        return client.listings.filter {
            $0.title.localizedCaseInsensitiveContains(searchText) ||
            $0.description.localizedCaseInsensitiveContains(searchText)
        }
    }
    
    var body: some View {
        NavigationView {
            ZStack {
                Color.black.ignoresSafeArea()
                
                VStack(spacing: 0) {
                    // Header
                    HStack {
                        Text("MARKETPLACE")
                            .font(.system(size: 20, weight: .bold, design: .monospaced))
                            .foregroundColor(.green)
                        
                        Spacer()
                        
                        Button(action: { showOrders = true }) {
                            ZStack(alignment: .topTrailing) {
                                Image(systemName: "lock.fill")
                                    .foregroundColor(.green)
                                
                                if !client.myEscrows.isEmpty {
                                    Circle()
                                        .fill(Color.red)
                                        .frame(width: 8, height: 8)
                                        .offset(x: 4, y: -4)
                                }
                            }
                        }
                        .padding(.trailing, 8)
                        
                        Button(action: { showCreateListing = true }) {
                            Image(systemName: "plus")
                                .foregroundColor(.green)
                        }
                    }
                    .padding()
                    
                    // Search
                    HStack {
                        Image(systemName: "magnifyingglass")
                            .foregroundColor(.green.opacity(0.5))
                        TextField("Search listings...", text: $searchText)
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
                    
                    // Listings
                    ScrollView {
                        LazyVStack(spacing: 12) {
                            ForEach(filteredListings) { listing in
                                ListingCard(listing: listing)
                                    .onTapGesture {
                                        selectedListing = listing
                                    }
                            }
                        }
                        .padding()
                    }
                }
            }
            .navigationBarHidden(true)
            .sheet(item: $selectedListing) { listing in
                ListingDetailView(listing: listing)
                    .environmentObject(client)
            }
            .sheet(isPresented: $showCreateListing) {
                CreateListingView()
                    .environmentObject(client)
            }
            .sheet(isPresented: $showOrders) {
                OrdersView()
                    .environmentObject(client)
            }
        }
    }
}

struct ListingCard: View {
    let listing: Listing
    
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                VStack(alignment: .leading, spacing: 4) {
                    Text(listing.title)
                        .font(.system(size: 14, weight: .bold, design: .monospaced))
                        .foregroundColor(.green)
                        .lineLimit(1)
                    
                    Text(listing.description)
                        .font(.system(size: 12, design: .monospaced))
                        .foregroundColor(.green.opacity(0.7))
                        .lineLimit(2)
                }
                
                Spacer()
                
                VStack(alignment: .trailing, spacing: 4) {
                    Text("\(listing.price, specifier: "%.2f")")
                        .font(.system(size: 16, weight: .bold, design: .monospaced))
                        .foregroundColor(.cyan)
                    
                    Text(listing.currency.isEmpty ? "USD" : listing.currency)
                        .font(.system(size: 10, design: .monospaced))
                        .foregroundColor(.cyan.opacity(0.7))
                }
            }
            
            HStack {
                Text(listing.category)
                    .font(.system(size: 10, design: .monospaced))
                    .foregroundColor(.green.opacity(0.5))
                    .padding(.horizontal, 8)
                    .padding(.vertical, 4)
                    .background(Color.green.opacity(0.1))
                    .cornerRadius(4)
                
                Spacer()
                
                Text("by \(listing.sellerName ?? listing.seller.prefix(8).description)")
                    .font(.system(size: 10, design: .monospaced))
                    .foregroundColor(.green.opacity(0.5))
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

struct ListingDetailView: View {
    @EnvironmentObject var client: KayakNetClient
    @Environment(\.dismiss) var dismiss
    let listing: Listing
    
    @State private var showOrderForm = false
    @State private var selectedCurrency = "XMR"
    @State private var refundAddress = ""
    @State private var deliveryInfo = ""
    @State private var isOrdering = false
    @State private var createdEscrow: Escrow?
    @State private var errorMessage: String?
    
    var body: some View {
        NavigationView {
            ZStack {
                Color.black.ignoresSafeArea()
                
                ScrollView {
                    VStack(alignment: .leading, spacing: 16) {
                        if let escrow = createdEscrow {
                            // Payment Details
                            PaymentDetailsView(escrow: escrow, onDone: { dismiss() })
                        } else if showOrderForm {
                            // Order Form
                            OrderFormView(
                                listing: listing,
                                selectedCurrency: $selectedCurrency,
                                refundAddress: $refundAddress,
                                deliveryInfo: $deliveryInfo,
                                isOrdering: $isOrdering,
                                errorMessage: $errorMessage,
                                onSubmit: placeOrder,
                                onBack: { showOrderForm = false }
                            )
                        } else {
                            // Listing Details
                            ListingDetailsContent(listing: listing, onBuy: { showOrderForm = true })
                        }
                    }
                    .padding()
                }
            }
            .navigationTitle(listing.title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .navigationBarTrailing) {
                    Button("Close") { dismiss() }
                        .foregroundColor(.green)
                }
            }
        }
    }
    
    func placeOrder() {
        guard !deliveryInfo.isEmpty else {
            errorMessage = "Please enter delivery information"
            return
        }
        
        isOrdering = true
        errorMessage = nil
        
        Task {
            let escrow = await client.createEscrow(
                listingId: listing.id,
                currency: selectedCurrency,
                refundAddress: refundAddress,
                deliveryInfo: deliveryInfo
            )
            
            await MainActor.run {
                isOrdering = false
                if let escrow = escrow {
                    createdEscrow = escrow
                } else {
                    errorMessage = "Failed to create order"
                }
            }
        }
    }
}

struct ListingDetailsContent: View {
    let listing: Listing
    let onBuy: () -> Void
    
    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text(listing.description)
                .font(.system(size: 14, design: .monospaced))
                .foregroundColor(.green.opacity(0.9))
            
            HStack {
                VStack(alignment: .leading) {
                    Text("Price")
                        .font(.system(size: 10, design: .monospaced))
                        .foregroundColor(.green.opacity(0.5))
                    Text("\(listing.price, specifier: "%.2f") \(listing.currency.isEmpty ? "USD" : listing.currency)")
                        .font(.system(size: 20, weight: .bold, design: .monospaced))
                        .foregroundColor(.cyan)
                }
                
                Spacer()
                
                VStack(alignment: .trailing) {
                    Text("Category")
                        .font(.system(size: 10, design: .monospaced))
                        .foregroundColor(.green.opacity(0.5))
                    Text(listing.category)
                        .font(.system(size: 14, design: .monospaced))
                        .foregroundColor(.green)
                }
            }
            
            VStack(alignment: .leading) {
                Text("Seller")
                    .font(.system(size: 10, design: .monospaced))
                    .foregroundColor(.green.opacity(0.5))
                Text(listing.sellerName ?? listing.seller.prefix(16).description)
                    .font(.system(size: 14, design: .monospaced))
                    .foregroundColor(.green)
            }
            
            // Accepts
            VStack(alignment: .leading) {
                Text("Accepts")
                    .font(.system(size: 10, design: .monospaced))
                    .foregroundColor(.green.opacity(0.5))
                HStack {
                    if listing.sellerXmrAddress != nil {
                        Text("XMR")
                            .font(.system(size: 12, design: .monospaced))
                            .foregroundColor(.orange)
                            .padding(.horizontal, 8)
                            .padding(.vertical, 4)
                            .background(Color.orange.opacity(0.2))
                            .cornerRadius(4)
                    }
                    if listing.sellerZecAddress != nil {
                        Text("ZEC")
                            .font(.system(size: 12, design: .monospaced))
                            .foregroundColor(.yellow)
                            .padding(.horizontal, 8)
                            .padding(.vertical, 4)
                            .background(Color.yellow.opacity(0.2))
                            .cornerRadius(4)
                    }
                }
            }
            
            Button(action: onBuy) {
                Text("BUY NOW")
                    .font(.system(size: 16, weight: .bold, design: .monospaced))
                    .foregroundColor(.black)
                    .frame(maxWidth: .infinity)
                    .padding()
                    .background(Color.green)
                    .cornerRadius(8)
            }
        }
    }
}

struct OrderFormView: View {
    let listing: Listing
    @Binding var selectedCurrency: String
    @Binding var refundAddress: String
    @Binding var deliveryInfo: String
    @Binding var isOrdering: Bool
    @Binding var errorMessage: String?
    let onSubmit: () -> Void
    let onBack: () -> Void
    
    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("PLACE ORDER")
                .font(.system(size: 16, weight: .bold, design: .monospaced))
                .foregroundColor(.orange)
            
            // Price
            HStack {
                Text(listing.title)
                    .font(.system(size: 12, design: .monospaced))
                    .foregroundColor(.green)
                Spacer()
                Text("\(listing.price, specifier: "%.2f") USD")
                    .font(.system(size: 14, weight: .bold, design: .monospaced))
                    .foregroundColor(.cyan)
            }
            .padding()
            .background(Color.green.opacity(0.1))
            .cornerRadius(8)
            
            // Currency
            VStack(alignment: .leading, spacing: 4) {
                Text("PAYMENT METHOD:")
                    .font(.system(size: 11, design: .monospaced))
                    .foregroundColor(.green.opacity(0.7))
                
                Picker("Currency", selection: $selectedCurrency) {
                    Text("MONERO (XMR)").tag("XMR")
                    Text("ZCASH (ZEC)").tag("ZEC")
                }
                .pickerStyle(.segmented)
            }
            
            // Refund Address
            VStack(alignment: .leading, spacing: 4) {
                Text("YOUR REFUND ADDRESS (optional):")
                    .font(.system(size: 11, design: .monospaced))
                    .foregroundColor(.green.opacity(0.7))
                
                TextField("Your \(selectedCurrency) address...", text: $refundAddress)
                    .font(.system(size: 12, design: .monospaced))
                    .foregroundColor(.green)
                    .padding()
                    .background(Color.green.opacity(0.05))
                    .cornerRadius(8)
                    .overlay(
                        RoundedRectangle(cornerRadius: 8)
                            .stroke(Color.green.opacity(0.3), lineWidth: 1)
                    )
            }
            
            // Delivery Info
            VStack(alignment: .leading, spacing: 4) {
                Text("DELIVERY INFO (encrypted):")
                    .font(.system(size: 11, design: .monospaced))
                    .foregroundColor(.green.opacity(0.7))
                
                TextEditor(text: $deliveryInfo)
                    .font(.system(size: 12, design: .monospaced))
                    .foregroundColor(.green)
                    .frame(minHeight: 80)
                    .padding(8)
                    .background(Color.green.opacity(0.05))
                    .cornerRadius(8)
                    .overlay(
                        RoundedRectangle(cornerRadius: 8)
                            .stroke(Color.green.opacity(0.3), lineWidth: 1)
                    )
            }
            
            // Escrow Info
            VStack(alignment: .leading, spacing: 4) {
                Text("ESCROW PROTECTION")
                    .font(.system(size: 12, weight: .bold, design: .monospaced))
                    .foregroundColor(.orange)
                Text("• Funds held securely until you confirm receipt")
                    .font(.system(size: 10, design: .monospaced))
                    .foregroundColor(.orange)
                Text("• 5% escrow fee")
                    .font(.system(size: 10, design: .monospaced))
                    .foregroundColor(.orange)
                Text("• 14-day auto-release after shipping")
                    .font(.system(size: 10, design: .monospaced))
                    .foregroundColor(.orange)
            }
            .padding()
            .background(Color.orange.opacity(0.1))
            .cornerRadius(8)
            .overlay(
                RoundedRectangle(cornerRadius: 8)
                    .stroke(Color.orange.opacity(0.3), lineWidth: 1)
            )
            
            if let error = errorMessage {
                Text(error)
                    .font(.system(size: 12, design: .monospaced))
                    .foregroundColor(.red)
            }
            
            HStack {
                Button(action: onBack) {
                    Text("BACK")
                        .font(.system(size: 14, weight: .bold, design: .monospaced))
                        .foregroundColor(.green)
                        .frame(maxWidth: .infinity)
                        .padding()
                        .background(Color.green.opacity(0.1))
                        .cornerRadius(8)
                }
                
                Button(action: onSubmit) {
                    if isOrdering {
                        ProgressView()
                            .progressViewStyle(CircularProgressViewStyle(tint: .black))
                    } else {
                        Text("CONFIRM ORDER")
                            .font(.system(size: 14, weight: .bold, design: .monospaced))
                    }
                }
                .foregroundColor(.black)
                .frame(maxWidth: .infinity)
                .padding()
                .background(deliveryInfo.isEmpty ? Color.gray : Color.green)
                .cornerRadius(8)
                .disabled(deliveryInfo.isEmpty || isOrdering)
            }
        }
    }
}

struct PaymentDetailsView: View {
    let escrow: Escrow
    let onDone: () -> Void
    
    var body: some View {
        VStack(spacing: 16) {
            Image(systemName: "checkmark.circle.fill")
                .font(.system(size: 48))
                .foregroundColor(.green)
            
            Text("PAYMENT REQUIRED")
                .font(.system(size: 18, weight: .bold, design: .monospaced))
                .foregroundColor(.orange)
            
            Text("Send exactly:")
                .font(.system(size: 12, design: .monospaced))
                .foregroundColor(.green.opacity(0.7))
            
            Text("\(escrow.amount, specifier: "%.8f") \(escrow.currency)")
                .font(.system(size: 24, weight: .bold, design: .monospaced))
                .foregroundColor(.green)
                .padding()
                .frame(maxWidth: .infinity)
                .background(Color.green.opacity(0.1))
                .cornerRadius(8)
                .overlay(
                    RoundedRectangle(cornerRadius: 8)
                        .stroke(Color.green, lineWidth: 2)
                )
            
            Text("To address:")
                .font(.system(size: 12, design: .monospaced))
                .foregroundColor(.green.opacity(0.7))
            
            Text(escrow.paymentAddress)
                .font(.system(size: 10, design: .monospaced))
                .foregroundColor(.cyan)
                .padding()
                .frame(maxWidth: .infinity)
                .background(Color.green.opacity(0.05))
                .cornerRadius(8)
                .contextMenu {
                    Button(action: {
                        UIPasteboard.general.string = escrow.paymentAddress
                    }) {
                        Label("Copy Address", systemImage: "doc.on.doc")
                    }
                }
            
            Button(action: {
                UIPasteboard.general.string = escrow.paymentAddress
            }) {
                Text("COPY ADDRESS")
                    .font(.system(size: 14, weight: .bold, design: .monospaced))
                    .foregroundColor(.green)
                    .frame(maxWidth: .infinity)
                    .padding()
                    .background(Color.green.opacity(0.1))
                    .cornerRadius(8)
            }
            
            VStack(alignment: .leading, spacing: 4) {
                Text("Order ID: \(escrow.orderId)")
                    .font(.system(size: 10, design: .monospaced))
                    .foregroundColor(.green.opacity(0.5))
                if let fee = escrow.feePercent {
                    Text("Fee: \(fee, specifier: "%.1f")%")
                        .font(.system(size: 10, design: .monospaced))
                        .foregroundColor(.green.opacity(0.5))
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            
            Text("After sending payment, check your orders to track status.\nRequires \(escrow.currency == "XMR" ? "10" : "6") confirmations.")
                .font(.system(size: 11, design: .monospaced))
                .foregroundColor(.orange)
                .padding()
                .background(Color.orange.opacity(0.1))
                .cornerRadius(8)
            
            Button(action: onDone) {
                Text("VIEW ORDERS")
                    .font(.system(size: 16, weight: .bold, design: .monospaced))
                    .foregroundColor(.black)
                    .frame(maxWidth: .infinity)
                    .padding()
                    .background(Color.green)
                    .cornerRadius(8)
            }
        }
    }
}

struct CreateListingView: View {
    @EnvironmentObject var client: KayakNetClient
    @Environment(\.dismiss) var dismiss
    
    @State private var title = ""
    @State private var description = ""
    @State private var price = ""
    @State private var currency = "XMR"
    @State private var category = "Digital Goods"
    @State private var xmrAddress = ""
    @State private var zecAddress = ""
    @State private var isCreating = false
    
    let categories = ["Digital Goods", "Services", "Physical", "Art", "Software", "Other"]
    
    var body: some View {
        NavigationView {
            ZStack {
                Color.black.ignoresSafeArea()
                
                ScrollView {
                    VStack(spacing: 16) {
                        FormField(title: "TITLE", text: $title)
                        FormField(title: "DESCRIPTION", text: $description, isMultiline: true)
                        
                        HStack {
                            FormField(title: "PRICE (USD)", text: $price)
                                .keyboardType(.decimalPad)
                            
                            VStack(alignment: .leading) {
                                Text("CURRENCY")
                                    .font(.system(size: 10, design: .monospaced))
                                    .foregroundColor(.green.opacity(0.7))
                                Picker("Currency", selection: $currency) {
                                    Text("XMR").tag("XMR")
                                    Text("ZEC").tag("ZEC")
                                }
                                .pickerStyle(.segmented)
                            }
                        }
                        
                        VStack(alignment: .leading) {
                            Text("CATEGORY")
                                .font(.system(size: 10, design: .monospaced))
                                .foregroundColor(.green.opacity(0.7))
                            Picker("Category", selection: $category) {
                                ForEach(categories, id: \.self) { cat in
                                    Text(cat).tag(cat)
                                }
                            }
                            .pickerStyle(.menu)
                            .tint(.green)
                        }
                        
                        VStack(alignment: .leading, spacing: 8) {
                            Text("PAYMENT ADDRESSES")
                                .font(.system(size: 12, weight: .bold, design: .monospaced))
                                .foregroundColor(.orange)
                            
                            FormField(title: "YOUR MONERO (XMR) ADDRESS", text: $xmrAddress, placeholder: "4...")
                            FormField(title: "YOUR ZCASH (ZEC) ADDRESS", text: $zecAddress, placeholder: "zs1...")
                        }
                        .padding()
                        .background(Color.orange.opacity(0.1))
                        .cornerRadius(8)
                        
                        Button(action: createListing) {
                            if isCreating {
                                ProgressView()
                                    .progressViewStyle(CircularProgressViewStyle(tint: .black))
                            } else {
                                Text("CREATE LISTING")
                                    .font(.system(size: 16, weight: .bold, design: .monospaced))
                            }
                        }
                        .foregroundColor(.black)
                        .frame(maxWidth: .infinity)
                        .padding()
                        .background(canCreate ? Color.green : Color.gray)
                        .cornerRadius(8)
                        .disabled(!canCreate || isCreating)
                    }
                    .padding()
                }
            }
            .navigationTitle("Create Listing")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .navigationBarLeading) {
                    Button("Cancel") { dismiss() }
                        .foregroundColor(.green)
                }
            }
        }
    }
    
    var canCreate: Bool {
        !title.isEmpty && !description.isEmpty && !price.isEmpty && Double(price) != nil
    }
    
    func createListing() {
        guard let priceValue = Double(price) else { return }
        isCreating = true
        
        Task {
            let success = await client.createListing(
                title: title,
                description: description,
                price: priceValue,
                currency: currency,
                category: category,
                xmrAddress: xmrAddress.isEmpty ? nil : xmrAddress,
                zecAddress: zecAddress.isEmpty ? nil : zecAddress
            )
            
            await MainActor.run {
                isCreating = false
                if success {
                    dismiss()
                }
            }
        }
    }
}

struct FormField: View {
    let title: String
    @Binding var text: String
    var placeholder: String = ""
    var isMultiline: Bool = false
    
    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(title)
                .font(.system(size: 10, design: .monospaced))
                .foregroundColor(.green.opacity(0.7))
            
            if isMultiline {
                TextEditor(text: $text)
                    .font(.system(size: 14, design: .monospaced))
                    .foregroundColor(.green)
                    .frame(minHeight: 80)
                    .padding(8)
                    .background(Color.green.opacity(0.05))
                    .cornerRadius(8)
                    .overlay(
                        RoundedRectangle(cornerRadius: 8)
                            .stroke(Color.green.opacity(0.3), lineWidth: 1)
                    )
            } else {
                TextField(placeholder, text: $text)
                    .font(.system(size: 14, design: .monospaced))
                    .foregroundColor(.green)
                    .padding()
                    .background(Color.green.opacity(0.05))
                    .cornerRadius(8)
                    .overlay(
                        RoundedRectangle(cornerRadius: 8)
                            .stroke(Color.green.opacity(0.3), lineWidth: 1)
                    )
            }
        }
    }
}

struct OrdersView: View {
    @EnvironmentObject var client: KayakNetClient
    @Environment(\.dismiss) var dismiss
    @State private var selectedTab = 0
    
    var buyingEscrows: [Escrow] {
        client.myEscrows.filter { $0.isBuyer }
    }
    
    var sellingEscrows: [Escrow] {
        client.myEscrows.filter { $0.isSeller }
    }
    
    var body: some View {
        NavigationView {
            ZStack {
                Color.black.ignoresSafeArea()
                
                VStack {
                    Picker("Orders", selection: $selectedTab) {
                        Text("BUYING (\(buyingEscrows.count))").tag(0)
                        Text("SELLING (\(sellingEscrows.count))").tag(1)
                    }
                    .pickerStyle(.segmented)
                    .padding()
                    
                    let escrows = selectedTab == 0 ? buyingEscrows : sellingEscrows
                    
                    if escrows.isEmpty {
                        Spacer()
                        Text(selectedTab == 0 ? "No purchases" : "No sales")
                            .font(.system(size: 14, design: .monospaced))
                            .foregroundColor(.green.opacity(0.5))
                        Spacer()
                    } else {
                        ScrollView {
                            LazyVStack(spacing: 12) {
                                ForEach(escrows) { escrow in
                                    EscrowCard(escrow: escrow, isSeller: selectedTab == 1)
                                }
                            }
                            .padding()
                        }
                    }
                }
            }
            .navigationTitle("My Orders")
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

struct EscrowCard: View {
    @EnvironmentObject var client: KayakNetClient
    let escrow: Escrow
    let isSeller: Bool
    
    var stateColor: Color {
        switch escrow.state.lowercased() {
        case "created": return .yellow
        case "funded": return .green
        case "shipped": return .cyan
        case "completed": return .green
        case "disputed": return .red
        default: return .gray
        }
    }
    
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                VStack(alignment: .leading) {
                    Text(escrow.listingTitle)
                        .font(.system(size: 14, weight: .bold, design: .monospaced))
                        .foregroundColor(.green)
                    Text("\(escrow.amount, specifier: "%.8f") \(escrow.currency)")
                        .font(.system(size: 12, design: .monospaced))
                        .foregroundColor(.cyan)
                }
                
                Spacer()
                
                Text(escrow.state.uppercased())
                    .font(.system(size: 10, weight: .bold, design: .monospaced))
                    .foregroundColor(stateColor)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 4)
                    .background(stateColor.opacity(0.2))
                    .cornerRadius(4)
            }
            
            Text("Order: \(escrow.orderId.prefix(16))...")
                .font(.system(size: 10, design: .monospaced))
                .foregroundColor(.green.opacity(0.5))
            
            // Show payment address for created state
            if escrow.state.lowercased() == "created" && !escrow.paymentAddress.isEmpty {
                Text("Pay to: \(escrow.paymentAddress.prefix(30))...")
                    .font(.system(size: 9, design: .monospaced))
                    .foregroundColor(.yellow)
                    .contextMenu {
                        Button(action: {
                            UIPasteboard.general.string = escrow.paymentAddress
                        }) {
                            Label("Copy Address", systemImage: "doc.on.doc")
                        }
                    }
            }
            
            // Actions based on state
            if escrow.state.lowercased() == "created" && !isSeller {
                Button(action: {
                    UIPasteboard.general.string = escrow.paymentAddress
                }) {
                    Text("COPY ADDRESS")
                        .font(.system(size: 10, weight: .bold, design: .monospaced))
                        .foregroundColor(.yellow)
                        .frame(maxWidth: .infinity)
                        .padding(8)
                        .background(Color.yellow.opacity(0.2))
                        .cornerRadius(4)
                }
            }
            
            if escrow.state.lowercased() == "funded" && isSeller {
                Button(action: {
                    Task {
                        _ = await client.markEscrowShipped(escrowId: escrow.escrowId, trackingInfo: "")
                    }
                }) {
                    Text("MARK AS SHIPPED")
                        .font(.system(size: 10, weight: .bold, design: .monospaced))
                        .foregroundColor(.cyan)
                        .frame(maxWidth: .infinity)
                        .padding(8)
                        .background(Color.cyan.opacity(0.2))
                        .cornerRadius(4)
                }
            }
            
            if escrow.state.lowercased() == "shipped" && !isSeller {
                Button(action: {
                    Task {
                        _ = await client.confirmEscrowReceived(escrowId: escrow.escrowId)
                    }
                }) {
                    Text("CONFIRM RECEIVED")
                        .font(.system(size: 10, weight: .bold, design: .monospaced))
                        .foregroundColor(.green)
                        .frame(maxWidth: .infinity)
                        .padding(8)
                        .background(Color.green.opacity(0.2))
                        .cornerRadius(4)
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

#Preview {
    MarketView()
        .environmentObject(KayakNetClient())
}

