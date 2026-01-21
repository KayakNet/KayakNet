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
            self.set_default_size(500, 600)
            
            self.daemon = KayakNetDaemon()
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
            
            # Content
            content = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=20)
            content.set_margin_top(40)
            content.set_margin_bottom(40)
            content.set_margin_start(40)
            content.set_margin_end(40)
            content.set_valign(Gtk.Align.CENTER)
            content.set_halign(Gtk.Align.CENTER)
            
            # Logo/Title
            logo_label = Gtk.Label(label="🛶")
            logo_label.add_css_class("title-1")
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
            
            # Buttons
            button_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=10)
            button_box.set_margin_top(30)
            button_box.set_halign(Gtk.Align.CENTER)
            
            self.open_btn = Gtk.Button(label="Open KayakNet")
            self.open_btn.add_css_class("suggested-action")
            self.open_btn.add_css_class("pill")
            self.open_btn.set_size_request(200, -1)
            self.open_btn.connect("clicked", self.on_open_clicked)
            self.open_btn.set_sensitive(False)
            button_box.append(self.open_btn)
            
            # Quick links
            links_box = Gtk.Box(spacing=10)
            links_box.set_halign(Gtk.Align.CENTER)
            links_box.set_margin_top(10)
            
            for name, path in [("Chat", "/chat"), ("Market", "/market"), ("Domains", "/domains")]:
                btn = Gtk.Button(label=name)
                btn.connect("clicked", self.on_link_clicked, path)
                btn.set_sensitive(False)
                links_box.append(btn)
                
            button_box.append(links_box)
            content.append(button_box)
            
            # Info
            info_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=5)
            info_box.set_margin_top(40)
            info_box.set_halign(Gtk.Align.CENTER)
            
            self.node_label = Gtk.Label(label="Node ID: --")
            self.node_label.add_css_class("dim-label")
            self.node_label.add_css_class("monospace")
            info_box.append(self.node_label)
            
            self.proxy_label = Gtk.Label(label="Proxy: 127.0.0.1:8888")
            self.proxy_label.add_css_class("dim-label")
            info_box.append(self.proxy_label)
            
            content.append(info_box)
            
            main_box.append(content)
            self.apply_css()
            
        def apply_css(self):
            """Apply custom CSS"""
            css = b"""
            window { background-color: #1a1a2e; }
            .title-1 { color: #00ff88; font-size: 32px; font-weight: bold; }
            .dim-label { color: #666; }
            .monospace { font-family: monospace; font-size: 11px; }
            button.suggested-action { background: linear-gradient(135deg, #00ff88, #00cc6a); color: #000; }
            button.pill { border-radius: 20px; padding: 12px 24px; }
            """
            provider = Gtk.CssProvider()
            provider.load_from_data(css)
            Gtk.StyleContext.add_provider_for_display(
                Gdk.Display.get_default(),
                provider,
                Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION
            )
        
        def on_open_clicked(self, button):
            webbrowser.open("http://127.0.0.1:8080")
        
        def on_link_clicked(self, button, path):
            webbrowser.open(f"http://127.0.0.1:8080{path}")
        
        def start_daemon(self):
            self.spinner.start()
            self.status_label.set_text("Starting KayakNet...")
            
            def _start():
                if not self.daemon.binary_path.exists():
                    GLib.idle_add(lambda: self.status_label.set_text("Downloading..."))
                    if not self.daemon.download_binary():
                        GLib.idle_add(lambda: self.status_label.set_text("Download failed"))
                        GLib.idle_add(lambda: self.spinner.stop())
                        return
                
                GLib.idle_add(lambda: self.status_label.set_text("Starting daemon..."))
                if not self.daemon.start():
                    GLib.idle_add(lambda: self.status_label.set_text("Failed to start"))
                    GLib.idle_add(lambda: self.spinner.stop())
                    return
                
                import time
                for i in range(30):
                    time.sleep(1)
                    try:
                        response = urllib.request.urlopen("http://127.0.0.1:8080/api/stats", timeout=2)
                        data = json.loads(response.read())
                        GLib.idle_add(lambda: self.on_daemon_ready(data))
                        return
                    except:
                        GLib.idle_add(lambda i=i: self.status_label.set_text(f"Waiting... ({i+1}s)"))
                
                GLib.idle_add(lambda: self.status_label.set_text("Timeout - daemon not responding"))
                GLib.idle_add(lambda: self.spinner.stop())
            
            thread = threading.Thread(target=_start, daemon=True)
            thread.start()
        
        def on_daemon_ready(self, stats):
            self.spinner.stop()
            self.status_label.set_text("● Connected")
            self.status_label.remove_css_class("dim-label")
            
            self.open_btn.set_sensitive(True)
            for child in self.open_btn.get_parent().get_last_child().observe_children():
                if isinstance(child, Gtk.Button):
                    child.set_sensitive(True)
            
            # Update info
            node_id = stats.get("node_id", "unknown")
            if len(node_id) > 20:
                node_id = node_id[:10] + "..." + node_id[-10:]
            self.node_label.set_text(f"Node: {node_id}")


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
            self.set_default_size(500, 600)
            self.set_position(Gtk.WindowPosition.CENTER)
            
            self.daemon = KayakNetDaemon()
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
            self.open_btn = Gtk.Button(label="Open KayakNet")
            self.open_btn.get_style_context().add_class("suggested-action")
            self.open_btn.set_size_request(200, 40)
            self.open_btn.connect("clicked", self.on_open_clicked)
            self.open_btn.set_sensitive(False)
            self.open_btn.set_margin_top(30)
            content.pack_start(self.open_btn, False, False, 0)
            
            # Quick links
            links_box = Gtk.Box(spacing=10)
            links_box.set_halign(Gtk.Align.CENTER)
            links_box.set_margin_top(10)
            
            self.link_buttons = []
            for name, path in [("Chat", "/chat"), ("Market", "/market"), ("Domains", "/domains")]:
                btn = Gtk.Button(label=name)
                btn.connect("clicked", self.on_link_clicked, path)
                btn.set_sensitive(False)
                links_box.pack_start(btn, False, False, 0)
                self.link_buttons.append(btn)
                
            content.pack_start(links_box, False, False, 0)
            
            # Info
            info_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=5)
            info_box.set_margin_top(40)
            
            self.node_label = Gtk.Label(label="Node ID: --")
            self.node_label.get_style_context().add_class("dim-label")
            info_box.pack_start(self.node_label, False, False, 0)
            
            self.proxy_label = Gtk.Label(label="Proxy: 127.0.0.1:8888")
            self.proxy_label.get_style_context().add_class("dim-label")
            info_box.pack_start(self.proxy_label, False, False, 0)
            
            content.pack_start(info_box, False, False, 0)
            
            main_box.pack_start(content, True, True, 0)
            self.apply_css()
            self.show_all()
            
        def apply_css(self):
            """Apply custom CSS"""
            css = b"""
            window { background-color: #1a1a2e; }
            .dim-label { color: #666; }
            label { color: #eee; }
            """
            provider = Gtk.CssProvider()
            provider.load_from_data(css)
            Gtk.StyleContext.add_provider_for_screen(
                Gdk.Screen.get_default(),
                provider,
                Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION
            )
        
        def on_open_clicked(self, button):
            webbrowser.open("http://127.0.0.1:8080")
        
        def on_link_clicked(self, button, path):
            webbrowser.open(f"http://127.0.0.1:8080{path}")
        
        def start_daemon(self):
            self.spinner.start()
            self.status_label.set_text("Starting KayakNet...")
            
            def _start():
                if not self.daemon.binary_path.exists():
                    GLib.idle_add(lambda: self.status_label.set_text("Downloading..."))
                    if not self.daemon.download_binary():
                        GLib.idle_add(lambda: self.status_label.set_text("Download failed"))
                        GLib.idle_add(lambda: self.spinner.stop())
                        return
                
                GLib.idle_add(lambda: self.status_label.set_text("Starting daemon..."))
                if not self.daemon.start():
                    GLib.idle_add(lambda: self.status_label.set_text("Failed to start"))
                    GLib.idle_add(lambda: self.spinner.stop())
                    return
                
                import time
                for i in range(30):
                    time.sleep(1)
                    try:
                        response = urllib.request.urlopen("http://127.0.0.1:8080/api/stats", timeout=2)
                        data = json.loads(response.read())
                        GLib.idle_add(lambda: self.on_daemon_ready(data))
                        return
                    except:
                        GLib.idle_add(lambda i=i: self.status_label.set_text(f"Waiting... ({i+1}s)"))
                
                GLib.idle_add(lambda: self.status_label.set_text("Timeout"))
                GLib.idle_add(lambda: self.spinner.stop())
            
            thread = threading.Thread(target=_start, daemon=True)
            thread.start()
        
        def on_daemon_ready(self, stats):
            self.spinner.stop()
            self.status_label.set_markup("<span foreground='#00ff88'>● Connected</span>")
            
            self.open_btn.set_sensitive(True)
            for btn in self.link_buttons:
                btn.set_sensitive(True)
            
            node_id = stats.get("node_id", "unknown")
            if len(node_id) > 20:
                node_id = node_id[:10] + "..." + node_id[-10:]
            self.node_label.set_text(f"Node: {node_id}")


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
