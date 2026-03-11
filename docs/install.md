# 安装指南

NPS 提供多种安装方式，推荐使用 **Docker 部署**，也支持 **二进制发布包安装** 及 **源码编译**。

---

## 1. Docker 安装（推荐）

提供 Docker 镜像，支持 **DockerHub** 和 **GitHub Container Registry (GHCR)** 。

### **1.1 NPS 服务器端**

#### **DockerHub（推荐）**
```bash
docker pull duan2001/nps
docker run -d --restart=always --name nps --net=host -v <local_conf_dir>:/conf -v /etc/localtime:/etc/localtime:ro duan2001/nps
```

#### **GHCR（可选）**
```bash
docker pull ghcr.io/djylb/nps
docker run -d --restart=always --name nps --net=host -v <local_conf_dir>:/conf -v /etc/localtime:/etc/localtime:ro ghcr.io/djylb/nps
```

---

### **1.2 NPC 客户端**

#### **DockerHub（推荐）**
```bash
docker pull duan2001/npc
docker run -d --restart=always --name npc --net=host duan2001/npc -server=xxx:123,yyy:456 -vkey=xxx,yyy -type=tls,tcp -log=off
```

#### **GHCR（可选）**
```bash
docker pull ghcr.io/djylb/npc
docker run -d --restart=always --name npc --net=host ghcr.io/djylb/npc -server=xxx:123,yyy:456 -vkey=xxx,yyy -type=tls,tcp -log=off
```

---

## 2. 脚本安装

> 此方式不支持 **Windows** 安装。

### 2.1 NPS
```bash
# Install (default configuration path: /etc/nps/; binary file path: /usr/bin/)
wget -qO- https://fastly.jsdelivr.net/gh/djylb/nps@master/install.sh | sudo sh -s nps
nps install
nps start|stop|restart|uninstall

# Update
nps update && nps restart
```

### 2.2 NPC
```bash
# Install
wget -qO- https://fastly.jsdelivr.net/gh/djylb/nps@master/install.sh | sudo sh -s npc
/usr/bin/npc install -server=xxx:123,yyy:456 -vkey=xxx,yyy -type=tls -log=off
npc start|stop|restart|uninstall

# Update
npc update && npc restart
```

### 2.3 脚本说明

* 不传任何参数时，脚本默认安装最新版本的 `nps` 和 `npc`，二进制文件会安装到系统路径（`/usr/bin` 或 `/usr/local/bin`），配置文件位于 `/etc/nps`。

* 脚本支持通过参数指定：

  * **模式**：`nps` | `npc` | `all`（默认 `all`）
  * **版本**：例如 `v0.29.0`，默认 `latest`
  * **安装目录**：指定路径时，压缩包将直接解压到该目录，而不会安装到系统路径。

* 同样支持以下环境变量：

  * `NPS_INSTALL_MODE`：等同于第一个参数
  * `NPS_INSTALL_VERSION`：等同于第二个参数
  * `NPS_INSTALL_DIR`：等同于第三个参数

---

## 3. 发布包安装

NPS 提供官方二进制安装包，适用于 **Windows、Linux、macOS、FreeBSD** 等多种平台。

