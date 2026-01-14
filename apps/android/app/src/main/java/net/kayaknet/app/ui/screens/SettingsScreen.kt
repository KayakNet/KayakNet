package net.kayaknet.app.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.launch
import net.kayaknet.app.KayakNetApp
import net.kayaknet.app.network.ConnectionState

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen() {
    val client = KayakNetApp.instance.client
    val connectionState by client.connectionState.collectAsState()
    val networkStats by client.networkStats.collectAsState()
    val blockedUsers by client.blockedUsers.collectAsState()
    val scope = rememberCoroutineScope()
    
    var nick by remember { mutableStateOf(client.getLocalNick()) }
    var bio by remember { mutableStateOf(client.getProfileBio()) }
    var status by remember { mutableStateOf(client.getProfileStatus()) }
    var bootstrapHost by remember { mutableStateOf(client.bootstrapHost) }
    var autoConnect by remember { mutableStateOf(client.isAutoConnectEnabled()) }
    
    var showEditProfile by remember { mutableStateOf(false) }
    var showBootstrapEdit by remember { mutableStateOf(false) }
    var showBlockedUsers by remember { mutableStateOf(false) }
    var showAbout by remember { mutableStateOf(false) }
    
    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(Color(0xFF0A0A0A))
    ) {
        // Header
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .background(Color(0xFF0D0D0D))
                .padding(16.dp)
        ) {
            Text(
                "SETTINGS",
                color = Color(0xFF00FF00),
                fontSize = 20.sp,
                fontWeight = FontWeight.Bold
            )
            
            Spacer(modifier = Modifier.height(16.dp))
            
            // Connection Status Card
            Card(
                colors = CardDefaults.cardColors(
                    containerColor = Color(0xFF151515)
                ),
                modifier = Modifier.fillMaxWidth()
            ) {
                Column(modifier = Modifier.padding(16.dp)) {
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.SpaceBetween,
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Box(
                                modifier = Modifier
                                    .size(10.dp)
                                    .clip(CircleShape)
                                    .background(
                                        when (connectionState) {
                                            ConnectionState.CONNECTED -> Color.Green
                                            ConnectionState.CONNECTING -> Color.Yellow
                                            ConnectionState.ERROR -> Color.Red
                                            ConnectionState.DISCONNECTED -> Color.Gray
                                        }
                                    )
                            )
                            Spacer(modifier = Modifier.width(8.dp))
                            Text(
                                when (connectionState) {
                                    ConnectionState.CONNECTED -> "CONNECTED"
                                    ConnectionState.CONNECTING -> "CONNECTING..."
                                    ConnectionState.ERROR -> "ERROR"
                                    ConnectionState.DISCONNECTED -> "DISCONNECTED"
                                },
                                color = Color(0xFF00FF00),
                                fontSize = 12.sp
                            )
                        }
                        
                        when (connectionState) {
                            ConnectionState.CONNECTED -> {
                                TextButton(onClick = { client.disconnect() }) {
                                    Text("DISCONNECT", color = Color.Red, fontSize = 12.sp)
                                }
                            }
                            ConnectionState.DISCONNECTED, ConnectionState.ERROR -> {
                                TextButton(onClick = { client.connect() }) {
                                    Text("CONNECT", color = Color(0xFF00FF00), fontSize = 12.sp)
                                }
                            }
                            else -> {}
                        }
                    }
                    
                    if (connectionState == ConnectionState.CONNECTED && networkStats != null) {
                        Spacer(modifier = Modifier.height(12.dp))
                        Row(
                            modifier = Modifier.fillMaxWidth(),
                            horizontalArrangement = Arrangement.SpaceEvenly
                        ) {
                            StatItem("Peers", networkStats!!.peers.toString())
                            StatItem("Listings", networkStats!!.listings.toString())
                            StatItem("Domains", networkStats!!.domains.toString())
                        }
                    }
                }
            }
        }
        
        LazyColumn(
            modifier = Modifier
                .fillMaxSize()
                .padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp)
        ) {
            // Profile Section
            item {
                Text(
                    "PROFILE",
                    color = Color(0xFF00FF00).copy(alpha = 0.5f),
                    fontSize = 12.sp,
                    modifier = Modifier.padding(vertical = 8.dp)
                )
            }
            
            item {
                SettingsItem(
                    icon = Icons.Filled.Person,
                    title = "Edit Profile",
                    subtitle = nick,
                    onClick = { showEditProfile = true }
                )
            }
            
            item {
                SettingsItem(
                    icon = Icons.Filled.Star,
                    title = "Node ID",
                    subtitle = client.getNodeId().take(32) + "...",
                    onClick = { }
                )
            }
            
            // Network Section
            item {
                Text(
                    "NETWORK",
                    color = Color(0xFF00FF00).copy(alpha = 0.5f),
                    fontSize = 12.sp,
                    modifier = Modifier.padding(vertical = 8.dp)
                )
            }
            
            item {
                SettingsItem(
                    icon = Icons.Filled.Settings,
                    title = "Bootstrap Server",
                    subtitle = bootstrapHost,
                    onClick = { showBootstrapEdit = true }
                )
            }
            
            item {
                SettingsToggle(
                    icon = Icons.Filled.PlayArrow,
                    title = "Auto-Connect",
                    subtitle = "Connect automatically on startup",
                    checked = autoConnect,
                    onCheckedChange = {
                        autoConnect = it
                        client.setAutoConnect(it)
                    }
                )
            }
            
            // Privacy Section
            item {
                Text(
                    "PRIVACY",
                    color = Color(0xFF00FF00).copy(alpha = 0.5f),
                    fontSize = 12.sp,
                    modifier = Modifier.padding(vertical = 8.dp)
                )
            }
            
            item {
                SettingsItem(
                    icon = Icons.Filled.Lock,
                    title = "Blocked Users",
                    subtitle = "${blockedUsers.size} blocked",
                    onClick = { showBlockedUsers = true }
                )
            }
            
            // About Section
            item {
                Text(
                    "ABOUT",
                    color = Color(0xFF00FF00).copy(alpha = 0.5f),
                    fontSize = 12.sp,
                    modifier = Modifier.padding(vertical = 8.dp)
                )
            }
            
            item {
                SettingsItem(
                    icon = Icons.Filled.Info,
                    title = "About KayakNet",
                    subtitle = "Version ${networkStats?.version ?: "1.0.0"}",
                    onClick = { showAbout = true }
                )
            }
        }
    }
    
    // Edit Profile Dialog
    if (showEditProfile) {
        var editNick by remember { mutableStateOf(nick) }
        var editBio by remember { mutableStateOf(bio) }
        var editStatus by remember { mutableStateOf(status) }
        
        AlertDialog(
            onDismissRequest = { showEditProfile = false },
            containerColor = Color(0xFF0D0D0D),
            title = { Text("EDIT PROFILE", color = Color(0xFF00FF00)) },
            text = {
                Column {
                    OutlinedTextField(
                        value = editNick,
                        onValueChange = { editNick = it },
                        label = { Text("Nickname", color = Color(0xFF00FF00).copy(alpha = 0.7f)) },
                        modifier = Modifier.fillMaxWidth(),
                        colors = OutlinedTextFieldDefaults.colors(
                            focusedBorderColor = Color(0xFF00FF00),
                            unfocusedBorderColor = Color(0xFF00FF00).copy(alpha = 0.3f),
                            cursorColor = Color(0xFF00FF00),
                            focusedTextColor = Color(0xFF00FF00),
                            unfocusedTextColor = Color(0xFF00FF00)
                        )
                    )
                    
                    Spacer(modifier = Modifier.height(8.dp))
                    
                    OutlinedTextField(
                        value = editBio,
                        onValueChange = { editBio = it },
                        label = { Text("Bio", color = Color(0xFF00FF00).copy(alpha = 0.7f)) },
                        modifier = Modifier.fillMaxWidth(),
                        minLines = 2,
                        colors = OutlinedTextFieldDefaults.colors(
                            focusedBorderColor = Color(0xFF00FF00),
                            unfocusedBorderColor = Color(0xFF00FF00).copy(alpha = 0.3f),
                            cursorColor = Color(0xFF00FF00),
                            focusedTextColor = Color(0xFF00FF00),
                            unfocusedTextColor = Color(0xFF00FF00)
                        )
                    )
                    
                    Spacer(modifier = Modifier.height(8.dp))
                    
                    Text("Status", color = Color(0xFF00FF00).copy(alpha = 0.7f), fontSize = 12.sp)
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.spacedBy(8.dp)
                    ) {
                        listOf("online", "away", "busy", "invisible").forEach { s ->
                            FilterChip(
                                selected = editStatus == s,
                                onClick = { editStatus = s },
                                label = { Text(s.uppercase(), fontSize = 10.sp) },
                                colors = FilterChipDefaults.filterChipColors(
                                    selectedContainerColor = when (s) {
                                        "online" -> Color.Green
                                        "away" -> Color.Yellow
                                        "busy" -> Color.Red
                                        else -> Color.Gray
                                    },
                                    selectedLabelColor = Color.Black,
                                    containerColor = Color.Transparent,
                                    labelColor = Color(0xFF00FF00)
                                )
                            )
                        }
                    }
                }
            },
            confirmButton = {
                Button(
                    onClick = {
                        scope.launch {
                            client.updateProfile(editNick, editBio, editStatus)
                            nick = editNick
                            bio = editBio
                            status = editStatus
                            showEditProfile = false
                        }
                    },
                    colors = ButtonDefaults.buttonColors(
                        containerColor = Color(0xFF00FF00),
                        contentColor = Color.Black
                    )
                ) {
                    Text("SAVE")
                }
            },
            dismissButton = {
                TextButton(onClick = { showEditProfile = false }) {
                    Text("CANCEL", color = Color(0xFF00FF00))
                }
            }
        )
    }
    
    // Bootstrap Edit Dialog
    if (showBootstrapEdit) {
        var editHost by remember { mutableStateOf(bootstrapHost) }
        
        AlertDialog(
            onDismissRequest = { showBootstrapEdit = false },
            containerColor = Color(0xFF0D0D0D),
            title = { Text("BOOTSTRAP SERVER", color = Color(0xFF00FF00)) },
            text = {
                Column {
                    OutlinedTextField(
                        value = editHost,
                        onValueChange = { editHost = it },
                        label = { Text("Server Address", color = Color(0xFF00FF00).copy(alpha = 0.7f)) },
                        modifier = Modifier.fillMaxWidth(),
                        colors = OutlinedTextFieldDefaults.colors(
                            focusedBorderColor = Color(0xFF00FF00),
                            unfocusedBorderColor = Color(0xFF00FF00).copy(alpha = 0.3f),
                            cursorColor = Color(0xFF00FF00),
                            focusedTextColor = Color(0xFF00FF00),
                            unfocusedTextColor = Color(0xFF00FF00)
                        )
                    )
                    
                    Spacer(modifier = Modifier.height(8.dp))
                    
                    Text(
                        "Warning: Changing this will require reconnecting",
                        color = Color.Yellow,
                        fontSize = 11.sp
                    )
                }
            },
            confirmButton = {
                Button(
                    onClick = {
                        client.setBootstrapHost(editHost)
                        bootstrapHost = editHost
                        client.disconnect()
                        client.connect()
                        showBootstrapEdit = false
                    },
                    colors = ButtonDefaults.buttonColors(
                        containerColor = Color(0xFF00FF00),
                        contentColor = Color.Black
                    )
                ) {
                    Text("SAVE")
                }
            },
            dismissButton = {
                TextButton(onClick = { showBootstrapEdit = false }) {
                    Text("CANCEL", color = Color(0xFF00FF00))
                }
            }
        )
    }
    
    // Blocked Users Dialog
    if (showBlockedUsers) {
        AlertDialog(
            onDismissRequest = { showBlockedUsers = false },
            containerColor = Color(0xFF0D0D0D),
            title = { Text("BLOCKED USERS", color = Color(0xFF00FF00)) },
            text = {
                if (blockedUsers.isEmpty()) {
                    Text("No blocked users", color = Color(0xFF00FF00).copy(alpha = 0.5f))
                } else {
                    LazyColumn(modifier = Modifier.height(300.dp)) {
                        items(blockedUsers.toList()) { userId ->
                            Row(
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .padding(vertical = 4.dp),
                                horizontalArrangement = Arrangement.SpaceBetween,
                                verticalAlignment = Alignment.CenterVertically
                            ) {
                                Text(
                                    userId.take(20) + "...",
                                    color = Color(0xFF00FF00),
                                    fontSize = 12.sp
                                )
                                IconButton(
                                    onClick = { client.unblockUser(userId) }
                                ) {
                                    Icon(
                                        Icons.Filled.Close,
                                        contentDescription = "Unblock",
                                        tint = Color.Red
                                    )
                                }
                            }
                        }
                    }
                }
            },
            confirmButton = {
                TextButton(onClick = { showBlockedUsers = false }) {
                    Text("CLOSE", color = Color(0xFF00FF00))
                }
            }
        )
    }
    
    // About Dialog
    if (showAbout) {
        AlertDialog(
            onDismissRequest = { showAbout = false },
            containerColor = Color(0xFF0D0D0D),
            title = { Text("KAYAKNET", color = Color(0xFF00FF00)) },
            text = {
                Column {
                    Text(
                        "Privacy-First P2P Network",
                        color = Color(0xFF00FF00),
                        fontWeight = FontWeight.Bold
                    )
                    
                    Spacer(modifier = Modifier.height(16.dp))
                    
                    Text(
                        "KayakNet is a decentralized peer-to-peer network with:\n\n" +
                        "- End-to-end encrypted messaging\n" +
                        "- Anonymous marketplace with crypto escrow\n" +
                        "- .kyk domain name system\n" +
                        "- Onion routing for privacy\n" +
                        "- No central servers\n",
                        color = Color(0xFF00FF00).copy(alpha = 0.8f),
                        fontSize = 12.sp
                    )
                    
                    Spacer(modifier = Modifier.height(8.dp))
                    
                    Text(
                        "Version: ${networkStats?.version ?: "1.0.0"}",
                        color = Color(0xFF00FF00).copy(alpha = 0.5f),
                        fontSize = 11.sp
                    )
                }
            },
            confirmButton = {
                TextButton(onClick = { showAbout = false }) {
                    Text("CLOSE", color = Color(0xFF00FF00))
                }
            }
        )
    }
}

