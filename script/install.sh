#!/bin/sh
# Minimal POSIX bootstrap also works on hosts where bash has not been installed yet.
if [ -z "${BASH_VERSION:-}" ]; then
    if ! command -v bash >/dev/null 2>&1; then
        case " $* " in *' --prefix '*) echo '暂存安装需要预先安装 bash' >&2; exit 1;; esac
        [ "$(id -u)" = 0 ] || { echo '安装 bash 需要 root 权限' >&2; exit 1; }
        if command -v apk >/dev/null 2>&1; then apk add --no-cache bash
        elif command -v apt-get >/dev/null 2>&1; then apt-get update && apt-get install -y bash
        elif command -v dnf >/dev/null 2>&1; then dnf install -y bash
        elif command -v yum >/dev/null 2>&1; then yum install -y bash
        else echo '请先安装 bash' >&2; exit 1; fi
    fi
    exec bash "$0" "$@"
fi
set -euo pipefail

REPOSITORY=ilnli/v2node
VERSION=latest
PREFIX=
NO_SERVICE=false
MIGRATE=false
MIGRATE_MANAGED=false
API_HOST=
HOST_ID=
ENROLLMENT_TOKEN=
NODE_IDS=()
NODE_IDS_CSV=
API_KEY=
CONFIG_SOURCE=
NEEDS_CONFIG=false
MANAGED=false
CONVERT_CONFIG=false
INSTALL_TEMP=
INSTALL_BUNDLE=

cleanup_install() {
    local status=$?
    trap - EXIT HUP INT TERM
    # EXIT can run after main's local variables have gone out of scope.
    if [[ -n $INSTALL_TEMP ]]; then rm -rf -- "$INSTALL_TEMP" || true; fi
    # Never remove a published release or a candidate owned by the upgrade guardian.
    if [[ -n $INSTALL_BUNDLE && ! -e "$STATE_DIR/pending-upgrade.json" &&
          $(readlink -f "$STATE_DIR/current" 2>/dev/null) != "$INSTALL_BUNDLE" ]]; then
        rm -rf -- "$INSTALL_BUNDLE" || true
    fi
    return "$status"
}

