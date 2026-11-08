# 日志

nps 和 npc 的日志都是**一个事件一行**。默认格式是 logfmt（`log_format=text`），也可以设为 JSON（`log_format=json`）。

```
time=2026-11-08T03:12:45.120Z level=WARN event=auth_failed module=bridge src_ip=203.0.113.7 src_port=51234 role=client reason=unknown_key
time=2026-11-08T03:12:46.002Z level=INFO module=p2p peer=12 msg="punch started"
```

- 每行开头的字段固定，依次是 `time`（UTC，精确到毫秒）、`level`、`event`（只有固定事件才有）、`module`；接着是事件自己的字段，`msg` 放在最后，给人看。
- 字段值带空格、引号或换行时，会加引号并转义。任何外部输入都无法伪造出一行新日志。
- 密钥、口令、令牌、Cookie 一律不写入日志。
- 高频事件有限频：同一事件、同一来源每分钟最多写一行，下一行用 `suppressed=` 记录这期间省略了多少行。

## 级别

| 级别 | 用于 |
| --- | --- |
| `error` | 需要人工处理，例如端口打不开、证书快到期仍续期失败 |
| `warn` | 安全事件或服务降级 |
| `info` | 状态变化（上线、下线、配置生效）和策略拒绝 |
| `debug` | 单个连接的细节 |
| `trace` | 比 debug 更细，用于逐包排查 |

可以按模块单独设级别，例如 `log_level=info,p2p=debug,acme=warn`。nps 修改 `log_level` 后立即生效，不需要重启。

常见模块有：`bridge`（npc 接入）、`client`（npc）、`web`（管理面板）、`httpproxy`（域名代理）、`proxy`（端口隧道）、`p2p`、`acme`（自动证书）、`lib`（第三方库）、`nps`（尚未细分的旧日志）。

## 固定事件

事件名和字段只会增加，不会改名。webhook 推送的事件名与日志里的相同。

| event | 级别 | 主要字段 | 安全日志 | webhook |
| --- | --- | --- | --- | --- |
| `auth_failed` | warn | `src_ip`、`src_port`、`role`、`identity`、`reason` | 是 | 是 |
| `login_failed` / `login_ok` | warn / info | `src_ip`、`user`、`reason` | 失败的写 | 是 |
| `ban_added` / `ban_removed` | warn / info | `src_ip`、`scope`、`until` | 添加的写 | 是 |
| `client_online` / `client_offline` | info | `identity`、`src_ip`、`carrier`、`version`、`duration_ms` | | 是 |
| `session_takeover` | warn | `identity`、`src_ip` | | |
| `limit_exceeded` | warn | `identity`、`route`、`kind` | | 是 |
| `tunnel_denied` | info | `src_ip`、`route`、`reason` | | |
| `relay_failed` | info | `route`、`target`、`reason` | | |
| `listen_failed` | error | `route`、`addr`、`reason` | | 是 |
| `tunnel_*` / `host_*` | info | 条目（`created`、`updated`、`started`、`stopped`、`deleted`） | | 是 |
| `config_applied` / `config_failed` | info / warn | `keys`、`reason` | | 是 |
| `update_available` / `update_applied` / `update_failed` | info / info / warn | `from`、`to`、`reason` | | 是 |
| `source_updated` / `source_failed` | info / warn | `source`、`reason` | | |
| `cert_issued` | info | `domain`、`issuer`、`renewal` | | 是 |
| `cert_failed` | warn，距到期不足 7 天时为 error | `domain`、`renewal`、`remaining_s`、`reason` | | 是 |
| `cert_revoked` | warn | `domain`、`reason` | | 是 |
| `tls_handshake_failed` | debug | `src_ip`、`src_port`、`listener`、`reason` | | |
| `connected` / `connect_failed` / `disconnected` | info / warn / warn | npc：`server`、`carrier`、`reason`、`dur_ms` | | |
| `p2p_connected` / `p2p_failed` / `p2p_path_changed` | info / info / debug | `peer`、`path`、`reason`、`rtt_ms` | | 是 |
| `log_fallback` | warn | `reason`（日志文件打不开，已改写到 stderr） | | |

- `route` 对隧道是 `模式:端口`（如 `tcp:8080`），对域名条目是域名；`client`、`tunnel` 是编号。
- `src_ip` 是对端的真实 IP。经过受信代理时，取代理解析出的地址。
- `reason` 的取值是固定的短词，可以直接用来做统计。

第三方库（自动证书、HTTP 服务器、标准库日志）输出的内容会作为普通行写入，带 `module`，但不带 `event`。

## 安全日志与 fail2ban

设置 `security_log=/var/log/nps/security.log` 后，nps 会把安全事件（`auth_failed`、`login_failed`、`ban_added`）另写一份到这个文件，主日志的级别设置不影响它。

```
# /etc/fail2ban/filter.d/nps.conf
[Definition]
failregex = ^time=\S+ level=WARN event=(auth_failed|login_failed) .*\bsrc_ip=<HOST>(\s|$)
ignoreregex =

# /etc/fail2ban/jail.d/nps.local
[nps]
enabled  = true
filter   = nps
logpath  = /var/log/nps/security.log
maxretry = 10
findtime = 10m
bantime  = 1h
```

nps 自己也会按来源限速和封禁；fail2ban 只是系统层面的可选补充。

## 统计

JSON 格式可以直接交给 jq、Loki、Elasticsearch 等工具处理。用 logfmt 时也可以简单统计，例如：

```bash
grep -o 'event=auth_failed .*src_ip=[^ ]*' nps.log | grep -o 'src_ip=[^ ]*' | sort | uniq -c | sort -rn | head
```

## 输出与轮转

- 在终端里直接运行时，控制台输出改用便于阅读的彩色格式；写文件或被重定向时始终是 logfmt 或 JSON。
- `log=stdout|file|both|off`；以服务方式运行时，`stdout` 改为写文件。
- 日志文件按大小轮转（`log_max_size`，单位 MB），保留份数和天数分别由 `log_max_files`、`log_max_days` 控制，可选压缩（`log_compress`）。轮转出的文件名形如 `nps-20261108T031245.000.log`。
- 新建的日志文件权限为 `0640`（属主可写，同组可读，例如给运行 fail2ban 的管理组）。
- 日志文件无法创建或写入时，改写到 stderr，并记一条 `log_fallback`，不影响启动。
- npc 在 OpenWrt 上默认不写日志（`-log=off`）。
