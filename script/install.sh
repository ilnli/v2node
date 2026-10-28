#!/usr/bin/env bash
set -euo pipefail

REPOSITORY=ilnli/v2node
VERSION=latest
PREFIX=
NO_SERVICE=false
MIGRATE=false
API_HOST=
NODE_IDS=()
NODE_IDS_CSV=
API_KEY=
CONFIG_SOURCE=
NEEDS_CONFIG=false

fail() { echo "错误: $*" >&2; exit 1; }
usage() {
    cat <<'HELP'
用法: install.sh [--version proxynode-vX.Y.Z] [--config FILE]
                  [--api-host URL --node-ids 101,102,103 --api-key KEY]
                  [--migrate-v2node] [--no-service] [--prefix DIR]
只从 ilnli/v2node 的 GitHub Release 下载并校验二进制。
默认保留已有 /etc/proxynode/config.json；迁移旧 v2node 必须显式指定。
--node-id 可重复使用；已有配置请用 pnode add 追加节点。
--prefix 必须配合 --no-service，可用于暂存安装而不修改系统服务。
HELP
}

set_paths() {
    BIN_DIR="$PREFIX/usr/local/bin"
    CONFIG_DIR="$PREFIX/etc/proxynode"
    STATE_DIR="$PREFIX/var/lib/proxynode"
}

download() {
    curl --fail --show-error --location --retry 3 --connect-timeout 15 --max-time 300 \
        --proto '=https' --tlsv1.2 --output "$2" "$1"
}

install_service() {
    local manager=$1
    if [[ $manager == systemd ]]; then
        install -d "$PREFIX/etc/systemd/system"
        cat > "$PREFIX/etc/systemd/system/proxynode.service" <<UNIT
[Unit]
Description=Proxynode service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=$CONFIG_DIR
Environment=XRAY_LOCATION_ASSET=$CONFIG_DIR
ExecStart=$BIN_DIR/proxynode server --config $CONFIG_DIR/config.json
Restart=on-failure
RestartSec=5
TimeoutStopSec=120
LimitNOFILE=1048576
UMask=0077

[Install]
WantedBy=multi-user.target
UNIT
        systemctl daemon-reload
        if [[ $NEEDS_CONFIG == false ]]; then
            if [[ $MIGRATE == true ]] && systemctl cat v2node.service >/dev/null 2>&1; then
                systemctl disable --now v2node
            fi
            systemctl enable proxynode
            systemctl restart proxynode
        fi
    else
        install -d "$PREFIX/etc/init.d"
        cat > "$PREFIX/etc/init.d/proxynode" <<UNIT
#!/sbin/openrc-run
name="proxynode"
description="Proxynode service"
command="$BIN_DIR/proxynode"
command_args="server --config $CONFIG_DIR/config.json"
command_background="yes"
pidfile="/run/proxynode.pid"
directory="$CONFIG_DIR"
export XRAY_LOCATION_ASSET="$CONFIG_DIR"
retry="TERM/120/KILL/5"
depend() { need net; }
UNIT
        chmod 755 "$PREFIX/etc/init.d/proxynode"
        if [[ $NEEDS_CONFIG == false ]]; then
            if [[ $MIGRATE == true && -f "$PREFIX/etc/init.d/v2node" ]]; then
                rc-service v2node stop
                rc-update del v2node default
            fi
            rc-update add proxynode default
            rc-service proxynode restart
        fi
    fi
}

