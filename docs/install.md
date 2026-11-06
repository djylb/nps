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
nps update
```

### 2.2 NPC
```bash
# Install
wget -qO- https://fastly.jsdelivr.net/gh/djylb/nps@master/install.sh | sudo sh -s npc
/usr/bin/npc install -server=xxx:123,yyy:456 -vkey=xxx,yyy -type=tls -log=off
npc start|stop|restart|uninstall

# Update
npc update
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

### 强制更新

服务端和客户端均支持以下命令，跳过当前版本与最新发布版本的比较，重新下载安装最新发布包：

```bash
nps update-force
npc update-force
```

Windows 使用 `.\nps.exe update-force` 或 `.\npc.exe update-force`，请以管理员身份运行；Linux 系统安装通常需要 root 权限。适用于同版本重新安装，也允许从较新的本地开发版回到最新正式发布版。仍保留原有的包摘要校验、下载程序可运行检查及发布标签与程序版本一致性检查；镜像回落沿用原更新流程。服务重启、网页及 Geo 数据更新行为与 `update` 相同，配置和实例密钥保持不变。

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
.\nps.exe update

# NPC Client
.\npc.exe install -server="xxx:123,yyy:456" -vkey="xxx,yyy" -type="tcp,tls" -log="off"
.\npc.exe start|stop|restart|uninstall

# Update
.\npc.exe update
```

<a id="windows7"></a>

#### Windows 7 系统更新与证书

Windows 7 请先安装 **Service Pack 1（SP1）** 并补齐系统更新，再使用上面的 `old` 包：32 位系统选择 `windows_386_*_old`，64 位系统选择 `windows_amd64_*_old`。`old` 表示使用兼容旧系统的 Go 工具链，不代表无需系统补丁。建议优先使用仍受支持的 Windows 版本。

**1. 程序打不开，提示 `fatal error: bcrypt.dll not found`**

此前 Win7 32 位测试中遇到过这个错误，补齐系统更新后程序可以启动。该提示不一定表示磁盘上真的没有 DLL：旧系统缺少安全 DLL 加载接口支持，也可能导致 Go 运行时加载系统库失败。

- 检查 **KB2533623** 或包含同等功能的后续更新。该更新为 Win7 提供 `LoadLibraryEx` 的 `LOAD_LIBRARY_SEARCH_*` 支持，参见 [Microsoft DLL 加载接口说明](https://learn.microsoft.com/zh-cn/windows/win32/api/libloaderapi/nf-libloaderapi-loadlibraryexa)。已安装替代更新时，无需强行重复安装旧补丁。
- 更新基础系统时，按微软要求安装 **KB4490628（服务堆栈更新）**、**KB4474419（SHA-2 签名支持更新）**，按要求重启，然后继续安装适用更新。这两项用于支持后续更新和签名验证，不等同于根证书包，也不能替代 DLL 加载接口更新。参见 [Microsoft SHA-2 更新要求](https://support.microsoft.com/en-us/servicing/os/windows/2020/09/2019-sha-2-code-signing-support-requirement-for-windows-and-wsus)。
- 离线补丁请从 [Microsoft Update Catalog](https://www.catalog.update.microsoft.com/) 按 KB 编号搜索，选择 Windows 7 SP1 对应的 x86/x64 包。补丁架构按**操作系统**选择，不按 npc.exe 的位数选择；提示“不适用于此计算机”时，检查 SP1、前置更新、系统架构及是否已被后续更新替代。
- 更新后先在命令提示符中运行 `npc.exe`，确认不再出现运行时崩溃，再安装服务。不要从第三方 DLL 下载站复制 `bcrypt.dll` 到程序目录。若提示的是 **`bcryptprimitives.dll not found`**，先确认没有误用普通 Windows 包；它与 `bcrypt.dll` 不是同一个文件。

**2. 程序能启动，但 TLS 连接失败或前台在线、服务离线**

先检查日志：`sc query Npc` 显示 `RUNNING` 只代表服务进程在运行，不代表已成功连接 nps。出现 `x509: certificate signed by unknown authority` 等错误时，检查以下内容：

- 系统日期、时间和时区正确；服务端证书未过期，访问域名在证书覆盖范围内，服务端发送了完整的中间证书链。更新根证书不能修复域名不匹配、证书过期或服务端漏发中间证书的问题。
- 补齐系统更新后，允许 Windows 自动更新受信任根证书；网络或组策略可能阻止此更新。根证书与 SHA-2 补丁是两回事，安装补丁后仍应检查根证书是否已更新。
- npc 服务默认以 **LocalSystem** 运行，和当前登录用户的证书存储区不同。手动导入时，应在 `mmc.exe` 中添加“证书”管理单元，选择“计算机帐户”→“本地计算机”，将可信根 CA 导入“受信任的根证书颁发机构”，中间 CA 导入“中间证书颁发机构”。只导入当前用户存储区，可能出现前台连接成功、服务连接失败。

无法在线更新时，可在一台能够正常访问 Windows Update 的 Windows 计算机上执行：

```bat
certutil -generateSSTFromWU roots.sst
```

将生成的文件传到 Win7，打开 `roots.sst`，按需导出所需的根 CA 证书，再通过上面的“本地计算机”管理单元导入。旧版 `certutil` 不支持该命令或下载失败时，在已更新的 Windows 计算机上生成即可。来源与操作方法见 [Microsoft 根证书更新说明](https://learn.microsoft.com/en-us/windows-server/identity/ad-cs/configure-trusted-roots-disallowed-certificates)。已从可信 CA 官方渠道获取并核对的单张根证书，也可以在**管理员命令提示符**中导入：

```bat
certutil -addstore Root root-ca.cer
```

此命令未使用 `-user`，写入本地计算机存储区；参见 [Microsoft 导入根证书说明](https://learn.microsoft.com/en-us/troubleshoot/windows-server/certificates-and-public-key-infrastructure-pki/valid-root-ca-certificates-untrusted)。不要把服务端叶子证书或不明来源的证书包当作根 CA 导入。NPS/NPC 不内置 curl/Mozilla 根证书包，更新系统信任库后应重启 npc 服务再测试。

**3. 更新后的服务验证**

用相同的服务器、vkey 和连接类型，先停止服务后前台运行，确认日志显示连接成功，再退出前台并启动服务，避免同时启动两个实例干扰判断：

```bat
npc.exe stop
npc.exe -server="nps.example.com:8025" -vkey="你的vkey" -type="tls"
REM 确认连接成功后按 Ctrl+C 退出，再执行以下命令
npc.exe start
sc query Npc
sc qc Npc
```

安装、启停服务请使用管理员命令提示符。通过 `sc qc Npc` 核对程序路径、连接参数和服务运行账户，并结合 npc 日志及 nps 管理页面确认在线；`1056` 表示服务实例已经运行，不代表 TLS 认证成功。

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
nps update

# NPC Client
./npc install
/usr/bin/npc install -server=xxx:123,yyy:456 -vkey=xxx,yyy -type=tcp,tls -log=off
npc start|stop|restart|uninstall

# Update
npc update
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
nps update

# NPC Client
./npc install
/usr/bin/npc install -server=xxx:123,yyy:123 -vkey=xxx,yyy -type=tcp,tls -log=off
npc start|stop|restart|uninstall

# Update
npc update
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
