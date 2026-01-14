package net.kayak.kayaknet.fragments

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.EditText
import android.widget.Toast
import androidx.appcompat.app.AlertDialog
import androidx.fragment.app.Fragment
import net.kayak.kayaknet.KayakNetApp
import net.kayak.kayaknet.databinding.FragmentSettingsBinding

class SettingsFragment : Fragment() {
    
    private var _binding: FragmentSettingsBinding? = null
    private val binding get() = _binding!!
    
    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?
    ): View {
        _binding = FragmentSettingsBinding.inflate(inflater, container, false)
        return binding.root
    }
    
    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        
        updateUI()
        
        binding.copyNodeId.setOnClickListener {
            copyNodeId()
        }
        
        binding.changeNickname.setOnClickListener {
            showChangeNicknameDialog()
        }
        
        binding.changeBootstrap.setOnClickListener {
            showChangeBootstrapDialog()
        }
        
        binding.reconnect.setOnClickListener {
            reconnect()
        }
        
        binding.viewEscrows.setOnClickListener {
            showMyEscrows()
        }
        
        binding.aboutApp.setOnClickListener {
            showAbout()
        }
    }
    
    private fun updateUI() {
        val node = KayakNetApp.node
        
        binding.nodeIdValue.text = node?.nodeID?.take(32)?.plus("...") ?: "Not connected"
        binding.versionValue.text = "v${node?.version ?: "0.1.14"}"
        binding.peerCountValue.text = node?.peerCount?.toString() ?: "0"
        binding.anonymousValue.text = if (node?.isAnonymous == true) "Yes" else "No"
    }
    
    private fun copyNodeId() {
        val nodeId = KayakNetApp.node?.nodeID ?: return
        
        val clipboard = requireContext().getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
        clipboard.setPrimaryClip(ClipData.newPlainText("Node ID", nodeId))
        
        Toast.makeText(context, "Node ID copied", Toast.LENGTH_SHORT).show()
    }
    
    private fun showChangeNicknameDialog() {
        val input = EditText(requireContext()).apply {
            hint = "Enter nickname"
        }
        
        AlertDialog.Builder(requireContext())
            .setTitle("Change Nickname")
            .setView(input)
            .setPositiveButton("Save") { _, _ ->
                val nick = input.text.toString().trim()
                if (nick.isNotEmpty()) {
                    KayakNetApp.node?.setNickname(nick)
                    Toast.makeText(context, "Nickname updated", Toast.LENGTH_SHORT).show()
                }
            }
            .setNegativeButton("Cancel", null)
            .show()
    }
    
    private fun showChangeBootstrapDialog() {
        val input = EditText(requireContext()).apply {
            hint = "Bootstrap address (ip:port)"
            setText("203.161.33.237:4242")
        }
        
        AlertDialog.Builder(requireContext())
            .setTitle("Bootstrap Node")
            .setMessage("Change will take effect on restart")
            .setView(input)
            .setPositiveButton("Save") { _, _ ->
                val addr = input.text.toString().trim()
                // Save to preferences
                requireContext().getSharedPreferences("kayaknet", Context.MODE_PRIVATE)
                    .edit()
                    .putString("bootstrap_addr", addr)
                    .apply()
                
                Toast.makeText(context, "Bootstrap saved", Toast.LENGTH_SHORT).show()
            }
            .setNegativeButton("Cancel", null)
            .show()
    }
    
    private fun reconnect() {
        Toast.makeText(context, "Reconnecting...", Toast.LENGTH_SHORT).show()
        
        // Restart node
        KayakNetApp.instance.stopNode()
        
        val prefs = requireContext().getSharedPreferences("kayaknet", Context.MODE_PRIVATE)
        val bootstrap = prefs.getString("bootstrap_addr", "203.161.33.237:4242") ?: "203.161.33.237:4242"
        
        if (KayakNetApp.instance.startNode(bootstrap)) {
            Toast.makeText(context, "Reconnected!", Toast.LENGTH_SHORT).show()
            updateUI()
        } else {
            Toast.makeText(context, "Failed to reconnect", Toast.LENGTH_SHORT).show()
        }
    }
    
    private fun showMyEscrows() {
        val escrowsJson = KayakNetApp.node?.myEscrows ?: "[]"
        
        AlertDialog.Builder(requireContext())
            .setTitle("My Escrows")
            .setMessage(if (escrowsJson == "[]") "No active escrows" else escrowsJson)
            .setPositiveButton("OK", null)
            .show()
    }
    
    private fun showAbout() {
        AlertDialog.Builder(requireContext())
            .setTitle("KayakNet")
            .setMessage("""
                KayakNet Anonymous P2P Network
                Version: ${KayakNetApp.node?.version ?: "0.1.14"}
                
                Features:
                - Onion routing (3-hop encryption)
                - Traffic analysis resistance
                - Anonymous chat
                - P2P marketplace with escrow
                - .kyk domain system
                
                Privacy first. No central servers.
            """.trimIndent())
            .setPositiveButton("OK", null)
            .show()
    }
    
    override fun onDestroyView() {
        super.onDestroyView()
        _binding = null
    }
}

