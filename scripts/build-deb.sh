#!/usr/bin/env bash
set -euo pipefail
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
PREFIX=/data/data/com.termux/files/usr
VERSION=${VERSION:-2.0.0}
ARCH=${ARCH:-aarch64}
GO_BIN=${GO_BIN:-/home/saltfish/.local/go1.26.5/bin/go}
DIST=${DIST:-"$ROOT/dist"}
STAGE="$DIST/deb-root"
PKG="$DIST/host-mcp_${VERSION}_${ARCH}.deb"
rm -rf "$STAGE"
install -d -m 0755 "$STAGE/DEBIAN" "$STAGE$PREFIX/bin" "$STAGE$PREFIX/var/service/host-mcp" "$STAGE$PREFIX/share/doc/host-mcp"
CGO_ENABLED=0 GOOS=android GOARCH=arm64 "$GO_BIN" build -trimpath -ldflags='-s -w' -o "$STAGE$PREFIX/bin/host-mcp" "$ROOT/cmd/host-mcp"
install -m 0755 "$ROOT/packaging/termux/runit/run" "$STAGE$PREFIX/var/service/host-mcp/run"
install -m 0644 "$ROOT/packaging/termux/runit/down" "$STAGE$PREFIX/var/service/host-mcp/down"
install -m 0644 "$ROOT/README.md" "$ROOT/docs/USAGE.md" "$ROOT/docs/SECURITY.md" "$ROOT/docs/POLICY.md" "$STAGE$PREFIX/share/doc/host-mcp/"
cat > "$STAGE/DEBIAN/control" <<CONTROL
Package: host-mcp
Version: $VERSION
Architecture: $ARCH
Maintainer: Thiasap <799721744@qq.com>
Depends: termux-services
Section: utilities
Priority: optional
Homepage: https://github.com/thiasap/host-mcp
Description: Secure local and trusted-LAN MCP server for Termux, Linux, and WSL
 Named rooted filesystem capabilities and constrained direct execution.
CONTROL
cat > "$STAGE/DEBIAN/postinst" <<'POSTINST'
#!/data/data/com.termux/files/usr/bin/sh
set -eu
exit 0
POSTINST
chmod 0755 "$STAGE/DEBIAN/postinst"
command dpkg-deb --root-owner-group --build "$STAGE" "$PKG"
sha256sum "$PKG" > "$PKG.sha256"
printf '%s\n' "$PKG"

