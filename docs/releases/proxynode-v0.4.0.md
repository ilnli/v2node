# Proxynode v0.4.0

新增按节点拦截 CN 目标，以及实际出站 IP、国家和拒绝结果审计。允许 CN 来源用户连接；是否拦截由该节点选中的目的地址路由规则决定。

## 更新内容

- 复用面板 `block_ip` / `geoip:cn` 路由，IP 拦截优先于 DNS、转发和默认路由；按需解析域名。
- `freedom` / `direct` 直连出口检查最终目标 IP，覆盖 IPv4、IPv6、TCP 及同一 UDP 会话的后续目标，多个节点的规则彼此隔离。
- 审计保留原目标和域名，新增实际出站目标 IP、可识别的国家以及放行、拒绝、失败结果。代理链无法观测的最终 IP 保持未知，不把中间代理地址当作访问目标。
- 路由和直连出口拒绝的请求进入审计；UDP 重复目标采用有界去重。现有缓存容量及满额丢弃新记录的规则保留。
- Docker 镜像打包经过校验的 GeoIP / Geosite 数据；安装包继续包含同一固定版本的数据。
- 修复安装失败和迁移中断时的清理处理。
- 沿用 Go 1.27.3 和 Xray v26.10.29，自有 fork 增加出站观测补丁，未更换代理内核。

## 升级与启用

先更新 ProxyBoard 主线。Docker 部署执行 `git pull` 和 `docker compose up -d --build`，等待面板自动迁移到 `0007_audit_connection_details` 并正常启动，再升级节点。

独立安装的节点：

```sh
sudo pnode update --version proxynode-v0.4.0
```

面板托管节点在主机管理中执行更新，指定 `proxynode-v0.4.0`。Docker 自行构建的节点更新源码后重建其镜像和容器；本 Release 提供二进制安装包，不发布预制容器镜像。

在面板路由管理创建 CN 阻断预设，并在需要启用的节点中勾选保存；只更新程序不会自动给所有节点开启 CN 拦截。CN 范围以 GeoIP 数据为准，代理链的远端解析结果不在本节点可观测范围内。

审计需开启节点的 `Audit.Enabled` 并持久化缓存目录。面板默认隐藏 `cp.cloudflare.com` 测活记录，可勾选显示；节点仍会上报，面板仍会保存。历史记录不自动补齐 IP 和国家。

## 验证

发布前主线 CI 已通过 Go 全套测试、核心竞态检测、安装脚本测试、真实托管进程生命周期及 Mihomo 31/31 项 VLESS 互通验证。新增真实 TCP/UDP 测试覆盖最终目标拦截和拒绝审计；真实节点到面板的审计链路已在 PostgreSQL 和 MySQL 上通过。发布流水线会再次完成测试后生成 Linux amd64、arm64 安装包及 `SHA256SUMS`，所有附件上传完成后才公开 Release。

2026-10-31 核对的 Mihomo 最新正式版仍为 v1.19.33，固定客户端校验值不变。覆盖范围和限制见 [Mihomo 互通说明](https://github.com/ilnli/v2node/blob/main/docs/mihomo-interop.md)。
