package net.kayaknet.app.ui.screens

import androidx.compose.foundation.border
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import net.kayaknet.app.KayakNetApp
import net.kayaknet.app.network.ConnectionState

@Composable
fun HomeScreen() {
    val client = KayakNetApp.instance.client
    val connectionState by client.connectionState.collectAsState()
    val peers by client.peers.collectAsState()
    val listings by client.listings.collectAsState()
    val domains by client.domains.collectAsState()
    
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(16.dp)
            .verticalScroll(rememberScrollState()),
        verticalArrangement = Arrangement.spacedBy(16.dp)
    ) {
        // Banner
        TerminalBox {
            Column(
                modifier = Modifier.fillMaxWidth(),
                horizontalAlignment = Alignment.CenterHorizontally
            ) {
                Text(
                    text = "KAYAKNET",
                    style = MaterialTheme.typography.headlineLarge,
                    color = MaterialTheme.colorScheme.primary
                )
                Text(
                    text = "Anonymous P2P Network",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
                Spacer(modifier = Modifier.height(8.dp))
                Text(
                    text = "Node: ${client.getNodeId().take(16)}...",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
        }
        
        // Stats Grid
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(12.dp)
        ) {
            StatCard(
                title = "STATUS",
                value = if (connectionState == ConnectionState.CONNECTED) "ONLINE" else "OFFLINE",
                modifier = Modifier.weight(1f)
            )
            StatCard(
                title = "PEERS",
                value = peers.size.toString(),
                modifier = Modifier.weight(1f)
            )
        }
        
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(12.dp)
        ) {
            StatCard(
                title = "LISTINGS",
                value = listings.size.toString(),
                modifier = Modifier.weight(1f)
            )
            StatCard(
                title = "DOMAINS",
                value = domains.size.toString(),
                modifier = Modifier.weight(1f)
            )
        }
        
        // Features
        TerminalBox {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(
                    text = "> FEATURES",
                    style = MaterialTheme.typography.titleMedium,
                    color = MaterialTheme.colorScheme.primary
                )
                FeatureItem("[+] Onion routing (3-hop)")
                FeatureItem("[+] End-to-end encryption")
                FeatureItem("[+] Traffic analysis resistance")
                FeatureItem("[+] P2P marketplace with escrow")
                FeatureItem("[+] .kyk domain system")
                FeatureItem("[+] Anonymous chat")
            }
        }
        
        // Connection Button
        if (connectionState != ConnectionState.CONNECTED) {
            Button(
                onClick = { client.connect() },
                modifier = Modifier.fillMaxWidth(),
                colors = ButtonDefaults.buttonColors(
                    containerColor = MaterialTheme.colorScheme.primary,
                    contentColor = MaterialTheme.colorScheme.onPrimary
                )
            ) {
                Text("CONNECT TO NETWORK")
            }
        } else {
            OutlinedButton(
                onClick = { client.disconnect() },
                modifier = Modifier.fillMaxWidth(),
                colors = ButtonDefaults.outlinedButtonColors(
                    contentColor = MaterialTheme.colorScheme.error
                )
            ) {
                Text("DISCONNECT")
            }
        }
    }
}

@Composable
fun TerminalBox(
    modifier: Modifier = Modifier,
    content: @Composable () -> Unit
) {
    Box(
        modifier = modifier
            .fillMaxWidth()
            .border(
                width = 1.dp,
                color = MaterialTheme.colorScheme.primary.copy(alpha = 0.3f)
            )
            .padding(16.dp)
    ) {
        content()
    }
}

@Composable
fun StatCard(
    title: String,
    value: String,
    modifier: Modifier = Modifier
) {
    TerminalBox(modifier = modifier) {
        Column(
            modifier = Modifier.fillMaxWidth(),
            horizontalAlignment = Alignment.CenterHorizontally
        ) {
            Text(
                text = title,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant
            )
            Text(
                text = value,
                style = MaterialTheme.typography.headlineMedium,
                color = MaterialTheme.colorScheme.primary
            )
        }
    }
}

@Composable
fun FeatureItem(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.bodyMedium,
        color = MaterialTheme.colorScheme.onSurfaceVariant
    )
}

