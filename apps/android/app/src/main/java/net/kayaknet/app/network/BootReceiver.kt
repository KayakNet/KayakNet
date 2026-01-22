package net.kayaknet.app.network

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.os.Build

class BootReceiver : BroadcastReceiver() {
    
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action == Intent.ACTION_BOOT_COMPLETED) {
            // Check if auto-connect is enabled
            val prefs = context.getSharedPreferences("kayaknet", Context.MODE_PRIVATE)
            val autoConnect = prefs.getBoolean("auto_connect", false)
            
            if (autoConnect) {
                val serviceIntent = Intent(context, KayakNetService::class.java).apply {
                    action = KayakNetService.ACTION_CONNECT
                }
                
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                    context.startForegroundService(serviceIntent)
                } else {
                    context.startService(serviceIntent)
                }
            }
        }
    }
}




