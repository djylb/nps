# 运维：面板、重载与更新

## Web 管理界面

默认地址 `http://公网IP:8081`，可管理客户端、隧道、域名转发、流量统计、全局参数和登录封禁。

列表支持：

- **批量操作**：勾选当前页条目，选择启用、停用、清空流量或删除后点击“执行”。翻页、搜索、排序或刷新后清空选择。依次处理，部分失败不阻止其余条目，结束后汇总。删除客户端也会删除其隧道和域名。
- **实时刷新**：默认每 3 秒同步当前页可见列和已展开详情的动态信息，不重载整表；可在标题右侧暂停。后台标签页不请求。新增或删除条目需手动刷新。
- **踢下线**：客户端列表中断开在线客户端的连接，配置不变。开启自动重连的 npc 几秒后会重新连上；要阻止连接请使用停用。
- **近期连接记录**：客户端详情显示最近几组上线、离线时间，组数由 `client_history_limit` 设置。

负数 ID 表示服务端本地代理的虚拟客户端，管理员可以查看、编辑、启停、设置限制和清空流量，也可以把隧道和域名关联到它。它不参与 npc 认证，不支持远程更新、删除或批量勾选。

## 重载配置

修改 `nps.conf` 后，以下任一方式让运行中的 nps 重新读取：

- Web 管理界面（管理员）：全局参数页的“重新加载配置”，或接口 `POST /global/reload`，见 [Web API](/reference/web-api.md)。Windows 用这种方式。
- 向进程发送 `SIGUSR1`（Windows 没有此信号）：

```bash
sudo systemctl kill -s USR1 Nps  # systemd 服务，服务名以实际安装为准
sudo kill -USR1 <pid>            # 其他方式，<pid> 为 nps 进程号
```

`nps reload` 命令已移除。

每次重载在日志中写一行结果，接口返回同样的内容：已生效的配置键、需要重启的配置键和失败项。配置文件读取或解析失败时什么都不变。某项设置应用失败（例如 Web 管理界面无法按新设置建立）时整次重载撤销，恢复原配置并应用回去。

立即生效：

- 日志级别与时间同步：`log_level`、`log_secrets`、`ntp_server`、`ntp_interval`
- 统计与限制：`system_info_display`、`flow_limit_type`、`client_history_limit`
- Webhook：`webhook_url`、`webhook_secret`、`webhook_timeout`、`webhook_retries`、`webhook_queue_size`、`webhook_report_interval`
- Web 管理界面：`appname`、`web_base_url`、`head_custom_code`、`web_close_on_not_found`、`instance_key_file`、`ip_register_max_hours`、`http_proxy_access_port`、`https_proxy_access_port`
- 账号与登录：`web_username`、`web_password`、`totp_secret`、`auth_key`、`auth_crypt_key`、`allow_x_real_ip`、`trusted_proxy_ips`、`allow_user_login`、`allow_user_register`、`allow_user_vkey_login`、`allow_user_change_username`
- 用户功能开关：`allow_flow_limit`、`allow_rate_limit`、`allow_time_limit`、`allow_connection_num_limit`、`allow_multi_ip`、`allow_tunnel_num_limit`、`allow_user_local`
- 登录防护：`open_captcha`、`force_pow`、`pow_bits`、`login_ban_time`、`login_ip_ban_time`、`login_user_ban_time`、`login_max_fail_times`、`login_max_body`、`login_max_skew`、`login_acl_mode`、`login_acl_rules`
- 客户端连接地址的显示：`bridge_addr`、`bridge_show_path`、`bridge_tcp_show`、`bridge_tcp_show_ip`、`bridge_tcp_show_port`、`bridge_kcp_show`、`bridge_kcp_show_ip`、`bridge_kcp_show_port`、`bridge_quic_show`、`bridge_quic_show_ip`、`bridge_quic_show_port`、`bridge_tls_show`、`bridge_tls_show_ip`、`bridge_tls_show_port`、`bridge_ws_show`、`bridge_ws_show_ip`、`bridge_ws_show_port`、`bridge_wss_show`、`bridge_wss_show_ip`、`bridge_wss_show_port`、`bridge_quic_show_alpn`
- 运行时读取的：`allow_local_proxy`、`allow_secret_link`、`allow_secret_local`、`http_add_origin_header`、`auth_time_window`（`allow_local_proxy` 只影响之后启动的隧道；`allow_local_proxy`、`allow_secret_link`、`allow_secret_local` 变更时点对点访问的授权失效，访问端重新授权）

以下变更需要重启才生效，重载时列出：

