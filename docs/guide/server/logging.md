# 日志

nps 和 npc 的日志都是**一个事件一行**。写文件或被重定向时是 logfmt（`键=值` 排成一行），在终端里直接运行时显示成彩色便于阅读，也可以固定为 logfmt 或 JSON。

```
time=2026-11-08T03:12:45.120Z level=WARN event=auth_failed module=bridge src_ip=203.0.113.7 src_port=51234 role=client reason=bad_key
time=2026-11-08T03:12:46.002Z level=INFO module=p2p peer=12 msg="punch started"
```

- 每行开头的字段固定，依次是 `time`（UTC，精确到毫秒）、`level`、`event`（只有固定事件才有）、`module`；接着是事件自己的字段，`msg` 放在最后，给人看。
- 字段值带空格、引号或换行时，会加引号并转义，对方提交的内容无法伪造出一行新日志。
- 密钥、口令、令牌、Cookie 默认不写入日志，显示为 `[REDACTED]`。开发调试时可以设 `log_secrets=true`（npc 用 `-log_secrets`）让它们原样出现，开启时会先记一条警告；生产环境不要开。
- 高频事件有限频：同一事件、同一来源每分钟最多写一行，下一行用 `suppressed=` 记录这期间省略了多少行。

## 日志写到哪里

nps 的日志相关配置都在 `nps.conf`，全部键见[服务端配置](/reference/server-config.md)：

| 配置 | 默认 | 作用 |
| --- | --- | --- |
| `log` | `stdout` | `stdout`（控制台）、`file`（文件）、`both`（两者）、`off`（不输出） |
| `log_path` | 系统默认 | 日志文件路径；`off` 或 `docker` 表示不写文件。系统默认：Windows 是程序目录下的 `nps.log`，其他系统是 `/var/log/nps.log` |
| `log_level` | `info` | 级别，可按模块设置，见下文 |
| `log_format` | `auto` | `auto`（终端彩色，其他情况 logfmt）、`text`（总是 logfmt）、`json` |
| `log_max_size`、`log_max_files`、`log_max_days`、`log_compress` | `5`（MB）、`30`、`30`、`false` | 轮转：单文件大小、保留份数、保留天数、是否压缩旧文件 |
| `security_log` | 空 | 另写一份只含安全事件的文件，见下文 |
| `access_log`、`access_log_format` | 空、`combined` | 域名转发的访问日志，见下文 |
| `log_memory` | `1000` | 内存里保留的最近日志条数，供网页和接口查看；`0` 关闭 |
| `log_instance` | 空 | 每行末尾加 `instance=`，多台 nps 的日志汇总到一处时用来区分 |
| `log_secrets` | `false` | 开发调试用，见上文 |

发布包自带的 `nps.conf` 把日志设成 `log=stdout`、`log_path=conf/nps.log`，轮转为 2 MB、10 份、7 天。要点：

- **前台运行**（直接执行 `nps`）时，日志输出到终端。
- **以服务运行**（`nps install` 之后由系统服务管理器启动，Docker 镜像也是这样启动的）时，没有控制台，`log=stdout` 会改为写 `log_path` 的文件。所以装成服务后要看日志，请去看这个文件，例如 `/etc/nps/conf/nps.log`；相对路径以 nps 的运行目录为基准（Linux 安装后是 `/etc/nps`）。
- **Docker** 里想用 `docker logs` 查看，把 `log` 设为 `both`；只要控制台输出，则设 `log_path=off`。
- 日志文件无法创建或写入时，nps 改写到标准错误，并记一条 `log_fallback`，不影响启动。
- 新建的日志文件权限是 `0640`（属主可写，同组可读，例如给运行 fail2ban 的用户组）。

日志相关设置里，`log_level`、`log_secrets` 重载后立即生效，其余需要重启，见[运维](/guide/server/operations.md)。

## 级别

| 级别 | 用于 |
| --- | --- |
| `error` | 需要人工处理，例如端口打不开、证书快到期仍续期失败 |
| `warn` | 安全事件或服务降级 |
| `info` | 状态变化（上线、下线、配置生效）和策略拒绝 |
| `debug` | 单个连接的细节 |
| `trace` | 比 debug 更细，用于逐包排查 |

另有 `off`，表示不输出。可以按模块单独设级别，例如 `log_level=info,p2p=debug,acme=warn`：逗号分隔，前面不带 `=` 的是默认级别，后面是各模块的级别。修改后重载即可，不用重启。

