package net.kayak.kayaknet.fragments

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.fragment.app.Fragment
import com.google.gson.Gson
import net.kayak.kayaknet.KayakNetApp
import net.kayak.kayaknet.MainActivity
import net.kayak.kayaknet.R
import net.kayak.kayaknet.databinding.FragmentHomeBinding
import kotlinx.coroutines.*

class HomeFragment : Fragment(), MainActivity.RefreshableFragment {
    
    private var _binding: FragmentHomeBinding? = null
    private val binding get() = _binding!!
    private val gson = Gson()
    private val scope = CoroutineScope(Dispatchers.Main + Job())
    
    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?
    ): View {
        _binding = FragmentHomeBinding.inflate(inflater, container, false)
        return binding.root
    }
    
    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        
        binding.swipeRefresh.setOnRefreshListener {
            refresh()
        }
        
        refresh()
    }
    
    override fun refresh() {
        scope.launch {
            updateStats()
            binding.swipeRefresh.isRefreshing = false
        }
    }
    
    private suspend fun updateStats() = withContext(Dispatchers.IO) {
        val node = KayakNetApp.node ?: return@withContext
        
        val statsJson = node.stats
        val stats = try {
            gson.fromJson(statsJson, Map::class.java)
        } catch (e: Exception) {
            emptyMap<String, Any>()
        }
        
        withContext(Dispatchers.Main) {
            binding.apply {
                val isConnected = (stats["peers"] as? Double)?.toInt() ?: 0 > 0
                val isAnonymous = stats["anonymous"] as? Boolean ?: false
                
                statusIndicator.setImageResource(
                    if (isConnected) R.drawable.ic_status_online else R.drawable.ic_status_offline
                )
                statusText.text = if (isConnected) "CONNECTED" else "OFFLINE"
                
                anonymityIndicator.setImageResource(
                    if (isAnonymous) R.drawable.ic_shield_on else R.drawable.ic_shield_off
                )
                anonymityText.text = if (isAnonymous) "ANONYMOUS" else "BUILDING..."
                
                peerCount.text = ((stats["peers"] as? Double)?.toInt() ?: 0).toString()
                relayCount.text = ((stats["relay_count"] as? Double)?.toInt() ?: 0).toString()
                listingCount.text = ((stats["listings"] as? Double)?.toInt() ?: 0).toString()
                domainCount.text = ((stats["domains"] as? Double)?.toInt() ?: 0).toString()
                
                nodeId.text = (stats["node_id"] as? String) ?: "---"
                version.text = "v${(stats["version"] as? String) ?: "0.1.14"}"
            }
        }
    }
    
    override fun onDestroyView() {
        super.onDestroyView()
        scope.cancel()
        _binding = null
    }
}

