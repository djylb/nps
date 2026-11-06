# NPC 配置文件 npc.conf

用法和完整示例见[配置文件模式](/guide/client/config-file.md)。段名含义：`[common]` 全局；含 `host` 的段是域名；名称以 `secret`、`p2p` 开头且没有 `mode` 的段是本地访问端；其他是隧道。

## [common]

`server_addr`、`conn_type`、`vkey`、`auto_reconnection`、`proxy_url`、`local_ip`、`dns_server`、`ntp_server`、`ntp_interval`、`basic_username`、`basic_password`、`web_username`、`web_password`、`compress`、`crypt`、`rate_limit`、`flow_limit`、`time_limit`、`max_conn`、`remark`、`pprof_addr`、`disconnect_timeout`、`tls_enable`。

## 域名段

`host`、`target_addr`、`target_is_https`、`host_change`、`scheme`、`location`、`path_rewrite`、`cert_file`、`key_file`、`https_just_proxy`、`auto_ssl`、`auto_https`、`auto_cors`、`compat_mode`、`redirect_url`、`multi_account`、`proxy_protocol`、`max_conn`、`upload_limit`、`download_limit`、`header_<名称>`（请求头）、`response_<名称>`（响应头）。

## 隧道段

`mode`（`tcp`、`udp`、`tcpTrans`、`mixProxy`、`httpProxy`、`socks5`、`secret`、`p2p`、`file`）、`server_port`、`server_ip`、`target_addr`、`target_port`、`target_ip`、`password`、`proxy_protocol`、`socks5_proxy`、`http_proxy`、`dest_acl_mode`、`dest_acl_rules`、`local_path`、`strip_pre`、`read_only`、`multi_account`、`max_conn`、`upload_limit`、`download_limit`。

## 本地访问段

`local_port`、`local_type`、`local_ip`、`password`、`target_addr`、`target_type`、`local_proxy`、`fallback_secret`。

```ini
[p2p_ssh]
local_port=2002
password=ssh3
target_addr=123.206.77.88:22
target_type=tcp
fallback_secret=true
```

`health_*` 配置节已不再支持，见[健康检查](/guide/tunnels/health-check.md)。
