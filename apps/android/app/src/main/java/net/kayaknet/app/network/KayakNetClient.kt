package net.kayaknet.app.network

import android.content.Context
import android.content.SharedPreferences
import android.util.Log
import com.google.gson.Gson
import com.google.gson.reflect.TypeToken
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import okhttp3.*
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.File
import java.security.KeyPair
import java.security.KeyPairGenerator
import java.security.MessageDigest
import java.security.SecureRandom
import java.util.concurrent.TimeUnit

class KayakNetClient(private val context: Context) {
    
    companion object {
        private const val TAG = "KayakNetClient"
        private const val PREFS_NAME = "kayaknet_prefs"
        private const val KEY_NODE_ID = "node_id"
        private const val KEY_NICK = "nick"
        private const val KEY_BOOTSTRAP = "bootstrap_host"
        private const val KEY_AUTO_CONNECT = "auto_connect"
        private const val SYNC_INTERVAL = 5000L // 5 seconds
    }
    
    private val gson = Gson()
    private val prefs: SharedPreferences = context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
    
    private val httpClient = OkHttpClient.Builder()
        .connectTimeout(15, TimeUnit.SECONDS)
        .readTimeout(15, TimeUnit.SECONDS)
        .writeTimeout(15, TimeUnit.SECONDS)
        .retryOnConnectionFailure(true)
        .build()
    
    private var nodeId: String = ""
    private val scope = CoroutineScope(Dispatchers.IO + SupervisorJob())
    private var syncJob: Job? = null
    
    // Connection state
    private val _connectionState = MutableStateFlow(ConnectionState.DISCONNECTED)
    val connectionState: StateFlow<ConnectionState> = _connectionState
    
    private val _peers = MutableStateFlow<List<Peer>>(emptyList())
    val peers: StateFlow<List<Peer>> = _peers
    
    private val _chatMessages = MutableStateFlow<Map<String, List<ChatMessage>>>(emptyMap())
    val chatMessages: StateFlow<Map<String, List<ChatMessage>>> = _chatMessages
    
    private val _listings = MutableStateFlow<List<Listing>>(emptyList())
    val listings: StateFlow<List<Listing>> = _listings
    
    private val _domains = MutableStateFlow<List<Domain>>(emptyList())
    val domains: StateFlow<List<Domain>> = _domains
    
    private val _myDomains = MutableStateFlow<List<Domain>>(emptyList())
    val myDomains: StateFlow<List<Domain>> = _myDomains
    
    private val _networkStats = MutableStateFlow<NetworkStats?>(null)
    val networkStats: StateFlow<NetworkStats?> = _networkStats
    
    private val _chatRooms = MutableStateFlow<List<ChatRoom>>(emptyList())
    val chatRooms: StateFlow<List<ChatRoom>> = _chatRooms
    
    var bootstrapHost = "203.161.33.237"
        private set
    var bootstrapPort = 8080
        private set
    
    init {
        loadOrCreateIdentity()
        bootstrapHost = prefs.getString(KEY_BOOTSTRAP, "203.161.33.237") ?: "203.161.33.237"
    }
    
    private fun loadOrCreateIdentity() {
        val savedId = prefs.getString(KEY_NODE_ID, null)
        if (savedId != null) {
            nodeId = savedId
        } else {
            // Generate new identity
            val keyGen = KeyPairGenerator.getInstance("EC")
            keyGen.initialize(256, SecureRandom())
            val keyPair = keyGen.generateKeyPair()
            
            val digest = MessageDigest.getInstance("SHA-256")
            val hash = digest.digest(keyPair.public.encoded)
            nodeId = hash.joinToString("") { "%02x".format(it) }
            
            prefs.edit().putString(KEY_NODE_ID, nodeId).apply()
        }
        Log.d(TAG, "Node ID: ${nodeId.take(16)}...")
    }
    
