#!/bin/bash
# Build KayakNet releases for all platforms

VERSION="${1:-v0.1.0}"
OUTPUT_DIR="dist"
BINARY_NAME="kayakd"

echo "Building KayakNet $VERSION releases..."

# Clean
rm -rf $OUTPUT_DIR
mkdir -p $OUTPUT_DIR

# Platforms to build
PLATFORMS=(
    "linux/amd64"
    "linux/arm64"
    "darwin/amd64"
    "darwin/arm64"
    "windows/amd64"
)

for PLATFORM in "${PLATFORMS[@]}"; do
    GOOS="${PLATFORM%/*}"
    GOARCH="${PLATFORM#*/}"
    
    OUTPUT_NAME="${BINARY_NAME}-${VERSION}-${GOOS}-${GOARCH}"
    
    if [ "$GOOS" = "windows" ]; then
        OUTPUT_NAME="${OUTPUT_NAME}.exe"
    fi
    
    echo "Building $OUTPUT_NAME..."
    
    GOOS=$GOOS GOARCH=$GOARCH go build -ldflags="-s -w -X main.Version=$VERSION" \
        -o "$OUTPUT_DIR/$OUTPUT_NAME" ./cmd/kayakd
    
    if [ $? -eq 0 ]; then
        # Create archive
        cd $OUTPUT_DIR
        if [ "$GOOS" = "windows" ]; then
            zip "${OUTPUT_NAME%.exe}.zip" "$OUTPUT_NAME" ../README.md ../LICENSE 2>/dev/null
        else
            tar czf "${OUTPUT_NAME}.tar.gz" "$OUTPUT_NAME" -C .. README.md LICENSE 2>/dev/null
        fi
        cd ..
        echo "  [OK] $OUTPUT_NAME"
    else
        echo "  [FAIL] $OUTPUT_NAME"
    fi
done

# Create checksums
cd $OUTPUT_DIR
sha256sum *.tar.gz *.zip 2>/dev/null > checksums.txt
cd ..

echo ""
echo "Releases built in $OUTPUT_DIR/"
ls -la $OUTPUT_DIR/

