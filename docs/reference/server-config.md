# 服务端配置 nps.conf

本页列出 `nps.conf` 的全部配置项。示例文件在发布包的 `conf/nps.conf`，只写了常用的几十项；没有写出的项使用本页的默认值。

## 文件和写法

- **位置**：安装后在 `/etc/nps/conf/nps.conf`（Windows 为 `C:\Program Files\nps\conf\nps.conf`）；直接在解压目录运行时是解压目录里的 `conf/nps.conf`。除 `.conf` 外，同一位置的 `nps.ini`、`nps.yaml`、`nps.yml`、`nps.json` 也能读。用 `-c` 指定别的位置，详见 [nps 命令行](/reference/nps-cli.md)。
- **格式**：`.conf`、`.ini` 是 `键=值`，`#` 或 `;` 开头的行是注释。YAML、JSON 里嵌套的层级用下划线连成键名，例如 `web:` 下的 `port` 等同于 `web_port`。
- **键名**不分大小写，`-`、`.`、空格都按 `_` 处理。
- **值**：布尔值写 `true`/`false`（也接受 `1`/`0`）；数字写十进制整数。空值，或无法识别的布尔值和数字，都当作“没写”，使用默认值。
- **相对路径**以运行目录为准：安装后是 `/etc/nps`（Windows 为 `C:\Program Files\nps`），在解压目录运行时是程序所在目录。只有 `instance_key_file` 例外，它相对 `nps.conf` 所在的目录。证书类的项可以直接填 PEM 文本，不必是文件路径。
- **默认值**列写的是配置里没有这一项时的值。示例文件里写了不同值的，在括号里注明“示例文件”。例如删掉 `web_port` 会关闭网页管理，而示例文件里是 `8081`。
- **生效**列：“立即”表示改完后在网页“全局参数”页点“重新加载配置”，或给进程发 `SIGUSR1` 就生效；“重启”表示要重启 nps 才生效。重载的做法和失败时的处理见[运维](/guide/server/operations.md)。
- 正式使用前请改掉 `web_password` 的示例值。

## 运行

| 键 | 默认值 | 取值和作用 | 生效 |
| --- | --- | --- | --- |
| `appname` | `nps` | 网页界面显示的名称，同时是这台服务器在接口里的节点名 | 立即 |
| `secure_mode` | `false`（示例文件 `true`） | `true`：检查认证的时间戳并防重放，能连接的 npc 最低协议版本为 0.33.0；`false`：0.26.0 起的 npc 都能连，但不做时间戳校验和防重放。版本对应关系见[版本规则与兼容范围](/reference/versioning.md) | 重启 |
| `dns_server` | 空（示例文件 `8.8.8.8`） | 优先使用的 DNS 服务器地址；它不可达时退回系统 DNS | 重启 |
| `disconnect_timeout` | `30`（示例文件 `30`） | 秒。客户端断开后等多久才判定为离线。写在配置文件里时，也会作为判定连接已失效的秒数下发给当前版本的 npc | 重启 |
| `timezone` | 空（示例文件未启用） | 时区名，如 `Asia/Shanghai`；空则用系统时区 | 重启 |

## 混合端口

混合端口 `mix_port` 是客户端连接服务器的统一入口：TCP 端口接收 `tls`、`wss` 和域名转发，同一个端口号的 UDP 接收 `quic`。npc 的连接方式见[连接方式](/guide/client/connect.md)。

