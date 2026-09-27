#!/bin/sh
# Install calport from GitHub releases.
#
#   On a box:     curl -fsSL https://raw.githubusercontent.com/sean-brydon/calport/main/install.sh | sh
#   CLI only:     curl -fsSL https://raw.githubusercontent.com/sean-brydon/calport/main/install.sh | sh -s -- calport
#
# The default installs calportd, starts it at boot, and prints a pairing link.
# Every download is checked against the release's SHA256SUMS before it runs.
#
# Environment:
#   CALPORT_VERSION   a release tag such as v0.1.0 (default: latest)
#   CALPORT_BIN_DIR   where binaries go (default: ~/.local/bin)
#   CALPORTD_LISTEN   address calportd listens on (default: this box's tailnet address)
#   CALPORT_NO_PAIR   set to skip printing a pairing link
set -eu

REPO="sean-brydon/calport"
COMPONENT="${1:-calportd}"
BIN_DIR="${CALPORT_BIN_DIR:-$HOME/.local/bin}"
VERSION="${CALPORT_VERSION:-latest}"

say() { printf '%s\n' "$*"; }
die() { printf 'calport install: %s\n' "$*" >&2; exit 1; }

case "$COMPONENT" in
calportd | calport) ;;
*) die "unknown component \"$COMPONENT\"; use calportd or calport" ;;
esac

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
linux | darwin) ;;
*) die "unsupported OS: $os" ;;
esac
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) die "unsupported CPU: $(uname -m)" ;;
esac
if [ "$COMPONENT" = calportd ] && [ "$os" != linux ]; then
	die "calportd runs on Linux boxes; on a Mac install the Calport app or: sh -s -- calport"
fi

if [ "$VERSION" = latest ]; then
	base="https://github.com/$REPO/releases/latest/download"
else
	base="https://github.com/$REPO/releases/download/$VERSION"
fi

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL --retry 3 -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -q -O "$2" "$1"; }
else
	die "needs curl or wget"
fi
if command -v sha256sum >/dev/null 2>&1; then
	sha() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
	sha() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
	die "needs sha256sum or shasum to verify the download"
fi

asset="$COMPONENT-$os-$arch"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

say "Downloading $asset ($VERSION)…"
fetch "$base/$asset" "$tmp/$asset" || die "could not download $base/$asset"
fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" || die "could not download $base/SHA256SUMS"
want=$(awk -v f="$asset" '$2 == f || $2 == "*"f { print $1 }' "$tmp/SHA256SUMS")
[ -n "$want" ] || die "SHA256SUMS has no entry for $asset"
got=$(sha "$tmp/$asset")
[ "$got" = "$want" ] || die "checksum mismatch for $asset (got $got, want $want)"

mkdir -p "$BIN_DIR"
chmod 755 "$tmp/$asset"
# Rename into place so a running copy keeps its old file until it restarts.
mv -f "$tmp/$asset" "$BIN_DIR/$COMPONENT.new"
mv -f "$BIN_DIR/$COMPONENT.new" "$BIN_DIR/$COMPONENT"
say "Installed $BIN_DIR/$COMPONENT"

case ":$PATH:" in
*":$BIN_DIR:"*) ;;
*) say "Note: $BIN_DIR is not on your PATH; add it to your shell profile." ;;
esac

[ "$COMPONENT" = calportd ] || exit 0

if [ -n "${CALPORTD_LISTEN:-}" ]; then
	"$BIN_DIR/calportd" install --listen "$CALPORTD_LISTEN"
else
	"$BIN_DIR/calportd" install ||
		die "calportd could not pick an address. If this box is not on a tailnet, rerun with CALPORTD_LISTEN=<ip>:7443"
fi
# A reinstall replaces the binary under a running service; restart it onto the new build.
if command -v systemctl >/dev/null 2>&1 && systemctl --user is-active --quiet calportd 2>/dev/null; then
	systemctl --user restart calportd
fi

[ -n "${CALPORT_NO_PAIR:-}" ] && exit 0
say ""
"$BIN_DIR/calportd" pair
