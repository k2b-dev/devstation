#!/bin/sh
# Offline installer tests: replace transport only; install real release artifacts.
set -eu
root=$(pwd)
version=${VERSION:-v0.0.0}
staging=$(mktemp -d)
trap 'rm -rf "$staging"' EXIT HUP INT TERM
mkdir -p "$staging/bin" "$staging/home"
cat > "$staging/bin/curl" <<'MOCK'
#!/bin/sh
set -eu
output=''
url=''
while [ "$#" -gt 0 ]; do
 case "$1" in
  -o) output=$2; shift ;;
  https://*) url=$1 ;;
 esac
 shift
done
case "$url" in
 */releases/latest) printf 'https://github.com/k2b-dev/devstation/releases/tag/%s' "$TEST_VERSION" ;;
 */checksums.txt) cp "$TEST_ASSETS/checksums.txt" "$output" ;;
 *) cp "$TEST_ASSETS/${url##*/}" "$output" ;;
esac
MOCK
chmod +x "$staging/bin/curl"
export TEST_ASSETS="$root/dist" TEST_VERSION="$version"
export PATH="$staging/bin:$PATH" HOME="$staging/home"
INSTALL_DIR="$staging/install" VERSION="$version" sh ./install.sh
[ "$("$staging/install/dev" version)" = "$version" ]
# Latest resolution and replacement of an existing binary.
INSTALL_DIR="$staging/install" VERSION=latest sh ./install.sh
[ "$("$staging/install/dev" version)" = "$version" ]
mkdir "$staging/bad"
cp dist/* "$staging/bad/"
for asset in "$staging/bad"/devstation_*; do printf 'corrupt' > "$asset"; done
if TEST_ASSETS="$staging/bad" INSTALL_DIR="$staging/install" VERSION="$version" sh ./install.sh; then
 echo 'installer accepted corrupt artifact' >&2; exit 1
fi
[ "$("$staging/install/dev" version)" = "$version" ]
if INSTALL_DIR="$staging/install" VERSION=../../invalid sh ./install.sh; then
 echo 'installer accepted invalid version' >&2; exit 1
fi
printf 'Installer tests passed.\n'
