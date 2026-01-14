package net.kayaknet.app.network

import android.content.Context
import android.content.SharedPreferences
import android.util.Base64
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
        private const val KEY_PROFILE_BIO = "profile_bio"
        private const val KEY_PROFILE_STATUS = "profile_status"
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
    
    // DM Features
    private val _conversations = MutableStateFlow<List<Conversation>>(emptyList())
    val conversations: StateFlow<List<Conversation>> = _conversations
    
    private val _dmMessages = MutableStateFlow<Map<String, List<ChatMessage>>>(emptyMap())
    val dmMessages: StateFlow<Map<String, List<ChatMessage>>> = _dmMessages
    
    private val _unreadDMs = MutableStateFlow(0)
    val unreadDMs: StateFlow<Int> = _unreadDMs
    
    private val _typingUsers = MutableStateFlow<Map<String, Boolean>>(emptyMap())
    val typingUsers: StateFlow<Map<String, Boolean>> = _typingUsers
    
    // Users
    private val _onlineUsers = MutableStateFlow<List<User>>(emptyList())
    val onlineUsers: StateFlow<List<User>> = _onlineUsers
    
    private val _blockedUsers = MutableStateFlow<Set<String>>(emptySet())
    val blockedUsers: StateFlow<Set<String>> = _blockedUsers
    
    // Escrow
    private val _myEscrows = MutableStateFlow<List<Escrow>>(emptyList())
    val myEscrows: StateFlow<List<Escrow>> = _myEscrows
    
    var bootstrapHost = "203.161.33.237"
        private set
    var bootstrapPort = 8080
        private set
    
    init {
        loadOrCreateIdentity()
        bootstrapHost = prefs.getString(KEY_BOOTSTRAP, "203.161.33.237") ?: "203.161.33.237"
        loadBlockedUsers()
    }
    
    private fun loadOrCreateIdentity() {
        val savedId = prefs.getString(KEY_NODE_ID, null)
        if (savedId != null) {
            nodeId = savedId
        } else {
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
    
    private fun loadBlockedUsers() {
        val blocked = prefs.getStringSet("blocked_users", emptySet()) ?: emptySet()
        _blockedUsers.value = blocked
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
            launch { fetchConversations() }
            launch { fetchUnreadCount() }
            launch { fetchOnlineUsers() }
            
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
                    currentMap[room] = messages.filter { msg ->
                        !_blockedUsers.value.contains(msg.sender_id)
                    }
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
    
    suspend fun sendChatMessage(room: String, message: String, replyTo: String? = null): Boolean {
        return try {
            val formBuilder = FormBody.Builder()
                .add("room", room)
                .add("message", message)
            
            replyTo?.let { formBuilder.add("reply_to", it) }
            
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/chat")
                .post(formBuilder.build())
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    delay(100)
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
    
    suspend fun sendChatMessageWithMedia(room: String, message: String, mediaType: String, mediaName: String, mediaData: String): Boolean {
        return try {
            val formBody = FormBody.Builder()
                .add("room", room)
                .add("message", message)
                .add("media_type", mediaType)
                .add("media_name", mediaName)
                .add("media_data", mediaData)
                .build()
            
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/chat")
                .post(formBody)
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    delay(100)
                    fetchChatHistory(room)
                    true
                } else false
            }
        } catch (e: Exception) {
            Log.e(TAG, "sendChatMessageWithMedia error: ${e.message}")
            false
        }
    }
    
    // ===== DM FEATURES =====
    
    private suspend fun fetchConversations() {
        try {
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/chat/conversations")
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    val body = response.body?.string() ?: "[]"
                    val type = object : TypeToken<List<Conversation>>() {}.type
                    val convs: List<Conversation> = gson.fromJson(body, type) ?: emptyList()
                    _conversations.value = convs.filter { conv ->
                        !conv.participants.all { _blockedUsers.value.contains(it) }
                    }
                }
            }
        } catch (e: Exception) {
            Log.e(TAG, "fetchConversations error: ${e.message}")
        }
    }
    
    private suspend fun fetchUnreadCount() {
        try {
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/chat/dm/unread")
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    val body = response.body?.string() ?: "{}"
                    val data = gson.fromJson(body, Map::class.java)
                    _unreadDMs.value = (data["unread"] as? Double)?.toInt() ?: 0
                }
            }
        } catch (e: Exception) {
            Log.e(TAG, "fetchUnreadCount error: ${e.message}")
        }
    }
    
    suspend fun fetchDMMessages(userId: String) {
        try {
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/chat/dm?user=$userId")
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    val body = response.body?.string() ?: "[]"
                    val type = object : TypeToken<List<ChatMessage>>() {}.type
                    val messages: List<ChatMessage> = gson.fromJson(body, type) ?: emptyList()
                    
                    val currentMap = _dmMessages.value.toMutableMap()
                    currentMap[userId] = messages
                    _dmMessages.value = currentMap
                }
            }
            
            // Mark as read
            markDMAsRead(userId)
        } catch (e: Exception) {
            Log.e(TAG, "fetchDMMessages error: ${e.message}")
        }
    }
    
    suspend fun sendDM(userId: String, message: String, replyTo: String? = null): Boolean {
        if (_blockedUsers.value.contains(userId)) return false
        
        return try {
            val formBuilder = FormBody.Builder()
                .add("user", userId)
                .add("message", message)
            
            replyTo?.let { formBuilder.add("reply_to", it) }
            
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/chat/dm")
                .post(formBuilder.build())
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    delay(100)
                    fetchDMMessages(userId)
                    fetchConversations()
                    true
                } else false
            }
        } catch (e: Exception) {
            Log.e(TAG, "sendDM error: ${e.message}")
            false
        }
    }
    
    suspend fun sendDMWithMedia(userId: String, message: String, mediaType: String, mediaName: String, mediaData: String): Boolean {
        return try {
            val formBody = FormBody.Builder()
                .add("user", userId)
                .add("message", message)
                .add("media_type", mediaType)
                .add("media_name", mediaName)
                .add("media_data", mediaData)
                .build()
            
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/chat/dm")
                .post(formBody)
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    delay(100)
                    fetchDMMessages(userId)
                    true
                } else false
            }
        } catch (e: Exception) {
            Log.e(TAG, "sendDMWithMedia error: ${e.message}")
            false
        }
    }
    
    suspend fun markDMAsRead(userId: String) {
        try {
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/chat/dm/read?user=$userId")
                .build()
            
            httpClient.newCall(request).execute()
            fetchUnreadCount()
        } catch (e: Exception) {
            Log.e(TAG, "markDMAsRead error: ${e.message}")
        }
    }
    
    suspend fun setDMTyping(userId: String, typing: Boolean) {
        try {
            val formBody = FormBody.Builder()
                .add("user", userId)
                .add("typing", typing.toString())
                .build()
            
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/chat/dm/typing")
                .post(formBody)
                .build()
            
            httpClient.newCall(request).execute()
        } catch (e: Exception) {
            Log.e(TAG, "setDMTyping error: ${e.message}")
        }
    }
    
    suspend fun getDMTyping(userId: String): List<String> {
        return try {
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/chat/dm/typing?user=$userId")
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    val body = response.body?.string() ?: "{}"
                    val data = gson.fromJson(body, Map::class.java)
                    @Suppress("UNCHECKED_CAST")
                    (data["typing"] as? List<String>) ?: emptyList()
                } else emptyList()
            }
        } catch (e: Exception) {
            emptyList()
        }
    }
    
    suspend fun pinConversation(userId: String, pinned: Boolean) {
        try {
            val formBody = FormBody.Builder()
                .add("user", userId)
                .add("pinned", pinned.toString())
                .build()
            
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/chat/dm/pin")
                .post(formBody)
                .build()
            
            httpClient.newCall(request).execute()
            fetchConversations()
        } catch (e: Exception) {
            Log.e(TAG, "pinConversation error: ${e.message}")
        }
    }
    
    suspend fun muteConversation(userId: String, muted: Boolean) {
        try {
            val formBody = FormBody.Builder()
                .add("user", userId)
                .add("muted", muted.toString())
                .build()
            
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/chat/dm/mute")
                .post(formBody)
                .build()
            
            httpClient.newCall(request).execute()
            fetchConversations()
        } catch (e: Exception) {
            Log.e(TAG, "muteConversation error: ${e.message}")
        }
    }
    
    suspend fun archiveConversation(userId: String) {
        try {
            val formBody = FormBody.Builder()
                .add("user", userId)
                .add("archived", "true")
                .build()
            
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/chat/dm/archive")
                .post(formBody)
                .build()
            
            httpClient.newCall(request).execute()
            fetchConversations()
        } catch (e: Exception) {
            Log.e(TAG, "archiveConversation error: ${e.message}")
        }
    }
    
    suspend fun deleteConversation(userId: String) {
        try {
            val formBody = FormBody.Builder()
                .add("user", userId)
                .add("all", "true")
                .build()
            
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/chat/dm/delete")
                .post(formBody)
                .build()
            
            httpClient.newCall(request).execute()
            fetchConversations()
        } catch (e: Exception) {
            Log.e(TAG, "deleteConversation error: ${e.message}")
        }
    }
    
    suspend fun searchDM(userId: String, query: String): List<ChatMessage> {
        return try {
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/chat/dm/search?user=$userId&q=$query")
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    val body = response.body?.string() ?: "[]"
                    val type = object : TypeToken<List<ChatMessage>>() {}.type
                    gson.fromJson(body, type) ?: emptyList()
                } else emptyList()
            }
        } catch (e: Exception) {
            Log.e(TAG, "searchDM error: ${e.message}")
            emptyList()
        }
    }
    
    // ===== USER FEATURES =====
    
    private suspend fun fetchOnlineUsers() {
        try {
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/chat/users")
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    val body = response.body?.string() ?: "[]"
                    val type = object : TypeToken<List<User>>() {}.type
                    val users: List<User> = gson.fromJson(body, type) ?: emptyList()
                    _onlineUsers.value = users.filter { !_blockedUsers.value.contains(it.id) }
                }
            }
        } catch (e: Exception) {
            Log.e(TAG, "fetchOnlineUsers error: ${e.message}")
        }
    }
    
    suspend fun getUser(userId: String): User? {
        return try {
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/chat/user?id=$userId")
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    val body = response.body?.string()
                    if (body != null && body != "null") {
                        gson.fromJson(body, User::class.java)
                    } else null
                } else null
            }
        } catch (e: Exception) {
            null
        }
    }
    
    suspend fun searchUsers(query: String): List<User> {
        return try {
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/chat/search-users?q=$query")
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    val body = response.body?.string() ?: "[]"
                    val type = object : TypeToken<List<User>>() {}.type
                    val users: List<User> = gson.fromJson(body, type) ?: emptyList()
                    users.filter { !_blockedUsers.value.contains(it.id) }
                } else emptyList()
            }
        } catch (e: Exception) {
            Log.e(TAG, "searchUsers error: ${e.message}")
            emptyList()
        }
    }
    
    suspend fun blockUser(userId: String) {
        try {
            val formBody = FormBody.Builder()
                .add("user", userId)
                .build()
            
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/chat/block")
                .post(formBody)
                .build()
            
            httpClient.newCall(request).execute()
            
            val blocked = _blockedUsers.value.toMutableSet()
            blocked.add(userId)
            _blockedUsers.value = blocked
            prefs.edit().putStringSet("blocked_users", blocked).apply()
        } catch (e: Exception) {
            Log.e(TAG, "blockUser error: ${e.message}")
        }
    }
    
    fun unblockUser(userId: String) {
        val blocked = _blockedUsers.value.toMutableSet()
        blocked.remove(userId)
        _blockedUsers.value = blocked
        prefs.edit().putStringSet("blocked_users", blocked).apply()
    }
    
    suspend fun updateProfile(nick: String, bio: String, status: String) {
        setLocalNick(nick)
        prefs.edit()
            .putString(KEY_PROFILE_BIO, bio)
            .putString(KEY_PROFILE_STATUS, status)
            .apply()
        
        try {
            val formBody = FormBody.Builder()
                .add("nick", nick)
                .add("bio", bio)
                .add("status", status)
                .build()
            
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/chat/profile")
                .post(formBody)
                .build()
            
            httpClient.newCall(request).execute()
        } catch (e: Exception) {
            Log.e(TAG, "updateProfile error: ${e.message}")
        }
    }
    
    // ===== MARKETPLACE FEATURES =====
    
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
    
    suspend fun searchListings(query: String, category: String? = null): List<Listing> {
        var url = "http://$bootstrapHost:$bootstrapPort/api/listings?search=$query"
        if (!category.isNullOrEmpty()) {
            url += "&category=$category"
        }
        
        return try {
            val request = Request.Builder()
                .url(url)
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    val body = response.body?.string() ?: "[]"
                    val type = object : TypeToken<List<Listing>>() {}.type
                    gson.fromJson(body, type) ?: emptyList()
                } else emptyList()
            }
        } catch (e: Exception) {
            Log.e(TAG, "searchListings error: ${e.message}")
            emptyList()
        }
    }
    
    suspend fun createListing(
        title: String,
        description: String,
        price: Double,
        currency: String,
        category: String,
        imageData: String? = null
    ): Result<Listing> {
        return try {
            val formBuilder = FormBody.Builder()
                .add("title", title)
                .add("description", description)
                .add("price", price.toString())
                .add("currency", currency)
                .add("category", category)
                .add("seller", nodeId)
                .add("seller_name", getLocalNick())
            
            imageData?.let { formBuilder.add("image", it) }
            
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/create-listing")
                .post(formBuilder.build())
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
    
    suspend fun getListing(listingId: String): Listing? {
        return _listings.value.find { it.id == listingId }
    }
    
    // ===== ESCROW FEATURES =====
    
    suspend fun createEscrow(listingId: String, amount: Double, currency: String): Result<Escrow> {
        return try {
            val formBody = FormBody.Builder()
                .add("listing_id", listingId)
                .add("amount", amount.toString())
                .add("currency", currency)
                .add("buyer", nodeId)
                .build()
            
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/escrow/create")
                .post(formBody)
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    val body = response.body?.string() ?: "{}"
                    val escrow = gson.fromJson(body, Escrow::class.java)
                    fetchMyEscrows()
                    Result.success(escrow)
                } else {
                    Result.failure(Exception("Failed to create escrow"))
                }
            }
        } catch (e: Exception) {
            Result.failure(e)
        }
    }
    
    suspend fun fetchMyEscrows() {
        try {
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/escrow/my?user=$nodeId")
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    val body = response.body?.string() ?: "[]"
                    val type = object : TypeToken<List<Escrow>>() {}.type
                    _myEscrows.value = gson.fromJson(body, type) ?: emptyList()
                }
            }
        } catch (e: Exception) {
            Log.e(TAG, "fetchMyEscrows error: ${e.message}")
        }
    }
    
    suspend fun releaseEscrow(escrowId: String): Boolean {
        return try {
            val formBody = FormBody.Builder()
                .add("escrow_id", escrowId)
                .add("user", nodeId)
                .build()
            
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/escrow/release")
                .post(formBody)
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    fetchMyEscrows()
                    true
                } else false
            }
        } catch (e: Exception) {
            Log.e(TAG, "releaseEscrow error: ${e.message}")
            false
        }
    }
    
    suspend fun disputeEscrow(escrowId: String, reason: String): Boolean {
        return try {
            val formBody = FormBody.Builder()
                .add("escrow_id", escrowId)
                .add("reason", reason)
                .add("user", nodeId)
                .build()
            
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/escrow/dispute")
                .post(formBody)
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    fetchMyEscrows()
                    true
                } else false
            }
        } catch (e: Exception) {
            Log.e(TAG, "disputeEscrow error: ${e.message}")
            false
        }
    }
    
    // ===== DOMAIN FEATURES =====
    
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
    
    suspend fun registerDomain(name: String, description: String, serviceType: String = ""): Result<Domain> {
        return try {
            val formBody = FormBody.Builder()
                .add("name", name)
                .add("description", description)
                .add("service_type", serviceType)
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
                        owner = nodeId,
                        serviceType = serviceType
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
    
    suspend fun renewDomain(name: String): Boolean {
        return try {
            val formBody = FormBody.Builder()
                .add("name", name)
                .add("owner", nodeId)
                .build()
            
            val request = Request.Builder()
                .url("http://$bootstrapHost:$bootstrapPort/api/domains/renew")
                .post(formBody)
                .build()
            
            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    fetchDomains()
                    true
                } else false
            }
        } catch (e: Exception) {
            Log.e(TAG, "renewDomain error: ${e.message}")
            false
        }
    }
    
    // ===== UTILITY =====
    
    fun getNodeId(): String = nodeId
    
    fun getLocalNick(): String {
        return prefs.getString(KEY_NICK, "anon-${nodeId.take(8)}") ?: "anon-${nodeId.take(8)}"
    }
    
    fun setLocalNick(nick: String) {
        prefs.edit().putString(KEY_NICK, nick).apply()
    }
    
    fun getProfileBio(): String {
        return prefs.getString(KEY_PROFILE_BIO, "") ?: ""
    }
    
    fun getProfileStatus(): String {
        return prefs.getString(KEY_PROFILE_STATUS, "online") ?: "online"
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
    val members: Int = 0,
    val topic: String = ""
)