| 键 | 默认值 | 取值和作用 | 生效 |
| --- | --- | --- | --- |
| `mix_port` | `0`（示例文件 `8024`） | 混合端口号，`0` 关闭。TCP 和 UDP 用同一个号码，防火墙要两种都放行 | 重启 |
| `mix_ip` | 同 `bridge_ip`（默认 `0.0.0.0`） | 混合端口的监听地址 | 重启 |
| `mix_plain` | `false` | `true`：混合端口也接受明文连接（`tcp`、`ws`）。只在前面有自己的 TLS 反向代理时打开。关闭时，明文 HTTP 请求仍交给域名转发，其他明文连接不响应 | 重启 |
| `mix_legacy` | `true` | 混合端口是否接受 0.34 及更早的 npc。确认没有这些客户端后可以关掉 | 重启 |
| `bridge_ip` | `0.0.0.0` | 客户端连接端口的默认监听地址；`mix_ip` 没写时沿用它 | 重启 |
| `bridge_addr` | 空 | 网页上生成的启动命令里写的服务器地址（只取主机部分）。空则用你访问网页时用的地址 | 立即 |
| `bridge_path` | `/ws` | WebSocket 连接（`ws`、`wss`）使用的路径 | 重启 |
| `bridge_host` | 空 | 客户端端口与网页或域名转发共用端口，或 HTTP/3 与客户端 QUIC 共用 UDP 端口时，用来认出客户端连接的域名 | 重启 |
| `bridge_select_mode` | 空，等同 `Primary` | 多个客户端用同一个 `vkey` 时怎么选：`Primary` 主备，`RoundRobin` 轮询，`Random` 随机。不区分大小写，也接受 `p`、`rr`、`rand` 或数字 `0`、`1`、`2`；写错时用主备 | 重启 |
| `bridge_cert_file` | 空 | `tls`、`wss` 连接用的证书，路径或 PEM 文本；空则自动生成 | 重启 |
| `bridge_key_file` | 空 | 上面证书的私钥，路径或 PEM 文本 | 重启 |
| `bridge_trusted_ips` | 空 | 客户端连接前面有反向代理时，信任的代理地址，逗号分隔，支持网段；`*` 表示全部。只有来自这些地址的连接才会采信下面的请求头 | 重启 |
| `bridge_real_ip_header` | 空 | 从哪个请求头取客户端真实 IP。空则依次看 `X-Forwarded-For`、`X-Real-IP` | 重启 |
| `public_vkey` | 空 | 公共密钥。设置后服务端自动建立一个使用该密钥的客户端，供用配置文件连接的 npc 使用；同时它拥有管理员级的 IP 登记权限。空为禁用 | 立即 |
| `visitor_vkey` | 空 | 访客密钥。用它连接的客户端只能访问点对点隧道，不能注册隧道。设置后，网页上点对点访问的命令用它代替客户端密钥。空为禁用 | 立即 |
| `allow_ports` | 空 | 允许使用的隧道端口，如 `9001-9009,10001,11000-12000`；空为不限制 | 重启 |
| `p2p_port` | `0`（示例文件 `6000`） | 点对点用的 NAT 观测端口，占用从该端口起的连续 3 个 UDP 端口；`0` 关闭点对点 | 重启 |
| `p2p_ip` | 空 | 告知客户端的服务器地址（IP 或域名，逗号分隔）。空则自动识别 IPv4 和 IPv6；只写 `0.0.0.0` 只识别 IPv4，只写 `::` 只识别 IPv6 | 重启 |

### 已弃用的键

下面的键对应 0.34 及更早的“每种连接方式一个端口”的写法，已弃用。新写法统一使用混合端口 `mix_port`，不要在新配置里使用它们。

