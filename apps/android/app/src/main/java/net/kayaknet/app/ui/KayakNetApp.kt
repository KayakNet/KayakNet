package net.kayaknet.app.ui

import androidx.compose.foundation.layout.*
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.navigation.NavDestination.Companion.hierarchy
import androidx.navigation.NavGraph.Companion.findStartDestination
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import net.kayaknet.app.KayakNetApp as App
import net.kayaknet.app.network.ConnectionState
import net.kayaknet.app.ui.screens.*

sealed class Screen(val route: String, val title: String, val icon: @Composable () -> Unit) {
    object Home : Screen("home", "Home", { Icon(Icons.Filled.Home, contentDescription = null) })
    object Chat : Screen("chat", "Chat", { Icon(Icons.Filled.Chat, contentDescription = null) })
    object Market : Screen("market", "Market", { Icon(Icons.Filled.Store, contentDescription = null) })
    object Domains : Screen("domains", "Domains", { Icon(Icons.Filled.Language, contentDescription = null) })
    object Settings : Screen("settings", "Settings", { Icon(Icons.Filled.Settings, contentDescription = null) })
}

val bottomNavItems = listOf(
    Screen.Home,
    Screen.Chat,
    Screen.Market,
    Screen.Domains,
    Screen.Settings
)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun KayakNetApp() {
    val navController = rememberNavController()
    val client = App.instance.client
    val connectionState by client.connectionState.collectAsState()
    
    // Auto-connect on launch
    LaunchedEffect(Unit) {
        if (connectionState == ConnectionState.DISCONNECTED) {
            client.connect()
        }
    }
    
    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Row(
                        horizontalArrangement = Arrangement.spacedBy(8.dp)
                    ) {
                        Text("[KAYAKNET]")
                        ConnectionIndicator(connectionState)
                    }
                },
                colors = TopAppBarDefaults.topAppBarColors(
                    containerColor = MaterialTheme.colorScheme.background,
                    titleContentColor = MaterialTheme.colorScheme.primary
                )
            )
        },
        bottomBar = {
            NavigationBar(
                containerColor = MaterialTheme.colorScheme.surface,
                contentColor = MaterialTheme.colorScheme.primary
            ) {
                val navBackStackEntry by navController.currentBackStackEntryAsState()
                val currentDestination = navBackStackEntry?.destination
                
                bottomNavItems.forEach { screen ->
                    NavigationBarItem(
                        icon = screen.icon,
                        label = { Text(screen.title.uppercase()) },
                        selected = currentDestination?.hierarchy?.any { it.route == screen.route } == true,
                        onClick = {
                            navController.navigate(screen.route) {
                                popUpTo(navController.graph.findStartDestination().id) {
                                    saveState = true
                                }
                                launchSingleTop = true
                                restoreState = true
                            }
                        },
                        colors = NavigationBarItemDefaults.colors(
                            selectedIconColor = MaterialTheme.colorScheme.primary,
                            selectedTextColor = MaterialTheme.colorScheme.primary,
                            unselectedIconColor = MaterialTheme.colorScheme.onSurfaceVariant,
                            unselectedTextColor = MaterialTheme.colorScheme.onSurfaceVariant,
                            indicatorColor = MaterialTheme.colorScheme.primaryContainer
                        )
                    )
                }
            }
        }
    ) { innerPadding ->
        NavHost(
            navController = navController,
            startDestination = Screen.Home.route,
            modifier = Modifier.padding(innerPadding)
        ) {
            composable(Screen.Home.route) { HomeScreen() }
            composable(Screen.Chat.route) { ChatScreen() }
            composable(Screen.Market.route) { MarketScreen() }
            composable(Screen.Domains.route) { DomainsScreen() }
            composable(Screen.Settings.route) { SettingsScreen() }
        }
    }
}

@Composable
fun ConnectionIndicator(state: ConnectionState) {
    val color = when (state) {
        ConnectionState.CONNECTED -> MaterialTheme.colorScheme.primary
        ConnectionState.CONNECTING -> MaterialTheme.colorScheme.tertiary
        ConnectionState.DISCONNECTED -> MaterialTheme.colorScheme.onSurfaceVariant
        ConnectionState.ERROR -> MaterialTheme.colorScheme.error
    }
    
    val text = when (state) {
        ConnectionState.CONNECTED -> "[ONLINE]"
        ConnectionState.CONNECTING -> "[CONNECTING...]"
        ConnectionState.DISCONNECTED -> "[OFFLINE]"
        ConnectionState.ERROR -> "[ERROR]"
    }
    
    Text(
        text = text,
        color = color,
        style = MaterialTheme.typography.labelMedium
    )
}

