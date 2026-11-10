# 运维：面板、重载与更新

nps 装好之后的日常管理：用网页面板管理客户端和隧道，改了配置文件让它生效，更新版本，停止和重启。装机步骤见[安装](/getting-started/install.md)，所有命令行参数见 [nps 命令行](/reference/nps-cli.md)。

## 网页管理界面

默认地址 `http://203.0.113.10:8081`（换成你的服务器 IP 和 `web_port`）。初始账号是 `nps.conf` 里的 `web_username` 和 `web_password`，发布包里两项都是空的，**首次使用前须先在 `nps.conf` 里设置**（未设置时登录页会提示）。

左侧菜单依次是：仪表盘、客户端、域名转发、TCP 隧道、UDP 隧道、混合代理、点对点访问、虚拟后端、全局参数、封禁管理、使用说明。各页面的字段说明见 [管理页面](/reference/web-ui.md)，用接口管理见 [Web API](/reference/web-api.md)。

客户端、隧道和域名的列表都支持：

- **批量操作**：勾选当前页的条目，在“批量操作”里选择启用、停用、清空流量或删除，点“执行”。翻页、搜索、排序或刷新后选择会清空。条目依次处理，个别失败不影响其余，结束后汇总结果。删除客户端会连同它的隧道和域名一起删除。
- **自动刷新**：默认每 3 秒更新当前页可见列和已展开详情里的动态数据（在线状态、连接数、流量），不重载整张表，可在标题右侧暂停。浏览器标签页在后台时不请求。新增或删除条目需要手动点“刷新”。
- **踢下线**：在客户端列表里断开某个在线客户端的连接，配置不变。开启了自动重连的 npc 几秒后会重新连上；要阻止它连接，请改用“停用”。
- **近期上线/离线**：客户端详情里显示最近几组上线、离线时间，组数由 `client_history_limit` 决定（默认 5，`0` 关闭）。

ID 为负数的是服务端本地代理的虚拟客户端。管理员可以查看、编辑、启停、设置限制和清空流量，也可以把隧道和域名挂到它名下；它不参与 npc 认证，不支持远程更新、删除和批量勾选。

## 修改配置并重载

`nps.conf` 的位置和全部配置项见[服务端配置](/reference/server-config.md)。配置目录不在默认位置时，用 `-conf_path` 指定，并且装成服务时也要带上，例如 `./nps install -conf_path=/app/nps`。改完文件后，**多数设置不用重启**，让运行中的 nps 重新读一遍即可：

- **网页**：管理员打开“全局参数”页，点“重新加载配置”。Windows 用这种方式。也可以调用接口 `POST /api/v1/global/reload`。
- **信号**：给 nps 进程发 `SIGUSR1`（Windows 没有这个信号）。

```bash
sudo systemctl kill -s USR1 Nps   # 用 nps install 装成服务的，服务名是 Nps
docker kill -s USR1 nps           # Docker 部署，nps 是容器名
sudo kill -USR1 <pid>             # 前台运行或其他方式，<pid> 是 nps 进程号
```

重载的规则：

- 每次重载在日志里写一条结果（事件 `config_applied` 或 `config_failed`，带 `file`、`keys`、`restart` 等字段），网页上直接列出“已生效”“需要重启”“失败”三组配置键，接口返回同样内容。
- 配置文件读不出来或有语法错误时，什么都不变。
- 某一项应用失败时（例如网页管理界面无法按新端口监听），整次重载撤销，已经应用的部分也会改回原样。
- 哪些设置重载就生效、哪些要重启，见本页下方的[完整清单](/guide/server/operations.md?id=各项设置重载后的效果)。需要重启的设置会在重载结果里列出，不会被悄悄忽略。

## 更新 nps

```bash
sudo nps update   # Linux、macOS
nps.exe update    # Windows，用管理员身份运行
```

