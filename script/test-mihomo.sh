#!/usr/bin/env bash
set -euo pipefail

repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
version=v1.19.33
sha256=132f6f3edf9a65e62f04853aed3358472e0b2b7666daa8419edfa2ddf5bb6323
if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
  echo 'The pinned interoperability client is Linux amd64 only.' >&2
  exit 1
fi
cache=${MIHOMO_CACHE_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/proxynode-interop}
mkdir -p "$cache"
archive="$cache/mihomo-linux-amd64-$version.gz"
if [[ ! -f $archive ]]; then
  curl -4 -fL --retry 3 --connect-timeout 15 --max-time 180 \
    "https://github.com/MetaCubeX/mihomo/releases/download/$version/mihomo-linux-amd64-$version.gz" \
    -o "$archive.part"
  mv "$archive.part" "$archive"
fi
printf '%s  %s\n' "$sha256" "$archive" | sha256sum --check --status
client="$cache/mihomo-$version"
gzip -dc "$archive" > "$client"
chmod +x "$client"

build_dir=''
trap '[[ -z $build_dir ]] || rm -rf "$build_dir"' EXIT
binary=${PROXYNODE_BIN:-}
if [[ -z $binary ]]; then
  build_dir=$(mktemp -d)
  binary="$build_dir/proxynode"
  (cd "$repo" && GOEXPERIMENT=jsonv2 go build -o "$binary" .)
fi
python3 "$repo/tests/interop/mihomo.py" --proxynode "$binary" --mihomo "$client" "$@"
