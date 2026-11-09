# Webhook

Webhook 让 nps 在发生事件时主动通知你的程序：客户端上下线、隧道和域名变更、登录失败、证书申请失败等，都以 JSON 用 HTTP POST 发到你指定的地址。默认关闭，设置 `webhook_url` 后启用，修改后[重载](/guide/server/operations.md)即生效，不用重启。

```ini
webhook_url=https://example.com/nps-events
webhook_secret=换成随机字符串
#webhook_timeout=5
#webhook_retries=3
#webhook_queue_size=1024
#webhook_report_interval=60
```

| 配置 | 含义 | 默认 |
| --- | --- | --- |
| `webhook_url` | 接收地址，留空关闭 | 空 |
| `webhook_secret` | HMAC-SHA256 签名密钥；留空不签名并告警 | 空 |
| `webhook_timeout` | 单次请求超时（秒） | `5` |
| `webhook_retries` | 失败重试次数，间隔从 1 秒起翻倍，最长 30 秒 | `3` |
| `webhook_queue_size` | 待发送队列长度；队列满时丢弃新事件并记录日志 | `1024` |
| `webhook_report_interval` | 全量上报间隔（秒），`0` 关闭 | `60` |

## 事件

| 类型 | 时机 | `data` |
| --- | --- | --- |
| `client_online`、`client_offline` | 客户端上下线 | 客户端 |
| `client_created`、`client_updated`、`client_deleted` | 客户端增删改（管理的客户端，不含公共和访客密钥） | 客户端 |
| `tunnel_created`、`tunnel_updated`、`tunnel_started`、`tunnel_stopped`、`tunnel_deleted` | 隧道变更 | 隧道 |
| `host_created`、`host_updated`、`host_started`、`host_stopped`、`host_deleted` | 域名变更 | 域名 |
| `tunnels_report` | 周期全量上报 | `tunnels` 和 `hosts` 列表 |

另外，日志里标记为推送的事件也会发到 webhook，事件名与[日志](/guide/server/logging.md?id=固定事件)相同：`auth_failed`、`login_failed`、`login_ok`、`ban_added`、`ban_removed`、`admin_action`、`limit_exceeded`、`quota_reached`、`target_down`、`target_up`、`listen_failed`、`config_applied`、`config_failed`、`update_available`、`update_applied`、`update_failed`、`cert_issued`、`cert_failed`、`cert_revoked`、`p2p_connected`、`p2p_failed`。它们的 `data` 是这一行日志的字段（键名同日志，数字是数字，错误是文本），另有 `level`、`module` 和 `msg`；密钥始终是 `[REDACTED]`。这类事件不经过日志级别过滤，但 `auth_failed`、`login_failed` 等仍按来源限频。

```json
{"id":"…","type":"auth_failed","time":"2026-11-08T03:17:14Z",
 "data":{"level":"WARN","module":"bridge","src_ip":"203.0.113.7","src_port":51802,"role":"client","carrier":"tcp","reason":"bad_key","err":"[REDACTED]"}}
```

Web 上保存隧道会重启它，因此依次收到 `updated`、`stopped`、`started`。事件不含密钥、密码和证书。

```json
{
  "id": "…",
  "type": "tunnel_created",
  "time": "2026-11-06T12:00:00Z",
  "data": {
    "id": 3, "client_id": 1, "mode": "tcp", "port": 8001,
    "target": "127.0.0.1:22", "remark": "ssh",
    "enabled": true, "from_client": false
  }
}
```

字段：

- 客户端：`id`、`remark`、`addr`、`version`
- 隧道：`id`、`client_id`、`mode`、`server_ip`、`port`、`target`、`remark`、`enabled`、`from_client`
- 域名：`id`、`client_id`、`host`、`location`、`scheme`、`target`、`remark`、`enabled`、`from_client`
- 全量上报的隧道额外有 `running`、`client_online`、`now_conn`、`inlet_flow`、`export_flow`；域名额外有后四项

`from_client` 为 true 表示条目由客户端配置发布，客户端离线后移除。空字段可能省略。

## 投递语义

- 按顺序投递，失败时重试。`id` 在重试时不变，接收方据此去重。
- 2xx 为成功。除 408、429 外的 4xx 不重试。不跟随重定向，3xx 算失败。
- nps 重启或 Webhook 配置变更时，未发出的事件不保留，可用全量上报对账。

## 请求头与签名

| 请求头 | 内容 |
| --- | --- |
| `X-NPS-Event-Id` | 事件 ID |
| `X-NPS-Event-Type` | 事件类型 |
| `X-NPS-Timestamp` | 本次尝试的 Unix 秒，每次重试更新 |
| `X-NPS-Signature` | `sha256=` 加 `HMAC-SHA256(secret, "<时间戳>.<请求体>")` 的十六进制；未设置密钥时没有此头 |

校验时使用**原始请求体**，并拒绝过旧的时间戳以防重放。

### Python

```python
import hashlib, hmac, time

def verify(secret: str, headers, body: bytes, max_age=300) -> bool:
    ts = headers.get("X-NPS-Timestamp", "")
    sig = headers.get("X-NPS-Signature", "")
    if not ts.isdigit() or abs(time.time() - int(ts)) > max_age:
        return False
    mac = hmac.new(secret.encode(), ts.encode() + b"." + body, hashlib.sha256)
    return hmac.compare_digest("sha256=" + mac.hexdigest(), sig)
```

### Go

```go
func verify(secret string, r *http.Request, body []byte) bool {
	ts := r.Header.Get("X-NPS-Timestamp")
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || math.Abs(float64(time.Now().Unix()-sec)) > 300 {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(r.Header.Get("X-NPS-Signature")))
}
```
