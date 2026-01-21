#!/usr/bin/env python3
"""
KayakNet Desktop GUI for Linux
Terminal-style GTK application matching the KayakNet aesthetic.
"""

import subprocess
import threading
import os
import sys
import signal
import urllib.request
import stat
import json
from pathlib import Path

import gi
gi.require_version('Gtk', '3.0')
from gi.repository import Gtk, GLib, Gio, Gdk, Pango

APP_ID = "net.kayaknet.desktop"
APP_NAME = "KayakNet"
APP_VERSION = "1.0.0"

BOOTSTRAP_SERVERS = [
    "203.161.33.237:8080",
    "144.172.94.195:8080"
]


def open_url(url):
    """Open URL in default browser"""
    try:
        subprocess.Popen(['xdg-open', url], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    except:
        try:
            subprocess.Popen(['firefox', url], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        except:
            try:
                subprocess.Popen(['chromium', url], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            except:
                pass


class KayakNetDaemon:
    """Manages the KayakNet daemon process"""
    
    def __init__(self):
        self.process = None
        self.binary_path = self._get_binary_path()
        self.data_dir = Path.home() / ".kayaknet"
        self.data_dir.mkdir(exist_ok=True)
        
    def _get_binary_path(self):
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
    
    def download_binary(self):
        if self.binary_path.exists():
            return True
        url = "https://github.com/KayakNet/downloads/raw/main/releases/linux/kayakd"
        try:
            self.data_dir.mkdir(exist_ok=True)
            urllib.request.urlretrieve(url, self.binary_path)
            self.binary_path.chmod(self.binary_path.stat().st_mode | stat.S_IEXEC)
            return True
        except Exception as e:
            print(f"Download failed: {e}")
            return False
    
    def start(self, bootstrap=None):
        if self.process and self.process.poll() is None:
            return True
        if not self.binary_path.exists():
            return False
        bootstrap = bootstrap or BOOTSTRAP_SERVERS[0]
        cmd = [str(self.binary_path), "-proxy", "-bootstrap", bootstrap]
        try:
            self.process = subprocess.Popen(
                cmd,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                cwd=str(self.data_dir)
            )
            return True
        except Exception as e:
            print(f"Failed to start: {e}")
            return False
    
    def stop(self):
        if self.process:
            self.process.terminate()
            try:
                self.process.wait(timeout=5)
            except:
                self.process.kill()
            self.process = None
    
    def is_running(self):
        return self.process is not None and self.process.poll() is None


class KayakNetWindow(Gtk.Window):
    """Main application window with terminal theme"""
    
    def __init__(self):
        super().__init__(title="KayakNet")
        self.set_default_size(480, 580)
        self.set_position(Gtk.WindowPosition.CENTER)
        self.set_resizable(True)
        
        self.daemon = KayakNetDaemon()
        self.buttons = []
        
        self.setup_ui()
        self.apply_css()
        self.connect("destroy", self.on_destroy)
        self.show_all()
        self.start_daemon()
    
    def setup_ui(self):
        # Main container
        main_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=0)
        self.add(main_box)
        
        # Terminal-style header
        header_box = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL)
        header_box.set_name("header")
        header_box.set_size_request(-1, 40)
        
        header_label = Gtk.Label()
        header_label.set_markup("<span font_family='monospace' foreground='#00ff00'> [KAYAKNET] // ANONYMOUS NETWORK </span>")
        header_label.set_halign(Gtk.Align.START)
        header_label.set_margin_start(15)
        header_box.pack_start(header_label, True, True, 0)
        
        main_box.pack_start(header_box, False, False, 0)
        
        # Content area
        content_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=0)
        content_box.set_name("content")
        content_box.set_margin_top(30)
        content_box.set_margin_bottom(30)
        content_box.set_margin_start(30)
        content_box.set_margin_end(30)
        
        # ASCII Logo
        logo_text = """
   ██╗  ██╗ █████╗ ██╗   ██╗ █████╗ ██╗  ██╗
   ██║ ██╔╝██╔══██╗╚██╗ ██╔╝██╔══██╗██║ ██╔╝
   █████╔╝ ███████║ ╚████╔╝ ███████║█████╔╝ 
   ██╔═██╗ ██╔══██║  ╚██╔╝  ██╔══██║██╔═██╗ 
   ██║  ██╗██║  ██║   ██║   ██║  ██║██║  ██╗
   ╚═╝  ╚═╝╚═╝  ╚═╝   ╚═╝   ╚═╝  ╚═╝╚═╝  ╚═╝"""
        
        logo_label = Gtk.Label()
        logo_label.set_markup(f"<span font_family='monospace' size='7000' foreground='#00ff00'>{logo_text}</span>")
        logo_label.set_halign(Gtk.Align.CENTER)
        content_box.pack_start(logo_label, False, False, 0)
        
        # Subtitle
        subtitle = Gtk.Label()
        subtitle.set_markup("<span font_family='monospace' foreground='#00aa00'>Anonymous // Encrypted // Unstoppable</span>")
        subtitle.set_margin_top(10)
        content_box.pack_start(subtitle, False, False, 0)
        
        # Status section
        status_frame = Gtk.Frame()
        status_frame.set_name("status-frame")
        status_frame.set_margin_top(25)
        status_frame.set_shadow_type(Gtk.ShadowType.NONE)
        
        status_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8)
        status_box.set_margin_top(15)
        status_box.set_margin_bottom(15)
        status_box.set_margin_start(15)
        status_box.set_margin_end(15)
        
        # Status line
        status_line = Gtk.Box(spacing=10)
        status_line.set_halign(Gtk.Align.CENTER)
        
        self.spinner = Gtk.Spinner()
        self.spinner.set_size_request(16, 16)
        status_line.pack_start(self.spinner, False, False, 0)
        
        self.status_label = Gtk.Label()
        self.status_label.set_markup("<span font_family='monospace' foreground='#00aa00'>[INIT] Starting...</span>")
        status_line.pack_start(self.status_label, False, False, 0)
        
        status_box.pack_start(status_line, False, False, 0)
        
        # Node info
        self.node_label = Gtk.Label()
        self.node_label.set_markup("<span font_family='monospace' size='9000' foreground='#00aa00'>Node: connecting...</span>")
        self.node_label.set_halign(Gtk.Align.CENTER)
        status_box.pack_start(self.node_label, False, False, 0)
        
        self.info_label = Gtk.Label()
        self.info_label.set_markup("<span font_family='monospace' size='9000' foreground='#00aa00'>Proxy: 127.0.0.1:8888 | API: 127.0.0.1:8080</span>")
        self.info_label.set_halign(Gtk.Align.CENTER)
        status_box.pack_start(self.info_label, False, False, 0)
        
        status_frame.add(status_box)
        content_box.pack_start(status_frame, False, False, 0)
        
        # Buttons section
        buttons_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=10)
        buttons_box.set_margin_top(25)
        buttons_box.set_halign(Gtk.Align.CENTER)
        
        # Main button
        self.open_btn = Gtk.Button()
        self.open_btn.set_name("main-btn")
        self.open_btn.set_size_request(280, 45)
        open_label = Gtk.Label()
        open_label.set_markup("<span font_family='monospace' weight='bold'>> OPEN KAYAKNET</span>")
        self.open_btn.add(open_label)
        self.open_btn.connect("clicked", self.on_open_clicked)
        self.open_btn.set_sensitive(False)
        buttons_box.pack_start(self.open_btn, False, False, 0)
        self.buttons.append(self.open_btn)
        
        # Navigation buttons row
        nav_box = Gtk.Box(spacing=10)
        nav_box.set_halign(Gtk.Align.CENTER)
        nav_box.set_margin_top(5)
        
        for label, path in [("[CHAT]", "/chat"), ("[MARKET]", "/market"), ("[DOMAINS]", "/domains")]:
            btn = Gtk.Button()
            btn.set_name("nav-btn")
            btn.set_size_request(85, 35)
            btn_label = Gtk.Label()
            btn_label.set_markup(f"<span font_family='monospace' size='9000'>{label}</span>")
            btn.add(btn_label)
            btn.connect("clicked", self.on_nav_clicked, path)
            btn.set_sensitive(False)
            nav_box.pack_start(btn, False, False, 0)
            self.buttons.append(btn)
        
        buttons_box.pack_start(nav_box, False, False, 0)
        content_box.pack_start(buttons_box, False, False, 0)
        
        # Footer
        footer = Gtk.Label()
        footer.set_markup("<span font_family='monospace' size='8000' foreground='#004400'>v" + APP_VERSION + " | github.com/KayakNet</span>")
        footer.set_margin_top(30)
        content_box.pack_start(footer, False, False, 0)
        
        main_box.pack_start(content_box, True, True, 0)
    
    def apply_css(self):
        css = b"""
        window {
            background-color: #000000;
        }
        #header {
            background-color: #0a0a0a;
            border-bottom-width: 1px;
            border-bottom-style: solid;
            border-bottom-color: #00ff00;
        }
        #content {
            background-color: #000000;
        }
        #status-frame {
            background-color: #0a0a0a;
            border-width: 1px;
            border-style: solid;
            border-color: #003300;
            border-radius: 5px;
        }
        #main-btn {
            background-color: #002200;
            background-image: none;
            border-width: 1px;
            border-style: solid;
            border-color: #00ff00;
            border-radius: 3px;
            color: #00ff00;
            padding: 10px 20px;
        }
        #main-btn:hover {
            background-color: #003300;
        }
        #main-btn:disabled {
            background-color: #111111;
            border-color: #333333;
            color: #444444;
        }
        #nav-btn {
            background-color: #0a0a0a;
            background-image: none;
            border-width: 1px;
            border-style: solid;
            border-color: #00aa00;
            border-radius: 3px;
            color: #00aa00;
            padding: 5px 10px;
        }
        #nav-btn:hover {
            background-color: #002200;
            border-color: #00ff00;
            color: #00ff00;
        }
        #nav-btn:disabled {
            background-color: #0a0a0a;
            border-color: #333333;
            color: #333333;
        }
        label {
            color: #00ff00;
        }
        """
        provider = Gtk.CssProvider()
        provider.load_from_data(css)
        Gtk.StyleContext.add_provider_for_screen(
            Gdk.Screen.get_default(),
            provider,
            Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION
        )
    
    def on_open_clicked(self, button):
        print("Opening http://127.0.0.1:8080")
        open_url("http://127.0.0.1:8080")
    
    def on_nav_clicked(self, button, path):
        url = f"http://127.0.0.1:8080{path}"
        print(f"Opening {url}")
        open_url(url)
    
    def on_destroy(self, widget):
        if self.daemon:
            self.daemon.stop()
        Gtk.main_quit()
    
    def start_daemon(self):
        self.spinner.start()
        
        def _start():
            # Check if already running
            try:
                response = urllib.request.urlopen("http://127.0.0.1:8080/api/stats", timeout=2)
                data = json.loads(response.read())
                GLib.idle_add(self.on_connected, data)
                return
            except:
                pass
            
            # Download if needed
            if not self.daemon.binary_path.exists():
                GLib.idle_add(self.set_status, "[DOWNLOAD] Fetching binary...")
                if not self.daemon.download_binary():
                    GLib.idle_add(self.set_status, "[ERROR] Download failed")
                    GLib.idle_add(self.spinner.stop)
                    return
            
            # Start daemon
            GLib.idle_add(self.set_status, "[INIT] Starting daemon...")
            if not self.daemon.start():
                GLib.idle_add(self.set_status, "[ERROR] Failed to start")
                GLib.idle_add(self.spinner.stop)
                return
            
            # Wait for connection
            import time
            for i in range(30):
                time.sleep(1)
                try:
                    response = urllib.request.urlopen("http://127.0.0.1:8080/api/stats", timeout=2)
                    data = json.loads(response.read())
                    GLib.idle_add(self.on_connected, data)
                    return
                except:
                    GLib.idle_add(self.set_status, f"[CONN] Connecting... {i+1}s")
            
            GLib.idle_add(self.set_status, "[ERROR] Connection timeout")
            GLib.idle_add(self.spinner.stop)
        
        thread = threading.Thread(target=_start, daemon=True)
        thread.start()
    
    def set_status(self, text):
        self.status_label.set_markup(f"<span font_family='monospace' foreground='#00aa00'>{text}</span>")
    
    def on_connected(self, stats):
        self.spinner.stop()
        self.status_label.set_markup("<span font_family='monospace' foreground='#00ff00' weight='bold'>[ONLINE] Connected to network</span>")
        
        # Enable all buttons
        for btn in self.buttons:
            btn.set_sensitive(True)
        
        # Update node info
        node_id = stats.get("node_id", "unknown")
        if node_id and len(node_id) > 20:
            node_id = node_id[:10] + "..." + node_id[-10:]
        self.node_label.set_markup(f"<span font_family='monospace' size='9000' foreground='#00aa00'>Node: {node_id}</span>")
        
        version = stats.get("version", "?")
        peers = stats.get("peers", 0)
        listings = stats.get("listings", 0)
        self.info_label.set_markup(f"<span font_family='monospace' size='9000' foreground='#00aa00'>{version} | {peers} peers | {listings} listings</span>")


def main():
    signal.signal(signal.SIGINT, signal.SIG_DFL)
    win = KayakNetWindow()
    Gtk.main()


if __name__ == "__main__":
    main()
