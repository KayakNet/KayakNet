#!/bin/bash
# KayakNet Launcher for macOS
# Double-click this file in Finder to start KayakNet

cd "$(dirname "$0")"

KAYAKD="./kayakd"

if [ ! -f "$KAYAKD" ]; then
    KAYAKD="./kayakd-darwin-arm64"
fi

if [ ! -f "$KAYAKD" ]; then
    KAYAKD="./kayakd-darwin-amd64"
fi

if [ ! -f "$KAYAKD" ]; then
    osascript -e 'display dialog "kayakd not found! Please place this file in the same folder as kayakd." buttons {"OK"} default button "OK" with icon stop'
    exit 1
fi

chmod +x "$KAYAKD"

clear
echo "╔════════════════════════════════════════════════════════════╗"
echo "║                Starting KayakNet...                        ║"
echo "║                                                            ║"
echo "║  Configure browser proxy to 127.0.0.1:8118                 ║"
echo "║  Then browse to any .kyk domain!                           ║"
echo "╚════════════════════════════════════════════════════════════╝"
echo ""

"$KAYAKD" -i --bootstrap 203.161.33.237:4242 --proxy --name "user-$(hostname -s)"


