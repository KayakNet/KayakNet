package net.kayak.kayaknet.adapters

import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.ImageView
import android.widget.TextView
import androidx.recyclerview.widget.RecyclerView
import coil.load
import net.kayak.kayaknet.R

class ListingAdapter(
    private val onItemClick: (Map<String, Any>) -> Unit
) : RecyclerView.Adapter<ListingAdapter.ListingViewHolder>() {
    
    private val listings = mutableListOf<Map<String, Any>>()
    
    fun updateListings(newListings: List<Map<String, Any>>) {
        listings.clear()
        listings.addAll(newListings)
        notifyDataSetChanged()
    }
    
    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): ListingViewHolder {
        val view = LayoutInflater.from(parent.context)
            .inflate(R.layout.item_listing, parent, false)
        return ListingViewHolder(view)
    }
    
    override fun onBindViewHolder(holder: ListingViewHolder, position: Int) {
        val listing = listings[position]
        
        val title = listing["title"] as? String ?: "Unknown"
        val category = listing["category"] as? String ?: "general"
        val price = listing["price"] as? Double ?: 0.0
        val currency = listing["currency"] as? String ?: "XMR"
        val sellerName = listing["seller_name"] as? String ?: "Anonymous"
        val imageUrl = listing["image"] as? String
        
        holder.title.text = title
        holder.category.text = category.uppercase()
        holder.price.text = "$price $currency"
        holder.seller.text = "by $sellerName"
        
        if (!imageUrl.isNullOrBlank()) {
            holder.image.load(imageUrl) {
                placeholder(R.drawable.ic_placeholder)
                error(R.drawable.ic_placeholder)
            }
        } else {
            holder.image.setImageResource(R.drawable.ic_placeholder)
        }
        
        holder.itemView.setOnClickListener {
            onItemClick(listing)
        }
    }
    
    override fun getItemCount(): Int = listings.size
    
    class ListingViewHolder(view: View) : RecyclerView.ViewHolder(view) {
        val image: ImageView = view.findViewById(R.id.listingImage)
        val title: TextView = view.findViewById(R.id.listingTitle)
        val category: TextView = view.findViewById(R.id.listingCategory)
        val price: TextView = view.findViewById(R.id.listingPrice)
        val seller: TextView = view.findViewById(R.id.listingSeller)
    }
}

