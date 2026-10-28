# Proxynode v0.2.2

- 新增简短入口 `pnode`，直接输入打开中文数字菜单。
- 支持 `pnode add`、`pnode list`、`pnode del`；节点 ID 可直接跟在命令后，如 `pnode add 101,102` 和 `pnode del 102`。
- 菜单可查看/添加/删除节点、重启/启动/停止服务、查看状态和日志、更新程序。
- 安装和更新自动创建入口，保留原 `proxynode-manager` 及长子命令；不覆盖同名无关程序。
- 继承配置备份、密钥不回显、不向原项目计数上报和无广告行为。内核与依赖不变。

旧版本执行一次 `sudo proxynode-manager update` 后，以后直接使用 `sudo pnode`；root 用户可直接输入 `pnode`。

REALITY 的 Mihomo 客户端仍需设置 `reality-opts.support-x25519mlkem768: true`。
