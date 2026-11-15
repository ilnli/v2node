# OpenClash 分发内核互通验证

核验日期：2026-11-15。来源为 [vernesong/OpenClash 的 core 分支](https://github.com/vernesong/OpenClash/tree/core)，固定到提交 [`5810424b40be9324db7595514882f324c2258798`](https://github.com/vernesong/OpenClash/tree/5810424b40be9324db7595514882f324c2258798)（2026-11-14 20:15:31 UTC）。下载路径包含完整提交号，不随分支后续更新漂移。

本快照的 `master` 和 `dev` 都分发 alpha 内核，不能将 `master` 等同于 Mihomo 官方稳定版。测试使用每个目录里的 `clash-linux-amd64-compatible.tar.gz`（GOAMD64=v1），真实执行文件名为 `clash`，版本信息标识为 Mihomo。官方 Mihomo v1.19.34 原有门槛继续保留。

| 来源目录 | 实际版本 | Go / 构建时间 UTC | 实测结果 |
| --- | --- | --- | --- |
| `master/meta` | `alpha-gbc76c13` | 1.26.9 / 2026-10-12 03:44:14 | 39/39 通过 |
| `dev/meta` | `alpha-g184857c` | 1.26.11 / 2026-11-14 20:08:21 | 39/39 通过 |
| `master/smart` | `alpha-smart-gdaf3ad0` | 1.26.9 / 2026-10-12 03:44:16 | 39/39 通过 |
| `dev/smart` | `alpha-smart-gc4df732` | 1.26.11 / 2026-11-14 20:08:20 | 39/39 通过 |

## 校验值

压缩包 SHA256 已与同一固定提交中各目录的 `checksums.txt` 核对；下表二进制 SHA256 是从 tar.gz 提取的原始 UPX 压缩可执行文件，也是下述历史 CI 的执行文件。启动异常修复后的执行文件另见报告中的 `mihomo_sha256`。

| 来源目录 | 压缩包 SHA256 | 二进制 SHA256 |
| --- | --- | --- |
| `master/meta` | `367130a1f3a99fbcd1a6acc1f2a5fc53ce43bef5af4f3278854de27694ccfa0c` | `7aaed1082d1239ffbf1259b1f0b6839fd36c63f312c330d0dd2a8ea7f8e70d3d` |
| `dev/meta` | `a4ad618f50a64a0c6f414ddd19e808e122efe5fd8e54617d1f3efaa8e64f233b` | `572e382cbe0a23f2f3995f0d94ee4d529a9e7f0ab085f7ea6acd81e1c39eafd7` |
| `master/smart` | `dbcff108ee8b83cbeeaf31cab0ba1e2f670e55765d8affc36df7faaf7400abc4` | `9ac94e76022ec4d0787e164d6d33f59eca3f5572c3ceedcb6255cd9597f6fcb1` |
| `dev/smart` | `dd12465b256ec8a40bebba2774a8caaeea809308c85cf577dd1ffe34433901ca` | `9b7de25d2127e6176ad7714d1c8eedf9827686d5ea6e55f61c15b71af4fa4545` |

## 执行与范围

沿用[官方客户端互通套件](mihomo-interop.md)的同一 39 项：其中 38 项使用所选 Mihomo/OpenClash 二进制，1 项使用匹配服务端 fork 的 Xray 客户端完成 ML-DSA 验签正反例。不能把该对照项称作 OpenClash 验签支持。

```bash
MIHOMO_SOURCE=openclash-dev-meta bash script/test-mihomo.sh --output /tmp/openclash-dev-meta-results
```

`MIHOMO_SOURCE` 可选 `official`（默认）、`openclash-master-meta`、`openclash-dev-meta`、`openclash-master-smart`、`openclash-dev-smart`。每次验证压缩包 SHA256、实际版本 token，报告记录不可变下载 URL、压缩包及客户端/服务端二进制 SHA256。CI 对四种来源分别运行全部用例；某种失败不会阻止收集其他来源结果，任一失败会让该验证步骤失败。

## UPX 启动异常处理（2026-11-15）

合并后 [CI 43686907058](https://github.com/ilnli/v2node/actions/runs/43686907058) 的前两次运行中，`master/meta` 分别在 `ws-tls` 和 `xhttp-tls-packet-up-download` 用例启动时退出 127，客户端日志为空，尚未进行传输。第二次运行的 `ws-tls` 已通过；两轮其他四组矩阵均为 39/39。原始可执行文件带有 UPX 3.95 加载器；[UPX 文档](https://github.com/upx/upx/blob/devel/doc/upx-doc.html) 说明加载器内部错误使用退出码 127，现象指向该启动路径，未取得失败时的系统调用跟踪以确定底层触发点。

套件现在先验证同一 OpenClash 原始包，再使用固定的 [UPX 5.3.0](https://github.com/upx/upx/releases/tag/v5.3.0) 执行 `upx -d`，移除运行时解压加载器后测试同一内核。UPX Linux amd64 下载包 SHA256 固定为 `79cea8a50b6fa102d10c7b79a419cd77dead3cc2d78661de2dc5e5815dc505c1`；下载、校验或解压失败仍直接失败。报告的 `client_preparation` 标记为 `upx-5.3.0-decompressed`，`client_archive_sha256` 保留 OpenClash 原始包校验值，`mihomo_sha256` 记录实际运行的解压后二进制。官方 Mihomo 不做该处理，标记为 `original`。

同一 `master/meta` 二进制的本地对照中，仅执行 `-v` 各 250 次：原始 UPX 文件出现两次退出 127 且输出为空，解压后 250 次全部成功，成功运行的版本输出完全相同。这在不加载代理配置、不进行网络连接的情况下复现了启动故障，并验证了移除运行时加载器后的结果。

此处理验证内核协议互通，不再覆盖原始 UPX 加载器在目标系统上的启动可靠性。没有重编译或更换内核，也没有重试、跳过或放宽失败用例。以下是处理前已通过的历史基线，不能代替修复后的 CI 结果。

[CI 43673311124](https://github.com/ilnli/v2node/actions/runs/43673311124) 在节点提交 [`48e2ee0a4d5b`](https://github.com/ilnli/v2node/commit/48e2ee0a4d5beedd9f368375cbcb357f8aecf409) 完成：四组各 **39/39** 通过，官方 Mihomo v1.19.34 同轮 **39/39** 通过；全包 Go 测试、核心 race、静态构建与真实 worker 生命周期检查也全部通过。没有失败项，没有跳过或放宽任何互通断言。

OpenClash 报告 ZIP（artifact `14145129023`）SHA256：`b75d6b632cc708692ff075d1fc688746bfeadd92fe6361a21aa06a2441e3b63e`；官方客户端对照 ZIP（artifact `14145064166`）SHA256：`4c84d24cf58a154b404f53b94a76dac2c1c47c14a8a987f828b34a726948e1ae`。两者均与 GitHub API artifact digest 核对一致，报告中的四个 OpenClash 二进制 SHA256 与上表一致。

五组使用同一个实际 proxynode 二进制，SHA256 为 `dd20478a0ddf8256820362e9fe2dc026a1f15fd29cc396dc9a87d47a450932c6`；服务端 Xray fork 仍固定在 `34846a645f348f69bea579ffc4daf47399c281e3`（v26.11.9），未为 OpenClash 修改内核。Xray 对照客户端 SHA256 为 `858e574c4d5738ca84a40cf0bc35fc75d8c356e631e4267e3bf9b348fbb9f2af`。

四组均通过无 TLS 的 Encryption + Vision、REALITY 最低版本接受/拒绝、错误证书固定值拒绝、XHTTP XMUX 并发与独立下载通道检查。最低版本用例只证明 `1.8.2` 门槛接受、`26.3.27` 门槛拒绝，不能由此推断各内核精确的握手版本字段。

仅验证 Linux x86_64 用户态内核协议互通。未验证 ARM/MIPS/LoongArch 路由器二进制、OpenWrt 固件、OpenClash LuCI 配置加工、TUN/透明代理与 DNS 劫持、Smart 策略组学习或真实路由器资源限制。没有安装或升级生产路由器。