- 日志输出：`log`、`log_path`、`log_max_files`、`log_max_days`、`log_max_size`、`log_compress`、`log_format`、`security_log`、`log_memory`、`log_instance`、`access_log`、`access_log_format`
- 进程：`timezone`、`dns_server`、`pprof_ip`、`pprof_port`、`geoip_path`、`geosite_path`、`acl_max_rules`、`flow_store_interval`
- 客户端连接监听：`bridge_ip`、`bridge_host`、`bridge_path`、`bridge_port`、`mix_ip`、`mix_port`、`mix_plain`、`mix_legacy`、`client_max_pipes`、`session_idle_s`、`session_resume_timeout`、`session_max_lanes`、`session_max_services`、`bridge_trusted_ips`、`bridge_real_ip_header`、`shaping_url`、`shaping_token`、`shaping_pubkey`、`shaping_file`、`new_protocol_tls_alpn`、`new_protocol_quic_alpn`、`new_protocol_ws_paths`、`compress_max_window`、`compress_max_ratio`、`bridge_tcp_ip`、`bridge_tcp_port`、`bridge_kcp_ip`、`bridge_kcp_port`、`bridge_quic_ip`、`bridge_quic_port`、`bridge_tls_ip`、`bridge_tls_port`、`bridge_ws_ip`、`bridge_ws_port`、`bridge_wss_ip`、`bridge_wss_port`、`tls_bridge_port`、`bridge_type`、`tcp_enable`、`kcp_enable`、`quic_enable`、`tls_enable`、`ws_enable`、`wss_enable`
- QUIC 传输：`quic_alpn`、`quic_keep_alive_period`、`quic_max_idle_timeout`、`quic_max_incoming_streams`
- 客户端连接：`bridge_select_mode`、`bridge_cert_file`、`bridge_key_file`、`secure_mode`、`allow_ports`、`public_vkey`、`visitor_vkey`、`disconnect_timeout`、`mux_ping_interval`
- 域名转发：`http_proxy_ip`、`http_proxy_port`、`https_proxy_port`、`http3_proxy_port`、`http_proxy_response_timeout`、`error_page`、`error_page_time_limit`、`error_page_flow_limit`、`error_always`、`fallback_404_file`、`bridge_http3`、`force_auto_ssl`、`x_nps_http_only`、`ssl_email`、`ssl_ca`、`ssl_path`、`ssl_zerossl_api`、`https_default_cert_file`、`https_default_key_file`、`ssl_cache_max`、`ssl_cache_reload`、`ssl_cache_idle`
- P2P：`p2p_ip`、`p2p_port`、`p2p_stun_servers`、`p2p_enable_upnp_portmap`、`p2p_enable_pcp_portmap`、`p2p_enable_natpmp_portmap`、`p2p_portmap_lease_seconds`、`p2p_probe_timeout_ms`、`p2p_handshake_timeout_ms`、`p2p_transport_timeout_ms`
- Web 管理界面监听：`web_ip`、`web_port`、`web_host`、`web_open_ssl`、`web_cert_file`、`web_key_file`
- IP 登记页监听：`ip_register_ip`、`ip_register_port`、`ip_register_cert_file`、`ip_register_key_file`

## 停止和重启

```bash
sudo nps stop
sudo nps restart
```

```powershell
nps.exe stop
nps.exe restart
```

## 更新

```bash
sudo nps update # Linux/macOS
nps.exe update  # Windows
```

已在运行的服务会自动重启，无需先停止；已是最新版本时不替换文件也不重启。发布包中的 Geo 数据比本地新时一并更新。`update-force` 见[强制更新](/getting-started/install-release.md)。能否直接升级由发布清单决定，规则的唯一来源是仓库中的 `release/update-rules.json`，当前规则不允许通过更新跨大版本（如 0.x.x 到 1.x.x），需手动升级。

替换前会验证新版本可运行，并更新系统服务实际运行的程序。

**单独更新 Geo 数据**，运行中的 nps 一分钟内生效：

```bash
sudo nps update-geo          # geoip，以及已存在的其他文件
sudo nps update-geo geosite  # 指定文件，没有时下载
```

**在网页上更新**：管理员在仪表盘“服务端版本”卡片点击更新按钮（仅在有新版本时显示），服务端更新后自动重启，页面随后刷新。

- 重启期间连接短暂中断，客户端会自动重连。
- Linux/macOS 等系统无论以服务还是命令行运行都支持；Windows 只支持 `nps install` 安装的系统服务。
- `-disable_remote_update` 禁止网页更新 nps 自身（不影响更新客户端）；官方 Docker 镜像默认已禁止。

## 手动替换程序

`nps update` 失败时，下载[最新版本](https://github.com/djylb/nps/releases/latest)，覆盖原有 `nps` 和 `web` 目录。程序位置可用 `whereis nps` 查找。

```bash
sudo systemctl stop Nps
sudo cp nps /usr/bin/nps
sudo chmod +x /usr/bin/nps
sudo systemctl start Nps
```

```powershell
Stop-Service Nps
Copy-Item -Path "PATH_TO_NEW_NPS_EXE" -Destination "PATH_TO_OLD_NPS_EXE_DIR" -Force
Start-Service Nps
```

权限不足时请以管理员身份运行。

## 实例主密钥

启动 Web 管理时，会在实际加载的配置文件目录中自动生成 `instance.key`，可用 `instance_key_file` 指定其他路径（相对路径以配置文件目录为基准）。Cookie 的签名和加密密钥由它派生，不依赖管理员账号、密码、TOTP 或 API 密钥。

- 后续重启或修改登录配置不会更换它。请备份，Docker 需持久挂载；多个副本只有共享同一密钥才能互认 Cookie。
- Unix 新建文件权限为 `0600`。自定义目录需预先存在且可写，并支持同目录原子硬链接，否则启动报错。
- 已有密钥无法读取、格式或校验和错误会导致启动失败，不会自动覆盖。删除后下次启动生成新密钥，原有 Cookie 全部失效。
- 它属于敏感凭据，不要公开或提交到版本库。

## 其他常用设置

```ini
# 流量持久化周期（分钟）；不写不持久化。不记录公钥连接的客户端
flow_store_interval=10
# 在面板显示系统负载
system_info_display=true
# 留空禁用公钥访问
public_vkey=
# 不写或为 0 则关闭 Web 管理
web_port=0
# 允许客户端用户登录。默认用户名 user，默认密码为各自 vkey，可修改
allow_user_login=true
# 在登录页显示注册按钮
allow_user_register=true
# 允许在隧道上指定监听 IP（npc.conf 的 server_ip）
allow_multi_ip=true
```

**代理到服务器本地**：相当于在 nps 服务器上启动一个 npc，把流量回送到本机。设置 `allow_local_proxy=true`（普通用户还需 `allow_user_local=true`），然后在隧道或域名上选择“转发到本地”。例如本机 Web 服务在 `5000` 端口，添加域名并把目标设为 `127.0.0.1:5000`。
