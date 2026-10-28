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
| 管理命令 | `/usr/local/bin/pnode`（指向 `proxynode-manager`，长命令仍兼容） |
| 配置及 geoip/geosite | `/etc/proxynode/` |
| 审计缓存 | `/var/lib/proxynode/audit/` |
| 服务名 | `proxynode` |

已有配置原样保留，传入的新面板参数不会覆盖它。新建配置默认 `Audit.Enabled=false`；没有传配置或完整面板参数时生成空 `Nodes` 列表，不启动服务；执行 `pnode add` 添加后，再执行 `pnode enable` 和 `pnode start`。面板审计接口准备好后再开启审计。

```sh
sudo pnode update
sudo pnode update --version proxynode-v0.2.2
sudo pnode restart
sudo pnode log
```

升级先下载并校验整个包，再替换二进制和资源，保留配置与审计缓存。校验失败不替换现有程序。脚本不安装系统依赖，也不自动修改防火墙。

## 多节点添加、查看和删除

终端输入 `pnode` 即可打开中文数字菜单。无终端时输出帮助，不等待输入。快捷命令是 `pnode add`、`pnode list`、`pnode del`、`pnode restart` 和 `pnode update`；例如 `pnode add 101,102` 会继续提示面板地址和密钥，`pnode del 102` 删除指定 ID。

已有旧版本需先运行一次 `sudo proxynode-manager update`，安装新入口。`pnode` 使用相对符号链接，因此管理脚本升级后入口仍有效；安装时若该名称被其他程序占用会报错，不覆盖无关文件。原有长入口和长子命令保持兼容。

菜单也可用 `pnode menu --config FILE` 管理指定配置；该选项仅影响菜单中的节点操作，服务操作仍针对系统的 proxynode 服务。

首次安装可以用 `--node-ids 101,102,103` 或重复 `--node-id` 配置同一面板下的多个 v2node 项；协议在面板的每个项中设置，本地不填写协议名。

```sh
sudo pnode add
# 交互填写面板地址、101,102,103 和密钥；密钥输入不回显
sudo pnode list
sudo pnode add --api-host https://panel.example.com --node-ids 104,105
# 省略 --api-key 时，终端继续提示输入密钥
sudo pnode del --api-host https://panel.example.com --node-id 104
sudo pnode restart
```

自动化可显式传入 `--api-key KEY`；交互输入避免密钥进入 shell 历史。节点操作支持 `--config FILE`，默认 `/etc/proxynode/config.json`。不联网获取协议、检查凭据或报告安装信息，列表只输出节点 ID 和 API 地址。

同一面板地址（忽略末尾 `/`）和 ID 不允许重复。删除时若同一 ID 对应多个面板，必须指定 `--api-host`；批量操作任一 ID 无效则全部不写入。配置原有 Log/Audit、未知字段和其他节点保持不变。修改采用文件锁、原子替换，保留上一次完整配置 `config.json.bak`，配置和备份权限为 0600；备份包含密钥，应按配置文件管理。

命令不会主动操作系统服务；保存后执行 `restart` 确保新配置生效，不依赖文件监听自动重载。删除最后一个节点会留下空列表。旧版无参数安装留下的示例节点不会自动删除，应先用 `list-nodes` 确认并移除示例 ID。

## 不包含原项目计数与广告

原安装脚本曾请求 `api.v-50.me/counter`；自有安装脚本已不包含这项请求，也没有广告横幅、推广或捐赠菜单。自动化测试检查安装只请求本 fork 的安装包和校验文件，并检查本地节点管理不发起网络请求。许可、NOTICE 和源码归属保留。

节点向配置的自有面板提交用户流量、在线状态和已开启的审计数据仍保留，不能将其与原项目的安装计数混为一谈。规则数据与互通测试客户端由 CI 下载，不是安装时的遥测。

## 迁移原 v2node

```sh
sudo bash install.sh --migrate-v2node
```

发现旧配置但未指定迁移时会停止安装并提示。迁移会在新配置不存在时复制 `/etc/v2node/config.json`，安装新程序，然后停用旧 `v2node` 服务再启动 `proxynode`，防止端口冲突。旧二进制和配置不删除；新服务启动失败时可查看日志，必要时停止新服务并手动恢复旧服务。`script/v2node.sh` 是转发到新管理命令的兼容入口。

## 无服务安装与暂存验证

```sh
bash install.sh --prefix /tmp/proxynode-staging --no-service --version proxynode-v0.2.2
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
