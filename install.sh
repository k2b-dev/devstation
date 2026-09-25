#!/bin/sh
set -eu

fail() { printf '%s\n' "devstation: $*" >&2; exit 1; }
[ "$(uname -s)" = Linux ] || fail 'only Linux is supported'
case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) fail 'supported architectures: amd64, arm64' ;;
esac
for command in curl sha256sum mktemp install; do
  command -v "$command" >/dev/null 2>&1 || fail "missing command: $command"
done
version=${VERSION:-latest}
if [ "$version" = latest ]; then
  url=$(curl --proto '=https' --proto-redir '=https' -fsSL --connect-timeout 15 --max-time 120 -o /dev/null -w '%{url_effective}' https://github.com/k2b-dev/devstation/releases/latest)
  version=${url##*/}
fi
printf '%s\n' "$version" | LC_ALL=C grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || fail 'VERSION must be vX.Y.Z'
destination=${INSTALL_DIR:-"$HOME/.local/bin"}
mkdir -p "$destination"
# Stage on the destination filesystem so replacement is atomic.
staging=$(mktemp -d "$destination/.devstation-install.XXXXXX")
trap 'rm -rf "$staging"' EXIT HUP INT TERM
asset="devstation_${version}_linux_${arch}"
base="https://github.com/k2b-dev/devstation/releases/download/$version"
curl --proto '=https' --proto-redir '=https' -fsSL --connect-timeout 15 --max-time 120 "$base/checksums.txt" -o "$staging/checksums.txt"
curl --proto '=https' --proto-redir '=https' -fsSL --connect-timeout 15 --max-time 120 "$base/$asset" -o "$staging/$asset"
awk -v name="$asset" '$2 == name { print }' "$staging/checksums.txt" > "$staging/selected.txt"
[ "$(wc -l < "$staging/selected.txt" | tr -d ' ')" = 1 ] || fail 'missing or duplicate checksum'
(cd "$staging" && sha256sum -c selected.txt) || fail 'checksum mismatch; existing binary unchanged'
install -m 755 "$staging/$asset" "$staging/dev"
[ ! -d "$destination/dev" ] || fail 'destination dev is a directory'
mv -f "$staging/dev" "$destination/dev"
printf 'Installed devstation %s at %s/dev\n' "$version" "$destination"
case ":$PATH:" in *":$destination:"*) ;; *) printf 'Add %s to PATH.\n' "$destination" ;; esac
