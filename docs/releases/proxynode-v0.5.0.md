# Proxynode v0.5.0

升级 Xray 安全基线，补齐 REALITY 配置与拦截原因审计，并扩大 Mihomo / OpenClash 的真实互通验证。

## 更新内容

- 自有 Xray fork 同步至 v26.11.9，保留 AnyTLS、TUIC 和出站审计；监听器显式按配置启用或禁用 MPTCP。
- REALITY 最低客户端版本由面板配置，兼容 `min_client_ver` / `minClientVer`，移除节点写死的 `0.0.1`。新增回落上传/下载限速字段映射，保留独立目标、多 SNI、多 ShortID 和 ML-DSA 参数。
- 拦截审计上报实际命中的路由规则类别、规则 ID 和条件；最终出口检查拒绝单独标明来源。历史记录不自动补齐原因。
- 无公网 IPv6 时，让默认直连出口与 DNS 使用一致的 IPv4 策略。
- 增补无 TLS 的 VLESS Encryption + Vision、REALITY 最低版本接受/拒绝、ML-DSA 验签对照、证书固定拒绝探针、XHTTP XMUX 与独立下载通道验证。
- OpenClash 测试先校验固定下载包，再通过固定版本 UPX 解包，规避原始压缩加载器偶发退出 127；保留内核版本与原包校验值，报告另记实际执行文件摘要，不跳过失败用例。

## 升级

建议先更新配套 ProxyBoard 主线并完成 `0008_audit_block_reason` 迁移，以展示新增审计理由和配置项。面板代码、订阅导出及登录保持修复需单独部署，不包含在节点安装包内。

独立节点执行：

```sh
sudo pnode update --version proxynode-v0.5.0
```

面板托管主机在更新时指定 `proxynode-v0.5.0`。安装包提供 Linux amd64、arm64 及 `SHA256SUMS`，沿用现有配置；发布版本不会自动升级线上节点或启用新的路由规则。

## 客户端兼容与验证

发布前实现提交 `40a90a124acf57bc64dcf50b2738853bc9c6ba43` 的 [完整 CI](https://github.com/ilnli/v2node/actions/runs/43689943173) 已通过 Go 全套测试、核心竞态检测、构建、安装脚本与真实 worker 生命周期检查。官方 Mihomo v1.19.34 和固定 OpenClash 快照的四组 Meta/Smart 内核各 **39/39** 通过，包含此前出现启动异常的两项用例。

每组 39 项中，38 项使用所选 Mihomo/OpenClash 内核，1 项由同一 fork 的 Xray 客户端验证 ML-DSA 正反例。OpenClash 结果覆盖解包后的 Linux amd64 用户态内核，不代表原始 UPX 加载器或路由器环境验证。发布流水线另行执行 Go、竞态、worker 与官方 Mihomo 门槛，通过后构建两种架构安装包并验证校验和，全部附件上传完成才公开 Release。

- Mihomo REALITY 配置需要 `support-x25519mlkem768: true` 和支持该 key share 的指纹；配套 ProxyBoard 会注入此选项，手工配置需自行核对。
- 最低版本为可选项。测试客户端在门槛 `26.3.27` 下会被拒绝，即使启用了上述 key share；不要把该值作为兼容这些客户端的默认设置。
- Shadowrocket iPhone **2.2.92（3445）真机验收仍待完成**；面板订阅格式回归与 Mihomo 测试不能替代该结果。
- ARM64 安装包完成交叉编译，不等于 ARM64 客户端互通实测；IPv6、外部 CDN/反代、全部参数排列及 OpenClash 路由器功能仍未全面验证。

具体版本、校验值、配置组合和限制见 [Mihomo 互通说明](https://github.com/ilnli/v2node/blob/proxynode-v0.5.0/docs/mihomo-interop.md) 与 [OpenClash 互通说明](https://github.com/ilnli/v2node/blob/proxynode-v0.5.0/docs/openclash-interop.md)。
