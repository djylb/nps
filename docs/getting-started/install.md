# 安装指南

只想尽快跑通？直接看[快速开始](/getting-started/quick-start.md)。

| 方式 | 适用 |
| --- | --- |
| [Docker](#docker) | 推荐，服务端首选 |
| [安装脚本](#script) | Linux、macOS、FreeBSD |
| [发布包](#release) | 需要手动选版本，或机器不能运行脚本 |
| [Windows](/getting-started/install-windows.md) | Windows 10/11；Windows 7 用 `old` 包 |
| [Android 与 OpenWrt](/getting-started/install-platforms.md) | 手机和路由器 |
| [从源码编译](/getting-started/install-source.md) | 自己构建 |

<a id="docker"></a>

## Docker

镜像发布在 DockerHub（[nps](https://hub.docker.com/r/duan2001/nps)、[npc](https://hub.docker.com/r/duan2001/npc)）和 GHCR（[nps](https://github.com/djylb/nps/pkgs/container/nps)、[npc](https://github.com/djylb/nps/pkgs/container/npc)）。用 GHCR 时把镜像名换成 `ghcr.io/djylb/nps`、`ghcr.io/djylb/npc`。

服务端：

```bash
docker run -d --restart=always --name nps --net=host \
  -v /opt/nps:/conf -v /etc/localtime:/etc/localtime:ro duan2001/nps
```

- `/conf` 保存配置、数据和实例密钥，必须挂载到宿主机。
- 第一次启动时，如果 `/conf/nps.conf` 不存在，容器只复制默认配置就退出，`--restart=always` 让它再次按这份配置启动。
- 镜像自带 Geo 数据（IP 和域名归属库）。镜像里的 Geo 数据版本更新时，会替换 `/conf` 中的 Geo 文件，`nps.conf` 不受影响；`nps update-geo` 下载的更新文件会保留。
- 镜像不能从网页更新，升级请换新镜像。

客户端：在网页“启动命令”页把“运行方式”选成 Docker，复制生成的命令。形如（网页生成的链接通常是 `type=auto`，还带路径和其他参数，以网页显示为准）：

```bash
docker run -d --restart=always --name npc --net=host duan2001/npc \
  -launch="npc://YOUR_VKEY@203.0.113.10:8024?type=tls"
```

隧道端口是在 nps 上开的，npc 容器不需要映射端口。群晖等 NAS 同样推荐 Docker。

<a id="script"></a>

## 安装脚本

不支持 Windows，需要 root 权限。

服务端：

```bash
wget -qO- https://fastly.jsdelivr.net/gh/djylb/nps@master/install.sh | sudo sh -s nps
```

配置文件在 `/etc/nps/conf/nps.conf`。先改好再注册服务并启动：

```bash
sudo nps install
sudo nps start
```

客户端：

```bash
wget -qO- https://fastly.jsdelivr.net/gh/djylb/nps@master/install.sh | sudo sh -s npc
```

然后从网页“启动命令”页复制命令运行（“程序路径”选“在 PATH 中”）。

不传参数时安装最新版的 nps 和 npc。

| 位置 | 环境变量 | 含义 | 默认 |
| --- | --- | --- | --- |
| 第 1 个 | `NPS_INSTALL_MODE` | `nps`、`npc` 或 `all` | `all` |
| 第 2 个 | `NPS_INSTALL_VERSION` | 版本号，如 `v0.35.1` | `latest` |
| 第 3 个 | `NPS_INSTALL_DIR` | 只解压到这个目录，不安装到系统路径 | 无 |

例如安装指定版本的 nps：

```bash
wget -qO- https://fastly.jsdelivr.net/gh/djylb/nps@master/install.sh | sudo sh -s nps v0.35.1
```

下载不顺时可以设置 `NPS_IPV4=1`（只用 IPv4）、`NPS_CONNECT_TIMEOUT=30`（连接超时秒数）。

<a id="release"></a>

## 发布包

下载地址是 `https://github.com/djylb/nps/releases/latest/download/文件名`，文件名按下表选。服务端包名以 `_server.tar.gz` 结尾，客户端以 `_client.tar.gz` 结尾。

| 系统 | 架构 | 文件名前缀 |
| --- | --- | --- |
| Linux | x86_64 / x86 | `linux_amd64`、`linux_386` |
| Linux | ARM64 | `linux_arm64` |
| Linux | ARMv5 / v6 / v7 | `linux_arm_v5`、`linux_arm_v6`、`linux_arm_v7` |
| macOS | Intel / Apple 芯片 | `darwin_amd64`、`darwin_arm64` |
| FreeBSD | x86_64 / x86 / ARM | `freebsd_amd64`、`freebsd_386`、`freebsd_arm` |

其他架构（MIPS、RISC-V、LoongArch 等）见[发布页](https://github.com/djylb/nps/releases/latest)。

例如 Linux x86_64 的服务端：

```bash
wget https://github.com/djylb/nps/releases/latest/download/linux_amd64_server.tar.gz
tar -xzf linux_amd64_server.tar.gz
```

解压得到 `nps`、`conf/nps.conf` 和 `web/`。可以先直接运行试试，配置文件就是 `conf/nps.conf`：

```bash
./nps
```

确认没问题后注册为系统服务：

```bash
sudo ./nps install
sudo nps start
```

`install` 第一次运行时会把 `conf/` 复制到 `/etc/nps/conf/`，之后就改那里的配置，解压目录里的配置不再生效。

客户端解压得到 `npc` 和 `conf/npc.conf`，运行方法见[快速开始](/getting-started/quick-start.md)第 4 步。

<a id="config-dir"></a>

## 配置文件在哪

| 安装方式 | 配置文件 |
| --- | --- |
| Docker | 挂载目录下的 `nps.conf`，如 `/opt/nps/nps.conf` |
| 安装脚本，或执行过 `nps install` | `/etc/nps/conf/nps.conf` |
| Windows，执行过 `nps.exe install` | `C:\Program Files\nps\conf\nps.conf` |
| 直接运行解压目录里的 `./nps` | 程序旁边的 `conf/nps.conf` |

要用其他位置，加 `-conf_path`，可以写目录，也可以直接写配置文件：

```bash
./nps -conf_path=/app/nps
sudo ./nps install -conf_path=/app/nps
```

全部配置项见[服务端配置](/reference/server-config.md)。

## 管理系统服务

注册后用这些命令管理：

```bash
sudo nps start
sudo nps stop
sudo nps restart
sudo nps uninstall
```

查看版本：

```bash
nps -version
npc -version
```

输出里的 `Version` 是发布版本，`Core version` 决定能否互联，范围见[版本规则](/reference/versioning.md)。

启动失败时先停掉服务，再在前台运行看输出。常见原因是端口冲突，见[排错](/guide/server/troubleshooting.md)：

```bash
sudo nps stop
sudo nps -conf_path=/etc/nps
```

## 在一台机器上运行多个 nps

`install` 只注册一个服务（Linux 上叫 `Nps`）。要运行多个实例，给每个实例准备单独的配置目录和端口，手动注册服务。

Linux（systemd），创建 `/etc/systemd/system/nps1.service`：

```ini
[Unit]
Description=NPS Server 1
Wants=network-online.target
After=network-online.target

[Service]
LimitNOFILE=65536
ExecStart=/usr/bin/nps "-conf_path=/app/nps1" "service"
Restart=always
RestartSec=120

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable nps1
sudo systemctl start nps1
```

Windows，以管理员身份运行 PowerShell：

```powershell
cmd /c 'sc create Nps1 binPath= "D:\NPS\nps.exe -conf_path=D:\NPS\ service" DisplayName= "NPS Server 1" start= auto'
sc start Nps1
```

手动注册的服务在更新程序前，要先手动停止。

## 更新

Docker 换新镜像；其他方式用：

```bash
sudo nps update
sudo npc update
```

已经是最新版本但想重装，用 `update-force`。Windows 用 `.\nps.exe update`，请以管理员身份运行。从旧版升级前先看[升级与旧配置迁移](/guide/server/upgrade.md)。
