# 按用途选隧道

隧道指一条“从哪里进来、到哪里去”的转发规则：访问者连到公网服务器上的某个入口，nps 把流量交给内网的 npc，npc 再转给内网的目标。本章每一页讲一种隧道，步骤都在网页上完成。

读之前，请先按[快速开始](/getting-started/quick-start.md)让 npc 在线。下文示例用到的值：

| 名称 | 示例值 |
| --- | --- |
| 公网服务器 | `203.0.113.10`，管理网址 `http://203.0.113.10:8081` |
| 域名 | `www.example.com` |
| 内网机器 | `192.168.1.10` |

## 怎么选

| 我想 | 用这个 | 公网需要开放 |
| --- | --- | --- |
| 用域名访问内网网站 | [域名转发](/guide/tunnels/domain-forwarding.md) | 共用 80、443，不额外占端口 |
| 从外面连内网的 SSH、远程桌面、数据库等 TCP 服务 | [TCP 隧道](/guide/tunnels/tcp.md) | 每条隧道一个端口 |
| 转发 DNS、游戏等 UDP 服务 | [UDP 隧道](/guide/tunnels/udp.md) | 每条隧道一个 UDP 端口 |
| 像 VPN 一样访问内网的任意设备 | [混合代理](/guide/tunnels/mixed-proxy.md) | 一个端口 |
| 不在公网开端口，只让持有密钥的人访问内网服务，必要时直连 | [点对点访问](/guide/tunnels/secret.md) | 不开端口，访问端也要运行 npc |
| 让应用不改任何配置，由防火墙把流量导进来 | [透明代理](/guide/tunnels/transparent.md) | 无，访问端运行 npc |
| 把 npc 本机提供的服务（例如文件共享）交给其他隧道或域名使用 | [虚拟后端](/guide/tunnels/virtual-backend.md) | 视使用它的隧道而定 |

多个后端的隧道可以加[健康检查](/guide/tunnels/health-check.md)，故障的后端不再分配连接。

## 添加隧道的通用步骤

1. 打开管理网址，登录。
2. 在左侧菜单点对应的页面（域名转发、TCP 隧道、UDP 隧道、混合代理、点对点访问），点“新增”。
3. 在“客户端 ID”里选这条隧道属于哪个客户端，也就是哪台内网机器上的 npc。
4. 填写监听端口、目标等，点“新增”保存。页面上的“全部展开”“全部收起”用来显示或折叠高级设置。

“服务端端口(留空自动生成)”留空时，nps 自动挑一个空闲端口。每个页面的字段说明见各自页面，所有字段的完整说明见[管理页面说明](/reference/web-ui.md)。

## 开放端口

隧道的监听端口要在服务器的防火墙和云厂商安全组里放行（TCP 隧道放 TCP，UDP 隧道放 UDP）。服务端设置了 `allow_ports` 时，端口还必须在允许范围内，见[服务端配置](/reference/server-config.md)。

## 相关页面

- 名词解释：[基本概念](/getting-started/concepts.md)
- 客户端怎么启动、注册成服务：[客户端](/guide/client/README.md)
- 连不通时：[排错](/guide/server/troubleshooting.md)
