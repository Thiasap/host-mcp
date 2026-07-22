#!/usr/bin/env bash
set -euo pipefail
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
GO_BIN=${GO_BIN:-/home/saltfish/.local/go1.26.5/bin/go}
VERSION=${VERSION:-2.0.0}
DIST=${DIST:-"$ROOT/dist"}
rm -rf "$DIST/release"
mkdir -p "$DIST/release"
for arch in amd64 arm64; do
  stage="$DIST/release/host-mcp_${VERSION}_linux_${arch}"
  mkdir -p "$stage/docs"
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" "$GO_BIN" build -trimpath -ldflags='-s -w' -o "$stage/host-mcp" "$ROOT/cmd/host-mcp"
  install -m 0644 "$ROOT/README.md" "$stage/"
  install -m 0644 "$ROOT/docs/USAGE.md" "$ROOT/docs/SECURITY.md" "$ROOT/docs/POLICY.md" "$stage/docs/"
  install -m 0644 "$ROOT/packaging/systemd/host-mcp.service" "$stage/"
  tar -C "$DIST/release" -czf "$DIST/release/host-mcp_${VERSION}_linux_${arch}.tar.gz" "$(basename "$stage")"
done
(cd "$DIST/release" && sha256sum ./*.tar.gz > SHA256SUMS)

