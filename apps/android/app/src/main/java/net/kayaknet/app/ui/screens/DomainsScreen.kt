package net.kayaknet.app.ui.screens

import androidx.compose.foundation.border
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Refresh
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
    val myDomains by client.myDomains.collectAsState()
    val scope = rememberCoroutineScope()
    
    var searchQuery by remember { mutableStateOf("") }
    var showRegisterDialog by remember { mutableStateOf(false) }
    var showWhoisDialog by remember { mutableStateOf(false) }
    var selectedTab by remember { mutableStateOf(0) }
    
    val filteredDomains = domains.filter { domain ->
        searchQuery.isEmpty() || 
        domain.name.contains(searchQuery, ignoreCase = true) ||
        domain.description.contains(searchQuery, ignoreCase = true)
    }
    
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
        
        // Tabs
        TabRow(
            selectedTabIndex = selectedTab,
            containerColor = MaterialTheme.colorScheme.surface,
            contentColor = MaterialTheme.colorScheme.primary
        ) {
            Tab(
                selected = selectedTab == 0,
                onClick = { selectedTab = 0 },
                text = { Text("BROWSE") }
            )
            Tab(
                selected = selectedTab == 1,
                onClick = { selectedTab = 1 },
                text = { Text("MY DOMAINS") }
            )
            Tab(
                selected = selectedTab == 2,
                onClick = { selectedTab = 2 },
                text = { Text("WHOIS") }
            )
        }
        
        Spacer(modifier = Modifier.height(12.dp))
        
        when (selectedTab) {
            0 -> BrowseDomainsTab(
                domains = filteredDomains,
                searchQuery = searchQuery,
                onSearchChange = { searchQuery = it },
                onRegister = { showRegisterDialog = true },
                onRefresh = { client.forceRefresh() },
                isConnected = connectionState == ConnectionState.CONNECTED,
                onConnect = { client.connect() },
                currentNodeId = client.getNodeId()
            )
            1 -> MyDomainsTab(
                domains = myDomains,
                onRegister = { showRegisterDialog = true },
                isConnected = connectionState == ConnectionState.CONNECTED
            )
            2 -> WhoisTab(
                onLookup = { name ->
                    scope.launch {
                        client.resolveDomain(name)
                    }
                }
            )
        }
    }
    
    // Register Dialog
    if (showRegisterDialog) {
        RegisterDomainDialog(
            onDismiss = { showRegisterDialog = false },
            onRegister = { name, description ->
                scope.launch {
                    val result = client.registerDomain(name, description)
                    if (result.isSuccess) {
                        showRegisterDialog = false
                    }
                }
            }
        )
    }
}

@Composable
fun BrowseDomainsTab(
    domains: List<Domain>,
    searchQuery: String,
    onSearchChange: (String) -> Unit,
    onRegister: () -> Unit,
    onRefresh: () -> Unit,
    isConnected: Boolean,
    onConnect: () -> Unit,
    currentNodeId: String
) {
    Column {
        // Search and Actions
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            OutlinedTextField(
                value = searchQuery,
                onValueChange = onSearchChange,
                modifier = Modifier.weight(1f),
                placeholder = { Text("Search domains...") },
                leadingIcon = { Icon(Icons.Filled.Search, contentDescription = null) },
                singleLine = true,
                colors = OutlinedTextFieldDefaults.colors(
                    focusedBorderColor = MaterialTheme.colorScheme.primary,
                    unfocusedBorderColor = MaterialTheme.colorScheme.primary.copy(alpha = 0.3f)
                )
            )
            
            IconButton(onClick = onRefresh) {
                Icon(Icons.Filled.Refresh, contentDescription = "Refresh", tint = MaterialTheme.colorScheme.primary)
            }
            
            IconButton(onClick = onRegister, enabled = isConnected) {
                Icon(Icons.Filled.Add, contentDescription = "Register", tint = MaterialTheme.colorScheme.primary)
            }
        }
        
        Spacer(modifier = Modifier.height(12.dp))
        
        if (!isConnected) {
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .border(1.dp, MaterialTheme.colorScheme.primary.copy(alpha = 0.3f)),
                contentAlignment = Alignment.Center
            ) {
                Column(horizontalAlignment = Alignment.CenterHorizontally) {
                    Text("// Connect to browse domains", color = MaterialTheme.colorScheme.onSurfaceVariant)
                    Spacer(modifier = Modifier.height(8.dp))
                    Button(onClick = onConnect) { Text("CONNECT") }
                }
            }
        } else if (domains.isEmpty()) {
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .border(1.dp, MaterialTheme.colorScheme.primary.copy(alpha = 0.3f)),
                contentAlignment = Alignment.Center
            ) {
                Column(horizontalAlignment = Alignment.CenterHorizontally) {
                    Text("// No domains found", color = MaterialTheme.colorScheme.onSurfaceVariant)
                    Spacer(modifier = Modifier.height(8.dp))
                    TextButton(onClick = onRegister) { Text("REGISTER FIRST DOMAIN") }
                }
            }
        } else {
            LazyColumn(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                items(domains) { domain ->
                    DomainCard(domain = domain, isOwned = domain.owner == currentNodeId)
                }
            }
        }
    }
}