| 键 | 默认值 | 说明 | 生效 |
| --- | --- | --- | --- |
| `bridge_port` | `0` | 已弃用，改用 `mix_port`。是 `bridge_tcp_port`、`bridge_kcp_port` 的默认值。只写了它、没写其他端口，且不与网页或域名转发共用端口时，它按混合端口处理：`tcp`、`tls`、`ws`、`wss` 都接受，按首字节分流。`bridge_type` 为 `kcp`、`udp`、`quic` 时，它是只监听 UDP 的端口 | 重启 |
| `bridge_tcp_port` | 同 `bridge_port` | 已弃用，改用 `mix_port`。`tcp` 端口，`0` 关闭 | 重启 |
| `bridge_tls_port` | `0` | 已弃用，改用 `mix_port`。`tls` 端口 | 重启 |
| `tls_bridge_port` | `0` | 已弃用，是 `bridge_tls_port` 的旧名，仍能读取，两个都写时 `bridge_tls_port` 优先。改写成 `mix_port` | 重启 |
| `bridge_ws_port` | `0` | 已弃用，改用 `mix_port`。`ws` 端口 | 重启 |
| `bridge_wss_port` | `0` | 已弃用，改用 `mix_port`。`wss` 端口 | 重启 |
| `bridge_quic_port` | `0` | 已弃用，改用 `mix_port`（它的 UDP 同号端口接 `quic`）。`quic` 端口 | 重启 |
| `bridge_kcp_port` | 同 `bridge_port` | 已弃用，改用 `quic`。KCP 端口；未关 `quic_enable` 时，这个 UDP 端口同时接 `quic`，按首个数据包区分 | 重启 |
| `bridge_tcp_ip` | 同 `bridge_ip` | 已弃用，改用 `mix_ip`。`tcp` 端口的监听地址 | 重启 |
| `bridge_tls_ip` | 同 `bridge_ip` | 已弃用，改用 `mix_ip`。`tls` 端口的监听地址 | 重启 |
| `bridge_ws_ip` | 同 `bridge_ip` | 已弃用，改用 `mix_ip`。`ws` 端口的监听地址 | 重启 |
| `bridge_wss_ip` | 同 `bridge_ip` | 已弃用，改用 `mix_ip`。`wss` 端口的监听地址 | 重启 |
| `bridge_quic_ip` | 同 `bridge_ip` | 已弃用，改用 `mix_ip`。`quic` 端口的监听地址 | 重启 |
| `bridge_kcp_ip` | 同 `bridge_ip` | 已弃用。KCP 端口的监听地址 | 重启 |
| `bridge_type` | `both` | 已弃用，改用 `mix_port`。取值 `tcp`、`kcp`、`quic`、`udp`、`both`，决定上面哪些端口生效 | 重启 |
| `tcp_enable` | `true` | 已弃用。设为 `false` 单独关闭 `bridge_tcp_port` | 重启 |
| `tls_enable` | `true` | 已弃用。设为 `false` 单独关闭 `bridge_tls_port` | 重启 |
| `ws_enable` | `true` | 已弃用。设为 `false` 单独关闭 `bridge_ws_port` | 重启 |
| `wss_enable` | `true` | 已弃用。设为 `false` 单独关闭 `bridge_wss_port` | 重启 |
| `quic_enable` | `true` | 已弃用。设为 `false` 单独关闭 `bridge_quic_port`，也让 KCP 端口不再接 `quic` | 重启 |
| `kcp_enable` | `true` | 已弃用。设为 `false` 关闭 KCP 端口 | 重启 |
| `bridge_tcp_show` | 对应端口启用时为 `true` | 已弃用。网页上是否显示 `tcp` 连接命令 | 立即 |
| `bridge_tcp_show_ip` | 空 | 已弃用。`tcp` 命令里显示的地址，空则用 `bridge_addr` | 立即 |
| `bridge_tcp_show_port` | 对应监听端口 | 已弃用。`tcp` 命令里显示的端口 | 立即 |
| `bridge_tls_show` | 对应端口启用时为 `true` | 已弃用。网页上是否显示 `tls` 连接命令 | 立即 |
| `bridge_tls_show_ip` | 空 | 已弃用。`tls` 命令里显示的地址 | 立即 |
| `bridge_tls_show_port` | 对应监听端口 | 已弃用。`tls` 命令里显示的端口 | 立即 |
| `bridge_ws_show` | 对应端口启用时为 `true` | 已弃用。网页上是否显示 `ws` 连接命令 | 立即 |
| `bridge_ws_show_ip` | 空 | 已弃用。`ws` 命令里显示的地址 | 立即 |
| `bridge_ws_show_port` | 对应监听端口 | 已弃用。`ws` 命令里显示的端口 | 立即 |
| `bridge_wss_show` | 对应端口启用时为 `true` | 已弃用。网页上是否显示 `wss` 连接命令 | 立即 |
| `bridge_wss_show_ip` | 空 | 已弃用。`wss` 命令里显示的地址 | 立即 |
| `bridge_wss_show_port` | 对应监听端口 | 已弃用。`wss` 命令里显示的端口 | 立即 |
| `bridge_quic_show` | 对应端口启用时为 `true` | 已弃用。网页上是否显示 `quic` 连接命令 | 立即 |
| `bridge_quic_show_ip` | 空 | 已弃用。`quic` 命令里显示的地址 | 立即 |
| `bridge_quic_show_port` | 对应监听端口 | 已弃用。`quic` 命令里显示的端口 | 立即 |
| `bridge_quic_show_alpn` | `quic_alpn` 的第一项 | 已弃用。`quic` 命令里显示的 ALPN 名称 | 立即 |
| `bridge_kcp_show` | 对应端口启用时为 `true` | 已弃用。网页上是否显示 `kcp` 连接命令 | 立即 |
| `bridge_kcp_show_ip` | 空 | 已弃用。`kcp` 命令里显示的地址 | 立即 |
| `bridge_kcp_show_port` | 对应监听端口 | 已弃用。`kcp` 命令里显示的端口 | 立即 |
| `bridge_show_path` | 同 `bridge_path` | 已弃用。`ws`、`wss` 命令里显示的路径 | 立即 |

## 域名代理

域名转发服务的监听、HTTPS 证书和自动申请证书。用法见 [HTTPS 与反向代理](/guide/server/https-and-proxy.md)。端口为 `0` 的不启动。