- 服务在运行也可以直接更新，更新完会自动重启；已经是最新版本时不替换文件、也不重启。
- 更新是覆盖式的：发布包里有的程序、网页文件（`web/views`、`web/static`）和配置参考文件会新增或覆盖，磁盘上包里没有的文件（例如你自己放的 404 页面）保留。更新失败或未确认而回滚时，网页目录一并恢复。发布包不带 Geo 数据，它只由 `nps update-geo` 或网页更新。
- 替换之前会先验证新版本能运行，并更新系统服务实际运行的那个程序。
- `nps update-force` 不管当前是不是最新版，都重新安装最新发布版，可用于同版本重装、从较新版退回，或重试之前失败的版本。
- 不会跨大版本（例如 0.x.x 到 1.x.x）更新，规则以发布清单为准，大版本升级需要手动进行。

**只更新 Geo 数据**，运行中的 nps 一分钟内自动生效：

```bash
sudo nps update-geo          # 更新 geoip，以及已经存在的其他 Geo 文件
sudo nps update-geo geosite  # 更新指定文件，本地没有时下载
```

**在网页上更新**：管理员在“仪表盘”的“服务端版本”卡片点更新按钮（有新版本时才显示），服务端更新后自动重启，页面随后自动刷新。

- 重启期间连接会短暂中断，npc 会自动重连。
- Linux、macOS 等系统不管以服务还是命令行运行都支持；Windows 只支持用 `nps install` 装成系统服务的情况。
- 启动参数 `-disable_remote_update` 可以禁止网页更新 nps 自身（不影响更新客户端）；官方 Docker 镜像默认已禁止，Docker 部署请换镜像升级。

从 0.34 及更早版本升级、Docker 换镜像和回退，见[升级到 0.35](/guide/server/upgrade.md)；升级前请先[备份](/guide/server/backup.md)。

### 手动替换程序

