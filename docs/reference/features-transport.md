# 传输与连接

## 连接方式与混合端口

npc 与 nps 之间的连接叫“载体”。服务端只需要一个统一监听端口 `mix_port`（初始配置是 `8024`，`0` 关闭）：

- TCP 端口接 `tls`、`wss`、`https` 载体和域名转发；
- 同一个端口号的 UDP 接 `quic`；
- `tcp`、`ws`、`http` 这类明文载体默认不接，打开 `mix_plain` 才接受，用于前面已经有自己的 TLS 反向代理的情况。

| npc 的 `type` | 走什么 | 需要服务端 |
| --- | --- | --- |
| `tls` | TCP + TLS | `mix_port` |
| `quic` | UDP + QUIC | `mix_port`（同端口号的 UDP 要放行） |
| `wss`、`https` | WebSocket / HTTP 载体加 TLS，可经 CDN、反向代理；路径默认 `/ws` | `mix_port` |
| `tcp`、`ws`、`http` | 明文 | `mix_port` 且 `mix_plain=true` |
| `auto` | npc 依次尝试 `tls`、`quic`、`wss`、`https`，不写 `type` 就是它 | `mix_port` |

怎么选、怎么写启动命令见[连接方式](/guide/client/connect.md)。链接和 `-server` 的完整格式见[启动链接与配置格式](/reference/npc-launch.md)。配置键的取值和默认值见[服务端配置](/reference/server-config.md)。

`mix_legacy`（默认开启）决定混合端口是否也接受 0.34 及更早的 npc；没有这类老客户端后可以关闭。

### 已弃用：KCP

KCP 已弃用。0.34 及更早的 npc 的 `-type kcp` 仍能连到服务端的 KCP 端口；新版 npc 写 `kcp` 时会提示并改用 `quic` 连接。新部署请用 `quic`。

### 已弃用：各协议单独端口

下面这些配置是 0.34 及更早版本的写法，现有配置照常读取，新配置不要再用，统一改用 `mix_port`。各自的默认值和相互关系见[服务端配置](/reference/server-config.md)。

| 配置 | 原来的作用 |
| --- | --- |
| `bridge_port` | TCP、KCP 端口的默认值 |
| `bridge_tcp_port`、`bridge_kcp_port`、`bridge_tls_port`、`bridge_quic_port`、`bridge_ws_port`、`bridge_wss_port` | 每种载体单独的端口 |
| `bridge_tcp_ip` 等各协议的监听地址 | 改用 `mix_ip` |
| `bridge_type`、`tcp_enable`、`kcp_enable`、`quic_enable`、`tls_enable`、`ws_enable`、`wss_enable` | 选择开哪些协议 |

怎么把旧配置换成新写法见[升级与旧配置迁移](/guide/server/upgrade.md)。`bridge_path`（WebSocket 路径，默认 `/ws`）不在弃用之列，`wss`、`https` 载体默认使用这个路径。

## 数据压缩

客户端与服务端之间的数据可以压缩，节省流量、加快传输。算法有 `snappy`、`zstd`、`flate`，也可以不压缩。

- 在网页“客户端”的“压缩”里设置整个客户端的默认算法；
- 隧道和域名转发可以单独指定“压缩算法”，默认“跟随客户端”，也可以选“不压缩”；
- 点对点直连的流量不经过服务端，不受这些设置影响。

## 加密

“客户端”的“加密”决定客户端与服务端之间的数据是否加密。底层载体已经是 `tls`、`quic`、`wss` 时不需要再加密一遍，可以关闭。服务端证书留空时自动生成（`bridge_cert_file`、`bridge_key_file` 可指定自己的证书）。

## 嵌套转发

隧道或域名转发的“目标”可以填特殊地址，把流量交给 nps 自己的其他部分：

| 目标写法 | 含义 | 条件 |
| --- | --- | --- |
| `tunnel://隧道ID`（如 `tunnel://11`） | 转发到另一条隧道 | 开启“代理到服务器本地”；目标隧道要处于运行状态，且自己的目标不能再是 `tunnel://` |
| `bridge://类型` | 转发到 nps 自己的客户端连接入口，类型有 `tcp`、`tls`、`ws`、`wss`、`mix`、`web`、`ipreg` | 管理员创建，开启“代理到服务器本地” |
| `service://标识` | 转发到客户端上报的虚拟后端，标识从“虚拟后端”页复制 | **不要**开启“代理到服务器本地”；发起的客户端必须与上报该虚拟后端的客户端相同 |

`bridge://mix` 接受全部 TCP 类载体，包括明文，不受 `mix_plain` 影响。`bridge://web` 是网页管理界面，`bridge://ipreg` 是 IP 登记页面（见[访问控制](/guide/server/access-control.md)），例如在域名转发里把目标设为 `bridge://ipreg`，就能用自己的域名提供登记页面。经 `bridge://` 进来的 WebSocket 或 HTTP 载体，路径由域名转发的 URL 路由决定。

这就是在一个端口上同时提供客户端连接、网站和管理界面的办法：用域名转发区分域名，客户端连接走 `mix_port`，管理界面通过 `bridge://web` 挂在某个域名下。

虚拟后端的用法见[虚拟后端](/guide/tunnels/virtual-backend.md)。

> 0.34 及更早版本靠 `bridge_port` 与 `web_port`、`http_proxy_port`、`https_proxy_port` 设成同一个端口来复用，这种写法已弃用。

## 断线判定与心跳

- `disconnect_timeout`（单位秒，默认 `30`）：客户端断开后等待多久才判定离线。写在 `nps.conf`。
- `mux_ping_interval`（单位秒，默认 `5`）：多路复用的心跳间隔，写在 `nps.conf`。
- 客户端断开后是否自动重连由 npc 的 `-auto_reconnect` 决定，默认开启。

其他连接相关配置（QUIC 的 `quic_alpn`、`quic_keep_alive_period`、`quic_max_idle_timeout` 等）见[服务端配置](/reference/server-config.md)。
