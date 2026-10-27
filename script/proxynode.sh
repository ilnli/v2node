#!/usr/bin/env bash
set -euo pipefail
case "${1:-help}" in
    update|install)
        shift
        # Bootstrap updates from our main branch; install.sh fetches only our releases.
        temp=$(mktemp)
        trap 'rm -f -- "$temp"' EXIT
        curl --fail --show-error --location --retry 3 --proto '=https' --tlsv1.2 \
            --output "$temp" https://raw.githubusercontent.com/ilnli/v2node/main/script/install.sh
        bash "$temp" "$@"
        ;;
    version) /usr/local/bin/proxynode version ;;
    config) "${EDITOR:-vi}" /etc/proxynode/config.json ;;
    start|stop|restart|status|enable|disable)
        if command -v systemctl >/dev/null && [[ -d /run/systemd/system ]]; then
            systemctl "$1" proxynode
        else
            case "$1" in
                enable) rc-update add proxynode default ;;
                disable) rc-update del proxynode default ;;
                *) rc-service proxynode "$1" ;;
            esac
        fi
        ;;
    log)
        if command -v journalctl >/dev/null && [[ -d /run/systemd/system ]]; then
            journalctl -u proxynode -n 100 -f
        else
            echo 'OpenRC 日志请配置 Log.Output 或使用系统日志服务。'
        fi
        ;;
    *) echo '用法: proxynode-manager {start|stop|restart|status|enable|disable|log|config|version|update [--version proxynode-vX.Y.Z]}' ;;
esac
