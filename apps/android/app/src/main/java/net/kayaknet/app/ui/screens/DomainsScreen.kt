package net.kayaknet.app.ui.screens

import androidx.compose.foundation.border
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.launch
import net.kayaknet.app.KayakNetApp
import net.kayaknet.app.network.ConnectionState
import net.kayaknet.app.network.Domain

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DomainsScreen() {
    val client = KayakNetApp.instance.client
    val connectionState by client.connectionState.collectAsState()
    val domains by client.domains.collectAsState()
    val scope = rememberCoroutineScope()
    
    var searchQuery by remember { mutableStateOf("") }
    var showRegisterDialog by remember { mutableStateOf(false) }
    var registerName by remember { mutableStateOf("") }
    var registerDesc by remember { mutableStateOf("") }
    var isRegistering by remember { mutableStateOf(false) }
    var registerError by remember { mutableStateOf<String?>(null) }
    
    val filteredDomains = domains.filter { domain ->
        searchQuery.isEmpty() || 
        domain.name.contains(searchQuery, ignoreCase = true) ||
        domain.description.contains(searchQuery, ignoreCase = true)
    }
    
    val myDomains = domains.filter { it.owner == client.getNodeId() }
    
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(16.dp)
    ) {
        // Stats
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(12.dp)
        ) {
            StatCard(
                title = "TOTAL",
                value = domains.size.toString(),
                modifier = Modifier.weight(1f)
            )
            StatCard(
                title = "MINE",
                value = myDomains.size.toString(),
                modifier = Modifier.weight(1f)
            )
        }
        
        Spacer(modifier = Modifier.height(16.dp))
        
        // Search and Register
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            OutlinedTextField(
                value = searchQuery,
                onValueChange = { searchQuery = it },
                modifier = Modifier.weight(1f),
                placeholder = { Text("Search domains...") },
                leadingIcon = { Icon(Icons.Filled.Search, contentDescription = null) },
                singleLine = true,
                colors = OutlinedTextFieldDefaults.colors(
                    focusedBorderColor = MaterialTheme.colorScheme.primary,
                    unfocusedBorderColor = MaterialTheme.colorScheme.primary.copy(alpha = 0.3f)
                )
            )
            
            IconButton(
                onClick = { showRegisterDialog = true },
                enabled = connectionState == ConnectionState.CONNECTED
            ) {
                Icon(
                    Icons.Filled.Add,
                    contentDescription = "Register",
                    tint = MaterialTheme.colorScheme.primary
                )
            }
        }
        
        Spacer(modifier = Modifier.height(16.dp))
        
        // Domains List
        if (connectionState != ConnectionState.CONNECTED) {
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .border(1.dp, MaterialTheme.colorScheme.primary.copy(alpha = 0.3f)),
                contentAlignment = Alignment.Center
            ) {
                Text(
                    text = "// Connect to browse domains",
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
        } else if (filteredDomains.isEmpty()) {
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .border(1.dp, MaterialTheme.colorScheme.primary.copy(alpha = 0.3f)),
                contentAlignment = Alignment.Center
            ) {
                Column(horizontalAlignment = Alignment.CenterHorizontally) {
                    Text(
                        text = "// No domains found",
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                    Spacer(modifier = Modifier.height(8.dp))
                    TextButton(onClick = { showRegisterDialog = true }) {
                        Text("REGISTER FIRST DOMAIN")
                    }
                }
            }
        } else {
            LazyColumn(
                verticalArrangement = Arrangement.spacedBy(8.dp)
            ) {
                items(filteredDomains) { domain ->
                    DomainCard(
                        domain = domain,
                        isOwned = domain.owner == client.getNodeId()
                    )
                }
            }
        }
    }
    
    // Register Dialog
    if (showRegisterDialog) {
        AlertDialog(
            onDismissRequest = { 
                if (!isRegistering) {
                    showRegisterDialog = false
                    registerError = null
                }
            },
            title = { Text("> REGISTER DOMAIN") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    OutlinedTextField(
                        value = registerName,
                        onValueChange = { 
                            registerName = it.lowercase().filter { c -> c.isLetterOrDigit() || c == '-' }
                        },
                        label = { Text("Domain Name") },
                        placeholder = { Text("mysite") },
                        singleLine = true,
                        enabled = !isRegistering,
                        trailingIcon = { Text(".kyk", color = MaterialTheme.colorScheme.primary) },
                        colors = OutlinedTextFieldDefaults.colors(
                            focusedBorderColor = MaterialTheme.colorScheme.primary,
                            unfocusedBorderColor = MaterialTheme.colorScheme.primary.copy(alpha = 0.3f)
                        )
                    )
                    
                    OutlinedTextField(
                        value = registerDesc,
                        onValueChange = { registerDesc = it },
                        label = { Text("Description (optional)") },
                        enabled = !isRegistering,
                        maxLines = 2,
                        colors = OutlinedTextFieldDefaults.colors(
                            focusedBorderColor = MaterialTheme.colorScheme.primary,
                            unfocusedBorderColor = MaterialTheme.colorScheme.primary.copy(alpha = 0.3f)
                        )
                    )
                    
                    if (registerError != null) {
                        Text(
                            text = registerError!!,
                            color = MaterialTheme.colorScheme.error,
                            style = MaterialTheme.typography.bodySmall
                        )
                    }
                }
            },
            confirmButton = {
                Button(
                    onClick = {
                        if (registerName.length >= 3) {
                            isRegistering = true
                            registerError = null
                            scope.launch {
                                val result = client.registerDomain(registerName, registerDesc)
                                result.fold(
                                    onSuccess = {
                                        showRegisterDialog = false
                                        registerName = ""
                                        registerDesc = ""
                                    },
                                    onFailure = {
                                        registerError = it.message
                                    }
                                )
                                isRegistering = false
                            }
                        } else {
                            registerError = "Name must be at least 3 characters"
                        }
                    },
                    enabled = registerName.isNotBlank() && !isRegistering
                ) {
                    if (isRegistering) {
                        CircularProgressIndicator(
                            modifier = Modifier.size(16.dp),
                            color = MaterialTheme.colorScheme.onPrimary
                        )
                    } else {
                        Text("REGISTER")
                    }
                }
            },
            dismissButton = {
                TextButton(
                    onClick = { showRegisterDialog = false },
                    enabled = !isRegistering
                ) {
                    Text("CANCEL")
                }
            },
            containerColor = MaterialTheme.colorScheme.surface,
            titleContentColor = MaterialTheme.colorScheme.primary
        )
    }
}

@Composable
fun DomainCard(
    domain: Domain,
    isOwned: Boolean
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .border(
                1.dp, 
                if (isOwned) 
                    MaterialTheme.colorScheme.tertiary.copy(alpha = 0.5f)
                else 
                    MaterialTheme.colorScheme.primary.copy(alpha = 0.3f)
            )
            .padding(12.dp)
    ) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically
        ) {
            Text(
                text = domain.fullName.uppercase(),
                style = MaterialTheme.typography.titleMedium,
                color = if (isOwned) 
                    MaterialTheme.colorScheme.tertiary 
                else 
                    MaterialTheme.colorScheme.primary
            )
            
            if (isOwned) {
                Text(
                    text = "[OWNED]",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.tertiary
                )
            }
        }
        
        if (domain.description.isNotBlank()) {
            Spacer(modifier = Modifier.height(4.dp))
            Text(
                text = domain.description,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant
            )
        }
        
        Spacer(modifier = Modifier.height(8.dp))
        
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween
        ) {
            Text(
                text = "OWNER: ${domain.owner.take(12)}...",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant
            )
            
            if (domain.expiresAt != null) {
                Text(
                    text = "EXP: ${domain.expiresAt}",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
        }
    }
}

