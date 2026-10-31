# CN 目标拦截实施计划

> 在当前会话使用 subagent-driven-development 实现；沿用现有 Xray 上游版本和 Go，在自有 fork 增加出站审计补丁。

**Goal:** 允许 CN 来源用户连接，阻止已启用节点代理访问 CN 目标。

**Architecture:** 复用面板 block_ip 路由（geoip:cn）与节点 route_id 勾选。让 IP 拦截规则先于 DNS/转发/默认路由执行，使用 IPOnDemand 解析域名；按节点为直连 freedom 出口增加相同的 finalRules，覆盖真实解析地址及 UDP 多目标数据包。Docker 打包与安装发行包一致的固定版 geodata。面板添加创建 CN 规则预设的入口，审计追加字段由数据库迁移 0007 提供。

**Tech Stack:** Go / Xray-core、自有 fork 的既有 finalRules、Vue 3 / FastAPI。

## 约束与取舍
- 仅匹配目的地址；不创建来源 IP 条件，不改监听和身份认证。
- CN 按所带 GeoIP 数据定义；不额外以中国企业域名或 .cn 后缀扩大范围。
- 复用现有路由开关；新增独立全局防火墙配置会与面板管理重复，因此暂不引入。
- 普通未配置 IP 拦截的节点保持既有路由和出口行为。
- 代理链在本节点按请求目标作路由拦截；直连出口另加最终地址检查。不是宿主机所有进程的防火墙。
- 缺失/无效 GeoIP 数据必须报错，不静默跳过拦截。

## Tasks
- [x] core/custom_test.go: 使用合成 CN IPv4/IPv6 geodata 与实际 Xray router，证明 CN 目的地被阻断、CN 来源访问非 CN 可通、域名解析匹配、同进程节点隔离、DNS/默认路由不能抢先匹配。
- [x] core/custom.go / core/outbound.go: 拦截优先级、按需 DNS、直连 finalRules 和按节点出口作用域；对 UDP 多目标执行真实数据流验证。
- [x] Dockerfile: 调用已有固定版本下载校验脚本，配置 XRAY_LOCATION_ASSET 并打包数据；记录原生安装不变。
- [x] ProxyBoard 路由管理增加 CN 预设，复用现有 API 和节点路由勾选；补充操作说明及对应测试。
- [x] 执行 Go 常规测试、相关并发检查、真实连接测试、前端构建；一次独立审查，修正重要问题并提交。

## Review Focus
- domain / IPv4 / IPv6 / TCP / UDP 目标检查与来源 IP 完全分离。
- 自定义默认出口和 UDP DNS 不覆盖 deny；多个节点互不影响。
- DNS 返回实际 CN 地址及 UDP 后续报文不会绕过直连 finalRules。
- 托管 manifest 与独立拉取使用相同的 route payload。

## 追加：实际出站审计（用户 2026-10-30 补充）
- 保留原始 destination/domain；新增可选 destination_ip、country_code、outcome（allowed/blocked/failed/unknown）。旧节点/旧批次继续接收，保持幂等哈希兼容。
- Xray fork 增加 context 范围的出站元数据回调，在 freedom 实际拨号/UDP 写入及 finalRules 拒绝时通知。代理链不伪造最终 IP，路由拒绝无实际连接时可为空。
- proxynode 在实际事件处排入原有有界审计队列；每会话有界去重避免 UDP 每包产生审计。GeoIP 本地查询，无法识别显示未知。
- 面板增加字段迁移、IP 搜索与结果筛选；前端默认 exclude_domain=cp.cloudflare.com，可开关。过滤发生在查询分页之前，不影响接收、保存和其他 API 查询。
- 使用 subagent-driven-development 将互不重叠的 Xray hook、面板、GeoIP/Docker 子任务并行实施。最终执行一次跨仓库审查。
- 内核补丁只增加观测，不升级上游版本/工具链；仍执行已有真实 Mihomo 互通套件验证。

## 实施结果
- 新增审计字段及迁移兼容旧节点、旧批次和已有分区；实际 PostgreSQL/MySQL 测试通过。
- 真实直连 TCP/UDP 与 UDP 后续目标测试通过，Mihomo 完整 31 项互通通过；节点全量测试及审计/核心 race 通过。
- 独立审查修复 direct 别名遗漏及代理链中间地址误报，修复已定向复核。
- 浏览器自动化受当前 Chrome 沙箱限制，未声称通过；前端 21 项测试及生产构建通过。
