# Proxynode v0.2.0

## 升级前：REALITY 客户端参数

使用本版本的 REALITY 节点时，Mihomo 客户端必须支持混合密钥交换，并在代理配置中明确设置：

```yaml
reality-opts:
  public-key: <原公钥>
  short-id: <原 short-id>
  support-x25519mlkem768: true
```

未启用该设置的 Mihomo 默认 REALITY 配置会认证失败。先更新订阅模板/客户端配置再升级节点。本次只改造节点；面板订阅模板尚未同步修改。

## 更新内容

- Xray 内核改用自有 `ilnli/Xray-core`，同步官方 v26.10.21，保留 wyx2685 的 AnyTLS、TUIC 入站与其他扩展。
- Go 1.27.3，包含对应的 XTLS Vision 兼容修复，以及官方 REALITY、gRPC、Mux 和证书根更新。
- 支持面板下发 `tls_settings.alpn`；XHTTP HTTP/3 可设置为 `["h3"]`，未配置时保持原默认值。
- 增加真实 Mihomo v1.19.33 与 proxynode 的 VLESS 数据传输测试，作为普通 CI 和 Release 的必过检查；详见仓库 `docs/mihomo-interop.md` 的实际测试范围。
- 保留原有审计缓冲上限、容量满丢弃新请求和确认后清理规则。新安装审计仍默认关闭，须待面板接收接口就绪后启用。

安装和更新仍只下载 `ilnli/v2node` 的 Release，提供 Linux amd64/arm64 及 SHA256SUMS。TUIC 出站仍未实现；本次没有切换成其他内核。
