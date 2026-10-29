#!/usr/bin/env bash
set -euo pipefail
fail() { echo "错误: $*" >&2; exit 1; }
usage() {
    cat <<'HELP'
用法: pnode [命令]
  不带参数      打开中文菜单（终端中）
  add [ID,...]  添加节点，按提示输入面板和密钥
  list          查看节点
  del [ID,...]  删除节点
  restart       重启服务，使配置生效
  update        更新程序和管理脚本
  start | stop | status | enable | disable | log | config | version
节点命令支持 --api-host URL、--node-id ID、--node-ids IDS、--api-key KEY。
--config FILE 可指定节点配置；原 proxynode-manager 命令及长子命令继续兼容。
修改前校验并保留一份 config.json.bak；密钥交互输入不回显。
托管主机的节点由面板分配；本地 add/del 不修改托管配置。
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
            *)
                if [[ $action != list-nodes && $1 != -* ]]; then
                    node_ids+=("$1")
                    shift
                else
                    fail "未知参数: $1"
                fi ;;

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
    if isinstance(config, dict) and config.get('Agent'):
        if action != 'list-nodes':
            raise ValueError('当前为托管主机，请在面板中添加、绑定或删除节点；没有修改本地配置')
        config.setdefault('Nodes', [])
        return original, config
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
        if config.get('Agent'):
            print('当前为托管主机，请在面板的主机与节点管理中查看或调整分配。')
            sys.exit(0)
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
    print('请执行 pnode restart，确保运行中的服务加载新配置。')
except (OSError, ValueError) as exc:
    print(f'错误: {exc}', file=sys.stderr)
    sys.exit(1)
PY
}

show_menu() {
    [[ -t 0 ]] || fail '菜单需要终端；可运行 pnode help 查看命令'
    if (( $# )); then
        [[ $# == 2 && $1 == --config && -n $2 ]] || fail '用法: pnode menu [--config FILE]'
    fi
    local choice action
    local -a options=()
    while true; do
        cat <<'MENU'

pnode 管理菜单
  1. 查看节点        2. 添加节点
  3. 删除节点        4. 重启服务
  5. 服务状态        6. 启动服务
  7. 停止服务        8. 更新程序
  9. 查看日志        0. 退出
MENU
        read -r -p '请选择 [0-9]: ' choice || return 0
        options=()
        case "$choice" in
            1) action=list; options=("$@") ;;
            2) action=add; options=("$@") ;;
            3) action=del; options=("$@") ;;
            4) action=restart ;;
            5) action=status ;;
            6) action=start ;;
            7) action=stop ;;
            8) action=update ;;
            9) action=log ;;
            0) return 0 ;;
            *) echo '请输入 0 到 9。'; continue ;;
        esac
        if bash "$0" "$action" "${options[@]}"; then
            # A successful update may replace this running script; reopen the menu.
            [[ $action != update ]] || return 0
        else
            echo '操作未完成，请根据上述错误检查后重试。' >&2
        fi
    done
}

if (( $# == 0 )); then
    if [[ -t 0 ]]; then show_menu; else usage; fi
    exit 0
fi

case "$1" in
    menu) shift; show_menu "$@" ;;
    add) shift; manage_nodes add-node "$@" ;;
    list) shift; manage_nodes list-nodes "$@" ;;
    del) shift; manage_nodes remove-node "$@" ;;
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
