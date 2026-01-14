package net.kayak.kayaknet

import android.app.Application
import android.app.NotificationChannel
import android.app.NotificationManager
import android.os.Build
import kayaknet.MobileNode

/**
 * KayakNet Application class
 * Manages the Go mobile node lifecycle
 */
class KayakNetApp : Application() {
    
    companion object {
        const val CHANNEL_ID = "kayaknet_service"
        const val CHANNEL_NAME = "KayakNet Service"
        
        lateinit var instance: KayakNetApp
            private set
        
        var node: MobileNode? = null
            private set
        
        fun isNodeRunning(): Boolean = node != null
    }
    
    override fun onCreate() {
        super.onCreate()
        instance = this
        createNotificationChannel()
    }
    
    private fun createNotificationChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val channel = NotificationChannel(
                CHANNEL_ID,
                CHANNEL_NAME,
                NotificationManager.IMPORTANCE_LOW
            ).apply {
                description = "KayakNet P2P Network Service"
                setShowBadge(false)
            }
            
            val notificationManager = getSystemService(NotificationManager::class.java)
            notificationManager.createNotificationChannel(channel)
        }
    }
    
    /**
     * Initialize and start the KayakNet node
     */
    fun startNode(bootstrapAddr: String = "203.161.33.237:4242"): Boolean {
        if (node != null) return true
        
        return try {
            val dataDir = filesDir.absolutePath
            node = kayaknet.Kayaknet.newMobileNode(dataDir)
            node?.start(bootstrapAddr, 0)
            true
        } catch (e: Exception) {
            e.printStackTrace()
            false
        }
    }
    
    /**
     * Stop the KayakNet node
     */
    fun stopNode() {
        node?.stop()
        node = null
    }
    
    override fun onTerminate() {
        stopNode()
        super.onTerminate()
    }
}

