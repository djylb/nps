# NPS

[![GitHub stars](https://img.shields.io/github/stars/djylb/nps.svg)](https://github.com/djylb/nps)
[![GitHub forks](https://img.shields.io/github/forks/djylb/nps.svg)](https://github.com/djylb/nps)
[![Release](https://github.com/djylb/nps/workflows/Release/badge.svg)](https://github.com/djylb/nps/actions)
[![GitHub All Releases](https://img.shields.io/github/downloads/djylb/nps/total)](https://github.com/djylb/nps/releases)

> 在 [GitHub](https://github.com/djylb/nps) 点击右上角 ⭐ Star 以支持我在空闲时间继续开发

---

NPS 是一款**轻量级**、**高性能**、**功能强大**的**内网穿透代理服务器**。

* **多协议支持**：原生支持 **TCP** 与 **UDP** 流量转发，可承载任意上层协议（SSH、RDP、数据库、游戏联机、内网 DNS、音视频流等）。
* **域名转发**：内置完整的 HTTP/HTTPS 反向代理能力，可通过自定义域名与证书，将公网请求安全透明地转发到内网 Web 服务，适用于线上灰度发布、微信/小程序调试、Webhook 回调等场景。
* **代理模式丰富**：内置 **HTTP 代理**、**Socks5 代理**，实现类似 VPN 的访问体验；还提供**私密代理、P2P 连接**，无需将端口暴露在公网环境下。
* **高效 P2P 直连**：支持 TCP/UDP 端到端映射、透明代理和 Socks5 直连；打洞成功时流量**不走服务器**，打洞失败 TCP 端口 可自动回落到私密代理。
* **Web 管理界面**：可视化控制台实时展示隧道状态、流量统计与访问日志，支持多用户、多隧道与细粒度访问控制。
* **多连接协议**：支持通过 TCP、KCP、TLS、QUIC、WS、WSS 协议连接服务器，适应各种不同环境需求。

---

## 背景

![image](https://cdn.jsdelivr.net/gh/djylb/nps/image/web.png)

---

## ✨ 核心功能

### 🕸️ **域名转发** (支持H3)

通过域名访问内网Web服务器，HTTP/HTTPS 反向代理，相当于 Nginx ，可用于：

* 内网 Web 服务上线部署
* 微信公众号、小程序本地调试
* Webhook 回调调试

### 🔌 **TCP 隧道**

映射任意 TCP 端口到 NPS 服务器，常用于：

* RDP 远程桌面
* 连接内网 SSH
* 远程数据库访问

### 📡 **UDP 隧道**

映射任意 UDP 端口到 NPS 服务器，常用于：

* 访问内网 DNS
* 内网游戏联机
* 音视频串流

### 🌍 **HTTP/Socks5 代理**

通过 HTTP/Socks5 代理访问内网资源，相当于 VPN ，常用于：

* 内部服务器远程访问
* 企业办公系统外网访问
* 远程内网运维调试

混合代理（HTTP/SOCKS5）支持**黑/白名单**限制访问目标地址。

### 🤫 **私密代理**

端到端的 TCP 端口映射，端口不会暴露于公网，适用于安全性较高的场景，所有流量经 NPS 服务器中转。

### 🌐 **P2P 连接**

支持 TCP/UDP 端到端映射、Socks5 隧道、透明代理。

  * **流量不走中转**：直连时不占用服务器带宽。
  * **自动回落**：若 P2P 打洞失败，TCP 端口映射将自动切换到“私密代理”中继模式。
