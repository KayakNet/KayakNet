#!/bin/bash
#
# Build all KayakNet Linux packages
#

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

echo "╔════════════════════════════════════════════════════════════╗"
echo "║           KayakNet Linux Package Builder                    ║"
echo "╚════════════════════════════════════════════════════════════╝"
echo ""

# Create dist directory
mkdir -p dist

# Build what we can
echo "=== Building packages ==="
echo ""

# 1. Copy raw script
echo "[1] Copying GUI script..."
cp kayaknet-gui.py dist/kayaknet-gui
chmod +x dist/kayaknet-gui
echo "    ✓ dist/kayaknet-gui"

# 2. Copy install script
echo "[2] Copying universal installer..."
cp install-universal.sh dist/install.sh
chmod +x dist/install.sh
echo "    ✓ dist/install.sh"

# 3. Build .deb if dpkg-deb available
if command -v dpkg-deb &> /dev/null; then
    echo "[3] Building .deb package..."
    chmod +x deb/build-deb.sh
    ./deb/build-deb.sh
    echo "    ✓ dist/kayaknet_*.deb"
else
    echo "[3] Skipping .deb (dpkg-deb not found)"
fi

# 4. Build AppImage if wget available
if command -v wget &> /dev/null; then
    echo "[4] Building AppImage..."
    chmod +x appimage/build-appimage.sh
    ./appimage/build-appimage.sh 2>/dev/null || echo "    ! AppImage build failed (may need appimagetool)"
else
    echo "[4] Skipping AppImage (wget not found)"
fi

# 5. Build RPM if rpmbuild available
if command -v rpmbuild &> /dev/null; then
    echo "[5] Building .rpm package..."
    chmod +x rpm/build-rpm.sh
    ./rpm/build-rpm.sh
    echo "    ✓ dist/kayaknet-*.rpm"
else
    echo "[5] Skipping .rpm (rpmbuild not found)"
fi

echo ""
echo "=== Build complete ==="
echo ""
echo "Output files in: $SCRIPT_DIR/dist/"
ls -la dist/
echo ""
echo "Distribution methods:"
echo "  • Universal: curl -sL https://kayaknet.io/install.sh | bash"
echo "  • Debian/Ubuntu: sudo dpkg -i kayaknet_*.deb"
echo "  • Fedora: sudo dnf install kayaknet-*.rpm"
echo "  • AppImage: ./KayakNet-*.AppImage"
echo "  • Manual: python3 kayaknet-gui"

