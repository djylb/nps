# 选择隧道类型

| 目标 | 使用 | 公网端口 |
| --- | --- | --- |
| 用域名访问内网网站 | [域名转发](/guide/tunnels/domain-forwarding.md) | 共用 80/443 |
| SSH、RDP、数据库等 TCP 服务 | [TCP 隧道](/guide/tunnels/tcp.md) | 每条隧道一个 |
| DNS、游戏等 UDP 服务 | [UDP 隧道](/guide/tunnels/udp.md) | 每条隧道一个 |
| 像 VPN 一样访问内网 | [混合代理](/guide/tunnels/mixed-proxy.md) | 一个 |
| 不开公网端口访问 TCP/UDP 服务，可打洞直连 | [点对点访问](/guide/tunnels/secret.md) | 无，访问端需运行 npc |
| 不改应用配置、由防火墙规则导流 | [透明代理](/guide/tunnels/transparent.md) | 视部署 |
| 共享本机目录 | [文件访问](/guide/tunnels/file.md) | 一个，只能用配置文件 |

所有方式都需要先[创建客户端并让 npc 在线](/getting-started/quick-start.md)。点对点访问还需要访问端再运行一个 npc 提供本地端口。