模块：`server`（nps 进程与隧道）、`bridge`（npc 接入）、`core`（连接协议与会话）、`p2p`、`httpproxy`（域名转发）、`proxy`（端口隧道）、`conn`、`web`（管理面板）、`store`（数据存储）、`config`、`acl`（名单）、`install`（安装与更新）、`geo`（Geo 数据）、`acme`（自动证书）、`crypt`、`clock`、`webhook`、`runenv`、`client`（npc）、`lib`（第三方库）。

## 字段

每个字段名只有一种含义和类型，可以直接按字段统计和建索引：

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `client` | 整数 | 客户端编号；点对点访问中对端编号为 `peer` |
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
| `carrier` | 字符串 | 连接方式：`tcp`、`tls`、`quic`、`ws`、`wss` |
| `mode` | 字符串 | 隧道模式 |
| `via` | 字符串 | 点对点访问的路径：`direct` 或 `relay` |
| `user` / `proxy_user` | 字符串 | 面板用户名 / 访问者给代理隧道的用户名 |
| `file` / `name` / `path` | 字符串 | 文件路径 / 没有编号的对象名 / URL 路径 |
| `method`、`status`、`bytes`、`in_bytes`、`out_bytes` | | HTTP 与流量，数字就是数字 |
| `dur_ms` | 整数 | 时长，统一为毫秒 |
| `until` | 时间 | 到期时间（RFC 3339） |
| `reason` | 字符串 | 固定短词，取值随事件列出；网络错误的通用取值见下 |
| `err` / `detail` | 字符串 | 错误文本 / 补充说明 |
| `count`、`keys`、`version` | | 数量、逗号分隔的配置键、版本 |

`msg` 是不含变量的简短英文短语，变量都在字段里。

网络错误的 `reason` 取值：`eof`、`timeout`（超时）、`refused`（对方拒绝，一般是没有程序在监听）、`reset`、`closed`、`dns`（域名解析失败）、`tls`（证书或握手问题）、`limit`、`in_use`（端口已被占用）、`permission`（没有权限，如占用低位端口）、`client_offline`、`other`。

## 固定事件

事件名和字段只会增加，不会改名。webhook 推送的事件名与日志里的相同，见 [Webhook](/guide/server/webhook.md)。排错时按事件名搜索最快，见[排错](/guide/server/troubleshooting.md)。

