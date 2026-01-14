package net.kayaknet.app.ui.screens

import androidx.compose.foundation.border
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.unit.dp
import net.kayaknet.app.KayakNetApp
import net.kayaknet.app.network.ConnectionState

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen() {
    val client = KayakNetApp.instance.client
    val connectionState by client.connectionState.collectAsState()
    val networkStats by client.networkStats.collectAsState()
    val clipboardManager = LocalClipboardManager.current
    
    var nickname by remember { mutableStateOf(client.getLocalNick()) }
    var showNodeId by remember { mutableStateOf(false) }
    var autoConnect by remember { mutableStateOf(client.isAutoConnectEnabled()) }
    var copiedNodeId by remember { mutableStateOf(false) }
    
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(16.dp)
            .verticalScroll(rememberScrollState()),
        verticalArrangement = Arrangement.spacedBy(16.dp)
    ) {
        // Profile Section
        SettingsSection(title = "PROFILE") {
            OutlinedTextField(
                value = nickname,
                onValueChange = { 
                    nickname = it
                    client.setLocalNick(it)
                },
                label = { Text("Nickname") },
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
                colors = OutlinedTextFieldDefaults.colors(
                    focusedBorderColor = MaterialTheme.colorScheme.primary,
                    unfocusedBorderColor = MaterialTheme.colorScheme.primary.copy(alpha = 0.3f)
                )
            )
            
            Spacer(modifier = Modifier.height(12.dp))
            
            // Node ID
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically
            ) {
                Column(modifier = Modifier.weight(1f)) {
                    Text(
                        text = "NODE ID",
                        style = MaterialTheme.typography.labelMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                    Text(
                        text = if (showNodeId) client.getNodeId() else "${client.getNodeId().take(16)}...",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.primary
                    )
                }
                
                Row {
                    IconButton(onClick = { showNodeId = !showNodeId }) {
                        Icon(
                            if (showNodeId) Icons.Filled.VisibilityOff else Icons.Filled.Visibility,
                            contentDescription = "Toggle visibility",
                            tint = MaterialTheme.colorScheme.onSurfaceVariant
                        )
                    }
                    
                    IconButton(onClick = { 
                        clipboardManager.setText(AnnotatedString(client.getNodeId()))
                        copiedNodeId = true
                    }) {
                        Icon(
                            if (copiedNodeId) Icons.Filled.Check else Icons.Filled.ContentCopy,
                            contentDescription = "Copy",
                            tint = if (copiedNodeId) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant
                        )
                    }
                }
            }
        }
        
        // Network Section
        SettingsSection(title = "NETWORK") {
            SettingsSwitch(
                title = "Auto-connect",
                description = "Connect to network on app start",
                checked = autoConnect,
                onCheckedChange = { 
                    autoConnect = it
                    client.setAutoConnect(it)
                }
            )
            
            Divider(color = MaterialTheme.colorScheme.primary.copy(alpha = 0.2f))
            
            SettingsItem(
                title = "Bootstrap Node",
                value = "${client.bootstrapHost}:${client.bootstrapPort}"
            )
            
            Divider(color = MaterialTheme.colorScheme.primary.copy(alpha = 0.2f))
            
            SettingsItem(
                title = "Connection Status",
                value = when (connectionState) {
                    ConnectionState.CONNECTED -> "CONNECTED"
                    ConnectionState.CONNECTING -> "CONNECTING..."
                    ConnectionState.DISCONNECTED -> "DISCONNECTED"
                    ConnectionState.ERROR -> "ERROR"
                },
                valueColor = when (connectionState) {
                    ConnectionState.CONNECTED -> MaterialTheme.colorScheme.primary
                    ConnectionState.ERROR -> MaterialTheme.colorScheme.error
                    else -> MaterialTheme.colorScheme.onSurfaceVariant
                }
            )
            
            Divider(color = MaterialTheme.colorScheme.primary.copy(alpha = 0.2f))
            
            SettingsItem(
                title = "Network Version",
                value = networkStats?.version ?: "Unknown"
            )
        }
        
        // Security Section
        SettingsSection(title = "SECURITY") {
            SettingsItem(
                title = "Encryption",
                value = "E2E + Onion (3-hop)"
            )
            
            Divider(color = MaterialTheme.colorScheme.primary.copy(alpha = 0.2f))
            
            SettingsItem(
                title = "Traffic Protection",
                value = "Padding + Mixing"
            )
            
            Divider(color = MaterialTheme.colorScheme.primary.copy(alpha = 0.2f))
            
            SettingsItem(
                title = "Identity",
                value = "Ed25519 Keypair"
            )
        }
        
        // About Section
        SettingsSection(title = "ABOUT") {
            SettingsItem(
                title = "App Version",
                value = "v0.1.17"
            )
            
            Divider(color = MaterialTheme.colorScheme.primary.copy(alpha = 0.2f))
            
            SettingsItem(
                title = "Protocol",
                value = "KayakNet P2P"
            )
            
            Divider(color = MaterialTheme.colorScheme.primary.copy(alpha = 0.2f))
            
            SettingsItem(
                title = "Network Peers",
                value = (networkStats?.peers ?: 0).toString()
            )
        }
        
        // Actions
        Spacer(modifier = Modifier.height(8.dp))
        
        if (connectionState == ConnectionState.CONNECTED) {
            Button(
                onClick = { client.forceRefresh() },
                modifier = Modifier.fillMaxWidth(),
                colors = ButtonDefaults.buttonColors(
                    containerColor = MaterialTheme.colorScheme.primary
                )
            ) {
                Icon(Icons.Filled.Refresh, contentDescription = null)
                Spacer(modifier = Modifier.width(8.dp))
                Text("FORCE SYNC")
            }
        }
        
        OutlinedButton(
            onClick = { 
                // Clear preferences (except node ID)
                val nodeId = client.getNodeId()
                // In a real app, you'd clear other data here
            },
            modifier = Modifier.fillMaxWidth(),
            colors = ButtonDefaults.outlinedButtonColors(
                contentColor = MaterialTheme.colorScheme.error
            )
        ) {
            Icon(Icons.Filled.Delete, contentDescription = null)
            Spacer(modifier = Modifier.width(8.dp))
            Text("CLEAR CACHE")
        }
    }
}

