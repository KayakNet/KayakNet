package net.kayaknet.app.network

import android.content.Context
import com.google.gson.Gson
import com.google.gson.reflect.TypeToken
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import okhttp3.*
import okhttp3.FormBody
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.File
import java.io.IOException
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress
import java.security.KeyPair
import java.security.KeyPairGenerator
import java.security.MessageDigest
import java.security.SecureRandom
import java.util.concurrent.TimeUnit

class KayakNetClient(private val context: Context) {
    
    private val gson = Gson()
    private val httpClient = OkHttpClient.Builder()
        .connectTimeout(30, TimeUnit.SECONDS)
        .readTimeout(30, TimeUnit.SECONDS)
        .writeTimeout(30, TimeUnit.SECONDS)
        .build()
    
    private var socket: DatagramSocket? = null
    private var keyPair: KeyPair? = null
    private var nodeId: String = ""
    
    private val scope = CoroutineScope(Dispatchers.IO + SupervisorJob())
    
    // Connection state
    private val _connectionState = MutableStateFlow(ConnectionState.DISCONNECTED)
    val connectionState: StateFlow<ConnectionState> = _connectionState
    
    private val _peers = MutableStateFlow<List<Peer>>(emptyList())
    val peers: StateFlow<List<Peer>> = _peers
    
    private val _chatMessages = MutableStateFlow<List<ChatMessage>>(emptyList())
    val chatMessages: StateFlow<List<ChatMessage>> = _chatMessages
    
    private val _listings = MutableStateFlow<List<Listing>>(emptyList())
    val listings: StateFlow<List<Listing>> = _listings
    
    private val _domains = MutableStateFlow<List<Domain>>(emptyList())
    val domains: StateFlow<List<Domain>> = _domains
    
    private var bootstrapHost = "203.161.33.237"
    private var bootstrapPort = 4242
    
    init {
        loadOrCreateIdentity()
    }
    
    private fun loadOrCreateIdentity() {
        val identityFile = File(context.filesDir, "identity.json")
        if (identityFile.exists()) {
            try {
                val data = identityFile.readText()
                val identity = gson.fromJson(data, Identity::class.java)
                nodeId = identity.nodeId
                // In real implementation, load the keypair from stored data
            } catch (e: Exception) {
                createNewIdentity(identityFile)
            }
        } else {
            createNewIdentity(identityFile)
        }
    }
    
    private fun createNewIdentity(file: File) {
        // Generate Ed25519-like identity (simplified for demo)
        val keyGen = KeyPairGenerator.getInstance("EC")
        keyGen.initialize(256, SecureRandom())
        keyPair = keyGen.generateKeyPair()
        
        // Generate node ID from public key hash
        val digest = MessageDigest.getInstance("SHA-256")
        val hash = digest.digest(keyPair!!.public.encoded)
        nodeId = hash.joinToString("") { "%02x".format(it) }
        
        // Save identity
        val identity = Identity(nodeId, System.currentTimeMillis())
        file.writeText(gson.toJson(identity))
    }
    
    fun connect() {
        scope.launch {
            _connectionState.value = ConnectionState.CONNECTING
            
            try {
                // Test connection via HTTP API
                val request = Request.Builder()
                    .url("http://$bootstrapHost:8080/api/stats")
                    .build()
                
                httpClient.newCall(request).execute().use { response ->
                    if (response.isSuccessful) {
                        _connectionState.value = ConnectionState.CONNECTED
                        // Load initial data
                        refreshData()
                    } else {
                        _connectionState.value = ConnectionState.ERROR
                    }
                }
            } catch (e: Exception) {
                _connectionState.value = ConnectionState.ERROR
            }
        }
    }
    
    fun disconnect() {
        scope.launch {
            socket?.close()
            socket = null
            _connectionState.value = ConnectionState.DISCONNECTED
        }
    }
    
    private fun startMessageListener() {
        scope.launch {
            while (isActive && socket != null && !socket!!.isClosed) {
                try {
                    val buffer = ByteArray(65535)
                    val packet = DatagramPacket(buffer, buffer.size)
                    socket?.receive(packet)
                    
                    val data = String(packet.data, 0, packet.length)
                    handleMessage(data)
                } catch (e: IOException) {
                    if (socket?.isClosed == true) break
                }
            }
        }
    }
    
    private fun handleMessage(data: String) {
        try {
            val msg = gson.fromJson(data, P2PMessage::class.java)
            when (msg.type) {
                "chat" -> {
                    val chatMsg = gson.fromJson(msg.payload, ChatMessage::class.java)
                    _chatMessages.value = _chatMessages.value + chatMsg
                }
                "listing" -> {
                    val listing = gson.fromJson(msg.payload, Listing::class.java)
                    _listings.value = _listings.value + listing
                }
                "domain" -> {
                    val domain = gson.fromJson(msg.payload, Domain::class.java)
                    _domains.value = _domains.value + domain
                }
            }
        } catch (e: Exception) {
            // Ignore malformed messages
        }
    }
    
