#!/usr/bin/env python3
"""
KayakNet Desktop GUI for Linux
A native GTK application that runs KayakNet with a beautiful interface.
Supports both GTK4 and GTK3 for maximum compatibility.
"""

import subprocess
import threading
import os
import sys
import signal
import urllib.request
import stat
import json
import webbrowser
from pathlib import Path

# Try GTK4 first, fall back to GTK3
try:
    import gi
    gi.require_version('Gtk', '4.0')
    gi.require_version('Adw', '1')
    from gi.repository import Gtk, Adw, GLib, Gio, Gdk
    GTK_VERSION = 4
    print("Using GTK4 with Adwaita")
except (ValueError, ImportError):
    import gi
    gi.require_version('Gtk', '3.0')
    from gi.repository import Gtk, GLib, Gio, Gdk
    GTK_VERSION = 3
    Adw = None
    print("Using GTK3 (fallback)")

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
                GLib.idle_add(progress_callback, "Downloading KayakNet binary...")
            
            self.data_dir.mkdir(exist_ok=True)
            urllib.request.urlretrieve(url, self.binary_path)
            self.binary_path.chmod(self.binary_path.stat().st_mode | stat.S_IEXEC)
            
            if progress_callback:
                GLib.idle_add(progress_callback, "Download complete!")
            return True
        except Exception as e:
            if progress_callback:
                GLib.idle_add(progress_callback, f"Download failed: {e}")
            return False
    
    def start(self, bootstrap=None):
        """Start the KayakNet daemon"""
        if self.process and self.process.poll() is None:
            return True
        
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


