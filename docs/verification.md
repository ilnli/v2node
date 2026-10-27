# 本地验证记录

日期：2026-10-27。所有测试使用合成记录、临时缓存目录和内存 HTTP transport，没有连接生产面板或数据库。

## 已通过

使用本机现有 Go 1.27.3：

```sh
GO111MODULE=off go test -race -count=1 -v ./common/audit
```

9 项主测试通过，其中 HTTP 确认测试包含 7 个响应场景；竞态检测没有报告问题。覆盖：

- 内存达到阈值后落盘、达到总容量后拒绝最新批次，旧记录仍按顺序上报。
- 不匹配的确认不删除数据；成功确认清理磁盘文件，并在积压排空后恢复内存模式。
- 文件写入失败保留旧数据；同一缓存目录拒绝并发进程持有。
- 正常关闭及重开后恢复队列顺序；已经确认的批次不恢复。
- 上报失败重试保持批次 ID；800 条并发记录正常关闭后均能恢复。
- 饱和时丢弃新增的 64 条记录，保留最早的 64 条，并计数。
- 节点身份、认证参数和请求数据；拒绝错误批次 ID、部分确认、旧版响应、缺失确认、HTTP 500 和超大响应。

58 个 Go 源文件通过语法解析；`git diff --check` 通过。一次独立代码审查未发现尚未解决的重要问题；审查覆盖两个 dispatcher 入口、用户/节点归属、缓存与退出流程。

## 尚未通过完整构建验证

尝试过 `GOEXPERIMENT=jsonv2 go test ./...` 和 `GOEXPERIMENT=jsonv2 go build -trimpath -o /tmp/proxynode-build .`。由于本机缺少修改版 Xray、Viper、Cobra 等依赖，包初始化/构建阶段未完成；不是完整测试通过。

普通依赖下载失败于 `proxy.golang.org` DNS socket 权限（`operation not permitted`）。用户已允许下载和升级依赖，但运行环境仍拒绝网络访问；提权下载请求亦被自动审批拒绝（`sandbox_approval: false`）。没有更改 `go.mod` / `go.sum`，也没有绕过运行环境限制。

尚待运行：`conf` 配置测试、`node` 部分启动清理测试、dispatcher 用户/目标/域名测试、全项目测试及生产二进制构建。语法检查和代码审查不能替代这些验证。

## 网络恢复后的验证

在项目根目录执行：

```sh
GOEXPERIMENT=jsonv2 go mod download
GOEXPERIMENT=jsonv2 go test ./...
GOEXPERIMENT=jsonv2 go test -race ./common/audit ./core/app/dispatcher
GOEXPERIMENT=jsonv2 go build -trimpath -o build_assets/proxynode .
```

用户允许更新依赖。建议先验证当前锁定版本，再评估补丁/次版本更新并重跑上述检查；保留上游修改版 Xray 的兼容关系。

面板接收接口尚未实现；实际节点协议连接、面板端幂等落库和完整部署验收也尚未执行。
