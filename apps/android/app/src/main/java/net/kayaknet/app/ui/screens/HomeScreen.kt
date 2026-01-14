package net.kayaknet.app.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
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
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import net.kayaknet.app.KayakNetApp
import net.kayaknet.app.network.ConnectionState

@Composable
fun HomeScreen(
    onNavigate: (String) -> Unit
) {
    val client = KayakNetApp.instance.client
    val connectionState by client.connectionState.collectAsState()
    val networkStats by client.networkStats.collectAsState()
    val unreadDMs by client.unreadDMs.collectAsState()
    
    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(Color(0xFF0A0A0A))
            .padding(16.dp),
        horizontalAlignment = Alignment.CenterHorizontally
    ) {
        Spacer(modifier = Modifier.height(24.dp))
        
        // Logo
        Box(
            modifier = Modifier
                .size(80.dp)
                .clip(CircleShape)
                .background(Color(0xFF00FF00).copy(alpha = 0.1f)),
            contentAlignment = Alignment.Center
        ) {
            Text(
                "K",
                color = Color(0xFF00FF00),
                fontSize = 40.sp,
                fontWeight = FontWeight.Bold,
                fontFamily = FontFamily.Monospace
            )
        }
        
        Spacer(modifier = Modifier.height(16.dp))
        
        Text(
            "KAYAKNET",
            color = Color(0xFF00FF00),
            fontSize = 28.sp,
            fontWeight = FontWeight.Bold,
            fontFamily = FontFamily.Monospace
        )
        
        Text(
            "Privacy-First P2P Network",
            color = Color(0xFF00FF00).copy(alpha = 0.7f),
            fontSize = 14.sp,
            fontFamily = FontFamily.Monospace
        )
        
        Spacer(modifier = Modifier.height(24.dp))
        
        // Connection Status
        Card(
            modifier = Modifier.fillMaxWidth(),
            colors = CardDefaults.cardColors(containerColor = Color(0xFF0D0D0D)),
            shape = RoundedCornerShape(8.dp)
        ) {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(16.dp),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically
            ) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Box(
                        modifier = Modifier
                            .size(12.dp)
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
                    Spacer(modifier = Modifier.width(12.dp))
                    Column {
                        Text(
                            when (connectionState) {
                                ConnectionState.CONNECTED -> "ONLINE"
                                ConnectionState.CONNECTING -> "CONNECTING..."
                                ConnectionState.ERROR -> "ERROR"
                                ConnectionState.DISCONNECTED -> "OFFLINE"
                            },
                            color = Color(0xFF00FF00),
                            fontWeight = FontWeight.Bold,
                            fontSize = 14.sp
                        )
                        if (connectionState == ConnectionState.CONNECTED && networkStats != null) {
                            Text(
                                "${networkStats!!.peers} peers",
                                color = Color(0xFF00FF00).copy(alpha = 0.6f),
                                fontSize = 12.sp
                            )
                        }
                    }
                }
                
                when (connectionState) {
                    ConnectionState.CONNECTED -> {
                        IconButton(onClick = { client.disconnect() }) {
                            Icon(
                                Icons.Filled.Close,
                                contentDescription = "Disconnect",
                                tint = Color.Red
                            )
                        }
                    }
                    ConnectionState.DISCONNECTED, ConnectionState.ERROR -> {
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
                    ConnectionState.CONNECTING -> {
                        CircularProgressIndicator(
                            modifier = Modifier.size(24.dp),
                            color = Color(0xFF00FF00),
                            strokeWidth = 2.dp
                        )
                    }
                }
            }
        }
        
        Spacer(modifier = Modifier.height(24.dp))
        
        // Network Stats
        if (connectionState == ConnectionState.CONNECTED && networkStats != null) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceEvenly
            ) {
                StatCard("Peers", networkStats!!.peers.toString())
                StatCard("Listings", networkStats!!.listings.toString())
                StatCard("Domains", networkStats!!.domains.toString())
            }
            
            Spacer(modifier = Modifier.height(24.dp))
        }
        
        // Navigation Grid
        val navItems = listOf(
            NavItem("CHAT", Icons.Filled.Email, "chat", unreadDMs > 0),
            NavItem("MARKET", Icons.Filled.ShoppingCart, "market", false),
            NavItem("DOMAINS", Icons.Filled.Star, "domains", false),
            NavItem("SETTINGS", Icons.Filled.Settings, "settings", false)
        )
        
        LazyVerticalGrid(
            columns = GridCells.Fixed(2),
            horizontalArrangement = Arrangement.spacedBy(12.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
            modifier = Modifier.weight(1f)
        ) {
            items(navItems) { item ->
                NavCard(
                    item = item,
                    onClick = { onNavigate(item.route) }
                )
            }
        }
        
        // Footer
        Text(
            "v${networkStats?.version ?: "1.0.0"} | Decentralized & Private",
            color = Color(0xFF00FF00).copy(alpha = 0.4f),
            fontSize = 10.sp,
            fontFamily = FontFamily.Monospace,
            modifier = Modifier.padding(top = 8.dp)
        )
    }
}

@Composable
private fun StatCard(label: String, value: String) {
    Card(
        colors = CardDefaults.cardColors(containerColor = Color(0xFF0D0D0D)),
        shape = RoundedCornerShape(8.dp),
        modifier = Modifier.width(100.dp)
    ) {
        Column(
            modifier = Modifier.padding(12.dp),
            horizontalAlignment = Alignment.CenterHorizontally
        ) {
            Text(
                value,
                color = Color(0xFF00FF00),
                fontSize = 24.sp,
                fontWeight = FontWeight.Bold,
                fontFamily = FontFamily.Monospace
            )
            Text(
                label,
                color = Color(0xFF00FF00).copy(alpha = 0.6f),
                fontSize = 10.sp
            )
        }
    }
}

private data class NavItem(
    val title: String,
    val icon: ImageVector,
    val route: String,
    val hasNotification: Boolean
)

@Composable
private fun NavCard(
    item: NavItem,
    onClick: () -> Unit
) {
    Card(
        modifier = Modifier
            .aspectRatio(1f)
            .clickable(onClick = onClick),
        colors = CardDefaults.cardColors(containerColor = Color(0xFF0D0D0D)),
        shape = RoundedCornerShape(12.dp)
    ) {
        Box(
            modifier = Modifier.fillMaxSize(),
            contentAlignment = Alignment.Center
        ) {
            Column(
                horizontalAlignment = Alignment.CenterHorizontally
            ) {
                Box {
                    Icon(
                        item.icon,
                        contentDescription = item.title,
                        tint = Color(0xFF00FF00),
                        modifier = Modifier.size(40.dp)
                    )
                    if (item.hasNotification) {
                        Box(
                            modifier = Modifier
                                .size(12.dp)
                                .clip(CircleShape)
                                .background(Color.Red)
                                .align(Alignment.TopEnd)
                        )
                    }
                }
                Spacer(modifier = Modifier.height(8.dp))
                Text(
                    item.title,
                    color = Color(0xFF00FF00),
                    fontSize = 14.sp,
                    fontWeight = FontWeight.Bold,
                    fontFamily = FontFamily.Monospace
                )
            }
        }
    }
}
