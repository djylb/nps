# 基本使用

---

## 1. 无配置文件模式（推荐）

[详细命令行参数](/npc_extend?id=_5-其他命令行参数)

📌 **适用于**

- **快速连接 NPS 服务器**
- **所有配置均在 Web 管理端完成**
- **客户端仅需运行一条命令**

📌 **普通连接（TCP 模式）**

```bash
./npc -server=ip:8024 -vkey=YOUR_CLIENT_VKEY -type=tcp
```

📌 **TLS 加密连接（安全模式）**

```bash
./npc -server=ip:8025 -vkey=YOUR_CLIENT_VKEY -type=tls
```

📌 **连接多个服务端**

```bash
./npc install -server=xx:12,yy:34 -vkey=xx,yy -type=tcp,tls
```

> **📌 说明**：
> - **默认端口 `8024` 为非 TLS 端口**，用于普通 TCP 连接
> - **如果 `-type=tls`，必须使用 `8025` 作为 TLS 端口**，否则连接失败

---

## 2. 注册到系统服务（开机启动 & 守护进程）

📌 **适用于**

- **保证 NPC 在服务器重启后自动运行**
- **无需手动启动，后台运行**

### **Linux/macOS**

```bash
# Standard connection (TCP)
sudo ./npc install -server=ip:8024 -vkey=xxx -type=tcp -log=off
# TLS connection (secure mode)
sudo ./npc install -server=ip:8025 -vkey=xxx -type=tls -log=off
# Connect to multiple servers
sudo ./npc install -server=xx:12,yy:34 -vkey=xx,yy -type=tcp,tls -log=off

# Start service
sudo npc start
# Stop service
sudo npc stop
# Uninstall (reinstall after changing parameters)
sudo npc uninstall
```

### **Windows**

```powershell
# Standard connection (TCP)
npc.exe install -server=ip:8024 -vkey=xxx -type=tcp -log=off
# TLS connection (secure mode)
npc.exe install -server=ip:8025 -vkey=xxx -type=tls -log=off
# Connect to multiple servers
npc.exe install -server=xx:12,yy:34 -vkey=xx,yy -type=tcp,tls -log=off

# Start service
npc.exe start
# Stop service
npc.exe stop
# Install with custom args
npc.exe install -server=xx,yy -vkey=xx,yy -type=tcp,tls -config=xxx,yyy -log=off
# Uninstall (reinstall after changing parameters)
npc.exe uninstall
```

