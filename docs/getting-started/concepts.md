# 核心概念

![网页管理界面](https://cdn.jsdelivr.net/gh/djylb/nps/image/web2.png)

```
访问者 ──> nps（公网）<── npc 主动连接 ── npc（内网）──> 内网服务
```

内网机器没有公网 IP，外面连不进来，但它可以主动连出去。所以由内网里的 npc 先连上公网的 nps，并保持这条连接；访问者连 nps 时，流量沿这条连接交给 npc，再由 npc 转给内网服务。

| 名词 | 说明 |
| --- | --- |
| nps | 运行在公网的服务端：监听隧道端口和域名，提供网页管理 |
| npc | 运行在内网的客户端：主动连接 nps，并访问内网服务 |
| 客户端 | nps 上的一条记录，用唯一验证密钥（vkey）识别。同一个 vkey 可以运行多个 npc |
| 隧道 | 按端口转发的规则：TCP 隧道、UDP 隧道、混合代理、点对点访问、虚拟后端 |
| 域名转发 | 按域名和路径转发的 HTTP/HTTPS 规则，所有客户端共用 80/443 端口 |
| 混合端口 | nps 上供 npc 连接的端口，配置项 `mix_port`，默认 `8024` |

## npc 怎么连接 nps

npc 和 nps 之间可以用几种方式传输，叫作“载体”。默认的混合端口在同一个端口号上接受：

| 载体 | 走的协议 | 说明 |
| --- | --- | --- |
| `tls` | TCP | 最常用 |
| `quic` | UDP | 基于 UDP，弱网下表现更好 |
| `wss` | TCP | 外观是 HTTPS 上的 WebSocket，能穿过只放行网页流量的网络和反向代理 |
| `auto` | 自动 | 依次尝试 tls、quic、wss、https，网页生成的启动命令默认用它 |

未加密的 `tcp`、`ws` 在混合端口上默认不接受，只有当混合端口前面有你自己的 TLS 反向代理、并设置了 `mix_plain=true` 时才会用到，见[反向代理与 HTTPS](/guide/server/https-and-proxy.md)。

nps 没有配置证书时会自动生成一张，所以不需要购买证书；双方靠 vkey 确认对方身份。更多连接参数见[连接与系统服务](/guide/client/connect.md)。

## 三种密钥

| 密钥 | 在哪里 | 能做什么 |
| --- | --- | --- |
| 唯一验证密钥（vkey） | 每个客户端一个，在网页“客户端”列表或编辑页 | 代表这个客户端连接 nps，不要公开 |
| `public_vkey` | `nps.conf`，默认留空（禁用） | 配置文件模式的客户端公钥 |
| `visitor_vkey` | `nps.conf`，默认留空 | 只能访问点对点隧道，不能注册隧道或拉取配置。设置后网页上的访问命令会用它 |

## 配置和数据存在哪

| 内容 | 位置 |
| --- | --- |
| 服务端配置 | `conf/nps.conf` |
| 客户端、隧道、域名 | `conf/clients.json`、`conf/tasks.json`、`conf/hosts.json` |
| 全局名单和限制 | `conf/global.json` |
| 实例主密钥 | 配置目录下的 `instance.key`，请备份，不要公开 |

配置目录按安装方式不同而不同，见[安装指南](/getting-started/install.md#config-dir)。备份方法见[备份与恢复](/guide/server/backup.md)。
