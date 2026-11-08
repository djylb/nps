# NPC 配置文件 npc.conf

用法和完整示例见[配置文件模式](/guide/client/config-file.md)。段名含义：`[common]` 全局；含 `host` 的段是域名；名称以 `secret`、`p2p` 开头且没有 `mode` 的段是本地访问端；其他是隧道。

## [common]

`server_addr`、`conn_type`、`pipes`、`vkey`、`auto_reconnection`、`proxy_url`、`local_ip`、`dns_server`、`ntp_server`、`ntp_interval`、`basic_username`、`basic_password`、`web_username`、`web_password`、`compress`、`crypt`、`rate_limit`、`flow_limit`、`time_limit`、`max_conn`、`remark`、`pprof_addr`、`disconnect_timeout`。

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
