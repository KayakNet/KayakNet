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
    val clipboardManager = LocalClipboardManager.current
    
    var nickname by remember { mutableStateOf(client.getLocalNick()) }
    var showNodeId by remember { mutableStateOf(false) }
    var autoConnect by remember { mutableStateOf(true) }
    var notifications by remember { mutableStateOf(true) }
    
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
                    }) {
                        Icon(
                            Icons.Filled.ContentCopy,
                            contentDescription = "Copy",
                            tint = MaterialTheme.colorScheme.onSurfaceVariant
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
                onCheckedChange = { autoConnect = it }
            )
            
            Divider(color = MaterialTheme.colorScheme.primary.copy(alpha = 0.2f))
            
            SettingsItem(
                title = "Bootstrap Node",
                value = "203.161.33.237:4242"
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
        }
        
        // Notifications Section
        SettingsSection(title = "NOTIFICATIONS") {
            SettingsSwitch(
                title = "Push Notifications",
                description = "Receive message notifications",
                checked = notifications,
                onCheckedChange = { notifications = it }
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
        }
        
        // About Section
        SettingsSection(title = "ABOUT") {
            SettingsItem(
                title = "Version",
                value = "v0.1.14"
            )
            
            Divider(color = MaterialTheme.colorScheme.primary.copy(alpha = 0.2f))
            
            SettingsItem(
                title = "Protocol",
                value = "KayakNet P2P"
            )
        }
        
        // Danger Zone
        Spacer(modifier = Modifier.height(16.dp))
        
        OutlinedButton(
            onClick = { /* TODO: Clear data */ },
            modifier = Modifier.fillMaxWidth(),
            colors = ButtonDefaults.outlinedButtonColors(
                contentColor = MaterialTheme.colorScheme.error
            )
        ) {
            Icon(Icons.Filled.Delete, contentDescription = null)
            Spacer(modifier = Modifier.width(8.dp))
            Text("CLEAR ALL DATA")
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
    valueColor: androidx.compose.ui.graphics.Color = MaterialTheme.colorScheme.onSurfaceVariant
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

