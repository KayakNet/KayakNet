package net.kayaknet.app.ui.screens

import android.net.Uri
import android.util.Base64
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import net.kayaknet.app.KayakNetApp
import net.kayaknet.app.network.ChatMessage
import net.kayaknet.app.network.ConnectionState
import net.kayaknet.app.network.Conversation
import net.kayaknet.app.network.User
import java.text.SimpleDateFormat
import java.util.*

@OptIn(ExperimentalMaterial3Api::class, ExperimentalFoundationApi::class)
@Composable
fun ChatScreen() {
    val client = KayakNetApp.instance.client
    val connectionState by client.connectionState.collectAsState()
    val chatMessages by client.chatMessages.collectAsState()
    val chatRooms by client.chatRooms.collectAsState()
    val conversations by client.conversations.collectAsState()
    val dmMessages by client.dmMessages.collectAsState()
    val unreadDMs by client.unreadDMs.collectAsState()
    val onlineUsers by client.onlineUsers.collectAsState()
    val scope = rememberCoroutineScope()
    val context = LocalContext.current
    
    var messageText by remember { mutableStateOf("") }
    var currentRoom by remember { mutableStateOf("general") }
    var currentDM by remember { mutableStateOf<String?>(null) }
    var isSending by remember { mutableStateOf(false) }
    var isRefreshing by remember { mutableStateOf(false) }
    var showUserSearch by remember { mutableStateOf(false) }
    var showDMOptions by remember { mutableStateOf(false) }
    var selectedConversation by remember { mutableStateOf<String?>(null) }
    var replyToMessage by remember { mutableStateOf<ChatMessage?>(null) }
    var searchQuery by remember { mutableStateOf("") }
    var tabIndex by remember { mutableStateOf(0) } // 0 = Rooms, 1 = DMs
    
    val messages = if (currentDM != null) {
        dmMessages[currentDM] ?: emptyList()
    } else {
        chatMessages[currentRoom] ?: emptyList()
    }
    val listState = rememberLazyListState()
    
    // File picker
    val filePickerLauncher = rememberLauncherForActivityResult(
        contract = ActivityResultContracts.GetContent()
    ) { uri: Uri? ->
        uri?.let {
            scope.launch {
                try {
                    val inputStream = context.contentResolver.openInputStream(uri)
                    val bytes = inputStream?.readBytes()
                    inputStream?.close()
                    if (bytes != null && bytes.size <= 1024 * 1024) {
                        val base64 = Base64.encodeToString(bytes, Base64.NO_WRAP)
                        val mimeType = context.contentResolver.getType(uri) ?: "application/octet-stream"
                        val fileName = uri.lastPathSegment ?: "file"
                        
                        if (currentDM != null) {
                            client.sendDMWithMedia(currentDM!!, messageText, mimeType, fileName, base64)
                        } else {
                            client.sendChatMessageWithMedia(currentRoom, messageText, mimeType, fileName, base64)
                        }
                        messageText = ""
                    }
                } catch (e: Exception) {
                    // Handle error
                }
            }
        }
    }
    
    // Auto-scroll to bottom when new messages arrive
    LaunchedEffect(messages.size) {
        if (messages.isNotEmpty()) {
            listState.animateScrollToItem(messages.size - 1)
        }
    }
    
    // Initial fetch
    LaunchedEffect(currentRoom, currentDM) {
        if (currentDM != null) {
            client.fetchDMMessages(currentDM!!)
        } else {
            client.fetchChatHistory(currentRoom)
        }
    }
    
    // Typing indicator polling for DMs
    LaunchedEffect(currentDM) {
        if (currentDM != null) {
            while (true) {
                delay(2000)
                // Polling handled in client
            }
        }
    }
    
    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(Color(0xFF0A0A0A))
    ) {
        // Tab selector
        TabRow(
            selectedTabIndex = tabIndex,
            containerColor = Color(0xFF0A0A0A),
            contentColor = Color(0xFF00FF00)
        ) {
            Tab(
                selected = tabIndex == 0,
                onClick = { 
                    tabIndex = 0
                    currentDM = null
                },
                text = { Text("CHANNELS") }
            )
            Tab(
                selected = tabIndex == 1,
                onClick = { tabIndex = 1 },
                text = { 
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text("DMs")
                        if (unreadDMs > 0) {
                            Spacer(modifier = Modifier.width(4.dp))
                            Badge(
                                containerColor = Color.Red,
                                contentColor = Color.White
                            ) {
                                Text(unreadDMs.toString())
                            }
                        }
                    }
                }
            )
        }
        
        Row(modifier = Modifier.weight(1f)) {
            // Sidebar
            Column(
                modifier = Modifier
                    .width(100.dp)
                    .fillMaxHeight()
                    .background(Color(0xFF0D0D0D))
                    .padding(8.dp)
            ) {
                if (tabIndex == 0) {
                    // Rooms list
                    Text(
                        "ROOMS",
                        color = Color(0xFF00FF00),
                        fontSize = 10.sp,
                        fontWeight = FontWeight.Bold
                    )
                    Spacer(modifier = Modifier.height(8.dp))
                    
                    val rooms = if (chatRooms.isNotEmpty()) {
                        chatRooms.map { it.name }
                    } else {
                        listOf("general", "market", "help", "random")
                    }
                    
                    LazyColumn {
                        items(rooms) { room ->
                            Text(
                                "#$room",
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .clickable { 
                                        currentRoom = room
                                        currentDM = null
                                    }
                                    .background(
                                        if (currentRoom == room && currentDM == null) 
                                            Color(0xFF00FF00).copy(alpha = 0.1f)
                                        else Color.Transparent
                                    )
                                    .padding(8.dp),
                                color = if (currentRoom == room && currentDM == null) 
                                    Color(0xFF00FF00) 
                                else 
                                    Color(0xFF00FF00).copy(alpha = 0.6f),
                                fontSize = 12.sp
                            )
                        }
                    }
                } else {
                    // DMs list
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.SpaceBetween,
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        Text(
                            "DMs",
                            color = Color(0xFF00FF00),
                            fontSize = 10.sp,
                            fontWeight = FontWeight.Bold
                        )
                        IconButton(
                            onClick = { showUserSearch = true },
                            modifier = Modifier.size(20.dp)
                        ) {
                            Icon(
                                Icons.Filled.Add,
                                contentDescription = "New DM",
                                tint = Color(0xFF00FF00),
                                modifier = Modifier.size(16.dp)
                            )
                        }
                    }
                    Spacer(modifier = Modifier.height(8.dp))
                    
                    LazyColumn {
                        items(conversations) { conv ->
                            val otherUser = conv.participants.find { it != client.getNodeId() } ?: conv.participants.firstOrNull() ?: ""
                            Row(
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .combinedClickable(
                                        onClick = { 
                                            currentDM = otherUser
                                            currentRoom = ""
                                        },
                                        onLongClick = {
                                            selectedConversation = otherUser
                                            showDMOptions = true
                                        }
                                    )
                                    .background(
                                        if (currentDM == otherUser) 
                                            Color(0xFF00FF00).copy(alpha = 0.1f)
                                        else Color.Transparent
                                    )
                                    .padding(8.dp),
                                horizontalArrangement = Arrangement.SpaceBetween,
                                verticalAlignment = Alignment.CenterVertically
                            ) {
                                Column(modifier = Modifier.weight(1f)) {
                                    Row(verticalAlignment = Alignment.CenterVertically) {
                                        if (conv.pinned) {
                                            Icon(
                                                Icons.Filled.Star,
                                                contentDescription = "Pinned",
                                                tint = Color(0xFF00FF00),
                                                modifier = Modifier.size(10.dp)
                                            )
                                            Spacer(modifier = Modifier.width(2.dp))
                                        }
                                        Text(
                                            otherUser.take(8),
                                            color = Color(0xFF00FF00),
                                            fontSize = 11.sp,
                                            maxLines = 1,
                                            overflow = TextOverflow.Ellipsis
                                        )
                                    }
                                }
                                if (conv.unread > 0) {
                                    Badge(
                                        containerColor = Color.Red,
                                        contentColor = Color.White
                                    ) {
                                        Text(
                                            conv.unread.toString(),
                                            fontSize = 9.sp
                                        )
                                    }
                                }
                            }
                        }
                    }
                }
            }
            
            // Main chat area
            Column(
                modifier = Modifier
                    .weight(1f)
                    .fillMaxHeight()
                    .padding(8.dp)
            ) {
                // Header
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(bottom = 8.dp),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    Text(
                        text = if (currentDM != null) "DM: ${currentDM?.take(12)}" else "#$currentRoom",
                        color = Color(0xFF00FF00),
                        fontWeight = FontWeight.Bold,
                        fontSize = 14.sp
                    )
                    
                    Row {
                        IconButton(
                            onClick = {
                                isRefreshing = true
                                scope.launch {
                                    if (currentDM != null) {
                                        client.fetchDMMessages(currentDM!!)
                                    } else {
                                        client.fetchChatHistory(currentRoom)
                                    }
                                    isRefreshing = false
                                }
                            },
                            modifier = Modifier.size(32.dp)
                        ) {
                            Icon(
                                Icons.Filled.Refresh,
                                contentDescription = "Refresh",
                                tint = Color(0xFF00FF00),
                                modifier = Modifier.size(18.dp)
                            )
                        }
                    }
                }
                
                // Messages
                Box(
                    modifier = Modifier
                        .weight(1f)
                        .fillMaxWidth()
                        .border(1.dp, Color(0xFF00FF00).copy(alpha = 0.3f))
                        .background(Color(0xFF050505))
                ) {
                    if (connectionState != ConnectionState.CONNECTED) {
                        Box(
                            modifier = Modifier.fillMaxSize(),
                            contentAlignment = Alignment.Center
                        ) {
                            Column(horizontalAlignment = Alignment.CenterHorizontally) {
                                Text(
                                    text = "// NOT CONNECTED",
                                    color = Color(0xFF00FF00).copy(alpha = 0.5f)
                                )
                                Spacer(modifier = Modifier.height(8.dp))
                                Button(
                                    onClick = { client.connect() },
                                    colors = ButtonDefaults.buttonColors(
                                        containerColor = Color(0xFF00FF00),
                                        contentColor = Color.Black
                                    )
                                ) {
                                    Text("CONNECT")
                                }
                            }
                        }
                    } else if (messages.isEmpty()) {
                        Box(
                            modifier = Modifier.fillMaxSize(),
                            contentAlignment = Alignment.Center
                        ) {
                            Text(
                                text = "// No messages",
                                color = Color(0xFF00FF00).copy(alpha = 0.5f)
                            )
                        }
                    } else {
                        LazyColumn(
                            state = listState,
                            modifier = Modifier
                                .fillMaxSize()
                                .padding(8.dp),
                            verticalArrangement = Arrangement.spacedBy(4.dp)
                        ) {
                            items(messages) { message ->
                                ChatMessageItem(
                                    message = message,
                                    isOwnMessage = message.sender_id == client.getNodeId(),
                                    onReply = { replyToMessage = message },
                                    onUserClick = { userId ->
                                        currentDM = userId
                                        tabIndex = 1
                                    }
                                )
                            }
                        }
                    }
                }
                
                // Reply preview
                if (replyToMessage != null) {
                    Row(
                        modifier = Modifier
                            .fillMaxWidth()
                            .background(Color(0xFF00FF00).copy(alpha = 0.1f))
                            .padding(8.dp),
                        horizontalArrangement = Arrangement.SpaceBetween,
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        Column(modifier = Modifier.weight(1f)) {
                            Text(
                                "Reply to ${replyToMessage?.nick ?: "anon"}:",
                                color = Color(0xFF00FF00),
                                fontSize = 10.sp
                            )
                            Text(
                                replyToMessage?.content?.take(50) ?: "",
                                color = Color(0xFF00FF00).copy(alpha = 0.7f),
                                fontSize = 11.sp,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis
                            )
                        }
                        IconButton(
                            onClick = { replyToMessage = null },
                            modifier = Modifier.size(24.dp)
                        ) {
                            Icon(
                                Icons.Filled.Close,
                                contentDescription = "Cancel",
                                tint = Color(0xFF00FF00),
                                modifier = Modifier.size(16.dp)
                            )
                        }
                    }
                }
                
                Spacer(modifier = Modifier.height(8.dp))
                
                // Input
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.spacedBy(4.dp),
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    IconButton(
                        onClick = { filePickerLauncher.launch("*/*") },
                        modifier = Modifier.size(36.dp)
                    ) {
                        Icon(
                            Icons.Filled.Add,
                            contentDescription = "Attach",
                            tint = Color(0xFF00FF00)
                        )
                    }
                    
                    OutlinedTextField(
                        value = messageText,
                        onValueChange = { messageText = it },
                        modifier = Modifier.weight(1f),
                        placeholder = { 
                            Text(
                                "Type message...",
                                color = Color(0xFF00FF00).copy(alpha = 0.5f)
                            ) 
                        },
                        enabled = connectionState == ConnectionState.CONNECTED && !isSending,
                        singleLine = true,
                        colors = OutlinedTextFieldDefaults.colors(
                            focusedBorderColor = Color(0xFF00FF00),
                            unfocusedBorderColor = Color(0xFF00FF00).copy(alpha = 0.3f),
                            cursorColor = Color(0xFF00FF00),
                            focusedTextColor = Color(0xFF00FF00),
                            unfocusedTextColor = Color(0xFF00FF00)
                        )
                    )
                    
                    IconButton(
                        onClick = {
                            if (messageText.isNotBlank() && !isSending) {
                                isSending = true
                                scope.launch {
                                    val success = if (currentDM != null) {
                                        client.sendDM(currentDM!!, messageText.trim(), replyToMessage?.id)
                                    } else {
                                        client.sendChatMessage(currentRoom, messageText.trim(), replyToMessage?.id)
                                    }
                                    if (success) {
                                        messageText = ""
                                        replyToMessage = null
                                    }
                                    isSending = false
                                }
                            }
                        },
                        enabled = connectionState == ConnectionState.CONNECTED && messageText.isNotBlank() && !isSending,
                        modifier = Modifier.size(36.dp)
                    ) {
                        if (isSending) {
                            CircularProgressIndicator(
                                modifier = Modifier.size(20.dp),
                                color = Color(0xFF00FF00),
                                strokeWidth = 2.dp
                            )
                        } else {
                            Icon(
                                Icons.Filled.Send,
                                contentDescription = "Send",
                                tint = if (messageText.isNotBlank()) 
                                    Color(0xFF00FF00) 
                                else 
                                    Color(0xFF00FF00).copy(alpha = 0.3f)
                            )
                        }
                    }
                }
            }
        }
    }
    
    // User search dialog
    if (showUserSearch) {
        AlertDialog(
            onDismissRequest = { showUserSearch = false },
            containerColor = Color(0xFF0D0D0D),
            title = { 
                Text("FIND USER", color = Color(0xFF00FF00)) 
            },
            text = {
                Column {
                    OutlinedTextField(
                        value = searchQuery,
                        onValueChange = { searchQuery = it },
                        placeholder = { Text("Search...", color = Color(0xFF00FF00).copy(alpha = 0.5f)) },
                        colors = OutlinedTextFieldDefaults.colors(
                            focusedBorderColor = Color(0xFF00FF00),
                            unfocusedBorderColor = Color(0xFF00FF00).copy(alpha = 0.3f),
                            cursorColor = Color(0xFF00FF00),
                            focusedTextColor = Color(0xFF00FF00)
                        ),
                        modifier = Modifier.fillMaxWidth()
                    )
                    Spacer(modifier = Modifier.height(8.dp))
                    LazyColumn(modifier = Modifier.height(200.dp)) {
                        items(onlineUsers.filter { 
                            it.nick.contains(searchQuery, ignoreCase = true) || 
                            it.id.contains(searchQuery, ignoreCase = true) 
                        }) { user ->
                            Row(
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .clickable {
                                        currentDM = user.id
                                        showUserSearch = false
                                        searchQuery = ""
                                    }
                                    .padding(8.dp),
                                verticalAlignment = Alignment.CenterVertically
                            ) {
                                Box(
                                    modifier = Modifier
                                        .size(8.dp)
                                        .clip(CircleShape)
                                        .background(
                                            when (user.status) {
                                                "online" -> Color.Green
                                                "away" -> Color.Yellow
                                                "busy" -> Color.Red
                                                else -> Color.Gray
                                            }
                                        )
                                )
                                Spacer(modifier = Modifier.width(8.dp))
                                Column {
                                    Text(
                                        user.nick.ifEmpty { "anon-${user.id.take(8)}" },
                                        color = Color(0xFF00FF00)
                                    )
                                    Text(
                                        user.id.take(16),
                                        color = Color(0xFF00FF00).copy(alpha = 0.5f),
                                        fontSize = 10.sp
                                    )
                                }
                            }
                        }
                    }
                }
            },
            confirmButton = {
                TextButton(onClick = { showUserSearch = false }) {
                    Text("CLOSE", color = Color(0xFF00FF00))
                }
            }
        )
    }
    
    // DM options dialog
    if (showDMOptions && selectedConversation != null) {
        AlertDialog(
            onDismissRequest = { showDMOptions = false },
            containerColor = Color(0xFF0D0D0D),
            title = { Text("OPTIONS", color = Color(0xFF00FF00)) },
            text = {
                Column {
                    val conv = conversations.find { c -> 
                        c.participants.contains(selectedConversation) 
                    }
                    
                    TextButton(
                        onClick = {
                            scope.launch {
                                client.pinConversation(selectedConversation!!, !(conv?.pinned ?: false))
                            }
                            showDMOptions = false
                        },
                        modifier = Modifier.fillMaxWidth()
                    ) {
                        Text(
                            if (conv?.pinned == true) "Unpin" else "Pin",
                            color = Color(0xFF00FF00)
                        )
                    }
                    
                    TextButton(
                        onClick = {
                            scope.launch {
                                client.muteConversation(selectedConversation!!, !(conv?.muted ?: false))
                            }
                            showDMOptions = false
                        },
                        modifier = Modifier.fillMaxWidth()
                    ) {
                        Text(
                            if (conv?.muted == true) "Unmute" else "Mute",
                            color = Color(0xFF00FF00)
                        )
                    }
                    
                    TextButton(
                        onClick = {
                            scope.launch {
                                client.archiveConversation(selectedConversation!!)
                            }
                            showDMOptions = false
                        },
                        modifier = Modifier.fillMaxWidth()
                    ) {
                        Text("Archive", color = Color(0xFF00FF00))
                    }
                    
                    TextButton(
                        onClick = {
                            scope.launch {
                                client.blockUser(selectedConversation!!)
                            }
                            showDMOptions = false
                        },
                        modifier = Modifier.fillMaxWidth()
                    ) {
                        Text("Block User", color = Color.Red)
                    }
                    
                    TextButton(
                        onClick = {
                            scope.launch {
                                client.deleteConversation(selectedConversation!!)
                            }
                            if (currentDM == selectedConversation) {
                                currentDM = null
                            }
                            showDMOptions = false
                        },
                        modifier = Modifier.fillMaxWidth()
                    ) {
                        Text("Delete", color = Color.Red)
                    }
                }
            },
            confirmButton = {
                TextButton(onClick = { showDMOptions = false }) {
                    Text("CLOSE", color = Color(0xFF00FF00))
                }
            }
        )
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
fun ChatMessageItem(
    message: ChatMessage,
    isOwnMessage: Boolean,
    onReply: () -> Unit,
    onUserClick: (String) -> Unit
) {
    val time = try {
        val inputFormat = SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss", Locale.getDefault())
        val outputFormat = SimpleDateFormat("HH:mm", Locale.getDefault())
        val date = inputFormat.parse(message.timestamp.take(19))
        if (date != null) outputFormat.format(date) else message.timestamp.takeLast(8)
    } catch (e: Exception) {
        message.timestamp.takeLast(8)
    }
    
    var showOptions by remember { mutableStateOf(false) }
    
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .combinedClickable(
                onClick = { },
                onLongClick = { showOptions = true }
            )
            .background(
                if (isOwnMessage) 
                    Color(0xFF00FF00).copy(alpha = 0.05f)
                else 
                    Color.Transparent
            )
            .padding(4.dp)
    ) {
        // Reply indicator
        if (!message.reply_to.isNullOrEmpty()) {
            Text(
                "^ Reply",
                color = Color(0xFF00FF00).copy(alpha = 0.5f),
                fontSize = 9.sp,
                modifier = Modifier.padding(start = 4.dp, bottom = 2.dp)
            )
        }
        
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween
        ) {
            Text(
                text = message.nick.ifEmpty { "anon-${message.sender_id.take(8)}" },
                fontSize = 11.sp,
                fontWeight = FontWeight.Bold,
                color = if (isOwnMessage) 
                    Color(0xFF00FFFF) 
                else 
                    Color(0xFF00FF00),
                modifier = Modifier.clickable { onUserClick(message.sender_id) }
            )
            Row(verticalAlignment = Alignment.CenterVertically) {
                if (message.edited) {
                    Text(
                        "(edited)",
                        color = Color(0xFF00FF00).copy(alpha = 0.4f),
                        fontSize = 9.sp
                    )
                    Spacer(modifier = Modifier.width(4.dp))
                }
                Text(
                    text = time,
                    fontSize = 9.sp,
                    color = Color(0xFF00FF00).copy(alpha = 0.5f)
                )
            }
        }
        
        Spacer(modifier = Modifier.height(2.dp))
        
        Text(
            text = message.content,
            fontSize = 13.sp,
            color = Color(0xFF00FF00).copy(alpha = 0.9f)
        )
        
        // Media
        message.media?.let { media ->
            Spacer(modifier = Modifier.height(4.dp))
            Text(
                "[${media.type.split("/").lastOrNull() ?: "file"}: ${media.name}]",
                color = Color(0xFF00FFFF),
                fontSize = 11.sp
            )
        }
        
        // Reactions
        message.reactions?.let { reactions ->
            if (reactions.isNotEmpty()) {
                Spacer(modifier = Modifier.height(4.dp))
                LazyRow(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                    items(reactions.entries.toList()) { (emoji, users) ->
                        Text(
                            "$emoji ${users.size}",
                            color = Color(0xFF00FF00),
                            fontSize = 11.sp,
                            modifier = Modifier
                                .background(
                                    Color(0xFF00FF00).copy(alpha = 0.1f),
                                    RoundedCornerShape(4.dp)
                                )
                                .padding(horizontal = 4.dp, vertical = 2.dp)
                        )
                    }
                }
            }
        }
    }
    
    // Options dropdown
    DropdownMenu(
        expanded = showOptions,
        onDismissRequest = { showOptions = false },
        containerColor = Color(0xFF0D0D0D)
    ) {
        DropdownMenuItem(
            text = { Text("Reply", color = Color(0xFF00FF00)) },
            onClick = {
                onReply()
                showOptions = false
            }
        )
        DropdownMenuItem(
            text = { Text("Copy", color = Color(0xFF00FF00)) },
            onClick = { showOptions = false }
        )
        if (!isOwnMessage) {
            DropdownMenuItem(
                text = { Text("DM User", color = Color(0xFF00FF00)) },
                onClick = {
                    onUserClick(message.sender_id)
                    showOptions = false
                }
            )
        }
    }
}
