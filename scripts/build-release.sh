#!/bin/sh
set -eu
version=${1:?usage: scripts/build-release.sh vX.Y.Z}
printf '%s\n' "$version" | LC_ALL=C grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || { echo 'invalid release version' >&2; exit 1; }
mkdir -p dist
for arch in amd64 arm64; do
  CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$version" -o "dist/devstation_${version}_linux_${arch}" ./cmd/dev
done
(cd dist && shasum -a 256 "devstation_${version}_linux_amd64" "devstation_${version}_linux_arm64" > checksums.txt)
