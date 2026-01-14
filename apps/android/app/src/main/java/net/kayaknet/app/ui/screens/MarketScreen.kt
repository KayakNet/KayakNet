package net.kayaknet.app.ui.screens

import android.net.Uri
import android.util.Base64
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.launch
import net.kayaknet.app.KayakNetApp
import net.kayaknet.app.network.ConnectionState
import net.kayaknet.app.network.Listing
import java.text.NumberFormat
import java.util.*

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MarketScreen() {
    val client = KayakNetApp.instance.client
    val connectionState by client.connectionState.collectAsState()
    val listings by client.listings.collectAsState()
    val myEscrows by client.myEscrows.collectAsState()
    val scope = rememberCoroutineScope()
    val context = LocalContext.current
    
    var searchQuery by remember { mutableStateOf("") }
    var selectedCategory by remember { mutableStateOf<String?>(null) }
    var showCreateListing by remember { mutableStateOf(false) }
    var selectedListing by remember { mutableStateOf<Listing?>(null) }
    var showEscrows by remember { mutableStateOf(false) }
    var sortBy by remember { mutableStateOf("recent") }
    
    val categories = listOf(
        "All", "Digital Goods", "Services", "Physical", "Art", "Software", "Other"
    )
    
    val filteredListings = listings.filter { listing ->
        val matchesSearch = searchQuery.isEmpty() || 
            listing.title.contains(searchQuery, ignoreCase = true) ||
            listing.description.contains(searchQuery, ignoreCase = true)
        val matchesCategory = selectedCategory == null || 
            selectedCategory == "All" ||
            listing.category.equals(selectedCategory, ignoreCase = true)
        matchesSearch && matchesCategory
    }.let { list ->
        when (sortBy) {
            "price_low" -> list.sortedBy { it.price }
            "price_high" -> list.sortedByDescending { it.price }
            else -> list // recent - keep original order
        }
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
                "MARKETPLACE",
                color = Color(0xFF00FF00),
                fontSize = 20.sp,
                fontWeight = FontWeight.Bold
            )
            Row {
                IconButton(onClick = { showEscrows = true }) {
                    Badge(
                        containerColor = if (myEscrows.isNotEmpty()) Color.Red else Color.Transparent
                    ) {
                        if (myEscrows.isNotEmpty()) {
                            Text(myEscrows.size.toString(), fontSize = 10.sp)
                        }
                    }
                    Icon(
                        Icons.Filled.Lock,
                        contentDescription = "Escrows",
                        tint = Color(0xFF00FF00)
                    )
                }
                IconButton(onClick = { showCreateListing = true }) {
                    Icon(
                        Icons.Filled.Add,
                        contentDescription = "Create Listing",
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
        
        // Search
        OutlinedTextField(
            value = searchQuery,
            onValueChange = { searchQuery = it },
            modifier = Modifier.fillMaxWidth(),
            placeholder = { Text("Search listings...", color = Color(0xFF00FF00).copy(alpha = 0.5f)) },
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
        
        // Categories
        LazyRow(
            horizontalArrangement = Arrangement.spacedBy(8.dp)
        ) {
            items(categories) { category ->
                FilterChip(
                    selected = selectedCategory == category || (category == "All" && selectedCategory == null),
                    onClick = { 
                        selectedCategory = if (category == "All") null else category
                    },
                    label = { 
                        Text(
                            category.uppercase(),
                            fontSize = 10.sp
                        )
                    },
                    colors = FilterChipDefaults.filterChipColors(
                        selectedContainerColor = Color(0xFF00FF00),
                        selectedLabelColor = Color.Black,
                        containerColor = Color.Transparent,
                        labelColor = Color(0xFF00FF00)
                    ),
                    border = FilterChipDefaults.filterChipBorder(
                        borderColor = Color(0xFF00FF00).copy(alpha = 0.3f),
                        selectedBorderColor = Color(0xFF00FF00),
                        enabled = true,
                        selected = selectedCategory == category
                    )
                )
            }
        }
        
        Spacer(modifier = Modifier.height(8.dp))
        
        // Sort options
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically
        ) {
            Text(
                "${filteredListings.size} listings",
                color = Color(0xFF00FF00).copy(alpha = 0.7f),
                fontSize = 12.sp
            )
            Row {
                listOf("recent" to "Recent", "price_low" to "Price ↑", "price_high" to "Price ↓").forEach { (key, label) ->
                    TextButton(
                        onClick = { sortBy = key },
                        colors = ButtonDefaults.textButtonColors(
                            contentColor = if (sortBy == key) Color(0xFF00FF00) else Color(0xFF00FF00).copy(alpha = 0.5f)
                        )
                    ) {
                        Text(label, fontSize = 10.sp)
                    }
                }
            }
        }
        
        Spacer(modifier = Modifier.height(8.dp))
        
        // Listings
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
        } else if (filteredListings.isEmpty()) {
            Box(
                modifier = Modifier.fillMaxSize(),
                contentAlignment = Alignment.Center
            ) {
                Text("// No listings found", color = Color(0xFF00FF00).copy(alpha = 0.5f))
            }
        } else {
            LazyColumn(
                verticalArrangement = Arrangement.spacedBy(12.dp)
            ) {
                items(filteredListings) { listing ->
                    ListingCard(
                        listing = listing,
                        onClick = { selectedListing = listing }
                    )
                }
            }
        }
    }
    
    // Create Listing Dialog
    if (showCreateListing) {
        CreateListingDialog(
            onDismiss = { showCreateListing = false },
            onCreate = { title, description, price, currency, category, imageData ->
                scope.launch {
                    client.createListing(title, description, price, currency, category, imageData)
                    showCreateListing = false
                }
            }
        )
    }
    
    // Listing Detail Dialog
    selectedListing?.let { listing ->
        ListingDetailDialog(
            listing = listing,
            onDismiss = { selectedListing = null },
            onBuy = { amount, currency ->
                scope.launch {
                    client.createEscrow(listing.id, amount, currency)
                }
            },
            onMessageSeller = {
                // Navigate to DM
            }
        )
    }
    
    // Escrows Dialog
    if (showEscrows) {
        EscrowsDialog(
            escrows = myEscrows,
            onDismiss = { showEscrows = false },
            onRelease = { escrowId ->
                scope.launch {
                    client.releaseEscrow(escrowId)
                }
            },
            onDispute = { escrowId, reason ->
                scope.launch {
                    client.disputeEscrow(escrowId, reason)
                }
            }
        )
    }
}

@Composable
fun ListingCard(
    listing: Listing,
    onClick: () -> Unit
) {
    Card(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick),
        colors = CardDefaults.cardColors(
            containerColor = Color(0xFF0D0D0D)
        ),
        border = CardDefaults.outlinedCardBorder().copy(
            brush = androidx.compose.ui.graphics.SolidColor(Color(0xFF00FF00).copy(alpha = 0.3f))
        )
    ) {
        Column(
            modifier = Modifier.padding(12.dp)
        ) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.Top
            ) {
                Column(modifier = Modifier.weight(1f)) {
                    Text(
                        listing.title,
                        color = Color(0xFF00FF00),
                        fontWeight = FontWeight.Bold,
                        fontSize = 14.sp,
                        maxLines = 2,
                        overflow = TextOverflow.Ellipsis
                    )
                    Spacer(modifier = Modifier.height(4.dp))
                    Text(
                        listing.description,
                        color = Color(0xFF00FF00).copy(alpha = 0.7f),
                        fontSize = 12.sp,
                        maxLines = 2,
                        overflow = TextOverflow.Ellipsis
                    )
                }
                
                Column(horizontalAlignment = Alignment.End) {
                    Text(
                        "${listing.price} ${listing.currency}",
                        color = Color(0xFF00FFFF),
                        fontWeight = FontWeight.Bold,
                        fontSize = 14.sp
                    )
                    Text(
                        listing.category,
                        color = Color(0xFF00FF00).copy(alpha = 0.5f),
                        fontSize = 10.sp
                    )
                }
            }
            
            Spacer(modifier = Modifier.height(8.dp))
            
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween
            ) {
                Text(
                    "by ${listing.sellerName.ifEmpty { listing.seller.take(8) }}",
                    color = Color(0xFF00FF00).copy(alpha = 0.5f),
                    fontSize = 10.sp
                )
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun CreateListingDialog(
    onDismiss: () -> Unit,
    onCreate: (String, String, Double, String, String, String?) -> Unit
) {
    var title by remember { mutableStateOf("") }
    var description by remember { mutableStateOf("") }
    var price by remember { mutableStateOf("") }
    var currency by remember { mutableStateOf("XMR") }
    var category by remember { mutableStateOf("Digital Goods") }
    var imageData by remember { mutableStateOf<String?>(null) }
    var isCreating by remember { mutableStateOf(false) }
    val context = LocalContext.current
    
    val imagePickerLauncher = rememberLauncherForActivityResult(
        contract = ActivityResultContracts.GetContent()
    ) { uri: Uri? ->
        uri?.let {
            try {
                val inputStream = context.contentResolver.openInputStream(uri)
                val bytes = inputStream?.readBytes()
                inputStream?.close()
                if (bytes != null && bytes.size <= 512 * 1024) {
                    imageData = Base64.encodeToString(bytes, Base64.NO_WRAP)
                }
            } catch (e: Exception) { }
        }
    }
    
    AlertDialog(
        onDismissRequest = onDismiss,
        containerColor = Color(0xFF0D0D0D),
        title = { Text("CREATE LISTING", color = Color(0xFF00FF00)) },
        text = {
            Column(
                modifier = Modifier.verticalScroll(androidx.compose.foundation.rememberScrollState())
            ) {
                OutlinedTextField(
                    value = title,
                    onValueChange = { title = it },
                    label = { Text("Title", color = Color(0xFF00FF00).copy(alpha = 0.7f)) },
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
                    value = description,
                    onValueChange = { description = it },
                    label = { Text("Description", color = Color(0xFF00FF00).copy(alpha = 0.7f)) },
                    modifier = Modifier.fillMaxWidth(),
                    minLines = 3,
                    colors = OutlinedTextFieldDefaults.colors(
                        focusedBorderColor = Color(0xFF00FF00),
                        unfocusedBorderColor = Color(0xFF00FF00).copy(alpha = 0.3f),
                        cursorColor = Color(0xFF00FF00),
                        focusedTextColor = Color(0xFF00FF00),
                        unfocusedTextColor = Color(0xFF00FF00)
                    )
                )
                
                Spacer(modifier = Modifier.height(8.dp))
                
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.spacedBy(8.dp)
                ) {
                    OutlinedTextField(
                        value = price,
                        onValueChange = { price = it.filter { c -> c.isDigit() || c == '.' } },
                        label = { Text("Price", color = Color(0xFF00FF00).copy(alpha = 0.7f)) },
                        modifier = Modifier.weight(1f),
                        colors = OutlinedTextFieldDefaults.colors(
                            focusedBorderColor = Color(0xFF00FF00),
                            unfocusedBorderColor = Color(0xFF00FF00).copy(alpha = 0.3f),
                            cursorColor = Color(0xFF00FF00),
                            focusedTextColor = Color(0xFF00FF00),
                            unfocusedTextColor = Color(0xFF00FF00)
                        )
                    )
                    
                    var currencyExpanded by remember { mutableStateOf(false) }
                    ExposedDropdownMenuBox(
                        expanded = currencyExpanded,
                        onExpandedChange = { currencyExpanded = it },
                        modifier = Modifier.weight(1f)
                    ) {
                        OutlinedTextField(
                            value = currency,
                            onValueChange = {},
                            readOnly = true,
                            label = { Text("Currency", color = Color(0xFF00FF00).copy(alpha = 0.7f)) },
                            trailingIcon = { ExposedDropdownMenuDefaults.TrailingIcon(expanded = currencyExpanded) },
                            modifier = Modifier.menuAnchor(),
                            colors = OutlinedTextFieldDefaults.colors(
                                focusedBorderColor = Color(0xFF00FF00),
                                unfocusedBorderColor = Color(0xFF00FF00).copy(alpha = 0.3f),
                                focusedTextColor = Color(0xFF00FF00),
                                unfocusedTextColor = Color(0xFF00FF00)
                            )
                        )
                        ExposedDropdownMenu(
                            expanded = currencyExpanded,
                            onDismissRequest = { currencyExpanded = false },
                            containerColor = Color(0xFF0D0D0D)
                        ) {
                            listOf("XMR", "ZEC").forEach { cur ->
                                DropdownMenuItem(
                                    text = { Text(cur, color = Color(0xFF00FF00)) },
                                    onClick = {
                                        currency = cur
                                        currencyExpanded = false
                                    }
                                )
                            }
                        }
                    }
                }
                
                Spacer(modifier = Modifier.height(8.dp))
                
                var categoryExpanded by remember { mutableStateOf(false) }
                ExposedDropdownMenuBox(
                    expanded = categoryExpanded,
                    onExpandedChange = { categoryExpanded = it }
                ) {
                    OutlinedTextField(
                        value = category,
                        onValueChange = {},
                        readOnly = true,
                        label = { Text("Category", color = Color(0xFF00FF00).copy(alpha = 0.7f)) },
                        trailingIcon = { ExposedDropdownMenuDefaults.TrailingIcon(expanded = categoryExpanded) },
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
                        expanded = categoryExpanded,
                        onDismissRequest = { categoryExpanded = false },
                        containerColor = Color(0xFF0D0D0D)
                    ) {
                        listOf("Digital Goods", "Services", "Physical", "Art", "Software", "Other").forEach { cat ->
                            DropdownMenuItem(
                                text = { Text(cat, color = Color(0xFF00FF00)) },
                                onClick = {
                                    category = cat
                                    categoryExpanded = false
                                }
                            )
                        }
                    }
                }
                
                Spacer(modifier = Modifier.height(8.dp))
                
                OutlinedButton(
                    onClick = { imagePickerLauncher.launch("image/*") },
                    modifier = Modifier.fillMaxWidth(),
                    colors = ButtonDefaults.outlinedButtonColors(
                        contentColor = Color(0xFF00FF00)
                    ),
                    border = ButtonDefaults.outlinedButtonBorder.copy(
                        brush = androidx.compose.ui.graphics.SolidColor(Color(0xFF00FF00).copy(alpha = 0.5f))
                    )
                ) {
                    Icon(Icons.Filled.Add, contentDescription = null)
                    Spacer(modifier = Modifier.width(8.dp))
                    Text(if (imageData != null) "Image Added" else "Add Image")
                }
            }
        },
        confirmButton = {
            Button(
                onClick = {
                    if (title.isNotBlank() && price.isNotBlank()) {
                        isCreating = true
                        onCreate(title, description, price.toDoubleOrNull() ?: 0.0, currency, category, imageData)
                    }
                },
                enabled = title.isNotBlank() && price.isNotBlank() && !isCreating,
                colors = ButtonDefaults.buttonColors(
                    containerColor = Color(0xFF00FF00),
                    contentColor = Color.Black
                )
            ) {
                if (isCreating) {
                    CircularProgressIndicator(
                        modifier = Modifier.size(16.dp),
                        color = Color.Black,
                        strokeWidth = 2.dp
                    )
                } else {
                    Text("CREATE")
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
fun ListingDetailDialog(
    listing: Listing,
    onDismiss: () -> Unit,
    onBuy: (Double, String) -> Unit,
    onMessageSeller: () -> Unit
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        containerColor = Color(0xFF0D0D0D),
        title = { 
            Text(
                listing.title,
                color = Color(0xFF00FF00),
                fontWeight = FontWeight.Bold
            )
        },
        text = {
            Column {
                Text(
                    listing.description,
                    color = Color(0xFF00FF00).copy(alpha = 0.9f)
                )
                
                Spacer(modifier = Modifier.height(16.dp))
                
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.SpaceBetween
                ) {
                    Column {
                        Text("Price", color = Color(0xFF00FF00).copy(alpha = 0.5f), fontSize = 10.sp)
                        Text(
                            "${listing.price} ${listing.currency}",
                            color = Color(0xFF00FFFF),
                            fontWeight = FontWeight.Bold,
                            fontSize = 18.sp
                        )
                    }
                    Column(horizontalAlignment = Alignment.End) {
                        Text("Category", color = Color(0xFF00FF00).copy(alpha = 0.5f), fontSize = 10.sp)
                        Text(listing.category, color = Color(0xFF00FF00))
                    }
                }
                
                Spacer(modifier = Modifier.height(16.dp))
                
                Text("Seller", color = Color(0xFF00FF00).copy(alpha = 0.5f), fontSize = 10.sp)
                Text(
                    listing.sellerName.ifEmpty { listing.seller.take(16) },
                    color = Color(0xFF00FF00)
                )
            }
        },
        confirmButton = {
            Button(
                onClick = { onBuy(listing.price, listing.currency) },
                colors = ButtonDefaults.buttonColors(
                    containerColor = Color(0xFF00FF00),
                    contentColor = Color.Black
                )
            ) {
                Text("BUY NOW")
            }
        },
        dismissButton = {
            Row {
                TextButton(onClick = onMessageSeller) {
                    Text("MESSAGE", color = Color(0xFF00FF00))
                }
                TextButton(onClick = onDismiss) {
                    Text("CLOSE", color = Color(0xFF00FF00))
                }
            }
        }
    )
}

@Composable
fun EscrowsDialog(
    escrows: List<net.kayaknet.app.network.Escrow>,
    onDismiss: () -> Unit,
    onRelease: (String) -> Unit,
    onDispute: (String, String) -> Unit
) {
    var disputeReason by remember { mutableStateOf("") }
    var showDisputeFor by remember { mutableStateOf<String?>(null) }
    
    AlertDialog(
        onDismissRequest = onDismiss,
        containerColor = Color(0xFF0D0D0D),
        title = { Text("MY ESCROWS", color = Color(0xFF00FF00)) },
        text = {
            if (escrows.isEmpty()) {
                Text("No active escrows", color = Color(0xFF00FF00).copy(alpha = 0.5f))
            } else {
                LazyColumn {
                    items(escrows) { escrow ->
                        Card(
                            modifier = Modifier
                                .fillMaxWidth()
                                .padding(vertical = 4.dp),
                            colors = CardDefaults.cardColors(containerColor = Color(0xFF151515))
                        ) {
                            Column(modifier = Modifier.padding(12.dp)) {
                                Row(
                                    modifier = Modifier.fillMaxWidth(),
                                    horizontalArrangement = Arrangement.SpaceBetween
                                ) {
                                    Text(
                                        "${escrow.amount} ${escrow.currency}",
                                        color = Color(0xFF00FFFF),
                                        fontWeight = FontWeight.Bold
                                    )
                                    Text(
                                        escrow.status.uppercase(),
                                        color = when (escrow.status) {
                                            "pending" -> Color.Yellow
                                            "funded" -> Color.Green
                                            "released" -> Color.Cyan
                                            "disputed" -> Color.Red
                                            else -> Color.Gray
                                        },
                                        fontSize = 10.sp
                                    )
                                }
                                
                                Spacer(modifier = Modifier.height(4.dp))
                                
                                Text(
                                    "ID: ${escrow.id.take(16)}...",
                                    color = Color(0xFF00FF00).copy(alpha = 0.5f),
                                    fontSize = 10.sp
                                )
                                
                                if (escrow.status == "funded") {
                                    Spacer(modifier = Modifier.height(8.dp))
                                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                        Button(
                                            onClick = { onRelease(escrow.id) },
                                            colors = ButtonDefaults.buttonColors(
                                                containerColor = Color.Green,
                                                contentColor = Color.Black
                                            ),
                                            modifier = Modifier.weight(1f)
                                        ) {
                                            Text("RELEASE", fontSize = 10.sp)
                                        }
                                        Button(
                                            onClick = { showDisputeFor = escrow.id },
                                            colors = ButtonDefaults.buttonColors(
                                                containerColor = Color.Red,
                                                contentColor = Color.White
                                            ),
                                            modifier = Modifier.weight(1f)
                                        ) {
                                            Text("DISPUTE", fontSize = 10.sp)
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
            }
        },
        confirmButton = {
            TextButton(onClick = onDismiss) {
                Text("CLOSE", color = Color(0xFF00FF00))
            }
        }
    )
    
    // Dispute dialog
    if (showDisputeFor != null) {
        AlertDialog(
            onDismissRequest = { showDisputeFor = null },
            containerColor = Color(0xFF0D0D0D),
            title = { Text("DISPUTE ESCROW", color = Color.Red) },
            text = {
                OutlinedTextField(
                    value = disputeReason,
                    onValueChange = { disputeReason = it },
                    label = { Text("Reason", color = Color(0xFF00FF00).copy(alpha = 0.7f)) },
                    modifier = Modifier.fillMaxWidth(),
                    minLines = 3,
                    colors = OutlinedTextFieldDefaults.colors(
                        focusedBorderColor = Color.Red,
                        unfocusedBorderColor = Color.Red.copy(alpha = 0.3f),
                        cursorColor = Color.Red,
                        focusedTextColor = Color(0xFF00FF00),
                        unfocusedTextColor = Color(0xFF00FF00)
                    )
                )
            },
            confirmButton = {
                Button(
                    onClick = {
                        onDispute(showDisputeFor!!, disputeReason)
                        showDisputeFor = null
                        disputeReason = ""
                    },
                    colors = ButtonDefaults.buttonColors(
                        containerColor = Color.Red,
                        contentColor = Color.White
                    )
                ) {
                    Text("SUBMIT DISPUTE")
                }
            },
            dismissButton = {
                TextButton(onClick = { showDisputeFor = null }) {
                    Text("CANCEL", color = Color(0xFF00FF00))
                }
            }
        )
    }
}
