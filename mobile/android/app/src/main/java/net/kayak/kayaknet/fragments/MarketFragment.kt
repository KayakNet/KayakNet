package net.kayak.kayaknet.fragments

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.Toast
import androidx.appcompat.app.AlertDialog
import androidx.fragment.app.Fragment
import androidx.recyclerview.widget.LinearLayoutManager
import com.google.gson.Gson
import com.google.gson.reflect.TypeToken
import net.kayak.kayaknet.KayakNetApp
import net.kayak.kayaknet.MainActivity
import net.kayak.kayaknet.adapters.ListingAdapter
import net.kayak.kayaknet.databinding.FragmentMarketBinding
import kotlinx.coroutines.*

class MarketFragment : Fragment(), MainActivity.RefreshableFragment {
    
    private var _binding: FragmentMarketBinding? = null
    private val binding get() = _binding!!
    private val gson = Gson()
    private val scope = CoroutineScope(Dispatchers.Main + Job())
    private lateinit var adapter: ListingAdapter
    
    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?
    ): View {
        _binding = FragmentMarketBinding.inflate(inflater, container, false)
        return binding.root
    }
    
    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        
        adapter = ListingAdapter { listing ->
            showListingDetails(listing)
        }
        
        binding.listingsRecycler.apply {
            layoutManager = LinearLayoutManager(context)
            adapter = this@MarketFragment.adapter
        }
        
        binding.searchButton.setOnClickListener {
            searchListings()
        }
        
        binding.fabCreate.setOnClickListener {
            showCreateListingDialog()
        }
        
        binding.swipeRefresh.setOnRefreshListener {
            refresh()
        }
        
        refresh()
    }
    
    private fun searchListings() {
        val query = binding.searchInput.text.toString()
        scope.launch(Dispatchers.IO) {
            val node = KayakNetApp.node ?: return@launch
            
            val json = if (query.isBlank()) {
                node.getListings("", 50)
            } else {
                node.searchListings(query)
            }
            
            val listings: List<Map<String, Any>> = try {
                val type = object : TypeToken<List<Map<String, Any>>>() {}.type
                gson.fromJson(json, type) ?: emptyList()
            } catch (e: Exception) {
                emptyList()
            }
            
            withContext(Dispatchers.Main) {
                adapter.updateListings(listings)
            }
        }
    }
    
    override fun refresh() {
        scope.launch(Dispatchers.IO) {
            val node = KayakNetApp.node ?: return@launch
            
            val json = node.getListings("", 50)
            val listings: List<Map<String, Any>> = try {
                val type = object : TypeToken<List<Map<String, Any>>>() {}.type
                gson.fromJson(json, type) ?: emptyList()
            } catch (e: Exception) {
                emptyList()
            }
            
            withContext(Dispatchers.Main) {
                adapter.updateListings(listings)
                binding.swipeRefresh.isRefreshing = false
            }
        }
    }
    
    private fun showListingDetails(listing: Map<String, Any>) {
        val title = listing["title"] as? String ?: "Unknown"
        val description = listing["description"] as? String ?: "No description"
        val price = listing["price"] as? Double ?: 0.0
        val currency = listing["currency"] as? String ?: "XMR"
        val seller = (listing["seller_id"] as? String)?.take(16) ?: "Unknown"
        
        AlertDialog.Builder(requireContext())
            .setTitle(title)
            .setMessage("""
                $description
                
                Price: $price $currency
                Seller: $seller...
            """.trimIndent())
            .setPositiveButton("Buy") { _, _ ->
                initiatePurchase(listing)
            }
            .setNegativeButton("Close", null)
            .show()
    }
    
    private fun initiatePurchase(listing: Map<String, Any>) {
        val listingId = listing["id"] as? String ?: return
        val sellerId = listing["seller_id"] as? String ?: return
        val price = listing["price"] as? Double ?: return
        val currency = listing["currency"] as? String ?: "XMR"
        
        AlertDialog.Builder(requireContext())
            .setTitle("Confirm Purchase")
            .setMessage("Create escrow for $price $currency?")
            .setPositiveButton("Yes") { _, _ ->
                scope.launch(Dispatchers.IO) {
                    try {
                        val escrowId = KayakNetApp.node?.createEscrow(listingId, sellerId, price, currency)
                        withContext(Dispatchers.Main) {
                            Toast.makeText(context, "Escrow created: ${escrowId?.take(16)}...", Toast.LENGTH_LONG).show()
                        }
                    } catch (e: Exception) {
                        withContext(Dispatchers.Main) {
                            Toast.makeText(context, "Failed: ${e.message}", Toast.LENGTH_SHORT).show()
                        }
                    }
                }
            }
            .setNegativeButton("Cancel", null)
            .show()
    }
    
    private fun showCreateListingDialog() {
        val layout = LinearLayout(requireContext()).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(50, 30, 50, 30)
        }
        
        val titleInput = EditText(requireContext()).apply { hint = "Title" }
        val descInput = EditText(requireContext()).apply { hint = "Description"; minLines = 3 }
        val priceInput = EditText(requireContext()).apply { hint = "Price"; inputType = android.text.InputType.TYPE_CLASS_NUMBER or android.text.InputType.TYPE_NUMBER_FLAG_DECIMAL }
        
        layout.addView(titleInput)
        layout.addView(descInput)
        layout.addView(priceInput)
        
        AlertDialog.Builder(requireContext())
            .setTitle("Create Listing")
            .setView(layout)
            .setPositiveButton("Create") { _, _ ->
                val title = titleInput.text.toString()
                val desc = descInput.text.toString()
                val price = priceInput.text.toString().toDoubleOrNull() ?: 0.0
                
                if (title.isNotBlank() && price > 0) {
                    createListing(title, desc, price)
                }
            }
            .setNegativeButton("Cancel", null)
            .show()
    }
    
    private fun createListing(title: String, description: String, price: Double) {
        scope.launch(Dispatchers.IO) {
            try {
                val listingId = KayakNetApp.node?.createListing(title, description, "general", price, "XMR")
                withContext(Dispatchers.Main) {
                    Toast.makeText(context, "Listing created!", Toast.LENGTH_SHORT).show()
                    refresh()
                }
            } catch (e: Exception) {
                withContext(Dispatchers.Main) {
                    Toast.makeText(context, "Failed: ${e.message}", Toast.LENGTH_SHORT).show()
                }
            }
        }
    }
    
    override fun onDestroyView() {
        super.onDestroyView()
        scope.cancel()
        _binding = null
    }
}