| 键 | 默认值 | 取值和作用 | 生效 |
| --- | --- | --- | --- |
| `http_proxy_ip` | `0.0.0.0` | HTTP、HTTPS 的监听地址 | 重启 |
| `http_proxy_port` | `0`（示例文件 `80`） | HTTP 端口 | 重启 |
| `https_proxy_port` | `0`（示例文件 `443`） | HTTPS 端口 | 重启 |
| `https_default_cert_file` | 空（示例文件 `conf/server.pem`） | 域名没有自带证书时使用的默认证书，路径或 PEM 文本 | 重启 |
| `https_default_key_file` | 空（示例文件 `conf/server.key`） | 默认证书的私钥，路径或 PEM 文本 | 重启 |
| `force_auto_ssl` | `false` | `true`：所有域名都自动申请证书。需要 80 和 443 能从公网访问 | 重启 |
| `ssl_email` | 空（示例文件 `you@yours.com`） | 自动申请证书时登记的邮箱，请换成自己的 | 重启 |
| `ssl_ca` | `LetsEncrypt`（示例文件 `LetsEncrypt`） | 证书颁发机构：`LetsEncrypt`、`ZeroSSL`、`GoogleTrust` | 重启 |
| `ssl_zerossl_api` | 空 | 使用 `ZeroSSL` 时的 API 密钥 | 重启 |
| `ssl_path` | `ssl`（示例文件 `conf/ssl`） | 自动申请的证书的保存目录 | 重启 |
| `ssl_cache_reload` | `0`（示例文件 `3600`） | 秒。每隔多久检查一次证书文件有没有更换，`0` 不检查 | 重启 |
| `http_add_origin_header` | `false`（示例文件 `true`） | `true`：转发给内网服务的请求带上访问者的真实 IP（`X-Forwarded-For`、`X-Real-IP`） | 立即 |
| `x_nps_http_only` | 空 | 前面的反向代理在请求头 `X-NPS-Http-Only` 里带上这个值时，nps 信任它：不再把 HTTP 请求跳转到 HTTPS，登录时也采信它传来的真实 IP。空为不启用 | 重启 |

## 网页管理

| 键 | 默认值 | 取值和作用 | 生效 |
| --- | --- | --- | --- |
| `web_username` | 空（示例文件也为空） | 管理员账号 | 立即 |
| `web_password` | 空（示例文件也为空） | 管理员密码。`web_username`、`web_password`、`totp_secret` 三项都为空且 `web_require_password` 未开时，打开网页无需登录就是管理员 | 立即 |
| `web_require_password` | `false`（示例文件 `true`） | `true`：`web_username` 为空，或密码和 `totp_secret` 都为空时，任何人都不能登录管理员（不会免登录），登录页提示先在 `nps.conf` 里设置，再重启或重载；客户端用户登录不受影响 | 立即 |
| `web_host` | 空（示例文件 `a.o.com`） | 网页管理与客户端端口共用端口时，用来认出网页访问的域名 | 重启 |
| `web_ip` | `0.0.0.0` | 监听地址 | 重启 |
| `web_port` | `0`（示例文件 `8081`） | 监听端口，`0` 关闭网页管理 | 重启 |
| `web_open_ssl` | `false`（示例文件 `false`） | `true`：网页管理使用 HTTPS | 重启 |
| `web_cert_file` | 空（示例文件 `conf/server.pem`） | 网页管理的 HTTPS 证书，路径或 PEM 文本 | 重启 |
| `web_key_file` | 空（示例文件 `conf/server.key`） | 网页管理的 HTTPS 私钥，路径或 PEM 文本 | 重启 |
| `web_base_url` | 空 | 网页地址的路径前缀，如 `/nps`，用于挂在反向代理的子路径下 | 立即 |
| `allow_user_login` | `false`（示例文件 `false`） | 允许客户端用户登录网页管理，登录后只能管理自己的客户端 | 立即 |
| `allow_user_register` | `false`（示例文件 `false`） | 允许在登录页注册用户 | 立即 |
| `allow_user_change_username` | `false`（示例文件 `false`） | 允许用户修改自己的用户名 | 立即 |

## 安全

登录来源名单的写法见[访问控制](/guide/server/access-control.md)。