`nps update` 失败时，到[发布页](https://github.com/djylb/nps/releases/latest)下载最新版本，覆盖原有的 `nps` 程序和 `web` 目录。程序位置可以用 `whereis nps` 查。

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

启动网页管理时，nps 会在配置文件所在目录自动生成 `instance.key`，用 `instance_key_file` 可以改位置（相对路径以配置文件所在目录为准）。网页登录的 Cookie 签名和加密密钥由它派生，与管理员账号、密码、TOTP、API 密钥都无关；`admit_key` 没单独设置时也由它派生，重启后不变。

- 以后重启、修改登录配置都不会更换它。**请备份**（见[备份与恢复](/guide/server/backup.md)）；Docker 部署要把它放在挂载的目录里；多个 nps 副本只有共享同一个文件才能互认 Cookie。
- 在 Unix 上新建文件的权限是 `0600`。自定义目录必须事先存在且可写，并支持同目录原子硬链接，否则启动报错。
- 已有的密钥读不出来、格式或校验和错误时启动会失败，不会自动覆盖。删掉文件后下次启动会生成新的，原有的登录状态全部失效。
- 它是敏感凭据，不要公开或提交到版本库。

## 常用设置

```ini
# 流量统计每隔多少分钟保存一次；不写则不持久化
flow_store_interval=10
# 在仪表盘显示系统负载
system_info_display=true
# 允许客户端用户登录网页（默认用户名 user，默认密码是各自的 vkey，可以修改）
allow_user_login=true
# 登录页显示注册按钮
allow_user_register=true
# 允许在隧道上指定监听 IP
allow_multi_ip=true
```

**代理到服务器本地**：相当于在 nps 所在的机器上起一个 npc，把流量送回本机。设置 `allow_local_proxy=true`（普通用户还需要 `allow_user_local=true`），然后在隧道或域名上选择“代理到服务器本地”。例如本机网站在 `5000` 端口，添加域名并把目标设为 `127.0.0.1:5000`。

## 各项设置重载后的效果

下面按设置分组列出：重载后马上生效的，和必须重启 nps 才生效的。每个配置键只出现一次，已弃用的键也在其中。

立即生效：

- 日志级别与时间同步：`log_level`、`log_secrets`、`ntp_server`、`ntp_interval`
- 统计与限制：`system_info_display`、`flow_limit_type`、`client_history_limit`
- Webhook：`webhook_url`、`webhook_secret`、`webhook_timeout`、`webhook_retries`、`webhook_queue_size`、`webhook_report_interval`
- Web 管理界面：`appname`、`web_base_url`、`head_custom_code`、`web_close_on_not_found`、`web_tunnel_port_range_max`、`instance_key_file`、`admit_key`、`ip_register_max_hours`、`http_proxy_access_port`、`https_proxy_access_port`
- 账号与登录：`web_username`、`web_password`、`totp_secret`、`web_require_password`、`auth_key`、`auth_crypt_key`、`allow_x_real_ip`、`trusted_proxy_ips`、`allow_user_login`、`allow_user_register`、`allow_user_vkey_login`、`allow_user_change_username`
- 用户功能开关：`allow_flow_limit`、`allow_rate_limit`、`allow_time_limit`、`allow_connection_num_limit`、`allow_multi_ip`、`allow_tunnel_num_limit`、`allow_user_local`
- 登录防护：`open_captcha`、`force_pow`、`pow_bits`、`login_ban_time`、`login_ip_ban_time`、`login_user_ban_time`、`login_max_fail_times`、`login_max_body`、`login_max_skew`、`login_acl_mode`、`login_acl_rules`
- 客户端连接地址的显示：`bridge_addr`、`bridge_show_path`、`bridge_tcp_show`、`bridge_tcp_show_ip`、`bridge_tcp_show_port`、`bridge_kcp_show`、`bridge_kcp_show_ip`、`bridge_kcp_show_port`、`bridge_quic_show`、`bridge_quic_show_ip`、`bridge_quic_show_port`、`bridge_tls_show`、`bridge_tls_show_ip`、`bridge_tls_show_port`、`bridge_ws_show`、`bridge_ws_show_ip`、`bridge_ws_show_port`、`bridge_wss_show`、`bridge_wss_show_ip`、`bridge_wss_show_port`、`bridge_quic_show_alpn`
- 公共密钥与访客密钥：`public_vkey`、`visitor_vkey`（原密钥的连接随即断开）
- 运行时读取的：`allow_local_proxy`、`http_add_origin_header`、`auth_time_window`（`allow_local_proxy` 只影响之后启动的隧道；`allow_local_proxy` 变更时点对点访问的授权失效，访问端重新授权）

以下变更需要重启才生效，重载时列出：

- 日志输出：`log`、`log_path`、`log_max_files`、`log_max_days`、`log_max_size`、`log_compress`、`log_format`、`security_log`、`log_memory`、`log_instance`、`access_log`、`access_log_format`
- 进程：`timezone`、`dns_server`、`pprof_ip`、`pprof_port`、`geoip_path`、`geosite_path`、`acl_max_rules`、`flow_store_interval`
- 客户端连接监听：`bridge_ip`、`bridge_host`、`bridge_path`、`bridge_port`、`mix_ip`、`mix_port`、`mix_plain`、`mix_legacy`、`client_max_pipes`、`session_idle_s`、`session_resume_timeout`、`session_max_lanes`、`session_max_services`、`bridge_trusted_ips`、`bridge_real_ip_header`、`shaping_url`、`shaping_token`、`shaping_pubkey`、`shaping_file`、`new_protocol_tls_alpn`、`new_protocol_quic_alpn`、`new_protocol_ws_paths`、`compress_max_window`、`compress_max_ratio`、`bridge_tcp_ip`、`bridge_tcp_port`、`bridge_kcp_ip`、`bridge_kcp_port`、`bridge_quic_ip`、`bridge_quic_port`、`bridge_tls_ip`、`bridge_tls_port`、`bridge_ws_ip`、`bridge_ws_port`、`bridge_wss_ip`、`bridge_wss_port`、`tls_bridge_port`、`bridge_type`、`tcp_enable`、`kcp_enable`、`quic_enable`、`tls_enable`、`ws_enable`、`wss_enable`
- QUIC 传输：`quic_alpn`、`quic_keep_alive_period`、`quic_max_idle_timeout`、`quic_max_incoming_streams`
- 客户端连接：`bridge_select_mode`、`bridge_cert_file`、`bridge_key_file`、`secure_mode`、`allow_ports`、`disconnect_timeout`、`mux_ping_interval`
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

Docker 部署用 `docker restart nps`。这几个命令控制的是用 `nps install` 装好的系统服务，没装服务、前台运行时按 `Ctrl+C` 停止。停止时 nps 最多等 10 秒让连接收尾。
