package net.kayak.kayaknet.fragments

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.Toast
import androidx.fragment.app.Fragment
import androidx.recyclerview.widget.LinearLayoutManager
import com.google.gson.Gson
import com.google.gson.reflect.TypeToken
import net.kayak.kayaknet.KayakNetApp
import net.kayak.kayaknet.MainActivity
import net.kayak.kayaknet.adapters.ChatMessageAdapter
import net.kayak.kayaknet.databinding.FragmentChatBinding
import kotlinx.coroutines.*

class ChatFragment : Fragment(), MainActivity.RefreshableFragment {
    
    private var _binding: FragmentChatBinding? = null
    private val binding get() = _binding!!
    private val gson = Gson()
    private val scope = CoroutineScope(Dispatchers.Main + Job())
    private lateinit var adapter: ChatMessageAdapter
    private var currentRoom = "general"
    
    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?
    ): View {
        _binding = FragmentChatBinding.inflate(inflater, container, false)
        return binding.root
    }
    
    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        
        adapter = ChatMessageAdapter()
        binding.messagesRecycler.apply {
            layoutManager = LinearLayoutManager(context).apply {
                stackFromEnd = true
            }
            adapter = this@ChatFragment.adapter
        }
        
        binding.roomName.text = "#$currentRoom"
        
        binding.sendButton.setOnClickListener {
            sendMessage()
        }
        
        binding.roomSelector.setOnClickListener {
            showRoomSelector()
        }
        
        binding.swipeRefresh.setOnRefreshListener {
            refresh()
        }
        
        // Join default room
        KayakNetApp.node?.joinRoom(currentRoom)
        
        refresh()
        startMessagePolling()
    }
    
    private fun sendMessage() {
        val message = binding.messageInput.text.toString().trim()
        if (message.isEmpty()) return
        
        scope.launch(Dispatchers.IO) {
            try {
                KayakNetApp.node?.sendChatMessage(currentRoom, message)
                withContext(Dispatchers.Main) {
                    binding.messageInput.text?.clear()
                    refresh()
                }
            } catch (e: Exception) {
                withContext(Dispatchers.Main) {
                    Toast.makeText(context, "Failed to send: ${e.message}", Toast.LENGTH_SHORT).show()
                }
            }
        }
    }
    
    private fun showRoomSelector() {
        val rooms = listOf("general", "market", "support", "random", "trading")
        
        androidx.appcompat.app.AlertDialog.Builder(requireContext())
            .setTitle("Select Room")
            .setItems(rooms.toTypedArray()) { _, which ->
                currentRoom = rooms[which]
                binding.roomName.text = "#$currentRoom"
                KayakNetApp.node?.joinRoom(currentRoom)
                refresh()
            }
            .show()
    }
    
    override fun refresh() {
        scope.launch(Dispatchers.IO) {
            val node = KayakNetApp.node ?: return@launch
            
            val historyJson = node.getChatHistory(currentRoom, 50)
            val messages: List<Map<String, Any>> = try {
                val type = object : TypeToken<List<Map<String, Any>>>() {}.type
                gson.fromJson(historyJson, type) ?: emptyList()
            } catch (e: Exception) {
                emptyList()
            }
            
            withContext(Dispatchers.Main) {
                adapter.updateMessages(messages)
                binding.messagesRecycler.scrollToPosition(adapter.itemCount - 1)
                binding.swipeRefresh.isRefreshing = false
            }
        }
    }
    
    private fun startMessagePolling() {
        scope.launch {
            while (isActive) {
                delay(2000)
                refresh()
            }
        }
    }
    
    override fun onDestroyView() {
        super.onDestroyView()
        scope.cancel()
        _binding = null
    }
}