    fun sendChatMessage(room: String, message: String) {
        scope.launch {
            try {
                // Send via HTTP POST to bootstrap API
                val formBody = FormBody.Builder()
                    .add("room", room)
                    .add("message", message)
                    .build()
                
                val request = Request.Builder()
                    .url("http://$bootstrapHost:8080/api/chat")
                    .post(formBody)
                    .build()
                
                httpClient.newCall(request).execute().use { response ->
                    if (response.isSuccessful) {
                        // Refresh messages to get the broadcasted message
                        fetchChatHistory(room)
                    }
                }
            } catch (e: Exception) {
                // Handle error silently
            }
        }
    }
    
    private fun sendToBootstrap(data: String) {
        try {
            val bytes = data.toByteArray()
            val address = InetAddress.getByName(bootstrapHost)
            val packet = DatagramPacket(bytes, bytes.size, address, bootstrapPort)
            socket?.send(packet)
        } catch (e: Exception) {
            // Handle send error
        }
    }
    
    suspend fun refreshData() {
        // Fetch data from bootstrap's HTTP API (if running proxy)
        fetchListings()
        fetchDomains()
        fetchChatHistory()
    }
    
    private suspend fun fetchListings() {
        try {
            val request = Request.Builder()
                .url("http://$bootstrapHost:8080/api/listings")
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    val body = response.body?.string() ?: "[]"
                    val type = object : TypeToken<List<Listing>>() {}.type
                    _listings.value = gson.fromJson(body, type) ?: emptyList()
                }
            }
        } catch (e: Exception) {
            // API might not be available
        }
    }
    
    private suspend fun fetchDomains() {
        try {
            val request = Request.Builder()
                .url("http://$bootstrapHost:8080/api/domains")
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    val body = response.body?.string() ?: "[]"
                    val type = object : TypeToken<List<Domain>>() {}.type
                    _domains.value = gson.fromJson(body, type) ?: emptyList()
                }
            }
        } catch (e: Exception) {
            // API might not be available
        }
    }
    
    private suspend fun fetchChatHistory(room: String = "general") {
        try {
            val request = Request.Builder()
                .url("http://$bootstrapHost:8080/api/chat?room=$room")
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    val body = response.body?.string() ?: "[]"
                    val type = object : TypeToken<List<ChatMessage>>() {}.type
                    val newMessages: List<ChatMessage> = gson.fromJson(body, type) ?: emptyList()
                    // Merge with existing messages from other rooms
                    val existingOtherRooms = _chatMessages.value.filter { it.room != room }
                    _chatMessages.value = existingOtherRooms + newMessages
                }
            }
        } catch (e: Exception) {
            // API might not be available
        }
    }
    
    suspend fun registerDomain(name: String, description: String): Result<Domain> {
        return try {
            val json = gson.toJson(mapOf(
                "name" to name,
                "description" to description
            ))
            
            val request = Request.Builder()
                .url("http://$bootstrapHost:8080/api/domains/register")
                .post(json.toRequestBody("application/json".toMediaType()))
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                val body = response.body?.string() ?: "{}"
                val result = gson.fromJson(body, Map::class.java)
                
                if (result["success"] == true) {
                    val domain = Domain(
                        name = name,
                        fullName = "$name.kyk",
                        description = description,
                        owner = nodeId
                    )
                    _domains.value = _domains.value + domain
                    Result.success(domain)
                } else {
                    Result.failure(Exception(result["error"]?.toString() ?: "Unknown error"))
                }
            }
        } catch (e: Exception) {
            Result.failure(e)
        }
    }
    
    fun getNodeId(): String = nodeId
    
    fun getLocalNick(): String {
        val prefs = context.getSharedPreferences("kayaknet", Context.MODE_PRIVATE)
        return prefs.getString("nick", "anon-${nodeId.take(8)}") ?: "anon"
    }
    
    fun setLocalNick(nick: String) {
        val prefs = context.getSharedPreferences("kayaknet", Context.MODE_PRIVATE)
        prefs.edit().putString("nick", nick).apply()
    }
    
    private fun generateMessageId(): String {
        val bytes = ByteArray(16)
        SecureRandom().nextBytes(bytes)
        return bytes.joinToString("") { "%02x".format(it) }
    }
}

enum class ConnectionState {
    DISCONNECTED,
    CONNECTING,
    CONNECTED,
    ERROR
}

data class Identity(
    val nodeId: String,
    val createdAt: Long
)

data class P2PMessage(
    val type: String,
    val from: String,
    val payload: String
)

data class Peer(
    val id: String,
    val address: String,
    val lastSeen: Long
)

data class ChatMessage(
    val id: String,
    val room: String,
    val sender: String,
    val nick: String,
    val content: String,
    val timestamp: Long,
    val mediaUrl: String? = null
)

data class Listing(
    val id: String,
    val title: String,
    val description: String,
    val price: Double,
    val currency: String,
    val category: String,
    val seller: String,
    val sellerName: String,
    val imageUrl: String? = null
)

data class Domain(
    val name: String,
    val fullName: String,
    val description: String,
    val owner: String,
    val serviceType: String? = null,
    val createdAt: String? = null,
    val expiresAt: String? = null
)