@Composable
fun MyDomainsTab(
    domains: List<Domain>,
    onRegister: () -> Unit,
    isConnected: Boolean
) {
    if (domains.isEmpty()) {
        Box(
            modifier = Modifier
                .fillMaxSize()
                .border(1.dp, MaterialTheme.colorScheme.primary.copy(alpha = 0.3f)),
            contentAlignment = Alignment.Center
        ) {
            Column(horizontalAlignment = Alignment.CenterHorizontally) {
                Text("// You don't own any domains", color = MaterialTheme.colorScheme.onSurfaceVariant)
                Spacer(modifier = Modifier.height(8.dp))
                Button(onClick = onRegister, enabled = isConnected) { Text("REGISTER DOMAIN") }
            }
        }
    } else {
        LazyColumn(verticalArrangement = Arrangement.spacedBy(8.dp)) {
            items(domains) { domain ->
                DomainCard(domain = domain, isOwned = true)
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun WhoisTab(onLookup: (String) -> Unit) {
    var domainName by remember { mutableStateOf("") }
    var lookupResult by remember { mutableStateOf<Domain?>(null) }
    var isLooking by remember { mutableStateOf(false) }
    val client = KayakNetApp.instance.client
    val scope = rememberCoroutineScope()
    
    Column(
        modifier = Modifier.fillMaxSize(),
        verticalArrangement = Arrangement.spacedBy(16.dp)
    ) {
        OutlinedTextField(
            value = domainName,
            onValueChange = { domainName = it.lowercase().filter { c -> c.isLetterOrDigit() || c == '-' } },
            modifier = Modifier.fillMaxWidth(),
            placeholder = { Text("Enter domain name...") },
            trailingIcon = { Text(".kyk", color = MaterialTheme.colorScheme.primary) },
            singleLine = true
        )
        
        Button(
            onClick = {
                if (domainName.isNotBlank()) {
                    isLooking = true
                    scope.launch {
                        lookupResult = client.resolveDomain(domainName)
                        isLooking = false
                    }
                }
            },
            modifier = Modifier.fillMaxWidth(),
            enabled = domainName.isNotBlank() && !isLooking
        ) {
            if (isLooking) {
                CircularProgressIndicator(modifier = Modifier.size(16.dp), strokeWidth = 2.dp)
            } else {
                Text("LOOKUP")
            }
        }
        
        if (lookupResult != null) {
            DomainCard(domain = lookupResult!!, isOwned = lookupResult!!.owner == client.getNodeId())
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun RegisterDomainDialog(
    onDismiss: () -> Unit,
    onRegister: (String, String) -> Unit
) {
    var name by remember { mutableStateOf("") }
    var description by remember { mutableStateOf("") }
    var isRegistering by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    
    AlertDialog(
        onDismissRequest = { if (!isRegistering) onDismiss() },
        title = { Text("> REGISTER DOMAIN") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it.lowercase().filter { c -> c.isLetterOrDigit() || c == '-' } },
                    label = { Text("Domain Name") },
                    placeholder = { Text("mysite") },
                    singleLine = true,
                    enabled = !isRegistering,
                    trailingIcon = { Text(".kyk", color = MaterialTheme.colorScheme.primary) },
                    modifier = Modifier.fillMaxWidth()
                )
                
                OutlinedTextField(
                    value = description,
                    onValueChange = { description = it },
                    label = { Text("Description (optional)") },
                    enabled = !isRegistering,
                    maxLines = 2,
                    modifier = Modifier.fillMaxWidth()
                )
                
                if (error != null) {
                    Text(text = error!!, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
                }
            }
        },
        confirmButton = {
            Button(
                onClick = {
                    if (name.length >= 3) {
                        isRegistering = true
                        error = null
                        onRegister(name, description)
                    } else {
                        error = "Name must be at least 3 characters"
                    }
                },
                enabled = name.isNotBlank() && !isRegistering
            ) {
                if (isRegistering) {
                    CircularProgressIndicator(modifier = Modifier.size(16.dp), strokeWidth = 2.dp)
                } else {
                    Text("REGISTER")
                }
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss, enabled = !isRegistering) { Text("CANCEL") }
        }
    )
}

@Composable
fun DomainCard(domain: Domain, isOwned: Boolean) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .border(
                1.dp, 
                if (isOwned) MaterialTheme.colorScheme.tertiary.copy(alpha = 0.5f)
                else MaterialTheme.colorScheme.primary.copy(alpha = 0.3f)
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
                color = if (isOwned) MaterialTheme.colorScheme.tertiary else MaterialTheme.colorScheme.primary
            )
            
            if (isOwned) {
                Text(text = "[OWNED]", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.tertiary)
            }
        }
        
        if (domain.description.isNotBlank()) {
            Spacer(modifier = Modifier.height(4.dp))
            Text(text = domain.description, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
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
                    text = "EXP: ${domain.expiresAt.take(10)}",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
        }
    }
}
