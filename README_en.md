# NPS Intranet Tunneling (Enhanced)

[![GitHub Stars](https://img.shields.io/github/stars/djylb/nps.svg)](https://github.com/djylb/nps)
[![GitHub Forks](https://img.shields.io/github/forks/djylb/nps.svg)](https://github.com/djylb/nps)
[![Release](https://github.com/djylb/nps/workflows/Release/badge.svg)](https://github.com/djylb/nps/actions)
[![GitHub All Releases](https://img.shields.io/github/downloads/djylb/nps/total)](https://github.com/djylb/nps/releases)

> ⭐️ Give us a star on [GitHub](https://github.com/djylb/nps) if you like it!

- [中文文档](https://github.com/djylb/nps/blob/master/README.md)

---

## Introduction

NPS is a lightweight and efficient intranet tunneling proxy server that supports forwarding multiple protocols (TCP, UDP, HTTP, HTTPS, SOCKS5, etc.). It features an intuitive web management interface that allows secure and convenient access to intranet resources from external networks, addressing a wide range of complex scenarios.

Since the original [NPS](https://github.com/ehang-io/nps) project has been inactive for a long time, this repository continues its development as one of the actively maintained community versions, featuring extensive refactoring, improved stability, and enhanced functionality.

- **Before asking questions, please check:** [Documentation](https://d-jy.net/docs/nps/) and [Issues](https://github.com/djylb/nps/issues)
- **Contributions welcome:** Submit PRs, provide feedback or suggestions, and help drive the project forward.
- **Join the discussion:** Connect with other users in our [Telegram Group](https://t.me/npsdev).
- **Android:**  [djylb/npsclient](https://github.com/djylb/npsclient)
- **OpenWrt:**  [djylb/nps-openwrt](https://github.com/djylb/nps-openwrt)
- **Mirror:**  [djylb/nps-mirror](https://github.com/djylb/nps-mirror)

---

## Key Features

- **Multi-Protocol Support**  
  Supports TCP/UDP forwarding, HTTP/HTTPS forwarding, HTTP/SOCKS5 proxy, P2P mode, Proxy Protocol support, HTTP/3 support, and more to accommodate various intranet access scenarios.

- **Cross-Platform Deployment**  
  Compatible with major platforms such as Linux and Windows, and can be easily installed as a system service.

- **Web Management Interface**  
  Provides real-time monitoring of traffic, connection status, and client states with an intuitive and user-friendly interface.

- **Security and Extensibility**  
  Built-in features such as encrypted transmission, traffic limiting, expiration restrictions, certificate management and renewal ensure data security.

- **Multiple Connection Protocols**
  Supports connecting to the server using TCP, KCP, TLS, QUIC, WS, and WSS protocols.

---

## Installation and Usage

For more detailed configuration options, please refer to the [Documentation](https://d-jy.net/docs/nps/) (some sections may be outdated).

### [Android](https://github.com/djylb/npsclient) | [OpenWrt](https://github.com/djylb/nps-openwrt)

### Docker Deployment

**DockerHub:**  [NPS](https://hub.docker.com/r/duan2001/nps) | [NPC](https://hub.docker.com/r/duan2001/npc)

**GHCR:**  [NPS](https://github.com/djylb/nps/pkgs/container/nps) | [NPC](https://github.com/djylb/nps/pkgs/container/npc)

> If you need to obtain the real client IP, you can use it together with [mmproxy](https://github.com/djylb/mmproxy-docker). For example: SSH.

#### NPS Server
```bash
docker pull duan2001/nps
docker run -d --restart=always --name nps --net=host -v $(pwd)/conf:/conf -v /etc/localtime:/etc/localtime:ro duan2001/nps
```

#### NPC Client
```bash
docker pull duan2001/npc
docker run -d --restart=always --name npc --net=host duan2001/npc -server=xxx:123,yyy:456 -vkey=key1,key2 -type=tls,tcp -log=off
```

### Server Installation

#### Linux
```bash
# Install (default configuration path: /etc/nps/; binary file path: /usr/bin/)
wget -qO- https://raw.githubusercontent.com/djylb/nps/refs/heads/master/install.sh | sudo sh -s nps
nps install
nps start|stop|restart|uninstall

# Update
nps update && nps restart
```

#### Windows
> Windows 7 users should use the version ending with old: [64](https://github.com/djylb/nps/releases/latest/download/windows_amd64_server_old.tar.gz) / [32](https://github.com/djylb/nps/releases/latest/download/windows_386_server_old.tar.gz)
```powershell
.\nps.exe install
.\nps.exe start|stop|restart|uninstall

# Update
.\nps.exe stop
.\nps-update.exe update
.\nps.exe start
```

### Client Installation

#### Linux
```bash
wget -qO- https://raw.githubusercontent.com/djylb/nps/refs/heads/master/install.sh | sudo sh -s npc
/usr/bin/npc install -server=xxx:123,yyy:456 -vkey=xxx,yyy -type=tls -log=off
npc start|stop|restart|uninstall

# Update
npc update && npc restart
```

#### Windows
> Windows 7 users should use the version ending with old: [64](https://github.com/djylb/nps/releases/latest/download/windows_amd64_client_old.tar.gz) / [32](https://github.com/djylb/nps/releases/latest/download/windows_386_client_old.tar.gz)
```powershell
.\npc.exe install -server="xxx:123,yyy:456" -vkey="xxx,yyy" -type="tls,tcp" -log="off"
.\npc.exe start|stop|restart|uninstall

# Update
.\npc.exe stop
.\npc-update.exe update
.\npc.exe start
```

> **Tip:** The client supports connecting to multiple servers simultaneously. Example:  
> `npc -server=xxx:123,yyy:456,zzz:789 -vkey=key1,key2,key3 -type=tcp,tls`  
> Here, `xxx:123` uses TCP, and `yyy:456` and `zzz:789` use TLS.

> If you need to connect to older server versions, add `-proto_version=0` to the startup command.

