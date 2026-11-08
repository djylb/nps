# 日志

nps 和 npc 的日志都是**一个事件一行**。写文件或被重定向时默认是 logfmt（`log_format=auto`，在终端里显示为彩色），也可以固定为 logfmt（`text`）或 JSON（`json`）。

```
time=2026-11-08T03:12:45.120Z level=WARN event=auth_failed module=bridge src_ip=203.0.113.7 src_port=51234 role=client reason=unknown_key
time=2026-11-08T03:12:46.002Z level=INFO module=p2p peer=12 msg="punch started"
```

- 每行开头的字段固定，依次是 `time`（UTC，精确到毫秒）、`level`、`event`（只有固定事件才有）、`module`；接着是事件自己的字段，`msg` 放在最后，给人看。
- 字段值带空格、引号或换行时，会加引号并转义。任何外部输入都无法伪造出一行新日志。
- 密钥、口令、令牌、Cookie 默认不写入日志（写成 `[REDACTED]`）。开发调试时可以设 `log_secrets=true`（npc 用 `-log_secrets`）让它们原样出现，开启时会先记一条警告；生产环境不要开。
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

模块：`server`（nps 进程与隧道）、`bridge`（npc 接入）、`p2p`、`httpproxy`（域名代理）、`proxy`（端口隧道）、`conn`、`web`（管理面板）、`store`（数据存储）、`config`、`install`（安装与更新）、`geo`（Geo 数据）、`acme`（自动证书）、`crypt`、`clock`、`webhook`、`client`（npc）、`lib`（第三方库）、`slogx`（日志本身）。

## 字段

每个字段名只有一种含义和类型，可以直接按字段统计和建索引：

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `client` | 整数 | 客户端编号；P2P 中对端编号为 `peer` |
| `tunnel` | 整数 | 隧道编号 |
| `host` | 整数 | 域名条目编号 |
| `domain` | 字符串 | 域名（域名条目、证书、SNI、请求的 Host） |
| `route` | 字符串 | 隧道为 `模式:端口`（如 `tcp:8080`），域名条目为域名，服务为名字（`web`、`bridge`、`ip_register`、`p2p`） |
| `src_ip` / `src_port` | 字符串 / 整数 | 对端真实 IP（不含端口，经受信代理时取解析出的地址）和端口 |
| `addr` | 字符串 | 不是 `src_ip` 的其他对端地址 `host:port` |
| `listen` | 字符串 | 监听地址 `ip:port` |
| `port` | 整数 | 单独的端口 |
| `target` | 字符串 | 为访问者拨号的目标 `host:port` |
| `server` | 字符串 | npc 连接的 nps 地址 |
| `carrier` | 字符串 | `tcp`、`tls`、`kcp`、`quic`、`ws`、`wss` |
| `mode` | 字符串 | 隧道模式 |
| `via` | 字符串 | P2P 路径：`direct` 或 `relay` |
| `user` / `proxy_user` | 字符串 | 面板用户名 / 访问者给代理隧道的用户名 |
| `file` / `name` / `path` | 字符串 | 文件路径 / 没有编号的对象名 / URL 路径 |
| `method`、`status`、`bytes`、`in_bytes`、`out_bytes` | | HTTP 与流量，数字就是数字 |
| `dur_ms` | 整数 | 时长，统一为毫秒 |
| `until` | 时间 | 到期时间（RFC 3339） |
| `reason` | 字符串 | 固定短词，取值随事件列出 |
| `err` / `detail` | 字符串 | 错误文本 / 补充说明 |
| `count`、`keys`、`version` | | 数量、逗号分隔的配置键、版本 |

`msg` 是不含变量的简短英文短语，变量都在字段里。

## 固定事件

事件名和字段只会增加，不会改名。webhook 推送的事件名与日志里的相同。完整的字段说明在源码 `lib/logevent/logevent.go`。

