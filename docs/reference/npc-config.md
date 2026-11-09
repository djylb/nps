# NPC 配置文件 npc.conf

用法和完整示例见[配置文件模式](/guide/client/config-file.md)。段名含义：`[common]` 全局；含 `host` 的段是域名；名称以 `secret`、`p2p` 开头且没有 `mode` 的段是本地访问端；其他是隧道。

## [common]

`server_addr`、`conn_type`、`pipes`、`scheduler_up`、`vkey`、`auto_reconnection`、`proxy_url`、`local_ip`、`dns_server`、`ntp_server`、`ntp_interval`、`basic_username`、`basic_password`、`web_username`、`web_password`、`compress`、`crypt`、`rate_limit`、`flow_limit`、`time_limit`、`max_conn`、`remark`、`pprof_addr`、`disconnect_timeout`，以及 `session_max_lanes`、`relay_idle`、`p2p_visitor_keep_server`、`session_resume_timeout`。

这些键与同名命令行参数（`-session_max_lanes`、`-relay_idle`、`-p2p_visitor_keep_server`、`-session_resume_timeout`、`-pipes`、`-scheduler_up`）是同一组设置：命令行给出的值是没有写这个键的配置文件和 `--launch` 载荷的默认值，文件里写了就以文件为准。没有本地段的文件托管会话，文件里的隧道和域名段由配置上传注册到服务端；有本地段的文件是纯访问者，所有本地段共用一条外层会话，按需建立；同一进程里同一服务端、同一 vkey 的托管和访问者共用这条会话，托管期间它不会因空闲断开。隧道的压缩由服务端决定，`[common] compress` 在用公共密钥上传时作为新建客户端的默认压缩；有本地段时它决定访问者嵌套流是否压缩（snappy），提供者的 Hello 不支持时不压缩。

`server_addr` 是一个连接（档案）：可以用 `|` 写多个承载候选，写法与命令行 `-server` 相同（见 [NPC 命令行参数](npc-cli.md#服务器地址)），不带方案的候选用 `conn_type`。`pipes` 为 `single`（默认）或 `multi`，同 `-pipes`。配置文件里不需要引号：

```ini
[common]
server_addr=tls://a.com:8025|wss://a.com:443/ws?tier=1|b.com:8024
conn_type=tcp
vkey=xxx
```

上例最后一个候选不带方案，承载取 `conn_type`；不带方案的候选没有查询参数，要设 `tier`、`standby` 等请写成 URL。URL 候选写错（未知方案、参数）时加载配置即报错。

旧写法 `tls_enable=true` 等同 `conn_type=tls`，并覆盖 `conn_type`（会告警）。

## 域名段

`host`、`target_addr`、`target_is_https`、`host_change`、`scheme`、`location`、`path_rewrite`、`cert_file`、`key_file`、`https_just_proxy`、`auto_ssl`、`auto_https`、`auto_cors`、`compat_mode`、`redirect_url`、`multi_account`、`proxy_protocol`、`max_conn`、`upload_limit`、`download_limit`、`header_<名称>`（请求头）、`response_<名称>`（响应头）。

## 隧道段

`mode`（`tcp`、`udp`、`tcpTrans`、`mixProxy`、`httpProxy`、`socks5`、`secret`、`file`；旧的 `p2p` 读作 `secret` 并告警）、`server_port`、`server_ip`、`target_addr`、`target_port`、`target_ip`、`password`、`proxy_protocol`、`socks5_proxy`、`http_proxy`、`dest_acl_mode`、`dest_acl_rules`、`local_path`、`strip_pre`、`read_only`、`multi_account`、`max_conn`、`upload_limit`、`download_limit`。

## 本地访问段

`[secret...]` 节：`local_port`、`local_type`（`tcp_udp` 默认、`tcp`、`udp`、`mixed`、`transparent`）、`conn_mode`（`auto` 默认、`relay`、`direct`）、`local_ip`、`password`、`target_addr`、`local_proxy`。

```ini
[secret_ssh]
local_port=2002
password=ssh3
local_type=tcp
conn_mode=auto
```

旧版的 `[p2p...]` 节和 `target_type`、`fallback_secret`、`local_type=p2p|p2ps|p2pt|secret` 仍可读取，自动换算并告警，对照表见[点对点访问](/guide/tunnels/secret.md#旧版写法)。

`health_*` 配置节已不再支持，读到时告警并忽略；健康检查改在服务端按隧道或域名配置，见[健康检查](/guide/tunnels/health-check.md)。
