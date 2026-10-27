#!/usr/bin/env bash
set -euo pipefail
version=${1:?用法: package.sh VERSION ARCH OUTPUT_DIR GEODATA_DIR}
arch=${2:?需要架构}
output=${3:?需要输出目录}
geodata=${4:?需要 geodata 目录}
[[ $version =~ ^proxynode-v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo '无效发布标签' >&2; exit 1; }
[[ $arch == amd64 || $arch == arm64 ]] || { echo '不支持的架构' >&2; exit 1; }
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
stage=$(mktemp -d)
trap 'rm -rf -- "$stage"' EXIT
mkdir -p "$output"
# Limit parallelism for small CI machines; default Go build cache remains enabled.
(cd "$root" && GOOS=linux GOARCH="$arch" CGO_ENABLED=0 GOEXPERIMENT=jsonv2 \
    go build -p "${BUILD_JOBS:-4}" -trimpath \
    -ldflags "-s -w -X github.com/wyx2685/v2node/cmd.version=$version" -o "$stage/proxynode" .)
cp "$root/script/proxynode.sh" "$stage/proxynode-manager"
cp "$root/script/install.sh" "$stage/install.sh"
cp "$root/examples/config.json" "$stage/config.example.json"
cp "$root/LICENSE" "$root/NOTICE.md" "$root/README.md" "$stage/"
cp "$geodata/geoip.dat" "$geodata/geosite.dat" "$geodata/GEODATA_SOURCE.txt" "$stage/"
chmod 755 "$stage/proxynode" "$stage/proxynode-manager" "$stage/install.sh"
tar -czf "$output/proxynode-linux-$arch.tar.gz" -C "$stage" \
    proxynode proxynode-manager install.sh config.example.json LICENSE NOTICE.md README.md \
    geoip.dat geosite.dat GEODATA_SOURCE.txt
(cd "$output" && sha256sum "proxynode-linux-$arch.tar.gz" > "proxynode-linux-$arch.tar.gz.sha256")
