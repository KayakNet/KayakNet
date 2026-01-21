#!/bin/bash
#
# KayakNet Linux GUI Installer
# Installs the KayakNet desktop application
#

set -e

echo "╔════════════════════════════════════════════════════════════╗"
echo "║           KayakNet Linux GUI Installer                     ║"
echo "╚════════════════════════════════════════════════════════════╝"
echo ""

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

# Check dependencies
echo -e "${YELLOW}[1/5]${NC} Checking dependencies..."

MISSING_DEPS=""

# Check Python
if ! command -v python3 &> /dev/null; then
    MISSING_DEPS="$MISSING_DEPS python3"
fi

# Check GTK4
python3 -c "import gi; gi.require_version('Gtk', '4.0')" 2>/dev/null || MISSING_DEPS="$MISSING_DEPS gir1.2-gtk-4.0"

# Check Adwaita
python3 -c "import gi; gi.require_version('Adw', '1')" 2>/dev/null || MISSING_DEPS="$MISSING_DEPS gir1.2-adw-1"

# Check WebKit
python3 -c "import gi; gi.require_version('WebKit', '6.0')" 2>/dev/null || MISSING_DEPS="$MISSING_DEPS gir1.2-webkit-6.0"

if [ -n "$MISSING_DEPS" ]; then
    echo -e "${RED}Missing dependencies:${NC}$MISSING_DEPS"
    echo ""
    echo "Install them with:"
    echo ""
    echo "  Ubuntu/Debian:"
    echo "    sudo apt install python3 gir1.2-gtk-4.0 libadwaita-1-dev gir1.2-adw-1 gir1.2-webkit-6.0"
    echo ""
    echo "  Fedora:"
    echo "    sudo dnf install python3 gtk4 libadwaita webkit2gtk4.1"
    echo ""
    echo "  Arch:"
    echo "    sudo pacman -S python gtk4 libadwaita webkit2gtk-4.1"
    echo ""
    exit 1
fi

echo -e "${GREEN}✓${NC} All dependencies found"

# Get installation directory
INSTALL_DIR="${HOME}/.local"
BIN_DIR="${INSTALL_DIR}/bin"
APP_DIR="${INSTALL_DIR}/share/applications"
ICON_DIR="${INSTALL_DIR}/share/icons/hicolor/256x256/apps"
KAYAKNET_DIR="${HOME}/.kayaknet"

# Create directories
echo -e "${YELLOW}[2/5]${NC} Creating directories..."
mkdir -p "$BIN_DIR"
mkdir -p "$APP_DIR"
mkdir -p "$ICON_DIR"
mkdir -p "$KAYAKNET_DIR"
echo -e "${GREEN}✓${NC} Directories created"

# Install the GUI script
echo -e "${YELLOW}[3/5]${NC} Installing KayakNet GUI..."
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cp "$SCRIPT_DIR/kayaknet-gui.py" "$BIN_DIR/kayaknet-gui"
chmod +x "$BIN_DIR/kayaknet-gui"
echo -e "${GREEN}✓${NC} GUI script installed"

# Install desktop file
echo -e "${YELLOW}[4/5]${NC} Installing desktop entry..."
sed "s|Exec=kayaknet-gui|Exec=$BIN_DIR/kayaknet-gui|g" "$SCRIPT_DIR/net.kayaknet.desktop.desktop" > "$APP_DIR/net.kayaknet.desktop"

# Create icon if not exists (using a simple SVG)
if [ ! -f "$ICON_DIR/kayaknet.png" ]; then
    # Check if we have the logo
    if [ -f "$SCRIPT_DIR/../../apps/android/app/src/main/res/drawable/kayaknet_logo.png" ]; then
        cp "$SCRIPT_DIR/../../apps/android/app/src/main/res/drawable/kayaknet_logo.png" "$ICON_DIR/kayaknet.png"
    else
        # Create a simple placeholder icon (green circle)
        convert -size 256x256 xc:black -fill '#00ff00' -draw "circle 128,128 128,20" "$ICON_DIR/kayaknet.png" 2>/dev/null || true
    fi
fi
echo -e "${GREEN}✓${NC} Desktop entry installed"

# Download KayakNet daemon
echo -e "${YELLOW}[5/5]${NC} Downloading KayakNet daemon..."
if [ ! -f "$KAYAKNET_DIR/kayakd" ]; then
    ARCH=$(uname -m)
    if [ "$ARCH" = "x86_64" ]; then
        BINARY_URL="https://github.com/KayakNet/downloads/raw/main/releases/linux/kayakd"
    elif [ "$ARCH" = "aarch64" ]; then
        BINARY_URL="https://github.com/KayakNet/downloads/raw/main/releases/linux/kayakd-arm64"
    else
        echo -e "${YELLOW}⚠${NC} Unsupported architecture: $ARCH"
        echo "  You may need to build kayakd from source"
        BINARY_URL=""
    fi
    
    if [ -n "$BINARY_URL" ]; then
        curl -L -o "$KAYAKNET_DIR/kayakd" "$BINARY_URL" 2>/dev/null || wget -O "$KAYAKNET_DIR/kayakd" "$BINARY_URL" 2>/dev/null || {
            echo -e "${YELLOW}⚠${NC} Could not download daemon. You can download it manually later."
        }
        chmod +x "$KAYAKNET_DIR/kayakd" 2>/dev/null || true
    fi
fi

if [ -f "$KAYAKNET_DIR/kayakd" ]; then
    echo -e "${GREEN}✓${NC} KayakNet daemon ready"
else
    echo -e "${YELLOW}⚠${NC} Daemon not downloaded - will attempt on first run"
fi

# Update desktop database
update-desktop-database "$APP_DIR" 2>/dev/null || true

echo ""
echo "╔════════════════════════════════════════════════════════════╗"
echo "║           Installation Complete!                           ║"
echo "╚════════════════════════════════════════════════════════════╝"
echo ""
echo "You can now run KayakNet from:"
echo ""
echo "  • Your application menu (search for 'KayakNet')"
echo "  • Terminal: kayaknet-gui"
echo ""

# Add to PATH if not already there
if [[ ":$PATH:" != *":$BIN_DIR:"* ]]; then
    echo -e "${YELLOW}Note:${NC} Add this to your ~/.bashrc or ~/.zshrc:"
    echo ""
    echo "  export PATH=\"\$HOME/.local/bin:\$PATH\""
    echo ""
fi

echo "Enjoy anonymous communication! 🟢"

