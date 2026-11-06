# 运维：面板、重载与更新

## Web 管理界面

默认地址 `http://公网IP:8081`，可管理客户端、隧道、域名转发、流量统计、全局参数和登录封禁。

列表支持：

- **批量操作**：勾选当前页条目，选择启用、停用、清空流量或删除后点击“执行”。翻页、搜索、排序或刷新后清空选择。依次处理，部分失败不阻止其余条目，结束后汇总。删除客户端也会删除其隧道和域名。
- **实时刷新**：默认每 3 秒同步当前页可见列和已展开详情的动态信息，不重载整表；可在标题右侧暂停。后台标签页不请求。新增或删除条目需手动刷新。
- **踢下线**：客户端列表中断开在线客户端的连接，配置不变。开启自动重连的 npc 几秒后会重新连上；要阻止连接请使用停用。
- **近期连接记录**：客户端详情显示最近几组上线、离线时间，组数由 `client_history_limit` 设置。

负数 ID 表示服务端本地代理的虚拟客户端，管理员可以查看、编辑、启停、设置限制和清空流量，也可以把隧道和域名关联到它。它不参与 npc 认证，不支持远程更新、删除或批量勾选。

## 重载配置

向运行中的 nps 发送 `SIGUSR1`：

```bash
sudo systemctl kill -s USR1 Nps  # systemd 服务，服务名以实际安装为准
sudo kill -USR1 <pid>            # 其他方式，<pid> 为 nps 进程号
```

Windows 没有重载信号，修改配置后需要重启服务。`nps reload` 命令已移除。

重载时应用日志级别、NTP、系统信息显示、Webhook 和 Web 管理界面设置；账号、登录限制、`auth_key` 等运行时读取的配置同时更新。以下变更需要重启才生效，重载时日志会提示：

- Web、HTTP/HTTPS/HTTP3、客户端连接、P2P、pprof 的监听设置
- QUIC 传输参数、日志输出方式（级别除外）
- `timezone`、`dns_server`、`secure_mode`、`allow_ports`、`public_vkey`、`visitor_vkey`
- `bridge_select_mode`、客户端连接证书
- `geoip_path`、`geosite_path`、`acl_max_rules`

## 停止和重启

```bash
sudo nps stop
sudo nps restart
```

```powershell
nps.exe stop
nps.exe restart
```

## 更新

```bash
sudo nps update # Linux/macOS
nps.exe update  # Windows
```

已在运行的服务会自动重启，无需先停止；已是最新版本时不替换文件也不重启。发布包中的 Geo 数据比本地新时一并更新。`update-force` 见[强制更新](/getting-started/install-release.md)。能否直接升级由发布清单决定，规则的唯一来源是仓库中的 `release/update-rules.json`，当前规则不允许通过更新跨大版本（如 0.x.x 到 1.x.x），需手动升级。

替换前会验证新版本可运行，并更新系统服务实际运行的程序。

**单独更新 Geo 数据**，运行中的 nps 一分钟内生效：

```bash
sudo nps update-geo
```

**在网页上更新**：管理员在仪表盘“服务端版本”卡片点击更新按钮（仅在有新版本时显示），服务端更新后自动重启，页面随后刷新。

- 重启期间连接短暂中断，客户端会自动重连。
- Linux/macOS 等系统无论以服务还是命令行运行都支持；Windows 只支持 `nps install` 安装的系统服务。
- `-disable_remote_update` 禁止网页更新；官方 Docker 镜像默认已禁止。

## 手动替换程序

`nps update` 失败时，下载[最新版本](https://github.com/djylb/nps/releases/latest)，覆盖原有 `nps` 和 `web` 目录。程序位置可用 `whereis nps` 查找。

```bash
sudo systemctl stop Nps
sudo cp nps /usr/bin/nps
sudo chmod +x /usr/bin/nps
sudo systemctl start Nps
```

```powershell
Stop-Service Nps
Copy-Item -Path "PATH_TO_NEW_NPS_EXE" -Destination "PATH_TO_OLD_NPS_EXE_DIR" -Force
Start-Service Nps
```

权限不足时请以管理员身份运行。

## 实例主密钥

启动 Web 管理时，会在实际加载的配置文件目录中自动生成 `instance.key`，可用 `instance_key_file` 指定其他路径（相对路径以配置文件目录为基准）。Cookie 的签名和加密密钥由它派生，不依赖管理员账号、密码、TOTP 或 API 密钥。

- 后续重启或修改登录配置不会更换它。请备份，Docker 需持久挂载；多个副本只有共享同一密钥才能互认 Cookie。
- Unix 新建文件权限为 `0600`。自定义目录需预先存在且可写，并支持同目录原子硬链接，否则启动报错。
- 已有密钥无法读取、格式或校验和错误会导致启动失败，不会自动覆盖。删除后下次启动生成新密钥，原有 Cookie 全部失效。
- 它属于敏感凭据，不要公开或提交到版本库。

## 其他常用设置

```ini
# 流量持久化周期（分钟）；不写不持久化。不记录公钥连接的客户端
flow_store_interval=10
# 在面板显示系统负载
system_info_display=true
# 留空禁用公钥访问
public_vkey=
# 不写或为 0 则关闭 Web 管理
web_port=0
# 允许客户端用户登录。默认用户名 user，默认密码为各自 vkey，可修改
allow_user_login=true
# 在登录页显示注册按钮
allow_user_register=true
# 允许在隧道上指定监听 IP（npc.conf 的 server_ip）
allow_multi_ip=true
```

**代理到服务器本地**：相当于在 nps 服务器上启动一个 npc，把流量回送到本机。设置 `allow_local_proxy=true`（普通用户还需 `allow_user_local=true`），然后在隧道或域名上选择“转发到本地”。例如本机 Web 服务在 `5000` 端口，添加域名并把目标设为 `127.0.0.1:5000`。