# ===================== GTK4 VERSION =====================
if GTK_VERSION == 4:
    
    class KayakNetWindow(Adw.ApplicationWindow):
        """Main application window (GTK4)"""
        
        def __init__(self, app):
            super().__init__(application=app, title=APP_NAME)
            self.set_default_size(500, 650)
            
            self.daemon = KayakNetDaemon()
            self.link_buttons = []  # Store button references
            self.setup_ui()
            self.start_daemon()
            
        def setup_ui(self):
            """Setup the UI components"""
            main_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL)
            self.set_content(main_box)
            
            # Header bar
            header = Adw.HeaderBar()
            title = Adw.WindowTitle(title="KayakNet", subtitle="Anonymous Network")
            header.set_title_widget(title)
            main_box.append(header)
            
            # Scrollable content
            scroll = Gtk.ScrolledWindow()
            scroll.set_policy(Gtk.PolicyType.NEVER, Gtk.PolicyType.AUTOMATIC)
            scroll.set_vexpand(True)
            
            # Content
            content = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=20)
            content.set_margin_top(40)
            content.set_margin_bottom(40)
            content.set_margin_start(40)
            content.set_margin_end(40)
            content.set_valign(Gtk.Align.CENTER)
            content.set_halign(Gtk.Align.CENTER)
            
            # Logo/Title
            logo_label = Gtk.Label()
            logo_label.set_markup("<span size='72000'>🛶</span>")
            content.append(logo_label)
            
            title_label = Gtk.Label(label="KayakNet")
            title_label.add_css_class("title-1")
            content.append(title_label)
            
            subtitle_label = Gtk.Label(label="Anonymous • Encrypted • Unstoppable")
            subtitle_label.add_css_class("dim-label")
            content.append(subtitle_label)
            
            # Status
            self.status_box = Gtk.Box(spacing=10)
            self.status_box.set_halign(Gtk.Align.CENTER)
            self.status_box.set_margin_top(20)
            
            self.spinner = Gtk.Spinner()
            self.spinner.set_size_request(24, 24)
            self.status_box.append(self.spinner)
            
            self.status_label = Gtk.Label(label="Starting...")
            self.status_box.append(self.status_label)
            content.append(self.status_box)
            
            # Main button
            button_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=10)
            button_box.set_margin_top(30)
            button_box.set_halign(Gtk.Align.CENTER)
            
            self.open_btn = Gtk.Button(label="🌐  Open KayakNet")
            self.open_btn.add_css_class("suggested-action")
            self.open_btn.add_css_class("pill")
            self.open_btn.set_size_request(220, 50)
            self.open_btn.connect("clicked", self.on_open_clicked)
            self.open_btn.set_sensitive(False)
            button_box.append(self.open_btn)
            
            # Quick links
            links_box = Gtk.Box(spacing=10)
            links_box.set_halign(Gtk.Align.CENTER)
            links_box.set_margin_top(15)
            
            for name, icon, path in [("💬 Chat", "Chat", "/chat"), ("🛒 Market", "Market", "/market"), ("🌐 Domains", "Domains", "/domains")]:
                btn = Gtk.Button(label=name)
                btn.set_size_request(100, 40)
                btn.connect("clicked", self.on_link_clicked, path)
                btn.set_sensitive(False)
                links_box.append(btn)
                self.link_buttons.append(btn)  # Store reference!
                
            button_box.append(links_box)
            content.append(button_box)
            
            # Info section
            info_frame = Gtk.Frame()
            info_frame.set_margin_top(30)
            info_frame.add_css_class("view")
            
            info_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8)
            info_box.set_margin_top(15)
            info_box.set_margin_bottom(15)
            info_box.set_margin_start(20)
            info_box.set_margin_end(20)
            
            self.node_label = Gtk.Label(label="Node: Connecting...")
            self.node_label.add_css_class("monospace")
            self.node_label.set_halign(Gtk.Align.START)
            info_box.append(self.node_label)
            
            self.proxy_label = Gtk.Label(label="Proxy: 127.0.0.1:8888")
            self.proxy_label.add_css_class("dim-label")
            self.proxy_label.set_halign(Gtk.Align.START)
            info_box.append(self.proxy_label)
            
            self.api_label = Gtk.Label(label="API: http://127.0.0.1:8080")
            self.api_label.add_css_class("dim-label")
            self.api_label.set_halign(Gtk.Align.START)
            info_box.append(self.api_label)
            
            info_frame.set_child(info_box)
            content.append(info_frame)
            
            scroll.set_child(content)
            main_box.append(scroll)
            self.apply_css()
            
        def apply_css(self):
            """Apply custom CSS"""
            css = b"""
            window { background-color: #0d1117; }
            .title-1 { color: #00ff88; font-size: 36px; font-weight: bold; }
            .dim-label { color: #8b949e; }
            .monospace { font-family: monospace; font-size: 12px; color: #c9d1d9; }
            button.suggested-action { 
                background: linear-gradient(135deg, #00ff88, #00cc6a); 
                color: #000; 
                font-weight: bold;
                font-size: 16px;
            }
            button.pill { border-radius: 25px; padding: 12px 24px; }
            button { background: #21262d; color: #c9d1d9; border: 1px solid #30363d; }
            button:hover { background: #30363d; }
            frame { background: #161b22; border: 1px solid #30363d; border-radius: 10px; }
            """
            provider = Gtk.CssProvider()
            provider.load_from_data(css)
            Gtk.StyleContext.add_provider_for_display(
                Gdk.Display.get_default(),
                provider,
                Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION
            )
        
        def on_open_clicked(self, button):
            print("Opening browser to http://127.0.0.1:8080")
            webbrowser.open("http://127.0.0.1:8080")
        
        def on_link_clicked(self, button, path):
            url = f"http://127.0.0.1:8080{path}"
            print(f"Opening browser to {url}")
            webbrowser.open(url)
        
        def start_daemon(self):
            self.spinner.start()
            self.status_label.set_text("Starting KayakNet...")
            
            def _start():
                # Check if already running
                try:
                    response = urllib.request.urlopen("http://127.0.0.1:8080/api/stats", timeout=2)
                    data = json.loads(response.read())
                    GLib.idle_add(lambda: self.on_daemon_ready(data))
                    return
                except:
                    pass
                
                if not self.daemon.binary_path.exists():
                    GLib.idle_add(lambda: self.status_label.set_text("Downloading binary..."))
                    if not self.daemon.download_binary():
                        GLib.idle_add(lambda: self.status_label.set_text("❌ Download failed"))
                        GLib.idle_add(lambda: self.spinner.stop())
                        return
                
                GLib.idle_add(lambda: self.status_label.set_text("Starting daemon..."))
                if not self.daemon.start():
                    GLib.idle_add(lambda: self.status_label.set_text("❌ Failed to start"))
                    GLib.idle_add(lambda: self.spinner.stop())
                    return
                
                import time
                for i in range(30):
                    time.sleep(1)
                    try:
                        response = urllib.request.urlopen("http://127.0.0.1:8080/api/stats", timeout=2)
                        data = json.loads(response.read())
                        GLib.idle_add(lambda d=data: self.on_daemon_ready(d))
                        return
                    except:
                        GLib.idle_add(lambda i=i: self.status_label.set_text(f"Connecting... ({i+1}s)"))
                
                GLib.idle_add(lambda: self.status_label.set_text("❌ Connection timeout"))
                GLib.idle_add(lambda: self.spinner.stop())
            
            thread = threading.Thread(target=_start, daemon=True)
            thread.start()
        
        def on_daemon_ready(self, stats):
            self.spinner.stop()
            self.status_label.set_markup("<span foreground='#00ff88' weight='bold'>● Connected</span>")
            
            # Enable all buttons
            self.open_btn.set_sensitive(True)
            for btn in self.link_buttons:
                btn.set_sensitive(True)
            
            # Update info
            node_id = stats.get("node_id", "unknown")
            if node_id and len(node_id) > 24:
                node_id = node_id[:12] + "..." + node_id[-12:]
            self.node_label.set_text(f"Node: {node_id}")
            
            version = stats.get("version", "unknown")
            peers = stats.get("peers", 0)
            listings = stats.get("listings", 0)
            self.api_label.set_text(f"API: http://127.0.0.1:8080 | v{version} | {peers} peers | {listings} listings")


    class KayakNetApp(Adw.Application):
        """Main application (GTK4)"""
        
        def __init__(self):
            super().__init__(application_id=APP_ID, flags=Gio.ApplicationFlags.FLAGS_NONE)
            self.window = None
            
        def do_activate(self):
            if not self.window:
                self.window = KayakNetWindow(self)
            self.window.present()
        
        def do_shutdown(self):
            if self.window and self.window.daemon:
                self.window.daemon.stop()
            Adw.Application.do_shutdown(self)