    fun connect() {
        scope.launch {
            _connectionState.value = ConnectionState.CONNECTING
            
            try {
                val request = Request.Builder()
                    .url("http://$bootstrapHost:$bootstrapPort/api/stats")
                    .build()
                
                httpClient.newCall(request).execute().use { response ->
                    if (response.isSuccessful) {
                        val body = response.body?.string()
                        if (body != null) {
                            val stats = gson.fromJson(body, NetworkStats::class.java)
                            _networkStats.value = stats
                        }
                        _connectionState.value = ConnectionState.CONNECTED
                        startSyncLoop()
                        Log.d(TAG, "Connected to KayakNet")
                    } else {
                        _connectionState.value = ConnectionState.ERROR
                        Log.e(TAG, "Connection failed: ${response.code}")
                    }
                }
            } catch (e: Exception) {
                _connectionState.value = ConnectionState.ERROR
                Log.e(TAG, "Connection error: ${e.message}")
            }
        }
    }
    
    fun disconnect() {
        syncJob?.cancel()
        syncJob = null
        _connectionState.value = ConnectionState.DISCONNECTED
    }
    
    private fun startSyncLoop() {
        syncJob?.cancel()
        syncJob = scope.launch {
            while (isActive) {
                try {
                    syncAll()
                } catch (e: Exception) {
                    Log.e(TAG, "Sync error: ${e.message}")
                }
                delay(SYNC_INTERVAL)
            }
        }
    }
    
    private suspend fun syncAll() {
        coroutineScope {
            launch { fetchStats() }
            launch { fetchChatRooms() }
            launch { fetchListings() }
            launch { fetchDomains() }
            // Fetch messages for all known rooms
            _chatRooms.value.forEach { room ->
                launch { fetchChatHistory(room.name) }
            }
        }
    }
    