| 键 | 默认值 | 取值和作用 | 生效 |
| --- | --- | --- | --- |
| `open_captcha` | `false`（示例文件 `true`） | 登录页是否要求验证码 | 立即 |
| `totp_secret` | 空 | 管理员的双因素认证密钥。设置后登录需要再输入 6 位动态码，接在密码后面，开启验证码时也可接在验证码后面。用 `nps -gen2fa` 生成密钥；用 `nps -get2fa=密钥` 查看当前动态码 | 立即 |
| `auth_key` | 空 | [Web API](/reference/web-api.md) 的认证密钥，空为禁用 | 立即 |
| `auth_crypt_key` | 空 | 用来加密“获取 authKey”结果的 AES 密钥（16 位），空为禁用 | 立即 |
| `allow_x_real_ip` | `false`（示例文件 `false`） | `true`：网页登录时采信 `X-Real-IP` 作为访问者 IP。前面有反向代理时打开 | 立即 |
| `trusted_proxy_ips` | `127.0.0.1`（示例文件 `127.0.0.1`） | 信任的反向代理地址，逗号分隔，支持网段。只有来自这些地址的请求才会采信 `X-Real-IP` | 立即 |
| `login_acl_mode` | `0`（示例文件未启用） | 登录来源名单：`0` 关闭，`1` 白名单（只有名单内的来源能登录），`2` 黑名单 | 立即 |
| `login_acl_rules` | 空（示例文件未启用） | 登录来源名单的规则，逗号或换行分隔，写法同[访问控制](/guide/server/access-control.md) | 立即 |
| `instance_key_file` | `instance.key`（示例文件未启用） | 实例主密钥文件，相对 `nps.conf` 所在目录。不存在时自动生成。网页登录状态的签名和客户端启动链接里的准入密钥都由它派生，请备份并保密，丢失后已发出的启动链接会失效 | 立即 |

## 限制

这些开关控制网页上是否出现对应的限制选项，以及是否执行。

| 键 | 默认值 | 取值和作用 | 生效 |
| --- | --- | --- | --- |
| `allow_flow_limit` | `false`（示例文件 `true`） | 允许给客户端、隧道、域名设置流量上限 | 立即 |
| `allow_rate_limit` | `false`（示例文件 `true`） | 允许设置带宽上限 | 立即 |
| `allow_time_limit` | `false`（示例文件 `true`） | 允许设置到期时间 | 立即 |
| `allow_tunnel_num_limit` | `false`（示例文件 `true`） | 允许限制客户端的隧道数量 | 立即 |
| `allow_connection_num_limit` | `false`（示例文件 `true`） | 允许限制客户端的并发连接数 | 立即 |
| `allow_local_proxy` | `false`（示例文件 `true`） | 允许隧道使用“代理到服务器本地”，即从 nps 所在机器出站。只影响之后启动的隧道；改动后点对点访问的授权会失效，访问端会重新授权 | 立即 |
| `allow_user_local` | 同 `allow_local_proxy`（示例文件 `false`） | 普通用户是否也能使用“代理到服务器本地”。示例文件里只有管理员能用 | 立即 |
| `allow_multi_ip` | `false`（示例文件 `true`） | 允许给隧道单独设置监听 IP | 立即 |
| `flow_store_interval` | `0`（示例文件 `1`） | 分钟。每隔多久把流量统计写入磁盘，`0` 不定时保存 | 重启 |
| `system_info_display` | `false`（示例文件 `true`） | 仪表盘显示服务器的系统负载 | 立即 |

## 日志

日志内容和排查见[日志](/guide/server/logging.md)。

| 键 | 默认值 | 取值和作用 | 生效 |
| --- | --- | --- | --- |
| `log` | `stdout`（示例文件 `stdout`） | 输出位置：`stdout` 屏幕、`file` 文件、`both` 两者、`off` 关闭。作为系统服务运行时，`stdout` 自动改为写文件 | 重启 |
| `log_level` | `info`（示例文件 `info`） | `trace`、`debug`、`info`、`warn`、`error`、`off`。可按模块单独设置，如 `info,p2p=debug` | 立即 |
| `log_path` | 空，使用系统默认路径（示例文件 `conf/nps.log`） | 日志文件路径。空则用系统默认：Linux/macOS 为 `/var/log/nps.log`，Windows 为程序目录下的 `nps.log`。写 `off`、`false` 或 `docker` 表示不写文件 | 重启 |
| `log_compress` | `false`（示例文件 `false`） | 旧日志文件是否压缩 | 重启 |
| `log_max_files` | `30`（示例文件 `10`） | 保留的日志文件个数 | 重启 |
| `log_max_days` | `30`（示例文件 `7`） | 日志文件保留的天数 | 重启 |
| `log_max_size` | `5`（示例文件 `2`） | MB。单个日志文件的大小上限，超过后换新文件；`0` 或负数不按大小切分 | 重启 |
| `security_log` | 空（示例文件未启用） | 另外写一份只含安全事件的文件，供 fail2ban 等工具读取。切分设置同主日志 | 重启 |
| `access_log` | 空（示例文件未启用） | 域名转发的访问日志文件，每个请求一行，包括被拒绝的。不受 `log_level` 影响，切分设置同主日志 | 重启 |

