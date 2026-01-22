#!/bin/bash
# Build KayakNet AppImage
# Creates a portable, single-file executable

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
BUILD_DIR="/tmp/kayaknet-appimage"
APP_DIR="$BUILD_DIR/KayakNet.AppDir"
VERSION="1.0.0"

echo "=== Building KayakNet AppImage ==="

# Clean previous build
rm -rf "$BUILD_DIR"
mkdir -p "$APP_DIR"

# Create AppDir structure
mkdir -p "$APP_DIR/usr/bin"
mkdir -p "$APP_DIR/usr/share/applications"
mkdir -p "$APP_DIR/usr/share/icons/hicolor/256x256/apps"
mkdir -p "$APP_DIR/usr/lib/python3/dist-packages"

# Copy main script
cp "$PROJECT_DIR/kayaknet-gui.py" "$APP_DIR/usr/bin/kayaknet-gui"
chmod +x "$APP_DIR/usr/bin/kayaknet-gui"

# Create wrapper script that uses system Python
cat > "$APP_DIR/AppRun" << 'EOF'
#!/bin/bash
HERE="$(dirname "$(readlink -f "${0}")")"
export PATH="${HERE}/usr/bin:${PATH}"
exec python3 "${HERE}/usr/bin/kayaknet-gui" "$@"
EOF
chmod +x "$APP_DIR/AppRun"

# Create desktop file
cat > "$APP_DIR/kayaknet.desktop" << EOF
[Desktop Entry]
Name=KayakNet
Comment=Anonymous Encrypted Network
Exec=kayaknet-gui
Icon=kayaknet
Terminal=false
Type=Application
Categories=Network;Security;
Keywords=anonymous;encrypted;chat;marketplace;privacy;
EOF
cp "$APP_DIR/kayaknet.desktop" "$APP_DIR/usr/share/applications/"

# Create icon (simple green circle for now)
if command -v convert &> /dev/null; then
    convert -size 256x256 xc:'#000000' -fill '#00ff00' \
        -draw "circle 128,128 128,30" \
        -fill '#000000' -pointsize 72 -gravity center \
        -annotate 0 'K' \
        "$APP_DIR/kayaknet.png"
else
    # Fallback: copy from Android if exists
    if [ -f "$PROJECT_DIR/../android/app/src/main/res/drawable/kayaknet_logo.png" ]; then
        cp "$PROJECT_DIR/../android/app/src/main/res/drawable/kayaknet_logo.png" "$APP_DIR/kayaknet.png"
    else
        echo "Warning: No icon created (install imagemagick for icon generation)"
        # Create minimal 1x1 PNG
        printf '\x89PNG\r\n\x1a\n' > "$APP_DIR/kayaknet.png"
    fi
fi
cp "$APP_DIR/kayaknet.png" "$APP_DIR/usr/share/icons/hicolor/256x256/apps/"

# Download appimagetool if not present
APPIMAGETOOL="$BUILD_DIR/appimagetool"
if [ ! -f "$APPIMAGETOOL" ]; then
    echo "Downloading appimagetool..."
    wget -q -O "$APPIMAGETOOL" "https://github.com/AppImage/AppImageKit/releases/download/continuous/appimagetool-x86_64.AppImage"
    chmod +x "$APPIMAGETOOL"
fi

# Build AppImage
cd "$BUILD_DIR"
ARCH=x86_64 "$APPIMAGETOOL" "$APP_DIR" "KayakNet-${VERSION}-x86_64.AppImage"

# Copy to output
mkdir -p "$PROJECT_DIR/dist"
mv "KayakNet-${VERSION}-x86_64.AppImage" "$PROJECT_DIR/dist/"

echo ""
echo "=== AppImage built successfully! ==="
echo "Output: $PROJECT_DIR/dist/KayakNet-${VERSION}-x86_64.AppImage"
echo ""
echo "To run: chmod +x KayakNet-*.AppImage && ./KayakNet-*.AppImage"

