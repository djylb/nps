# NPS 内网穿透 (全修)

[![GitHub Stars](https://img.shields.io/github/stars/djylb/nps.svg)](https://github.com/djylb/nps)
[![GitHub Forks](https://img.shields.io/github/forks/djylb/nps.svg)](https://github.com/djylb/nps)
[![Release](https://github.com/djylb/nps/workflows/Release/badge.svg)](https://github.com/djylb/nps/actions)
[![GitHub All Releases](https://img.shields.io/github/downloads/djylb/nps/total)](https://github.com/djylb/nps/releases)

> 在 [GitHub](https://github.com/djylb/nps) 点击右上角 ⭐ Star 以支持我在空闲时间继续开发

> 由于 GitHub 限制浏览器语言为中文（Accept-Language=zh-CN) 访问 *.githubusercontent.com ，图标可能无法正常显示。

- [English](https://github.com/djylb/nps/blob/master/README.md)

---

## 简介

NPS 是一款轻量高效的内网穿透代理服务器，支持多种协议（TCP、UDP、HTTP、HTTPS、SOCKS5 等）转发。它提供直观的 Web 管理界面，使得内网资源能安全、便捷地在外网访问，同时满足多种复杂场景的需求。

由于[NPS](https://github.com/ehang-io/nps)停更已久，本仓库整合社区更新二次开发而来。

- **提问前请先查阅：**  [文档](https://d-jy.net/docs/nps/) 与 [Issues](https://github.com/djylb/nps/issues)
- **欢迎参与：**  提交 PR、反馈问题或建议，共同推动项目发展。
- **讨论交流：**  加入 [Telegram 交流群](https://t.me/npsdev) 与其他用户交流经验。
- **Android：**  [djylb/npsclient](https://github.com/djylb/npsclient)
- **OpenWrt：**  [djylb/nps-openwrt](https://github.com/djylb/nps-openwrt)
- **Mirror：**  [djylb/nps-mirror](https://github.com/djylb/nps-mirror)

![NPS Web UI](https://cdn.jsdelivr.net/gh/djylb/nps/image/web.png)

---

## 主要特性

- **多协议支持**  
  TCP/UDP 转发、HTTP/HTTPS 转发、HTTP/SOCKS5 代理、点对点访问、Proxy Protocol支持、HTTP/3支持等，满足各种内网访问场景。

- **跨平台部署**  
  支持 Linux、Windows 等主流平台，可轻松安装为系统服务。

- **Web 管理界面**  
  实时监控流量、连接情况以及客户端状态，操作简单直观。

- **安全与扩展**  
  内置加密传输、流量限制、到期限制、证书管理续签等多重功能，保障数据安全。

- **多连接协议**  
  客户端通过 TLS、QUIC 或 WSS 连接服务器，共用一个端口。

---

## 安装与使用

更多详细配置请参考 [文档](https://d-jy.net/docs/nps/)。

### [Android](https://github.com/djylb/npsclient) | [OpenWrt](https://github.com/djylb/nps-openwrt)

### Docker 部署

***DockerHub***： [NPS](https://hub.docker.com/r/duan2001/nps) [NPC](https://hub.docker.com/r/duan2001/npc)

***GHCR***： [NPS](https://github.com/djylb/nps/pkgs/container/nps) [NPC](https://github.com/djylb/nps/pkgs/container/npc)

> 有真实IP获取需求可配合 [mmproxy](https://github.com/djylb/mmproxy-docker) 使用。例如：SSH

#### NPS 服务端
```bash
docker pull duan2001/nps
docker run -d --restart=always --name nps --net=host -v $(pwd)/conf:/conf -v /etc/localtime:/etc/localtime:ro duan2001/nps
```

> **提示：** 首次启动会在挂载目录生成 `nps.conf`，请修改默认管理密码（`web_password`），然后执行 `docker restart nps`。

#### NPC 客户端
```bash
docker pull duan2001/npc
docker run -d --restart=always --name npc --net=host duan2001/npc -launch="npc://YOUR_VKEY@203.0.113.10:8024?type=tls"
```

> **提示：** 请从网页管理端客户端的“启动命令”页复制命令，避免手动填写错误。`YOUR_VKEY` 是该页显示的唯一验证密钥。

### 服务端安装

#### Linux
```bash
# 安装（配置文件：/etc/nps/conf/nps.conf；程序：/usr/bin/nps）
wget -qO- https://fastly.jsdelivr.net/gh/djylb/nps@master/install.sh | sudo sh -s nps
sudo nps install
sudo nps start|stop|restart|uninstall

# 更新
sudo nps update
sudo nps update-force
```

> **提示：** 首次安装后请先编辑 `/etc/nps/conf/nps.conf`，确认配置无误后再执行 `nps start`。

#### Windows
> Windows 7 用户请使用 old 结尾版本 [64](https://github.com/djylb/nps/releases/latest/download/windows_amd64_server_old.tar.gz) / [32](https://github.com/djylb/nps/releases/latest/download/windows_386_server_old.tar.gz)
> 请先补齐 SP1、系统补丁和根证书，详见 [Windows 7 系统更新与证书说明](docs/getting-started/install-windows.md#windows7)。
```powershell
.\nps.exe install
.\nps.exe start|stop|restart|uninstall

# 更新
.\nps.exe update
.\nps.exe update-force
```

### 客户端安装

#### Linux
```bash
wget -qO- https://fastly.jsdelivr.net/gh/djylb/nps@master/install.sh | sudo sh -s npc
sudo /usr/bin/npc install -launch="npc://YOUR_VKEY@203.0.113.10:8024?type=tls"
sudo npc start|stop|restart|uninstall

# 更新
sudo npc update
sudo npc update-force
```

> **提示：** 在网页管理端客户端的“启动命令”页，把“运行方式”选成“安装为服务”，复制生成的命令。

#### Windows
> Windows 7 用户请使用 old 结尾版本 [64](https://github.com/djylb/nps/releases/latest/download/windows_amd64_client_old.tar.gz) / [32](https://github.com/djylb/nps/releases/latest/download/windows_386_client_old.tar.gz)
> 请先补齐 SP1、系统补丁和根证书，详见 [Windows 7 系统更新与证书说明](docs/getting-started/install-windows.md#windows7)。
```powershell
.\npc.exe install -launch="npc://YOUR_VKEY@203.0.113.10:8024?type=tls"
.\npc.exe start|stop|restart|uninstall

# 更新
.\npc.exe update
.\npc.exe update-force
```

> **提示：** 客户端支持同时连接多个服务器：重复 `-launch`，每个服务器一条。

---
