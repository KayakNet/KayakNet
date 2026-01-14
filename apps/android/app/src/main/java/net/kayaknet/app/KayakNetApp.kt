package net.kayaknet.app

import android.app.Application
import android.app.NotificationChannel
import android.app.NotificationManager
import android.os.Build
import net.kayaknet.app.network.KayakNetClient

class KayakNetApp : Application() {
    
    lateinit var client: KayakNetClient
        private set
    
    override fun onCreate() {
        super.onCreate()
        instance = this
        
        // Initialize the KayakNet client
        client = KayakNetClient(this)
        
        // Create notification channels
        createNotificationChannels()
    }
    
    private fun createNotificationChannels() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val serviceChannel = NotificationChannel(
                CHANNEL_SERVICE,
                "KayakNet Service",
                NotificationManager.IMPORTANCE_LOW
            ).apply {
                description = "KayakNet background connection"
            }
            
            val messageChannel = NotificationChannel(
                CHANNEL_MESSAGES,
                "Messages",
                NotificationManager.IMPORTANCE_HIGH
            ).apply {
                description = "Chat messages and notifications"
            }
            
            val notificationManager = getSystemService(NotificationManager::class.java)
            notificationManager.createNotificationChannel(serviceChannel)
            notificationManager.createNotificationChannel(messageChannel)
        }
    }
    
    companion object {
        const val CHANNEL_SERVICE = "kayaknet_service"
        const val CHANNEL_MESSAGES = "kayaknet_messages"
        
        lateinit var instance: KayakNetApp
            private set
    }
}

