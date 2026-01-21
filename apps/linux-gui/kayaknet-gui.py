#!/usr/bin/env python3
"""
KayakNet Desktop GUI for Linux
A native GTK4 + WebKit application that runs KayakNet with a beautiful interface.
"""

import gi
gi.require_version('Gtk', '4.0')
gi.require_version('Adw', '1')

# Try WebKit 6.0 first, fall back to WebKit2 4.1
try:
    gi.require_version('WebKit', '6.0')
    from gi.repository import WebKit
    WEBKIT_VERSION = 6
except ValueError:
    gi.require_version('WebKit2', '4.1')
    from gi.repository import WebKit2 as WebKit
    WEBKIT_VERSION = 4

from gi.repository import Gtk, Adw, GLib, Gio, Gdk
import subprocess
import threading
import os
import sys
import signal
import urllib.request
import stat
import json
from pathlib import Path

APP_ID = "net.kayaknet.desktop"
APP_NAME = "KayakNet"
APP_VERSION = "1.0.0"

BOOTSTRAP_SERVERS = [
    "203.161.33.237:8080",
    "144.172.94.195:8080"
]

class KayakNetDaemon:
    """Manages the KayakNet daemon process"""
    
    def __init__(self):
        self.process = None
        self.binary_path = self._get_binary_path()
        self.data_dir = Path.home() / ".kayaknet"
        self.data_dir.mkdir(exist_ok=True)
        
    def _get_binary_path(self):
        """Get path to kayaknet binary"""
        # Check common locations
        locations = [
            Path.home() / ".kayaknet" / "kayakd",
            Path("/usr/local/bin/kayakd"),
            Path("/usr/bin/kayakd"),
            Path("./kayakd"),
        ]
        
        for loc in locations:
            if loc.exists() and os.access(loc, os.X_OK):
                return loc
        
        return Path.home() / ".kayaknet" / "kayakd"
    
    def download_binary(self, progress_callback=None):
        """Download the KayakNet binary if not present"""
        if self.binary_path.exists():
            return True
            
        url = "https://github.com/KayakNet/downloads/raw/main/releases/linux/kayakd"
        
        try:
            if progress_callback:
                progress_callback("Downloading KayakNet binary...")
            
            self.data_dir.mkdir(exist_ok=True)
            urllib.request.urlretrieve(url, self.binary_path)
            
            # Make executable
            self.binary_path.chmod(self.binary_path.stat().st_mode | stat.S_IEXEC)
            
            if progress_callback:
                progress_callback("Download complete!")
            return True
        except Exception as e:
            if progress_callback:
                progress_callback(f"Download failed: {e}")
            return False
    
    def start(self, bootstrap=None):
        """Start the KayakNet daemon"""
        if self.process and self.process.poll() is None:
            return True  # Already running
        
        if not self.binary_path.exists():
            return False
        
        bootstrap = bootstrap or BOOTSTRAP_SERVERS[0]
        
        cmd = [
            str(self.binary_path),
            "-proxy",
            "-bootstrap", bootstrap
        ]
        
        try:
            self.process = subprocess.Popen(
                cmd,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                cwd=str(self.data_dir)
            )
            return True
        except Exception as e:
            print(f"Failed to start daemon: {e}")
            return False
    
    def stop(self):
        """Stop the KayakNet daemon"""
        if self.process:
            self.process.terminate()
            try:
                self.process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.process.kill()
            self.process = None
    
    def is_running(self):
        """Check if daemon is running"""
        return self.process is not None and self.process.poll() is None
    
    def get_output(self):
        """Get daemon output (non-blocking)"""
        if not self.process:
            return None
        try:
            return self.process.stdout.readline().decode('utf-8', errors='ignore')
        except:
            return None