fail() { echo "错误: $*" >&2; exit 1; }
usage() {
    cat <<'HELP'
用法: install.sh [--version proxynode-vX.Y.Z] [--config FILE]
                  [--api-host URL --host-id ID --enrollment-token TOKEN]
                  [--api-host URL --node-ids 101,102,103 --api-key KEY]
                  [--migrate-v2node] [--migrate-managed] [--no-service] [--prefix DIR]
只从 ilnli/v2node 的 GitHub Release 下载并校验完整发布包。
托管模式首次接入后，通过面板添加节点、配置证书和更新程序。
已有独立节点配置转为托管模式需 --migrate-managed，并保留私密配置备份。
默认保留已有配置；--migrate-v2node 用于显式迁移旧 v2node 配置。
--prefix 必须配合 --no-service，不修改真实系统服务或安装系统依赖。
HELP
}
set_paths() {
    BIN_DIR="$PREFIX/usr/local/bin"
    CONFIG_DIR="$PREFIX/etc/proxynode"
    STATE_DIR="$PREFIX/var/lib/proxynode"
}
ensure_dependencies() {
    local missing=false tool
    for tool in curl tar sha256sum python3 install; do command -v "$tool" >/dev/null || missing=true; done
    [[ $missing == true ]] || return 0
    [[ -z $PREFIX ]] || fail "暂存安装需要预先安装 curl、tar、python3 和 coreutils"
    if command -v apt-get >/dev/null; then
        apt-get update
        DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates curl tar python3 coreutils
    elif command -v dnf >/dev/null; then dnf install -y ca-certificates curl tar python3 coreutils
    elif command -v yum >/dev/null; then yum install -y ca-certificates curl tar python3 coreutils
    elif command -v apk >/dev/null; then apk add --no-cache ca-certificates curl tar python3 coreutils
    else fail "无法自动安装依赖，请安装 curl、tar、python3 和 coreutils"; fi
    for tool in curl tar sha256sum python3 install; do command -v "$tool" >/dev/null || fail "缺少依赖: $tool"; done
}
download() {
    curl --fail --show-error --location --retry 3 --connect-timeout 15 --max-time 300 \
        --max-filesize 268435456 --proto '=https' --tlsv1.2 --output "$2" "$1"
}
publish_bundle() {
    python3 - "$STATE_DIR" "$1" "$CONFIG_DIR/config.json" "$NO_SERVICE" "$CONVERT_CONFIG" <<'PYPUBLISH'
import json, os, pathlib, re, subprocess, sys, tempfile, time
root,bundle,config,staging,converting=sys.argv[1:]
root,bundle=pathlib.Path(root),pathlib.Path(bundle)
current=root/'current'
if current.exists() and not current.is_symlink(): sys.exit('发布路径已被其他文件占用')
if (root/'pending-upgrade.json').exists(): sys.exit('上一轮升级尚未完成，保留当前发布版本')
previous=current.resolve() if current.exists() else None
if previous and staging == 'false' and converting == 'false' and json.load(open(config)).get('Agent'):
    # The embedded version is needed for the guardian's PID/version startup handshake.
    output=subprocess.check_output([str(bundle/'proxynode'),'version'],text=True,timeout=15)
    match=re.search(r'\bproxynode-v[0-9]+\.[0-9]+\.[0-9]+\b',output)
    if not match: sys.exit('发布包缺少有效版本，无法启用升级回滚保护')
    (root/'upgrade-result.json').unlink(missing_ok=True)
    fd,path=tempfile.mkstemp(prefix='.pending-upgrade-',dir=root)
    try:
        with os.fdopen(fd,'w') as target:
            json.dump({'previous':str(previous),'candidate':str(bundle),'version':match.group(),
                       'started_at':int(time.time())},target)
            target.flush();os.fsync(target.fileno())
        os.link(path,root/'pending-upgrade.json')
    finally: os.unlink(path)
temporary=root/('.current-install-'+str(os.getpid()))
os.symlink(bundle,temporary);os.replace(temporary,current)
PYPUBLISH
}
install_service() {
    local manager=$1
    if [[ $manager == systemd ]]; then
        install -d "$PREFIX/etc/systemd/system"
        cat > "$PREFIX/etc/systemd/system/proxynode.service" <<UNIT
[Unit]
Description=Proxynode supervised service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=$CONFIG_DIR
ExecStart=$STATE_DIR/supervise.sh --root $STATE_DIR --config $CONFIG_DIR/config.json
Restart=on-failure
RestartSec=5
TimeoutStopSec=140
LimitNOFILE=1048576
UMask=0077

[Install]
WantedBy=multi-user.target
UNIT
        systemctl daemon-reload
        if [[ $NEEDS_CONFIG == false ]]; then
            if [[ $MIGRATE == true ]] && systemctl cat v2node.service >/dev/null 2>&1; then systemctl disable --now v2node; fi
            systemctl enable proxynode
            systemctl restart proxynode
        fi
    else
        install -d "$PREFIX/etc/init.d"
        cat > "$PREFIX/etc/init.d/proxynode" <<UNIT
#!/sbin/openrc-run
name="proxynode"
description="Proxynode supervised service"
command="$STATE_DIR/supervise.sh"
command_args="--root $STATE_DIR --config $CONFIG_DIR/config.json"
command_background="yes"
pidfile="/run/proxynode.pid"
directory="$CONFIG_DIR"
retry="TERM/140/KILL/5"
depend() { need net; }
UNIT
        chmod 755 "$PREFIX/etc/init.d/proxynode"
        if [[ $NEEDS_CONFIG == false ]]; then
            if [[ $MIGRATE == true && -f "$PREFIX/etc/init.d/v2node" ]]; then rc-service v2node stop; rc-update del v2node default; fi
            rc-update add proxynode default
            rc-service proxynode restart
        fi
    fi
}
main() {
    while (( $# )); do
        case "$1" in
            --version|--prefix|--api-host|--host-id|--enrollment-token|--node-id|--node-ids|--api-key|--config)
                [[ $# -ge 2 && -n $2 ]] || fail "$1 缺少参数"
                case "$1" in
                    --version) VERSION=$2 ;; --prefix) PREFIX=${2%/} ;; --api-host) API_HOST=$2 ;;
                    --host-id) HOST_ID=$2 ;; --enrollment-token) ENROLLMENT_TOKEN=$2 ;;
                    --node-id|--node-ids) NODE_IDS+=("$2") ;; --api-key) API_KEY=$2 ;; --config) CONFIG_SOURCE=$2 ;;
                esac
                shift 2 ;;
            --no-service) NO_SERVICE=true; shift ;;
            --migrate-v2node) MIGRATE=true; shift ;;
            --migrate-managed) MIGRATE_MANAGED=true; shift ;;
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
    case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) fail "不支持的 CPU 架构" ;; esac
    set_paths
    ensure_dependencies
    if [[ -n $HOST_ID || -n $ENROLLMENT_TOKEN ]]; then
        MANAGED=true
        [[ -n $API_HOST && $HOST_ID =~ ^[1-9][0-9]*$ && $ENROLLMENT_TOKEN =~ ^pn_enroll_[A-Za-z0-9_-]{20,128}$ ]] || fail "需要有效的 api-host/host-id/enrollment-token"
        [[ ${#NODE_IDS[@]} == 0 && -z $API_KEY && -z $CONFIG_SOURCE ]] || fail "托管接入不能同时指定节点令牌、节点 ID 或配置文件"
    elif [[ -n $API_HOST || ${#NODE_IDS[@]} -gt 0 || -n $API_KEY ]]; then
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
    if [[ -n $API_HOST ]]; then
        python3 - "$API_HOST" <<'PYURL'
import sys
from urllib.parse import urlsplit
value = sys.argv[1]
u = urlsplit(value)
if u.scheme not in ('http', 'https') or not u.hostname or u.username or u.password or u.query or u.fragment or any(c.isspace() for c in value):
    sys.exit('面板地址必须是 HTTP(S) URL，不包含凭据、查询参数或片段')
PYURL
    fi
    [[ $MIGRATE_MANAGED == false || $MANAGED == true ]] || fail "--migrate-managed 需要完整的托管接入参数"
    if [[ -e "$BIN_DIR/pnode" || -L "$BIN_DIR/pnode" ]]; then
        [[ -L "$BIN_DIR/pnode" && ( $(readlink "$BIN_DIR/pnode") == proxynode-manager || $(readlink "$BIN_DIR/pnode") == "$BIN_DIR/proxynode-manager" ) ]] || fail "pnode 已被其他程序占用: $BIN_DIR/pnode"
    fi
    if [[ -f "$PREFIX/etc/v2node/config.json" && ! -f "$CONFIG_DIR/config.json" && $MIGRATE == false ]]; then fail "发现旧 v2node 配置，请使用 --migrate-v2node 显式迁移"; fi
    if [[ $MANAGED == true && -f "$CONFIG_DIR/config.json" ]]; then
        local existing
        existing=$(python3 - "$CONFIG_DIR/config.json" "$API_HOST" "$HOST_ID" <<'PYCURRENT'
import json,sys
config=json.load(open(sys.argv[1]));agent=config.get('Agent')
if not agent: print('legacy')
elif str(agent.get('HostID')) != sys.argv[3] or agent.get('ApiHost','').rstrip('/') != sys.argv[2].rstrip('/'):
    sys.exit('现有托管身份属于另一台主机；请先在面板中处理原主机身份，不会覆盖当前配置')
else: print('managed')
PYCURRENT
        ) || fail "现有配置无法转换"
        if [[ $existing == legacy ]]; then
            [[ $MIGRATE_MANAGED == true ]] || fail "已有节点配置；请使用 --migrate-managed 显式转换并保留备份"
            CONVERT_CONFIG=true
        fi
    fi
    local manager=
    if [[ $NO_SERVICE == false ]]; then
        if command -v systemctl >/dev/null && [[ -d /run/systemd/system ]]; then manager=systemd
        elif command -v rc-service >/dev/null; then manager=openrc
        else fail "未发现 systemd/OpenRC；可使用 --no-service 安装后手动运行"; fi
    fi
    local temp archive base expected
    INSTALL_TEMP=$(mktemp -d "${TMPDIR:-/tmp}/proxynode-install.XXXXXXXX")
    temp=$INSTALL_TEMP
    trap cleanup_install EXIT
    trap 'exit 129' HUP
    trap 'exit 130' INT
    trap 'exit 143' TERM
    archive="proxynode-linux-$arch.tar.gz"
    base="https://github.com/$REPOSITORY/releases"
    if [[ $VERSION == latest ]]; then base+="/latest/download"; else base+="/download/$VERSION"; fi
    download "$base/$archive" "$temp/$archive"
    download "$base/SHA256SUMS" "$temp/SHA256SUMS"
    [[ $(wc -c < "$temp/SHA256SUMS") -le 65536 ]] || fail "校验文件过大"
    expected=$(awk -v name="$archive" '$2 == name { print $1 }' "$temp/SHA256SUMS")
    [[ $expected =~ ^[0-9a-fA-F]{64}$ ]] || fail "SHA256SUMS 中缺少有效的 $archive 校验值"
    (cd "$temp" && printf '%s  %s\n' "$expected" "$archive" | sha256sum --check --status) || fail "SHA-256 校验失败；保留旧安装"
    python3 - "$temp/$archive" "$temp" <<'PYEXTRACT'
import os, pathlib, shutil, sys, tarfile
source, destination = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2])
required = {'proxynode','proxynode-manager','install.sh','supervise.sh','config.example.json','geoip.dat','geosite.dat'}
allowed = required | {'LICENSE','NOTICE.md','README.md','GEODATA_SOURCE.txt'}
if source.stat().st_size > 256*1024**2: sys.exit('发布包过大')
with tarfile.open(source, 'r:gz') as archive:
    seen, size = set(), 0
    for member in archive:
        if member.name not in allowed or member.name in seen or not member.isreg() or member.size < 0:
            sys.exit('发布包包含不安全的文件')
        size += member.size
        if member.size > 256*1024**2 or size > 512*1024**2: sys.exit('解压数据过大')
        seen.add(member.name)
        with archive.extractfile(member) as src, (destination/member.name).open('xb') as dst:
            shutil.copyfileobj(src,dst,65536)
    if not required <= seen: sys.exit('发布包缺少必要文件')
PYEXTRACT
    if [[ ! -f "$CONFIG_DIR/config.json" || $CONVERT_CONFIG == true ]]; then
        if [[ -n $CONFIG_SOURCE ]]; then cp -- "$CONFIG_SOURCE" "$temp/new-config.json"
        elif [[ $MIGRATE == true && -f "$PREFIX/etc/v2node/config.json" && $MANAGED == false ]]; then cp -- "$PREFIX/etc/v2node/config.json" "$temp/new-config.json"
        else
            [[ -n $API_HOST ]] || NEEDS_CONFIG=true
            python3 - "$temp/config.example.json" "$temp/new-config.json" "$API_HOST" "$NODE_IDS_CSV" "$HOST_ID" "$STATE_DIR/managed" 3<<< "$API_KEY" 4<<< "$ENROLLMENT_TOKEN" <<'PYCONFIG'
import json, os, sys
src, dst, host, nodes, host_id, directory = sys.argv[1:]
key, enrollment = os.fdopen(3).read()[:-1], os.fdopen(4).read()[:-1]
config=json.load(open(src));config.setdefault('Audit',{})['Enabled']=False;config['Nodes']=[];config.pop('Agent',None)
if host_id:
    config['Agent']={'ApiHost':host.rstrip('/'),'HostID':int(host_id),'EnrollmentToken':enrollment,'Directory':directory}
elif host:
    config['Nodes']=[{'ApiHost':host.rstrip('/'),'NodeID':int(value),'ApiKey':key,'Timeout':15} for value in nodes.split(',')]
with open(dst,'w') as f:json.dump(config,f,ensure_ascii=False,indent=2)
PYCONFIG
        fi
        python3 -m json.tool "$temp/new-config.json" >/dev/null
    fi
    install -d "$BIN_DIR"
    install -d -m 700 "$CONFIG_DIR" "$STATE_DIR" "$STATE_DIR/audit" "$STATE_DIR/releases"
    local bundle
    INSTALL_BUNDLE=$(mktemp -d "$STATE_DIR/releases/$VERSION-XXXXXXXX")
    bundle=$INSTALL_BUNDLE
    for file in proxynode proxynode-manager install.sh supervise.sh; do install -m 755 "$temp/$file" "$bundle/$file"; done
    for file in config.example.json geoip.dat geosite.dat; do install -m 644 "$temp/$file" "$bundle/$file"; done
    for file in LICENSE NOTICE.md README.md GEODATA_SOURCE.txt; do [[ ! -f "$temp/$file" ]] || install -m 644 "$temp/$file" "$bundle/$file"; done
    if [[ $CONVERT_CONFIG == true ]]; then install -m 600 "$CONFIG_DIR/config.json" "$CONFIG_DIR/config.json.legacy-$(date +%Y%m%d%H%M%S)-$$"; fi
    if [[ $MIGRATE == true && $MANAGED == true && -f "$PREFIX/etc/v2node/config.json" ]]; then install -m 600 "$PREFIX/etc/v2node/config.json" "$CONFIG_DIR/config.json.legacy-$(date +%Y%m%d%H%M%S)-$$"; fi
    if [[ -f "$temp/new-config.json" ]]; then install -m 600 "$temp/new-config.json" "$CONFIG_DIR/config.json"; fi
    # Keep the guardian fixed across remote upgrades. Each bundle carries its copy for installation/recovery.
    [[ -f "$STATE_DIR/supervise.sh" ]] || install -m 755 "$bundle/supervise.sh" "$STATE_DIR/supervise.sh"
    publish_bundle "$bundle"
    ln -sfn "$STATE_DIR/current/proxynode" "$BIN_DIR/proxynode"
    ln -sfn "$STATE_DIR/current/proxynode-manager" "$BIN_DIR/proxynode-manager"
    ln -sfn proxynode-manager "$BIN_DIR/pnode"
    ln -sfn "$STATE_DIR/current/install.sh" "$STATE_DIR/install.sh"
    ln -sfn "$STATE_DIR/current/geoip.dat" "$CONFIG_DIR/geoip.dat"
    ln -sfn "$STATE_DIR/current/geosite.dat" "$CONFIG_DIR/geosite.dat"
    if [[ $NO_SERVICE == false ]]; then install_service "$manager"; fi
    echo "已从 $REPOSITORY 安装 $archive；配置: $CONFIG_DIR/config.json"
    if [[ $NEEDS_CONFIG == true ]]; then echo '请先执行 pnode add 添加节点，再执行: pnode enable && pnode start'
    elif [[ $MANAGED == true ]]; then echo '托管模式已配置；主机接入后，请在面板分配节点和证书。'; fi
    cleanup_install
}
if [[ ${BASH_SOURCE[0]} == "$0" ]]; then main "$@"; fi
