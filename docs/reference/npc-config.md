# npc.conf 配置

`conf/npc.conf` 是 npc 的 INI 配置文件。运行 npc 时不带 `-launch`、`-server`、`-vkey` 就读取它，也可以用 `-config=路径` 指定，多个路径用逗号分隔。

基本格式：`[段名]` 开始一段，每行 `键=值`，`#` 开头的行是注释。段名不能重复，必须有 `[common]` 段。值里可以用 `{{.环境变量名}}` 引用环境变量。

```ini
[common]
server_addr=203.0.113.10:8024
conn_type=tls
vkey=YOUR_VKEY
auto_reconnection=true
```

`vkey` 在网页“客户端”列表的“唯一验证密钥”处查看。

段的识别顺序：

1. 名称以 `secret` 开头且没有 `mode` 键：点对点访问段。
2. 名称以 `p2p` 开头且没有 `mode` 键：已弃用的点对点访问段。
3. 含 `health_` 开头的键：已弃用，整段被忽略并报警告。
4. 名称是 `common`：公共段。
5. 含 `host` 键：域名转发段（已弃用）。
6. 其他：隧道段（已弃用）。

## [common]

### 连接

| 键 | 默认值 | 说明 |
| --- | --- | --- |
| `server_addr` | 无 | 服务端地址。可以用 `\|` 写多条路径，路径是 URL 时参数见[启动链接与配置格式](/reference/npc-launch.md)。不写协议的路径，协议取 `conn_type`。写错时报错 |
| `vkey` | 无 | 客户端密钥 |
| `conn_type` | `tcp` | 不写协议的路径使用的协议：`tcp`、`tls`、`ws`、`wss`、`quic`。`kcp` 已弃用，报警告后按 `quic` 连接 |
| `auto_reconnection` | `true` | 断线后自动重连 |
| `proxy_url` | 无 | 连接服务端使用的代理，格式同 `-proxy` |
| `local_ip` | 无 | 出站连接使用的本机源地址 |
| `dns_server` | 无 | 解析域名用的 DNS 服务器，覆盖 `-dns_server` |
| `ntp_server` | 无 | NTP 服务器，覆盖 `-ntp_server` |
| `ntp_interval` | 无 | 分钟，覆盖 `-ntp_interval` |
| `disconnect_timeout` | 无 | 保留项，无需设置 |
| `pprof_addr` | 无 | PProf 调试地址 `ip:port` |

### 连接的高级设置

下面的键没写时，使用同名命令行参数的值，含义见 [npc 命令行参数](/reference/npc-cli.md)：`pipes`、`scheduler_up`、`session_max_lanes`、`session_resume_timeout`、`session_usage_interval`、`relay_idle`、`p2p_visitor_keep_server`、`new_protocol_pin`。

### 新建客户端时使用的键

配置文件定义了隧道或域名转发，并且 `vkey` 填的是服务端的公共密钥 `public_vkey` 时，服务端会新建一个客户端，下列键作为它的设置。使用已有客户端的 vkey 时，这些设置以网页上为准。

| 键 | 说明 |
| --- | --- |
| `remark` | 备注 |
| `basic_username`、`basic_password` | Basic 认证账号 |
| `web_username`、`web_password` | 客户端登录网页的账号 |
| `compress` | 是否压缩。带点对点访问段时，它还决定访问流量是否压缩 |
| `crypt` | 是否加密 |
| `rate_limit` | 限速 |
| `flow_limit` | 流量上限 |
| `time_limit` | 到期时间 |
| `max_conn` | 最大连接数 |

### 已弃用的键

| 键 | 处理方式 | 替代写法 |
| --- | --- | --- |
| `tls_enable` | `true` 时等同 `conn_type=tls` 并覆盖 `conn_type`，不一致时报警告 | `conn_type=tls` |
| `new_protocol` | 被忽略，报警告 | 删除 |

## 点对点访问段

名称以 `secret` 开头的段，在本机开一个监听端口访问点对点隧道。同一条连接的多个访问段共用一个会话。

```ini
[secret_ssh]
local_type=tcp
conn_mode=auto
local_port=2001
password=YOUR_ACCESS_KEY
target_addr=192.168.1.10:22
```

| 键 | 默认值 | 说明 |
| --- | --- | --- |
| `local_type` | `tcp_udp` | `tcp`、`udp`、`tcp_udp`、`mixed`、`socks5`、`http`、`transparent` |
| `conn_mode` | `auto` | `auto`、`relay`、`direct` |
| `local_port` | 无 | 本地监听端口 |
| `local_ip` | 全部地址 | 本地监听地址 |
| `password` | 无 | 点对点隧道的访问密钥 |
| `target_addr` | 隧道的默认目标 | 要访问的目标 |
| `local_proxy` | `false` | 由服务端连接目标 |

含义见[启动链接与配置格式](/reference/npc-launch.md)中的本地访问 `-L`。

