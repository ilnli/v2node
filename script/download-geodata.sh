#!/usr/bin/env bash
set -euo pipefail
# Pin each release build to a reproducible rules dataset; update intentionally.
readonly geodata_tag=202610262207
readonly geodata_repo=Loyalsoldier/v2ray-rules-dat
output=${1:?用法: download-geodata.sh OUTPUT_DIR}
mkdir -p "$output"
for name in geoip.dat geosite.dat; do
    for asset in "$name" "$name.sha256sum"; do
        curl --fail --silent --show-error --location --retry 3 --retry-all-errors --connect-timeout 15 --max-time 300 --proto '=https' --tlsv1.2 \
            --output "$output/$asset" "https://github.com/$geodata_repo/releases/download/$geodata_tag/$asset"
    done
    (cd "$output" && sha256sum --check "$name.sha256sum")
done
printf '%s\n' "$geodata_repo $geodata_tag" > "$output/GEODATA_SOURCE.txt"
