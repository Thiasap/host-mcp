TERMUX_PKG_HOMEPAGE=https://github.com/thiasap/host-mcp
TERMUX_PKG_DESCRIPTION="Secure local and trusted-LAN MCP server for Termux, Linux, and WSL"
TERMUX_PKG_LICENSE="MIT"
TERMUX_PKG_MAINTAINER="Thiasap"
TERMUX_PKG_VERSION=2.0.0
TERMUX_PKG_SRCURL=.
TERMUX_PKG_DEPENDS="termux-services"
TERMUX_PKG_BUILD_IN_SRC=true
termux_step_make() { go build -trimpath -ldflags='-s -w' -o host-mcp ./cmd/host-mcp; }
termux_step_make_install() {
    install -Dm755 host-mcp "$TERMUX_PREFIX/bin/host-mcp"
    install -Dm755 packaging/termux/runit/run "$TERMUX_PREFIX/var/service/host-mcp/run"
    install -Dm644 packaging/termux/runit/down "$TERMUX_PREFIX/var/service/host-mcp/down"
}