# ===================== GTK3 VERSION =====================
else:
    
    class KayakNetWindow(Gtk.ApplicationWindow):
        """Main application window (GTK3)"""
        
        def __init__(self, app):
            super().__init__(application=app, title=APP_NAME)
            self.set_default_size(500, 650)
            self.set_position(Gtk.WindowPosition.CENTER)
            
            self.daemon = KayakNetDaemon()
            self.link_buttons = []
            self.setup_ui()
            self.start_daemon()
            
        def setup_ui(self):
            """Setup the UI components"""
            main_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=0)
            self.add(main_box)
            
            # Header bar
            header = Gtk.HeaderBar()
            header.set_show_close_button(True)
            header.set_title("KayakNet")
            header.set_subtitle("Anonymous Network")
            self.set_titlebar(header)
            
            # Scrollable content
            scroll = Gtk.ScrolledWindow()
            scroll.set_policy(Gtk.PolicyType.NEVER, Gtk.PolicyType.AUTOMATIC)
            
            # Content
            content = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=20)
            content.set_margin_top(40)
            content.set_margin_bottom(40)
            content.set_margin_start(40)
            content.set_margin_end(40)
            content.set_valign(Gtk.Align.CENTER)
            content.set_halign(Gtk.Align.CENTER)
            
            # Logo
            logo_label = Gtk.Label()
            logo_label.set_markup("<span size='72000'>🛶</span>")
            content.pack_start(logo_label, False, False, 0)
            
            title_label = Gtk.Label()
            title_label.set_markup("<span size='xx-large' weight='bold' foreground='#00ff88'>KayakNet</span>")
            content.pack_start(title_label, False, False, 0)
            
            subtitle_label = Gtk.Label(label="Anonymous • Encrypted • Unstoppable")
            subtitle_label.get_style_context().add_class("dim-label")
            content.pack_start(subtitle_label, False, False, 0)
            
            # Status
            status_box = Gtk.Box(spacing=10)
            status_box.set_halign(Gtk.Align.CENTER)
            status_box.set_margin_top(20)
            
            self.spinner = Gtk.Spinner()
            status_box.pack_start(self.spinner, False, False, 0)
            
            self.status_label = Gtk.Label(label="Starting...")
            status_box.pack_start(self.status_label, False, False, 0)
            content.pack_start(status_box, False, False, 0)
            
            # Open button
            self.open_btn = Gtk.Button(label="🌐  Open KayakNet")
            self.open_btn.get_style_context().add_class("suggested-action")
            self.open_btn.set_size_request(220, 50)
            self.open_btn.connect("clicked", self.on_open_clicked)
            self.open_btn.set_sensitive(False)
            self.open_btn.set_margin_top(30)
            content.pack_start(self.open_btn, False, False, 0)
            
            # Quick links
            links_box = Gtk.Box(spacing=10)
            links_box.set_halign(Gtk.Align.CENTER)
            links_box.set_margin_top(15)
            
            for name, path in [("💬 Chat", "/chat"), ("🛒 Market", "/market"), ("🌐 Domains", "/domains")]:
                btn = Gtk.Button(label=name)
                btn.set_size_request(100, 40)
                btn.connect("clicked", self.on_link_clicked, path)
                btn.set_sensitive(False)
                links_box.pack_start(btn, False, False, 0)
                self.link_buttons.append(btn)
                
            content.pack_start(links_box, False, False, 0)
            
            # Info
            info_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8)
            info_box.set_margin_top(40)
            
            self.node_label = Gtk.Label(label="Node: Connecting...")
            self.node_label.get_style_context().add_class("dim-label")
            info_box.pack_start(self.node_label, False, False, 0)
            
            self.proxy_label = Gtk.Label(label="Proxy: 127.0.0.1:8888")
            self.proxy_label.get_style_context().add_class("dim-label")
            info_box.pack_start(self.proxy_label, False, False, 0)
            
            self.api_label = Gtk.Label(label="API: http://127.0.0.1:8080")
            self.api_label.get_style_context().add_class("dim-label")
            info_box.pack_start(self.api_label, False, False, 0)
            
            content.pack_start(info_box, False, False, 0)
            
            scroll.add(content)
            main_box.pack_start(scroll, True, True, 0)
            self.apply_css()
            self.show_all()
            
        def apply_css(self):
            """Apply custom CSS"""
            css = b"""
            window { background-color: #0d1117; }
            .dim-label { color: #8b949e; }
            label { color: #c9d1d9; }
            button { background: #21262d; color: #c9d1d9; border: 1px solid #30363d; }
            button:hover { background: #30363d; }
            """
            provider = Gtk.CssProvider()
            provider.load_from_data(css)
            Gtk.StyleContext.add_provider_for_screen(
                Gdk.Screen.get_default(),
                provider,
                Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION
            )
        
        def on_open_clicked(self, button):
            print("Opening browser to http://127.0.0.1:8080")
            webbrowser.open("http://127.0.0.1:8080")
        
        def on_link_clicked(self, button, path):
            url = f"http://127.0.0.1:8080{path}"
            print(f"Opening browser to {url}")
            webbrowser.open(url)
        
        def start_daemon(self):
            self.spinner.start()
            self.status_label.set_text("Starting KayakNet...")
            
            def _start():
                # Check if already running
                try:
                    response = urllib.request.urlopen("http://127.0.0.1:8080/api/stats", timeout=2)
                    data = json.loads(response.read())
                    GLib.idle_add(lambda: self.on_daemon_ready(data))
                    return
                except:
                    pass
                
                if not self.daemon.binary_path.exists():
                    GLib.idle_add(lambda: self.status_label.set_text("Downloading binary..."))
                    if not self.daemon.download_binary():
                        GLib.idle_add(lambda: self.status_label.set_text("❌ Download failed"))
                        GLib.idle_add(lambda: self.spinner.stop())
                        return
                
                GLib.idle_add(lambda: self.status_label.set_text("Starting daemon..."))
                if not self.daemon.start():
                    GLib.idle_add(lambda: self.status_label.set_text("❌ Failed to start"))
                    GLib.idle_add(lambda: self.spinner.stop())
                    return
                
                import time
                for i in range(30):
                    time.sleep(1)
                    try:
                        response = urllib.request.urlopen("http://127.0.0.1:8080/api/stats", timeout=2)
                        data = json.loads(response.read())
                        GLib.idle_add(lambda d=data: self.on_daemon_ready(d))
                        return
                    except:
                        GLib.idle_add(lambda i=i: self.status_label.set_text(f"Connecting... ({i+1}s)"))
                
                GLib.idle_add(lambda: self.status_label.set_text("❌ Connection timeout"))
                GLib.idle_add(lambda: self.spinner.stop())
            
            thread = threading.Thread(target=_start, daemon=True)
            thread.start()
        
        def on_daemon_ready(self, stats):
            self.spinner.stop()
            self.status_label.set_markup("<span foreground='#00ff88' weight='bold'>● Connected</span>")
            
            # Enable all buttons
            self.open_btn.set_sensitive(True)
            for btn in self.link_buttons:
                btn.set_sensitive(True)
            
            node_id = stats.get("node_id", "unknown")
            if node_id and len(node_id) > 24:
                node_id = node_id[:12] + "..." + node_id[-12:]
            self.node_label.set_text(f"Node: {node_id}")
            
            version = stats.get("version", "unknown")
            peers = stats.get("peers", 0)
            listings = stats.get("listings", 0)
            self.api_label.set_text(f"API: http://127.0.0.1:8080 | v{version} | {peers} peers | {listings} listings")


    class KayakNetApp(Gtk.Application):
        """Main application (GTK3)"""
        
        def __init__(self):
            super().__init__(application_id=APP_ID, flags=Gio.ApplicationFlags.FLAGS_NONE)
            self.window = None
            
        def do_activate(self):
            if not self.window:
                self.window = KayakNetWindow(self)
            self.window.present()
        
        def do_shutdown(self):
            if self.window and self.window.daemon:
                self.window.daemon.stop()
            Gtk.Application.do_shutdown(self)


def main():
    signal.signal(signal.SIGINT, signal.SIG_DFL)
    app = KayakNetApp()
    return app.run(sys.argv)


if __name__ == "__main__":
    sys.exit(main())
