# 服务端配置 nps.conf

默认路径：Linux/macOS `/etc/nps/conf/nps.conf`，Windows `C:\Program Files\nps\conf\nps.conf`；也支持同目录的 `.ini`、`.yaml`、`.yml`、`.json`。用 `-conf_path` 指定目录或文件：

```bash
./nps -conf_path=/app/nps
nps.exe -conf_path=D:\test\nps
```

下表“默认”是**配置项缺失时**的值，“示例”是发布包 [conf/nps.conf](https://github.com/djylb/nps/tree/master/conf/nps.conf) 写入的值（`-` 表示示例中未启用）。二者可能不同，例如删掉 `web_port` 会关闭 Web 管理。

修改后重启生效；哪些项可以重载见[重载配置](/guide/server/operations.md)。**正式部署请修改 `web_password` 等默认值。**

## 基础

| 名称 | 默认 | 示例 | 说明 |
| --- | --- | --- | --- |
| `appname` | `nps` | `nps` | 应用名称 |
| `runmode` | 空 | `pro` | 运行模式（`dev`、`pro`） |
| `secure_mode` | `false` | `true` | 安全模式，见[兼容范围](/getting-started/versioning.md) |
| `auth_time_window` | `300` | `300` | 客户端认证允许的时间偏差（秒）。`0` 关闭时间校验，负数用默认值；仅安全模式生效。防重放记录保留两倍窗口（`0` 时 600 秒），重启清空；关闭校验后，记录过期的旧请求仍可重放。不影响 `login_max_skew` |
| `dns_server` | 空 | `8.8.8.8` | 优先使用的 DNS，不可达时回退到系统 DNS |
| `timezone` | 空 | - | 时区，如 `Asia/Shanghai` |
| `ntp_server` | 空 | - | NTP 服务器 |
| `ntp_interval` | `5` | - | NTP 最小查询间隔（分钟） |
| `geoip_path` / `geosite_path` | `conf/geoip.dat` / `conf/geosite.dat` | - | Geo 数据路径 |
| `acl_max_rules` | `1000` | - | 客户端、隧道、域名每份名单及目标名单的最大规则条数，`0` 不限；全局名单不受限 |
| `ip_register_max_hours` | `2` | - | 用公钥或未设上限的客户端口令登记时的最长有效期（小时） |
| `ip_register_ip` / `ip_register_port` | `0.0.0.0` / `0` | - | 单独提供登记页面的监听，端口 `0` 不单独监听 |
| `ip_register_cert_file` / `ip_register_key_file` | 空 | - | 单独登记页面的证书和私钥（路径或 PEM），都配置时使用 HTTPS |
| `client_history_limit` | `5` | `5` | 保留的上线/离线记录组数，`0` 关闭，负数用默认值 |
| `disconnect_timeout` | `30` | `30` | 客户端断开超时（秒） |
| `flow_store_interval` | `0` | `1` | 流量持久化间隔（分钟），`0` 不持久化 |

## Web 管理

| 名称 | 默认 | 示例 | 说明 |
| --- | --- | --- | --- |
| `web_ip` | `0.0.0.0` | `0.0.0.0` | 监听地址 |
| `web_port` | `0` | `8081` | 监听端口，`0` 关闭 |
| `web_host` | 空 | `a.o.com` | 端口复用时访问管理页面的域名 |
| `web_base_url` | 空 | - | 路径前缀，如 `/nps` |
| `web_open_ssl` | `false` | `false` | 启用 HTTPS |
| `web_cert_file` / `web_key_file` | 空 | `conf/server.pem` / `conf/server.key` | 证书和私钥（路径或 PEM） |
| `web_close_on_not_found` | `false` | - | 未知路径尽量不响应直接断开 |
| `web_username` / `web_password` | 空 | `admin` / `123` | 管理员账号。**请修改** |
| `totp_secret` | 空 | 空 | TOTP 密钥。启用后登录时在密码（或验证码）后追加 6 位动态码；`web_password` 留空可仅用 TOTP。用 `nps -gen2fa` 生成 |
| `instance_key_file` | 配置目录 `instance.key` | - | [实例主密钥](/guide/server/operations.md) |
| `head_custom_code` | 空 | - | 插入页面头部的自定义代码 |
| `open_captcha` | `false` | `true` | 登录验证码 |
| `pow_bits` | `20` | `20` | PoW 计算位数 |
| `force_pow` | `false` | - | 强制 PoW |
| `login_ban_time` | `5` | - | 两次登录最小间隔（秒） |
| `login_ip_ban_time` | `180` | - | IP 维度失败重置周期（秒） |
| `login_user_ban_time` | `3600` | - | 用户名维度失败重置周期（秒） |
| `login_max_fail_times` | `10` | - | 最大失败次数 |
| `login_max_body` | `1024` | - | 登录请求体上限（字节） |
| `login_max_skew` | `300000` | - | 时间戳偏移容忍（毫秒） |
| `login_acl_mode` | `0` | - | `0` 关闭，`1` 白名单，`2` 黑名单 |
| `login_acl_rules` | 空 | - | [登录来源](/guide/server/access-control.md)规则，逗号或换行分隔 |
| `allow_x_real_ip` | `false` | `false` | 允许通过 X-Real-IP 获取真实 IP |
| `trusted_proxy_ips` | `127.0.0.1` | `127.0.0.1` | 受信任的代理 IP，逗号分隔 |
| `allow_user_login` | `false` | `false` | 允许客户端用户登录 |
| `allow_user_vkey_login` | 同 `allow_user_login` | - | 允许用 `vkey` 登录 |
| `allow_user_register` | `false` | `false` | 允许注册 |
| `allow_user_change_username` | `false` | `false` | 允许修改用户名 |

## Web API

| 名称 | 默认 | 说明 |
| --- | --- | --- |
| `auth_key` | 空 | [Web API](/reference/web-api.md) 密钥，留空禁用 |
| `auth_crypt_key` | 空 | 获取 authKey 的 AES 密钥（16 位），留空禁用 |

## 密钥

| 名称 | 默认 | 说明 |
| --- | --- | --- |
| `public_vkey` | 空 | 客户端配置文件模式的公钥，留空禁用 |
| `visitor_vkey` | 空 | 访客密钥，只能访问 secret/P2P 隧道，不能注册隧道或拉取配置；设置后网页上的访问命令使用它 |

## 域名转发

| 名称 | 默认 | 示例 | 说明 |
| --- | --- | --- | --- |
| `http_proxy_ip` | `0.0.0.0` | `0.0.0.0` | 监听地址 |
| `http_proxy_port` | `0` | `80` | HTTP 端口，`0` 不启动 |
| `https_proxy_port` | `0` | `443` | HTTPS 端口，`0` 不启动 |
| `http3_proxy_port` | 同 `https_proxy_port` | - | HTTP/3 端口，`0` 关闭 |
| `http_proxy_access_port` / `https_proxy_access_port` | 同监听端口 | - | 对外访问端口，仅用于生成链接 |
| `http_proxy_response_timeout` | `100` | `100` | 后端响应头超时（秒） |
| `http_add_origin_header` | `false` | `true` | 添加真实 IP 请求头 |
| `x_nps_http_only` | 空 | 空 | 前置代理通过 `X-NPS-Http-Only` 传此值时信任它，避免 301 |
| `https_default_cert_file` / `https_default_key_file` | 空 | `conf/server.pem` / `conf/server.key` | 默认证书 |
| `force_auto_ssl` | `false` | `false` | 强制自动申请证书 |
| `ssl_path` | `ssl` | `conf/ssl` | 自动证书保存目录 |
| `ssl_email` | 空 | `you@yours.com` | ACME 邮箱 |
| `ssl_ca` | `LetsEncrypt` | `LetsEncrypt` | `LetsEncrypt`、`ZeroSSL`、`GoogleTrust` |
| `ssl_zerossl_api` | 空 | - | ZeroSSL API 密钥 |
| `ssl_cache_max` | `0` | `0` | 证书缓存上限，`0` 不限 |
| `ssl_cache_reload` | `0` | `3600` | 检测证书文件变更的间隔（秒） |
| `ssl_cache_idle` | `60` | `60` | 闲置证书移出缓存的时间（分钟） |
| `error_page` | `web/static/page/error.html` | - | 错误页 |
| `error_page_time_limit` / `error_page_flow_limit` | 空 | - | 到期、超流量时的错误页 |
| `error_always` | `false` | - | 总是显示错误页 |

## 客户端连接

| 名称 | 默认 | 示例 | 说明 |
| --- | --- | --- | --- |
| `bridge_ip` | `0.0.0.0` | `0.0.0.0` | 监听地址 |
| `bridge_tcp_ip`、`bridge_kcp_ip`、`bridge_tls_ip`、`bridge_quic_ip`、`bridge_ws_ip`、`bridge_wss_ip` | 同 `bridge_ip` | - | 各协议单独的监听地址 |
| `bridge_port` | `0` | - | TCP、KCP 端口的默认值 |
| `bridge_tcp_port` | 同 `bridge_port` | `8024` | `0` 关闭，下同 |
| `bridge_kcp_port` | 同 `bridge_port` | `8024` | |
| `bridge_tls_port` | `0` | `8025` | 也接受旧名 `tls_bridge_port` |
| `bridge_quic_port` | `0` | `8025` | |
| `bridge_ws_port` | `0` | `8026` | |
| `bridge_wss_port` | `0` | `8027` | |
| `bridge_path` | `/ws` | `/ws` | WS 路径 |
| `bridge_real_ip_header` / `bridge_trusted_ips` | 空 | - | WS 取真实 IP 的请求头和信任的代理 |
| `bridge_host` | 空 | `xxx.com` | 端口复用时的主机名 |
| `bridge_http3` | `true` | - | 允许经 HTTP/3 端口连接 |
| `bridge_cert_file` / `bridge_key_file` | 空 | - | 证书，留空自动生成 |
| `bridge_select_mode` | 空（主备） | `Primary` | 主备 `0`/`Primary`/`p`，轮询 `1`/`RoundRobin`/`rr`，随机 `2`/`Random`/`rand`；配置文件发布的条目固定轮询 |
| `bridge_addr` | 空 | 空 | 网页命令显示的地址，留空跟随网页地址 |
| `bridge_type` | `both` | - | 已弃用；`tcp`、`kcp`、`quic`、`udp`、`both` |
| `tcp_enable`、`kcp_enable`、`quic_enable`、`tls_enable`、`ws_enable`、`wss_enable` | `true` | - | 单独关闭某协议 |
| `bridge_show_path` | 同 `bridge_path` | - | 网页命令显示的路径 |
| `bridge_tcp_show`、`bridge_kcp_show`、`bridge_quic_show`、`bridge_tls_show`、`bridge_ws_show`、`bridge_wss_show` | 协议启用时为 `true` | - | 是否在网页命令显示 |
| `bridge_tcp_show_ip`、`bridge_kcp_show_ip`、`bridge_quic_show_ip`、`bridge_tls_show_ip`、`bridge_ws_show_ip`、`bridge_wss_show_ip` | 空 | - | 网页命令显示的 IP |
| `bridge_tcp_show_port`、`bridge_kcp_show_port`、`bridge_quic_show_port`、`bridge_tls_show_port`、`bridge_ws_show_port`、`bridge_wss_show_port` | 监听端口 | - | 网页命令显示的端口 |
| `bridge_quic_show_alpn` | `quic_alpn` 第一项 | - | 网页命令显示的 ALPN |
| `quic_alpn` | `nps` | - | QUIC ALPN 列表 |
| `quic_keep_alive_period` | `10` | - | QUIC 保活周期（秒） |
| `quic_max_idle_timeout` | `30` | - | QUIC 最大空闲（秒） |
| `quic_max_incoming_streams` | `100000` | - | QUIC 最大并发接收流 |
| `mux_ping_interval` | `5` | - | 多路复用心跳间隔（秒） |

## P2P

| 名称 | 默认 | 示例 | 说明 |
| --- | --- | --- | --- |
| `p2p_port` | `0` | `6000` | NAT 观测端起始 UDP 端口，占用 `p2p_port`..`p2p_port+2`；`0` 关闭 P2P |
| `p2p_ip` | 空 | 空 | 告知客户端的地址：IP 或域名，逗号分隔。留空同时自动识别 IPv4、IPv6；`0.0.0.0` 只识别 IPv4，`::` 只识别 IPv6 |
| `p2p_stun_servers` | 内置列表 | - | 下发给客户端的 STUN，逗号分隔，`none` 关闭 |
| `p2p_probe_timeout_ms` | 2000 | - | NAT 探测超时 |
| `p2p_handshake_timeout_ms` | 20000 | - | 打洞超时（所有轮次共享） |
| `p2p_transport_timeout_ms` | 10000 | - | 打洞成功后建立 QUIC 的超时 |
| `p2p_enable_upnp_portmap`、`p2p_enable_pcp_portmap`、`p2p_enable_natpmp_portmap` | `false` | - | 允许客户端使用的端口映射协议 |
| `p2p_portmap_lease_seconds` | `3600` | - | 端口映射租期（秒） |

## 功能开关

| 名称 | 默认 | 示例 | 说明 |
| --- | --- | --- | --- |
| `allow_ports` | 空 | - | 允许的隧道端口，如 `9001-9009,10001,11000-12000` |
| `allow_flow_limit` | `false` | `true` | 流量限制 |
| `flow_limit_type` | `total` | - | 客户端流量限制口径：`total` 上下行流量，`actual` 实际消耗（×2）；隧道和域名始终按上下行流量 |
| `allow_rate_limit` | `false` | `true` | 带宽限制 |
| `allow_time_limit` | `false` | `true` | 到期时间限制 |
| `allow_tunnel_num_limit` | `false` | `true` | 客户端隧道数限制 |
| `allow_connection_num_limit` | `false` | `true` | 客户端连接数限制 |
| `allow_local_proxy` | `false` | `true` | 服务端本地代理 |
| `allow_user_local` | 同 `allow_local_proxy` | `false` | 用户使用本地代理 |
| `allow_secret_link` | `false` | `false` | 私密代理指定目标 |
| `allow_secret_local` | `false` | `false` | 私密代理连到服务端本地 |
| `allow_multi_ip` | `false` | `true` | 配置隧道监听 IP |
| `system_info_display` | `false` | `true` | 显示系统负载 |

## Webhook

`webhook_url`、`webhook_secret`、`webhook_timeout`、`webhook_retries`、`webhook_queue_size`、`webhook_report_interval`，见 [Webhook](/guide/server/webhook.md)。

## 日志与调试

| 名称 | 默认 | 示例 | 说明 |
| --- | --- | --- | --- |
| `log` | `stdout` | `stdout` | `stdout`、`file`、`both`、`off`；以服务运行时 `stdout` 改写文件 |
| `log_level` | `info` | `info,bridge=debug` | `trace`、`debug`、`info`、`warn`、`error`、`off`；可按模块单独设置，如 `info,p2p=debug,acme=warn`；修改后热生效 |
| `log_format` | `text` | `json` | `text`（logfmt，一行一个事件）或 `json`，见[日志](../guide/server/logging.md) |
| `security_log` | 空 | `/var/log/nps/security.log` | 另写一份只含安全事件的文件，供 fail2ban 使用；轮转设置同上 |
| `log_path` | 系统默认 | `conf/nps.log` | 路径、`off`、`docker`；系统默认为 Windows 程序目录 `nps.log`，其他 `/var/log/nps.log` |
| `log_compress` | `false` | `false` | 压缩日志 |
| `log_max_files` | `30` | `10` | 日志文件数上限 |
| `log_max_days` | `30` | `7` | 保留天数 |
| `log_max_size` | `5` | `2` | 单文件大小（MB） |
| `pprof_ip` / `pprof_port` | 空 / `0` | - | pprof 监听；端口 `0` 关闭 |
