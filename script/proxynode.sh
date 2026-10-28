#!/usr/bin/env bash
set -euo pipefail
fail() { echo "错误: $*" >&2; exit 1; }
usage() {
    cat <<'HELP'
用法: proxynode-manager COMMAND [OPTIONS]
  add-node     [--api-host URL] [--node-ids 101,102,103] [--api-key KEY]
  remove-node  [--api-host URL] [--node-id ID | --node-ids IDS]
  list-nodes
  config | start | stop | restart | status | enable | disable | log | version
  update [--version proxynode-vX.Y.Z] | install [安装参数]
节点命令支持 --config FILE，默认 /etc/proxynode/config.json。
--node-id 可以重复使用。add-node 缺少参数时交互输入，密钥不回显。
修改前校验并保留一份 config.json.bak；完成后执行 restart 确保生效。
HELP
}

manage_nodes() {
    local action=$1 config_file=/etc/proxynode/config.json api_host= api_key= value
    local -a node_ids=()
    shift
    while (( $# )); do
        case "$1" in
            --config|--api-host|--node-id|--node-ids|--api-key)
                [[ $# -ge 2 && -n $2 ]] || fail "$1 缺少参数"
                case "$1" in
                    --config) config_file=$2 ;;
                    --api-host) api_host=$2 ;;
                    --node-id|--node-ids) node_ids+=("$2") ;;
                    --api-key) api_key=$2 ;;
                esac
                shift 2 ;;
            -h|--help) usage; return ;;
            *) fail "未知参数: $1" ;;
        esac
    done
    if [[ $action == add-node && -z $api_host && -t 0 ]]; then
        read -r -p '面板 API 地址: ' api_host
    fi
    if [[ $action != list-nodes && ${#node_ids[@]} == 0 && -t 0 ]]; then
        read -r -p '节点 ID（多个用逗号分隔）: ' value
        node_ids+=("$value")
    fi
    if [[ $action == add-node && -z $api_key && -t 0 ]]; then
        read -r -s -p '节点通信密钥（不回显）: ' api_key
        echo
    fi
    # Pass the key on a private descriptor, not in the Python process arguments.
    python3 - "$action" "$config_file" "$api_host" "${node_ids[@]}" 3<<< "$api_key" <<'PY'
import fcntl
import json
import os
from pathlib import Path
import re
import sys
import tempfile
from urllib.parse import urlsplit

action, filename, host, *raw_ids = sys.argv[1:]
key = os.fdopen(3).read()[:-1]
path = Path(os.path.abspath(filename))


def normalize_host(value):
    parsed = urlsplit(value)
    if (parsed.scheme not in ('http', 'https') or not parsed.hostname
            or parsed.username is not None or parsed.password is not None
            or parsed.query or parsed.fragment or any(c.isspace() for c in value)):
        raise ValueError('面板地址必须是 HTTP(S) URL，不包含凭据、查询参数或片段')
    return value.rstrip('/')


def load_config():
    if path.is_symlink():
        raise ValueError('配置是符号链接，请通过 --config 指定实际文件')
    original = path.read_bytes()
    config = json.loads(original)
    if not isinstance(config, dict) or not isinstance(config.get('Nodes'), list):
        raise ValueError('配置必须包含 Nodes 数组')
    for node in config['Nodes']:
        if (not isinstance(node, dict) or type(node.get('NodeID')) is not int
                or node['NodeID'] <= 0 or not isinstance(node.get('ApiHost'), str)):
            raise ValueError('现有 Nodes 中存在无效的节点 ID 或面板地址')
        normalize_host(node['ApiHost'])
    return original, config


def atomic_write(target, data, owner):
    fd, temporary = tempfile.mkstemp(prefix='.' + target.name + '.', dir=target.parent)
    try:
        with os.fdopen(fd, 'wb') as output:
            os.fchmod(output.fileno(), 0o600)
            if os.geteuid() == 0:
                os.fchown(output.fileno(), owner.st_uid, owner.st_gid)
            output.write(data)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, target)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


try:
    if action == 'list-nodes':
        _, config = load_config()
        print('节点 ID\t面板 API 地址')
        for node in config['Nodes']:
            print(f"{node['NodeID']}\t{normalize_host(node['ApiHost'])}")
        if not config['Nodes']:
            print('尚未配置节点。')
        sys.exit(0)
    ids = []
    for item in raw_ids:
        for value in item.split(','):
            value = value.strip()
            if not re.fullmatch(r'[1-9][0-9]*', value):
                raise ValueError('节点 ID 必须为正整数，多个 ID 用逗号分隔')
            ids.append(int(value))
    if not ids or len(set(ids)) != len(ids):
        raise ValueError('请提供节点 ID，且同次操作不能包含重复 ID')
    if host:
        host = normalize_host(host)
    if action == 'add-node' and (not host or not key):
        raise ValueError('添加节点需要 --api-host、--node-id/--node-ids 和 --api-key，或在终端交互输入')
    # Lock a stable sibling file: atomic replacement changes the config inode.
    lock_fd = os.open(str(path) + '.lock', os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    with os.fdopen(lock_fd, 'w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        original, config = load_config()
        nodes = config['Nodes']
        if action == 'add-node':
            existing = {(normalize_host(n['ApiHost']), n['NodeID']) for n in nodes}
            if any((host, node_id) in existing for node_id in ids):
                raise ValueError('该面板下的节点 ID 已存在；没有修改配置')
            nodes.extend({'ApiHost': host, 'NodeID': node_id, 'ApiKey': key, 'Timeout': 15}
                         for node_id in ids)
        else:
            remove = set()
            for node_id in ids:
                matches = [i for i, n in enumerate(nodes) if n['NodeID'] == node_id
                           and (not host or normalize_host(n['ApiHost']) == host)]
                if len(matches) != 1:
                    raise ValueError('节点不存在或 ID 对应多个面板；请用 --api-host 明确指定')
                remove.add(matches[0])
            config['Nodes'] = [n for i, n in enumerate(nodes) if i not in remove]
        owner = path.stat()
        backup = Path(str(path) + '.bak')
        atomic_write(backup, original, owner)
        atomic_write(path, (json.dumps(config, ensure_ascii=False, indent=2) + '\n').encode(), owner)
    verb = '添加' if action == 'add-node' else '删除'
    print(f"已{verb}节点 {', '.join(map(str, ids))}；配置: {path}；备份: {backup}")
    print('请执行 proxynode-manager restart，确保运行中的服务加载新配置。')
except (OSError, ValueError) as exc:
    print(f'错误: {exc}', file=sys.stderr)
    sys.exit(1)
PY
}

case "${1:-help}" in
    add-node|remove-node|list-nodes) manage_nodes "$@" ;;
    help|-h|--help) usage ;;
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
    *) usage >&2; exit 1 ;;
esac
