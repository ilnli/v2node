# OpenClash 分发内核互通验证

核验日期：2026-11-15。来源为 [vernesong/OpenClash 的 core 分支](https://github.com/vernesong/OpenClash/tree/core)，固定到提交 [`5810424b40be9324db7595514882f324c2258798`](https://github.com/vernesong/OpenClash/tree/5810424b40be9324db7595514882f324c2258798)（2026-11-14 20:15:31 UTC）。下载路径包含完整提交号，不随分支后续更新漂移。

本快照的 `master` 和 `dev` 都分发 alpha 内核，不能将 `master` 等同于 Mihomo 官方稳定版。测试使用每个目录里的 `clash-linux-amd64-compatible.tar.gz`（GOAMD64=v1），真实执行文件名为 `clash`，版本信息标识为 Mihomo。官方 Mihomo v1.19.34 原有门槛继续保留。

| 来源目录 | 实际版本 | Go / 构建时间 UTC |
| --- | --- | --- |
| `master/meta` | `alpha-gbc76c13` | 1.26.9 / 2026-10-12 03:44:14 |
| `dev/meta` | `alpha-g184857c` | 1.26.11 / 2026-11-14 20:08:21 |
| `master/smart` | `alpha-smart-gdaf3ad0` | 1.26.9 / 2026-10-12 03:44:16 |
| `dev/smart` | `alpha-smart-gc4df732` | 1.26.11 / 2026-11-14 20:08:20 |

## 校验值

压缩包 SHA256 已与同一固定提交中各目录的 `checksums.txt` 核对；二进制 SHA256 是解包后实际执行文件的值。

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

目前已确认下载校验、实际版本、脚本选择和版本不匹配拒绝；真实传输结果待本次 CI 完成后记录，不能用官方版本的通过结果替代。

仅验证 Linux x86_64 用户态内核协议互通。未验证 ARM/MIPS/LoongArch 路由器二进制、OpenWrt 固件、OpenClash LuCI 配置加工、TUN/透明代理与 DNS 劫持、Smart 策略组学习或真实路由器资源限制。没有安装或升级生产路由器。