| event | 级别 | 主要字段 | 安全日志 | webhook |
| --- | --- | --- | --- | --- |
| `auth_failed` | warn | `src_ip`、`src_port`、`role`（`client`、`visitor`、`secret`、`api`、`http_auth`）、`client`、`proxy_user`、`route`、`reason`（`bad_key`、`bad_password`、`disabled`、`malformed`、`timeout`、`version`、`forbidden`） | 是 | 是 |
| `login_failed` / `login_ok` | warn / info | `src_ip`、`user`、`reason`（`bad_password`、`bad_captcha`、`bad_pow`、`bad_nonce`、`expired`、`malformed`、`banned`、`acl`） | 失败的写 | 是 |
| `ban_added` / `ban_removed` | warn / info | `src_ip`（或 IPv6 的 `prefix`、`user`）、`scope`、`until` | 添加的写 | 是 |
| `admin_action` | info | `src_ip`、`user`、`action`（如 `client_update`、`tunnel_delete`、`config_export`）、`client`/`tunnel`/`host`、`result`、`reason` | | 是 |
| `client_online` / `client_offline` | info | `client`、`src_ip`、`carrier`、`version`；下线另有 `dur_ms`、`reason`（`closed`：连接断开且约 15 秒内没有重连；`deleted`：被管理员踢下线、停用或删除） | | 是（存储层发） |
| `session_takeover` | warn | `client`、`src_ip`、`addr` | | |
| `limit_exceeded` / `quota_reached` | warn | `client`、`route`、`kind`（`flow`、`conn`、`rate`、`expire`）；后者是额度用尽这一状态变化 | | 是 |
| `tunnel_denied` | info | `src_ip`、`route`、`reason`（`acl`、`dest_acl`、`not_registered`、`http_disabled`、`socks5_disabled`、`client_offline`） | | |
| `relay_failed` | info | `route`、`client`、`target`、`reason` | | |
| `target_down` / `target_up` | warn / info | `route`、`target`、`reason`（`check_failed`、`check_passed`） | | 是 |
| `http_access` | debug | `route`、`host`、`src_ip`、`method`、`domain`、`path`、`proto`、`status`、`bytes`、`dur_ms`、`target` | | |
| `conn_closed` | debug | `route`、`tunnel`、`client`、`src_ip`、`target`、`in_bytes`、`out_bytes`、`dur_ms` | | |
| `tunnel_started` / `tunnel_stopped` | info | `route`、`tunnel`、`client`、`reason` | | 是（存储层发） |
| `client_*` / `tunnel_*` / `host_*` 增删改 | | 只发给 webhook，`data` 是条目本身 | | 是 |
| `started` / `stopping` | info | `version`、`role`、`pid`、`file` | | |
| `listening` / `listen_failed` | info / error | `route`、`listen` 或 `port`、`carrier`、`tls`、`reason` | | 失败的发 |
| `config_applied` / `config_failed` | info / warn | `file`、`keys`、`restart`、`reason` | | 是 |
| `update_available` / `update_applied` / `update_failed` | info / info / warn | `from`、`to`、`reason`（`update_failed`、`rolled_back`、`restart_failed`） | | 是 |
| `source_loaded` / `source_updated` / `source_failed` | info / info / warn | `name`（`geoip`、`geosite`）、`file`、`reason` | | |
| `cert_loaded` | info | `route`、`domain`、`file`、`until`（到期时间）、`issuer` | | |
| `cert_issued` / `cert_failed` / `cert_revoked` | info / warn（不足 7 天时 error） / warn | `domain`、`renewal`、`remaining_s`、`reason`、`err` | | 是 |
| `tls_handshake_failed` | debug | `src_ip`、`src_port`、`listener`、`reason` | | |
| `connected` / `connect_failed` / `disconnected` | info / warn / warn | npc：`server`、`carrier`、`attempt`、`reason`、`dur_ms` | | |
| `p2p_connected` / `p2p_failed` / `p2p_path_changed` | info / info / debug | `client`、`peer`、`tunnel`、`session`、`via`、`reason` | | 是（nps 侧） |
| `log_fallback` / `log_dropped` / `log_secrets` | warn | 日志文件打不开 / 异步队列丢行 / 开发开关打开 | | |

