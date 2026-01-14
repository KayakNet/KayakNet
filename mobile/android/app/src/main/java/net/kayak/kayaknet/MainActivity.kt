package net.kayak.kayaknet

import android.content.Intent
import android.os.Bundle
import android.view.Menu
import android.view.MenuItem
import android.widget.Toast
import androidx.appcompat.app.AppCompatActivity
import androidx.fragment.app.Fragment
import com.google.android.material.bottomnavigation.BottomNavigationView
import net.kayak.kayaknet.databinding.ActivityMainBinding
import net.kayak.kayaknet.fragments.*
import net.kayak.kayaknet.service.KayakNetService

class MainActivity : AppCompatActivity() {
    
    private lateinit var binding: ActivityMainBinding
    
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        binding = ActivityMainBinding.inflate(layoutInflater)
        setContentView(binding.root)
        
        setSupportActionBar(binding.toolbar)
        
        setupBottomNavigation()
        
        // Start the network service
        startKayakNetService()
        
        // Load default fragment
        if (savedInstanceState == null) {
            loadFragment(HomeFragment())
        }
    }
    
    private fun setupBottomNavigation() {
        binding.bottomNavigation.setOnItemSelectedListener { item ->
            val fragment: Fragment = when (item.itemId) {
                R.id.nav_home -> HomeFragment()
                R.id.nav_chat -> ChatFragment()
                R.id.nav_market -> MarketFragment()
                R.id.nav_domains -> DomainsFragment()
                R.id.nav_settings -> SettingsFragment()
                else -> HomeFragment()
            }
            loadFragment(fragment)
            true
        }
    }
    
    private fun loadFragment(fragment: Fragment) {
        supportFragmentManager.beginTransaction()
            .replace(R.id.fragmentContainer, fragment)
            .commit()
    }
    
    private fun startKayakNetService() {
        val intent = Intent(this, KayakNetService::class.java)
        startForegroundService(intent)
    }
    
    override fun onCreateOptionsMenu(menu: Menu): Boolean {
        menuInflater.inflate(R.menu.main_menu, menu)
        return true
    }
    
    override fun onOptionsItemSelected(item: MenuItem): Boolean {
        return when (item.itemId) {
            R.id.action_status -> {
                showNetworkStatus()
                true
            }
            R.id.action_refresh -> {
                refreshData()
                true
            }
            else -> super.onOptionsItemSelected(item)
        }
    }
    
    private fun showNetworkStatus() {
        val node = KayakNetApp.node
        if (node == null) {
            Toast.makeText(this, "Not connected", Toast.LENGTH_SHORT).show()
            return
        }
        
        val status = """
            Node ID: ${node.nodeID.take(16)}...
            Peers: ${node.peerCount}
            Anonymous: ${if (node.isAnonymous) "Yes" else "No"}
            Version: ${node.version}
        """.trimIndent()
        
        androidx.appcompat.app.AlertDialog.Builder(this)
            .setTitle("Network Status")
            .setMessage(status)
            .setPositiveButton("OK", null)
            .show()
    }
    
    private fun refreshData() {
        Toast.makeText(this, "Refreshing...", Toast.LENGTH_SHORT).show()
        // Trigger refresh in current fragment
        val currentFragment = supportFragmentManager.findFragmentById(R.id.fragmentContainer)
        if (currentFragment is RefreshableFragment) {
            currentFragment.refresh()
        }
    }
    
    override fun onDestroy() {
        super.onDestroy()
        // Don't stop service - let it run in background
    }
}

interface RefreshableFragment {
    fun refresh()
}

