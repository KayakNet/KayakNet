#!/bin/bash
# Build KayakNet .rpm package for Fedora/RHEL/CentOS

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
VERSION="1.0.0"
PACKAGE="kayaknet"

echo "=== Building KayakNet .rpm package ==="

# Setup rpmbuild directories
mkdir -p ~/rpmbuild/{BUILD,RPMS,SOURCES,SPECS,SRPMS}

# Create source tarball
TARBALL_DIR="/tmp/${PACKAGE}-${VERSION}"
rm -rf "$TARBALL_DIR"
mkdir -p "$TARBALL_DIR"
cp "$PROJECT_DIR/kayaknet-gui.py" "$TARBALL_DIR/"
cp "$SCRIPT_DIR/kayaknet.spec" ~/rpmbuild/SPECS/

cd /tmp
tar czf ~/rpmbuild/SOURCES/${PACKAGE}-${VERSION}.tar.gz ${PACKAGE}-${VERSION}

# Build RPM
cd ~/rpmbuild/SPECS
rpmbuild -ba kayaknet.spec

# Copy to output
mkdir -p "$PROJECT_DIR/dist"
cp ~/rpmbuild/RPMS/noarch/${PACKAGE}-${VERSION}*.rpm "$PROJECT_DIR/dist/" 2>/dev/null || \
cp ~/rpmbuild/RPMS/x86_64/${PACKAGE}-${VERSION}*.rpm "$PROJECT_DIR/dist/" 2>/dev/null || true

echo ""
echo "=== .rpm package built successfully! ==="
echo "Output: $PROJECT_DIR/dist/"
echo ""
echo "To install: sudo dnf install kayaknet-*.rpm"