| event | 级别 | 主要字段 | 安全日志 | webhook |
| --- | --- | --- | --- | --- |
| `auth_failed` | warn | `src_ip`、`src_port`、`role`（`client`、`visitor`、`secret`、`api`、`http_auth`）、`client`、`proxy_user`、`route`、`carrier`、`reason`（`bad_key`、`bad_password`、`disabled`、`malformed`、`timeout`、`version`、`forbidden`） | 是 | 是 |
| `login_failed` / `login_ok` | warn / info | `src_ip`、`user`、`reason`（`bad_password`、`bad_captcha`、`bad_pow`、`bad_nonce`、`expired`、`malformed`、`banned`、`acl`、`session_failed`、`error`） | 失败的写 | 是 |
| `ban_added` / `ban_removed` | warn / info | `src_ip`（或 IPv6 的 `prefix`、`user`）、`scope`、`until` | 添加的写 | 是 |
| `admin_action` | info | `src_ip`、`user`、`action`（如 `client_update`、`tunnel_delete`、`config_export`）、`client`/`tunnel`/`host`、`result`、`reason` | | 是 |
| `client_online` / `client_offline` | info | `client`、`src_ip`、`carrier`、`version`；下线另有 `dur_ms`、`reason`（`replaced`：同一客户端重新连上，旧会话被取代；`closed`：连接断开且没有及时重连；`deleted`：被管理员踢下线、停用或删除） | | 是 |
| `session_takeover` | warn | `client`、`src_ip`、`addr` | | |
| `session_suspended` / `session_resumed` | debug | 会话的连接全部断开、等待恢复 / 恢复；`client`、`src_ip`、`carrier`，恢复另有 `dur_ms` | | |
| `limit_exceeded` | warn | `client`、`route`、`kind`（`flow`、`conn`、`rate`、`expire`、`pipes`） | | 是 |
| `quota_reached` | warn | `client`、`tunnel`、`host`、`kind`（`flow`、`expire`）；额度用尽这一状态变化 | | 是 |
| `tunnel_denied` | info | `src_ip`、`route`、`reason`（`acl`、`dest_acl`、`not_registered`、`http_disabled`、`socks5_disabled`、`auth`、`client_offline`） | | |
| `relay_failed` | info | `route`、`client`、`target`、`reason`（网络错误取值，或 `no_target`、`client_offline`） | | |
| `target_down` / `target_up` | warn / info | `route`、`target`、`reason`（`check_failed`、`check_passed`） | | 是 |
| `http_access` | debug | `route`、`host`、`src_ip`、`method`、`domain`、`path`、`proto`、`status`、`bytes`、`dur_ms`、`target` | | |
| `conn_closed` | debug | `route`、`tunnel`、`client`、`src_ip`、`target`、`in_bytes`、`out_bytes`、`dur_ms` | | |
| `tunnel_started` / `tunnel_stopped` | info | `route`、`tunnel`、`client`、`reason`；域名条目对应 `host_started` / `host_stopped` | | 是 |
| `client_*`、`tunnel_*`、`host_*` 的增删改 | | 只发给 webhook，`data` 是条目本身 | | 是 |
| `started` / `stopping` | info | `version`、`role`、`pid`、`file`；nps 的 `started` 另有 `min_client_version`（接受的最低协议版本） | | |
| `listening` / `listen_failed` | info / error | `route`、`listen` 或 `port`、`carrier`、`tls`、`reason` | | 失败的发 |
| `config_applied` / `config_failed` | info / warn | `file`、`keys`、`restart`、`trigger`（`signal`、`web`）、`reason` | | 是 |
| `update_available` / `update_applied` / `update_failed` | info / info / warn | `from`、`to`、`reason`（`update_failed`、`rolled_back`、`restart_failed`） | | 是 |
| `source_loaded` / `source_updated` / `source_failed` | info / info / warn | `name`（`geoip`、`geosite`）、`file`、`reason` | | |
| `cert_loaded` | info | `route`、`domain`、`file`、`until`（到期时间）、`issuer` | | |
| `cert_issued` / `cert_failed` / `cert_revoked` | info / warn（不足 7 天时 error） / warn | `domain`、`renewal`、`remaining_s`、`reason`（失败时：`issue_failed`、`challenge_failed`、`rate_limited`、`dns_failed`、`account_failed`、`load_failed`）、`err` | | 是 |
| `tls_handshake_failed` | debug | `src_ip`、`src_port`、`listener`、`reason` | | |
| `connected` / `connect_failed` / `disconnected` | info / warn / warn | npc：`server`、`carrier`、`attempt`、`reason`（连接失败：`dial`、`send_type`、`signal_stream`、`no_tunnel`、`establish`；断开：`lost`、`closed`）、`dur_ms` | | |
| `p2p_connected` / `p2p_failed` / `p2p_path_changed` | info / info / debug | `client`、`peer`、`tunnel`、`session`、`via`、`reason` | | 是（nps 侧） |
| `log_fallback` / `log_dropped` / `log_secrets` | warn | 日志文件打不开 / 异步队列丢行 / 开发开关打开 | | |

第三方库（自动证书、HTTP 服务器、标准库日志）输出的内容会作为普通行写入，带 `module`，但没有 `event`。

## 轮转与重新打开日志

nps 自己按大小轮转日志，旧文件名形如 `nps-20261108T031245.000.log`，保留份数和天数由 `log_max_files`、`log_max_days` 控制。

如果想改用系统的 logrotate 等外部工具移走日志，给进程发 `SIGHUP`，它会关闭并重新打开日志文件（主日志和 `security_log`；Windows 没有这个信号）：

```bash
sudo systemctl kill -s HUP Nps    # nps 的系统服务；npc 的服务名以实际安装为准
docker kill -s HUP nps            # Docker 部署
```

```
# /etc/logrotate.d/nps
/etc/nps/conf/nps.log {
    weekly
    rotate 8
    compress
    missingok
    postrotate
        systemctl kill -s HUP Nps
    endscript
}
```

用这种方式时，把 `log_max_size` 设为 `0` 关闭 nps 自己的按大小轮转，避免两套轮转同时动文件。`access_log` 由 nps 自己轮转，不响应 `SIGHUP`。注意 `SIGHUP` 只重开日志文件，重新读取配置要发 `SIGUSR1`，见[运维](/guide/server/operations.md)。

