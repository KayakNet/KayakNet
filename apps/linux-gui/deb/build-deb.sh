#!/bin/bash
# Build KayakNet .deb package for Debian/Ubuntu

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
VERSION="1.0.0"
PACKAGE="kayaknet"
ARCH="amd64"
BUILD_DIR="/tmp/kayaknet-deb"
DEB_DIR="$BUILD_DIR/${PACKAGE}_${VERSION}_${ARCH}"

echo "=== Building KayakNet .deb package ==="

# Clean
rm -rf "$BUILD_DIR"
mkdir -p "$DEB_DIR/DEBIAN"
mkdir -p "$DEB_DIR/usr/bin"
mkdir -p "$DEB_DIR/usr/share/applications"
mkdir -p "$DEB_DIR/usr/share/icons/hicolor/256x256/apps"
mkdir -p "$DEB_DIR/usr/share/doc/kayaknet"

# Control file
cat > "$DEB_DIR/DEBIAN/control" << EOF
Package: kayaknet
Version: $VERSION
Section: net
Priority: optional
Architecture: $ARCH
Depends: python3 (>= 3.8), python3-gi, gir1.2-gtk-3.0
Maintainer: KayakNet Team <team@kayaknet.io>
Homepage: https://kayaknet.io
Description: Anonymous Encrypted Network
 KayakNet is a decentralized anonymous network for private
 communication and commerce.
 .
 Features:
  - End-to-end encrypted messaging
  - Anonymous marketplace with crypto escrow
  - .kyk domain registration
  - Onion routing for traffic analysis resistance
EOF

# Post-install script
cat > "$DEB_DIR/DEBIAN/postinst" << 'EOF'
#!/bin/bash
set -e
update-desktop-database /usr/share/applications 2>/dev/null || true
gtk-update-icon-cache /usr/share/icons/hicolor 2>/dev/null || true
EOF
chmod 755 "$DEB_DIR/DEBIAN/postinst"

# Copy main script
cp "$PROJECT_DIR/kayaknet-gui.py" "$DEB_DIR/usr/bin/kayaknet-gui"
chmod 755 "$DEB_DIR/usr/bin/kayaknet-gui"

# Desktop file
cat > "$DEB_DIR/usr/share/applications/kayaknet.desktop" << EOF
[Desktop Entry]
Name=KayakNet
Comment=Anonymous Encrypted Network
Exec=/usr/bin/kayaknet-gui
Icon=kayaknet
Terminal=false
Type=Application
Categories=Network;InstantMessaging;Security;
Keywords=anonymous;encrypted;chat;marketplace;privacy;
StartupNotify=true
EOF

# Copyright
cat > "$DEB_DIR/usr/share/doc/kayaknet/copyright" << EOF
Format: https://www.debian.org/doc/packaging-manuals/copyright-format/1.0/
Upstream-Name: KayakNet
Source: https://github.com/KayakNet/KayakNet

Files: *
Copyright: 2024-2026 KayakNet Team
License: MIT
EOF

# Icon (copy from Android or create placeholder)
if [ -f "$PROJECT_DIR/../android/app/src/main/res/drawable/kayaknet_logo.png" ]; then
    cp "$PROJECT_DIR/../android/app/src/main/res/drawable/kayaknet_logo.png" \
       "$DEB_DIR/usr/share/icons/hicolor/256x256/apps/kayaknet.png"
fi

# Build package
cd "$BUILD_DIR"
dpkg-deb --build "$DEB_DIR"

# Copy to output
mkdir -p "$PROJECT_DIR/dist"
mv "${DEB_DIR}.deb" "$PROJECT_DIR/dist/"

echo ""
echo "=== .deb package built successfully! ==="
echo "Output: $PROJECT_DIR/dist/${PACKAGE}_${VERSION}_${ARCH}.deb"
echo ""
echo "To install: sudo dpkg -i kayaknet_*.deb && sudo apt-get install -f"

