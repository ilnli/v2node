# Mihomo → proxynode VLESS 互通门槛

## 2026-11-14 安全升级验证

本分支更新到 Xray v26.11.9 fork，客户端固定最新正式版 Mihomo v1.19.34（2026-11-11 发布）。Linux amd64 压缩包 SHA256：`223fab877e5795ce780ba39902e5f0b1113c1256ae244a7160f1165e017e9ccc`。来源：[Mihomo Release](https://github.com/MetaCubeX/mihomo/releases/tag/v1.19.34)。

矩阵扩展为 39 项，新增裸 TCP Encryption+Vision、最低版本接受/拒绝、ML-DSA 服务端兼容、真实 Xray 客户端 ML-DSA 验签及错误验证公钥拒绝、XMUX 并发和独立下载端点。证书固定场景新增错误指纹拒绝。报告同时记录两个客户端与服务端的二进制 SHA256。脚本会构建与节点同一 fork 的 Xray 测试客户端，可用 `XRAY_BIN` 指定已有构建。

[完整 CI 43621839778](https://github.com/ilnli/v2node/actions/runs/43621839778) 在节点提交 [`d50e7001ecee`](https://github.com/ilnli/v2node/commit/d50e7001eceeb337edf28bee229406a01721f763) 通过 **39/39** 项，且全包 Go 测试、核心 race 检查、静态构建和真实 managed worker 生命周期检查全部通过。内核固定为 `34846a645f348f69bea579ffc4daf47399c281e3`（v26.11.9 fork）。

互通报告 ZIP（GitHub artifact `14124159237`）SHA256 已与 GitHub API 元数据核对：`ec823adc2aa62fdf4b80fb2bcceaea7e08fbcd6144b1a17a98180b2f8f7cc641`。实际节点二进制 SHA256：`4921133909b1e45202249e114fd24f548fa8bc67878f555d84dc07451be216bd`；Mihomo 二进制 SHA256：`0a68807c7c93963bb66d07717fe0f3ac3945b422b525f8a1e19438d278d7ae11`；Xray 测试客户端 SHA256：`858e574c4d5738ca84a40cf0bc35fc75d8c356e631e4267e3bf9b348fbb9f2af`。

- Mihomo v1.19.34 REALITY 仍上报 `1.8.2`，设置 `min_client_ver: 26.3.27` 会拒绝它，即使已启用 `support-x25519mlkem768: true`。最低版本默认保持上游未配置状态，管理员可显式启用。
- Mihomo v1.19.34 没有 ML-DSA 验证选项；`mldsa65-compat` 只证明添加服务端签名不破坏兼容，验签正反例由真实 Xray 客户端执行。临时靶站证书大于 3500 字节；本地 OpenSSL 靶站不声明支持混合后量子密钥交换。
- XMUX 验证并发双向传输；下载分离通过独立 TCP 转发端口返回同一 XHTTP session hub，并断言该端口实际承载数据；另含 REALITY 上传、不同 SNI 与证书固定的 TLS 下载桥接，确认下载通道清除上传 REALITY 配置。Mihomo 清除该配置需要 `reality-opts: {public-key: ""}`；空对象会因缺少必填 `public-key` 解析失败。它不等于外部 CDN 或生产网关验证。
- 回落限速默认关闭；[官方文档](https://xtls.github.io/config/transport.html#realityobject)指出它自身会形成特征。仅在确需限制盗用时按目标特点配置，避免所有节点统一固定值。

依赖锁由 [CI 43620013723](https://github.com/ilnli/v2node/actions/runs/43620013723) 的 `go mod tidy` 生成后原样提交；下载的锁文件 ZIP 已与 GitHub API artifact digest 核对：`1b9862e84ea71e123e855bbf27dcc6ca5f419a9a699b8aa3ee196d522b8ad4ce`。CI 不自动修改或提交依赖。

## 历史发布基线

核验日期：2026-10-29。客户端固定为当日最新正式版 [Mihomo v1.19.33](https://github.com/MetaCubeX/mihomo/releases/tag/v1.19.33)，服务端使用真实 proxynode 进程、Xray-core v26.10.29 加保留的 wyx2685 扩展、Go 1.27.3。Mihomo 是测试客户端，服务端没有更换为其他代理内核。

最终静态发布候选通过完整 **31/31** 项互通矩阵，包含所有 Vision direct-copy 断言、HTTP/3、证书指纹校验和错误 UUID 拒绝探针。结果只适用于下列显式配置及已声明的覆盖范围。

官方下载资产：`mihomo-linux-amd64-v1.19.33.gz`。SHA256：

```text
132f6f3edf9a65e62f04853aed3358472e0b2b7666daa8419edfa2ddf5bb6323
```

脚本每次校验压缩包后解压，Python 再核对客户端版本；报告同时记录实际客户端和服务端二进制 SHA256。今后升级必须重新确认最新正式版并更新版本、下载地址、校验值和本文日期，不能把当前固定版本永远称作“最新”。

## 运行

依赖：Linux amd64、Python 3.11+、OpenSSL、curl、gzip、sha256sum，以及构建 proxynode 所需的 Go 工具链。只在回环地址启动测试服务，不需要生产面板、证书、域名或外网测试目标。首次下载 Mihomo 和构建依赖需要网络。

在仓库根目录运行：

```bash
bash script/test-mihomo.sh --output /tmp/mihomo-results
```

输出目录必须尚不存在。默认构建临时 proxynode；也可使用已构建的发布候选：

```bash
PROXYNODE_BIN=/absolute/path/proxynode \
MIHOMO_CACHE_DIR=/tmp/mihomo-cache \
bash script/test-mihomo.sh --output /tmp/mihomo-results
```

单项诊断使用 `--case tcp-reality-vision`，可重复指定；`--list` 列出测试名称。所有矩阵项通过才返回退出码 0。CI 应始终保存输出目录，包括失败时的 `report.json`、每项结果、两个真实进程的日志、面板响应和客户端/服务端配置。目录内证书及密钥均临时生成，仅用于测试。

## 验证内容

Python 标准库测试程序启动临时面板 HTTP API，供真实 proxynode 拉取节点、用户和在线信息。真实 Mihomo 通过 SOCKS5 接收探针流量，再通过 VLESS 连接 proxynode。每项检查：

- 256 KiB 随机 HTTP POST 请求和同长度回显，逐字节比对双向数据。
- 256 KiB 随机 TLS 1.3 内层传输，避免只测明文而漏过 Vision 路径。Vision 项额外断言服务端日志出现 TLS 1.3 识别与 direct-copy 切换。
- 一个 SOCKS5 UDP association 上连续发送 1、257、1200、4096 字节数据报，验证经 VLESS XUDP 转发后的内容和包边界。
- `xorpub-0rtt` 再建立连接，检查已有票据后的 TLS 数据传输。
- 固定证书指纹项关闭 `skip-cert-verify`；随后用错误 UUID 重启客户端，必须拒绝传输，防止路由意外直连造成假阳性。

Mihomo 打开监听端口时还可能处于加载状态。测试先通过专用 DIRECT 规则访问假面板的 readiness 端口，确认其数据通路已就绪。实际 HTTP、TLS、UDP 目标使用不同端口，始终匹配 proxynode 代理。生产中的用户认证、节点构建、面板字段映射、Xray 入站和出站均参与测试。

Xray 默认阻止私网目的地址。测试面板通过正式 `default_out` 配置机制添加仅允许 `127.0.0.1/32` 的 freedom `finalRules`，使本地回显可达；这是测试夹具配置，不修改生产默认策略。

## 矩阵

以下共 39 项。正向互通项包含 TCP、内层 TLS 和 UDP/XUDP 双向数据校验；最低版本拒绝项只执行拒绝探针，ML-DSA 验签项使用同一 fork 的真实 Xray 客户端。

| 配置 | 项数 | 说明 |
| --- | ---: | --- |
| TCP，无 TLS / TLS / REALITY | 3 | 基本路径 |
| TCP + Vision，TLS / REALITY | 2 | TLS 1.3 direct-copy |
| WS，无 TLS / TLS | 2 | 标准 WebSocket |
| HTTPUpgrade，无 TLS / TLS | 2 | Mihomo 的 WS upgrade 选项 |
| gRPC，无 TLS / TLS / REALITY | 3 | HTTP/2 |
| XHTTP packet-up / stream-up / stream-one / auto，TLS / REALITY | 8 | TLS 项显式 HTTP/2 |
| XHTTP packet-up / auto，无 TLS | 2 | 明文 HTTP |
| TCP + VLESS Encryption | 3 | native/1rtt、xorpub/0rtt、random/1rtt |
| TCP + TLS + Vision + VLESS Encryption | 1 | native/1rtt |
| TCP + TLS，固定证书指纹 | 1 | 含错误 UUID、错误证书指纹拒绝探针 |
| WS + TLS early data | 1 | `Sec-WebSocket-Protocol`，2048 字节 |
| HTTPUpgrade + TLS fast-open | 1 | 提前发送数据 |
| XHTTP packet-up + TLS，显式 HTTP/1.1 / HTTP/3 | 2 | 面板 `tls_settings.alpn` 和客户端 ALPN 一致 |
| TCP，无 TLS + Vision + VLESS Encryption | 1 | native/1rtt，包含 direct-copy 断言 |
| REALITY 最低客户端版本 | 2 | 接受 1.8.2、拒绝 26.3.27 门槛 |
| REALITY ML-DSA-65 | 2 | Mihomo 兼容；Xray 正确验证公钥与错误验证公钥拒绝 |
| XHTTP XMUX、独立下载通道 | 3 | 并发 TLS；TLS 下载转发；REALITY 上传与独立 TLS 下载 |

不声称任意交叉组合均合法或兼容。Vision 覆盖 TCP + TLS/REALITY 和无 TLS + Encryption（native/1rtt）；VLESS Encryption 覆盖主要模式及上述 Vision 组合，未穷举密钥链、填充、票据和传输组合。HTTP/3 通过真实 proxynode 的 `tls_settings.alpn: ["h3"]` 配置启用。

## 必须说明的限制

**REALITY 客户端必须配置 `reality-opts.support-x25519mlkem768: true`，并使用支持该 key share 的指纹（矩阵使用 `chrome`）。** Mihomo v1.19.34 默认移除该 key share，而当前 REALITY 服务端拒绝缺少它的 ClientHello。实测默认/false 配置认证失败；开启后合法矩阵通过。这是客户端迁移要求，不能把默认配置宣称为兼容。依据：[Mihomo REALITY 选项](https://github.com/MetaCubeX/mihomo/blob/v1.19.34/adapter/outbound/reality.go)、[握手实现](https://github.com/MetaCubeX/mihomo/blob/v1.19.34/component/tls/reality.go)。

**Mihomo 的普通 UDP 请求在此版本默认使用 XUDP。** 不设置 `packet-encoding` 或配置 `xudp: false` 不会切换到原始 VLESS UDP；初始化代码会重新开启 XUDP。不能把另一次相同 XUDP 测试当作原始 UDP 覆盖。`packetaddr` 是另一种编码，本矩阵未测试。依据：[Mihomo VLESS 初始化代码](https://github.com/MetaCubeX/mihomo/blob/v1.19.34/adapter/outbound/vless.go)。

尚未测试：IPv6、外部 CDN/反代、VLESS Mux、REALITY 靶站混合后量子密钥交换、全部加密密钥/填充/传输排列。Mihomo 本身不支持 ML-DSA 验签，签名正反例使用 Xray 客户端。HTTP/3 + REALITY 在 Mihomo 中不支持，不纳入合法矩阵。失败是实际执行但未通过；不支持是双方参数/能力不允许；未测试是本矩阵未覆盖，三者必须区分。

## 2026-10-30 审计补丁复验

官方最新正式版核验仍为 v1.19.33（发布于 2026-10-21），下载资产 SHA256 不变。包含出站审计 hook 和 CN 目标规则的候选再次通过上述 31/31 项；额外的真实 TCP/UDP 测试验证拒绝后续 UDP 目标以及实际 IP/拒绝审计，PostgreSQL 和 MySQL 的真实 Mihomo → proxynode → 面板链路均通过。原覆盖限制仍适用。

## 2026-11-15 OpenClash 分发内核补验

保留官方 v1.19.34 门槛，新增固定 OpenClash `core` 提交下的 Meta/Smart、master/dev 四组 Linux amd64-compatible 实际二进制。四组各通过同一 39 项（38 项使用对应内核，1 项为 Xray ML-DSA 验签对照），官方客户端同轮也通过 39/39。具体版本、摘要、CI 证据及路由器环境限制见 [OpenClash 互通验证](openclash-interop.md)。
