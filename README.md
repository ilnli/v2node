# Proxynode

基于本地 v2node 的独立节点项目，新增连接审计采集和有界缓存。兼容原有 V2Board / ProxyBoard 节点配置及协议；Go module 路径暂保留上游名称，二进制名为 `proxynode`。来源和许可证见 [NOTICE.md](NOTICE.md)。

## 审计行为

- 记录面板用户 ID、UTC 毫秒时间、节点 ID、节点协议、TCP/UDP、原始目标地址和端口，以及可获取的域名。
- 复用 HTTP Host、TLS SNI、QUIC 嗅探。记录的是连接尝试，不是每次网页请求、已连接证明或 HTTPS 完整 URL；没有可见域名时留空。
- 异步批量上报。正常使用内存，积压超过内存阈值后转入磁盘；收到匹配的完整确认才删除批次。
- 整个进程共享限制：默认内存队列 64 MiB，允许配置到 128 MiB，为入口队列、索引和工作缓冲留出余量，将审计活跃缓冲预算控制在 256 MiB 内。**这不是 Xray 进程 RSS/Go 堆的硬上限。**
- 磁盘缓存最多 1024 MiB；容量不足或入口队列满时丢弃**新到记录**，保留旧记录。日志输出丢弃计数。
- 正常退出、配置重载会将未上报内存批次落盘，重启后恢复；强制杀进程或断电可能丢失尚未落盘的记录。

## 安装与升级

官方发布位置：[ilnli/v2node Releases](https://github.com/ilnli/v2node/releases)。支持 Linux amd64 / arm64；需要 Bash、curl、tar、sha256sum 和 Python 3。

```sh
curl -fL https://raw.githubusercontent.com/ilnli/v2node/main/script/install.sh -o install.sh
sudo bash install.sh --api-host https://panel.example.com --node-id 1 --api-key '节点密钥'
```

已有配置会原样保留；全新配置默认关闭审计，待面板接口可用后再开启。已有 v2node 使用 `sudo bash install.sh --migrate-v2node` 显式迁移配置和服务。也可用 `--config /path/to/config.json` 导入配置。

```sh
sudo proxynode-manager update
sudo proxynode-manager update --version proxynode-v0.1.0
proxynode-manager version
sudo proxynode-manager status
```

安装和升级只从 **ilnli/v2node** 的 Release 下载，并先验证 SHA-256。配置和审计缓存不会因升级被清空。更多说明见 [部署与发布](docs/deployment.md)。

## 构建与运行

当前构建使用 Go 1.27.3 与 `jsonv2`，依赖自有 [Xray-core fork](https://github.com/ilnli/Xray-core)，已同步官方 v26.10.21 并保留 AnyTLS/TUIC 扩展。升级 Go/Xray 前须执行 [Mihomo VLESS 互通测试](docs/mihomo-interop.md)：

```sh
GOEXPERIMENT=jsonv2 go build -trimpath -o build_assets/proxynode .
./build_assets/proxynode version
./build_assets/proxynode server --config ./examples/config.json
```

先修改示例中的面板地址、节点 ID、密钥及缓存目录。默认配置路径为 `/etc/proxynode/config.json`。省略 `Audit` 或设置 `Enabled: false` 时不采集审计，继续兼容现有面板。

**本阶段尚未改造面板。** 启用审计前，面板需实现 [审计接口](docs/audit-api.md)；接口缺失时，节点会重试、缓存，达到上限后丢弃新记录。

Docker：`docker build -t proxynode .`。运行时挂载配置至 `/etc/proxynode/config.json`，并将 `/var/lib/proxynode/audit` 挂载到持久卷；另按实际协议映射节点端口。启用磁盘审计需要 Linux/macOS/BSD；其他平台仍可关闭审计使用节点。

## 验证

```sh
GOEXPERIMENT=jsonv2 go test ./...
GOEXPERIMENT=jsonv2 go test -race ./common/audit ./core/app/dispatcher
```

缓存和 HTTP 上报模块只依赖标准库，无法下载外部依赖时可单独验证：

```sh
GO111MODULE=off go test -race ./common/audit
```

缓存策略及限制详见 [运行说明](docs/audit-operations.md)。本地验证结果见 [verification.md](docs/verification.md)。
