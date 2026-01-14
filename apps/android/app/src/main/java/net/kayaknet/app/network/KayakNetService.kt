package net.kayaknet.app.network

import android.app.Notification
import android.app.PendingIntent
import android.app.Service
import android.content.Intent
import android.os.IBinder
import androidx.core.app.NotificationCompat
import net.kayaknet.app.KayakNetApp
import net.kayaknet.app.MainActivity
import net.kayaknet.app.R

class KayakNetService : Service() {
    
    override fun onBind(intent: Intent?): IBinder? = null
    
    override fun onCreate() {
        super.onCreate()
        startForeground(NOTIFICATION_ID, createNotification())
    }
    
    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_CONNECT -> {
                KayakNetApp.instance.client.connect()
            }
            ACTION_DISCONNECT -> {
                KayakNetApp.instance.client.disconnect()
                stopSelf()
            }
        }
        return START_STICKY
    }
    
    private fun createNotification(): Notification {
        val pendingIntent = PendingIntent.getActivity(
            this,
            0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE
        )
        
        val disconnectIntent = PendingIntent.getService(
            this,
            1,
            Intent(this, KayakNetService::class.java).apply {
                action = ACTION_DISCONNECT
            },
            PendingIntent.FLAG_IMMUTABLE
        )
        
        return NotificationCompat.Builder(this, KayakNetApp.CHANNEL_SERVICE)
            .setContentTitle("KayakNet")
            .setContentText("Connected to anonymous network")
            .setSmallIcon(android.R.drawable.ic_menu_share)
            .setContentIntent(pendingIntent)
            .addAction(
                android.R.drawable.ic_menu_close_clear_cancel,
                "Disconnect",
                disconnectIntent
            )
            .setOngoing(true)
            .build()
    }
    
    companion object {
        const val NOTIFICATION_ID = 1
        const val ACTION_CONNECT = "net.kayaknet.app.CONNECT"
        const val ACTION_DISCONNECT = "net.kayaknet.app.DISCONNECT"
    }
}