## 高级

一般不需要改的项。

### 运行和数据

| 键 | 默认值 | 取值和作用 | 生效 |
| --- | --- | --- | --- |
| `ntp_server` | 空 | 时间同步的 NTP 服务器地址，空为不同步 | 立即 |
| `ntp_interval` | `5` | 分钟。时间同步的最短间隔 | 立即 |
| `client_history_limit` | `5` | 客户端详情里保留的上线、离线记录组数，`0` 关闭，负数当 `5` | 立即 |
| `geoip_path` | `conf/geoip.dat` | IP 归属地数据文件，访问控制名单里按国家写规则时用。可用 `nps update-geo` 更新 | 重启 |
| `geosite_path` | `conf/geosite.dat` | 域名分类数据文件，同上 | 重启 |
| `acl_max_rules` | `1000` | 客户端、隧道、域名各自名单的最大规则条数，`0` 不限；全局名单不受此限 | 重启 |
| `pprof_ip` | 空 | 性能分析接口的监听地址，仅排查问题时使用 | 重启 |
| `pprof_port` | `0` | 性能分析接口的端口，`0` 关闭 | 重启 |
| `ip_register_max_hours` | `2` | 小时。用公共密钥，或没设置上限的客户端密钥登记 IP 时，登记的最长有效期 | 立即 |
| `ip_register_ip` | `0.0.0.0` | 单独提供 IP 登记页面时的监听地址 | 重启 |
| `ip_register_port` | `0` | 单独提供 IP 登记页面的端口，`0` 不单独监听 | 重启 |
| `ip_register_cert_file` | 空 | 登记页面的 HTTPS 证书，路径或 PEM 文本；与私钥都填了才用 HTTPS | 重启 |
| `ip_register_key_file` | 空 | 登记页面的 HTTPS 私钥 | 重启 |

### 网页管理细节

| 键 | 默认值 | 取值和作用 | 生效 |
| --- | --- | --- | --- |
| `admit_key` | 空 | 客户端启动链接里的准入密钥，64 位十六进制。空则由实例主密钥自动派生，重启后不变 | 立即 |
| `head_custom_code` | 空 | 插入到每个网页 `<head>` 里的自定义代码 | 立即 |
| `web_close_on_not_found` | `false` | `true`：访问不存在的网页路径时直接断开连接，不返回内容 | 立即 |
| `web_tunnel_port_range_max` | `1000` | 在网页上新增 TCP、UDP 隧道时，端口范围（如 `8000-8100,9000`）一次最多展开成几条隧道，`0` 不限 | 立即 |
| `flow_limit_type` | `total` | 客户端流量上限的统计口径：`total` 按上下行流量之和；`actual` 按实际消耗（转发一份数据进出各算一次，约为两倍）。隧道和域名始终按 `total` | 立即 |
| `http_proxy_access_port` | 同 `http_proxy_port` | 访问者从外面访问域名转发的 HTTP 端口，只用来生成链接。前面有端口映射或反向代理时才需要 | 立即 |
| `https_proxy_access_port` | 同 `https_proxy_port` | 同上，HTTPS 端口 | 立即 |
| `force_pow` | `false` | `true`：每次登录都要做工作量证明 | 立即 |
| `pow_bits` | `20` | 工作量证明的难度位数，`0` 关闭该检查 | 立即 |
| `login_ban_time` | `5` | 秒。一次登录失败后，该来源暂时不能再试的时间 | 立即 |
| `login_ip_ban_time` | `180` | 秒。同一 IP 在这段时间内失败达到 `login_max_fail_times` 次后，被封禁这么久 | 立即 |
| `login_user_ban_time` | `3600` | 秒。同一用户名的失败次数达到上限后，被封禁的时间 | 立即 |
| `login_max_fail_times` | `10` | 触发封禁的失败次数 | 立即 |
| `login_max_body` | `1024` | 字节。登录请求内容的大小上限 | 立即 |
| `login_max_skew` | `300000` | 毫秒。登录请求的时间戳与服务器时间的最大偏差 | 立即 |
| `auth_time_window` | `300` | 秒。`secure_mode` 下客户端认证允许的时钟偏差，`0` 不检查时间，负数当 `300` | 立即 |
| `allow_user_vkey_login` | 同 `allow_user_login` | 允许用户用客户端密钥（`vkey`）登录网页 | 立即 |

