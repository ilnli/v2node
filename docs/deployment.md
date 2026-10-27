# 部署与发布

## 安装来源

源码及部署脚本：`https://github.com/ilnli/v2node` 的 `main` 分支。

二进制仅来自本 fork 的 GitHub Release：

- `proxynode-linux-amd64.tar.gz`
- `proxynode-linux-arm64.tar.gz`
- `SHA256SUMS`

默认下载 `releases/latest/download/...`；`--version proxynode-vX.Y.Z` 改为对应固定版本。不回退到 `wyx2685/v2node`。管理命令更新自身时也只请求本 fork 的安装脚本。

## 安装与更新

需要 Linux amd64/arm64、Bash、curl、tar、sha256sum、Python 3。系统服务支持 systemd 和 OpenRC。

```sh
curl -fL https://raw.githubusercontent.com/ilnli/v2node/main/script/install.sh -o install.sh
sudo bash install.sh --api-host https://panel.example.com --node-id 1 --api-key '节点密钥'
```

密钥也可预先写进仅 root 可读的配置文件，再使用 `--config /path/to/config.json`，避免密钥出现在命令参数中。

安装位置：

| 内容 | 路径 |
| --- | --- |
| 节点程序 | `/usr/local/bin/proxynode` |
| 管理命令 | `/usr/local/bin/proxynode-manager` |
| 配置及 geoip/geosite | `/etc/proxynode/` |
| 审计缓存 | `/var/lib/proxynode/audit/` |
| 服务名 | `proxynode` |

已有配置原样保留，传入的新面板参数不会覆盖它。新建配置默认 `Audit.Enabled=false`；没有传配置或完整面板参数时只生成模板，不启动服务，填好配置后执行 `proxynode-manager enable` 和 `proxynode-manager start`。面板审计接口准备好后再开启审计。

```sh
sudo proxynode-manager update
sudo proxynode-manager update --version proxynode-v0.1.0
sudo proxynode-manager restart
sudo proxynode-manager log
```

升级先下载并校验整个包，再替换二进制和资源，保留配置与审计缓存。校验失败不替换现有程序。脚本不安装系统依赖，也不自动修改防火墙。

## 迁移原 v2node

```sh
sudo bash install.sh --migrate-v2node
```

发现旧配置但未指定迁移时会停止安装并提示。迁移会在新配置不存在时复制 `/etc/v2node/config.json`，安装新程序，然后停用旧 `v2node` 服务再启动 `proxynode`，防止端口冲突。旧二进制和配置不删除；新服务启动失败时可查看日志，必要时停止新服务并手动恢复旧服务。`script/v2node.sh` 是转发到新管理命令的兼容入口。

## 无服务安装与暂存验证

```sh
bash install.sh --prefix /tmp/proxynode-staging --no-service --version proxynode-v0.1.0
```

此模式把所有文件安装到指定前缀下，不操作系统服务。可用于检查发布包；直接部署且自行管理进程时使用 `--no-service`。

## 发布流程

`main` 推送和 Pull Request 运行 Go 测试、审计竞态检测、安装脚本测试及构建。推送 `proxynode-vX.Y.Z` 标签会触发 `Release Proxynode`：

1. 确认标签提交属于 `main`，执行测试。
2. 构建 Linux amd64/arm64 静态二进制，打包脚本、配置、许可和已校验的规则数据。
3. 合并并验证 SHA-256 清单，上传到本仓库的草稿 Release。
4. 所有附件上传完成后公开 Release，并设置为 latest。

```sh
git switch main
git pull --ff-only
git tag proxynode-v0.1.1
git push origin proxynode-v0.1.1
```

使用 GitHub 自带 `GITHUB_TOKEN`，无需新建发布密钥。只有发布作业获得 `contents: write`；测试和构建只读。工作流限制发布目标为 `ilnli/v2node`。本次不发布容器镜像；Dockerfile 保留供自行构建。

规则数据固定在 `script/download-geodata.sh` 指定的发布版本，由 CI 下载并校验后装入本 fork 的安装包。客户端部署不请求上游 v2node 的任何二进制。
