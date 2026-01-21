# KayakNet Linux GUI

A native GTK4 + WebKit desktop application for KayakNet on Linux.

![KayakNet GUI](screenshot.png)

## Features

- 🖥️ **Native GTK4 Interface** - Feels at home on GNOME, KDE, and other Linux desktops
- 🚀 **Auto-starts daemon** - KayakNet starts automatically when you open the app
- 🌐 **Integrated WebView** - Full access to chat, marketplace, and domains
- 🔒 **Proxy Support** - Browse .kyk domains seamlessly
- 🎨 **Dark Mode** - Matches KayakNet's aesthetic

## Quick Install

```bash
# Clone the repo (if you haven't)
git clone https://github.com/KayakNet/KayakNet.git
cd KayakNet/apps/linux-gui

# Run the installer
chmod +x install.sh
./install.sh
```

## Dependencies

### Ubuntu/Debian
```bash
sudo apt install python3 gir1.2-gtk-4.0 libadwaita-1-dev gir1.2-adw-1 gir1.2-webkit-6.0
```

### Fedora
```bash
sudo dnf install python3 gtk4 libadwaita webkit2gtk4.1 python3-gobject
```

### Arch Linux
```bash
sudo pacman -S python gtk4 libadwaita webkit2gtk-4.1 python-gobject
```

### openSUSE
```bash
sudo zypper install python3 gtk4 libadwaita webkit2gtk4 python3-gobject
```

## Manual Installation

If you prefer to install manually:

```bash
# 1. Copy the script
mkdir -p ~/.local/bin
cp kayaknet-gui.py ~/.local/bin/kayaknet-gui
chmod +x ~/.local/bin/kayaknet-gui

# 2. Copy desktop file
mkdir -p ~/.local/share/applications
cp net.kayaknet.desktop.desktop ~/.local/share/applications/net.kayaknet.desktop

# 3. Add to PATH (add to ~/.bashrc)
export PATH="$HOME/.local/bin:$PATH"

# 4. Run
kayaknet-gui
```

## Running from Terminal

```bash
# If installed
kayaknet-gui

# Or run directly
python3 kayaknet-gui.py
```

## How It Works

1. **On first launch**: Downloads the `kayakd` binary to `~/.kayaknet/`
2. **Starts daemon**: Runs `kayakd -proxy -bootstrap 203.161.33.237:8080`
3. **Loads UI**: Opens the web interface at `http://127.0.0.1:8080` in an embedded WebView
4. **Proxy ready**: The daemon runs a proxy at `127.0.0.1:8888` for .kyk domains

## Configuration

Configuration is stored in `~/.kayaknet/`:

```
~/.kayaknet/
├── kayakd          # The daemon binary
├── node.key        # Your node's private key
└── data/           # Local data storage
```

## Troubleshooting

### "WebKit not found"
Install WebKit GTK:
```bash
# Ubuntu/Debian
sudo apt install gir1.2-webkit-6.0

# If that doesn't work, try WebKit 4.1
sudo apt install gir1.2-webkit2-4.1
```

Then edit `kayaknet-gui.py` and change:
```python
gi.require_version('WebKit', '6.0')
```
to:
```python
gi.require_version('WebKit2', '4.1')
```

### "Adwaita not found"
```bash
sudo apt install libadwaita-1-dev gir1.2-adw-1
```

### Daemon won't start
Check if port 8080 is already in use:
```bash
lsof -i :8080
```

### Manual daemon start
```bash
~/.kayaknet/kayakd -proxy -bootstrap 203.161.33.237:8080
```

## Uninstall

```bash
rm ~/.local/bin/kayaknet-gui
rm ~/.local/share/applications/net.kayaknet.desktop
rm -rf ~/.kayaknet  # Optional: removes all data
```

## Building from Source

If you want to build the KayakNet daemon from source:

```bash
# Clone main repo
git clone https://github.com/KayakNet/KayakNet.git
cd KayakNet

# Build
go build -o kayakd ./cmd/kayakd

# Copy to ~/.kayaknet
mkdir -p ~/.kayaknet
cp kayakd ~/.kayaknet/
```

## License

MIT License - See [LICENSE](../../LICENSE) for details.

## Links

- [KayakNet Website](https://kayaknet.io)
- [Documentation](https://docs.kayaknet.io)
- [GitHub](https://github.com/KayakNet/KayakNet)
- [Discord](https://discord.gg/kayaknet)