@Composable
private fun StatItem(label: String, value: String) {
    Column(horizontalAlignment = Alignment.CenterHorizontally) {
        Text(
            value,
            color = Color(0xFF00FF00),
            fontWeight = FontWeight.Bold,
            fontSize = 16.sp
        )
        Text(
            label,
            color = Color(0xFF00FF00).copy(alpha = 0.5f),
            fontSize = 10.sp
        )
    }
}

@Composable
private fun SettingsItem(
    icon: ImageVector,
    title: String,
    subtitle: String,
    onClick: () -> Unit
) {
    Card(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick),
        colors = CardDefaults.cardColors(containerColor = Color(0xFF0D0D0D))
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(16.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            Icon(
                icon,
                contentDescription = null,
                tint = Color(0xFF00FF00),
                modifier = Modifier.size(24.dp)
            )
            Spacer(modifier = Modifier.width(16.dp))
            Column(modifier = Modifier.weight(1f)) {
                Text(title, color = Color(0xFF00FF00))
                Text(
                    subtitle,
                    color = Color(0xFF00FF00).copy(alpha = 0.5f),
                    fontSize = 12.sp
                )
            }
            Icon(
                Icons.Filled.KeyboardArrowRight,
                contentDescription = null,
                tint = Color(0xFF00FF00).copy(alpha = 0.5f)
            )
        }
    }
}

@Composable
private fun SettingsToggle(
    icon: ImageVector,
    title: String,
    subtitle: String,
    checked: Boolean,
    onCheckedChange: (Boolean) -> Unit
) {
    Card(
        colors = CardDefaults.cardColors(containerColor = Color(0xFF0D0D0D)),
        modifier = Modifier.fillMaxWidth()
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(16.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            Icon(
                icon,
                contentDescription = null,
                tint = Color(0xFF00FF00),
                modifier = Modifier.size(24.dp)
            )
            Spacer(modifier = Modifier.width(16.dp))
            Column(modifier = Modifier.weight(1f)) {
                Text(title, color = Color(0xFF00FF00))
                Text(
                    subtitle,
                    color = Color(0xFF00FF00).copy(alpha = 0.5f),
                    fontSize = 12.sp
                )
            }
            Switch(
                checked = checked,
                onCheckedChange = onCheckedChange,
                colors = SwitchDefaults.colors(
                    checkedThumbColor = Color(0xFF00FF00),
                    checkedTrackColor = Color(0xFF00FF00).copy(alpha = 0.3f),
                    uncheckedThumbColor = Color.Gray,
                    uncheckedTrackColor = Color.Gray.copy(alpha = 0.3f)
                )
            )
        }
    }
}