📌 **下载地址**：[🔗 最新发布页面](https://github.com/djylb/nps/releases/latest)

---

### **3.1 Windows 安装**

**Windows 10/11 用户（推荐）**：
- [64 位（Server）](https://github.com/djylb/nps/releases/latest/download/windows_amd64_server.tar.gz)
- [64 位（Client）](https://github.com/djylb/nps/releases/latest/download/windows_amd64_client.tar.gz)
- [32 位（Server）](https://github.com/djylb/nps/releases/latest/download/windows_386_server.tar.gz)
- [32 位（Client）](https://github.com/djylb/nps/releases/latest/download/windows_386_client.tar.gz)
- [ARM64（Server）](https://github.com/djylb/nps/releases/latest/download/windows_arm64_server.tar.gz)
- [ARM64（Client）](https://github.com/djylb/nps/releases/latest/download/windows_arm64_client.tar.gz)

**Windows 7 用户（使用 `old` 结尾版本）**：
- [64 位（Server）](https://github.com/djylb/nps/releases/latest/download/windows_amd64_server_old.tar.gz)
- [64 位（Client）](https://github.com/djylb/nps/releases/latest/download/windows_amd64_client_old.tar.gz)
- [32 位（Server）](https://github.com/djylb/nps/releases/latest/download/windows_386_server_old.tar.gz)
- [32 位（Client）](https://github.com/djylb/nps/releases/latest/download/windows_386_client_old.tar.gz)

📌 **安装方式（解压后进入文件夹）**
```powershell
# NPS Server
.\nps.exe install
.\nps.exe start|stop|restart|uninstall

# Support custom config path
.\nps.exe -conf_path="D:\test\nps"
.\nps.exe install -conf_path="D:\test\nps"

# Update
.\nps.exe stop
.\nps-update.exe update
.\nps.exe start

# NPC Client
.\npc.exe install -server="xxx:123,yyy:456" -vkey="xxx,yyy" -type="tcp,tls" -log="off"
.\npc.exe start|stop|restart|uninstall

# Update
.\npc.exe stop
.\npc-update.exe update
.\npc.exe start
```

---

### **3.2 Linux 安装**
📌 **推荐使用 Docker 运行。**

#### **X86/64**
- [64 位（Server）](https://github.com/djylb/nps/releases/latest/download/linux_amd64_server.tar.gz)
- [64 位（Client）](https://github.com/djylb/nps/releases/latest/download/linux_amd64_client.tar.gz)
- [32 位（Server）](https://github.com/djylb/nps/releases/latest/download/linux_386_server.tar.gz)
- [32 位（Client）](https://github.com/djylb/nps/releases/latest/download/linux_386_client.tar.gz)

#### **ARM**
- [ARM64（Server）](https://github.com/djylb/nps/releases/latest/download/linux_arm64_server.tar.gz)
- [ARM64（Client）](https://github.com/djylb/nps/releases/latest/download/linux_arm64_client.tar.gz)
- [ARMv5（Server）](https://github.com/djylb/nps/releases/latest/download/linux_arm_v5_server.tar.gz)
- [ARMv5（Client）](https://github.com/djylb/nps/releases/latest/download/linux_arm_v5_client.tar.gz)
- [ARMv6（Server）](https://github.com/djylb/nps/releases/latest/download/linux_arm_v6_server.tar.gz)
- [ARMv6（Client）](https://github.com/djylb/nps/releases/latest/download/linux_arm_v6_client.tar.gz)
- [ARMv7（Server）](https://github.com/djylb/nps/releases/latest/download/linux_arm_v7_server.tar.gz)
- [ARMv7（Client）](https://github.com/djylb/nps/releases/latest/download/linux_arm_v7_client.tar.gz)

📌 **安装方式（解压后进入文件夹）**
```bash
# NPS Server
./nps install
nps start|stop|restart|uninstall

# Support custom config path
./nps -conf_path="/app/nps"
./nps install -conf_path="/app/nps"

# Update
nps update && nps restart

# NPC Client
./npc install
/usr/bin/npc install -server=xxx:123,yyy:456 -vkey=xxx,yyy -type=tcp,tls -log=off
npc start|stop|restart|uninstall

# Update
npc update && npc restart
```

---

### **3.3 macOS 安装**
- [Intel（Server）](https://github.com/djylb/nps/releases/latest/download/darwin_amd64_server.tar.gz)
- [Intel（Client）](https://github.com/djylb/nps/releases/latest/download/darwin_amd64_client.tar.gz)
- [Apple Silicon（Server）](https://github.com/djylb/nps/releases/latest/download/darwin_arm64_server.tar.gz)
- [Apple Silicon（Client）](https://github.com/djylb/nps/releases/latest/download/darwin_arm64_client.tar.gz)

📌 **安装方式（解压后进入文件夹）**
```bash
# NPS Server
./nps install
nps start|stop|restart|uninstall

# Support custom config path
./nps -conf_path="/app/nps"
./nps install -conf_path="/app/nps"

# Update
nps update && nps restart

# NPC Client
./npc install
/usr/bin/npc install -server=xxx:123,yyy:123 -vkey=xxx,yyy -type=tcp,tls -log=off
npc start|stop|restart|uninstall

# Update
npc update && npc restart
```

---

### **3.4 FreeBSD 安装**
- [AMD64（Server）](https://github.com/djylb/nps/releases/latest/download/freebsd_amd64_server.tar.gz)
- [AMD64（Client）](https://github.com/djylb/nps/releases/latest/download/freebsd_amd64_client.tar.gz)
- [386（Server）](https://github.com/djylb/nps/releases/latest/download/freebsd_386_server.tar.gz)
- [386（Client）](https://github.com/djylb/nps/releases/latest/download/freebsd_386_client.tar.gz)
- [ARM（Server）](https://github.com/djylb/nps/releases/latest/download/freebsd_arm_server.tar.gz)
- [ARM（Client）](https://github.com/djylb/nps/releases/latest/download/freebsd_arm_client.tar.gz)

---

## 4. Android 使用

### **4.1 APK (仅限NPC)**
#### [NPS Client](https://github.com/djylb/npsclient)
#### [Google Play](https://play.google.com/store/apps/details?id=com.duanlab.npsclient)
- [全架构](https://github.com/djylb/npsclient/releases/latest/download/app-universal-release.apk)
- [ARM64](https://github.com/djylb/npsclient/releases/latest/download/app-arm64-v8a-release.apk)
- [ARM32](https://github.com/djylb/npsclient/releases/latest/download/app-armeabi-v7a-release.apk)
- [X8664](https://github.com/djylb/npsclient/releases/latest/download/app-x86_64-release.apk)


### **4.2 Termux 运行**
- [ARM64（Server）](https://github.com/djylb/nps/releases/latest/download/android_arm64_server.tar.gz)
- [ARM64（Client）](https://github.com/djylb/nps/releases/latest/download/android_arm64_client.tar.gz)。

---

## 5. OpenWrt 使用

#### [djylb/nps-openwrt](https://github.com/djylb/nps-openwrt)

---

## 6. 源码安装（Go 编译）

### **6.1 安装依赖**
```bash
go get -u github.com/djylb/nps
```

### **6.2 编译**
#### **NPS 服务器**
```bash
go build -o nps cmd/nps/nps.go
```

#### **NPC 客户端**
```bash
go build -o npc cmd/npc/npc.go
```

编译完成后，即可使用 `./nps` 或 `./npc` 启动。

---

## 7. 相关链接

- **最新发布版本**：[GitHub Releases](https://github.com/djylb/nps/releases/latest)
- **Android**：[djylb/npsclient](https://github.com/djylb/npsclient)
- **OpenWrt**：[djylb/nps-openwrt](https://github.com/djylb/nps-openwrt)
- **DockerHub 镜像**
  - [NPS Server](https://hub.docker.com/r/duan2001/nps)
  - [NPC Client](https://hub.docker.com/r/duan2001/npc)
- **GHCR 镜像**
  - [NPS Server](https://github.com/djylb/nps/pkgs/container/nps)
  - [NPC Client](https://github.com/djylb/nps/pkgs/container/npc)
