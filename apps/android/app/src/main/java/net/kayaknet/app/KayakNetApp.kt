package net.kayaknet.app

import android.app.Application
import android.app.NotificationChannel
import android.app.NotificationManager
import android.os.Build
import net.kayaknet.app.network.KayakNetClient

class KayakNetApp : Application() {
    
    companion object {
        lateinit var instance: KayakNetApp
            private set
        
        const val CHANNEL_SERVICE = "kayaknet_service"
    }
    
    lateinit var client: KayakNetClient
        private set
    
    override fun onCreate() {
        super.onCreate()
        instance = this
        
        createNotificationChannel()
        
        client = KayakNetClient(this)
        
        // Auto-connect if enabled
        if (client.isAutoConnectEnabled()) {
            client.connect()
        }
    }
    
    private fun createNotificationChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val channel = NotificationChannel(
                CHANNEL_SERVICE,
                "KayakNet Service",
                NotificationManager.IMPORTANCE_LOW
            ).apply {
                description = "KayakNet network connection"
            }
            
            val manager = getSystemService(NotificationManager::class.java)
            manager?.createNotificationChannel(channel)
        }
    }
}
