# 点对点访问

**适用范围：** 不占用公网端口、防止他人连接的服务，例如 SSH、远程桌面、大流量传输、游戏联机（TCP 和 UDP）。提供端 npc 上建一条点对点访问隧道，访问端 npc 用隧道密钥在本机开一个端口；流量经 nps 中转，或打洞成功后直连，由访问端的 `conn_mode` 决定。

**场景：** 从本机的 `2000` 端口访问内网 `10.2.50.2:22`。

1. 要直连时，在 `nps.conf` 设置 `p2p_port`，防火墙开放 UDP `p2p_port`..`p2p_port+2`（设为 `6000` 时开放 `6000-6002`）。必要时设置 `p2p_ip`，留空自动识别 IPv4 和 IPv6，可填 IP 或 DDNS 域名。服务端在路由器后时，把这 3 个 UDP 端口一并映射。只用中转时不需要。
2. 在客户端中添加点对点访问隧道，设置唯一密钥 `sshkey` 和内网目标 `10.2.50.2:22`。
3. 在本机运行（具体命令可从 Web 复制）：

```bash
./npc -launch="npc://123@1.1.1.1:8024?type=tcp" -access=sshkey -local_type=tcp_udp -local_port=2000 -conn_mode=auto
```

`-local_port` 指定本地端口，默认 `2000`。然后：

```bash
ssh -p 2000 root@127.0.0.1
```

**不要公开访问命令**，这会把提供端 npc 所在内网暴露给对方。访问方不需要客户端自己的密钥：配置 `visitor_vkey` 后，网页上的访问命令使用这个只能访问隧道的密钥。

## 连接方式

`-conn_mode`（npc.conf 为 `conn_mode`）：

| 取值 | 行为 |
| --- | --- |
| `auto`（默认） | 先经 nps 中转，打洞成功后转为直连；打洞失败时继续中转，并在后台以低速率重试直连 |
| `relay` | 始终经 nps 中转，不打洞 |
| `direct` | 只直连，打洞失败时连接失败 |

隧道的限速只对中转流量生效，直连流量不经过服务端。

## 本地监听

`-local_type`（npc.conf 为 `local_type`）：

| 取值 | 本地提供 |
| --- | --- |
| `tcp_udp`（默认） | TCP 和 UDP 端口映射 |
| `tcp` | 只有 TCP 端口映射 |
| `udp` | 只有 UDP 端口映射 |
| `mixed` | HTTP/SOCKS5 代理 |
| `transparent` | [透明代理](/guide/tunnels/transparent.md) |

配置文件写法见 [npc.conf](/reference/npc-config.md)。

## 访问范围

隧道的目标访问控制（`dest_acl_mode`、`dest_acl_rules`）就是访问端的访问范围，中转和直连都按它检查：

- 关闭（默认）：访问端只能连到隧道的目标。访问端用 `-target` 指定隧道目标之外的地址时，连接被拒绝。
- 白名单或黑名单：访问端可以自己指定目标和网络（`-target=ip:port`，以及 `mixed`、`transparent` 访问端连接的地址），只能连到名单允许的目标；网络受隧道 `target_type` 限制。不指定目标时连到隧道的目标。

被拒绝的连接不会改连到隧道目标，日志记 `tunnel_denied`（`reason=dest_acl`）：中转时在 nps，直连时在提供端 npc。

`allow_secret_local=true`（同时需要 `allow_local_proxy=true`）允许指定目标的访问端用 `-local_proxy=true` 由服务端自己连接目标，只走中转。

访问端每个隧道密钥只向 nps 证明一次（挑战应答，线路上不传可重放的密钥摘要），得到的授权供之后的中转连接和直连共用。隧道停用、修改或删除，服务端相关配置重载，访问端或提供端下线时，授权失效，经它中转的连接和直连随之关闭，访问端下次连接时重新授权。

## NAT 类型与排查

NAT 类型由 nps 的观测端在每次打洞时测量，访问端与提供端都会在日志中输出本端结果，例如：

```
P2P local port 2000: punched public path ...; local NAT udp4 mapping=eim filtering=pdf ports=preserving
```

打洞失败时日志给出原因与下一次尝试的时间。两端都是随机端口分配的对称型 NAT 时通常无法直连，`auto` 下继续中转。

直连仅使用 QUIC，支持 IPv4/IPv6 双栈。全局名单不限制 `p2p_port`。相关配置：`p2p_stun_servers`、`p2p_probe_timeout_ms`、`p2p_handshake_timeout_ms`、`p2p_transport_timeout_ms`，以及默认关闭的端口映射 `p2p_enable_upnp_portmap`、`p2p_enable_pcp_portmap`、`p2p_enable_natpmp_portmap`、`p2p_portmap_lease_seconds`。见[服务端配置](/reference/server-config.md)。

## 旧版写法

0.34 及以前的“私密代理”（secret）和“P2P”合并为点对点访问。旧配置照常读取，自动换算并在日志中告警，给出新写法：

| 旧写法 | 新写法 |
| --- | --- |
| 隧道 `mode=p2p`（npc.conf 任务、nps 存储、面板导入） | `mode=secret`，密钥不变 |
| `[p2p...]` 节 | `[secret...]` 节 |
| `local_type=p2p` 或不写（`[p2p...]` 节、命令行） | `local_type` 由 `target_type` 定，`conn_mode=auto` |
| `local_type=secret`，或 `[secret...]` 节里的 `target_type` | `conn_mode=relay`，`local_type` 由 `target_type` 定 |
| `target_type=all` / `tcp` / `udp` | `local_type=tcp_udp` / `tcp` / `udp` |
| `local_type=p2ps` | `local_type=mixed` |
| `local_type=p2pt` | `local_type=transparent` |
| `fallback_secret=false` | `conn_mode=direct` |
| `-disable_p2p` | `-conn_mode=relay` |

`nps.conf` 中的 `allow_secret_link` 已弃用：设为 `true` 时，没有目标访问控制的点对点访问隧道允许访问端指定任意目标，启动时告警。请给这些隧道设置目标访问控制名单后删除该键。

0.35 起点对点访问的协议不兼容旧版 npc：访问端和提供端都需要 0.35 或更新的 npc。

显式写出的新键优先于换算结果。既没有 `local_type` 也没有 `conn_mode` 的 `[secret...]` 节是旧写法，保持原来的中转（`relay`）并告警；写上 `conn_mode=auto` 即可尝试直连。新写法只要给出 `local_type` 或 `conn_mode`，默认就是 `auto`。`-launch` 只接受新写法。Web 和 API 不再接受 `mode=p2p`。