class KayakNetWindow(Adw.ApplicationWindow):
    """Main application window"""
    
    def __init__(self, app):
        super().__init__(application=app, title=APP_NAME)
        self.set_default_size(1200, 800)
        
        self.daemon = KayakNetDaemon()
        self.setup_ui()
        self.start_daemon()
        
    def setup_ui(self):
        """Setup the UI components"""
        # Main box
        main_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL)
        self.set_content(main_box)
        
        # Header bar
        header = Adw.HeaderBar()
        
        # Title
        title = Adw.WindowTitle(title="KayakNet", subtitle="Anonymous Network")
        header.set_title_widget(title)
        
        # Status indicator
        self.status_box = Gtk.Box(spacing=6)
        self.status_icon = Gtk.Image.new_from_icon_name("network-offline-symbolic")
        self.status_label = Gtk.Label(label="Connecting...")
        self.status_box.append(self.status_icon)
        self.status_box.append(self.status_label)
        header.pack_start(self.status_box)
        
        # Menu button
        menu_button = Gtk.MenuButton()
        menu_button.set_icon_name("open-menu-symbolic")
        
        # Create menu
        menu = Gio.Menu()
        menu.append("Refresh", "app.refresh")
        menu.append("Copy Node ID", "app.copy_node_id")
        menu.append("Settings", "app.settings")
        menu.append("About", "app.about")
        menu.append("Quit", "app.quit")
        menu_button.set_menu_model(menu)
        header.pack_end(menu_button)
        
        # Navigation buttons
        nav_box = Gtk.Box(spacing=0)
        nav_box.add_css_class("linked")
        
        self.nav_buttons = {}
        pages = [
            ("Home", "go-home-symbolic", "/"),
            ("Chat", "user-available-symbolic", "/chat"),
            ("Market", "emblem-shopping-symbolic", "/market"),
            ("Domains", "network-server-symbolic", "/domains"),
        ]
        
        for name, icon, path in pages:
            btn = Gtk.ToggleButton()
            btn.set_icon_name(icon)
            btn.set_tooltip_text(name)
            btn.connect("toggled", self.on_nav_clicked, path)
            nav_box.append(btn)
            self.nav_buttons[path] = btn
        
        self.nav_buttons["/"].set_active(True)
        header.pack_start(nav_box)
        
        main_box.append(header)
        
        # Stack for content
        self.stack = Gtk.Stack()
        self.stack.set_transition_type(Gtk.StackTransitionType.CROSSFADE)
        
        # Loading page
        loading_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=20)
        loading_box.set_valign(Gtk.Align.CENTER)
        loading_box.set_halign(Gtk.Align.CENTER)
        
        spinner = Gtk.Spinner()
        spinner.set_size_request(64, 64)
        spinner.start()
        loading_box.append(spinner)
        
        self.loading_label = Gtk.Label(label="Starting KayakNet...")
        self.loading_label.add_css_class("title-2")
        loading_box.append(self.loading_label)
        
        self.stack.add_named(loading_box, "loading")
        
        # WebView page
        self.webview = WebKit.WebView()
        self.webview.set_vexpand(True)
        self.webview.set_hexpand(True)
        
        # Configure WebView settings
        settings = self.webview.get_settings()
        settings.set_enable_javascript(True)
        settings.set_enable_developer_extras(True)
        settings.set_javascript_can_access_clipboard(True)
        
        # Dark mode
        self.webview.set_background_color(Gdk.RGBA(red=0, green=0, blue=0, alpha=1))
        
        self.webview.connect("load-changed", self.on_load_changed)
        self.webview.connect("load-failed", self.on_load_failed)
        
        self.stack.add_named(self.webview, "webview")
        
        # Error page
        error_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=20)
        error_box.set_valign(Gtk.Align.CENTER)
        error_box.set_halign(Gtk.Align.CENTER)
        
        error_icon = Gtk.Image.new_from_icon_name("dialog-error-symbolic")
        error_icon.set_pixel_size(64)
        error_box.append(error_icon)
        
        self.error_label = Gtk.Label(label="Failed to connect")
        self.error_label.add_css_class("title-2")
        error_box.append(self.error_label)
        
        retry_btn = Gtk.Button(label="Retry")
        retry_btn.add_css_class("suggested-action")
        retry_btn.connect("clicked", lambda _: self.start_daemon())
        error_box.append(retry_btn)
        
        self.stack.add_named(error_box, "error")
        
        self.stack.set_visible_child_name("loading")
        main_box.append(self.stack)
        
        # Apply custom CSS
        self.apply_css()
    
    def apply_css(self):
        """Apply custom CSS styling"""
        css = b"""
        window {
            background-color: #0a0a0a;
        }
        headerbar {
            background-color: #111111;
            border-bottom: 1px solid #00ff0033;
        }
        .linked button {
            background-color: transparent;
            color: #00ff00;
            border: none;
        }
        .linked button:checked {
            background-color: #00ff0022;
        }
        .linked button:hover {
            background-color: #00ff0011;
        }
        label {
            color: #00ff00;
        }
        .title-2 {
            font-size: 18px;
            font-weight: bold;
        }
        """
        
        provider = Gtk.CssProvider()
        provider.load_from_data(css)
        Gtk.StyleContext.add_provider_for_display(
            Gdk.Display.get_default(),
            provider,
            Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION
        )
    
    def on_nav_clicked(self, button, path):
        """Handle navigation button clicks"""
        if button.get_active():
            # Deactivate other buttons
            for p, btn in self.nav_buttons.items():
                if p != path:
                    btn.set_active(False)
            
            # Navigate
            self.webview.load_uri(f"http://127.0.0.1:8080{path}")
    
    def on_load_changed(self, webview, event):
        """Handle WebView load events"""
        if event == WebKit.LoadEvent.FINISHED:
            self.stack.set_visible_child_name("webview")
            self.update_status(True)
    
    def on_load_failed(self, webview, event, uri, error):
        """Handle WebView load failures"""
        self.error_label.set_text(f"Failed to load: {error.message}")
        self.stack.set_visible_child_name("error")
        self.update_status(False)
        return True
    
    def update_status(self, connected):
        """Update the connection status indicator"""
        if connected:
            self.status_icon.set_from_icon_name("network-transmit-receive-symbolic")
            self.status_label.set_text("Connected")
            self.status_icon.add_css_class("success")
        else:
            self.status_icon.set_from_icon_name("network-offline-symbolic")
            self.status_label.set_text("Disconnected")
            self.status_icon.remove_css_class("success")
    
    def start_daemon(self):
        """Start the KayakNet daemon"""
        self.stack.set_visible_child_name("loading")
        self.loading_label.set_text("Starting KayakNet...")
        
        def _start():
            # Check if binary exists
            if not self.daemon.binary_path.exists():
                GLib.idle_add(lambda: self.loading_label.set_text("Downloading KayakNet..."))
                if not self.daemon.download_binary():
                    GLib.idle_add(lambda: self.show_error("Failed to download binary"))
                    return
            
            # Start daemon
            GLib.idle_add(lambda: self.loading_label.set_text("Starting daemon..."))
            if not self.daemon.start():
                GLib.idle_add(lambda: self.show_error("Failed to start daemon"))
                return
            
            # Wait for daemon to be ready
            import time
            for i in range(30):  # Wait up to 30 seconds
                time.sleep(1)
                try:
                    import urllib.request
                    urllib.request.urlopen("http://127.0.0.1:8080/api/stats", timeout=2)
                    GLib.idle_add(self.load_webview)
                    return
                except:
                    GLib.idle_add(lambda: self.loading_label.set_text(f"Waiting for daemon... ({i+1}s)"))
            
            GLib.idle_add(lambda: self.show_error("Daemon failed to start"))
        
        thread = threading.Thread(target=_start, daemon=True)
        thread.start()
    
    def load_webview(self):
        """Load the WebView with KayakNet UI"""
        self.webview.load_uri("http://127.0.0.1:8080/")
    
    def show_error(self, message):
        """Show error page"""
        self.error_label.set_text(message)
        self.stack.set_visible_child_name("error")
        self.update_status(False)