📌 **Windows 客户端退出后自动重启**：
请按照以下图示配置 Windows 任务计划：
![image](https://cdn.jsdelivr.net/gh/djylb/nps/docs/windows_client_service_configuration.png)

📌 **日志文件位置**：[可通过参数配置](/npc_extend?id=_5-其他命令行参数)

- **Windows**：当前运行目录下
- **Linux/macOS**：`/var/log/npc.log`

---

## 3. 客户端更新

📌 **首先进入到对应的客户端二进制文件目录**

### **步骤**

**执行更新**（已在运行的 NPC 服务会自动重启，无需先停止）
```bash
sudo npc update  # Linux/macOS
npc.exe update   # Windows
```

已是最新版本时不会替换文件，也不会重启服务。

📌 **如果更新失败**，请 **手动下载** [最新版本](https://github.com/djylb/nps/releases/latest)，然后覆盖原有的 `npc` 文件。

### **远程更新**

管理员可以在 Web 管理界面的客户端列表中，点击在线客户端的更新按钮，让客户端自行更新到最新版本并重启。更新按钮仅在客户端支持远程更新（v0.34.9 及以上）且版本低于服务端时显示。

- 客户端从 GitHub 下载官方发布版本并校验，替换前会先验证新版本能在本机运行，失败时保持原版本不变
- Linux/macOS/OpenWrt 等系统下，无论以服务还是命令行方式运行都支持；Windows 下仅支持通过 `npc install` 安装的系统服务，命令行方式运行时会忽略更新指令
- 启动参数加上 `-disable_remote_update` 可禁止远程更新；官方 Docker 镜像默认已禁止

---

## 4. 配置文件模式（适用于高级用户）

📌 **适用于**

- **不使用 Web 配置**
- **使用 `nps` 的公钥或客户端私钥进行验证**
- **可在 `npc.conf` 文件中完成所有设置**

📌 **启动 NPC**

```bash
./npc -config=/path/to/npc.conf
# Support multiple config files for multi-instance
./npc -config=/path/to/npc1.conf,/path/to/npc2.conf
```

📌 **示例配置文件**：
[📌 示例 `npc.conf`](https://github.com/djylb/nps/tree/master/conf/npc.conf)

#### 全局配置

```ini
[common]
server_addr=127.0.0.1:8024
conn_type=tcp
vkey=123
dns_server=8.8.8.8
auto_reconnection=true
#local_ip=192.168.1.100
#max_conn=1000
#flow_limit=1000
#rate_limit=1000
#basic_username=11
#basic_password=3
#web_username=user
#web_password=1234
#crypt=true
#compress=true
#pprof_addr=0.0.0.0:9999
#disconnect_timeout=60
```

| 项              | 含义                         |
|----------------|----------------------------|
| server_addr    | 服务端ip/域名:port              |
| conn_type      | 与服务端通信模式(tcp或kcp)          |
| vkey           | 服务端配置文件中的密钥(非web)          |
| basic_username | socks5或http(s)密码保护用户名(可忽略；用户名或密码任一非空即开启认证) |
| basic_password | socks5或http(s)密码保护密码(可忽略)  |
| compress       | 是否压缩传输(true或false或忽略)      |
| crypt          | 是否加密传输(true或false或忽略)      |
| rate_limit     | 速度限制，可忽略                   |
| flow_limit     | 流量限制，可忽略                   |
| remark         | 客户端备注，可忽略                  |
| max_conn       | 最大连接数，可忽略                  |
| pprof_addr     | debug pprof ip:port        |

#### 域名代理

```ini
[common]
server_addr=1.1.1.1:8024
vkey=123
[web1]
host=a.proxy.com
target_addr=127.0.0.1:8080,127.0.0.1:8082
host_change=www.proxy.com
header_set_proxy=nps
```

| 项           | 含义                                             |
|-------------|------------------------------------------------|
| web1        | 备注                                             |
| host        | 域名                                             |
| target_addr | 内网目标，负载均衡时多个目标，逗号隔开                            |
| host_change | 请求host修改                                       |
| header_xxx  | 请求header修改或添加，header_proxy表示添加header proxy:nps |

#### tcp隧道模式

```ini
[common]
server_addr=1.1.1.1:8024
vkey=123
[tcp]
mode=tcp
target_addr=127.0.0.1:8080
server_port=9001
```

| 项           | 含义        |
|-------------|-----------|
| mode        | tcp       |
| server_port | 在服务端的代理端口 |
| target_addr | 内网目标      |

#### udp隧道模式

```ini
[common]
server_addr=1.1.1.1:8024
vkey=123
[udp]
mode=udp
target_addr=127.0.0.1:8080
server_port=9002
```

| 项           | 含义        |
|-------------|-----------|
| mode        | udp       |
| server_port | 在服务端的代理端口 |
| target_addr | 内网目标      |

#### http代理模式

```ini
[common]
server_addr=1.1.1.1:8024
vkey=123
[http]
mode=httpProxy
server_port=9003
```

| 项           | 含义        |
|-------------|-----------|
| mode        | httpProxy |
| server_port | 在服务端的代理端口 |

#### socks5代理模式

```ini
[common]
server_addr=1.1.1.1:8024
vkey=123
[socks5]
mode=socks5
server_port=9004
multi_account=multi_account.conf
```

| 项             | 含义                                                                                                                                         |
|---------------|--------------------------------------------------------------------------------------------------------------------------------------------|
| mode          | socks5                                                                                                                                     |
| server_port   | 在服务端的代理端口                                                                                                                                  |
| multi_account | socks5多账号配置文件（可选),配置后使用basic_username和basic_password无法通过认证 <br> multi_account.conf要与可执行文件npc同一目录，或者npc.conf里面写相对路径,conf/multi_account.conf |

#### 私密代理模式

```ini
[common]
server_addr=1.1.1.1:8024
vkey=123
[secret_ssh]
mode=secret
password=ssh2
target_addr=10.1.50.2:22
```

| 项           | 含义     |
|-------------|--------|
| mode        | secret |
| password    | 唯一密钥   |
| target_addr | 内网目标   |

#### p2p代理模式

```ini
[common]
server_addr=1.1.1.1:8024
vkey=123
[p2p_ssh]
mode=p2p
password=ssh2
target_addr=10.1.50.2:22
```

| 项           | 含义   |
|-------------|------|
| mode        | p2p  |
| password    | 唯一密钥 |
| target_addr | 内网目标 |

#### 文件访问模式

利用nps提供一个公网可访问的本地文件服务（WebDAV，浏览器也可直接浏览）。

**文件模式只能在 npc.conf 中配置，并用 `npc -config=npc.conf` 启动**；网页管理界面不能创建文件隧道，命令行（`-server`/`-vkey`）模式也不会启动文件服务。

```ini
[common]
server_addr=1.1.1.1:8024
vkey=123
[file]
mode=file
server_port=9100
local_path=/tmp/
strip_pre=/web/
read_only=false
multi_account=/etc/npc/file_users.conf
```

| 项             | 含义                                                         |
|---------------|------------------------------------------------------------|
| mode          | file                                                       |
| server_port   | 在 nps 服务端开启的端口                                            |
| server_ip     | 可选，nps 服务端监听该端口时绑定的地址（如 0.0.0.0），不是访问地址                   |
| local_path    | npc 本机上要共享的目录                                             |
| strip_pre     | URL 前缀，访问时需带上该前缀                                          |
| read_only     | 为 true 时只读：只允许浏览/下载，拒绝上传、删除、移动等写操作                         |
| multi_account | 可选，账号文件路径，每行 `用户名=密码`；配置后访问需 Basic 认证（`[common]` 的 basic_username/basic_password 也会生效） |

访问地址为 `http://<server-ip>:<server_port>/<strip_pre>`，例如上例访问 `http://1.1.1.1:9100/web/` 即相当于访问 npc 本机的 `/tmp/` 目录。其中：

- `<server-ip>` 是 **nps 服务端** 的地址（公网 IP 或域名），端口 `server_port` 由 nps 服务端监听；
- 它不是 npc 本机的 `127.0.0.1`，也不是 `[file]` 段里的 `server_ip`（那只是服务端监听的绑定地址）。

Windows 下用记事本等编辑 npc.conf 均可：UTF-8（含或不含 BOM）编码、CRLF 换行、段头或键值前后多余空格都能正确解析。

#### 断线重连

```ini
[common]
auto_reconnection=true
```

✅ **如需更多帮助，请查看 [文档](https://github.com/djylb/nps) 或提交 [GitHub Issues](https://github.com/djylb/nps/issues) 反馈问题。**

### 隧道与域名转发限速

隧道和域名转发支持独立配置上行、下行限速，单位 KB/s（1 KB/s = 1024 B/s），0 表示不限速。上行指访问者到目标服务，下行指目标服务到访问者。同一条目的所有连接共用各自方向的额度，并同时受客户端原有的双向合计限速、服务端全局限速约束；客户端 `rate_limit` 的含义不变，旧配置无需修改。

网页新增、编辑页面可设置限速，列表中的限速值可点击清空对应方向。客户端和隧道列表提供可排序的网速、上行网速、下行网速，默认只显示合计网速，可在列选择菜单开启另外两列。

通过 npc.conf 配置时，在对应隧道或域名段添加：

```ini
upload_limit=100
download_limit=500
```

这些限制和速度统计作用于经过 nps 转发的数据；P2P 直连流量不经过服务端，不计入服务端统计或限速。已有数据文件缺少上述字段时按不限速处理。

### 列表批量操作

客户端（管理员）、隧道和域名转发列表支持勾选当前页条目，在“批量操作”中选择启用、停用、清空流量或删除，再点击“执行”。表头勾选框可全选当前页；翻页、搜索、排序或刷新后清空选择，不跨页保留。删除客户端也会删除其隧道和域名，执行前会提示确认。

批量操作沿用逐项操作的权限检查，依次处理，结束后汇总成功和失败项。部分失败不会阻止其余条目处理；超时不自动重试，需刷新确认结果。已运行的隧道不会因批量启用而重启。

### 隧道连接数限制

可在隧道新增、编辑的限制设置中配置最大连接数，或在 npc.conf 的隧道段设置 `max_conn=10`。0（默认）表示不限，与客户端 `[common]` 段的 `max_conn` 总连接数限制同时生效，旧配置无需修改。列表详情中的最大连接数可点击清空。

TCP、混合代理、文件模式按访问连接计数，普通 UDP 按活跃转发会话计数，SOCKS5 UDP 跟随其 TCP 控制连接计数。达到上限时拒绝新连接，断开后释放名额。限额校验本身不主动关闭已有连接；网页编辑仍沿用原有的重启隧道行为。私密隧道的中继连接同样受限，P2P 直连不经过服务端，不受此限制。域名转发条目不在本项配置范围内。

### 全局转发连接数限制

管理员可在“全局参数”设置最大转发连接数，0（默认）表示不限，页面同时显示当前占用数。该设置保存在全局数据文件中，重启后保留；旧文件未配置时不限。它与客户端和隧道连接数上限同时生效，任意一级达到上限都拒绝新转发，已有转发退出后释放名额。降低全局上限不会断开已有转发。

计数沿用客户端连接数的口径：TCP、HTTPS 透传按连接，UDP 按会话，SOCKS5 UDP 跟随控制连接，HTTP/HTTPS 域名转发按并发请求。npc 控制连接、管理网页和 P2P 直连不计入此项。

### 客户端近期连接记录

客户端详情按组显示上线、离线时间，`nps.conf` 的 `client_history_limit` 设置保留组数，默认 5，0 关闭记录，负数或无效值使用默认值；修改后重启生效。同一客户端有多个 npc 连接时，全部断开并被服务端确认离线后才填写离线时间。虚拟客户端不记录。记录随客户端数据按现有持久化周期保存；服务端重启导致缺失的离线时间留空，下次上线新增一组，不补写上一组。
