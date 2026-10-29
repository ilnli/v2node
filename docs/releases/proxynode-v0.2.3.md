# Proxynode v0.2.3

- 将自有 Xray-core fork 同步至官方 **v26.10.29**，固定提交 `3c160b27cb42435487f273c095b31271e812f47f`，保留 AnyTLS/TUIC 和 Shadowsocks 扩展。
- 纳入 Freedom 阻断延时、WebSocket 正常关闭、DNS 响应大小检查、入站初始化顺序及 Geodata 等上游修复。
- Go 工具链保持 **1.27.3**。审计队列容量、满额丢弃新记录的策略和面板接口不变。
- VLESS Encryption 无须因本次升级而开启：TLS/REALITY 配置仍可使用 `none`。上游 Xray 出站收紧了完全关闭加密时的私网地址例外判断。
- 上游 XDRIVE 更新包含在依赖中，但本版本未新增面板适配或宣称 Mihomo 支持 XDRIVE。

## 验证

2026-10-29 核验当前 Mihomo 最新正式版仍为 **v1.19.33**，官方压缩包 SHA256 与固定值一致。静态发布候选通过 **31/31** 项真实互通，覆盖 TCP/TLS/REALITY、Vision、WS、gRPC、HTTPUpgrade、XHTTP、VLESS Encryption，以及每项双向 TCP、内层 TLS 和 UDP/XUDP 传输。完整 Go 测试、审计/分发器竞态检查及 18 项部署脚本测试通过。未覆盖组合不作兼容承诺。

## 更新

```bash
sudo pnode update
pnode version
sudo pnode status
```

需要固定版本时使用 `sudo pnode update --version proxynode-v0.2.3`。更新下载本 fork 的二进制并保留已有配置。

REALITY 的 Mihomo 客户端仍需设置 `reality-opts.support-x25519mlkem768: true`，并使用支持相应 key share 的指纹（测试使用 `chrome`）。配置覆盖范围与限制见 [Mihomo 互通说明](../mihomo-interop.md)。
