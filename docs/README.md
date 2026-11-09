# NPS

[![GitHub stars](https://img.shields.io/github/stars/djylb/nps.svg)](https://github.com/djylb/nps)
[![GitHub forks](https://img.shields.io/github/forks/djylb/nps.svg)](https://github.com/djylb/nps)
[![Release](https://github.com/djylb/nps/workflows/Release/badge.svg)](https://github.com/djylb/nps/actions)
[![GitHub All Releases](https://img.shields.io/github/downloads/djylb/nps/total)](https://github.com/djylb/nps/releases)

> 在 [GitHub](https://github.com/djylb/nps) 点击右上角 Star 以支持我在空闲时间继续开发

NPS 是一款**轻量级**、**高性能**、**功能强大**的**内网穿透代理服务器**。一条最小链路由公网的 `nps` 服务端、内网的 `npc` 客户端和一条转发规则组成。

![image](https://cdn.jsdelivr.net/gh/djylb/nps/image/web.png)

## 核心功能

- **域名转发**（支持 HTTP/3）：HTTP/HTTPS 反向代理，相当于 Nginx，用于内网 Web 服务上线、微信公众号和小程序本地调试、Webhook 回调调试。
- **TCP 隧道**：映射任意 TCP 端口，用于 RDP、SSH、远程数据库。
- **UDP 隧道**：映射任意 UDP 端口，用于内网 DNS、游戏联机、音视频串流。
- **HTTP/SOCKS5 代理**：像 VPN 一样访问内网资源；混合代理支持目标黑白名单。
- **点对点访问**：TCP/UDP 映射、HTTP/SOCKS5、透明代理，端口不暴露在公网；经 NPS 中转或打洞直连（直连时流量**不走服务器**），默认打洞失败时继续中转。
- **Web 管理界面**：实时展示隧道状态和流量，支持多用户和细粒度访问控制。
- **多种连接协议**：客户端可通过 TCP、KCP、TLS、QUIC、WS、WSS 连接服务器。

## 从哪里开始

| 你要做什么 | 建议先看 |
| --- | --- |
| 第一次部署和验证 | [10 分钟快速开始](/getting-started/quick-start.md) |
| 选择安装方式 | [安装指南](/getting-started/install.md) |
| 按用途选择转发方式 | [选择隧道类型](/getting-started/tunnel-types.md) |
| 查询服务端配置 | [服务端配置 nps.conf](/reference/server-config.md) |
| 查询客户端参数 | [NPC 命令行参数](/reference/npc-cli.md)、[npc.conf（已弃用）](/reference/npc-config.md) |
| 用脚本管理 | [Web API](/reference/web-api.md)、[Webhook](/guide/server/webhook.md) |
| 升级前确认兼容性 | [版本规则与兼容范围](/getting-started/versioning.md) |
| 遇到问题 | [FAQ](/reference/faq.md) |