data class ChatMessage(
    val id: String = "",
    val type: Int = 0,
    val room: String = "",
    val sender_id: String = "",
    val receiver_id: String = "",
    val nick: String = "",
    val content: String = "",
    val timestamp: String = "",
    val reactions: Map<String, List<String>>? = null,
    val media: MediaAttachment? = null,
    val reply_to: String? = null,
    val edited: Boolean = false
)

data class MediaAttachment(
    val type: String = "",
    val name: String = "",
    val data: String = "",
    val size: Long = 0
)

data class Conversation(
    val id: String = "",
    val participants: List<String> = emptyList(),
    val last_message: String = "",
    val unread: Int = 0,
    val pinned: Boolean = false,
    val muted: Boolean = false,
    val archived: Boolean = false
)

data class User(
    val id: String = "",
    val nick: String = "",
    val status: String = "online",
    val status_msg: String = "",
    val bio: String = "",
    val avatar: String = "",
    val last_seen: String = "",
    val allow_dms: Boolean = true
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
    val imageUrl: String? = null,
    val image: String? = null,
    val created_at: String? = null
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

data class Escrow(
    val id: String = "",
    val listing_id: String = "",
    val buyer: String = "",
    val seller: String = "",
    val amount: Double = 0.0,
    val currency: String = "",
    val status: String = "",
    val address: String = "",
    val created_at: String = ""
)
