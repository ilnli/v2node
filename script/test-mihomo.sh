#!/usr/bin/env bash
set -euo pipefail

repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
version=v1.19.34
sha256=223fab877e5795ce780ba39902e5f0b1113c1256ae244a7160f1165e017e9ccc
source=${MIHOMO_SOURCE:-official}
openclash_ref=5810424b40be9324db7595514882f324c2258798
case "$source" in
  official) ;;
  openclash-master-meta)
    version=alpha-gbc76c13
    sha256=367130a1f3a99fbcd1a6acc1f2a5fc53ce43bef5af4f3278854de27694ccfa0c ;;
  openclash-dev-meta)
    version=alpha-g184857c
    sha256=a4ad618f50a64a0c6f414ddd19e808e122efe5fd8e54617d1f3efaa8e64f233b ;;
  openclash-master-smart)
    version=alpha-smart-gdaf3ad0
    sha256=dbcff108ee8b83cbeeaf31cab0ba1e2f670e55765d8affc36df7faaf7400abc4 ;;
  openclash-dev-smart)
    version=alpha-smart-gc4df732
    sha256=dd12465b256ec8a40bebba2774a8caaeea809308c85cf577dd1ffe34433901ca ;;
  *) echo "Unknown pinned Mihomo source: $source" >&2; exit 2 ;;
esac
if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
  echo 'The pinned interoperability client is Linux amd64 only.' >&2
  exit 1
fi
cache=${MIHOMO_CACHE_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/proxynode-interop}
mkdir -p "$cache"
if [[ $source == official ]]; then
  archive="$cache/mihomo-linux-amd64-$version.gz"
  url="https://github.com/MetaCubeX/mihomo/releases/download/$version/mihomo-linux-amd64-$version.gz"
else
  channel=${source#openclash-}
  channel=${channel/-/\/}
  archive="$cache/$source-$openclash_ref.tar.gz"
  url="https://raw.githubusercontent.com/vernesong/OpenClash/$openclash_ref/$channel/clash-linux-amd64-compatible.tar.gz"
fi
if [[ ! -f $archive ]]; then
  curl -4 -fL --retry 3 --connect-timeout 15 --max-time 180 \
    "$url" \
    -o "$archive.part"
  mv "$archive.part" "$archive"
fi
printf '%s  %s\n' "$sha256" "$archive" | sha256sum --check --status
client="$cache/$source-$version"
preparation=original
if [[ $source == official ]]; then
  gzip -dc "$archive" > "$client"
else
  tar -xOzf "$archive" clash > "$client"
  # The distributed UPX 3.95 loader can exit 127 before Mihomo starts on CI.
  # Unpack the same verified client instead of retrying or skipping failed cases.
  upx_version=5.3.0
  upx_sha256=79cea8a50b6fa102d10c7b79a419cd77dead3cc2d78661de2dc5e5815dc505c1
  upx_archive="$cache/upx-$upx_version-amd64_linux.tar.xz"
  if [[ ! -f $upx_archive ]]; then
    curl -4 -fL --retry 3 --connect-timeout 15 --max-time 180 \
      "https://github.com/upx/upx/releases/download/v$upx_version/upx-$upx_version-amd64_linux.tar.xz" \
      -o "$upx_archive.part"
    mv "$upx_archive.part" "$upx_archive"
  fi
  printf '%s  %s\n' "$upx_sha256" "$upx_archive" | sha256sum --check --status
  upx="$cache/upx-$upx_version"
  tar -xOJf "$upx_archive" "upx-$upx_version-amd64_linux/upx" > "$upx"
  chmod +x "$upx"
  "$upx" -d "$client"
  preparation="upx-$upx_version-decompressed"
fi
chmod +x "$client"

build_dir=''
trap '[[ -z $build_dir ]] || rm -rf "$build_dir"' EXIT
binary=${PROXYNODE_BIN:-}
if [[ -z $binary ]]; then
  build_dir=$(mktemp -d)
  binary="$build_dir/proxynode"
  (cd "$repo" && GOEXPERIMENT=jsonv2 go build -o "$binary" .)
fi
xray=${XRAY_BIN:-}
if [[ -z $xray ]]; then
  [[ -n $build_dir ]] || build_dir=$(mktemp -d)
  xray="$build_dir/xray"
  (cd "$repo" && GOEXPERIMENT=jsonv2 go build -o "$xray" github.com/xtls/xray-core/main)
fi
python3 "$repo/tests/interop/mihomo.py" --proxynode "$binary" --mihomo "$client" --xray "$xray" \
  --expected-version "$version" --client-source "$url" --client-archive-sha256 "$sha256" \
  --client-preparation "$preparation" "$@"