### 域名转发细节

| 键 | 默认值 | 取值和作用 | 生效 |
| --- | --- | --- | --- |
| `http3_proxy_port` | 同 `https_proxy_port` | HTTP/3 的 UDP 端口，`0` 关闭。与 `mix_port` 相同时，同一个 UDP 端口既服务 HTTP/3 又接 `quic` 连接，此时 ALPN `h3` 归 HTTP/3 | 重启 |
| `bridge_http3` | `true` | 允许客户端的 `quic` 连接复用 HTTP/3 的 UDP 端口 | 重启 |
| `http_proxy_response_timeout` | `100` | 秒。等待内网服务返回响应头的时间 | 重启 |
| `error_page` | `web/static/page/error.html` | 域名转发的错误页文件路径 | 重启 |
| `error_page_time_limit` | 空 | 域名到期时显示的页面文件路径，空为不单独设置 | 重启 |
| `error_page_flow_limit` | 空 | 域名流量用完时显示的页面文件路径，空为不单独设置 | 重启 |
| `error_always` | `false` | 访问没有配置的域名时：`false` 直接断开连接，`true` 返回 404 错误页 | 重启 |
| `fallback_404_file` | 空 | 客户端连接端口上找不到对应去向的连接收到的 404 页面文件；空则用一个很短的内置页面。不受 `error_always` 影响 | 重启 |
| `ssl_cache_max` | `0` | 内存里缓存的域名证书个数上限，`0` 不限 | 重启 |
| `ssl_cache_idle` | `60` | 分钟。证书闲置多久后移出缓存 | 重启 |

### 客户端连接细节

| 键 | 默认值 | 取值和作用 | 生效 |
| --- | --- | --- | --- |
| `client_max_pipes` | `0` | 单个客户端在所有连接上的底层数据通道总数上限，`0` 不限；超过后拒绝新通道，已有的不受影响 | 重启 |
| `session_idle_s` | `0` | 秒。空闲连接的探测周期，`0` 用内置默认值（15 秒）。写了正数也会下发给 npc | 重启 |
| `session_resume_timeout` | `0` | 秒。连接断开后等客户端恢复的时间：`0` 为 30 秒，负数为不恢复。等待期间客户端仍算在线。写了正数会下发给 npc，以服务端的为准 | 重启 |
| `session_max_lanes` | `0` | 每条 QUIC 连接允许的并行通道数：`0` 为 16，`1` 关闭并行通道 | 重启 |
| `session_max_services` | `0` | 一个客户端连接最多可上报的内部服务条数：`0` 为 4096，负数不限 | 重启 |
| `compress_max_window` | `0` | 字节。压缩传输时解压窗口的上限，`0` 为 8 MiB | 重启 |
| `compress_max_ratio` | `0` | 压缩传输时解压后与压缩前的大小比上限，`0` 为 4096，负数不检查 | 重启 |
| `new_protocol_tls_alpn` | 空 | 当前版本 npc 的 `tls` 连接上通告的 ALPN 名称，逗号分隔；空则不通告 | 重启 |
| `new_protocol_quic_alpn` | `h3` | 当前版本 npc 的 `quic` 连接使用的 ALPN 名称，逗号分隔。它与 `quic_alpn` 不能有相同的名字，否则启动报错；`http3_proxy_port` 等于 `mix_port` 时两个列表都不能含 `h3` | 重启 |
| `new_protocol_ws_paths` | 空 | 逗号分隔的路径。当前版本 npc 的 WebSocket 连接可以使用这些路径 | 重启 |
| `quic_alpn` | `nps` | 0.34 及更早的 npc 的 `quic` 连接使用的 ALPN 名称，逗号分隔 | 重启 |
| `quic_keep_alive_period` | `10` | 秒。`quic` 保活包的间隔 | 重启 |
| `quic_max_idle_timeout` | `30` | 秒。`quic` 连接空闲多久后断开 | 重启 |
| `quic_max_incoming_streams` | `100000` | 每条 `quic` 连接允许同时打开的数据流个数 | 重启 |
| `mux_ping_interval` | `5` | 秒。0.34 及更早的 npc 连接上多路复用心跳的间隔 | 重启 |
| `shaping_url` | 空 | 流量整形配置的下载地址。默认没有，配置了来源后，还要在客户端“编辑”页的“高级设置”里把“流量整形”设为“是”才对该客户端生效 | 重启 |
| `shaping_token` | 空 | 访问 `shaping_url` 时带的令牌 | 重启 |
| `shaping_pubkey` | 空 | 十六进制的公钥。设置后，下载的内容签名不对就不采用 | 重启 |
| `shaping_file` | `conf/shaping.json` | 本地的流量整形配置文件。没有设置 `shaping_url` 且这个文件存在时使用它 | 重启 |