main() {
    while (( $# )); do
        case "$1" in
            --version|--prefix|--api-host|--node-id|--node-ids|--api-key|--config)
                [[ $# -ge 2 && -n $2 ]] || fail "$1 缺少参数"
                case "$1" in
                    --version) VERSION=$2 ;;
                    --prefix) PREFIX=${2%/} ;;
                    --api-host) API_HOST=$2 ;;
                    --node-id|--node-ids) NODE_IDS+=("$2") ;;
                    --api-key) API_KEY=$2 ;;
                    --config) CONFIG_SOURCE=$2 ;;
                esac
                shift 2 ;;
            --no-service) NO_SERVICE=true; shift ;;
            --migrate-v2node) MIGRATE=true; shift ;;
            -h|--help) usage; return ;;
            *) fail "未知参数: $1" ;;
        esac
    done
    [[ $VERSION == latest || $VERSION =~ ^proxynode-v[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "版本格式应为 proxynode-vX.Y.Z"
    [[ -z $PREFIX || $PREFIX == /* ]] || fail "--prefix 必须是绝对路径"
    [[ -z $PREFIX || $NO_SERVICE == true ]] || fail "--prefix 必须配合 --no-service"
    [[ -n $PREFIX || $EUID == 0 ]] || fail "系统安装需要 root 权限"
    [[ $(uname -s) == Linux ]] || fail "部署脚本仅支持 Linux"
    local arch
    case "$(uname -m)" in
        x86_64|amd64) arch=amd64 ;;
        aarch64|arm64) arch=arm64 ;;
        *) fail "不支持的 CPU 架构" ;;
    esac
    for tool in curl tar sha256sum python3 install; do
        command -v "$tool" >/dev/null || fail "缺少依赖: $tool"
    done
    if [[ -n $API_HOST || ${#NODE_IDS[@]} -gt 0 || -n $API_KEY ]]; then
        [[ -n $API_HOST && ${#NODE_IDS[@]} -gt 0 && -n $API_KEY ]] || fail "需要完整的 api-host/node-id/api-key"
        NODE_IDS_CSV=$(python3 - "${NODE_IDS[@]}" <<'PYIDS'
import re, sys
ids = [part.strip() for arg in sys.argv[1:] for part in arg.split(',')]
if any(not re.fullmatch(r'[1-9][0-9]*', item) for item in ids) or len(set(ids)) != len(ids):
    sys.exit('节点 ID 必须是互不重复的正整数')
print(','.join(ids))
PYIDS
        ) || fail "无效节点列表"
        [[ -z $CONFIG_SOURCE ]] || fail "--config 与面板参数不能同时使用"
    fi
    set_paths
    if [[ -e "$BIN_DIR/pnode" || -L "$BIN_DIR/pnode" ]]; then
        [[ -L "$BIN_DIR/pnode" && ( $(readlink "$BIN_DIR/pnode") == proxynode-manager || $(readlink "$BIN_DIR/pnode") == "$BIN_DIR/proxynode-manager" ) ]] || fail "pnode 已被其他程序占用: $BIN_DIR/pnode"
    fi
    if [[ -f "$PREFIX/etc/v2node/config.json" && ! -f "$CONFIG_DIR/config.json" && $MIGRATE == false ]]; then
        fail "发现旧 v2node 配置，请使用 --migrate-v2node 显式迁移"
    fi
    local manager=
    if [[ $NO_SERVICE == false ]]; then
        if command -v systemctl >/dev/null && [[ -d /run/systemd/system ]]; then
            manager=systemd
        elif command -v rc-service >/dev/null; then
            manager=openrc
        else
            fail "未发现 systemd/OpenRC；可使用 --no-service 安装后手动运行"
        fi
    fi
    local temp archive base expected
    temp=$(mktemp -d)
    trap 'rm -rf -- "$temp"' EXIT
    archive="proxynode-linux-$arch.tar.gz"
    base="https://github.com/$REPOSITORY/releases"
    if [[ $VERSION == latest ]]; then base+="/latest/download"; else base+="/download/$VERSION"; fi
    download "$base/$archive" "$temp/$archive"
    download "$base/SHA256SUMS" "$temp/SHA256SUMS"
    expected=$(awk -v name="$archive" '$2 == name { print $1 }' "$temp/SHA256SUMS")
    [[ $expected =~ ^[0-9a-fA-F]{64}$ ]] || fail "SHA256SUMS 中缺少有效的 $archive 校验值"
    (cd "$temp" && printf '%s  %s\n' "$expected" "$archive" | sha256sum --check --status) || fail "SHA-256 校验失败；保留旧安装"
    # Extract only the fixed files used by this installer from the verified archive.
    tar -xzf "$temp/$archive" -C "$temp" --no-same-owner \
        proxynode proxynode-manager install.sh config.example.json geoip.dat geosite.dat
    [[ -f "$temp/proxynode" && ! -L "$temp/proxynode" ]] || fail "发布包缺少二进制"
    if [[ ! -f "$CONFIG_DIR/config.json" ]]; then
        if [[ -n $CONFIG_SOURCE ]]; then
            cp -- "$CONFIG_SOURCE" "$temp/new-config.json"
        elif [[ $MIGRATE == true && -f "$PREFIX/etc/v2node/config.json" ]]; then
            cp -- "$PREFIX/etc/v2node/config.json" "$temp/new-config.json"
        else
            [[ -n $API_HOST ]] || NEEDS_CONFIG=true
            python3 - "$temp/config.example.json" "$temp/new-config.json" "$API_HOST" "$NODE_IDS_CSV" "$API_KEY" <<'PY'
import json, sys
src, dst, host, node, key = sys.argv[1:]
with open(src) as f: config = json.load(f)
config.setdefault('Audit', {})['Enabled'] = False
config['Nodes'] = []
if host:
    config['Nodes'] = [{'ApiHost': host.rstrip('/'), 'NodeID': int(value), 'ApiKey': key, 'Timeout': 15} for value in node.split(',')]
with open(dst, 'w') as f: json.dump(config, f, ensure_ascii=False, indent=2)
PY
        fi
        python3 -m json.tool "$temp/new-config.json" >/dev/null
    fi
    install -d "$BIN_DIR"
    install -d -m 700 "$CONFIG_DIR" "$STATE_DIR" "$STATE_DIR/audit"
    if [[ -f "$temp/new-config.json" ]]; then install -m 600 "$temp/new-config.json" "$CONFIG_DIR/config.json"; fi
    install -m 755 "$temp/proxynode" "$BIN_DIR/proxynode.new"
    mv -f "$BIN_DIR/proxynode.new" "$BIN_DIR/proxynode"
    install -m 755 "$temp/proxynode-manager" "$BIN_DIR/proxynode-manager"
    ln -sfn proxynode-manager "$BIN_DIR/pnode"
    install -m 755 "$temp/install.sh" "$STATE_DIR/install.sh"
    install -m 644 "$temp/geoip.dat" "$temp/geosite.dat" "$CONFIG_DIR/"
    if [[ $NO_SERVICE == false ]]; then install_service "$manager"; fi
    if [[ ! -f "$temp/new-config.json" && ${#NODE_IDS[@]} -gt 0 ]]; then
        echo '已有配置已保留；如需添加节点，请执行 pnode add。'
    fi
    echo "已从 $REPOSITORY 安装 $archive；配置: $CONFIG_DIR/config.json"
    if [[ $NEEDS_CONFIG == true ]]; then
        echo "请先执行 pnode add 添加节点，再执行: pnode enable && pnode start"
    fi
    rm -rf -- "$temp"
    trap - EXIT
}

if [[ ${BASH_SOURCE[0]} == "$0" ]]; then main "$@"; fi
