# Repository Guidelines

## 内核与依赖约束

- 本项目只使用 **XTLS/Xray-core** 作为代理内核。不要把 Xray、V2Ray、V2Fly 混为一谈，也不要替换成 V2Ray/V2Fly 或 sing-box 内核。
- 当前通过 `go.mod` 的 `replace` 使用 `ilnli/Xray-core`（从 `wyx2685/Xray-core` fork，已同步官方 v26.10.29）。工具库依赖（例如 `v2fly/ss-bloomring`、`sagernet/sing`）不等于运行对应项目的内核。
- 不得直接删除 `replace`：当前代码使用 fork 新增的 AnyTLS/TUIC 包和配置类型。差异及依据见 `docs/xray-core-comparison.md`。
- 用户已授权建立并维护自有 Xray fork；后续继续同步正式版及必要补丁。保留许可、上游来源和可追溯提交，避免无关重写。

## Mihomo VLESS 升级门槛

- 升级 Xray、REALITY、uTLS，或影响 TLS 内部结构的 Go 工具链版本前，必须确认当时 **Mihomo 最新正式版**，记录版本、日期和校验值。
- 必须用真实 Mihomo 进程连接真实 proxynode，验证双方支持且参数合法的 VLESS 配置组合；至少覆盖 TCP/TLS/REALITY、Vision、WS、gRPC、HTTPUpgrade、XHTTP、VLESS Encryption 和 UDP/XUDP，详见比较文档。
- 配置加载、编译、启动和普通单元测试通过不能替代真实连接与双向数据传输验证。失败、未测和不支持必须明确区分，不得宣称任意组合全部兼容。
- 互通套件见 `script/test-mihomo.sh` 与 `docs/mihomo-interop.md`，CI 和发布必须通过该套件；未覆盖组合不可声称已验证。Go 1.27.3+ 的 Vision 兼容修复需要特别核对。

## 开发与发布

- 沿用 Go、`gofmt`、`GOEXPERIMENT=jsonv2`；常规测试为 `go test ./...`，核心并发检查见 CI。
- 审计上限由整个进程共享：活跃缓冲预算 256 MiB、磁盘最多 1 GiB；容量满时丢弃新记录，保留旧记录。
- 只针对新增变更和明确问题验证，不反复审查已验证旧代码。
- 发布目标是 `ilnli/v2node`；`proxynode-vX.Y.Z` 标签触发自有 Release。部署脚本不得回退下载原项目二进制。
