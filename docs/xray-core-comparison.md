# Xray-core 修改版对比与 Mihomo 升级约束

## 2026-10-30 出站审计补丁

依赖在上述官方版本基础上固定到自有 fork [`4155e282792c`](https://github.com/ilnli/Xray-core/commit/4155e282792c44c3ab09f49570ec0da544bd9034)。补丁仅增加 context 范围的出站元数据回调，在 Freedom 实际拨号、UDP 写入和最终规则拒绝处报告目标及结果，不变更 TLS/REALITY/VLESS 实现和 Go 工具链。代理链禁止把中间代理地址误记为最终目标。

2026-10-30 重新查询 Mihomo 官方 Release，最新正式版仍为 v1.19.33；Linux amd64 压缩包 SHA256 与互通文档固定值一致。新构建真实互通矩阵 **31/31** 通过，另通过 TCP/UDP 目标阻断、CN 来源不受影响、UDP 后续目标、代理链事件隔离和端到端面板审计验证。

## 2026-10-29 同步状态

现行依赖已同步官方 [v26.10.29](https://github.com/XTLS/Xray-core/releases/tag/v26.10.29)，fork 合并提交为 [`3c160b27cb42`](https://github.com/ilnli/Xray-core/commit/3c160b27cb42435487f273c095b31271e812f47f)。当日 `go.mod` 的 `replace` 固定到该提交，Go 工具链为 1.27.3。AnyTLS/TUIC、Shadowsocks 扩展和旧 fork 历史保留，非生成的 VLESS、Vision、REALITY、XHTTP 实现与官方版本一致。

本次纳入 Freedom `blockDelay`、WebSocket 正常关闭、DNS 响应边界、入站模块初始化顺序及 Geodata 等修复，并包含上游 XDRIVE 更新。VLESS Encryption 仍可设置为 `none` 并使用 TLS/REALITY；Xray 出站对免传输加密的私网地址例外判断更严格。仅在回环受信网络执行的无 TLS 测试，不代表公网明文部署建议。

合并冲突仅涉及 CI，保留自有 fork 的固定工具链及 Geodata 测试流程，未引入上游缓存等待作业和全仓生成代码格式重写。当前真实互通结果见 [Mihomo 互通说明](mihomo-interop.md)。

## 2026-10-28 同步记录

当日建立 [ilnli/Xray-core](https://github.com/ilnli/Xray-core)，将官方 v26.10.21 合入 wyx2685 的历史，保留扩展。同步提交为 [`d84c11216aba`](https://github.com/ilnli/Xray-core/commit/d84c11216aba6397cc7e30b5cc3b1aacde59dd33)，当时 proxynode 的 `replace` 固定到该提交；Go 构建版本改为 1.27.3。

此次包含下文提到的 Go 1.27.3+ Vision 修复及 REALITY、gRPC、Mux 等上游更新。非生成的 VLESS、Vision、REALITY、XHTTP 代码与官方 v26.10.21 一致。自定义 dispatcher 同步上游新增的路由错误日志，保留审计和限速。真实互通范围与结果单独记录在 [Mihomo 互通说明](mihomo-interop.md)。

以下是 **2026-10-27 升级前快照**，其中“当前 fork”指原 wyx2685 提交；保留它用于解释为何需要自有 fork，不代表现行依赖仍缺少更新。

## 比较对象

- 实际依赖：`wyx2685/Xray-core` 提交 [`e087e74a1bf25b46f2776d1a3b53a68f6018d654`](https://github.com/wyx2685/Xray-core/commit/e087e74a1bf25b46f2776d1a3b53a68f6018d654)，也是核对时该 fork 的 main。`go.mod` 的 `replace` 决定实际使用的代码。
- 官方共同基点：`XTLS/Xray-core` 提交 [`b26a91de4f3294e26a0ad0a970b81a386a41f789`](https://github.com/XTLS/Xray-core/commit/b26a91de4f3294e26a0ad0a970b81a386a41f789)，提交名为 `Xray-core v26.9.30`。
- 核对时官方最新 Release：[v26.10.21](https://github.com/XTLS/Xray-core/releases/tag/v26.10.21)，在上述基点之后有 58 个提交。不要把“fork 补丁”与“尚未同步的官方更新”混在一起。
- wyx2685 仓库的 GitHub parent/source 都是 `XTLS/Xray-core`。本项目使用 Xray 内核，不是 V2Ray/V2Fly 内核。

## Fork 相对同一官方基点的实质变化

完整差异有 112 个文件，其中 44 个不是生成的 `.pb.go` 文件；其余大量改动是 import 排序、空行等生成代码格式变化。

| 范围 | 已确认的变化 | 对 proxynode 的影响 |
| --- | --- | --- |
| AnyTLS | 增加入站、出站、账户、padding、流复用和会话维护实现 | 当前 AnyTLS 节点及账户适配依赖这些新增包 |
| TUIC | 增加 QUIC 入站、认证、TCP/UDP 转发、0-RTT 设置及 transport；入站 worker 注入认证信息 | 当前节点使用其入站；**出站和 transport dialer 仍明确返回未实现错误**，不能称完整双向 TUIC 支持 |
| 传统 Shadowsocks | 重做用户验证和缓存路径、增加派生密钥复用及失败封禁逻辑；已知用户的 UDP 直接解密，不再每包创建临时 Validator | 改变多用户认证与异常流量处理；不是 SS2022 协议整体重写 |
| Trojan | 首包缓冲改为 `buf.New()` 并在结束时释放 | 改变缓冲分配方式，未进行吞吐/内存基准测试 |
| 日志 | 两类 deprecated warning 函数变成空操作；stdout/stderr 去掉内核时间前缀 | 不再看到部分弃用提醒；removed-feature 错误仍然保留 |
| 依赖 | 新增 `sagernet/sing`、`v2fly/ss-bloomring` 及间接 `riobard/go-bloom` | 都是被 Xray fork 使用的库，不能据此认定内核变成 sing-box/V2Fly |

主要源码证据：[AnyTLS](https://github.com/wyx2685/Xray-core/tree/e087e74a1bf25b46f2776d1a3b53a68f6018d654/proxy/anytls)、[TUIC 出站](https://github.com/wyx2685/Xray-core/blob/e087e74a1bf25b46f2776d1a3b53a68f6018d654/proxy/tuic/outbound.go)、[Shadowsocks 验证](https://github.com/wyx2685/Xray-core/blob/e087e74a1bf25b46f2776d1a3b53a68f6018d654/proxy/shadowsocks/validator.go)、[日志变更](https://github.com/wyx2685/Xray-core/blob/e087e74a1bf25b46f2776d1a3b53a68f6018d654/common/errors/feature_errors.go)。

不要把“优化”注释当作验证结果：新增 `ShardedBloomRing` 没有实际调用方；SS 数据缓冲池缺少对应归还路径，不能声称所有缓冲都实现循环复用。缓存仍要进行实际解密验证；源地址包含端口，也不能仅据注释承诺同 IP 多连接 O(1)。封禁逻辑有启用条件，尚未验证性能收益或误伤率。

## 与 VLESS 直接相关的结论

逐文件比较得到：

- `proxy/vless` 的 18 个非生成文件与官方基点相同。
- `transport/internet/splithttp` 的 17 个非生成文件相同。
- `transport/internet/reality` 的 3 个非生成文件相同。
- `infra/conf/vless.go`、`proxy/proxy.go` 也相同。

因此当前 fork 的 VLESS Encryption、Vision、XHTTP 和 REALITY 能力主要继承自官方 v26.9.30；它没有单独重写这些协议来实现 Mihomo 兼容。实际连接还取决于 proxynode 的配置映射、面板下发参数和客户端设置，源码相同不等于所有组合实测通过。

官方在基点之后加入了 [XTLS Vision: Compatible with Go 1.27.3+](https://github.com/XTLS/Xray-core/commit/0f127f934c6d7c10e4bb6acbe98033db9e5fd922)：适配 TLS `rawInput` 从值到指针的内部结构差异。当前 fork 未包含该修复。官方后续还更新了 REALITY，并修复了 gRPC 重连、Mux 等问题。

已发布的 `proxynode-v0.1.0` 经 `go version -m` 核对由 Go 1.27.1 构建；本机另有 Go 1.27.3 构建产物，但其 Vision 互通并未验证。**Go 工具链更新本身也必须纳入兼容验证，不能只检查 Xray 版本号。**

## 为什么不能直接换回官方依赖

proxynode 的 `core/distro/all/all.go` 无条件导入 fork 的 AnyTLS/TUIC 包；`core/user.go` 构造 `anytls.Account`、`tuic.Account`；`core/inbound.go` 使用 `AnyTLSServerConfig`、`TuicServerConfig`。官方同基点缺少这些包和类型。因此仅删除 `replace` 会缺失当前适配所需的代码，即使运行配置不启用这些协议也一样。

后续如需同步官方修复，合适路线是基于 wyx2685 的 fork 维护我们自己的 Xray 分支，保留必要扩展，以明确的官方提交为基线同步或移植补丁。用户已授权必要时采用该路线。本次核对没有切换或升级内核依赖。

## Mihomo 升级验收要求

核对时最新正式版为 [Mihomo v1.19.33](https://github.com/MetaCubeX/mihomo/releases/tag/v1.19.33)。以后每次升级重新确认 latest，记录具体版本和二进制校验值，不能永久把这个版本当“最新版”。参考：[VLESS 配置](https://wiki.metacubex.one/config/proxies/vless/)、[传输配置](https://wiki.metacubex.one/config/proxies/transport/)、[TLS/REALITY 配置](https://wiki.metacubex.one/config/proxies/tls/)。

| 验收维度 | 必须关注的配置 |
| --- | --- |
| 基础连接 | TCP、TLS、REALITY；UUID、SNI、ALPN、证书验证、公钥/short-id |
| Vision | `xtls-rprx-vision` 与两端允许的安全层、传输组合；特别核对 Go 工具链兼容 |
| WS / HTTPUpgrade / gRPC | 路径、Host、early-data、HTTPUpgrade/fast-open、serviceName；Mihomo HTTPUpgrade 使用 `ws-opts` 映射 |
| XHTTP | `stream-one`、`stream-up`、`packet-up` 及自动选择；HTTP/1.1、H2、H3 的合法组合，含路径与必要头部 |
| VLESS Encryption | `mlkem768x25519plus` 的 native/xorpub/random、1-RTT/0-RTT、密钥及 padding 的合法组合 |
| UDP | 普通 UDP 与 XUDP 的收发；不能把 V2Ray 的 packetaddr 支持视为 Xray 兼容证据 |
| 回归 | 既有可用配置、用户动态更新、必要的流量统计与审计归属不退化 |

“各种配置”指双方实际支持、参数匹配且组合合法的配置，不是任意字段的笛卡尔积。例如 Mihomo 的 TCP HTTP 伪装要映射到 Xray TCP header；旧的独立 H2 transport 与 XHTTP 的 H2 不是同一协议，当前官方基点已移除前者，不能仅重命名为 XHTTP 宣称兼容。

升级前需要真实 Mihomo → 真实 proxynode → 本地测试目标的可重复连接和双向数据测试，并对比升级前后结果。失败、未测、不支持分别记录；配置解析、启动、`version` 输出、普通 CI 通过都不能代替协议互通。当前 CI 尚未覆盖这份完整矩阵，第一次相关升级必须先补齐所需测试；本文件不宣称全部组合已通过。