    private suspend fun fetchStats() {
        try {
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/stats")
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    val body = response.body?.string()
                    if (body != null) {
                        _networkStats.value = gson.fromJson(body, NetworkStats::class.java)
                    }
                }
            }
        } catch (e: Exception) {
            Log.e(TAG, "fetchStats error: ${e.message}")
        }
    }
    
    private suspend fun fetchChatRooms() {
        try {
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/chat/rooms")
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    val body = response.body?.string() ?: "[]"
                    val type = object : TypeToken<List<ChatRoom>>() {}.type
                    _chatRooms.value = gson.fromJson(body, type) ?: emptyList()
                }
            }
        } catch (e: Exception) {
            Log.e(TAG, "fetchChatRooms error: ${e.message}")
        }
    }
    
    suspend fun fetchChatHistory(room: String) {
        try {
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/chat?room=$room")
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    val body = response.body?.string() ?: "[]"
                    val type = object : TypeToken<List<ChatMessage>>() {}.type
                    val messages: List<ChatMessage> = gson.fromJson(body, type) ?: emptyList()
                    
                    val currentMap = _chatMessages.value.toMutableMap()
                    currentMap[room] = messages
                    _chatMessages.value = currentMap
                }
            }
        } catch (e: Exception) {
            Log.e(TAG, "fetchChatHistory error: ${e.message}")
        }
    }
    
    fun getMessagesForRoom(room: String): List<ChatMessage> {
        return _chatMessages.value[room] ?: emptyList()
    }
    
    suspend fun sendChatMessage(room: String, message: String): Boolean {
        return try {
            val formBody = FormBody.Builder()
                .add("room", room)
                .add("message", message)
                .build()
            
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/chat")
                .post(formBody)
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    // Immediately refresh to get the new message
                    fetchChatHistory(room)
                    true
                } else {
                    Log.e(TAG, "sendChatMessage failed: ${response.code}")
                    false
                }
            }
        } catch (e: Exception) {
            Log.e(TAG, "sendChatMessage error: ${e.message}")
            false
        }
    }
    
    private suspend fun fetchListings() {
        try {
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/listings")
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    val body = response.body?.string() ?: "[]"
                    val type = object : TypeToken<List<Listing>>() {}.type
                    _listings.value = gson.fromJson(body, type) ?: emptyList()
                }
            }
        } catch (e: Exception) {
            Log.e(TAG, "fetchListings error: ${e.message}")
        }
    }
    
    private suspend fun fetchDomains() {
        try {
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/domains")
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    val body = response.body?.string() ?: "[]"
                    val type = object : TypeToken<List<Domain>>() {}.type
                    _domains.value = gson.fromJson(body, type) ?: emptyList()
                }
            }
            
            // Also fetch my domains
            val myRequest = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/domains/my?owner=$nodeId")
                .build()
            
            httpClient.newCall(myRequest).execute().use { response ->
                if (response.isSuccessful) {
                    val body = response.body?.string() ?: "[]"
                    val type = object : TypeToken<List<Domain>>() {}.type
                    _myDomains.value = gson.fromJson(body, type) ?: emptyList()
                }
            }
        } catch (e: Exception) {
            Log.e(TAG, "fetchDomains error: ${e.message}")
        }
    }
    
    suspend fun registerDomain(name: String, description: String): Result<Domain> {
        return try {
            val formBody = FormBody.Builder()
                .add("name", name)
                .add("description", description)
                .add("owner", nodeId)
                .build()
            
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/domains/register")
                .post(formBody)
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                val body = response.body?.string() ?: "{}"
                
                if (response.isSuccessful) {
                    fetchDomains()
                    val domain = Domain(
                        name = name,
                        fullName = "$name.kyk",
                        description = description,
                        owner = nodeId
                    )
                    Result.success(domain)
                } else {
                    Result.failure(Exception("Registration failed: $body"))
                }
            }
        } catch (e: Exception) {
            Result.failure(e)
        }
    }
    
    suspend fun createListing(
        title: String,
        description: String,
        price: Double,
        currency: String,
        category: String
    ): Result<Listing> {
        return try {
            val formBody = FormBody.Builder()
                .add("title", title)
                .add("description", description)
                .add("price", price.toString())
                .add("currency", currency)
                .add("category", category)
                .add("seller", nodeId)
                .add("seller_name", getLocalNick())
                .build()
            
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/create-listing")
                .post(formBody)
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    fetchListings()
                    val listing = Listing(
                        id = "",
                        title = title,
                        description = description,
                        price = price,
                        currency = currency,
                        category = category,
                        seller = nodeId,
                        sellerName = getLocalNick()
                    )
                    Result.success(listing)
                } else {
                    Result.failure(Exception("Failed to create listing"))
                }
            }
        } catch (e: Exception) {
            Result.failure(e)
        }
    }
    
    suspend fun resolveDomain(name: String): Domain? {
        return try {
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/domains/resolve?name=$name")
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    val body = response.body?.string()
                    if (body != null && body != "null") {
                        gson.fromJson(body, Domain::class.java)
                    } else null
                } else null
            }
        } catch (e: Exception) {
            null
        }
    }
    
    fun getNodeId(): String = nodeId
    
    fun getLocalNick(): String {
        return prefs.getString(KEY_NICK, "anon-${nodeId.take(8)}") ?: "anon-${nodeId.take(8)}"
    }
    
    fun setLocalNick(nick: String) {
        prefs.edit().putString(KEY_NICK, nick).apply()
    }
    
    fun setBootstrapHost(host: String) {
        bootstrapHost = host
        prefs.edit().putString(KEY_BOOTSTRAP, host).apply()
    }
    
    fun isAutoConnectEnabled(): Boolean {
        return prefs.getBoolean(KEY_AUTO_CONNECT, true)
    }
    
    fun setAutoConnect(enabled: Boolean) {
        prefs.edit().putBoolean(KEY_AUTO_CONNECT, enabled).apply()
    }
    
    fun forceRefresh() {
        scope.launch {
            syncAll()
        }
    }
}

enum class ConnectionState {
    DISCONNECTED,
    CONNECTING,
    CONNECTED,
    ERROR
}

data class NetworkStats(
    val peers: Int = 0,
    val listings: Int = 0,
    val domains: Int = 0,
    val messages: Int = 0,
    val version: String = ""
)

data class Peer(
    val id: String,
    val address: String,
    val lastSeen: Long
)

data class ChatRoom(
    val name: String,
    val description: String = "",
    val members: Int = 0
)

data class ChatMessage(
    val id: String = "",
    val type: String = "message",
    val room: String = "",
    val sender_id: String = "",
    val nick: String = "",
    val content: String = "",
    val timestamp: String = "",
    val reactions: Map<String, List<String>>? = null,
    val media: MediaAttachment? = null
)

data class MediaAttachment(
    val type: String = "",
    val name: String = "",
    val data: String = ""
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