@Composable
fun SettingsSection(
    title: String,
    content: @Composable ColumnScope.() -> Unit
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .border(1.dp, MaterialTheme.colorScheme.primary.copy(alpha = 0.3f))
    ) {
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 12.dp, vertical = 8.dp)
        ) {
            Text(
                text = "> $title",
                style = MaterialTheme.typography.titleSmall,
                color = MaterialTheme.colorScheme.primary
            )
        }
        
        Divider(color = MaterialTheme.colorScheme.primary.copy(alpha = 0.3f))
        
        Column(
            modifier = Modifier.padding(12.dp),
            content = content
        )
    }
}

@Composable
fun SettingsItem(
    title: String,
    value: String,
    valueColor: Color = MaterialTheme.colorScheme.onSurfaceVariant
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 8.dp),
        horizontalArrangement = Arrangement.SpaceBetween
    ) {
        Text(
            text = title,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurface
        )
        Text(
            text = value,
            style = MaterialTheme.typography.bodyMedium,
            color = valueColor
        )
    }
}

@Composable
fun SettingsSwitch(
    title: String,
    description: String,
    checked: Boolean,
    onCheckedChange: (Boolean) -> Unit
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 8.dp),
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically
    ) {
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = title,
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurface
            )
            Text(
                text = description,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant
            )
        }
        
        Switch(
            checked = checked,
            onCheckedChange = onCheckedChange,
            colors = SwitchDefaults.colors(
                checkedThumbColor = MaterialTheme.colorScheme.primary,
                checkedTrackColor = MaterialTheme.colorScheme.primaryContainer
            )
        )
    }
}
