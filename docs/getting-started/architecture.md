# 架构与核心概念

可在网页上配置和管理 TCP、UDP 隧道、代理隧道、HTTP/HTTPS 转发等，也可以只用客户端配置文件。

![image](https://cdn.jsdelivr.net/gh/djylb/nps/image/web2.png)

```
访问者 ──> nps（公网）──多路复用连接──> npc（内网）──> 内网目标
```

| 概念 | 说明 |
| --- | --- |
| nps | 公网服务端：监听隧道和域名，提供 Web 管理 |
| npc | 内网客户端：主动连接 nps，并访问内网目标 |
| 客户端 | nps 上的一条记录，以 `vkey` 认证；同一 `vkey` 可以运行多个 npc |
| 隧道 | 端口类规则：TCP、UDP、混合代理、点对点访问、文件 |
| 域名转发 | 按域名和路径路由的 HTTP/HTTPS 规则，所有客户端共用端口 |

## 三种密钥

| 密钥 | 能做什么 |
| --- | --- |
| 客户端 `vkey` | 代表该客户端：连接、发布配置、访问隧道。不要公开 |
| `public_vkey` | 客户端以配置文件模式启动时使用；留空即禁用公钥访问 |
| `visitor_vkey` | 只能访问 secret/P2P 隧道，不能注册隧道或拉取配置。设置后网页上的访问命令使用它 |

## 配置的来源

- **Web 管理的条目**保存在 `conf/clients.json`、`tasks.json`、`hosts.json`，重启后保留。
- **配置文件发布的条目**（`npc -config`）由 npc 每次连接时注册；发布它的 npc 全部离线后条目被移除。
- 全局名单和全局限制保存在 `conf/global.json`。数据文件解析失败时保留 `.bad` 备份。

## 客户端连接协议

npc 可通过 TCP、KCP、TLS、QUIC、WS、WSS 连接 nps，见[传输与连接](/reference/features-transport.md)。
