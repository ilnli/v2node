# Proxynode v0.3.0

本版本新增 ProxyBoard 托管主机：一次安装后，从面板配置多协议节点、TLS 证书、HTTP 网关与远程维护。原有独立模式继续兼容。

- 主机使用独立、限定节点范围的凭据，支持空节点待命、配置同步、最后成功配置恢复、撤销后停止监听。
- 自动申请/续期 ACME HTTP-01、DNS-01 证书，或接收上传 PEM；私有保存账号和证书，更新后重新加载。
- 内置 TLS 网关支持 WS、HTTPUpgrade、gRPC、XHTTP，内部上游仅监听 loopback；HY2/TUIC 使用原生 UDP。
- 远程更新仅从 ilnli/v2node 下载校验过的 Release，由独立服务监督进程确认启动，失败自动恢复旧安装包。
- 重启前关闭连接并持久化未结算流量，以固定批次 ID 重试；修复并发计数读取/清零和任务关停问题。
- 保持 Xray v26.10.29 自有 fork 与 Go 1.27.3，不引入其他代理内核。

## 升级方式

先升级 ProxyBoard 并执行新增数据库迁移，再在「主机与证书」生成接入命令。已有 proxynode 转托管附加 `--migrate-managed`；原 v2node 同时附加 `--migrate-v2node`。先绑定原节点 ID，避免转换后空监听。详见 [托管文档](https://github.com/ilnli/v2node/blob/main/docs/managed-hosts.md)。

旧独立模式可继续 `sudo pnode update`。现有连接可能在配置应用或升级时中断。生产 DNS 解析、云安全组和已有端口占用仍需部署者正确配置。

## 验证

Go 全套测试、并发检测、安装及监督进程回滚测试通过。实际 Mihomo v1.19.33 的既有 VLESS 矩阵 31/31 通过；真实 ProxyBoard/PostgreSQL/托管节点矩阵 11/11 通过，覆盖七种直连协议、四种 TLS 网关及 TCP/TLS/UDP 往返。ACME 使用本地 Pebble 完成签发、续期和账号复用验证。

上述结果只覆盖文档列出的合法组合，不代表任意协议/传输/TLS 参数都可互换。