class KayakNetApp(Adw.Application):
    """Main application class"""
    
    def __init__(self):
        super().__init__(
            application_id=APP_ID,
            flags=Gio.ApplicationFlags.FLAGS_NONE
        )
        self.window = None
        
    def do_startup(self):
        Adw.Application.do_startup(self)
        
        # Create actions
        self.create_action("refresh", self.on_refresh)
        self.create_action("copy_node_id", self.on_copy_node_id)
        self.create_action("settings", self.on_settings)
        self.create_action("about", self.on_about)
        self.create_action("quit", self.on_quit)
    
    def do_activate(self):
        if not self.window:
            self.window = KayakNetWindow(self)
        self.window.present()
    
    def create_action(self, name, callback):
        action = Gio.SimpleAction.new(name, None)
        action.connect("activate", callback)
        self.add_action(action)
    
    def on_refresh(self, action, param):
        if self.window:
            self.window.webview.reload()
    
    def on_copy_node_id(self, action, param):
        try:
            import urllib.request
            response = urllib.request.urlopen("http://127.0.0.1:8080/api/stats")
            data = json.loads(response.read())
            node_id = data.get("node_id", "")
            if node_id:
                clipboard = Gdk.Display.get_default().get_clipboard()
                clipboard.set(node_id)
        except:
            pass
    
    def on_settings(self, action, param):
        # TODO: Implement settings dialog
        pass
    
    def on_about(self, action, param):
        about = Adw.AboutWindow(
            application_name="KayakNet",
            application_icon="network-transmit-receive-symbolic",
            version=APP_VERSION,
            developer_name="KayakNet Team",
            website="https://kayaknet.io",
            issue_url="https://github.com/KayakNet/KayakNet/issues",
            license_type=Gtk.License.MIT_X11,
            comments="Anonymous • Encrypted • Unstoppable\n\nThe decentralized network for private communication and commerce.",
            developers=["KayakNet Team"],
            transient_for=self.window
        )
        about.present()
    
    def on_quit(self, action, param):
        if self.window and self.window.daemon:
            self.window.daemon.stop()
        self.quit()


def main():
    # Handle Ctrl+C
    signal.signal(signal.SIGINT, signal.SIG_DFL)
    
    app = KayakNetApp()
    return app.run(sys.argv)


if __name__ == "__main__":
    sys.exit(main())

