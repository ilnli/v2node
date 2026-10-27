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

## 完整验证已通过

运行环境切换到自动审批后，依赖下载已获批准。官方模块源连接发生 EOF/TLS 错误，改用 `GOPROXY=https://goproxy.cn` 后下载完成，保留 `go.sum` 校验。

先验证原锁定版本，再按用户授权将 `golang.org/x/sys` 从 `v0.48.0` 更新到 `v0.49.0`；其余查询的直接依赖未发现更新，修改版 Xray 依赖保持不变。以下结果均基于升级后的最终代码：

| 验证 | 结果 |
| --- | --- |
| `GOEXPERIMENT=jsonv2 go test -p 4 -count=1 ./...` | 全部通过；包括审计、配置、dispatcher 和 node 测试 |
| `GOEXPERIMENT=jsonv2 go test -p 4 -race -count=1 ./common/audit ./core/app/dispatcher` | 两个包均通过，无竞态报告 |
| `GOEXPERIMENT=jsonv2 go build -p 4 -trimpath -ldflags '-s -w -X github.com/wyx2685/v2node/cmd.version=0.1.0-audit' -o build_assets/proxynode .` | 构建通过 |
| `./build_assets/proxynode version` | 输出 `proxynode 0.1.0-audit` |
| `git diff --check` | 通过 |

产物：`build_assets/proxynode`，Linux amd64，约 77 MiB，按 `.gitignore` 留在本地，不纳入 Git。

先前文档中的网络权限阻塞已解除；当前不存在依赖下载或完整构建未验证的问题。

## 验证范围

测试验证配置容量限制、未启动节点的安全清理、鉴权用户归属、原始地址与嗅探域名保留及域名回退。面板接收接口尚未实现；实际节点协议连接、面板端幂等落库和完整部署验收尚未执行。

## Fork 发布与部署预检

2026-10-27：`ilnli/v2node` 的 main 历史已保留并接入本地改造，不使用强制推送。发布工作流和安装脚本通过以下预检：

- 6 项部署测试：fork 下载地址、SHA-256 失败保护、配置/缓存保留、amd64/arm64 选择及旧服务显式迁移顺序。
- Bash 语法检查、actionlint、全项目 Go 测试通过。
- 固定 geodata 发布版本 `202610262207` 的两个数据文件均通过 SHA-256 校验。
- Linux amd64 实际静态安装包构建、校验、临时目录安装与 `version` 执行通过；下载由测试 transport 映射到实际构建产物，没有安装系统服务。
- 独立部署/发布代码审查未发现重要问题。后续 GitHub CI 和真实 Release 验证另行记录。
