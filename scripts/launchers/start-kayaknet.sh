#!/bin/bash
# KayakNet Launcher for Linux/macOS
# Double-click this file or run from terminal

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
KAYAKD="$SCRIPT_DIR/kayakd"

# Check if kayakd exists
if [ ! -f "$KAYAKD" ]; then
    # Try current directory
    KAYAKD="./kayakd"
fi

if [ ! -f "$KAYAKD" ]; then
    echo "Error: kayakd not found!"
    echo "Please place this script in the same directory as kayakd"
    read -p "Press Enter to exit..."
    exit 1
fi

# Make executable
chmod +x "$KAYAKD"

echo "╔════════════════════════════════════════════════════════════╗"
echo "║                Starting KayakNet...                        ║"
echo "╚════════════════════════════════════════════════════════════╝"
echo ""

# Start with bootstrap and proxy
"$KAYAKD" -i --bootstrap 203.161.33.237:4242 --proxy --name "user-$(hostname)"

