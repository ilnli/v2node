# 节点审计上报接口（版本 1）

这是 proxynode 与 ProxyBoard 面板已实现的接口约定。现有流量和在线用户接口保持不变。请先完成面板数据库升级和管理页面部署，再启用节点 `Audit.Enabled`；原 V2Board 不支持此接口。

## 请求

`POST /api/v1/server/UniProxy/audit?node_type=v2node&node_id=3&token=<节点密钥>`

认证沿用现有节点接口。面板必须验证节点密钥及节点身份，不允许仅凭用户登录令牌调用。生产使用 HTTPS，访问日志应隐藏 token 查询参数。节点不跟随重定向，错误日志不输出带密钥的 URL。

`Content-Type: application/json`

```json
{
  "batch_id": "ee950cc4e1f1d3f016c21e7084ae57cc3",
  "target": "内部面板地址与节点ID的SHA256标识",
  "records": [
    {
      "user_id": 42,
      "node_id": 3,
      "timestamp_ms": 1793102400123,
      "destination": "203.0.113.8",
      "port": 443,
      "network": "tcp",
      "domain": "example.com",
      "protocol": "vless",
      "destination_ip": "203.0.113.8",
      "country_code": "US",
      "outcome": "allowed"
    }
  ]
}
```

- `batch_id`：随机 128 位 ID（32 个十六进制字符），重试及磁盘恢复保持不变。
- `target`：缓存路由标识，不是认证依据；不包含密钥。面板可忽略此字段。
- `user_id`：节点认证用户对应的面板数值 ID，不发送 UUID、密码或邮箱。
- `node_id`：面板节点 ID，应与已认证请求节点一致；节点协议另由 `protocol` 表示。不同面板和节点之间不可混淆。
- `timestamp_ms`：节点观察连接的 Unix 毫秒时间（UTC），并非面板接收时间。
- `destination`：客户端请求的原始 IP 或域名，不保证是最终出站解析的 IP。
- `domain`：嗅探到的域名，或原始目标中的域名；未知时省略。不保证能穿透 ECH 等隐藏域名的机制。
- `network`：`tcp` 或 `udp`；`protocol` 为节点协议，例如 `vmess`、`vless`、`trojan`。
- `destination_ip`：实际出站观测到的目标 IP，IPv4 或 IPv6；出站失败时可能是尝试连接的 IP，不代表已经建立连接。未取得实际 IP（例如域名在路由阶段被阻断，或通过远端代理解析）时省略，不把 DNS 候选地址当成已经连接的 IP。
- `country_code`：由节点本地 `geoip.dat` 对 `destination_ip` 查询得到的大写两字母国家代码；缺少目标 IP、资源不可用或没有匹配时省略，面板显示未知。不是用户来源国家。
- `outcome`：`allowed` 表示 TCP 拨号成功或 UDP 数据报已发送，不代表应用层请求成功；`blocked` 表示被规则阻断；`failed` 表示出站失败；`unknown` 表示未观测到最终出站结果（例如代理链或 DNS 专用出口）。旧节点可省略这三个新字段，面板按未知显示；旧磁盘批次继续按原格式上报。

单批最多 64 条，整个 JSON 最多 256 KiB。每批仅包含同一面板节点的记录。

面板审计页默认隐藏 `cp.cloudflare.com` 连通性检测记录，可关闭此筛选查看。筛选在查询分页前执行，不影响节点上报、面板保存或未指定该筛选的 API 查询。

## 成功确认与幂等

只有以下 HTTP 200 响应被视为完整接收成功：

```json
{"data":{"batch_id":"ee950cc4e1f1d3f016c21e7084ae57cc3","accepted":1}}
```

`accepted` 必须等于请求记录数；`batch_id` 必须原样匹配。面板在同一数据库事务内保存保留范围内的记录和批次去重标记，提交后才响应。去重身份为 `(node_type, node_id, batch_id)`，其中本接口只接受 `v2node`。重复批次不重复写入，但仍返回原批次条数；同 ID 不同内容返回 409。

ProxyBoard 默认最多保留 30 天，审计明细、索引、批次回执等合计最多 100 GB（十进制），容量优先。分区清理同时维护持久化的退役时间水位，避免回执删除后重试复活旧数据。已过期或已因容量清理的记录确认后不再入库，所以 `accepted` 表示按保留策略处理完成，不承诺每条永久保存；混合新旧记录仍只做整批确认。不要重置面板退役水位。

不采用部分确认。超时、网络错误、非 200、错误批次 ID、条数不符、畸形或超过 8 KiB 的响应都保留原批次并重试。节点一次只发送一个批次；重试间隔从 1 秒增长到最多 30 秒，单次请求超时 10 秒。确认丢失可能导致重发，因此传输不保证恰好一次。
