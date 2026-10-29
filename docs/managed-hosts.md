# 面板托管主机

从 `proxynode-v0.3.0` 起，可由 ProxyBoard 统一创建、修改和删除节点、管理证书及 HTTP 网关。一个主机可同时运行多个不同协议的 v2node 节点 ID，内核仍只有自有 Xray-core fork。

## 安装与旧节点转换

先升级面板及数据库。在「主机与证书」新建主机，复制注册命令到节点服务器执行一次：

```sh
curl -fsSL https://raw.githubusercontent.com/ilnli/v2node/main/script/install.sh -o install.sh
sudo sh install.sh --api-host https://backend.example.com --host-id 1 --enrollment-token 'pn_enroll_REPLACE_WITH_PANEL_TOKEN'
```

注册令牌由面板生成，15 分钟有效。安装器补齐 Bash、curl、Python 3、tar、校验工具和 CA 证书，支持 apt/dnf/yum/apk；创建 systemd/OpenRC 服务。首次下载脚本需要已有 curl。安装后主机生成并持久化自己的 `pn_host_` 凭据，不持有管理员系统密钥或全局节点令牌。

已有 proxynode 独立配置转托管时附加 `--migrate-managed`；原 v2node 同时附加 `--migrate-v2node`，显式停用旧服务。安装器保留权限为 0600 的配置备份。先在面板把原节点 ID 绑定到主机；没有绑定节点时，托管进程在线但不创建代理监听。已有托管身份不能被另一个主机 ID 静默覆盖。

后续在面板选择主机、节点协议及端口即可。托管模式的监听列表由面板决定，不使用本地 `pnode add/del` 管理。`pnode status/log/restart` 可保留作为排错入口；通常无需再次登录服务器。

## 支持范围

| 功能 | 配置与行为 |
| --- | --- |
| 多协议 | VLESS、VMess、Trojan、Shadowsocks、Hysteria2、TUIC、AnyTLS；每个节点独立 ID/监听 |
| TLS | 上传 PEM，或 ACME HTTP-01/DNS-01；自动续期和重新加载 |
| HTTP 网关 | 内置 WS、HTTPUpgrade、gRPC、XHTTP TLS 终止/转发，后端限定 loopback |
| REALITY / Vision | 由 Xray 直接监听，不经过 HTTP 网关 |
| HY2 / TUIC | 原生 UDP + TLS，ALPN h3，无需 HTTP 网关 |
| 维护 | 面板重新应用、重启代理、指定本 fork Release 升级、查看状态/错误 |

DNS A/AAAA、云安全组及已有端口占用由部署者控制。HTTP-01 需要公网 TCP 80 直达节点；DNS-01 需要相应提供商凭据。节点不会任意停止现有 Nginx 或修改云防火墙。TCP 与 UDP 可使用相同数值端口，同网络内冲突会报错。

## 状态、更新与恢复

配置先整体验证，再替换代理工作进程；失败恢复上一份可运行配置。管理进程在空节点、配置错误时仍保持在线。默认 15 秒轮询；断网期间保留正在运行的监听。冷启动可使用最后成功的节点配置快照，但仍需用户接口可达，以取得当前有效账号；整站离线时自动等待恢复。收到身份撤销/删除的明确拒绝后停止监听并删除恢复快照。

面板配置变更会重启该主机的代理工作进程，现有连接可能中断。关停时关闭活跃连接并保存流量批次；未确认批次保留同一 ID 重试，面板负责幂等结算。每节点流量队列最多 4 MiB；审计队列独立使用原有进程级 1 GiB 磁盘预算。永久解绑后遗留的未确认批次保留，重新绑定该 ID 才能继续上报。

远程更新仅接受 `proxynode-vX.Y.Z`，下载 `ilnli/v2node` Release 并校验 SHA-256。候选二进制实际加载当前配置，通过后原子切换完整安装包，由独立监督进程验证新管理进程健康；启动失败/超时回滚旧包。命令结果持久化，回执丢失不会重复执行重启或更新。手工运行二进制、未配置监督进程的容器不接受远程更新。

## 持久化路径

| 内容 | 默认位置 |
| --- | --- |
| 本地 Agent 配置 | `/etc/proxynode/config.json` |
| 安装包与当前版本指针 | `/var/lib/proxynode/releases/`、`/var/lib/proxynode/current` |
| 管理身份、最后配置、命令日志 | `/var/lib/proxynode/managed/` |
| 证书/ACME 账号、流量队列、审计队列 | 上述目录的 `certificates/`、`traffic/`、`audit/` |

上述状态含私密资料，备份与恢复应保持 root 私有权限。不要把主机身份目录克隆给另一台在线服务器。普通更新保留身份、配置和缓存。

## 验证

```sh
GOEXPERIMENT=jsonv2 go test ./...
python3 -m unittest discover -s tests/deployment -v
python3 tests/interop/managed.py --proxynode /path/to/proxynode
```

真实面板/PostgreSQL/Mihomo 集成脚本为 `tests/interop/panel_managed.py`，从 ProxyBoard 后端的 uv 环境运行；参数见 `--help`。仅使用隔离本地测试库。既有 [31 组 VLESS 互通测试](mihomo-interop.md) 仍是 CI 与发布门槛，不能以配置加载成功替代实际数据传输验证。
