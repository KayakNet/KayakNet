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
from gi.repository import Gtk, GLib, Gdk

APP_VERSION = "1.0.0"
BOOTSTRAP = "203.161.33.237:8080"


LOG_FILE = "/tmp/kayaknet-gui.log"

def log(msg):
    """Log to file and stdout"""
    print(msg)
    with open(LOG_FILE, "a") as f:
        f.write(f"{msg}\n")

def open_browser(url):
    """Open URL in browser"""
    log(f"[CLICK] Opening: {url}")
    
    # Try browsers in order of preference
    browsers = [
        ['firefox', url],
        ['firefox', '--new-tab', url],
        ['google-chrome', url],
        ['chromium', url],
        ['chromium-browser', url],
        ['xdg-open', url],
    ]
    
    for cmd in browsers:
        try:
            log(f"[CLICK] Trying: {cmd[0]}")
            proc = subprocess.Popen(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            log(f"[CLICK] Launched {cmd[0]} (pid: {proc.pid})")
            return
        except FileNotFoundError:
            continue
        except Exception as e:
            log(f"[CLICK] {cmd[0]} failed: {e}")
            continue
    
    log("[ERROR] No browser found!")


class KayakNetDaemon:
    def __init__(self):
        self.process = None
        self.data_dir = Path.home() / ".kayaknet"
        self.data_dir.mkdir(exist_ok=True)
        self.binary_path = self.data_dir / "kayakd"
        
    def download(self):
        if self.binary_path.exists():
            return True
        url = "https://github.com/KayakNet/downloads/raw/main/releases/linux/kayakd"
        try:
            urllib.request.urlretrieve(url, self.binary_path)
            self.binary_path.chmod(self.binary_path.stat().st_mode | stat.S_IEXEC)
            return True
        except Exception as e:
            print(f"Download failed: {e}")
            return False
    
    def start(self):
        if self.process and self.process.poll() is None:
            return True
        if not self.binary_path.exists():
            return False
        try:
            self.process = subprocess.Popen(
                [str(self.binary_path), "-proxy", "-bootstrap", BOOTSTRAP],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                cwd=str(self.data_dir)
            )
            return True
        except Exception as e:
            print(f"Start failed: {e}")
            return False
    
    def stop(self):
        if self.process:
            self.process.terminate()
            self.process = None


class KayakNetWindow(Gtk.Window):
    def __init__(self):
        super().__init__(title="KayakNet")
        self.set_default_size(450, 500)
        self.set_position(Gtk.WindowPosition.CENTER)
        
        self.daemon = KayakNetDaemon()
        self.all_buttons = []
        
        self.apply_css()
        self.build_ui()
        self.connect("destroy", self.on_quit)
        self.show_all()
        
        # Start daemon in background
        threading.Thread(target=self.connect_daemon, daemon=True).start()
    
    def apply_css(self):
        css = """
        window { background-color: #000000; }
        .header { background-color: #0a0a0a; padding: 10px; }
        .header-text { color: #00ff00; font-family: monospace; font-size: 14px; }
        .logo { color: #00ff00; font-family: monospace; }
        .subtitle { color: #00aa00; font-family: monospace; font-size: 12px; }
        .status-box { background-color: #0a0a0a; padding: 15px; border-radius: 5px; }
        .status-text { color: #00aa00; font-family: monospace; font-size: 11px; }
        .status-online { color: #00ff00; font-family: monospace; font-weight: bold; }
        .main-button {
            background-color: #002200;
            color: #00ff00;
            font-family: monospace;
            font-weight: bold;
            font-size: 14px;
            border: 1px solid #00ff00;
            border-radius: 3px;
            padding: 12px 24px;
        }
        .main-button:hover { background-color: #003300; }
        .main-button:disabled { background-color: #111; color: #333; border-color: #333; }
        .nav-button {
            background-color: #0a0a0a;
            color: #00aa00;
            font-family: monospace;
            font-size: 11px;
            border: 1px solid #00aa00;
            border-radius: 3px;
            padding: 8px 12px;
        }
        .nav-button:hover { background-color: #002200; color: #00ff00; border-color: #00ff00; }
        .nav-button:disabled { background-color: #0a0a0a; color: #333; border-color: #333; }
        .footer { color: #004400; font-family: monospace; font-size: 10px; }
        """
        provider = Gtk.CssProvider()
        provider.load_from_data(css.encode())
        Gtk.StyleContext.add_provider_for_screen(
            Gdk.Screen.get_default(), provider,
            Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION
        )
    
    def build_ui(self):
        vbox = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=0)
        self.add(vbox)
        
        # Header
        header = Gtk.Box()
        header.get_style_context().add_class("header")
        header_label = Gtk.Label(label="[KAYAKNET] // ANONYMOUS NETWORK")
        header_label.get_style_context().add_class("header-text")
        header.pack_start(header_label, False, False, 15)
        vbox.pack_start(header, False, False, 0)
        
        # Content
        content = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=15)
        content.set_margin_top(25)
        content.set_margin_bottom(25)
        content.set_margin_start(30)
        content.set_margin_end(30)
        
        # Logo
        logo = Gtk.Label()
        logo.set_markup('<span font_family="monospace" size="xx-large" foreground="#00ff00" weight="bold">KAYAKNET</span>')
        content.pack_start(logo, False, False, 10)
        
        # Subtitle
        subtitle = Gtk.Label(label="Anonymous // Encrypted // Unstoppable")
        subtitle.get_style_context().add_class("subtitle")
        content.pack_start(subtitle, False, False, 0)
        
        # Status box
        status_frame = Gtk.Frame()
        status_frame.set_shadow_type(Gtk.ShadowType.NONE)
        status_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=5)
        status_box.get_style_context().add_class("status-box")
        status_box.set_margin_top(20)
        
        self.status_label = Gtk.Label(label="[INIT] Starting...")
        self.status_label.get_style_context().add_class("status-text")
        status_box.pack_start(self.status_label, False, False, 5)
        
        self.node_label = Gtk.Label(label="Node: connecting...")
        self.node_label.get_style_context().add_class("status-text")
        status_box.pack_start(self.node_label, False, False, 2)
        
        self.info_label = Gtk.Label(label="Proxy: 127.0.0.1:8888")
        self.info_label.get_style_context().add_class("status-text")
        status_box.pack_start(self.info_label, False, False, 2)
        
        status_frame.add(status_box)
        content.pack_start(status_frame, False, False, 10)
        
        # Main button
        self.open_btn = Gtk.Button(label="> OPEN KAYAKNET")
        self.open_btn.get_style_context().add_class("main-button")
        self.open_btn.set_size_request(250, 50)
        self.open_btn.connect("clicked", self.on_open_click)
        self.open_btn.set_sensitive(True)  # Start enabled for testing
        self.all_buttons.append(self.open_btn)
        
        btn_box = Gtk.Box()
        btn_box.set_halign(Gtk.Align.CENTER)
        btn_box.set_margin_top(15)
        btn_box.pack_start(self.open_btn, False, False, 0)
        content.pack_start(btn_box, False, False, 0)
        
        # Nav buttons
        nav_box = Gtk.Box(spacing=10)
        nav_box.set_halign(Gtk.Align.CENTER)
        nav_box.set_margin_top(10)
        
        for text, path in [("[CHAT]", "/chat"), ("[MARKET]", "/market"), ("[DOMAINS]", "/domains")]:
            btn = Gtk.Button(label=text)
            btn.get_style_context().add_class("nav-button")
            btn.connect("clicked", self.on_nav_click, path)
            btn.set_sensitive(True)  # Start enabled for testing
            nav_box.pack_start(btn, False, False, 0)
            self.all_buttons.append(btn)
        
        content.pack_start(nav_box, False, False, 0)
        
        # Footer
        footer = Gtk.Label(label=f"v{APP_VERSION} | github.com/KayakNet")
        footer.get_style_context().add_class("footer")
        footer.set_margin_top(25)
        content.pack_start(footer, False, False, 0)
        
        vbox.pack_start(content, True, False, 0)
    
    def on_open_click(self, widget):
        log("[BUTTON] Open button clicked!")
        open_browser("http://127.0.0.1:8080")
    
    def on_nav_click(self, widget, path):
        log(f"[BUTTON] Nav button clicked: {path}")
        open_browser(f"http://127.0.0.1:8080{path}")
    
    def on_quit(self, widget):
        self.daemon.stop()
        Gtk.main_quit()
    
    def connect_daemon(self):
        import time
        
        # Check if already running
        try:
            resp = urllib.request.urlopen("http://127.0.0.1:8080/api/stats", timeout=2)
            data = json.loads(resp.read())
            GLib.idle_add(self.on_connected, data)
            return
        except:
            pass
        
        # Download if needed
        if not self.daemon.binary_path.exists():
            GLib.idle_add(self.set_status, "[DOWNLOAD] Fetching binary...")
            if not self.daemon.download():
                GLib.idle_add(self.set_status, "[ERROR] Download failed")
                return
        
        # Start daemon
        GLib.idle_add(self.set_status, "[INIT] Starting daemon...")
        if not self.daemon.start():
            GLib.idle_add(self.set_status, "[ERROR] Start failed")
            return
        
        # Wait for ready
        for i in range(30):
            time.sleep(1)
            GLib.idle_add(self.set_status, f"[CONN] Connecting... {i+1}s")
            try:
                resp = urllib.request.urlopen("http://127.0.0.1:8080/api/stats", timeout=2)
                data = json.loads(resp.read())
                GLib.idle_add(self.on_connected, data)
                return
            except:
                pass
        
        GLib.idle_add(self.set_status, "[ERROR] Connection timeout")
    
    def set_status(self, text):
        self.status_label.set_text(text)
    
    def on_connected(self, stats):
        self.status_label.set_text("[ONLINE] Connected")
        self.status_label.get_style_context().remove_class("status-text")
        self.status_label.get_style_context().add_class("status-online")
        
        # Enable buttons
        for btn in self.all_buttons:
            btn.set_sensitive(True)
        
        # Update info
        node_id = stats.get("node_id", "unknown")
        if node_id and len(node_id) > 16:
            node_id = node_id[:8] + "..." + node_id[-8:]
        self.node_label.set_text(f"Node: {node_id}")
        
        v = stats.get("version", "?")
        p = stats.get("peers", 0)
        l = stats.get("listings", 0)
        self.info_label.set_text(f"{v} | {p} peers | {l} listings")


def main():
    signal.signal(signal.SIGINT, signal.SIG_DFL)
    win = KayakNetWindow()
    Gtk.main()


if __name__ == "__main__":
    main()
