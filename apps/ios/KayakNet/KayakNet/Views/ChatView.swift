import SwiftUI

struct ChatView: View {
    @EnvironmentObject var client: KayakNetClient
    @State private var selectedRoom: String = "general"
    @State private var messageText: String = ""
    @State private var showRoomPicker = false
    
    var messages: [ChatMessage] {
        client.chatMessages[selectedRoom] ?? []
    }
    
    var body: some View {
        NavigationView {
            ZStack {
                Color.black.ignoresSafeArea()
                
                VStack(spacing: 0) {
                    // Header
                    HStack {
                        Text("CHAT")
                            .font(.system(size: 20, weight: .bold, design: .monospaced))
                            .foregroundColor(.green)
                        
                        Spacer()
                        
                        Button(action: { showRoomPicker = true }) {
                            HStack {
                                Text("#\(selectedRoom)")
                                    .font(.system(size: 14, design: .monospaced))
                                Image(systemName: "chevron.down")
                                    .font(.system(size: 12))
                            }
                            .foregroundColor(.green)
                            .padding(.horizontal, 12)
                            .padding(.vertical, 6)
                            .background(Color.green.opacity(0.1))
                            .cornerRadius(4)
                        }
                    }
                    .padding()
                    
                    Divider()
                        .background(Color.green.opacity(0.3))
                    
                    // Messages
                    ScrollViewReader { proxy in
                        ScrollView {
                            LazyVStack(alignment: .leading, spacing: 12) {
                                ForEach(messages) { message in
                                    MessageBubble(message: message)
                                        .id(message.id)
                                }
                            }
                            .padding()
                        }
                        .onChange(of: messages.count) { _ in
                            if let lastMessage = messages.last {
                                withAnimation {
                                    proxy.scrollTo(lastMessage.id, anchor: .bottom)
                                }
                            }
                        }
                    }
                    
                    Divider()
                        .background(Color.green.opacity(0.3))
                    
                    // Input
                    HStack(spacing: 12) {
                        TextField("Message...", text: $messageText)
                            .font(.system(size: 14, design: .monospaced))
                            .foregroundColor(.green)
                            .padding(12)
                            .background(Color.green.opacity(0.05))
                            .cornerRadius(8)
                            .overlay(
                                RoundedRectangle(cornerRadius: 8)
                                    .stroke(Color.green.opacity(0.3), lineWidth: 1)
                            )
                        
                        Button(action: sendMessage) {
                            Image(systemName: "paperplane.fill")
                                .font(.system(size: 18))
                                .foregroundColor(.black)
                                .frame(width: 44, height: 44)
                                .background(Color.green)
                                .cornerRadius(8)
                        }
                        .disabled(messageText.isEmpty)
                    }
                    .padding()
                }
            }
            .navigationBarHidden(true)
            .onAppear {
                Task {
                    await client.fetchChatMessages(room: selectedRoom)
                }
            }
            .sheet(isPresented: $showRoomPicker) {
                RoomPickerView(selectedRoom: $selectedRoom, showPicker: $showRoomPicker)
                    .environmentObject(client)
            }
        }
    }
    
    func sendMessage() {
        let text = messageText.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return }
        
        messageText = ""
        
        Task {
            _ = await client.sendChatMessage(room: selectedRoom, message: text)
        }
    }
}

struct MessageBubble: View {
    let message: ChatMessage
    
    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(message.nick)
                    .font(.system(size: 12, weight: .bold, design: .monospaced))
                    .foregroundColor(.cyan)
                
                Text(formatTime(message.timestamp))
                    .font(.system(size: 10, design: .monospaced))
                    .foregroundColor(.green.opacity(0.5))
            }
            
            Text(message.message)
                .font(.system(size: 14, design: .monospaced))
                .foregroundColor(.green)
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.green.opacity(0.05))
        .cornerRadius(8)
    }
    
    func formatTime(_ timestamp: String) -> String {
        // Simple time extraction
        if timestamp.count >= 16 {
            return String(timestamp.prefix(16).suffix(5))
        }
        return timestamp
    }
}

struct RoomPickerView: View {
    @EnvironmentObject var client: KayakNetClient
    @Binding var selectedRoom: String
    @Binding var showPicker: Bool
    
    var body: some View {
        NavigationView {
            ZStack {
                Color.black.ignoresSafeArea()
                
                List(client.chatRooms) { room in
                    Button(action: {
                        selectedRoom = room.name
                        showPicker = false
                        Task {
                            await client.fetchChatMessages(room: room.name)
                        }
                    }) {
                        HStack {
                            Text("#\(room.name)")
                                .font(.system(size: 16, design: .monospaced))
                                .foregroundColor(.green)
                            
                            Spacer()
                            
                            if selectedRoom == room.name {
                                Image(systemName: "checkmark")
                                    .foregroundColor(.green)
                            }
                        }
                    }
                    .listRowBackground(Color.black)
                }
                .listStyle(.plain)
            }
            .navigationTitle("Select Room")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .navigationBarTrailing) {
                    Button("Done") {
                        showPicker = false
                    }
                    .foregroundColor(.green)
                }
            }
        }
    }
}

#Preview {
    ChatView()
        .environmentObject(KayakNetClient())
}


