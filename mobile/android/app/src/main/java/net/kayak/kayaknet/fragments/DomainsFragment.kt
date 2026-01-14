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
import net.kayak.kayaknet.adapters.DomainAdapter
import net.kayak.kayaknet.databinding.FragmentDomainsBinding
import kotlinx.coroutines.*

class DomainsFragment : Fragment(), MainActivity.RefreshableFragment {
    
    private var _binding: FragmentDomainsBinding? = null
    private val binding get() = _binding!!
    private val gson = Gson()
    private val scope = CoroutineScope(Dispatchers.Main + Job())
    private lateinit var adapter: DomainAdapter
    
    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?
    ): View {
        _binding = FragmentDomainsBinding.inflate(inflater, container, false)
        return binding.root
    }
    
    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        
        adapter = DomainAdapter { domain ->
            showDomainDetails(domain)
        }
        
        binding.domainsRecycler.apply {
            layoutManager = LinearLayoutManager(context)
            adapter = this@DomainsFragment.adapter
        }
        
        binding.searchButton.setOnClickListener {
            searchDomains()
        }
        
        binding.fabRegister.setOnClickListener {
            showRegisterDialog()
        }
        
        binding.myDomainsButton.setOnClickListener {
            showMyDomains()
        }
        
        binding.swipeRefresh.setOnRefreshListener {
            refresh()
        }
        
        refresh()
    }
    
    private fun searchDomains() {
        val query = binding.searchInput.text.toString()
        scope.launch(Dispatchers.IO) {
            val node = KayakNetApp.node ?: return@launch
            val json = node.searchDomains(query)
            
            val domains: List<Map<String, Any>> = try {
                val type = object : TypeToken<List<Map<String, Any>>>() {}.type
                gson.fromJson(json, type) ?: emptyList()
            } catch (e: Exception) {
                emptyList()
            }
            
            withContext(Dispatchers.Main) {
                adapter.updateDomains(domains)
            }
        }
    }
    
    override fun refresh() {
        scope.launch(Dispatchers.IO) {
            val node = KayakNetApp.node ?: return@launch
            val json = node.searchDomains("")
            
            val domains: List<Map<String, Any>> = try {
                val type = object : TypeToken<List<Map<String, Any>>>() {}.type
                gson.fromJson(json, type) ?: emptyList()
            } catch (e: Exception) {
                emptyList()
            }
            
            withContext(Dispatchers.Main) {
                adapter.updateDomains(domains)
                binding.swipeRefresh.isRefreshing = false
            }
        }
    }
    
    private fun showMyDomains() {
        scope.launch(Dispatchers.IO) {
            val node = KayakNetApp.node ?: return@launch
            val json = node.myDomains
            
            val domains: List<Map<String, Any>> = try {
                val type = object : TypeToken<List<Map<String, Any>>>() {}.type
                gson.fromJson(json, type) ?: emptyList()
            } catch (e: Exception) {
                emptyList()
            }
            
            withContext(Dispatchers.Main) {
                if (domains.isEmpty()) {
                    Toast.makeText(context, "You don't own any domains", Toast.LENGTH_SHORT).show()
                } else {
                    adapter.updateDomains(domains)
                }
            }
        }
    }
    
    private fun showDomainDetails(domain: Map<String, Any>) {
        val name = domain["name"] as? String ?: "Unknown"
        val fullName = domain["full_name"] as? String ?: "$name.kyk"
        val description = domain["description"] as? String ?: "No description"
        val owner = (domain["node_id"] as? String)?.take(16) ?: "Unknown"
        val expiresAt = domain["expires_at"] as? String ?: "Unknown"
        
        AlertDialog.Builder(requireContext())
            .setTitle(fullName.uppercase())
            .setMessage("""
                $description
                
                Owner: $owner...
                Expires: $expiresAt
            """.trimIndent())
            .setPositiveButton("OK", null)
            .show()
    }
    
    private fun showRegisterDialog() {
        val layout = LinearLayout(requireContext()).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(50, 30, 50, 30)
        }
        
        val nameInput = EditText(requireContext()).apply { 
            hint = "Domain name (e.g., mysite)"
        }
        val descInput = EditText(requireContext()).apply { 
            hint = "Description (optional)"
            minLines = 2
        }
        
        layout.addView(nameInput)
        layout.addView(descInput)
        
        AlertDialog.Builder(requireContext())
            .setTitle("Register .kyk Domain")
            .setView(layout)
            .setPositiveButton("Register") { _, _ ->
                val name = nameInput.text.toString().lowercase().trim()
                val desc = descInput.text.toString()
                
                if (name.length >= 3) {
                    registerDomain(name, desc)
                } else {
                    Toast.makeText(context, "Name must be at least 3 characters", Toast.LENGTH_SHORT).show()
                }
            }
            .setNegativeButton("Cancel", null)
            .show()
    }
    
    private fun registerDomain(name: String, description: String) {
        scope.launch(Dispatchers.IO) {
            try {
                KayakNetApp.node?.registerDomain(name, description, "")
                withContext(Dispatchers.Main) {
                    Toast.makeText(context, "$name.kyk registered!", Toast.LENGTH_SHORT).show()
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

