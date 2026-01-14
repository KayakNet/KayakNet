package net.kayak.kayaknet.adapters

import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import androidx.recyclerview.widget.RecyclerView
import net.kayak.kayaknet.R

class DomainAdapter(
    private val onItemClick: (Map<String, Any>) -> Unit
) : RecyclerView.Adapter<DomainAdapter.DomainViewHolder>() {
    
    private val domains = mutableListOf<Map<String, Any>>()
    
    fun updateDomains(newDomains: List<Map<String, Any>>) {
        domains.clear()
        domains.addAll(newDomains)
        notifyDataSetChanged()
    }
    
    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): DomainViewHolder {
        val view = LayoutInflater.from(parent.context)
            .inflate(R.layout.item_domain, parent, false)
        return DomainViewHolder(view)
    }
    
    override fun onBindViewHolder(holder: DomainViewHolder, position: Int) {
        val domain = domains[position]
        
        val name = domain["name"] as? String ?: "unknown"
        val fullName = domain["full_name"] as? String ?: "$name.kyk"
        val description = domain["description"] as? String ?: ""
        val serviceType = domain["service_type"] as? String ?: ""
        val owner = (domain["node_id"] as? String)?.take(12) ?: "?"
        
        holder.name.text = fullName.uppercase()
        holder.description.text = if (description.isNotBlank()) description else "No description"
        holder.owner.text = "Owner: $owner..."
        holder.serviceType.text = if (serviceType.isNotBlank()) serviceType.uppercase() else ""
        holder.serviceType.visibility = if (serviceType.isNotBlank()) View.VISIBLE else View.GONE
        
        holder.itemView.setOnClickListener {
            onItemClick(domain)
        }
    }
    
    override fun getItemCount(): Int = domains.size
    
    class DomainViewHolder(view: View) : RecyclerView.ViewHolder(view) {
        val name: TextView = view.findViewById(R.id.domainName)
        val description: TextView = view.findViewById(R.id.domainDescription)
        val owner: TextView = view.findViewById(R.id.domainOwner)
        val serviceType: TextView = view.findViewById(R.id.domainServiceType)
    }
}