## 安全日志与 fail2ban

设置 `security_log=/var/log/nps/security.log` 后，nps 会把安全事件（`auth_failed`、`login_failed`、`ban_added`）另写一份到这个文件，主日志的级别设置不影响它，轮转设置与主日志相同。

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
- `auth_failed` 和 `login_failed` 对同一来源每分钟最多写一行，期间省略的次数记在 `suppressed=` 里，所以一行代表一分钟内至少一次失败。上面的 `maxretry = 3`、`findtime = 10m` 相当于“10 分钟里有 3 分钟都在失败”。

nps 自己也会按来源限速和封禁；fail2ban 只是系统层面的可选补充。

## 访问日志

`access_log=logs/access.log` 让域名转发把每个请求（包括被拒绝的）写到单独的文件，不受 `log_level` 影响，轮转设置同主日志。`access_log_format` 选 `combined`（Web 服务器通用格式，末尾追加耗时微秒和 Host）或 `json`。GoAccess 等工具可以直接读：

```bash
goaccess logs/access.log --log-format=COMBINED
```

## 在网页和接口里看日志

nps 在内存里保留最近 `log_memory` 条日志（默认 1000）。管理员可以通过接口 `GET /api/v1/logs/entries` 按级别、模块、事件、关键字筛选，也可以按序号增量跟随；`/api/v1/logs/override` 能临时把某个模块调高级别，到期自动恢复，不用改配置文件。接口说明见 [Web API](/reference/web-api.md)。

## npc 的日志

npc 的日志格式、字段和事件与 nps 一致，参数在命令行上设置，完整列表见 [npc 命令行](/reference/npc-cli.md)：

```bash
npc -launch="npc://YOUR_VKEY@203.0.113.10:8024?type=tls" -log=file -log_level=info,p2p=debug
```

- 各日志参数的默认值见 [npc 命令行](/reference/npc-cli.md)，含义同 nps。默认写文件（OpenWrt 上不写）；交互式运行且没有显式给 `-log` 时输出到控制台。
- 给 npc 进程发 `SIGHUP` 同样重新打开日志文件。

## 分析工具

- **Loki**：`{job="nps"} | logfmt` 或 `| json` 直接取字段，例如 `| logfmt | event="auth_failed"`；`dur_ms`、`bytes` 可以用 `unwrap` 做统计。标签只用实例和文件，不要把 `src_ip`、`route`、`user` 这类取值很多的字段设为标签。
- **Vector / Fluent Bit**：logfmt 或 JSON 解析器即可，时间字段是 `time`（RFC 3339，UTC，毫秒）。Fluent Bit 建议用 JSON 格式。
- **jq**：`log_format=json` 后 `jq 'select(.event=="auth_failed") | .src_ip'`。
- **Elasticsearch / OpenSearch**：用 JSON 格式，`time` 映射为 `@timestamp`。`event`、`host` 与 ECS 的同名对象字段冲突，导入时改名（如 `nps.event`、`nps.host`）。
- **lnav**：自带 logfmt 支持，可直接打开。
- **多实例**：设置 `log_instance`，汇总后按 `instance=` 区分。

logfmt 也可以直接用命令行统计，例如按来源统计认证失败：

```bash
grep -o 'event=auth_failed module=[^ ]* src_ip=[^ ]*' nps.log | grep -o 'src_ip=[^ ]*' | sort | uniq -c | sort -rn | head
```

## 性能分析（pprof）

排查 CPU 或内存问题时，可以开启 Go 的 pprof 调试端口，默认关闭。nps 在 `nps.conf` 设 `pprof_ip`、`pprof_port`（端口为 `0` 或留空即关闭），npc 用 `-pprof=ip:port`。这个端口没有认证，**只监听 `127.0.0.1`**，需要时再用 SSH 转发访问。

以 `127.0.0.1:9999` 为例：

- `http://127.0.0.1:9999/debug/pprof/goroutine?debug=2`：所有 goroutine 的栈，可以看出属于哪个客户端或隧道。
- `http://127.0.0.1:9999/debug/pprof/heap`：内存使用。
- `go tool pprof http://127.0.0.1:9999/debug/pprof/profile?seconds=30`：采样 30 秒的 CPU 使用。
