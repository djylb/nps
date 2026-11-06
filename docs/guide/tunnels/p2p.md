# P2P

**适用范围：** 大流量传输、游戏联机等（支持 TCP 和 UDP）。流量不经过公网服务器；TCP 映射在打洞失败时自动回落到私密代理。

P2P 和 NAT 类型关系较大，不保证 100% 成功。访问端和提供端的 npc 均需 0.34.8 及以上版本，其中低于 0.35 的 npc 不作保证，建议两端都升级。

**场景：** 从本机的 `2000` 端口访问内网 `10.2.50.2:22`。

1. 在 `nps.conf` 设置 `p2p_port`，防火墙开放 UDP `p2p_port`..`p2p_port+2`（设为 `6000` 时开放 `6000-6002`）。必要时设置 `p2p_ip`，留空自动识别 IPv4 和 IPv6，可填 IP 或 DDNS 域名。服务端在路由器后时，把这 3 个 UDP 端口一并映射。
2. 在客户端中添加 P2P 隧道，设置唯一密钥 `p2pssh`。
3. 在本机运行（具体命令可从 Web 复制）：

```bash
./npc -server=1.1.1.1:8024 -vkey=123 -password=p2pssh -target=10.2.50.2:22
```

`-local_port` 指定本地端口，默认 `2000`。然后：

```bash
ssh -p 2000 root@127.0.0.1
```

**不要公开 P2P 连接命令**，这会把 npc 所在内网暴露给对方。访问方不需要客户端自己的密钥：配置 `visitor_vkey` 后，网页上的访问命令使用这个只能访问隧道的密钥。

## 访问模式

| `-local_type` | 本地提供 |
| --- | --- |
| `p2p`（默认） | 端口映射，`-target_type` 选择 `all`、`tcp`、`udp` |
| `p2ps` | HTTP/SOCKS5 代理 |
| `p2pt` | [透明代理](/guide/tunnels/transparent.md) |
| `secret` | 私密代理，不打洞 |

配置文件写法见 [npc.conf](/reference/npc-config.md)。

## NAT 类型与排查

NAT 类型由 nps 的观测端在每次打洞时测量，访问端与提供端都会在日志中输出本端结果，例如：

```
P2P local port 2000: punched public path ...; local NAT udp4 mapping=eim filtering=pdf ports=preserving
```

打洞失败时日志给出原因与下一次尝试的时间。两端都是随机端口分配的对称型 NAT 时通常无法直连。开启 `fallback_secret`（默认开启）后自动走私密代理中转，并在后台以低速率继续尝试直连。

直连仅使用 QUIC，支持 IPv4/IPv6 双栈。全局名单不限制 `p2p_port`。相关配置：`p2p_stun_servers`、`p2p_probe_timeout_ms`、`p2p_handshake_timeout_ms`、`p2p_transport_timeout_ms`，以及默认关闭的端口映射 `p2p_enable_upnp_portmap`、`p2p_enable_pcp_portmap`、`p2p_enable_natpmp_portmap`、`p2p_portmap_lease_seconds`。见[服务端配置](/reference/server-config.md)。
