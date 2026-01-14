package net.kayaknet.app

import android.app.Application
import net.kayaknet.app.network.KayakNetClient

class KayakNetApp : Application() {
    
    companion object {
        lateinit var instance: KayakNetApp
            private set
    }
    
    lateinit var client: KayakNetClient
        private set
    
    override fun onCreate() {
        super.onCreate()
        instance = this
        client = KayakNetClient(this)
        
        // Auto-connect if enabled
        if (client.isAutoConnectEnabled()) {
            client.connect()
        }
    }
}
