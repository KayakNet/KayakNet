package net.kayaknet.app.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.launch
import net.kayaknet.app.KayakNetApp
import net.kayaknet.app.network.ConnectionState
import net.kayaknet.app.network.Domain
import java.text.SimpleDateFormat
import java.util.*

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DomainsScreen() {
    val client = remember { KayakNetApp.instance.client }
    val connectionState by client.connectionState.collectAsState()
    val domains by client.domains.collectAsState()
    val myDomains by client.myDomains.collectAsState()
    val scope = rememberCoroutineScope()
    
    var searchQuery by remember { mutableStateOf("") }
    var showRegister by remember { mutableStateOf(false) }
    var showWhois by remember { mutableStateOf(false) }
    var selectedDomain by remember { mutableStateOf<Domain?>(null) }
    var tabIndex by remember { mutableStateOf(0) } // 0 = Browse, 1 = My Domains
    
    val filteredDomains = domains.filter { domain ->
        searchQuery.isEmpty() ||
        domain.name.contains(searchQuery, ignoreCase = true) ||
        domain.description.contains(searchQuery, ignoreCase = true)
    }
    
    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(Color(0xFF0A0A0A))
            .padding(16.dp)
    ) {
        // Header
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically
        ) {
            Text(
                ".KYK DOMAINS",
                color = Color(0xFF00FF00),
                fontSize = 20.sp,
                fontWeight = FontWeight.Bold
            )
            Row {
                IconButton(onClick = { showWhois = true }) {
                    Icon(
                        Icons.Filled.Search,
                        contentDescription = "WHOIS",
                        tint = Color(0xFF00FF00)
                    )
                }
                IconButton(onClick = { showRegister = true }) {
                    Icon(
                        Icons.Filled.Add,
                        contentDescription = "Register",
                        tint = Color(0xFF00FF00)
                    )
                }
                IconButton(onClick = { client.forceRefresh() }) {
                    Icon(
                        Icons.Filled.Refresh,
                        contentDescription = "Refresh",
                        tint = Color(0xFF00FF00)
                    )
                }
            }
        }
        
        Spacer(modifier = Modifier.height(16.dp))
        
        // Tabs
        TabRow(
            selectedTabIndex = tabIndex,
            containerColor = Color(0xFF0A0A0A),
            contentColor = Color(0xFF00FF00)
        ) {
            Tab(
                selected = tabIndex == 0,
                onClick = { tabIndex = 0 },
                text = { Text("BROWSE") }
            )
            Tab(
                selected = tabIndex == 1,
                onClick = { tabIndex = 1 },
                text = { 
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text("MY DOMAINS")
                        if (myDomains.isNotEmpty()) {
                            Spacer(modifier = Modifier.width(4.dp))
                            Badge(containerColor = Color(0xFF00FF00), contentColor = Color.Black) {
                                Text(myDomains.size.toString())
                            }
                        }
                    }
                }
            )
        }
        
        Spacer(modifier = Modifier.height(16.dp))
        
        if (tabIndex == 0) {
            // Search
            OutlinedTextField(
                value = searchQuery,
                onValueChange = { searchQuery = it },
                modifier = Modifier.fillMaxWidth(),
                placeholder = { Text("Search domains...", color = Color(0xFF00FF00).copy(alpha = 0.5f)) },
                leadingIcon = {
                    Icon(Icons.Filled.Search, contentDescription = null, tint = Color(0xFF00FF00))
                },
                trailingIcon = {
                    if (searchQuery.isNotEmpty()) {
                        IconButton(onClick = { searchQuery = "" }) {
                            Icon(Icons.Filled.Close, contentDescription = "Clear", tint = Color(0xFF00FF00))
                        }
                    }
                },
                colors = OutlinedTextFieldDefaults.colors(
                    focusedBorderColor = Color(0xFF00FF00),
                    unfocusedBorderColor = Color(0xFF00FF00).copy(alpha = 0.3f),
                    cursorColor = Color(0xFF00FF00),
                    focusedTextColor = Color(0xFF00FF00),
                    unfocusedTextColor = Color(0xFF00FF00)
                ),
                singleLine = true
            )
            
            Spacer(modifier = Modifier.height(12.dp))
            
            Text(
                "${filteredDomains.size} domains registered",
                color = Color(0xFF00FF00).copy(alpha = 0.5f),
                fontSize = 12.sp
            )
            
            Spacer(modifier = Modifier.height(8.dp))
        }
        
        // Content
        if (connectionState != ConnectionState.CONNECTED) {
            Box(
                modifier = Modifier.fillMaxSize(),
                contentAlignment = Alignment.Center
            ) {
                Column(horizontalAlignment = Alignment.CenterHorizontally) {
                    Text("// NOT CONNECTED", color = Color(0xFF00FF00).copy(alpha = 0.5f))
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
        } else {
            val displayDomains = if (tabIndex == 0) filteredDomains else myDomains
            
            if (displayDomains.isEmpty()) {
                Box(
                    modifier = Modifier.fillMaxSize(),
                    contentAlignment = Alignment.Center
                ) {
                    Column(horizontalAlignment = Alignment.CenterHorizontally) {
                        Text(
                            if (tabIndex == 0) "// No domains found" else "// You don't own any domains",
                            color = Color(0xFF00FF00).copy(alpha = 0.5f)
                        )
                        if (tabIndex == 1) {
                            Spacer(modifier = Modifier.height(8.dp))
                            Button(
                                onClick = { showRegister = true },
                                colors = ButtonDefaults.buttonColors(
                                    containerColor = Color(0xFF00FF00),
                                    contentColor = Color.Black
                                )
                            ) {
                                Text("REGISTER DOMAIN")
                            }
                        }
                    }
                }
            } else {
                LazyColumn(
                    verticalArrangement = Arrangement.spacedBy(8.dp)
                ) {
                    items(displayDomains) { domain ->
                        DomainCard(
                            domain = domain,
                            isOwned = tabIndex == 1,
                            onClick = { selectedDomain = domain },
                            onRenew = if (tabIndex == 1) {
                                {
                                    scope.launch {
                                        client.renewDomain(domain.name)
                                    }
                                }
                            } else null
                        )
                    }
                }
            }
        }
    }
    
    // Register Dialog
    if (showRegister) {
        RegisterDomainDialog(
            onDismiss = { showRegister = false },
            onRegister = { name, description, serviceType ->
                scope.launch {
                    val result = client.registerDomain(name, description, serviceType)
                    if (result.isSuccess) {
                        showRegister = false
                    }
                }
            }
        )
    }
    
    // WHOIS Dialog
    if (showWhois) {
        WhoisDialog(
            onDismiss = { showWhois = false },
            onLookup = { name ->
                scope.launch {
                    val domain = client.resolveDomain(name)
                    if (domain != null) {
                        selectedDomain = domain
                        showWhois = false
                    }
                }
            }
        )
    }
    
    // Domain Detail Dialog
    selectedDomain?.let { domain ->
        DomainDetailDialog(
            domain = domain,
            onDismiss = { selectedDomain = null }
        )
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DomainCard(
    domain: Domain,
    isOwned: Boolean,
    onClick: () -> Unit,
    onRenew: (() -> Unit)? = null
) {
    val expiresAt = domain.expiresAt?.let {
        try {
            val inputFormat = SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss", Locale.getDefault())
            val date = inputFormat.parse(it.take(19))
            date
        } catch (e: Exception) { null }
    }
    
    val isExpiringSoon = expiresAt?.let {
        val daysUntilExpiry = (it.time - System.currentTimeMillis()) / (1000 * 60 * 60 * 24)
        daysUntilExpiry < 30
    } ?: false
    
    Card(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick),
        colors = CardDefaults.cardColors(
            containerColor = Color(0xFF0D0D0D)
        ),
        border = CardDefaults.outlinedCardBorder().copy(
            brush = androidx.compose.ui.graphics.SolidColor(
                if (isExpiringSoon) Color.Yellow.copy(alpha = 0.5f)
                else Color(0xFF00FF00).copy(alpha = 0.3f)
            )
        )
    ) {
        Column(
            modifier = Modifier.padding(12.dp)
        ) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically
            ) {
                Column(modifier = Modifier.weight(1f)) {
                    Text(
                        "${domain.name}.kyk",
                        color = Color(0xFF00FF00),
                        fontWeight = FontWeight.Bold,
                        fontSize = 16.sp
                    )
                    if (domain.description.isNotEmpty()) {
                        Text(
                            domain.description,
                            color = Color(0xFF00FF00).copy(alpha = 0.7f),
                            fontSize = 12.sp,
                            maxLines = 2,
                            overflow = TextOverflow.Ellipsis
                        )
                    }
                }
                
                if (domain.serviceType?.isNotEmpty() == true) {
                    Badge(
                        containerColor = Color(0xFF00FFFF).copy(alpha = 0.2f),
                        contentColor = Color(0xFF00FFFF)
                    ) {
                        Text(domain.serviceType, fontSize = 9.sp)
                    }
                }
            }
            
            Spacer(modifier = Modifier.height(8.dp))
            
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically
            ) {
                Text(
                    "Owner: ${domain.owner.take(12)}...",
                    color = Color(0xFF00FF00).copy(alpha = 0.5f),
                    fontSize = 10.sp
                )
                
                if (expiresAt != null) {
                    val daysUntilExpiry = ((expiresAt.time - System.currentTimeMillis()) / (1000 * 60 * 60 * 24)).toInt()
                    Text(
                        if (daysUntilExpiry > 0) "Expires in ${daysUntilExpiry}d" else "EXPIRED",
                        color = if (isExpiringSoon) Color.Yellow else Color(0xFF00FF00).copy(alpha = 0.5f),
                        fontSize = 10.sp
                    )
                }
            }
            
            if (isOwned && onRenew != null && isExpiringSoon) {
                Spacer(modifier = Modifier.height(8.dp))
                Button(
                    onClick = onRenew,
                    colors = ButtonDefaults.buttonColors(
                        containerColor = Color.Yellow,
                        contentColor = Color.Black
                    ),
                    modifier = Modifier.fillMaxWidth()
                ) {
                    Text("RENEW NOW", fontSize = 12.sp)
                }
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun RegisterDomainDialog(
    onDismiss: () -> Unit,
    onRegister: (String, String, String) -> Unit
) {
    var name by remember { mutableStateOf("") }
    var description by remember { mutableStateOf("") }
    var serviceType by remember { mutableStateOf("") }
    var isRegistering by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    
    val serviceTypes = listOf("", "chat", "market", "file", "api", "web", "other")
    
    // Validate domain name
    val isValidName = name.matches(Regex("^[a-z0-9][a-z0-9-]*[a-z0-9]$|^[a-z0-9]$")) && 
                      name.length >= 3 && 
                      name.length <= 32
    
    AlertDialog(
        onDismissRequest = onDismiss,
        containerColor = Color(0xFF0D0D0D),
        title = { Text("REGISTER DOMAIN", color = Color(0xFF00FF00)) },
        text = {
            Column {
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it.lowercase().filter { c -> c.isLetterOrDigit() || c == '-' } },
                    label = { Text("Domain Name", color = Color(0xFF00FF00).copy(alpha = 0.7f)) },
                    suffix = { Text(".kyk", color = Color(0xFF00FF00)) },
                    modifier = Modifier.fillMaxWidth(),
                    isError = name.isNotEmpty() && !isValidName,
                    supportingText = {
                        if (name.isNotEmpty() && !isValidName) {
                            Text("3-32 chars, alphanumeric and hyphens only", color = Color.Red, fontSize = 10.sp)
                        }
                    },
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
                    value = description,
                    onValueChange = { description = it },
                    label = { Text("Description (optional)", color = Color(0xFF00FF00).copy(alpha = 0.7f)) },
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
                
                var serviceExpanded by remember { mutableStateOf(false) }
                ExposedDropdownMenuBox(
                    expanded = serviceExpanded,
                    onExpandedChange = { serviceExpanded = it }
                ) {
                    OutlinedTextField(
                        value = serviceType.ifEmpty { "None" },
                        onValueChange = {},
                        readOnly = true,
                        label = { Text("Service Type", color = Color(0xFF00FF00).copy(alpha = 0.7f)) },
                        trailingIcon = { ExposedDropdownMenuDefaults.TrailingIcon(expanded = serviceExpanded) },
                        modifier = Modifier
                            .fillMaxWidth()
                            .menuAnchor(),
                        colors = OutlinedTextFieldDefaults.colors(
                            focusedBorderColor = Color(0xFF00FF00),
                            unfocusedBorderColor = Color(0xFF00FF00).copy(alpha = 0.3f),
                            focusedTextColor = Color(0xFF00FF00),
                            unfocusedTextColor = Color(0xFF00FF00)
                        )
                    )
                    ExposedDropdownMenu(
                        expanded = serviceExpanded,
                        onDismissRequest = { serviceExpanded = false },
                        modifier = Modifier.background(Color(0xFF0D0D0D))
                    ) {
                        serviceTypes.forEach { type ->
                            DropdownMenuItem(
                                text = { Text(type.ifEmpty { "None" }, color = Color(0xFF00FF00)) },
                                onClick = {
                                    serviceType = type
                                    serviceExpanded = false
                                }
                            )
                        }
                    }
                }
                
                error?.let {
                    Spacer(modifier = Modifier.height(8.dp))
                    Text(it, color = Color.Red, fontSize = 12.sp)
                }
                
                Spacer(modifier = Modifier.height(8.dp))
                
                Text(
                    "Registration is valid for 1 year",
                    color = Color(0xFF00FF00).copy(alpha = 0.5f),
                    fontSize = 11.sp
                )
            }
        },
        confirmButton = {
            Button(
                onClick = {
                    if (isValidName) {
                        isRegistering = true
                        onRegister(name, description, serviceType)
                    }
                },
                enabled = name.isNotBlank() && isValidName && !isRegistering,
                colors = ButtonDefaults.buttonColors(
                    containerColor = Color(0xFF00FF00),
                    contentColor = Color.Black
                )
            ) {
                if (isRegistering) {
                    CircularProgressIndicator(
                        modifier = Modifier.size(16.dp),
                        color = Color.Black,
                        strokeWidth = 2.dp
                    )
                } else {
                    Text("REGISTER")
                }
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) {
                Text("CANCEL", color = Color(0xFF00FF00))
            }
        }
    )
}

@Composable
fun WhoisDialog(
    onDismiss: () -> Unit,
    onLookup: (String) -> Unit
) {
    var query by remember { mutableStateOf("") }
    var isLooking by remember { mutableStateOf(false) }
    
    AlertDialog(
        onDismissRequest = onDismiss,
        containerColor = Color(0xFF0D0D0D),
        title = { Text("WHOIS LOOKUP", color = Color(0xFF00FF00)) },
        text = {
            OutlinedTextField(
                value = query,
                onValueChange = { query = it.lowercase().filter { c -> c.isLetterOrDigit() || c == '-' } },
                label = { Text("Domain Name", color = Color(0xFF00FF00).copy(alpha = 0.7f)) },
                suffix = { Text(".kyk", color = Color(0xFF00FF00)) },
                modifier = Modifier.fillMaxWidth(),
                colors = OutlinedTextFieldDefaults.colors(
                    focusedBorderColor = Color(0xFF00FF00),
                    unfocusedBorderColor = Color(0xFF00FF00).copy(alpha = 0.3f),
                    cursorColor = Color(0xFF00FF00),
                    focusedTextColor = Color(0xFF00FF00),
                    unfocusedTextColor = Color(0xFF00FF00)
                )
            )
        },
        confirmButton = {
            Button(
                onClick = {
                    if (query.isNotBlank()) {
                        isLooking = true
                        onLookup(query)
                    }
                },
                enabled = query.isNotBlank() && !isLooking,
                colors = ButtonDefaults.buttonColors(
                    containerColor = Color(0xFF00FF00),
                    contentColor = Color.Black
                )
            ) {
                if (isLooking) {
                    CircularProgressIndicator(
                        modifier = Modifier.size(16.dp),
                        color = Color.Black,
                        strokeWidth = 2.dp
                    )
                } else {
                    Text("LOOKUP")
                }
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) {
                Text("CANCEL", color = Color(0xFF00FF00))
            }
        }
    )
}

@Composable
fun DomainDetailDialog(
    domain: Domain,
    onDismiss: () -> Unit
) {
    val dateFormat = SimpleDateFormat("yyyy-MM-dd HH:mm", Locale.getDefault())
    
    AlertDialog(
        onDismissRequest = onDismiss,
        containerColor = Color(0xFF0D0D0D),
        title = { 
            Text(
                "${domain.name}.kyk",
                color = Color(0xFF00FF00),
                fontWeight = FontWeight.Bold
            )
        },
        text = {
            Column {
                if (domain.description.isNotEmpty()) {
                    Text(
                        domain.description,
                        color = Color(0xFF00FF00).copy(alpha = 0.9f)
                    )
                    Spacer(modifier = Modifier.height(16.dp))
                }
                
                DetailRow("Owner", domain.owner.take(24) + "...")
                
                domain.serviceType?.let {
                    if (it.isNotEmpty()) {
                        DetailRow("Service Type", it)
                    }
                }
                
                val createdDate = domain.createdAt?.let { parseDate(it) }
                if (createdDate != null) {
                    DetailRow("Registered", dateFormat.format(createdDate))
                }
                
                val expiresDate = domain.expiresAt?.let { parseDate(it) }
                if (expiresDate != null) {
                    DetailRow("Expires", dateFormat.format(expiresDate))
                }
            }
        },
        confirmButton = {
            TextButton(onClick = onDismiss) {
                Text("CLOSE", color = Color(0xFF00FF00))
            }
        }
    )
}

private fun parseDate(dateStr: String): Date? {
    return try {
        val inputFormat = SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss", Locale.getDefault())
        inputFormat.parse(dateStr.take(19))
    } catch (e: Exception) {
        null
    }
}

@Composable
private fun DetailRow(label: String, value: String) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 4.dp),
        horizontalArrangement = Arrangement.SpaceBetween
    ) {
        Text(
            label,
            color = Color(0xFF00FF00).copy(alpha = 0.5f),
            fontSize = 12.sp
        )
        Text(
            value,
            color = Color(0xFF00FF00),
            fontSize = 12.sp
        )
    }
}