以下写法已弃用，读取时自动换算并报警告：

| 写法 | 换算结果 | 替代写法 |
| --- | --- | --- |
| `[p2p…]` 段 | `conn_mode=auto`，`local_type` 按 `target_type` | `[secret…]` 段，写明 `local_type` 和 `conn_mode` |
| `local_type=p2p` | `conn_mode=auto`，`local_type` 按 `target_type` | 现行的 `local_type` 和 `conn_mode` |
| `local_type=p2ps` | `local_type=mixed`，`conn_mode=auto` | `local_type=mixed` |
| `local_type=p2pt` | `local_type=transparent`，`conn_mode=auto` | `local_type=transparent` |
| `local_type=secret` | `conn_mode=relay`，`local_type` 按 `target_type` | `conn_mode=relay` |
| `target_type` | `tcp`、`udp` 为同名 `local_type`，其他为 `tcp_udp` | `local_type` |
| `fallback_secret` | `false` 时 `conn_mode=direct`，否则 `auto` | `conn_mode` |
| `[secret…]` 段里没有 `local_type` 也没有 `conn_mode` | `conn_mode=relay` | 写明 `conn_mode` |

同一段里同时写了现行的 `local_type` 或 `conn_mode` 时，以现行写法为准。这些写法放在 `-launch` 的载荷里会直接报错。

## 隧道段（已弃用）

在客户端配置里定义隧道的写法已弃用，请改在网页上创建，见[按用途选隧道](/guide/tunnels/README.md)。配置文件定义隧道时，客户端需要开启“允许客户端通过配置文件连接”，npc 每次连接时上传给服务端。

| 键 | 说明 |
| --- | --- |
| `mode` | `tcp`、`udp`、`tcpTrans`、`mixProxy`、`httpProxy`、`socks5`、`secret`。已弃用的取值：`p2p` 读作 `secret` 并报警告；`file` 见下文 |
| `server_port` | 服务端监听端口，可以写多个 |
| `server_ip` | 服务端监听地址 |
| `target_addr` | 目标地址，多个用逗号分隔 |
| `target_port` | 目标端口，写入目标 |
| `target_ip` | 目标 IP |
| `password` | 点对点隧道的访问密钥 |
| `http_proxy` | `mixProxy` 开启 HTTP 代理 |
| `socks5_proxy` | `mixProxy` 开启 SOCKS5 代理 |
| `dest_acl_mode` | 目标访问控制模式 |
| `dest_acl_rules` | 目标访问控制规则，多条用逗号分隔 |
| `proxy_protocol` | 向目标发送 Proxy Protocol |
| `multi_account` | 账号文件路径，文件里每行 `用户=密码` |
| `max_conn` | 最大连接数 |
| `upload_limit`、`download_limit` | 上传、下载限速 |

`mode=file` 用于提供文件访问，已弃用，用来声明[虚拟后端](/guide/tunnels/virtual-backend.md)的文件共享。不带 `server_port` 时只提供虚拟后端，需要在网页上建隧道指向它；带 `server_port` 时自动转换成虚拟后端和一条指向它的 TCP 隧道，并报警告。它的专用键：

| 键 | 说明 |
| --- | --- |
| `local_path` | 本地目录 |
| `strip_pre` | 去掉的 URL 前缀 |
| `read_only` | 只读 |

## 域名转发段（已弃用）

在客户端配置里定义域名转发的写法已弃用，请改在网页上创建，见[域名转发](/guide/tunnels/domain-forwarding.md)。

| 键 | 说明 |
| --- | --- |
| `host` | 域名，含这个键的段就是域名转发段 |
| `target_addr` | 目标地址，多个用逗号分隔 |
| `target_is_https` | 用 HTTPS 连接目标 |
| `host_change` | 修改发给目标的 Host |
| `scheme` | 接受的协议，默认 `all` |
| `location` | URL 路径 |
| `path_rewrite` | 路径重写 |
| `redirect_url` | 重定向地址 |
| `cert_file`、`key_file` | 证书和私钥，值是文件路径或 PEM 内容 |
| `https_just_proxy` | HTTPS 不解密，直接转发 |
| `auto_ssl` | 自动申请证书 |
| `auto_https` | HTTP 自动跳转 HTTPS |
| `auto_cors` | 自动添加跨域响应头 |
| `compat_mode` | 兼容模式 |
| `proxy_protocol` | 向目标发送 Proxy Protocol |
| `multi_account` | 账号文件路径 |
| `max_conn` | 最大连接数 |
| `upload_limit`、`download_limit` | 上传、下载限速 |
| `header_<名称>` | 修改请求头 |
| `response_<名称>` | 修改响应头 |

## 健康检查（已移除）

含 `health_` 开头键的段已不支持，整段被忽略并报警告。请改在服务端按隧道或域名配置，见[健康检查](/guide/tunnels/health-check.md)。
