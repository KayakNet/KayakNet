package net.kayak.kayaknet.service

import android.app.Notification
import android.app.PendingIntent
import android.app.Service
import android.content.Intent
import android.os.IBinder
import androidx.core.app.NotificationCompat
import net.kayak.kayaknet.KayakNetApp
import net.kayak.kayaknet.MainActivity
import net.kayak.kayaknet.R
import kotlinx.coroutines.*

/**
 * Foreground service to keep KayakNet connection alive
 */
class KayakNetService : Service() {
    
    private val scope = CoroutineScope(Dispatchers.IO + Job())
    
    override fun onBind(intent: Intent?): IBinder? = null
    
    override fun onCreate() {
        super.onCreate()
        startForeground(NOTIFICATION_ID, createNotification())
    }
    
    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        // Start the node if not already running
        if (!KayakNetApp.isNodeRunning()) {
            scope.launch {
                val prefs = getSharedPreferences("kayaknet", MODE_PRIVATE)
                val bootstrap = prefs.getString("bootstrap_addr", "203.161.33.237:4242") ?: "203.161.33.237:4242"
                
                KayakNetApp.instance.startNode(bootstrap)
                
                // Update notification periodically
                startStatusUpdates()
            }
        }
        
        return START_STICKY
    }
    
    private fun createNotification(): Notification {
        val intent = Intent(this, MainActivity::class.java)
        val pendingIntent = PendingIntent.getActivity(
            this, 0, intent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
        
        val peerCount = KayakNetApp.node?.peerCount ?: 0
        val isAnonymous = KayakNetApp.node?.isAnonymous ?: false
        
        val statusText = when {
            peerCount == 0 -> "Connecting..."
            isAnonymous -> "Connected (Anonymous)"
            else -> "Connected ($peerCount peers)"
        }
        
        return NotificationCompat.Builder(this, KayakNetApp.CHANNEL_ID)
            .setContentTitle("KayakNet")
            .setContentText(statusText)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentIntent(pendingIntent)
            .setOngoing(true)
            .setSilent(true)
            .build()
    }
    
    private fun startStatusUpdates() {
        scope.launch {
            while (isActive) {
                delay(30_000) // Update every 30 seconds
                
                val notification = createNotification()
                val notificationManager = getSystemService(NOTIFICATION_SERVICE) as android.app.NotificationManager
                notificationManager.notify(NOTIFICATION_ID, notification)
            }
        }
    }
    
    override fun onDestroy() {
        super.onDestroy()
        scope.cancel()
        // Don't stop the node - let it persist
    }
    
    companion object {
        const val NOTIFICATION_ID = 1001
    }
}