第三方库（自动证书、HTTP 服务器、标准库日志）输出的内容会作为普通行写入，带 `module`，但不带 `event`。

## 安全日志与 fail2ban

设置 `security_log=/var/log/nps/security.log` 后，nps 会把安全事件（`auth_failed`、`login_failed`、`ban_added`）另写一份到这个文件，主日志的级别设置不影响它。

```
# /etc/fail2ban/filter.d/nps.conf
[Definition]
failregex = ^time=\S* level=WARN event=(auth_failed|login_failed) module=\S+ src_ip=<HOST>(\s|$)
ignoreregex =

# /etc/fail2ban/jail.d/nps.local
[nps]
enabled  = true
filter   = nps
logpath  = /var/log/nps/security.log
maxretry = 3
findtime = 10m
bantime  = 1h
```

- 过滤器里写 `time=\S*`：fail2ban 匹配前会先把时间戳从行里去掉。
- 安全事件的 `src_ip` 总是紧跟在 `module=` 之后，过滤器必须锚定在这个位置；不要写成 `.*src_ip=`，否则用户名等由对方填写的字段里如果伪造了 `src_ip=`，fail2ban 会去封禁别人的地址。
- `auth_failed` 对同一来源每分钟最多写一行，期间省略的次数记在 `suppressed=` 里，所以一行代表一分钟内至少一次失败；`login_failed` 每次失败都写一行。上面的 `maxretry = 3`、`findtime = 10m` 对 npc 密钥错误相当于"10 分钟里有 3 分钟在失败"，对面板登录是 10 分钟内失败 3 次。

nps 自己也会按来源限速和封禁；fail2ban 只是系统层面的可选补充。

## 分析工具

- **Loki**：`{job="nps"} | logfmt` 或 `| json` 直接取字段，例如 `| logfmt | event="auth_failed"`；`dur_ms`、`bytes` 可以用 `unwrap` 做统计。标签只用实例和文件，不要把 `src_ip`、`route`、`user` 这类取值很多的字段设为标签。
- **Vector / Fluent Bit**：logfmt 或 JSON 解析器即可，时间字段是 `time`（RFC 3339，UTC，毫秒）。Fluent Bit 建议用 JSON 格式。
- **jq**：`log_format=json` 后 `jq 'select(.event=="auth_failed") | .src_ip'`。
- **Elasticsearch / OpenSearch**：用 JSON 格式，`time` 映射为 `@timestamp`。`event`、`host` 与 ECS 的同名对象字段冲突，导入时改名（如 `nps.event`、`nps.host`）。
- **lnav**：自带 logfmt 支持，可直接打开。
- **GoAccess 等访问日志分析**：设置 `access_log`，格式为 combined，`goaccess access.log --log-format=COMBINED`。
- **多实例**：设置 `log_instance`，每行末尾带上 `instance=`，汇总后按它区分。

logfmt 也可以直接用命令行统计，例如按来源统计认证失败：

```bash
grep -o 'event=auth_failed module=[^ ]* src_ip=[^ ]*' nps.log | grep -o 'src_ip=[^ ]*' | sort | uniq -c | sort -rn | head
```

## 输出与轮转

- 在终端里直接运行时，控制台输出改用便于阅读的彩色格式；写文件或被重定向时始终是 logfmt 或 JSON。
- `log=stdout|file|both|off`；以服务方式运行时，`stdout` 改为写文件。
- 日志文件按大小轮转（`log_max_size`，单位 MB），保留份数和天数分别由 `log_max_files`、`log_max_days` 控制，可选压缩（`log_compress`）。轮转出的文件名形如 `nps-20261108T031245.000.log`。
- 新建的日志文件权限为 `0640`（属主可写，同组可读，例如给运行 fail2ban 的管理组）。
- 日志文件无法创建或写入时，改写到 stderr，并记一条 `log_fallback`，不影响启动。
- npc 在 OpenWrt 上默认不写日志（`-log=off`）。
