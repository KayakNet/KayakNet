#!/bin/bash
#
# KayakNet Universal Linux Installer
# Works on: Ubuntu, Debian, Fedora, Arch, openSUSE, and most other distros
#
# Usage: curl -sL https://kayaknet.io/install.sh | bash
#

set -e

VERSION="1.0.0"
INSTALL_DIR="$HOME/.local"
BIN_DIR="$INSTALL_DIR/bin"
APP_DIR="$INSTALL_DIR/share/applications"
ICON_DIR="$INSTALL_DIR/share/icons/hicolor/256x256/apps"
KAYAKNET_DIR="$HOME/.kayaknet"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

echo -e "${GREEN}"
cat << 'EOF'
   ██╗  ██╗ █████╗ ██╗   ██╗ █████╗ ██╗  ██╗
   ██║ ██╔╝██╔══██╗╚██╗ ██╔╝██╔══██╗██║ ██╔╝
   █████╔╝ ███████║ ╚████╔╝ ███████║█████╔╝ 
   ██╔═██╗ ██╔══██║  ╚██╔╝  ██╔══██║██╔═██╗ 
   ██║  ██╗██║  ██║   ██║   ██║  ██║██║  ██╗
   ╚═╝  ╚═╝╚═╝  ╚═╝   ╚═╝   ╚═╝  ╚═╝╚═╝  ╚═╝
EOF
echo -e "${NC}"
echo -e "${CYAN}Anonymous // Encrypted // Unstoppable${NC}"
echo ""
echo "KayakNet Linux Installer v${VERSION}"
echo "=================================="
echo ""

# Detect package manager and install dependencies
install_deps() {
    echo -e "${YELLOW}[1/4]${NC} Installing dependencies..."
    
    if command -v apt-get &> /dev/null; then
        echo "Detected: Debian/Ubuntu"
        sudo apt-get update -qq
        sudo apt-get install -y -qq python3 python3-gi gir1.2-gtk-3.0 wget
    elif command -v dnf &> /dev/null; then
        echo "Detected: Fedora/RHEL"
        sudo dnf install -y -q python3 python3-gobject gtk3 wget
    elif command -v pacman &> /dev/null; then
        echo "Detected: Arch Linux"
        sudo pacman -Sy --noconfirm python python-gobject gtk3 wget
    elif command -v zypper &> /dev/null; then
        echo "Detected: openSUSE"
        sudo zypper install -y python3 python3-gobject gtk3 wget
    elif command -v apk &> /dev/null; then
        echo "Detected: Alpine"
        sudo apk add python3 py3-gobject gtk+3.0 wget
    else
        echo -e "${YELLOW}Warning: Unknown package manager. Please install manually:${NC}"
        echo "  - python3"
        echo "  - python3-gi (PyGObject)"
        echo "  - gtk3"
    fi
    
    echo -e "${GREEN}✓${NC} Dependencies installed"
}

# Create directories
create_dirs() {
    echo -e "${YELLOW}[2/4]${NC} Creating directories..."
    mkdir -p "$BIN_DIR"
    mkdir -p "$APP_DIR"
    mkdir -p "$ICON_DIR"
    mkdir -p "$KAYAKNET_DIR"
    echo -e "${GREEN}✓${NC} Directories created"
}

# Download and install
install_kayaknet() {
    echo -e "${YELLOW}[3/4]${NC} Downloading KayakNet..."
    
    # Download GUI script
    SCRIPT_URL="https://raw.githubusercontent.com/KayakNet/KayakNet/main/apps/linux-gui/kayaknet-gui.py"
    wget -q -O "$BIN_DIR/kayaknet-gui" "$SCRIPT_URL" || \
    curl -sL -o "$BIN_DIR/kayaknet-gui" "$SCRIPT_URL"
    chmod +x "$BIN_DIR/kayaknet-gui"
    
    # Download daemon binary
    echo "Downloading daemon..."
    DAEMON_URL="https://github.com/KayakNet/downloads/raw/main/releases/linux/kayakd"
    wget -q -O "$KAYAKNET_DIR/kayakd" "$DAEMON_URL" || \
    curl -sL -o "$KAYAKNET_DIR/kayakd" "$DAEMON_URL"
    chmod +x "$KAYAKNET_DIR/kayakd"
    
    echo -e "${GREEN}✓${NC} KayakNet downloaded"
}

# Create desktop entry
create_desktop_entry() {
    echo -e "${YELLOW}[4/4]${NC} Creating desktop entry..."
    
    cat > "$APP_DIR/kayaknet.desktop" << EOF
[Desktop Entry]
Name=KayakNet
Comment=Anonymous Encrypted Network
Exec=$BIN_DIR/kayaknet-gui
Icon=kayaknet
Terminal=false
Type=Application
Categories=Network;InstantMessaging;Security;
Keywords=anonymous;encrypted;chat;marketplace;privacy;
StartupNotify=true
EOF

    # Update desktop database
    update-desktop-database "$APP_DIR" 2>/dev/null || true
    
    echo -e "${GREEN}✓${NC} Desktop entry created"
}

# Main installation
main() {
    # Check if running as root
    if [ "$EUID" -eq 0 ]; then
        echo -e "${RED}Error: Do not run as root. Run as normal user.${NC}"
        exit 1
    fi
    
    install_deps
    create_dirs
    install_kayaknet
    create_desktop_entry
    
    # Add to PATH if needed
    if [[ ":$PATH:" != *":$BIN_DIR:"* ]]; then
        echo ""
        echo -e "${YELLOW}Add this to your ~/.bashrc or ~/.zshrc:${NC}"
        echo "  export PATH=\"\$HOME/.local/bin:\$PATH\""
        echo ""
    fi
    
    echo ""
    echo -e "${GREEN}╔════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${GREEN}║           KayakNet installed successfully!                  ║${NC}"
    echo -e "${GREEN}╚════════════════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "Run KayakNet:"
    echo "  • From application menu: Search for 'KayakNet'"
    echo "  • From terminal: kayaknet-gui"
    echo ""
    echo -e "${CYAN}Welcome to the anonymous internet.${NC}"
}

# Run
main "$@"