### 点对点

点对点的使用见[点对点访问](/guide/tunnels/secret.md)。`p2p_port` 和 `p2p_ip` 在“混合端口”一节。

| 键 | 默认值 | 取值和作用 | 生效 |
| --- | --- | --- | --- |
| `p2p_stun_servers` | 空，使用内置列表 | 下发给客户端用来探测 NAT 类型的 STUN 服务器，`主机:端口` 逗号分隔；写 `none` 表示不下发 | 重启 |
| `p2p_probe_timeout_ms` | `0`，即 2000 | 毫秒。NAT 探测超时，有效范围 500 到 10000，超出范围按边界值处理 | 重启 |
| `p2p_handshake_timeout_ms` | `0`，即 20000 | 毫秒。打洞超时（所有轮次共用），范围 2000 到 60000 | 重启 |
| `p2p_transport_timeout_ms` | `0`，即 10000 | 毫秒。打洞成功后建立连接的超时，范围 1000 到 60000 | 重启 |
| `p2p_enable_upnp_portmap` | `false` | 允许客户端用 UPnP 在路由器上映射端口 | 重启 |
| `p2p_enable_pcp_portmap` | `false` | 允许客户端用 PCP 映射端口 | 重启 |
| `p2p_enable_natpmp_portmap` | `false` | 允许客户端用 NAT-PMP 映射端口 | 重启 |
| `p2p_portmap_lease_seconds` | `3600` | 秒。端口映射的租期 | 重启 |

### 日志细节

| 键 | 默认值 | 取值和作用 | 生效 |
| --- | --- | --- | --- |
| `log_format` | `auto` | `auto`：终端上带颜色，写文件或重定向时是 logfmt；`text`：始终 logfmt；`json`：JSON | 重启 |
| `log_memory` | `1000` | 内存里保留的最近日志条数，供网页上查看日志，`0` 关闭 | 重启 |
| `log_secrets` | `false` | 调试用：日志里原样写出密钥和口令。开启时会记一条警告，正式环境保持关闭 | 立即 |
| `log_instance` | 空 | 非空时每行日志末尾加 `instance=这个值`，多台 nps 的日志汇总到一起时用来区分 | 重启 |
| `access_log_format` | `combined` | 访问日志格式：`combined`（常见的 Web 服务器格式，末尾多两项：耗时微秒和域名）或 `json` | 重启 |

### Webhook

接收地址、事件和签名见 [Webhook](/guide/server/webhook.md)。

| 键 | 默认值 | 取值和作用 | 生效 |
| --- | --- | --- | --- |
| `webhook_url` | 空 | 接收事件的地址，空为关闭 | 立即 |
| `webhook_secret` | 空 | 签名密钥，空则不签名 | 立即 |
| `webhook_timeout` | `5` | 秒。单次请求超时，`0` 或负数当 `5` | 立即 |
| `webhook_retries` | `3` | 失败后重试次数，负数当 `0` | 立即 |
| `webhook_queue_size` | `1024` | 待发送队列长度，`0` 或负数当 `1024` | 立即 |
| `webhook_report_interval` | `60` | 秒。全量上报的间隔，`0` 关闭 | 立即 |

## 已移除的键

下面的键在更早的版本里有效，现在已移除。配置里还写着它们时，nps 照常启动，并在日志里提示。请按右侧的说明改写后删除。

| 键 | 说明 |
| --- | --- |
| `new_protocol` | 已移除。当前协议总是开启，直接删除这一行 |
| `log_color` | 已移除。日志不再有单独的颜色开关，终端上自动着色，格式用 `log_format` 选 |
| `ip_limit` | 已移除。它曾让所有客户端只接受已登记 IP 的访问者。现在逐个设置：在客户端、隧道或域名的编辑页打开“仅允许已登记的 IP”，或在 Web API 里用 `registered_only=1` |
| `allow_secret_local` | 已移除。点对点访问端的本地代理，在隧道打开“代理到服务器本地”且 `allow_local_proxy` 为 `true` 时生效 |
| `allow_secret_link` | 已移除。点对点访问端可以指定任意目标，除非隧道自己的目标名单另有限制 |

`tls_bridge_port` 是 `bridge_tls_port` 的旧名，仍能读取，见“混合端口”一节的“已弃用的键”。
