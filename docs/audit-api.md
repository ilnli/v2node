# 节点审计上报接口（版本 1）

这是节点端已实现、面板端待实现的接口约定。现有流量和在线用户接口保持不变。

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
      "protocol": "vless"
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

单批最多 64 条，整个 JSON 最多 256 KiB。每批仅包含同一面板节点的记录。

## 成功确认与幂等

只有以下 HTTP 200 响应被视为完整接收成功：

```json
{"data":{"batch_id":"ee950cc4e1f1d3f016c21e7084ae57cc3","accepted":1}}
```

`accepted` 必须等于请求记录数；`batch_id` 必须原样匹配。面板应在同一数据库事务内保存记录和批次去重标记，提交后才响应。建议去重键为 `(node_type, node_id, batch_id)`。重复批次不重复写入，但仍返回原批次条数；去重标记应覆盖可能的重试/缓存保留周期，不能短于未确认批次可能存在的时间。

不采用部分确认。超时、网络错误、非 200、错误批次 ID、条数不符、畸形或超过 8 KiB 的响应都保留原批次并重试。节点一次只发送一个批次；重试间隔从 1 秒增长到最多 30 秒，单次请求超时 10 秒。确认丢失可能导致重发，因此传输不保证恰好一次。
