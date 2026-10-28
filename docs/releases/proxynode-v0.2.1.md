# Proxynode v0.2.1

- 管理脚本增加 `add-node`、`remove-node`、`list-nodes`，支持一次添加多个 v2node ID，同一进程可加载不同协议节点。
- `add-node` 可交互输入面板地址、ID 和密钥；密钥不回显，节点列表不显示密钥。
- 配置修改保留其他设置，拒绝重复/歧义操作，提供原子写入、并发锁和一份 0600 备份。保存后执行 `proxynode-manager restart` 确保生效。
- 首次安装支持 `--node-ids 101,102,103` 和重复 `--node-id`；无面板参数时使用空节点列表，便于之后添加。
- 安装和管理脚本不包含原项目安装计数、遥测或广告；只从 ilnli/v2node 更新，保留向自有面板发送的业务上报。

已有用户执行 `sudo proxynode-manager update --version proxynode-v0.2.1` 获取新脚本。不会覆盖现有配置或审计缓存。随后可运行 `sudo proxynode-manager add-node`。

内核与 v0.2.0 相同，仍使用 Xray-core v26.10.21。REALITY 的 Mihomo 客户端仍须设置 `reality-opts.support-x25519mlkem768: true`。
