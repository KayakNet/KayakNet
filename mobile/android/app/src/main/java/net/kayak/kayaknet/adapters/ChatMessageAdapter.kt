package net.kayak.kayaknet.adapters

import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import androidx.recyclerview.widget.RecyclerView
import net.kayak.kayaknet.R
import java.text.SimpleDateFormat
import java.util.*

class ChatMessageAdapter : RecyclerView.Adapter<ChatMessageAdapter.MessageViewHolder>() {
    
    private val messages = mutableListOf<Map<String, Any>>()
    private val dateFormat = SimpleDateFormat("HH:mm", Locale.getDefault())
    
    fun updateMessages(newMessages: List<Map<String, Any>>) {
        messages.clear()
        messages.addAll(newMessages)
        notifyDataSetChanged()
    }
    
    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): MessageViewHolder {
        val view = LayoutInflater.from(parent.context)
            .inflate(R.layout.item_chat_message, parent, false)
        return MessageViewHolder(view)
    }
    
    override fun onBindViewHolder(holder: MessageViewHolder, position: Int) {
        val message = messages[position]
        
        val senderName = message["sender_name"] as? String ?: "Anonymous"
        val senderId = (message["sender_id"] as? String)?.take(8) ?: "?"
        val content = message["content"] as? String ?: ""
        val timestamp = message["timestamp"] as? String ?: ""
        
        holder.sender.text = "$senderName [$senderId]"
        holder.content.text = content
        holder.time.text = formatTimestamp(timestamp)
    }
    
    override fun getItemCount(): Int = messages.size
    
    private fun formatTimestamp(timestamp: String): String {
        return try {
            // Parse ISO 8601 format
            val date = SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss", Locale.getDefault()).parse(timestamp)
            dateFormat.format(date ?: Date())
        } catch (e: Exception) {
            ""
        }
    }
    
    class MessageViewHolder(view: View) : RecyclerView.ViewHolder(view) {
        val sender: TextView = view.findViewById(R.id.messageSender)
        val content: TextView = view.findViewById(R.id.messageContent)
        val time: TextView = view.findViewById(R.id.messageTime)
    }
}

